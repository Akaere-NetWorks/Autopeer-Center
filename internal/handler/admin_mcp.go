package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/akaere/autopeer-center/internal/middleware"
	"github.com/akaere/autopeer-center/internal/queue"
	"github.com/akaere/autopeer-center/internal/repository"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

var adminMCPLog = logrus.WithField("pkg", "handler.admin_mcp")

type adminMCPSession struct {
	adminID      string
	sessionID    string
	adminKeyID   string
	capabilities []string
	ch           chan []byte
	cancel       context.CancelFunc
	sem          chan struct{}
}

type adminMCPToolDefinition struct {
	Tool       mcpTool
	Capability string
	Sensitive  bool
	Handler    func(context.Context, json.RawMessage, *adminMCPToolDefinition) (interface{}, error)
}

type AdminMCPHandler struct {
	mcp      repository.MCPRepository
	peers    repository.PeerRepository
	nodes    repository.NodeRepository
	metrics  repository.MetricsRepository
	settings repository.SettingsRepository
	stats    repository.StatsRepository
	releases repository.ReleaseRepository
	bot      repository.BotRepository
	sessions sync.Map
	sess     *mcpSessionDB
}

func NewAdminMCPHandler(mcp repository.MCPRepository, peers repository.PeerRepository, nodes repository.NodeRepository, metrics repository.MetricsRepository, settings repository.SettingsRepository, stats repository.StatsRepository, releases repository.ReleaseRepository, bot repository.BotRepository, q *queue.Queue) (*AdminMCPHandler, func()) {
	sessDB, stop := newMCPSessionDB(mcp, q, adminMCPLog)
	return &AdminMCPHandler{mcp: mcp, peers: peers, nodes: nodes, metrics: metrics, settings: settings, stats: stats, releases: releases, bot: bot, sess: sessDB}, stop
}

func (h *AdminMCPHandler) HandleSSE(w http.ResponseWriter, r *http.Request) {
	adminID := middleware.GetAdminID(r.Context())
	adminKeyID := middleware.GetAdminMCPKeyID(r.Context())

	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	sessionID := uuid.New().String()
	ctx, cancel := context.WithCancel(r.Context())
	sess := &adminMCPSession{
		adminID:      adminID,
		sessionID:    sessionID,
		adminKeyID:   adminKeyID,
		capabilities: middleware.GetMCPCapabilities(r.Context()),
		ch:           make(chan []byte, 64),
		cancel:       cancel,
		sem:          make(chan struct{}, 5),
	}
	h.sessions.Store(sessionID, sess)

	h.sess.insertMCPSession(sessionID, 0, "", true, adminKeyID)

	defer func() {
		h.sessions.Delete(sessionID)
		cancel()
		h.sess.closeMCPSession(sessionID)
	}()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	msgEndpoint := fmt.Sprintf("/api/v1/admin/mcp?session_id=%s", sessionID)
	fmt.Fprintf(w, "event: endpoint\ndata: %s\n\n", msgEndpoint)
	flusher.Flush()

	adminMCPLog.WithFields(logrus.Fields{"session_id": sessionID, "admin_id": adminID}).Info("admin SSE session opened")

	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-ctx.Done():
			adminMCPLog.WithField("session_id", sessionID).Info("admin SSE session closed")
			return
		case <-keepalive.C:
			fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()
			h.sess.pingMCPSession(sessionID)
		case msg, ok := <-sess.ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", msg)
			flusher.Flush()
		}
	}
}

func (h *AdminMCPHandler) writeAdminAuditLog(sessionID, adminID, toolName string, args interface{}, resultOK bool, errMsg string) {
	h.sess.writeAuditLog(sessionID, 0, adminID, toolName, args, resultOK, errMsg)
}

