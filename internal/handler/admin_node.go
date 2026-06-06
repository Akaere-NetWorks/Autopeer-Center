package handler

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"

	"github.com/akaere/autopeer-center/internal/middleware"
	"github.com/akaere/autopeer-center/internal/model"
	"github.com/akaere/autopeer-center/internal/ws"
	"github.com/go-chi/chi/v5"
	"github.com/sirupsen/logrus"
)

type createNodeReq struct {
	Name         string `json:"name"`
	Location     string `json:"location"`
	PublicIP     string `json:"public_ip"`
	OurASN       int64  `json:"our_asn"`
	OurLLA       string `json:"our_lla"`
	OurWgPubkey  string `json:"our_wg_pubkey"`
	BirdPeerDir  string `json:"bird_peer_dir"`
	WgDir        string `json:"wg_dir"`
	WgPortPrefix int    `json:"wg_port_prefix"`
}

type updateNodeReq struct {
	Name        string `json:"name"`
	Location    string `json:"location"`
	PublicIP    string `json:"public_ip"`
	OurLLA      string `json:"our_lla"`
	OurWgPubkey string `json:"our_wg_pubkey"`
	Enabled     *bool  `json:"enabled"`
}

func (h *AdminHandler) ListNodes(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	results, err := h.stats.AdminListNodes(ctx)
	if err != nil {
		adminLog.WithError(err).Error("ListNodes: failed to fetch nodes")
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to fetch nodes")
		return
	}

	type nodeResp struct {
		ID            string   `json:"id"`
		Name          string   `json:"name"`
		Location      string   `json:"location"`
		PublicIP      string   `json:"public_ip"`
		OurASN        int64    `json:"our_asn"`
		OurLLA        string   `json:"our_lla"`
		OurWgPubkey   string   `json:"our_wg_pubkey"`
		BirdPeerDir   string   `json:"bird_peer_dir"`
		WgDir         string   `json:"wg_dir"`
		WgPortPrefix  int      `json:"wg_port_prefix"`
		Online        bool     `json:"online"`
		Enabled       bool     `json:"enabled"`
		AgentVersion  string   `json:"agent_version"`
		AgentState    string   `json:"agent_state"`
		CreatedAt     string   `json:"created_at"`
		AuthMode      string   `json:"auth_mode"`
		ActivePeers   int      `json:"active_peers"`
		AvgRttMs      *float64 `json:"avg_rtt_ms,omitempty"`
		LastSeen      *string  `json:"last_seen,omitempty"`
		TotalRxMB     *float64 `json:"total_rx_mb,omitempty"`
		TotalTxMB     *float64 `json:"total_tx_mb,omitempty"`
		MemAllocMB    *int64   `json:"mem_alloc_mb,omitempty"`
		MemSysMB      *int64   `json:"mem_sys_mb,omitempty"`
		NumGoroutine  *int     `json:"num_goroutine,omitempty"`
		UptimeSecs    *int64   `json:"uptime_secs,omitempty"`
	}

	nodes := make([]nodeResp, 0, len(results))
	for _, n := range results {
		nodes = append(nodes, nodeResp{
			ID:            n.ID,
			Name:          n.Name,
			Location:      n.Location,
			PublicIP:      n.PublicIP,
			OurASN:        n.OurASN,
			OurLLA:        n.OurLLA,
			OurWgPubkey:   n.OurWgPubkey,
			BirdPeerDir:   n.BirdPeerDir,
			WgDir:         n.WgDir,
			WgPortPrefix:  n.WgPortPrefix,
			Online:        n.Online,
			Enabled:       n.Enabled,
			AgentVersion:  n.AgentVersion,
			AgentState:    n.AgentState,
			CreatedAt:     n.CreatedAt.UTC().Format(time.RFC3339),
			AuthMode:      n.AuthMode,
			ActivePeers:   n.ActivePeers,
			AvgRttMs:      n.AvgRttMs,
			LastSeen:      n.LastSeen,
			TotalRxMB:     n.TotalRxMB,
			TotalTxMB:     n.TotalTxMB,
			MemAllocMB:    n.MemAllocMB,
			MemSysMB:      n.MemSysMB,
			NumGoroutine:  n.NumGoroutine,
			UptimeSecs:    n.UptimeSecs,
		})
	}

	adminLog.WithField("count", len(nodes)).Debug("ListNodes returning results")

	JSON(w, http.StatusOK, nodes)
}

