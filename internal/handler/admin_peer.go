package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/akaere/autopeer-center/internal/apiversion"
	"github.com/akaere/autopeer-center/internal/middleware"
	"github.com/akaere/autopeer-center/internal/model"
	"github.com/akaere/autopeer-center/internal/repository"
	"github.com/akaere/autopeer-center/internal/service"
	"github.com/akaere/autopeer-center/internal/ws"
	"github.com/go-chi/chi/v5"
	"github.com/sirupsen/logrus"
)

type peerHistoryItem struct {
	ID        string      `json:"id"`
	Action    string      `json:"action"`
	Operator  string      `json:"operator"`
	Detail    interface{} `json:"detail,omitempty"`
	CreatedAt string      `json:"created_at"`
}

type peerAgentSyncResp struct {
	DesiredState            string `json:"desired_state"`
	ConfigurationStatus     string `json:"configuration_status"`
	AgentOnline             bool   `json:"agent_online"`
	NodeAgentState          string `json:"node_agent_state,omitempty"`
	NodeAgentVersion        string `json:"node_agent_version,omitempty"`
	NodeAgentStateChangedAt string `json:"node_agent_state_changed_at,omitempty"`
	CheckedAt               string `json:"checked_at"`
}

type peerImportEntry struct {
	ASN                int64  `json:"asn"`
	RemotePubkey       string `json:"remote_pubkey"`
	RemoteEndpoint     string `json:"remote_endpoint"`
	RemoteLLA          string `json:"remote_lla"`
	WgListenPort       int    `json:"wg_listen_port"`
	WgInterface        string `json:"wg_interface_name"`
	BgpProtoName       string `json:"bgp_proto_name"`
	BirdConfigFilename string `json:"bird_config_filename"`
	WgManaged          bool   `json:"wg_managed"`
}

type missingEmailInfo struct {
	PeerID string `json:"peer_id"`
	ASN    int64  `json:"asn"`
	Reason string `json:"reason"`
}

// peerExportEntry is the portable JSON shape for a single peer used by the
// admin export/import endpoints. NodeName is filled on export as a human
// readable helper and ignored on import (peers are keyed on NodeID + RemoteASN).
type peerExportEntry struct {
	NodeID             string  `json:"node_id"`
	NodeName           string  `json:"node_name,omitempty"`
	RemoteASN          int64   `json:"remote_asn"`
	RemotePubkey       string  `json:"remote_pubkey"`
	RemoteEndpoint     string  `json:"remote_endpoint"`
	RemoteLLA          string  `json:"remote_lla"`
	ContactEmail       string  `json:"contact_email"`
	WgListenPort       int     `json:"wg_listen_port"`
	WgInterfaceName    string  `json:"wg_interface_name"`
	BgpProtoName       string  `json:"bgp_proto_name"`
	BirdConfigFilename string  `json:"bird_config_filename"`
	WgManaged          bool    `json:"wg_managed"`
	MTU                *int    `json:"mtu,omitempty"`
	WgPreSharedKey     *string `json:"wg_preshared_key,omitempty"`
	Status             string  `json:"status"`
}

// peerExportFile is the top-level wrapper produced by ExportPeers and accepted
// by ImportPeers.
type peerExportFile struct {
	Version    int               `json:"version"`
	ExportedAt string            `json:"exported_at"`
	Peers      []peerExportEntry `json:"peers"`
}

const peerExportVersion = 1

// peerImportError describes a single entry that could not be imported.
type peerImportError struct {
	NodeID string `json:"node_id"`
	ASN    int64  `json:"asn"`
	Reason string `json:"reason"`
}