func (h *AdminMCPHandler) HandleMessage(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		http.Error(w, `{"error":"missing session_id"}`, http.StatusBadRequest)
		return
	}

	val, ok := h.sessions.Load(sessionID)
	if !ok {
		http.Error(w, `{"error":"session not found or expired"}`, http.StatusNotFound)
		return
	}
	sess := val.(*adminMCPSession)

	var req jsonRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.sendAdminSSE(sess, jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      nil,
			Error:   &jsonRPCErr{Code: -32700, Message: "Parse error"},
		})
		w.WriteHeader(http.StatusAccepted)
		return
	}

	callerAdminID := middleware.GetAdminID(r.Context())
	if sess.adminID != callerAdminID {
		http.Error(w, `{"error":"session not found or expired"}`, http.StatusNotFound)
		return
	}

	select {
	case sess.sem <- struct{}{}:
	default:
		http.Error(w, `{"error":"too many concurrent requests"}`, http.StatusTooManyRequests)
		return
	}

	dispatchCtx, dispatchCancel := context.WithTimeout(context.Background(), 30*time.Second)
	go func() {
		defer dispatchCancel()
		defer func() { <-sess.sem }()
		h.dispatchAdmin(dispatchCtx, sess, req)
	}()

	w.WriteHeader(http.StatusAccepted)
}

func (h *AdminMCPHandler) sendAdminSSE(sess *adminMCPSession, resp jsonRPCResponse) {
	b, err := json.Marshal(resp)
	if err != nil {
		return
	}
	select {
	case sess.ch <- b:
	default:
		adminMCPLog.Warn("admin SSE channel full, dropping message")
	}
}

func (h *AdminMCPHandler) dispatchAdmin(ctx context.Context, sess *adminMCPSession, req jsonRPCRequest) {
	var result interface{}
	var rpcErr *jsonRPCErr

	switch req.Method {
	case "initialize":
		result = h.handleAdminInitialize()
	case "tools/list":
		result = h.handleAdminToolsList(sess.capabilities)
	case "tools/call":
		result, rpcErr = h.handleAdminToolsCall(ctx, sess, req.Params)
	default:
		rpcErr = &jsonRPCErr{Code: -32601, Message: fmt.Sprintf("Method not found: %s", req.Method)}
	}

	h.sendAdminSSE(sess, jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  result,
		Error:   rpcErr,
	})
}

func (h *AdminMCPHandler) handleAdminInitialize() interface{} {
	return map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"serverInfo": map[string]string{
			"name":    "AutoPeer Admin MCP",
			"version": "1.0.0",
		},
		"capabilities": map[string]interface{}{
			"tools": map[string]interface{}{},
		},
	}
}

func adminReadTool(name, description, capability string, schema mcpToolSchema, handler func(context.Context, json.RawMessage, *adminMCPToolDefinition) (interface{}, error)) adminMCPToolDefinition {
	return adminMCPToolDefinition{Tool: mcpTool{Name: name, Description: description, InputSchema: schema}, Capability: capability, Handler: handler}
}

