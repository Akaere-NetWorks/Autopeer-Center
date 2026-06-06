package handler

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/akaere/autopeer-center/internal/middleware"
	"github.com/akaere/autopeer-center/internal/model"
	"github.com/akaere/autopeer-center/internal/peering"
	"github.com/akaere/autopeer-center/internal/queue"
	"github.com/akaere/autopeer-center/internal/repository"
	"github.com/akaere/autopeer-center/internal/ws"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/sirupsen/logrus"
)

var mcpLog = logrus.WithField("pkg", "handler.mcp")

type mcpSession struct {
	asn          int64
	sessionID    string
	keyID        string
	capabilities []string
	ch           chan []byte
	cancel       context.CancelFunc
	sem          chan struct{}
}

const (
	mcpModeRead      = "read"
	mcpModeSyncWrite = "sync_write"
	mcpModeOperation = "operation"
)

type mcpToolDefinition struct {
	Tool       mcpTool
	Capability string
	Mode       string
	Handler    func(context.Context, int64, json.RawMessage, *mcpToolDefinition, string) (interface{}, error)
}

type mcpCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type mcpContextKey string

const mcpContextKeyKeyID mcpContextKey = "mcp_key_id"

type MCPHandler struct {
	mcp                repository.MCPRepository
	peers              repository.PeerRepository
	nodes              repository.NodeRepository
	metrics            repository.MetricsRepository
	settings           repository.SettingsRepository
	hub                *ws.Hub
	sessions           sync.Map
	sess               *mcpSessionDB
	assistantApprovals sync.Map
}

func NewMCPHandler(mcp repository.MCPRepository, peers repository.PeerRepository, nodes repository.NodeRepository, metrics repository.MetricsRepository, settings repository.SettingsRepository, hub *ws.Hub, q *queue.Queue) (*MCPHandler, func()) {
	sessDB, stop := newMCPSessionDB(mcp, q, mcpLog)
	h := &MCPHandler{mcp: mcp, peers: peers, nodes: nodes, metrics: metrics, settings: settings, hub: hub, sess: sessDB}
	approvalStop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				h.cleanupAssistantApprovals()
			case <-approvalStop:
				return
			}
		}
	}()
	return h, func() {
		close(approvalStop)
		stop()
	}
}

func (h *MCPHandler) HandleSSE(w http.ResponseWriter, r *http.Request) {
	asn := middleware.GetASN(r.Context())
	keyID := middleware.GetMCPKeyID(r.Context())

	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	sessionID := uuid.New().String()
	ctx, cancel := context.WithCancel(r.Context())
	sess := &mcpSession{
		asn:          asn,
		sessionID:    sessionID,
		keyID:        keyID,
		capabilities: middleware.GetMCPCapabilities(r.Context()),
		ch:           make(chan []byte, 64),
		cancel:       cancel,
		sem:          make(chan struct{}, 5),
	}
	h.sessions.Store(sessionID, sess)

	h.sess.insertMCPSession(sessionID, asn, keyID, false, "")

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

	msgEndpoint := fmt.Sprintf("/api/v1/mcp?session_id=%s", sessionID)
	fmt.Fprintf(w, "event: endpoint\ndata: %s\n\n", msgEndpoint)
	flusher.Flush()

	mcpLog.WithFields(logrus.Fields{"session_id": sessionID, "asn": asn}).Info("SSE session opened")

	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-ctx.Done():
			mcpLog.WithField("session_id", sessionID).Info("SSE session closed (client disconnected)")
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

func (h *MCPHandler) writeAuditLog(sessionID string, asn int64, adminID, toolName string, args interface{}, resultOK bool, errMsg string) {
	h.sess.writeAuditLog(sessionID, asn, adminID, toolName, args, resultOK, errMsg)
}

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *jsonRPCErr `json:"error,omitempty"`
}

type jsonRPCErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (h *MCPHandler) HandleMessage(w http.ResponseWriter, r *http.Request) {
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
	sess := val.(*mcpSession)

	var req jsonRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.sendSSE(sess, jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      nil,
			Error:   &jsonRPCErr{Code: -32700, Message: "Parse error"},
		})
		w.WriteHeader(http.StatusAccepted)
		return
	}

	callerASN := middleware.GetASN(r.Context())
	if sess.asn != callerASN {
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
		h.dispatch(dispatchCtx, sess, req)
	}()

	w.WriteHeader(http.StatusAccepted)
}

func (h *MCPHandler) sendSSE(sess *mcpSession, resp jsonRPCResponse) {
	b, err := json.Marshal(resp)
	if err != nil {
		return
	}
	select {
	case sess.ch <- b:
	default:
		mcpLog.Warn("SSE channel full, dropping message")
	}
}

func (h *MCPHandler) dispatch(ctx context.Context, sess *mcpSession, req jsonRPCRequest) {
	var result interface{}
	var rpcErr *jsonRPCErr

	switch req.Method {
	case "initialize":
		result = h.handleInitialize()
	case "tools/list":
		result = h.handleToolsList(sess.capabilities)
	case "tools/call":
		result, rpcErr = h.handleToolsCall(ctx, sess, req.Params)
	default:
		rpcErr = &jsonRPCErr{Code: -32601, Message: fmt.Sprintf("Method not found: %s", req.Method)}
	}

	h.sendSSE(sess, jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  result,
		Error:   rpcErr,
	})
}

func (h *MCPHandler) handleInitialize() interface{} {
	return map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"serverInfo": map[string]string{
			"name":    "AutoPeer MCP",
			"version": "1.0.0",
		},
		"capabilities": map[string]interface{}{
			"tools": map[string]interface{}{},
		},
	}
}

type mcpToolSchema struct {
	Type       string                 `json:"type"`
	Properties map[string]interface{} `json:"properties"`
	Required   []string               `json:"required,omitempty"`
}

type mcpTool struct {
	Name        string        `json:"name"`
	Description string        `json:"description"`
	InputSchema mcpToolSchema `json:"inputSchema"`
}

func hasCapability(caps []string, required string) bool {
	if required == "" {
		return true
	}
	for _, cap := range caps {
		if cap == required {
			return true
		}
	}
	return false
}

func decodeStrict(raw json.RawMessage, dest interface{}) error {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dest); err != nil {
		return fmt.Errorf("invalid arguments")
	}
	return nil
}

func mcpReadTool(name, description, capability string, schema mcpToolSchema, handler func(context.Context, int64, json.RawMessage, *mcpToolDefinition, string) (interface{}, error)) mcpToolDefinition {
	return mcpToolDefinition{Tool: mcpTool{Name: name, Description: description, InputSchema: schema}, Capability: capability, Mode: mcpModeRead, Handler: handler}
}

func mcpWriteTool(name, description, capability string, schema mcpToolSchema, handler func(context.Context, int64, json.RawMessage, *mcpToolDefinition, string) (interface{}, error)) mcpToolDefinition {
	return mcpToolDefinition{Tool: mcpTool{Name: name, Description: description, InputSchema: schema}, Capability: capability, Mode: mcpModeSyncWrite, Handler: handler}
}

func mcpOperationTool(name, description, capability string, schema mcpToolSchema, handler func(context.Context, int64, json.RawMessage, *mcpToolDefinition, string) (interface{}, error)) mcpToolDefinition {
	return mcpToolDefinition{Tool: mcpTool{Name: name, Description: description, InputSchema: schema}, Capability: capability, Mode: mcpModeOperation, Handler: handler}
}