func (h *AdminHandler) GetPeer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	peerID := chi.URLParam(r, "id")

	adminLog.WithField("peer_id", peerID).Debug("GetPeer entry")

	peer, err := h.peers.GetByID(ctx, peerID)
	if err != nil {
		ErrorJSON(w, r, http.StatusNotFound, "peer_not_found", "Peer not found")
		return
	}

	node, err := h.nodes.GetByID(ctx, peer.NodeID)
	if err != nil {
		ErrorJSON(w, r, http.StatusNotFound, "peer_not_found", "Node not found")
		return
	}

	ms, _ := h.metrics.GetLatestPeerMetricSummary(ctx, peerID)
	historyLogs, _ := h.auditSvc.ListByTargetID(ctx, peerID, 50)
	agentOnline := h.hub != nil && h.hub.IsOnline(peer.NodeID)
	agentSync := peerAgentSyncResp{CheckedAt: time.Now().UTC().Format(time.RFC3339)}
	switch {
	case !peer.WgManaged:
		agentSync.DesiredState = "manual"
		agentSync.ConfigurationStatus = "manual"
	case peer.Status == "active":
		agentSync.DesiredState = "configured"
		if agentOnline {
			agentSync.ConfigurationStatus = "expected_configured"
		} else {
			agentSync.ConfigurationStatus = "agent_offline"
		}
	case peer.Status == "suspended":
		agentSync.DesiredState = "absent"
		agentSync.ConfigurationStatus = "removed"
	case peer.Status == "pending":
		agentSync.DesiredState = "absent"
		agentSync.ConfigurationStatus = "pending"
	case peer.Status == "rejected":
		agentSync.DesiredState = "absent"
		agentSync.ConfigurationStatus = "rejected"
	default:
		agentSync.DesiredState = "unknown"
		agentSync.ConfigurationStatus = "unknown"
	}
	if node != nil {
		agentSync.AgentOnline = agentOnline
		agentSync.NodeAgentState = node.AgentState
		agentSync.NodeAgentVersion = node.AgentVersion
		if !node.AgentStateChangedAt.IsZero() {
			agentSync.NodeAgentStateChangedAt = node.AgentStateChangedAt.UTC().Format(time.RFC3339)
		}
	}

	type metricSummary struct {
		LatestRtt              *float64 `json:"latest_rtt"`
		LatestBGPState         *string  `json:"latest_bgp_state"`
		LatestHandshake        *string  `json:"latest_handshake"`
		LatestBGPUptime        *int     `json:"latest_bgp_uptime_secs"`
		LatestWgActualEndpoint *string  `json:"latest_wg_actual_endpoint"`
	}

	var metricResp metricSummary
	if ms != nil {
		metricResp.LatestRtt = ms.RttMs
		metricResp.LatestBGPState = ms.BGPState
		metricResp.LatestBGPUptime = ms.BGPUptimeSecs
		metricResp.LatestWgActualEndpoint = ms.WgActualEndpoint
		if ms.LastHandshake != nil {
			s := ms.LastHandshake.Format(time.RFC3339)
			metricResp.LatestHandshake = &s
		}
	}

	adminLog.WithField("peer_id", peerID).Debug("GetPeer returning result")

	JSONVersioned(w, r, http.StatusOK, apiversion.ResourcePeer, "peer", map[string]interface{}{
		"peer": map[string]interface{}{
			"id":                        peer.ID,
			"node_id":                   peer.NodeID,
			"remote_asn":                peer.RemoteASN,
			"remote_pubkey":             peer.RemotePubkey,
			"remote_endpoint":           peer.RemoteEndpoint,
			"remote_lla":                peer.RemoteLLA,
			"contact_email":             peer.ContactEmail,
			"wg_listen_port":            peer.WgListenPort,
			"wg_interface_name":         peer.WgInterfaceName,
			"wg_managed":                peer.WgManaged,
			"status":                    peer.Status,
			"reject_reason":             peer.RejectReason,
			"endpoint_mismatch_since":   peer.EndpointMismatchSince,
			"bgp_suspended_by_endpoint": peer.BGPSuspendedByEndpoint,
			"created_at":                peer.CreatedAt,
			"updated_at":                peer.UpdatedAt,
			"node_name":                 node.Name,
			"node_location":             node.Location,
			"node_public_ip":            node.PublicIP,
			"node_our_lla":              node.OurLLA,
			"node_our_wg_pubkey":        node.OurWgPubkey,
			"mtu":                       peer.MTU,
			"bgp_proto_name":            peer.BgpProtoName,
			"bird_config_filename":      peer.BirdConfigFilename,
		},
		"metrics":    metricResp,
		"agent_sync": agentSync,
		"history": func() []peerHistoryItem {
			items := make([]peerHistoryItem, 0, len(historyLogs))
			for _, l := range historyLogs {
				items = append(items, peerHistoryItem{
					ID:        l.ID,
					Action:    l.Action,
					Operator:  l.Operator,
					Detail:    l.Detail,
					CreatedAt: l.CreatedAt.UTC().Format(time.RFC3339),
				})
			}
			return items
		}(),
	})
}

func (h *AdminHandler) PeerMetrics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	peerID := chi.URLParam(r, "id")

	hours := 24
	if q := r.URL.Query().Get("hours"); q != "" {
		if v, err := strconv.Atoi(q); err == nil && v > 0 && v <= 720 {
			hours = v
		}
	}

	mms, err := h.metrics.QueryPeerMetrics(ctx, peerID, hours)
	if err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to fetch metrics")
		return
	}

	type metricPoint struct {
		Time            time.Time `json:"time"`
		RxBytes         int64     `json:"rx_bytes"`
		TxBytes         int64     `json:"tx_bytes"`
		RttMs           *float64  `json:"rtt_ms"`
		BGPState        *string   `json:"bgp_state"`
		LastHandshake   *string   `json:"last_handshake"`
		RoutesImported  *int      `json:"routes_imported"`
		RoutesExported  *int      `json:"routes_exported"`
		RoutesPreferred *int      `json:"routes_preferred"`
	}

	points := make([]metricPoint, 0, len(mms))
	for _, m := range mms {
		var bgpState *string
		if m.BGPState != "" {
			bgpState = &m.BGPState
		}
		var hs *string
		if m.LastHandshake != nil {
			s := m.LastHandshake.Format(time.RFC3339)
			hs = &s
		}
		points = append(points, metricPoint{
			Time:            m.Time,
			RxBytes:         m.RxBytes,
			TxBytes:         m.TxBytes,
			RttMs:           m.RttMs,
			BGPState:        bgpState,
			LastHandshake:   hs,
			RoutesImported:  m.RoutesImported,
			RoutesExported:  m.RoutesExported,
			RoutesPreferred: m.RoutesPreferred,
		})
	}

	var latestRtt *float64
	var latestBGPState *string
	var latestHandshake *string
	if len(points) > 0 {
		latestRtt = points[len(points)-1].RttMs
		latestBGPState = points[len(points)-1].BGPState
		latestHandshake = points[len(points)-1].LastHandshake
	}

	JSON(w, http.StatusOK, map[string]any{
		"peer_id":          peerID,
		"latest_rtt":       latestRtt,
		"latest_bgp_state": latestBGPState,
		"latest_handshake": latestHandshake,
		"points":           points,
	})
}