func (h *AdminMCPHandler) adminToolDefinitions() []adminMCPToolDefinition {
	emptySchema := mcpToolSchema{Type: "object", Properties: map[string]interface{}{}}
	peerIDSchema := mcpToolSchema{Type: "object", Properties: map[string]interface{}{"id": map[string]string{"type": "string", "description": "Peer UUID"}}, Required: []string{"id"}}
	return []adminMCPToolDefinition{
		adminReadTool("nodes_list", "List all BGP peering nodes.", "admin:read:topology", emptySchema, h.handleAdminToolNodesList),
		adminReadTool("nodes_stats", "Get aggregated node statistics.", "admin:read:topology", emptySchema, h.handleAdminToolNodesStats),
		adminReadTool("peers_list_all", "List all peers across all ASNs with optional status filter.", "admin:read:peers", mcpToolSchema{Type: "object", Properties: map[string]interface{}{"status": map[string]string{"type": "string", "description": "Filter by status"}, "page": map[string]interface{}{"type": "integer"}, "per_page": map[string]interface{}{"type": "integer"}}}, h.handleAdminToolPeersListAll),
		adminReadTool("peers_list_by_asn", "List all peers for a specific ASN.", "admin:read:peers", mcpToolSchema{Type: "object", Properties: map[string]interface{}{"asn": map[string]interface{}{"type": "integer"}}, Required: []string{"asn"}}, h.handleAdminToolPeersListByASN),
		adminReadTool("peer_get", "Get details of a single peer by ID.", "admin:read:peers", peerIDSchema, h.handleAdminToolPeerGet),
		adminReadTool("peer_get_sensitive", "Get sensitive details of a single peer by ID, including endpoint, email, public keys, and node addresses.", "admin:read:sensitive", peerIDSchema, h.handleAdminToolPeerGetSensitive),
		adminReadTool("peer_metrics", "Get time-series metrics for any peer.", "admin:read:metrics", mcpToolSchema{Type: "object", Properties: map[string]interface{}{"id": map[string]string{"type": "string"}, "hours": map[string]interface{}{"type": "integer"}}, Required: []string{"id"}}, h.handleAdminToolPeerMetrics),
		adminReadTool("users_list", "List all ASNs that have at least one peer on the platform.", "admin:read:peers", emptySchema, h.handleAdminToolUsersList),
		adminReadTool("mcp_keys_list", "List all user MCP keys metadata.", "admin:read:mcp_keys", mcpToolSchema{Type: "object", Properties: map[string]interface{}{"asn": map[string]interface{}{"type": "integer"}}}, h.handleAdminToolMCPKeysList),
		adminReadTool("mcp_sessions_list", "List recent MCP sessions.", "admin:read:mcp_keys", mcpToolSchema{Type: "object", Properties: map[string]interface{}{"limit": map[string]interface{}{"type": "integer"}}}, h.handleAdminToolMCPSessionsList),
		adminReadTool("audit_logs_list", "List MCP audit logs with optional filters.", "admin:read:audit", mcpToolSchema{Type: "object", Properties: map[string]interface{}{"asn": map[string]interface{}{"type": "integer"}, "tool": map[string]string{"type": "string"}, "page": map[string]interface{}{"type": "integer"}, "per_page": map[string]interface{}{"type": "integer"}}}, h.handleAdminToolAuditLogsList),
		adminReadTool("site_settings_get", "Get all current site settings.", "admin:read:settings", emptySchema, h.handleAdminToolSiteSettingsGet),
		adminReadTool("notification_settings_get", "Get notification settings.", "admin:read:settings", emptySchema, h.handleAdminToolNotificationSettingsGet),
		adminReadTool("releases_list", "List agent releases.", "admin:read:system", emptySchema, h.handleAdminToolReleasesList),
		adminReadTool("bot_settings_get", "Get Telegram bot settings metadata.", "admin:read:settings", emptySchema, h.handleAdminToolBotSettingsGet),
		adminReadTool("bot_stats_get", "Get Telegram bot command statistics.", "admin:read:system", emptySchema, h.handleAdminToolBotStatsGet),
		adminReadTool("bot_blocked_list", "List Telegram bot blocked users.", "admin:read:system", emptySchema, h.handleAdminToolBotBlockedList),
		adminReadTool("system_stats_get", "Get administrator system statistics.", "admin:read:system", emptySchema, h.handleAdminToolSystemStatsGet),
	}
}

func (h *AdminMCPHandler) handleAdminToolsList(capabilities []string) interface{} {
	tools := []mcpTool{}
	for _, def := range h.adminToolDefinitions() {
		if hasCapability(capabilities, def.Capability) {
			tools = append(tools, def.Tool)
		}
	}
	return map[string]interface{}{"tools": tools}
}

func (h *AdminMCPHandler) findAdminTool(name string) (*adminMCPToolDefinition, bool) {
	for _, def := range h.adminToolDefinitions() {
		if def.Tool.Name == name {
			return &def, true
		}
	}
	return nil, false
}