func (h *MCPHandler) userToolDefinitions() []mcpToolDefinition {
	emptySchema := mcpToolSchema{Type: "object", Properties: map[string]interface{}{}}
	peerIDSchema := mcpToolSchema{Type: "object", Properties: map[string]interface{}{"id": map[string]string{"type": "string", "description": "Peer UUID"}}, Required: []string{"id"}}
	idempotency := map[string]string{"type": "string", "description": "Unique idempotency key for this write request"}
	return []mcpToolDefinition{
		mcpReadTool("nodes_list", "List all available DN42 BGP peering nodes you can peer with.", "read:nodes", emptySchema, h.handleToolNodesList),
		mcpReadTool("peer_creation_status", "Check whether new peer creation is currently enabled on the platform.", "read:peers", emptySchema, h.handleToolPeerCreationStatus),
		mcpReadTool("peer_list", "List all BGP peers belonging to your ASN.", "read:peers", emptySchema, h.handleToolPeerList),
		mcpReadTool("peer_summary", "List all BGP peers with their latest RTT, BGP state, and handshake time.", "read:metrics", emptySchema, h.handleToolPeerSummary),
		mcpReadTool("peer_get", "Get full details of a single peer including node information.", "read:peers", peerIDSchema, h.handleToolPeerGet),
		mcpReadTool("peer_get_metrics", "Get time-series metrics for a peer (RTT, BGP state, bytes transferred).", "read:metrics", mcpToolSchema{Type: "object", Properties: map[string]interface{}{"id": map[string]string{"type": "string", "description": "Peer UUID"}, "hours": map[string]interface{}{"type": "integer", "description": "Hours of history (1-720, default 24)"}}, Required: []string{"id"}}, h.handleToolPeerGetMetrics),
		mcpReadTool("mcp_audit_logs_list", "List recent MCP audit logs for your ASN.", "read:audit", mcpToolSchema{Type: "object", Properties: map[string]interface{}{"limit": map[string]interface{}{"type": "integer", "description": "Max results (1-200, default 50)"}}}, h.handleToolUserMCPAuditLogs),
		mcpReadTool("operations_list", "List pending MCP operations for your ASN.", "read:peers", mcpToolSchema{Type: "object", Properties: map[string]interface{}{"limit": map[string]interface{}{"type": "integer", "description": "Max results (1-200, default 50)"}}}, h.handleToolOperationsList),
		mcpReadTool("operation_get", "Get one pending MCP operation by ID.", "read:peers", peerIDSchema, h.handleToolOperationGet),
		mcpWriteTool("peer_create", "Create a new BGP peer on a node. The peer starts in pending status awaiting approval.", "write:peer:create", mcpToolSchema{Type: "object", Properties: map[string]interface{}{"node_id": map[string]string{"type": "string", "description": "Node UUID (from nodes_list)"}, "remote_pubkey": map[string]string{"type": "string", "description": "Your WireGuard public key (Base64)"}, "remote_endpoint": map[string]string{"type": "string", "description": "Your public endpoint IP:Port or hostname:Port"}, "remote_lla": map[string]string{"type": "string", "description": "Your WireGuard link-local IPv6 address (fe80::/10)"}, "mtu": map[string]interface{}{"type": "integer", "description": "Optional MTU override (576-9000)"}, "idempotency_key": idempotency}, Required: []string{"node_id", "remote_pubkey", "remote_endpoint", "remote_lla", "idempotency_key"}}, h.handleToolPeerCreate),
		mcpWriteTool("peer_update_pending", "Update WireGuard parameters of a pending or rejected peer. Active peers are not changed by this tool.", "write:peer:update_pending", mcpToolSchema{Type: "object", Properties: map[string]interface{}{"id": map[string]string{"type": "string", "description": "Peer UUID"}, "remote_pubkey": map[string]string{"type": "string", "description": "New WireGuard public key"}, "remote_endpoint": map[string]string{"type": "string", "description": "New endpoint IP:Port"}, "remote_lla": map[string]string{"type": "string", "description": "New link-local IPv6 address"}, "mtu": map[string]interface{}{"type": "integer", "description": "New MTU (576-9000)"}, "idempotency_key": idempotency}, Required: []string{"id", "idempotency_key"}}, h.handleToolPeerUpdatePending),
		mcpWriteTool("peer_cancel_pending", "Cancel a pending or rejected peer request. Active peers are not deleted by this tool.", "write:peer:cancel_pending", mcpToolSchema{Type: "object", Properties: map[string]interface{}{"id": map[string]string{"type": "string", "description": "Peer UUID"}, "idempotency_key": idempotency}, Required: []string{"id", "idempotency_key"}}, h.handleToolPeerCancelPending),
		mcpOperationTool("peer_update_operation_create", "Request administrator review for updating an active or suspended peer.", "write:operation:create", mcpToolSchema{Type: "object", Properties: map[string]interface{}{"id": map[string]string{"type": "string", "description": "Peer UUID"}, "remote_pubkey": map[string]string{"type": "string", "description": "Requested WireGuard public key"}, "remote_endpoint": map[string]string{"type": "string", "description": "Requested endpoint IP:Port"}, "remote_lla": map[string]string{"type": "string", "description": "Requested link-local IPv6 address"}, "mtu": map[string]interface{}{"type": "integer", "description": "Requested MTU (576-9000)"}, "idempotency_key": idempotency}, Required: []string{"id", "idempotency_key"}}, h.handleToolPeerUpdateOperationCreate),
		mcpOperationTool("peer_delete_operation_create", "Request administrator review for deleting an active or suspended peer.", "write:operation:create", mcpToolSchema{Type: "object", Properties: map[string]interface{}{"id": map[string]string{"type": "string", "description": "Peer UUID"}, "idempotency_key": idempotency}, Required: []string{"id", "idempotency_key"}}, h.handleToolPeerDeleteOperationCreate),
	}
}