func (h *AdminHandler) ListPeers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	status := r.URL.Query().Get("status")
	asnStr := r.URL.Query().Get("asn")
	nodeID := r.URL.Query().Get("node_id")

	adminLog.WithFields(logrus.Fields{
		"status": status, "asn": asnStr, "node_id": nodeID,
	}).Debug("ListPeers entry")

	page := 1
	perPage := 20
	if p, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && p > 0 {
		page = p
	}
	if pp, err := strconv.Atoi(r.URL.Query().Get("per_page")); err == nil && pp > 0 && pp <= 200 {
		perPage = pp
	}

	params := repository.ListParams{
		Status:   status,
		NodeID:   nodeID,
		Page:     page,
		PageSize: perPage,
	}
	if asn, err := strconv.ParseInt(asnStr, 10, 64); err == nil {
		params.ASN = asn
	}

	results, total, err := h.peers.List(ctx, params)
	if err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to fetch peers")
		return
	}

	type peerResp struct {
		ID                      string     `json:"id"`
		NodeID                  string     `json:"node_id"`
		RemoteASN               int64      `json:"remote_asn"`
		RemotePubkey            string     `json:"remote_pubkey"`
		RemoteEndpoint          string     `json:"remote_endpoint"`
		RemoteLLA               string     `json:"remote_lla"`
		ContactEmail            string     `json:"contact_email"`
		WgListenPort            int        `json:"wg_listen_port"`
		WgInterfaceName         string     `json:"wg_interface_name"`
		WgManaged               bool       `json:"wg_managed"`
		Status                  string     `json:"status"`
		RejectReason            *string    `json:"reject_reason,omitempty"`
		CreatedAt               string     `json:"created_at"`
		UpdatedAt               string     `json:"updated_at"`
		NodeName                string     `json:"node_name"`
		NodeLocation            string     `json:"node_location"`
		MTU                     *int       `json:"mtu,omitempty"`
		LatestRtt               *float64   `json:"latest_rtt,omitempty"`
		LatestBGPState          *string    `json:"latest_bgp_state,omitempty"`
		RxBytes24h              int64      `json:"rx_bytes_24h"`
		TxBytes24h              int64      `json:"tx_bytes_24h"`
		EndpointMismatchSince   *time.Time `json:"endpoint_mismatch_since,omitempty"`
		BGPSuspendedByEndpoint  bool       `json:"bgp_suspended_by_endpoint"`
	}

	peers := make([]peerResp, 0, len(results))
	for _, r := range results {
		peers = append(peers, peerResp{
			ID:                      r.ID,
			NodeID:                  r.NodeID,
			RemoteASN:               r.RemoteASN,
			RemotePubkey:            r.RemotePubkey,
			RemoteEndpoint:          r.RemoteEndpoint,
			RemoteLLA:               r.RemoteLLA,
			ContactEmail:            r.ContactEmail,
			WgListenPort:            r.WgListenPort,
			WgInterfaceName:         r.WgInterfaceName,
			WgManaged:               r.WgManaged,
			Status:                  r.Status,
			RejectReason:            r.RejectReason,
			CreatedAt:               r.CreatedAt.UTC().Format(time.RFC3339),
			UpdatedAt:               r.UpdatedAt.UTC().Format(time.RFC3339),
			NodeName:                r.NodeName,
			NodeLocation:            r.NodeLocation,
			MTU:                     r.MTU,
			LatestRtt:               r.LatestRtt,
			LatestBGPState:          r.LatestBGPState,
			RxBytes24h:              r.RxBytes24h,
			TxBytes24h:              r.TxBytes24h,
			EndpointMismatchSince:   r.EndpointMismatchSince,
			BGPSuspendedByEndpoint:  r.BGPSuspendedByEndpoint,
		})
	}

	adminLog.WithField("count", len(peers)).Debug("ListPeers returning results")

	JSONVersioned(w, r, http.StatusOK, apiversion.ResourcePeer, "peers", map[string]interface{}{
		"peers":    peers,
		"total":    total,
		"page":     page,
		"per_page": perPage,
	})
}

type approvePeerResult struct {
	NodeName        string
	NodeLocation    string
	NodePublicIP    string
	NodeOurLLA      string
	NodeOurWgPubkey string
}

func (h *AdminHandler) approvePeerCore(ctx context.Context, peerID, actor string) (*approvePeerResult, error) {
	peer, err := h.peers.GetByID(ctx, peerID)
	if err != nil {
		return nil, fmt.Errorf("peer not found: %w", err)
	}
	if peer.Status != "pending" {
		return nil, fmt.Errorf("peer not found or not pending")
	}

	adminLog.WithField("peer_id", peerID).Debug("sending peer.add command to agent")
	resp, err := h.hub.SendCommand(peer.NodeID, ws.TypePeerAdd, ws.PeerAddPayload{
		PeerID:         peerID,
		ASN:            peer.RemoteASN,
		RemoteEndpoint: peer.RemoteEndpoint,
		RemoteWgPubkey: peer.RemotePubkey,
		RemoteLLA:      peer.RemoteLLA,
		ListenPort:     peer.WgListenPort,
		WgInterface:    peer.WgInterfaceName,
		MTU:            peer.MTU,
		WgPreSharedKey: peer.WgPreSharedKey,
	})
	if err != nil {
		return nil, fmt.Errorf("agent unreachable: %w", err)
	}
	if resp.Success != nil && !*resp.Success {
		return nil, fmt.Errorf("agent failed to configure peer: %s", resp.Error)
	}

	if err := h.peers.UpdateStatus(ctx, peerID, "active"); err != nil {
		adminLog.WithField("peer_id", peerID).Error("CRITICAL: agent has peer active but DB update failed, attempting rollback")
		rollbackResp, rollbackErr := h.hub.SendCommand(peer.NodeID, ws.TypePeerRemove, ws.PeerRemovePayload{PeerID: peerID, ASN: peer.RemoteASN})
		if rollbackErr != nil || (rollbackResp.Success != nil && !*rollbackResp.Success) {
			adminLog.WithField("peer_id", peerID).Error("CRITICAL: agent has peer active but DB update failed AND rollback failed - manual cleanup required")
			h.auditSvc.Log(ctx, "peer.approve.diverged", actor, &peerID, map[string]interface{}{"asn": peer.RemoteASN, "rollback_err": rollbackErr})
		}
		return nil, fmt.Errorf("failed to update peer status in DB: %w", err)
	}

	node, _ := h.nodes.GetByID(ctx, peer.NodeID)

	adminLog.WithFields(logrus.Fields{"peer_id": peerID, "email": peer.ContactEmail}).Debug("sending approval email")
	go h.email.SendPeerApproved(h.refreshContactEmail(peer.ContactEmail, peer.RemoteASN), map[string]interface{}{
		"asn":             peer.RemoteASN,
		"nodeName":        node.Name,
		"nodeLocation":    node.Location,
		"ourPublicIp":     node.PublicIP,
		"ourWgPubkey":     node.OurWgPubkey,
		"ourLla":          node.OurLLA,
		"listenPort":      peer.WgListenPort,
		"remoteWgPubkey":  peer.RemotePubkey,
		"remoteLla":       peer.RemoteLLA,
		"remoteEndpoint":  peer.RemoteEndpoint,
		"wgInterfaceName": peer.WgInterfaceName,
	})

	h.auditSvc.Log(ctx, "peer.approve", actor, &peerID, map[string]interface{}{"asn": peer.RemoteASN})

	adminLog.WithField("peer_id", peerID).Info("peer approved successfully")

	return &approvePeerResult{
		NodeName:        node.Name,
		NodeLocation:    node.Location,
		NodePublicIP:    node.PublicIP,
		NodeOurLLA:      node.OurLLA,
		NodeOurWgPubkey: node.OurWgPubkey,
	}, nil
}