func (h *AdminMCPHandler) handleAdminToolsCall(ctx context.Context, sess *adminMCPSession, raw json.RawMessage) (interface{}, *jsonRPCErr) {
	var p mcpCallParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &jsonRPCErr{Code: -32602, Message: "Invalid params"}
	}
	if len(p.Arguments) == 0 {
		p.Arguments = json.RawMessage(`{}`)
	}
	def, ok := h.findAdminTool(p.Name)
	if !ok {
		return nil, &jsonRPCErr{Code: -32601, Message: fmt.Sprintf("Unknown tool: %s", p.Name)}
	}
	if !hasCapability(sess.capabilities, def.Capability) {
		h.writeAdminAuditLog(sess.sessionID, sess.adminID, p.Name, sanitizeArgs(p.Name, p.Arguments), false, "forbidden")
		return nil, &jsonRPCErr{Code: -32003, Message: "forbidden"}
	}
	started := time.Now()
	data, err := def.Handler(ctx, p.Arguments, def)
	duration := time.Since(started).Milliseconds()
	resultOK := err == nil
	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	}
	h.sess.writeAuditLogDetailed(auditEntry{sessionID: sess.sessionID, adminID: sess.adminID, adminKeyID: sess.adminKeyID, toolName: p.Name, args: sanitizeArgs(p.Name, p.Arguments), capability: def.Capability, mode: mcpModeRead, durationMs: duration, resultOK: resultOK, errMsg: errMsg})
	if err != nil {
		return nil, &jsonRPCErr{Code: -32000, Message: err.Error()}
	}
	return mcpTextResult(data), nil
}

func (h *AdminMCPHandler) adminToolNodesList(ctx context.Context) (interface{}, error) {
	nodeList, err := h.nodes.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to query nodes: %w", err)
	}

	type nodeResp struct {
		ID          string    `json:"id"`
		Name        string    `json:"name"`
		Location    string    `json:"location"`
		PublicIP    string    `json:"public_ip"`
		OurASN      int64     `json:"our_asn"`
		OurLLA      string    `json:"our_lla"`
		OurWgPubkey string    `json:"our_wg_pubkey"`
		Online      bool      `json:"online"`
		Enabled     bool      `json:"enabled"`
		CreatedAt   time.Time `json:"created_at"`
	}
	nodes := make([]nodeResp, 0, len(nodeList))
	for _, n := range nodeList {
		nodes = append(nodes, nodeResp{
			ID:          n.ID,
			Name:        n.Name,
			Location:    n.Location,
			PublicIP:    n.PublicIP,
			OurASN:      n.OurASN,
			OurLLA:      n.OurLLA,
			OurWgPubkey: n.OurWgPubkey,
			Online:      n.Online,
			Enabled:     n.Enabled,
			CreatedAt:   n.CreatedAt,
		})
	}
	return nodes, nil
}

func (h *AdminMCPHandler) adminToolNodesStats(ctx context.Context) (interface{}, error) {
	nodeList, err := h.nodes.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to query node stats: %w", err)
	}

	type nodeStat struct {
		ID           string `json:"id"`
		Name         string `json:"name"`
		Online       bool   `json:"online"`
		Enabled      bool   `json:"enabled"`
		ActivePeers  int    `json:"active_peers"`
		PendingPeers int    `json:"pending_peers"`
		TotalPeers   int    `json:"total_peers"`
	}
	stats := make([]nodeStat, 0, len(nodeList))
	for _, n := range nodeList {
		peersOnNode, _ := h.peers.ListByNode(ctx, n.ID)
		active, pending, total := 0, 0, len(peersOnNode)
		for _, p := range peersOnNode {
			switch p.Status {
			case "active":
				active++
			case "pending":
				pending++
			}
		}
		stats = append(stats, nodeStat{
			ID:           n.ID,
			Name:         n.Name,
			Online:       n.Online,
			Enabled:      n.Enabled,
			ActivePeers:  active,
			PendingPeers: pending,
			TotalPeers:   total,
		})
	}
	return stats, nil
}