func (h *MCPHandler) handleToolsList(capabilities []string) interface{} {
	tools := []mcpTool{}
	for _, def := range h.userToolDefinitions() {
		if hasCapability(capabilities, def.Capability) {
			tools = append(tools, def.Tool)
		}
	}
	return map[string]interface{}{"tools": tools}
}

func (h *MCPHandler) findUserTool(name string) (*mcpToolDefinition, bool) {
	for _, def := range h.userToolDefinitions() {
		if def.Tool.Name == name {
			return &def, true
		}
	}
	return nil, false
}

func (h *MCPHandler) handleToolsCall(ctx context.Context, sess *mcpSession, raw json.RawMessage) (interface{}, *jsonRPCErr) {
	var p mcpCallParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &jsonRPCErr{Code: -32602, Message: "Invalid params"}
	}
	if len(p.Arguments) == 0 {
		p.Arguments = json.RawMessage(`{}`)
	}
	def, ok := h.findUserTool(p.Name)
	if !ok {
		return nil, &jsonRPCErr{Code: -32601, Message: fmt.Sprintf("Unknown tool: %s", p.Name)}
	}
	if !hasCapability(sess.capabilities, def.Capability) {
		h.writeAuditLog(sess.sessionID, sess.asn, "", p.Name, sanitizeArgs(p.Name, p.Arguments), false, "forbidden")
		return nil, &jsonRPCErr{Code: -32003, Message: "forbidden"}
	}

	idemKey, reqHash, idemErr := h.prepareIdempotency(ctx, sess, p, def)
	if idemErr != nil {
		h.writeAuditLog(sess.sessionID, sess.asn, "", p.Name, sanitizeArgs(p.Name, p.Arguments), false, idemErr.Error())
		return nil, &jsonRPCErr{Code: -32602, Message: idemErr.Error()}
	}
	if idemKey != nil && idemKey.Status != "processing" {
		if idemKey.RequestHash != reqHash {
			return nil, &jsonRPCErr{Code: -32009, Message: "idempotency_conflict"}
		}
		if idemKey.Status == "succeeded" {
			return mcpTextResult(idemKey.ResponseJSON), nil
		}
		return nil, &jsonRPCErr{Code: -32000, Message: idemKey.ErrorMessage}
	}
	if idemKey != nil && idemKey.Status == "processing" && idemKey.CreatedAt.IsZero() == false {
		if idemKey.RequestHash != reqHash {
			return nil, &jsonRPCErr{Code: -32009, Message: "idempotency_conflict"}
		}
		return nil, &jsonRPCErr{Code: -32029, Message: "operation_in_progress"}
	}

	started := time.Now()
	ctx = context.WithValue(ctx, mcpContextKeyKeyID, sess.keyID)
	data, err := def.Handler(ctx, sess.asn, p.Arguments, def, reqHash)
	duration := time.Since(started).Milliseconds()
	resultOK := err == nil
	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	}
	if idemKey != nil {
		status := "succeeded"
		if err != nil {
			status = "failed"
		}
		_ = h.mcp.CompleteIdempotency(ctx, idemKey.ID, status, inferResourceType(p.Name), inferResourceID(data), data, "tool_failed", errMsg)
	}
	h.sess.writeAuditLogDetailed(auditEntry{sessionID: sess.sessionID, asn: sess.asn, keyID: sess.keyID, toolName: p.Name, args: sanitizeArgs(p.Name, p.Arguments), capability: def.Capability, mode: def.Mode, idempotencyKey: stringPtrValue(extractIdempotencyKey(p.Arguments)), requestHash: reqHash, durationMs: duration, resultOK: resultOK, errMsg: errMsg})
	if err != nil {
		return nil, &jsonRPCErr{Code: -32000, Message: err.Error()}
	}
	return mcpTextResult(data), nil
}