func (h *AdminHandler) ApprovePeer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	peerID := chi.URLParam(r, "id")
	adminEmail := middleware.GetEmail(ctx)

	adminLog.WithField("peer_id", peerID).Debug("ApprovePeer entry")

	release, busy := acquirePeerLock(ctx, h.locker, "peer:"+peerID)
	if busy {
		ErrorJSON(w, r, http.StatusConflict, "operation_in_progress", "Another operation is in progress for this peer, please retry")
		return
	}
	defer release()

	_, err := h.approvePeerCore(ctx, peerID, adminEmail)
	if err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "approve_failed", err.Error())
		return
	}

	JSON(w, http.StatusOK, map[string]string{"status": "active"})
}

func (h *AdminHandler) RejectPeer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	peerID := chi.URLParam(r, "id")
	adminEmail := middleware.GetEmail(ctx)

	var body struct {
		Reason string `json:"reason"`
	}
	if err := DecodeJSON(r, &body); err != nil || body.Reason == "" {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "Rejection reason is required")
		return
	}

	adminLog.WithFields(logrus.Fields{"peer_id": peerID, "reason": body.Reason}).Debug("RejectPeer entry")

	peer, err := h.peers.GetByID(ctx, peerID)
	if err != nil || peer.Status != "pending" {
		ErrorJSON(w, r, http.StatusNotFound, "peer_not_found", "Peer not found or not in pending status")
		return
	}

	node, _ := h.nodes.GetByID(ctx, peer.NodeID)

	if err := h.peers.UpdateStatus(ctx, peerID, "rejected", repository.WithRejectReason(body.Reason)); err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "failed to update peer status")
		return
	}

	if h.GetSettingValue("peer_rejected_enabled") == "true" {
		go h.email.SendPeerRejected(h.refreshContactEmail(peer.ContactEmail, peer.RemoteASN), peer.RemoteASN, node.Name, body.Reason)
	}

	h.auditSvc.Log(ctx, "peer.reject", adminEmail, &peerID, map[string]interface{}{"asn": peer.RemoteASN, "reason": body.Reason})

	adminLog.WithField("peer_id", peerID).Info("peer rejected")

	JSON(w, http.StatusOK, map[string]string{"status": "rejected"})
}

func (h *AdminHandler) SuspendPeer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	peerID := chi.URLParam(r, "id")
	adminEmail := middleware.GetEmail(ctx)

	adminLog.WithField("peer_id", peerID).Debug("SuspendPeer entry")

	var body struct {
		Reason string `json:"reason"`
	}
	DecodeJSON(r, &body)

	release, busy := acquirePeerLock(ctx, h.locker, "peer:"+peerID)
	if busy {
		ErrorJSON(w, r, http.StatusConflict, "operation_in_progress", "Another operation is in progress for this peer, please retry")
		return
	}
	defer release()

	peer, err := h.peers.GetByID(ctx, peerID)
	if err != nil || peer.Status != "active" {
		ErrorJSON(w, r, http.StatusNotFound, "peer_not_found", "Peer not found or not active")
		return
	}

	node, _ := h.nodes.GetByID(ctx, peer.NodeID)

	adminLog.WithField("peer_id", peerID).Debug("sending peer.remove command to agent for suspension")
	resp, err := h.hub.SendCommand(peer.NodeID, ws.TypePeerRemove, ws.PeerRemovePayload{
		PeerID: peerID,
		ASN:    peer.RemoteASN,
	})
	if err != nil {
		ErrorJSON(w, r, http.StatusServiceUnavailable, "agent_error", "Failed to reach agent: "+err.Error())
		return
	}
	if resp.Success != nil && !*resp.Success {
		ErrorJSON(w, r, http.StatusInternalServerError, "agent_error", "Agent failed to remove peer: "+resp.Error)
		return
	}

	if err := h.peers.UpdateStatus(ctx, peerID, "suspended"); err != nil {
		adminLog.WithField("peer_id", peerID).Error("SuspendPeer: tunnel removed but DB update failed, attempting rollback")
		rollbackResp, rollbackErr := h.hub.SendCommand(peer.NodeID, ws.TypePeerAdd, ws.PeerAddPayload{
			PeerID:         peerID,
			ASN:            peer.RemoteASN,
			RemoteEndpoint: peer.RemoteEndpoint,
			RemoteWgPubkey: peer.RemotePubkey,
			RemoteLLA:      peer.RemoteLLA,
			ListenPort:     peer.WgListenPort,
			WgInterface:    peer.WgInterfaceName,
			MTU:            peer.MTU,
			WgPreSharedKey: peer.WgPreSharedKey,
		})
		if rollbackErr != nil || (rollbackResp.Success != nil && !*rollbackResp.Success) {
			adminLog.WithField("peer_id", peerID).Error("CRITICAL: SuspendPeer tunnel removed, DB update failed AND rollback failed - manual cleanup required")
		}
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "failed to update peer status")
		return
	}

	h.auditSvc.Log(ctx, "peer.suspend", adminEmail, &peerID, map[string]interface{}{"asn": peer.RemoteASN})

	if h.GetSettingValue("peer_suspended_enabled") == "true" {
		go h.email.SendPeerSuspended(h.refreshContactEmail(peer.ContactEmail, peer.RemoteASN), peer.RemoteASN, node.Name, body.Reason)
	}

	adminLog.WithField("peer_id", peerID).Info("peer suspended")

	h.hub.ClearPeerAlerts(peerID)

	JSON(w, http.StatusOK, map[string]string{"status": "suspended"})
}