func (h *AdminMCPHandler) adminToolPeersListAll(ctx context.Context, status *string, page, perPage int) (interface{}, error) {
	params := repository.ListParams{
		Status:   "",
		Page:     page,
		PageSize: perPage,
	}
	if status != nil && *status != "" {
		params.Status = *status
	}
	results, _, err := h.peers.List(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("failed to query peers: %w", err)
	}

	type peerResp struct {
		ID              string    `json:"id"`
		NodeID          string    `json:"node_id"`
		RemoteASN       int64     `json:"remote_asn"`
		RemotePubkey    string    `json:"remote_pubkey"`
		RemoteEndpoint  string    `json:"remote_endpoint"`
		RemoteLLA       string    `json:"remote_lla"`
		ContactEmail    string    `json:"contact_email"`
		WgListenPort    int       `json:"wg_listen_port"`
		WgInterfaceName string    `json:"wg_interface_name"`
		WgManaged       bool      `json:"wg_managed"`
		Status          string    `json:"status"`
		RejectReason    *string   `json:"reject_reason,omitempty"`
		CreatedAt       time.Time `json:"created_at"`
		UpdatedAt       time.Time `json:"updated_at"`
		NodeName        string    `json:"node_name"`
		NodeLocation    string    `json:"node_location"`
		MTU             *int      `json:"mtu,omitempty"`
	}
	peers := make([]peerResp, 0, len(results))
	for _, r := range results {
		peers = append(peers, peerResp{
			ID:              r.ID,
			NodeID:          r.NodeID,
			RemoteASN:       r.RemoteASN,
			RemotePubkey:    r.RemotePubkey,
			RemoteEndpoint:  r.RemoteEndpoint,
			RemoteLLA:       r.RemoteLLA,
			ContactEmail:    r.ContactEmail,
			WgListenPort:    r.WgListenPort,
			WgInterfaceName: r.WgInterfaceName,
			WgManaged:       r.WgManaged,
			Status:          r.Status,
			RejectReason:    r.RejectReason,
			CreatedAt:       r.CreatedAt,
			UpdatedAt:       r.UpdatedAt,
			NodeName:        r.NodeName,
			NodeLocation:    r.NodeLocation,
			MTU:             r.MTU,
		})
	}
	return peers, nil
}

func (h *AdminMCPHandler) adminToolPeersListByASN(ctx context.Context, asn int64) (interface{}, error) {
	if asn == 0 {
		return nil, fmt.Errorf("asn is required")
	}
	results, err := h.peers.ListByASN(ctx, asn)
	if err != nil {
		return nil, fmt.Errorf("failed to query peers: %w", err)
	}

	type peerResp struct {
		ID              string    `json:"id"`
		NodeID          string    `json:"node_id"`
		RemoteASN       int64     `json:"remote_asn"`
		RemotePubkey    string    `json:"remote_pubkey"`
		RemoteEndpoint  string    `json:"remote_endpoint"`
		RemoteLLA       string    `json:"remote_lla"`
		ContactEmail    string    `json:"contact_email"`
		WgListenPort    int       `json:"wg_listen_port"`
		WgInterfaceName string    `json:"wg_interface_name"`
		WgManaged       bool      `json:"wg_managed"`
		Status          string    `json:"status"`
		RejectReason    *string   `json:"reject_reason,omitempty"`
		CreatedAt       time.Time `json:"created_at"`
		UpdatedAt       time.Time `json:"updated_at"`
		NodeName        string    `json:"node_name"`
		NodeLocation    string    `json:"node_location"`
		MTU             *int      `json:"mtu,omitempty"`
	}
	peers := make([]peerResp, 0, len(results))
	for _, r := range results {
		peers = append(peers, peerResp{
			ID:              r.ID,
			NodeID:          r.NodeID,
			RemoteASN:       r.RemoteASN,
			RemotePubkey:    r.RemotePubkey,
			RemoteEndpoint:  r.RemoteEndpoint,
			RemoteLLA:       r.RemoteLLA,
			ContactEmail:    r.ContactEmail,
			WgListenPort:    r.WgListenPort,
			WgInterfaceName: r.WgInterfaceName,
			WgManaged:       r.WgManaged,
			Status:          r.Status,
			RejectReason:    r.RejectReason,
			CreatedAt:       r.CreatedAt,
			UpdatedAt:       r.UpdatedAt,
			NodeName:        r.NodeName,
			NodeLocation:    r.NodeLocation,
			MTU:             r.MTU,
		})
	}
	return peers, nil
}