func sanitizeArgs(toolName string, raw json.RawMessage) map[string]interface{} {
	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	for _, key := range []string{"remote_pubkey", "remote_endpoint", "contact_email", "token", "secret", "password", "signed_message", "approval_token"} {
		delete(m, key)
	}
	return m
}

func mustJSON(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func mcpTextResult(data interface{}) map[string]interface{} {
	return map[string]interface{}{
		"content": []map[string]interface{}{
			{"type": "text", "text": mustJSON(data)},
		},
	}
}

func stringPtrValue(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func extractIdempotencyKey(raw json.RawMessage) string {
	var args struct {
		IdempotencyKey string `json:"idempotency_key"`
	}
	_ = json.Unmarshal(raw, &args)
	return args.IdempotencyKey
}

func canonicalRequestHash(raw json.RawMessage) string {
	var value map[string]interface{}
	_ = json.Unmarshal(raw, &value)
	delete(value, "idempotency_key")
	b, _ := json.Marshal(value)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (h *MCPHandler) prepareIdempotency(ctx context.Context, sess *mcpSession, p mcpCallParams, def *mcpToolDefinition) (*model.MCPIdempotencyKey, string, error) {
	if def.Mode == mcpModeRead {
		return nil, "", nil
	}
	idem := extractIdempotencyKey(p.Arguments)
	if len(idem) < 16 || len(idem) > 128 {
		return nil, "", fmt.Errorf("idempotency_key must be 16 to 128 characters")
	}
	reqHash := canonicalRequestHash(p.Arguments)
	existing, err := h.mcp.GetIdempotency(ctx, "user", sess.keyID, "", p.Name, idem)
	if err == nil {
		return existing, reqHash, nil
	}
	if err != sql.ErrNoRows {
		return nil, reqHash, err
	}
	asn := sess.asn
	keyID := sess.keyID
	entry := &model.MCPIdempotencyKey{ActorType: "user", KeyID: &keyID, ASN: &asn, ToolName: p.Name, IdempotencyKey: idem, RequestHash: reqHash, Status: "processing"}
	if err := h.mcp.CreateIdempotency(ctx, entry); err != nil {
		existing, getErr := h.mcp.GetIdempotency(ctx, "user", sess.keyID, "", p.Name, idem)
		if getErr == nil {
			return existing, reqHash, nil
		}
		return nil, reqHash, err
	}
	entry.CreatedAt = time.Now()
	return entry, reqHash, nil
}

func inferResourceType(toolName string) string {
	if strings.HasPrefix(toolName, "peer_") {
		return "peer"
	}
	if strings.Contains(toolName, "operation") {
		return "operation"
	}
	return ""
}

func inferResourceID(data interface{}) string {
	m, ok := data.(map[string]interface{})
	if !ok {
		return ""
	}
	for _, key := range []string{"id", "operation_id", "peer_id"} {
		if value, ok := m[key].(string); ok {
			return value
		}
	}
	return ""
}

func (h *MCPHandler) toolNodesList(ctx context.Context) (interface{}, error) {
	nodeList, err := h.nodes.ListEnabled(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to query nodes: %w", err)
	}

	type nodeResp struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Location    string `json:"location"`
		PublicIP    string `json:"public_ip"`
		OurASN      int64  `json:"our_asn"`
		OurLLA      string `json:"our_lla"`
		OurWgPubkey string `json:"our_wg_pubkey"`
		Online      bool   `json:"online"`
		Enabled     bool   `json:"enabled"`
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
		})
	}
	return nodes, nil
}