func (h *AdminHandler) UnsuspendPeer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	peerID := chi.URLParam(r, "id")
	adminEmail := middleware.GetEmail(ctx)

	adminLog.WithField("peer_id", peerID).Debug("UnsuspendPeer entry")

	release, busy := acquirePeerLock(ctx, h.locker, "peer:"+peerID)
	if busy {
		ErrorJSON(w, r, http.StatusConflict, "operation_in_progress", "Another operation is in progress for this peer, please retry")
		return
	}
	defer release()

	peer, err := h.peers.GetByID(ctx, peerID)
	if err != nil || peer.Status != "suspended" {
		ErrorJSON(w, r, http.StatusNotFound, "peer_not_found", "Peer not found or not suspended")
		return
	}

	node, _ := h.nodes.GetByID(ctx, peer.NodeID)

	adminLog.WithField("peer_id", peerID).Debug("sending peer.add command to agent for unsuspension")
	resp, err := h.hub.SendCommand(peer.NodeID, ws.TypePeerAdd, ws.PeerAddPayload{
		PeerID:         peerID,
		ASN:            peer.RemoteASN,
		RemoteEndpoint: peer.RemoteEndpoint,
		RemoteWgPubkey: peer.RemotePubkey,
		RemoteLLA:      peer.RemoteLLA,
		ListenPort:     peer.WgListenPort,
		WgInterface:    peer.WgInterfaceName,
		MTU:            peer.MTU,
		WgPreSharedKey: peer.WgPreSharedKey,
	})
	if err != nil {
		ErrorJSON(w, r, http.StatusServiceUnavailable, "agent_error", "Failed to reach agent: "+err.Error())
		return
	}
	if resp.Success != nil && !*resp.Success {
		ErrorJSON(w, r, http.StatusInternalServerError, "agent_error", "Agent failed to add peer: "+resp.Error)
		return
	}

	if err := h.peers.UpdateStatus(ctx, peerID, "active"); err != nil {
		adminLog.WithField("peer_id", peerID).Error("UnsuspendPeer: agent has peer active but DB update failed, attempting rollback")
		rollbackResp, rollbackErr := h.hub.SendCommand(peer.NodeID, ws.TypePeerRemove, ws.PeerRemovePayload{
			PeerID: peerID,
			ASN:    peer.RemoteASN,
		})
		if rollbackErr != nil || (rollbackResp.Success != nil && !*rollbackResp.Success) {
			adminLog.WithField("peer_id", peerID).Error("CRITICAL: UnsuspendPeer agent has peer active, DB update failed AND rollback failed - manual cleanup required")
		}
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "failed to update peer status")
		return
	}

	h.auditSvc.Log(ctx, "peer.unsuspend", adminEmail, &peerID, map[string]interface{}{"asn": peer.RemoteASN})

	if h.GetSettingValue("peer_unsuspended_enabled") == "true" {
		go h.email.SendPeerUnsuspended(h.refreshContactEmail(peer.ContactEmail, peer.RemoteASN), peer.RemoteASN, node.Name)
	}

	adminLog.WithField("peer_id", peerID).Info("peer unsuspended")

	JSON(w, http.StatusOK, map[string]string{"status": "active"})
}