func (h *AdminHandler) CreateNode(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	adminEmail := middleware.GetEmail(ctx)

	var req createNodeReq
	if err := DecodeJSON(r, &req); err != nil {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "Invalid JSON body")
		return
	}

	if req.Name == "" || req.Location == "" || req.PublicIP == "" || req.OurLLA == "" || req.OurWgPubkey == "" {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "Required fields: name, location, public_ip, our_lla, our_wg_pubkey")
		return
	}

	if req.OurASN == 0 {
		req.OurASN = 4242420000
	}
	if req.BirdPeerDir == "" {
		req.BirdPeerDir = "/etc/bird/dn42/peer/"
	}
	if req.WgDir == "" {
		req.WgDir = "/etc/wireguard/"
	}
	if req.WgPortPrefix == 0 {
		req.WgPortPrefix = 5
	}

	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to generate agent token")
		return
	}
	agentToken := hex.EncodeToString(tokenBytes)

	adminLog.WithFields(logrus.Fields{
		"name": req.Name, "location": req.Location, "token_prefix": agentToken[:8],
	}).Debug("CreateNode entry")

	node := &model.Node{
		Name:         req.Name,
		Location:     req.Location,
		AgentToken:   agentToken,
		PublicIP:     req.PublicIP,
		OurASN:       req.OurASN,
		OurLLA:       req.OurLLA,
		OurWgPubkey:  req.OurWgPubkey,
		BirdPeerDir:  req.BirdPeerDir,
		WgDir:        req.WgDir,
		WgPortPrefix: req.WgPortPrefix,
	}
	if err := h.nodes.Create(ctx, node); err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to create node")
		return
	}

	h.auditSvc.Log(ctx, "node.create", adminEmail, &node.ID, map[string]interface{}{"name": req.Name})

	adminLog.WithField("node_id", node.ID).Info("node created")

	JSON(w, http.StatusCreated, map[string]interface{}{
		"id":          node.ID,
		"agent_token": agentToken,
		"message":     "Save this agent token - it will not be shown again",
	})
}

func (h *AdminHandler) UpdateNode(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	nodeID := chi.URLParam(r, "id")
	adminEmail := middleware.GetEmail(ctx)

	var req updateNodeReq
	if err := DecodeJSON(r, &req); err != nil {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "Invalid JSON body")
		return
	}

	adminLog.WithFields(logrus.Fields{
		"node_id": nodeID, "name": req.Name, "location": req.Location,
		"public_ip": req.PublicIP, "our_lla": req.OurLLA, "our_wg_pubkey": req.OurWgPubkey, "enabled": req.Enabled,
	}).Debug("UpdateNode entry")

	node, err := h.nodes.GetByID(ctx, nodeID)
	if err != nil {
		ErrorJSON(w, r, http.StatusNotFound, "not_found", "Node not found")
		return
	}

	if req.Name != "" {
		node.Name = req.Name
	}
	if req.Location != "" {
		node.Location = req.Location
	}
	if req.PublicIP != "" {
		node.PublicIP = req.PublicIP
	}
	if req.OurLLA != "" {
		node.OurLLA = req.OurLLA
	}
	if req.OurWgPubkey != "" {
		node.OurWgPubkey = req.OurWgPubkey
	}
	if req.Enabled != nil {
		node.Enabled = *req.Enabled
	}

	if err := h.nodes.Update(ctx, node); err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to update node")
		return
	}

	h.auditSvc.Log(ctx, "node.update", adminEmail, &nodeID, nil)

	adminLog.WithField("node_id", nodeID).Info("node updated")

	JSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