func (h *MCPHandler) toolPeerCreationStatus(ctx context.Context) (interface{}, error) {
	val, err := h.settings.GetSiteSetting(ctx, "peer_creation_enabled")
	if err != nil {
		return map[string]bool{"enabled": true}, nil
	}
	return map[string]bool{"enabled": val == "true"}, nil
}

func (h *MCPHandler) toolPeerList(ctx context.Context, asn int64) (interface{}, error) {
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

func (h *MCPHandler) toolPeerSummary(ctx context.Context, asn int64) (interface{}, error) {
	results, err := h.peers.SummaryByASN(ctx, asn)
	if err != nil {
		return nil, fmt.Errorf("failed to query peer summary: %w", err)
	}

	type summaryRow struct {
		PeerID          string   `json:"peer_id"`
		LatestRTT       *float64 `json:"latest_rtt"`
		LatestBGPState  *string  `json:"latest_bgp_state"`
		LatestHandshake *string  `json:"latest_handshake"`
		LatestBGPUptime *int     `json:"latest_bgp_uptime_secs"`
	}
	out := make([]summaryRow, 0, len(results))
	for _, r := range results {
		var lh *string
		if r.LatestHandshake != nil {
			s := r.LatestHandshake.Format(time.RFC3339)
			lh = &s
		}
		out = append(out, summaryRow{
			PeerID:          r.PeerID,
			LatestRTT:       r.LatestRtt,
			LatestBGPState:  r.LatestBGPState,
			LatestHandshake: lh,
			LatestBGPUptime: r.LatestBGPUptime,
		})
	}
	return out, nil
}

func (h *MCPHandler) toolPeerGet(ctx context.Context, asn int64, id string) (interface{}, error) {
	if id == "" {
		return nil, fmt.Errorf("id is required")
	}
	result, err := h.peers.GetByIDAndASNWithNode(ctx, id, asn)
	if err != nil {
		return nil, fmt.Errorf("peer not found")
	}

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
	return peerDetail{
		ID:              result.ID,
		NodeID:          result.NodeID,
		RemoteASN:       result.RemoteASN,
		RemotePubkey:    result.RemotePubkey,
		RemoteEndpoint:  result.RemoteEndpoint,
		RemoteLLA:       result.RemoteLLA,
		ContactEmail:    result.ContactEmail,
		WgListenPort:    result.WgListenPort,
		WgInterfaceName: result.WgInterfaceName,
		WgManaged:       result.WgManaged,
		Status:          result.Status,
		RejectReason:    result.RejectReason,
		CreatedAt:       result.CreatedAt,
		UpdatedAt:       result.UpdatedAt,
		MTU:             result.MTU,
		NodeName:        result.NodeName,
		NodeLocation:    result.NodeLocation,
		NodePublicIP:    result.NodePublicIP,
		NodeOurLLA:      result.NodeOurLLA,
		NodeOurWgPubkey: result.NodeOurWgPubkey,
	}, nil
}

func (h *MCPHandler) toolPeerGetMetrics(ctx context.Context, asn int64, id string, hours int) (interface{}, error) {
	if id == "" {
		return nil, fmt.Errorf("id is required")
	}
	_, err := h.peers.GetByIDAndASN(ctx, id, asn)
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

func (h *MCPHandler) toolPeerGetAllMetrics(ctx context.Context, asn int64, hours int) (interface{}, error) {
	peers, err := h.peers.ListByASN(ctx, asn)
	if err != nil {
		return nil, fmt.Errorf("failed to query peers: %w", err)
	}

	type peerMetrics struct {
		PeerID       string      `json:"peer_id"`
		NodeName     string      `json:"node_name"`
		NodeLocation string      `json:"node_location"`
		Status       string      `json:"status"`
		Metrics      interface{} `json:"metrics"`
	}

	results := make([]peerMetrics, 0, len(peers))
	for _, peer := range peers {
		metrics, err := h.toolPeerGetMetrics(ctx, asn, peer.ID, hours)
		if err != nil {
			return nil, err
		}
		results = append(results, peerMetrics{
			PeerID:       peer.ID,
			NodeName:     peer.NodeName,
			NodeLocation: peer.NodeLocation,
			Status:       peer.Status,
			Metrics:      metrics,
		})
	}

	return map[string]interface{}{"hours": hours, "peers": results}, nil
}

func int64Ptr(v int64) *int64 { return &v }
func stringPtr(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

func (h *MCPHandler) toolPeerCreate(ctx context.Context, asn int64, nodeID, pubkey, endpoint, lla string, mtu *int) (interface{}, error) {
	if nodeID == "" || pubkey == "" || endpoint == "" || lla == "" {
		return nil, fmt.Errorf("node_id, remote_pubkey, remote_endpoint, and remote_lla are required")
	}
	if !base64Re.MatchString(pubkey) || len(pubkey) < 40 {
		return nil, fmt.Errorf("invalid WireGuard public key (must be Base64, at least 40 chars)")
	}
	if !validateEndpoint(endpoint) {
		return nil, fmt.Errorf("invalid endpoint format (expected IP:Port or hostname:Port)")
	}
	if !validateLLA(lla) {
		return nil, fmt.Errorf("invalid link-local address (must be in fe80::/10)")
	}
	if mtu != nil && (*mtu < 576 || *mtu > 9000) {
		return nil, fmt.Errorf("MTU must be between 576 and 9000")
	}

	creationEnabled, err := h.settings.GetSiteSetting(ctx, "peer_creation_enabled")
	if err == nil && creationEnabled == "false" {
		return nil, fmt.Errorf("peer creation is currently disabled by the administrator")
	}

	node, err := h.nodes.GetByID(ctx, nodeID)
	if err != nil || !node.Enabled {
		return nil, fmt.Errorf("node not found or disabled")
	}

	exists, _ := h.peers.ExistsByNodeAndASN(ctx, nodeID, asn)
	if exists {
		return nil, fmt.Errorf("you already have a peer on this node")
	}

	pendingCount, _ := h.peers.CountPendingByASN(ctx, asn)
	if pendingCount >= 10 {
		return nil, fmt.Errorf("too many pending peer requests (max 10)")
	}

	wgPort := peering.WgPort(asn, node.WgPortPrefix)
	wgIfName := peering.WgInterfaceName(asn)

	portExists, _ := h.peers.ExistsByNodeAndPort(ctx, nodeID, wgPort)
	if portExists {
		return nil, fmt.Errorf("WireGuard port %d is already in use on this node; contact the administrator", wgPort)
	}

	peer := &model.Peer{
		NodeID:          nodeID,
		RemoteASN:       asn,
		RemotePubkey:    pubkey,
		RemoteEndpoint:  endpoint,
		RemoteLLA:       lla,
		ContactEmail:    "",
		WgListenPort:    wgPort,
		WgInterfaceName: wgIfName,
		MTU:             mtu,
		Status:          "pending",
	}
	if err := h.peers.Create(ctx, peer); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, fmt.Errorf("WireGuard port %d is already in use on this node (concurrent request); contact the administrator", wgPort)
		}
		return nil, fmt.Errorf("failed to create peer: %w", err)
	}

	return map[string]interface{}{
		"id":                peer.ID,
		"status":            "pending",
		"node_name":         node.Name,
		"wg_listen_port":    wgPort,
		"wg_interface_name": wgIfName,
		"message":           "Peer created and is pending approval by an administrator.",
	}, nil
}