func (h *AdminHandler) DeletePeer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	peerID := chi.URLParam(r, "id")
	adminEmail := middleware.GetEmail(ctx)

	release, busy := acquirePeerLock(ctx, h.locker, "peer:"+peerID)
	if busy {
		ErrorJSON(w, r, http.StatusConflict, "operation_in_progress", "Another operation is in progress for this peer, please retry")
		return
	}
	defer release()

	peer, err := h.peers.GetByID(ctx, peerID)
	if err != nil {
		ErrorJSON(w, r, http.StatusNotFound, "peer_not_found", "Peer not found")
		return
	}

	node, _ := h.nodes.GetByID(ctx, peer.NodeID)

	adminLog.WithFields(logrus.Fields{"peer_id": peerID, "status": peer.Status}).Debug("DeletePeer entry")

	if peer.Status == "active" {
		adminLog.WithField("peer_id", peerID).Debug("sending peer.remove command to agent for deletion")
		resp, cmdErr := h.hub.SendCommand(peer.NodeID, ws.TypePeerRemove, ws.PeerRemovePayload{
			PeerID: peerID, ASN: peer.RemoteASN,
		})
		if cmdErr != nil {
			ErrorJSON(w, r, http.StatusServiceUnavailable, "agent_error", "Failed to reach agent: "+cmdErr.Error())
			return
		}
		if resp.Success != nil && !*resp.Success {
			ErrorJSON(w, r, http.StatusInternalServerError, "agent_error", "Agent failed to remove peer: "+resp.Error)
			return
		}
	}

	if err := h.peers.Delete(ctx, peerID); err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to delete peer")
		return
	}

	h.auditSvc.Log(ctx, "peer.admin_delete", adminEmail, &peerID, map[string]interface{}{"asn": peer.RemoteASN})

	nodeName := ""
	if node != nil {
		nodeName = node.Name
	}
	if h.GetSettingValue("peer_deleted_enabled") == "true" && peer.ContactEmail != "" {
		go h.email.SendPeerDeleted(peer.ContactEmail, peer.RemoteASN, nodeName)
	}

	go h.hub.NotifyPeerBot(peer.RemoteASN, service.NotificationPeerDeleted, "peer.deleted", map[string]interface{}{
		"asn":      peer.RemoteASN,
		"nodeName": nodeName,
		"peerId":   peerID,
	})

	adminLog.WithField("peer_id", peerID).Info("peer deleted")

	h.hub.ClearPeerAlerts(peerID)

	JSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *AdminHandler) ImportNodePeers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	nodeID := chi.URLParam(r, "id")
	adminEmail := middleware.GetEmail(ctx)

	adminLog.WithField("node_id", nodeID).Debug("ImportNodePeers entry")

	resp, err := h.hub.SendCommand(nodeID, ws.TypePeersImport, nil)
	if err != nil {
		ErrorJSON(w, r, http.StatusServiceUnavailable, "agent_error", "Failed to reach agent: "+err.Error())
		return
	}
	if resp.Success != nil && !*resp.Success {
		ErrorJSON(w, r, http.StatusInternalServerError, "agent_error", "Agent scan failed: "+resp.Error)
		return
	}

	adminLog.WithFields(logrus.Fields{"node_id": nodeID, "success": resp.Success}).Debug("agent import command response")

	var payload struct {
		Peers []peerImportEntry `json:"peers"`
	}
	if resp.Payload != nil {
		if data, err := json.Marshal(resp.Payload); err == nil {
			json.Unmarshal(data, &payload)
		}
	}

	inserted := 0
	skipped := 0
	dbErrors := 0
	var missingEmails []missingEmailInfo

	for _, p := range payload.Peers {
		email, lookupErr := h.registry.LookupASNEmail(p.ASN)
		if lookupErr != nil {
			adminLog.WithError(lookupErr).WithField("asn", p.ASN).Warn("registry email lookup failed during import")
			email = ""
		}

		wgManaged := p.WgManaged || (p.BgpProtoName == "" && p.BirdConfigFilename == "")
		peerID, err := h.peers.ImportPeer(ctx, &model.Peer{
			NodeID:             nodeID,
			RemoteASN:          p.ASN,
			RemotePubkey:       p.RemotePubkey,
			RemoteEndpoint:     p.RemoteEndpoint,
			RemoteLLA:          p.RemoteLLA,
			ContactEmail:       email,
			WgListenPort:       p.WgListenPort,
			WgInterfaceName:    p.WgInterface,
			BgpProtoName:       p.BgpProtoName,
			BirdConfigFilename: p.BirdConfigFilename,
			WgManaged:          wgManaged,
		})
		if err != nil {
			dbErrors++
			adminLog.WithError(err).WithField("asn", p.ASN).Error("peer import DB insert error")
			continue
		}
		if peerID == "" {
			skipped++
			continue
		}

		inserted++
		h.auditSvc.Log(ctx, "peer.import_peer", adminEmail, &peerID, map[string]interface{}{
			"asn":        p.ASN,
			"node_id":    nodeID,
			"wg_managed": wgManaged,
		})
		if email == "" && lookupErr != nil {
			missingEmails = append(missingEmails, missingEmailInfo{
				PeerID: peerID,
				ASN:    p.ASN,
				Reason: lookupErr.Error(),
			})
		}
	}

	h.auditSvc.Log(ctx, "peer.import", adminEmail, &nodeID, map[string]interface{}{
		"inserted":      inserted,
		"skipped":       skipped,
		"db_errors":     dbErrors,
		"total":         len(payload.Peers),
		"missing_email": len(missingEmails),
	})

	adminLog.WithFields(logrus.Fields{
		"node_id":       nodeID,
		"inserted":      inserted,
		"skipped":       skipped,
		"db_errors":     dbErrors,
		"missing_email": len(missingEmails),
	}).Info("peer import completed")

	JSON(w, http.StatusOK, map[string]interface{}{
		"status":        "completed",
		"inserted":      inserted,
		"skipped":       skipped,
		"db_errors":     dbErrors,
		"total":         len(payload.Peers),
		"missing_email": missingEmails,
	})
}