func (h *AdminHandler) DeleteNode(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	nodeID := chi.URLParam(r, "id")
	adminEmail := middleware.GetEmail(ctx)

	adminLog.WithField("node_id", nodeID).Debug("DeleteNode entry")

	allPeers, err := h.peers.ListByNode(ctx, nodeID)
	if err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to verify peers")
		return
	}

	nonRejected := 0
	for _, p := range allPeers {
		if p.Status != "rejected" {
			nonRejected++
		}
	}

	adminLog.WithFields(logrus.Fields{"node_id": nodeID, "non_rejected_peers": nonRejected}).Debug("DeleteNode peers count")

	if nonRejected > 0 {
		ErrorJSON(w, r, http.StatusConflict, "node_has_peers", "Node has peers in pending/active/suspended state, clear them first")
		return
	}

	if err := h.nodes.DeleteWithPeers(ctx, nodeID); err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to delete node")
		return
	}

	h.auditSvc.Log(ctx, "node.delete", adminEmail, &nodeID, nil)

	adminLog.WithField("node_id", nodeID).Info("node deleted")

	h.hub.ClearNodeAlerts(nodeID)

	JSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *AdminHandler) RefreshBirdDetails(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	nodeID := chi.URLParam(r, "id")

	adminLog.WithField("node_id", nodeID).Debug("RefreshBirdDetails entry")

	resp, err := h.hub.SendCommand(nodeID, ws.TypeBirdDetails, nil)
	if err != nil {
		ErrorJSON(w, r, http.StatusServiceUnavailable, "agent_error", "Failed to reach agent: "+err.Error())
		return
	}
	if resp.Success != nil && !*resp.Success {
		ErrorJSON(w, r, http.StatusInternalServerError, "agent_error", "BIRD refresh failed: "+resp.Error)
		return
	}

	var payload struct {
		Details []struct {
			Name            string `json:"name"`
			State           string `json:"state"`
			RoutesImported  int    `json:"routes_imported"`
			RoutesExported  int    `json:"routes_exported"`
			RoutesPreferred int    `json:"routes_preferred"`
			UptimeSecs      int    `json:"bgp_uptime_secs"`
		} `json:"details"`
	}
	if resp.Payload != nil {
		if data, err := json.Marshal(resp.Payload); err == nil {
			json.Unmarshal(data, &payload)
		}
	}

	adminLog.WithFields(logrus.Fields{"node_id": nodeID, "protocols": len(payload.Details)}).Debug("BIRD protocols found")

	updated := 0
	for _, d := range payload.Details {
		if err := h.metrics.InsertBirdMetric(ctx, nodeID, d.Name, d.State, d.RoutesImported, d.RoutesExported, d.RoutesPreferred, d.UptimeSecs); err != nil {
			continue
		}
		updated++
	}

	adminLog.WithFields(logrus.Fields{"node_id": nodeID, "updated": updated}).Info("BIRD details refresh completed")

	JSON(w, http.StatusOK, map[string]interface{}{
		"protocols": len(payload.Details),
		"updated":   updated,
	})
}

func (h *AdminHandler) RegenerateToken(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	nodeID := chi.URLParam(r, "id")
	adminEmail := middleware.GetEmail(ctx)

	_, err := h.nodes.GetByID(ctx, nodeID)
	if err != nil {
		ErrorJSON(w, r, http.StatusNotFound, "not_found", "Node not found")
		return
	}

	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to generate agent token")
		return
	}
	agentToken := hex.EncodeToString(tokenBytes)

	if err := h.nodes.SetAgentToken(ctx, nodeID, agentToken); err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to update agent token")
		return
	}

	h.auditSvc.Log(ctx, "node.regenerate_token", adminEmail, &nodeID, nil)

	adminLog.WithField("node_id", nodeID).Info("agent token regenerated")

	JSON(w, http.StatusOK, map[string]interface{}{
		"agent_token": agentToken,
		"node_id":     nodeID,
		"message":     "Save this agent token - it will not be shown again",
	})
}

func (h *AdminHandler) ResetPubkey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	nodeID := chi.URLParam(r, "id")
	adminEmail := middleware.GetEmail(ctx)

	_, err := h.nodes.GetByID(ctx, nodeID)
	if err != nil {
		ErrorJSON(w, r, http.StatusNotFound, "not_found", "Node not found")
		return
	}

	if err := h.nodes.ResetAgentPubkey(ctx, nodeID); err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to reset agent pubkey")
		return
	}

	h.auditSvc.Log(ctx, "node.reset_pubkey", adminEmail, &nodeID, nil)
	adminLog.WithField("node_id", nodeID).Info("agent pubkey reset")

	JSON(w, http.StatusOK, map[string]string{"status": "pubkey_cleared"})
}