func (h *AdminMCPHandler) adminToolPeerGet(ctx context.Context, id string) (interface{}, error) {
	if id == "" {
		return nil, fmt.Errorf("id is required")
	}
	p, err := h.peers.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("peer not found")
	}
	n, _ := h.nodes.GetByID(ctx, p.NodeID)

	type peerDetail struct {
		ID              string    `json:"id"`
		NodeID          string    `json:"node_id"`
		RemoteASN       int64     `json:"remote_asn"`
		RemotePubkey    string    `json:"remote_pubkey"`
		RemoteEndpoint  string    `json:"remote_endpoint"`
		RemoteLLA       string    `json:"remote_lla"`
		ContactEmail    string    `json:"contact_email"`
		WgListenPort    int       `json:"wg_listen_port"`
		WgInterfaceName string    `json:"wg_interface_name"`
		WgManaged       bool      `json:"wg_managed"`
		Status          string    `json:"status"`
		RejectReason    *string   `json:"reject_reason,omitempty"`
		CreatedAt       time.Time `json:"created_at"`
		UpdatedAt       time.Time `json:"updated_at"`
		MTU             *int      `json:"mtu,omitempty"`
		NodeName        string    `json:"node_name"`
		NodeLocation    string    `json:"node_location"`
		NodePublicIP    string    `json:"node_public_ip"`
		NodeOurLLA      string    `json:"node_our_lla"`
		NodeOurWgPubkey string    `json:"node_our_wg_pubkey"`
	}
	detail := peerDetail{
		ID:              p.ID,
		NodeID:          p.NodeID,
		RemoteASN:       p.RemoteASN,
		RemotePubkey:    p.RemotePubkey,
		RemoteEndpoint:  p.RemoteEndpoint,
		RemoteLLA:       p.RemoteLLA,
		ContactEmail:    p.ContactEmail,
		WgListenPort:    p.WgListenPort,
		WgInterfaceName: p.WgInterfaceName,
		WgManaged:       p.WgManaged,
		Status:          p.Status,
		RejectReason:    p.RejectReason,
		CreatedAt:       p.CreatedAt,
		UpdatedAt:       p.UpdatedAt,
		MTU:             p.MTU,
	}
	if n != nil {
		detail.NodeName = n.Name
		detail.NodeLocation = n.Location
		detail.NodePublicIP = n.PublicIP
		detail.NodeOurLLA = n.OurLLA
		detail.NodeOurWgPubkey = n.OurWgPubkey
	}
	return detail, nil
}

func (h *AdminMCPHandler) adminToolPeerGetSummary(ctx context.Context, id string) (interface{}, error) {
	if id == "" {
		return nil, fmt.Errorf("id is required")
	}
	p, err := h.peers.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("peer not found")
	}
	n, _ := h.nodes.GetByID(ctx, p.NodeID)
	type peerSummary struct {
		ID              string    `json:"id"`
		NodeID          string    `json:"node_id"`
		RemoteASN       int64     `json:"remote_asn"`
		WgListenPort    int       `json:"wg_listen_port"`
		WgInterfaceName string    `json:"wg_interface_name"`
		WgManaged       bool      `json:"wg_managed"`
		Status          string    `json:"status"`
		RejectReason    *string   `json:"reject_reason,omitempty"`
		CreatedAt       time.Time `json:"created_at"`
		UpdatedAt       time.Time `json:"updated_at"`
		MTU             *int      `json:"mtu,omitempty"`
		NodeName        string    `json:"node_name"`
		NodeLocation    string    `json:"node_location"`
	}
	out := peerSummary{ID: p.ID, NodeID: p.NodeID, RemoteASN: p.RemoteASN, WgListenPort: p.WgListenPort, WgInterfaceName: p.WgInterfaceName, WgManaged: p.WgManaged, Status: p.Status, RejectReason: p.RejectReason, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt, MTU: p.MTU}
	if n != nil {
		out.NodeName = n.Name
		out.NodeLocation = n.Location
	}
	return out, nil
}