// ExportPeers dumps all peers as a JSON file for backup/migration. The output
// round-trips back through ImportPeers.
func (h *AdminHandler) ExportPeers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	adminEmail := middleware.GetEmail(ctx)

	peers, err := h.peers.ListAll(ctx)
	if err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to load peers")
		return
	}

	nodeNames := make(map[string]string)
	if nodes, err := h.nodes.List(ctx); err == nil {
		for _, n := range nodes {
			nodeNames[n.ID] = n.Name
		}
	}

	entries := make([]peerExportEntry, 0, len(peers))
	for _, p := range peers {
		entries = append(entries, peerExportEntry{
			NodeID:             p.NodeID,
			NodeName:           nodeNames[p.NodeID],
			RemoteASN:          p.RemoteASN,
			RemotePubkey:       p.RemotePubkey,
			RemoteEndpoint:     p.RemoteEndpoint,
			RemoteLLA:          p.RemoteLLA,
			ContactEmail:       p.ContactEmail,
			WgListenPort:       p.WgListenPort,
			WgInterfaceName:    p.WgInterfaceName,
			BgpProtoName:       p.BgpProtoName,
			BirdConfigFilename: p.BirdConfigFilename,
			WgManaged:          p.WgManaged,
			MTU:                p.MTU,
			WgPreSharedKey:     p.WgPreSharedKey,
			Status:             p.Status,
		})
	}

	file := peerExportFile{
		Version:    peerExportVersion,
		ExportedAt: time.Now().UTC().Format(time.RFC3339),
		Peers:      entries,
	}

	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to encode export")
		return
	}

	h.auditSvc.Log(ctx, "peer.export", adminEmail, nil, map[string]interface{}{
		"count": len(entries),
	})

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="peers-export.json"`)
	w.Write(data)
}

// ImportPeers inserts (or, when overwrite=true, upserts) peers from a JSON dump
// produced by ExportPeers. It writes DB rows only; it does not push config to
// agents. Invalid entries (missing node_id, bad ASN, unknown node) are reported
// in the response errors list rather than failing the whole import.
func (h *AdminHandler) ImportPeers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	adminEmail := middleware.GetEmail(ctx)
	overwrite := r.URL.Query().Get("overwrite") == "true"

	body, err := io.ReadAll(r.Body)
	r.Body.Close()
	if err != nil {
		ErrorJSON(w, r, http.StatusBadRequest, "invalid_request", "Failed to read request body")
		return
	}

	var entries []peerExportEntry
	var file peerExportFile
	if err := json.Unmarshal(body, &file); err == nil && file.Peers != nil {
		entries = file.Peers
	} else if err := json.Unmarshal(body, &entries); err != nil {
		ErrorJSON(w, r, http.StatusBadRequest, "invalid_json", "Body must be a peer export object or a JSON array of peers")
		return
	}

	imported := 0
	overwritten := 0
	skipped := 0
	importErrors := make([]peerImportError, 0)
	nodeExists := make(map[string]bool)

	for _, e := range entries {
		if e.NodeID == "" {
			importErrors = append(importErrors, peerImportError{ASN: e.RemoteASN, Reason: "missing node_id"})
			continue
		}
		if e.RemoteASN <= 0 {
			importErrors = append(importErrors, peerImportError{NodeID: e.NodeID, ASN: e.RemoteASN, Reason: "invalid remote_asn"})
			continue
		}

		ok, checked := nodeExists[e.NodeID]
		if !checked {
			if _, err := h.nodes.GetByID(ctx, e.NodeID); err != nil {
				ok = false
			} else {
				ok = true
			}
			nodeExists[e.NodeID] = ok
		}
		if !ok {
			importErrors = append(importErrors, peerImportError{NodeID: e.NodeID, ASN: e.RemoteASN, Reason: "node not found"})
			continue
		}

		peer := &model.Peer{
			NodeID:             e.NodeID,
			RemoteASN:          e.RemoteASN,
			RemotePubkey:       e.RemotePubkey,
			RemoteEndpoint:     e.RemoteEndpoint,
			RemoteLLA:          e.RemoteLLA,
			ContactEmail:       e.ContactEmail,
			WgListenPort:       e.WgListenPort,
			WgInterfaceName:    e.WgInterfaceName,
			BgpProtoName:       e.BgpProtoName,
			BirdConfigFilename: e.BirdConfigFilename,
			WgManaged:          e.WgManaged,
			MTU:                e.MTU,
			WgPreSharedKey:     e.WgPreSharedKey,
			Status:             e.Status,
		}

		action, err := h.peers.ImportPeerFull(ctx, peer, overwrite)
		if err != nil {
			adminLog.WithError(err).WithFields(logrus.Fields{"asn": e.RemoteASN, "node_id": e.NodeID}).Error("peer json import DB error")
			importErrors = append(importErrors, peerImportError{NodeID: e.NodeID, ASN: e.RemoteASN, Reason: "db error: " + err.Error()})
			continue
		}

		switch action {
		case "imported":
			imported++
			h.auditSvc.Log(ctx, "peer.import_peer", adminEmail, &peer.ID, map[string]interface{}{
				"asn":     e.RemoteASN,
				"node_id": e.NodeID,
				"source":  "json",
			})
		case "overwritten":
			overwritten++
			h.auditSvc.Log(ctx, "peer.import_peer", adminEmail, &peer.ID, map[string]interface{}{
				"asn":       e.RemoteASN,
				"node_id":   e.NodeID,
				"source":    "json",
				"overwrite": true,
			})
		case "skipped":
			skipped++
		}
	}

	h.auditSvc.Log(ctx, "peer.import_json", adminEmail, nil, map[string]interface{}{
		"imported":    imported,
		"overwritten": overwritten,
		"skipped":     skipped,
		"errors":      len(importErrors),
		"total":       len(entries),
		"overwrite":   overwrite,
	})

	adminLog.WithFields(logrus.Fields{
		"imported":    imported,
		"overwritten": overwritten,
		"skipped":     skipped,
		"errors":      len(importErrors),
		"total":       len(entries),
	}).Info("peer json import completed")

	JSON(w, http.StatusOK, map[string]interface{}{
		"status":      "completed",
		"imported":    imported,
		"overwritten": overwritten,
		"skipped":     skipped,
		"total":       len(entries),
		"errors":      importErrors,
	})
}

func (h *AdminHandler) UpdateContactEmail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	peerID := chi.URLParam(r, "id")
	adminEmail := middleware.GetEmail(ctx)

	var body struct {
		Email string `json:"email"`
		Retry bool   `json:"retry"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "Invalid JSON body")
		return
	}
	if body.Email == "" && !body.Retry {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "Provide email or set retry=true")
		return
	}

	peer, err := h.peers.GetByID(ctx, peerID)
	if err != nil {
		ErrorJSON(w, r, http.StatusNotFound, "peer_not_found", "Peer not found")
		return
	}

	source := "manual"
	newEmail := body.Email

	if body.Retry {
		fresh, err := h.registry.LookupASNEmail(peer.RemoteASN)
		if err != nil {
			ErrorJSON(w, r, http.StatusBadRequest, "registry_error", "Registry lookup failed: "+err.Error())
			return
		}
		newEmail = fresh
		source = "registry"
	}

	if err := h.peers.UpdateContactEmail(ctx, peerID, newEmail); err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to update contact email")
		return
	}

	h.auditSvc.Log(ctx, "peer.email_updated", adminEmail, &peerID, map[string]interface{}{
		"asn":    peer.RemoteASN,
		"source": source,
	})

	adminLog.WithFields(logrus.Fields{"peer_id": peerID, "asn": peer.RemoteASN, "source": source}).Info("contact email updated")

	JSON(w, http.StatusOK, map[string]string{"email": newEmail})
}

func (h *AdminHandler) UpdatePeer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	peerID := chi.URLParam(r, "id")
	adminEmail := middleware.GetEmail(ctx)

	release, busy := acquirePeerLock(ctx, h.locker, "peer:"+peerID)
	if busy {
		ErrorJSON(w, r, http.StatusConflict, "operation_in_progress", "Another operation is in progress for this peer, please retry")
		return
	}
	defer release()

	body, err := decodePeerUpdateJSON(r)
	if err != nil {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "Invalid JSON body")
		return
	}

	if body.RemotePubkey == nil && body.RemoteEndpoint == nil && body.RemoteLLA == nil && !body.MTUSet {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "No fields to update")
		return
	}

	if body.RemotePubkey != nil && (!base64Re.MatchString(*body.RemotePubkey) || len(*body.RemotePubkey) < 40) {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "Invalid WireGuard public key (must be Base64)")
		return
	}
	if body.RemoteEndpoint != nil && !validateEndpoint(*body.RemoteEndpoint) {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "Invalid endpoint (format: IP:Port or [IPv6]:Port)")
		return
	}
	if body.RemoteLLA != nil && !validateLLA(*body.RemoteLLA) {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "Invalid link-local address (must be fe80::/10)")
		return
	}
	if body.MTUSet && body.MTU != nil && (*body.MTU < 576 || *body.MTU > 9000) {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "MTU must be between 576 and 9000")
		return
	}

	peer, err := h.peers.GetByID(ctx, peerID)
	if err != nil {
		ErrorJSON(w, r, http.StatusNotFound, "peer_not_found", "Peer not found")
		return
	}

	node, _ := h.nodes.GetByID(ctx, peer.NodeID)

	newPubkey := peer.RemotePubkey
	newEndpoint := peer.RemoteEndpoint
	newLLA := peer.RemoteLLA
	newMTU := peer.MTU
	if body.RemotePubkey != nil {
		newPubkey = *body.RemotePubkey
	}
	if body.RemoteEndpoint != nil {
		newEndpoint = *body.RemoteEndpoint
	}
	if body.RemoteLLA != nil {
		newLLA = *body.RemoteLLA
	}
	if body.MTUSet {
		newMTU = body.MTU
	}

	if peer.Status == "active" {
		removeResp, removeErr := h.hub.SendCommand(peer.NodeID, ws.TypePeerRemove, ws.PeerRemovePayload{
			PeerID: peerID,
			ASN:    peer.RemoteASN,
		})
		if removeErr != nil {
			adminLog.WithError(removeErr).WithField("peer_id", peerID).Error("UpdatePeer: peer.remove failed")
			ErrorJSON(w, r, http.StatusServiceUnavailable, "agent_error", "Failed to remove old peer config: "+removeErr.Error())
			return
		}
		if removeResp.Success != nil && !*removeResp.Success {
			adminLog.WithField("peer_id", peerID).Error("UpdatePeer: peer.remove command rejected by agent")
			ErrorJSON(w, r, http.StatusInternalServerError, "agent_error", "Agent rejected peer removal: "+removeResp.Error)
			return
		}

		addResp, addErr := h.hub.SendCommand(peer.NodeID, ws.TypePeerAdd, ws.PeerAddPayload{
			PeerID:         peerID,
			ASN:            peer.RemoteASN,
			RemoteEndpoint: newEndpoint,
			RemoteWgPubkey: newPubkey,
			RemoteLLA:      newLLA,
			ListenPort:     peer.WgListenPort,
			WgInterface:    peer.WgInterfaceName,
			MTU:            newMTU,
			WgPreSharedKey: peer.WgPreSharedKey,
		})
		if addErr != nil {
			adminLog.WithError(addErr).WithField("peer_id", peerID).Error("UpdatePeer: peer.add failed after remove, rolling back")
			h.hub.SendCommand(peer.NodeID, ws.TypePeerAdd, ws.PeerAddPayload{
				PeerID:         peerID,
				ASN:            peer.RemoteASN,
				RemoteEndpoint: peer.RemoteEndpoint,
				RemoteWgPubkey: peer.RemotePubkey,
				RemoteLLA:      peer.RemoteLLA,
				ListenPort:     peer.WgListenPort,
				WgInterface:    peer.WgInterfaceName,
				MTU:            peer.MTU,
				WgPreSharedKey: peer.WgPreSharedKey,
			})
			ErrorJSON(w, r, http.StatusInternalServerError, "agent_error", "Failed to re-add peer (rollback attempted): "+addErr.Error())
			return
		}
		if addResp.Success != nil && !*addResp.Success {
			adminLog.WithField("peer_id", peerID).Error("UpdatePeer: peer.add rejected after remove, rolling back")
			h.hub.SendCommand(peer.NodeID, ws.TypePeerAdd, ws.PeerAddPayload{
				PeerID:         peerID,
				ASN:            peer.RemoteASN,
				RemoteEndpoint: peer.RemoteEndpoint,
				RemoteWgPubkey: peer.RemotePubkey,
				RemoteLLA:      peer.RemoteLLA,
				ListenPort:     peer.WgListenPort,
				WgInterface:    peer.WgInterfaceName,
				MTU:            peer.MTU,
				WgPreSharedKey: peer.WgPreSharedKey,
			})
			ErrorJSON(w, r, http.StatusInternalServerError, "agent_error", "Agent rejected peer re-add (rollback attempted): "+addResp.Error)
			return
		}
	}

	fields := map[string]interface{}{
		"remote_pubkey":   newPubkey,
		"remote_endpoint": newEndpoint,
		"remote_lla":      newLLA,
		"mtu":             newMTU,
		"updated_at":      "now()",
	}
	if err := h.peers.UpdateFields(ctx, peerID, fields); err != nil {
		adminLog.WithError(err).WithField("peer_id", peerID).Error("UpdatePeer: DB update failed after agent commands")
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Agent commands succeeded but DB update failed")
		return
	}

	changes := map[string]interface{}{}
	if body.RemotePubkey != nil {
		changes["remote_pubkey"] = "updated"
	}
	if body.RemoteEndpoint != nil {
		changes["remote_endpoint"] = "updated"
	}
	if body.RemoteLLA != nil {
		changes["remote_lla"] = "updated"
	}
	if body.MTUSet {
		changes["old_mtu"] = peer.MTU
		changes["new_mtu"] = body.MTU
	}
	changes["asn"] = peer.RemoteASN

	h.auditSvc.Log(ctx, "peer.update", adminEmail, &peerID, changes)

	adminLog.WithFields(logrus.Fields{"peer_id": peerID, "changes": changes}).Info("peer updated")

	_ = node

	JSON(w, http.StatusOK, map[string]string{"status": "updated"})
}