func (h *AdminMCPHandler) adminToolPeerMetrics(ctx context.Context, id string, hours int) (interface{}, error) {
	if id == "" {
		return nil, fmt.Errorf("id is required")
	}
	_, err := h.peers.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("peer not found")
	}

	metrics, err := h.metrics.QueryPeerMetrics(ctx, id, hours)
	if err != nil {
		return nil, fmt.Errorf("failed to query metrics: %w", err)
	}

	type metricPoint struct {
		Time          time.Time  `json:"time"`
		RxBytes       *int64     `json:"rx_bytes"`
		TxBytes       *int64     `json:"tx_bytes"`
		RttMs         *float64   `json:"rtt_ms"`
		BGPState      *string    `json:"bgp_state"`
		LastHandshake *time.Time `json:"last_handshake"`
		BGPUptimeSecs *int       `json:"bgp_uptime_secs"`
	}
	points := make([]metricPoint, 0, len(metrics))
	for _, m := range metrics {
		points = append(points, metricPoint{
			Time:          m.Time,
			RxBytes:       int64Ptr(m.RxBytes),
			TxBytes:       int64Ptr(m.TxBytes),
			RttMs:         m.RttMs,
			BGPState:      stringPtr(m.BGPState),
			LastHandshake: m.LastHandshake,
			BGPUptimeSecs: m.BGPUptimeSecs,
		})
	}
	return map[string]interface{}{"peer_id": id, "hours": hours, "points": points}, nil
}

func (h *AdminMCPHandler) adminToolUsersList(ctx context.Context) (interface{}, error) {
	allPeers, _, err := h.peers.List(ctx, repository.ListParams{Page: 1, PageSize: 10000})
	if err != nil {
		return nil, fmt.Errorf("failed to query users: %w", err)
	}

	type userEntry struct {
		ASN         int64     `json:"asn"`
		PeerCount   int       `json:"peer_count"`
		ActivePeers int       `json:"active_peers"`
		FirstPeerAt time.Time `json:"first_peer_at"`
	}
	asnMap := make(map[int64]*userEntry)
	asnOrder := []int64{}
	for _, p := range allPeers {
		entry, ok := asnMap[p.RemoteASN]
		if !ok {
			entry = &userEntry{ASN: p.RemoteASN, FirstPeerAt: p.CreatedAt}
			asnMap[p.RemoteASN] = entry
			asnOrder = append(asnOrder, p.RemoteASN)
		}
		entry.PeerCount++
		if p.Status == "active" {
			entry.ActivePeers++
		}
		if p.CreatedAt.Before(entry.FirstPeerAt) {
			entry.FirstPeerAt = p.CreatedAt
		}
	}
	users := make([]userEntry, 0, len(asnMap))
	for _, asn := range asnOrder {
		users = append(users, *asnMap[asn])
	}
	return users, nil
}

func (h *AdminMCPHandler) adminToolMCPKeysList(ctx context.Context, asn *int64) (interface{}, error) {
	asnVal := int64(0)
	if asn != nil {
		asnVal = *asn
	}
	keys, err := h.mcp.ListAllUserKeys(ctx, asnVal)
	if err != nil {
		return nil, fmt.Errorf("failed to query MCP keys: %w", err)
	}

	type keyEntry struct {
		ID         string     `json:"id"`
		ASN        int64      `json:"asn"`
		Name       string     `json:"name"`
		KeyPrefix  string     `json:"key_prefix"`
		ExpiresAt  *time.Time `json:"expires_at"`
		LastUsedAt *time.Time `json:"last_used_at"`
		CreatedAt  time.Time  `json:"created_at"`
	}
	result := make([]keyEntry, 0, len(keys))
	for _, k := range keys {
		result = append(result, keyEntry{
			ID:         k.ID,
			ASN:        k.ASN,
			Name:       k.Name,
			KeyPrefix:  k.KeyPrefix,
			ExpiresAt:  k.ExpiresAt,
			LastUsedAt: k.LastUsedAt,
			CreatedAt:  k.CreatedAt,
		})
	}
	return result, nil
}

func (h *AdminMCPHandler) adminToolMCPSessionsList(ctx context.Context, limit int) (interface{}, error) {
	sessions, err := h.mcp.ListSessions(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query MCP sessions: %w", err)
	}

	type sessionEntry struct {
		ID             string     `json:"id"`
		ASN            *int64     `json:"asn,omitempty"`
		KeyID          *string    `json:"key_id,omitempty"`
		IsAdmin        bool       `json:"is_admin"`
		AdminKeyID     *string    `json:"admin_key_id,omitempty"`
		ConnectedAt    time.Time  `json:"connected_at"`
		DisconnectedAt *time.Time `json:"disconnected_at,omitempty"`
		LastPingAt     *time.Time `json:"last_ping_at,omitempty"`
	}
	result := make([]sessionEntry, 0, len(sessions))
	for _, s := range sessions {
		result = append(result, sessionEntry{
			ID:             s.ID,
			ASN:            s.ASN,
			KeyID:          s.KeyID,
			IsAdmin:        s.IsAdmin,
			AdminKeyID:     s.AdminKeyID,
			ConnectedAt:    s.ConnectedAt,
			DisconnectedAt: s.DisconnectedAt,
			LastPingAt:     s.LastPingAt,
		})
	}
	return result, nil
}

func (h *AdminMCPHandler) adminToolAuditLogsList(ctx context.Context, asn *int64, tool *string, page, perPage int) (interface{}, error) {
	params := repository.ListParams{
		Page:     page,
		PageSize: perPage,
	}
	if asn != nil {
		params.ASN = *asn
	}
	if tool != nil && *tool != "" {
		params.Search = *tool
	}
	logs, _, err := h.mcp.ListAuditLogs(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("failed to query audit logs: %w", err)
	}

	type auditEntry struct {
		ID        string          `json:"id"`
		SessionID *string         `json:"session_id,omitempty"`
		ASN       *int64          `json:"asn,omitempty"`
		AdminID   *string         `json:"admin_id,omitempty"`
		ToolName  string          `json:"tool_name"`
		Args      json.RawMessage `json:"args,omitempty"`
		ResultOK  bool            `json:"result_ok"`
		ErrorMsg  *string         `json:"error_msg,omitempty"`
		CalledAt  time.Time       `json:"called_at"`
	}
	result := make([]auditEntry, 0, len(logs))
	for _, l := range logs {
		var argsJSON json.RawMessage
		if l.Args != nil {
			argsJSON, _ = json.Marshal(l.Args)
		}
		result = append(result, auditEntry{
			ID:        l.ID,
			SessionID: l.SessionID,
			ASN:       l.ASN,
			AdminID:   l.AdminID,
			ToolName:  l.ToolName,
			Args:      argsJSON,
			ResultOK:  l.ResultOK,
			ErrorMsg:  l.ErrorMsg,
			CalledAt:  l.CalledAt,
		})
	}
	return map[string]interface{}{
		"logs":     result,
		"page":     page,
		"per_page": perPage,
	}, nil
}

func (h *AdminMCPHandler) adminToolSiteSettingsGet(ctx context.Context) (interface{}, error) {
	settings, err := h.settings.GetAllSiteSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to query site settings: %w", err)
	}
	result := map[string]string{}
	for _, s := range settings {
		result[s.SettingKey] = s.SettingValue
	}
	return result, nil
}
