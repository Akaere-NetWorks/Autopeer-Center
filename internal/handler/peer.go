package handler

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/akaere/autopeer-center/internal/apiversion"
	"github.com/akaere/autopeer-center/internal/lock"
	"github.com/akaere/autopeer-center/internal/middleware"
	"github.com/akaere/autopeer-center/internal/model"
	"github.com/akaere/autopeer-center/internal/peering"
	"github.com/akaere/autopeer-center/internal/repository"
	"github.com/akaere/autopeer-center/internal/service"
	"github.com/akaere/autopeer-center/internal/ws"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/sirupsen/logrus"
)

var peerLog = logrus.WithField("pkg", "handler.peer")

type PeerHandler struct {
	peers    repository.PeerRepository
	nodes    repository.NodeRepository
	metrics  repository.MetricsRepository
	registry *service.RegistryService
	email    *service.EmailService
	audit    *service.AuditService
	hub      *ws.Hub
	admin    *AdminHandler
	locker   lock.Locker
}

func NewPeerHandler(peers repository.PeerRepository, nodes repository.NodeRepository, metrics repository.MetricsRepository, registry *service.RegistryService, email *service.EmailService, audit *service.AuditService, hub *ws.Hub, locker lock.Locker) *PeerHandler {
	return &PeerHandler{
		peers:    peers,
		nodes:    nodes,
		metrics:  metrics,
		registry: registry,
		email:    email,
		audit:    audit,
		hub:      hub,
		locker:   locker,
	}
}

func (h *PeerHandler) SetAdminHandler(a *AdminHandler) {
	h.admin = a
}

var (
	base64Re   = regexp.MustCompile(`^[A-Za-z0-9+/]+=*$`)
	hostnameRe = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?)*$`)
)

func validateEndpoint(ep string) bool {
	hostStr, portStr, err := net.SplitHostPort(ep)
	if err != nil {
		return false
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return false
	}
	if net.ParseIP(hostStr) != nil {
		return true
	}
	return hostnameRe.MatchString(hostStr)
}

func validateLLA(lla string) bool {
	ip := net.ParseIP(lla)
	if ip == nil {
		return false
	}
	return ip.IsLinkLocalUnicast()
}

type createPeerReq struct {
	NodeID         string `json:"node_id"`
	RemotePubkey   string `json:"remote_pubkey"`
	RemoteEndpoint string `json:"remote_endpoint"`
	RemoteLLA      string `json:"remote_lla"`
	MTU            *int   `json:"mtu"`
	EnablePSK      bool   `json:"enable_psk"`
}

func (h *PeerHandler) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	asn := middleware.GetASN(ctx)
	peerLog.WithField("asn", asn).Debug("list peers request")

	results, err := h.peers.ListByASN(ctx, asn)
	if err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to fetch peers")
		return
	}

	type peerResp struct {
		ID                     string     `json:"id"`
		NodeID                 string     `json:"node_id"`
		RemoteASN              int64      `json:"remote_asn"`
		RemotePubkey           string     `json:"remote_pubkey"`
		RemoteEndpoint         string     `json:"remote_endpoint"`
		RemoteLLA              string     `json:"remote_lla"`
		ContactEmail           string     `json:"contact_email"`
		WgListenPort           int        `json:"wg_listen_port"`
		WgInterfaceName        string     `json:"wg_interface_name"`
		WgManaged              bool       `json:"wg_managed"`
		Status                 string     `json:"status"`
		RejectReason           *string    `json:"reject_reason,omitempty"`
		EndpointMismatchSince  *time.Time `json:"endpoint_mismatch_since,omitempty"`
		BGPSuspendedByEndpoint bool       `json:"bgp_suspended_by_endpoint"`
		CreatedAt              time.Time  `json:"created_at"`
		UpdatedAt              time.Time  `json:"updated_at"`
		NodeName               string     `json:"node_name"`
		NodeLocation           string     `json:"node_location"`
		MTU                    *int       `json:"mtu,omitempty"`
		WgPreSharedKey         *string    `json:"wg_preshared_key,omitempty"`
	}

	peers := make([]peerResp, 0, len(results))
	for _, r := range results {
		p := r.Peer()
		peers = append(peers, peerResp{
			ID:                     p.ID,
			NodeID:                 p.NodeID,
			RemoteASN:              p.RemoteASN,
			RemotePubkey:           p.RemotePubkey,
			RemoteEndpoint:         p.RemoteEndpoint,
			RemoteLLA:              p.RemoteLLA,
			ContactEmail:           p.ContactEmail,
			WgListenPort:           p.WgListenPort,
			WgInterfaceName:        p.WgInterfaceName,
			WgManaged:              p.WgManaged,
			Status:                 p.Status,
			RejectReason:           p.RejectReason,
			EndpointMismatchSince:  p.EndpointMismatchSince,
			BGPSuspendedByEndpoint: p.BGPSuspendedByEndpoint,
			CreatedAt:              p.CreatedAt,
			UpdatedAt:              p.UpdatedAt,
			NodeName:               r.NodeName,
			NodeLocation:           r.NodeLocation,
			MTU:                    p.MTU,
			WgPreSharedKey:         p.WgPreSharedKey,
		})
	}

	peerLog.WithFields(logrus.Fields{
		"asn":          asn,
		"result_count": len(peers),
	}).Debug("list peers result")

	JSONVersioned(w, r, http.StatusOK, apiversion.ResourcePeer, "", peers)
}

func (h *PeerHandler) Create(w http.ResponseWriter, r *http.Request) {
	if h.admin != nil && h.admin.GetSiteSettingValue("peer_creation_enabled") != "true" {
		ErrorJSON(w, r, http.StatusForbidden, "peer_creation_disabled", "Peer creation is currently disabled")
		return
	}

	ctx := r.Context()
	asn := middleware.GetASN(ctx)

	var req createPeerReq
	if err := DecodeJSON(r, &req); err != nil {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "Invalid JSON body")
		return
	}

	truncKey := req.RemotePubkey
	if len(truncKey) > 12 {
		truncKey = truncKey[:12] + "..."
	}
	peerLog.WithFields(logrus.Fields{
		"asn":      asn,
		"node_id":  req.NodeID,
		"pubkey":   truncKey,
		"endpoint": req.RemoteEndpoint,
		"lla":      req.RemoteLLA,
	}).Debug("create peer request")

	if req.NodeID == "" {
		peerLog.Debug("validation failed: node_id is required")
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "node_id is required")
		return
	}
	if !base64Re.MatchString(req.RemotePubkey) || len(req.RemotePubkey) < 40 {
		peerLog.Debug("validation failed: invalid remote_pubkey")
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "Invalid WireGuard public key (must be Base64)")
		return
	}
	if !validateEndpoint(req.RemoteEndpoint) {
		peerLog.Debug("validation failed: invalid remote_endpoint")
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "Invalid endpoint (format: IP:Port or [IPv6]:Port)")
		return
	}
	if !validateLLA(req.RemoteLLA) {
		peerLog.Debug("validation failed: invalid remote_lla")
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "Invalid link-local address (must be fe80::/10)")
		return
	}
	if req.MTU != nil && (*req.MTU < 576 || *req.MTU > 9000) {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "MTU must be between 576 and 9000")
		return
	}

	node, err := h.nodes.GetByID(ctx, req.NodeID)
	if err != nil || !node.Enabled {
		peerLog.WithField("node_id", req.NodeID).Debug("node not found or disabled")
		ErrorJSON(w, r, http.StatusBadRequest, "invalid_node", "Node not found or disabled")
		return
	}
	peerLog.WithFields(logrus.Fields{
		"node_id":   req.NodeID,
		"node_name": node.Name,
	}).Debug("node lookup succeeded")

	// Serialize concurrent creates for the same (node, ASN) so two requests
	// can't both pass the existence/port checks and race on insert.
	release, busy := acquirePeerLock(ctx, h.locker, fmt.Sprintf("peer-create:%s:%d", req.NodeID, asn))
	if busy {
		ErrorJSON(w, r, http.StatusConflict, "operation_in_progress", "Another peer operation is in progress, please retry")
		return
	}
	defer release()

	exists, _ := h.peers.ExistsByNodeAndASN(ctx, req.NodeID, asn)
	if exists {
		peerLog.WithFields(logrus.Fields{"node_id": req.NodeID, "asn": asn}).Debug("peer already exists")
		ErrorJSON(w, r, http.StatusConflict, "peer_exists", "You already have a peer on this node")
		return
	}
	peerLog.Debug("uniqueness check passed")

	email, err := h.registry.LookupASNEmail(asn)
	if err != nil {
		peerLog.WithField("asn", asn).Warn("registry email lookup failed, proceeding with empty contact email")
		email = ""
	}
	maskedEmail := email
	if len(maskedEmail) > 4 {
		maskedEmail = "****" + maskedEmail[len(maskedEmail)-4:]
	}
	peerLog.WithField("email", maskedEmail).Debug("registry email lookup succeeded")

	wgPort := peering.WgPort(asn, node.WgPortPrefix)
	wgIfName := peering.WgInterfaceName(asn)
	peerLog.WithFields(logrus.Fields{
		"wg_port":    wgPort,
		"wg_if_name": wgIfName,
	}).Debug("calculated wgPort and wgIfName")

	pendingCount, _ := h.peers.CountPendingByASN(ctx, asn)
	if pendingCount >= 10 {
		ErrorJSON(w, r, http.StatusTooManyRequests, "too_many_pending", "You have too many pending peer requests")
		return
	}

	portExists, _ := h.peers.ExistsByNodeAndPort(ctx, req.NodeID, wgPort)
	if portExists {
		ErrorJSON(w, r, http.StatusConflict, "port_conflict",
			fmt.Sprintf("WireGuard port %d is already used on this node; your ASN shares a port suffix with an existing peer. Please contact the administrator.", wgPort))
		return
	}

	var psk *string
	if req.EnablePSK {
		key, genErr := peering.GeneratePSK()
		if genErr != nil {
			peerLog.WithError(genErr).Error("failed to generate preshared key")
			ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to generate pre-shared key")
			return
		}
		psk = &key
	}

	now := time.Now().UTC()
	peer := &model.Peer{
		NodeID:          req.NodeID,
		RemoteASN:       asn,
		RemotePubkey:    req.RemotePubkey,
		RemoteEndpoint:  req.RemoteEndpoint,
		RemoteLLA:       req.RemoteLLA,
		ContactEmail:    email,
		WgListenPort:    wgPort,
		WgInterfaceName: wgIfName,
		WgManaged:       true,
		MTU:             req.MTU,
		WgPreSharedKey:  psk,
		Status:          "pending",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	err = h.peers.Create(ctx, peer)
	if err != nil {
		peerLog.WithError(err).Debug("DB insert failed")
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && strings.Contains(pgErr.ConstraintName, "port") {
			ErrorJSON(w, r, http.StatusConflict, "port_conflict", "computed WireGuard port conflicts with an existing peer on this node")
			return
		}
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to create peer")
		return
	}
	peerID := peer.ID
	peerLog.WithField("peer_id", peerID).Debug("peer inserted into database")

	operator := fmt.Sprintf("AS%d", asn)
	h.audit.Log(ctx, "peer.create", operator, &peerID, map[string]any{
		"node_id": req.NodeID,
		"asn":     asn,
	})

	autoApprove := h.admin != nil && h.admin.GetSiteSettingValue("auto_approve_peers") == "true"
	if autoApprove {
		peerLog.WithField("peer_id", peerID).Info("auto-approve enabled, attempting automatic approval")
		_, approveErr := h.admin.approvePeerCore(ctx, peerID, "system:auto-approve")
		if approveErr != nil {
			peerLog.WithError(approveErr).WithField("peer_id", peerID).Warn("auto-approve failed, peer remains pending")
			go h.email.SendPeerSubmitted(email, asn, node.Name, node.Location, node.OurWgPubkey, node.OurLLA, node.PublicIP, time.Now().UTC().Format(time.RFC3339))
			peerLog.Debug("fallback notification email send initiated")
		} else {
			JSON(w, http.StatusCreated, map[string]any{
				"id":                peerID,
				"status":            "active",
				"wg_listen_port":    wgPort,
				"wg_interface_name": wgIfName,
				"mtu":               req.MTU,
				"wg_preshared_key":  psk,
			})
			return
		}
	} else {
		go h.email.SendPeerSubmitted(email, asn, node.Name, node.Location, node.OurWgPubkey, node.OurLLA, node.PublicIP, time.Now().UTC().Format(time.RFC3339))
		peerLog.Debug("notification email send initiated")
	}

	go h.hub.NotifyPeerBot(asn, service.NotificationPeerSubmitted, "peer.submitted", map[string]interface{}{
		"asn":      asn,
		"nodeName": node.Name,
	})

	peerLog.WithFields(logrus.Fields{
		"asn":     asn,
		"peer_id": peerID,
	}).Info("peer created")

	JSON(w, http.StatusCreated, map[string]any{
		"id":                peerID,
		"status":            "pending",
		"wg_listen_port":    wgPort,
		"wg_interface_name": wgIfName,
		"mtu":               req.MTU,
		"wg_preshared_key":  psk,
	})
}

func (h *PeerHandler) Get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	asn := middleware.GetASN(ctx)
	peerID := chi.URLParam(r, "id")
	peerLog.WithFields(logrus.Fields{
		"peer_id": peerID,
		"asn":     asn,
	}).Debug("get peer request")

	result, err := h.peers.GetByIDAndASNWithNode(ctx, peerID, asn)
	if err != nil {
		peerLog.WithField("peer_id", peerID).Debug("peer not found")
		ErrorJSON(w, r, http.StatusNotFound, "peer_not_found", "Peer not found")
		return
	}

	type peerResp struct {
		ID                     string     `json:"id"`
		NodeID                 string     `json:"node_id"`
		RemoteASN              int64      `json:"remote_asn"`
		RemotePubkey           string     `json:"remote_pubkey"`
		RemoteEndpoint         string     `json:"remote_endpoint"`
		RemoteLLA              string     `json:"remote_lla"`
		ContactEmail           string     `json:"contact_email"`
		WgListenPort           int        `json:"wg_listen_port"`
		WgInterfaceName        string     `json:"wg_interface_name"`
		WgManaged              bool       `json:"wg_managed"`
		Status                 string     `json:"status"`
		RejectReason           *string    `json:"reject_reason,omitempty"`
		EndpointMismatchSince  *time.Time `json:"endpoint_mismatch_since,omitempty"`
		BGPSuspendedByEndpoint bool       `json:"bgp_suspended_by_endpoint"`
		CreatedAt              time.Time  `json:"created_at"`
		UpdatedAt              time.Time  `json:"updated_at"`
		NodeName               string     `json:"node_name"`
		NodeLocation           string     `json:"node_location"`
		NodePublicIP           string     `json:"node_public_ip"`
		NodeOurLLA             string     `json:"node_our_lla"`
		NodeOurWgPubkey        string     `json:"node_our_wg_pubkey"`
		MTU                    *int       `json:"mtu,omitempty"`
		WgPreSharedKey         *string    `json:"wg_preshared_key,omitempty"`
	}

	p := peerResp{
		ID:                     result.Peer().ID,
		NodeID:                 result.Peer().NodeID,
		RemoteASN:              result.Peer().RemoteASN,
		RemotePubkey:           result.Peer().RemotePubkey,
		RemoteEndpoint:         result.Peer().RemoteEndpoint,
		RemoteLLA:              result.Peer().RemoteLLA,
		ContactEmail:           result.Peer().ContactEmail,
		WgListenPort:           result.Peer().WgListenPort,
		WgInterfaceName:        result.Peer().WgInterfaceName,
		WgManaged:              result.Peer().WgManaged,
		Status:                 result.Peer().Status,
		RejectReason:           result.Peer().RejectReason,
		EndpointMismatchSince:  result.Peer().EndpointMismatchSince,
		BGPSuspendedByEndpoint: result.Peer().BGPSuspendedByEndpoint,
		CreatedAt:              result.Peer().CreatedAt,
		UpdatedAt:              result.Peer().UpdatedAt,
		NodeName:               result.NodeName,
		NodeLocation:           result.NodeLocation,
		NodePublicIP:           result.NodePublicIP,
		NodeOurLLA:             result.NodeOurLLA,
		NodeOurWgPubkey:        result.NodeOurWgPubkey,
		MTU:                    result.Peer().MTU,
		WgPreSharedKey:         result.Peer().WgPreSharedKey,
	}

	peerLog.WithField("peer_id", peerID).Debug("peer found")

	JSONVersioned(w, r, http.StatusOK, apiversion.ResourcePeer, "", p)
}

func (h *PeerHandler) Delete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	asn := middleware.GetASN(ctx)
	peerID := chi.URLParam(r, "id")
	peerLog.WithFields(logrus.Fields{
		"peer_id": peerID,
		"asn":     asn,
	}).Debug("delete peer request")

	release, busy := acquirePeerLock(ctx, h.locker, "peer:"+peerID)
	if busy {
		ErrorJSON(w, r, http.StatusConflict, "operation_in_progress", "Another peer operation is in progress, please retry")
		return
	}
	defer release()

	peer, err := h.peers.GetByIDAndASN(ctx, peerID, asn)
	if err != nil {
		peerLog.WithField("peer_id", peerID).Debug("peer not found for deletion")
		ErrorJSON(w, r, http.StatusNotFound, "peer_not_found", "Peer not found")
		return
	}

	peerLog.WithFields(logrus.Fields{
		"peer_id": peerID,
		"status":  peer.Status,
	}).Debug("current peer status")

	if peer.Status == "active" {
		peerLog.WithFields(logrus.Fields{
			"peer_id": peerID,
			"node_id": peer.NodeID,
		}).Debug("sending peer remove command to agent")
		resp, err := h.hub.SendCommand(peer.NodeID, ws.TypePeerRemove, ws.PeerRemovePayload{
			PeerID: peerID,
			ASN:    asn,
		})
		if err != nil {
			peerLog.WithError(err).Debug("agent command failed")
			ErrorJSON(w, r, http.StatusServiceUnavailable, "agent_error", "Failed to reach agent: "+err.Error())
			return
		}
		if resp.Success != nil && !*resp.Success {
			peerLog.WithField("error", resp.Error).Debug("agent failed to remove peer")
			ErrorJSON(w, r, http.StatusInternalServerError, "agent_error", "Agent failed to remove peer: "+resp.Error)
			return
		}
		peerLog.Debug("agent peer remove command succeeded")
	}

	err = h.peers.Delete(ctx, peerID)
	if err != nil {
		peerLog.WithError(err).WithField("peer_id", peerID).Debug("DB delete failed")
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to delete peer")
		return
	}
	peerLog.WithField("peer_id", peerID).Debug("peer deleted from database")

	operator := fmt.Sprintf("AS%d", asn)
	h.audit.Log(ctx, "peer.delete", operator, &peerID, nil)

	go h.hub.NotifyPeerBot(asn, service.NotificationPeerDeleted, "peer.deleted", map[string]interface{}{
		"asn":    asn,
		"peerId": peerID,
	})

	peerLog.WithFields(logrus.Fields{
		"asn":     asn,
		"peer_id": peerID,
	}).Info("peer deleted")

	JSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *PeerHandler) Metrics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	asn := middleware.GetASN(ctx)
	peerID := chi.URLParam(r, "id")
	peerLog.WithFields(logrus.Fields{
		"peer_id": peerID,
		"asn":     asn,
	}).Debug("metrics request")

	_, err := h.peers.GetByIDAndASN(ctx, peerID, asn)
	if err != nil {
		peerLog.WithField("peer_id", peerID).Debug("peer not found for metrics")
		ErrorJSON(w, r, http.StatusNotFound, "peer_not_found", "Peer not found")
		return
	}

	summary, _ := h.metrics.GetLatestPeerMetricSummary(ctx, peerID)

	hours := 24
	if q := r.URL.Query().Get("hours"); q != "" {
		if v, err := strconv.Atoi(q); err == nil && v > 0 && v <= 720 {
			hours = v
		}
	}

	dbMetrics, err := h.metrics.QueryPeerMetrics(ctx, peerID, hours)
	if err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to fetch metrics")
		return
	}

	type metricPoint struct {
		Time          time.Time `json:"time"`
		RxBytes       int64     `json:"rx_bytes"`
		TxBytes       int64     `json:"tx_bytes"`
		RttMs         *float64  `json:"rtt_ms"`
		BGPState      *string   `json:"bgp_state"`
		LastHandshake *string   `json:"last_handshake"`
	}

	points := make([]metricPoint, 0, len(dbMetrics))
	for _, m := range dbMetrics {
		var bgpState *string
		if m.BGPState != "" {
			bgpState = &m.BGPState
		}
		var handshake *string
		if m.LastHandshake != nil {
			s := m.LastHandshake.Format(time.RFC3339)
			handshake = &s
		}
		points = append(points, metricPoint{
			Time:          m.Time,
			RxBytes:       m.RxBytes,
			TxBytes:       m.TxBytes,
			RttMs:         m.RttMs,
			BGPState:      bgpState,
			LastHandshake: handshake,
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

	peerLog.WithFields(logrus.Fields{
		"peer_id":      peerID,
		"hours":        hours,
		"result_count": len(points),
	}).Debug("metrics result")

	JSON(w, http.StatusOK, map[string]any{
		"peer_id":                    peerID,
		"latest_rtt":                 latestRtt,
		"latest_bgp_state":           latestBGPState,
		"latest_handshake":           latestHandshake,
		"latest_wg_actual_endpoint":  func() *string { if summary != nil { return summary.WgActualEndpoint }; return nil }(),
		"points":                     points,
	})
}

func (h *PeerHandler) Summary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	asn := middleware.GetASN(ctx)
	peerLog.WithField("asn", asn).Debug("summary request")

	results, err := h.peers.SummaryByASN(ctx, asn)
	if err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to fetch peer summary")
		return
	}

	type summaryItem struct {
		PeerID          string   `json:"peer_id"`
		LatestRtt       *float64 `json:"latest_rtt"`
		LatestBGPState  *string  `json:"latest_bgp_state"`
		LatestHandshake *string  `json:"latest_handshake"`
		LatestBGPUptime *int     `json:"latest_bgp_uptime_secs"`
	}

	items := make([]summaryItem, 0, len(results))
	for _, r := range results {
		var lh *string
		if r.LatestHandshake != nil {
			s := r.LatestHandshake.Format(time.RFC3339)
			lh = &s
		}
		items = append(items, summaryItem{
			PeerID:          r.PeerID,
			LatestRtt:       r.LatestRtt,
			LatestBGPState:  r.LatestBGPState,
			LatestHandshake: lh,
			LatestBGPUptime: r.LatestBGPUptime,
		})
	}

	peerLog.WithFields(logrus.Fields{
		"asn":          asn,
		"result_count": len(items),
	}).Debug("summary result")

	JSON(w, http.StatusOK, items)
}

func (h *PeerHandler) CreationStatus(w http.ResponseWriter, r *http.Request) {
	enabled := true
	if h.admin != nil {
		enabled = h.admin.GetSiteSettingValue("peer_creation_enabled") == "true"
	}
	JSON(w, http.StatusOK, map[string]bool{"enabled": enabled})
}

func (h *PeerHandler) Update(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	asn := middleware.GetASN(ctx)
	peerID := chi.URLParam(r, "id")

	peerLog.WithFields(logrus.Fields{
		"peer_id": peerID,
		"asn":     asn,
	}).Debug("update peer request")

	release, busy := acquirePeerLock(ctx, h.locker, "peer:"+peerID)
	if busy {
		ErrorJSON(w, r, http.StatusConflict, "operation_in_progress", "Another peer operation is in progress, please retry")
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

	peer, err := h.peers.GetByIDAndASN(ctx, peerID, asn)
	if err != nil {
		peerLog.WithField("peer_id", peerID).Debug("peer not found for update")
		ErrorJSON(w, r, http.StatusNotFound, "peer_not_found", "Peer not found")
		return
	}

	if peer.Status != "pending" && peer.Status != "active" {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "Only pending or active peers can be edited")
		return
	}

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
		peerLog.WithField("peer_id", peerID).Debug("removing peer from agent for reconfiguration")
		removeResp, removeErr := h.hub.SendCommand(peer.NodeID, ws.TypePeerRemove, ws.PeerRemovePayload{
			PeerID: peerID,
			ASN:    asn,
		})
		if removeErr != nil {
			peerLog.WithError(removeErr).WithField("peer_id", peerID).Error("Update: peer.remove failed")
			ErrorJSON(w, r, http.StatusServiceUnavailable, "agent_error", "Failed to remove old peer config: "+removeErr.Error())
			return
		}
		if removeResp.Success != nil && !*removeResp.Success {
			ErrorJSON(w, r, http.StatusInternalServerError, "agent_error", "Agent rejected peer removal: "+removeResp.Error)
			return
		}

		addResp, addErr := h.hub.SendCommand(peer.NodeID, ws.TypePeerAdd, ws.PeerAddPayload{
			PeerID:         peerID,
			ASN:            asn,
			RemoteEndpoint: newEndpoint,
			RemoteWgPubkey: newPubkey,
			RemoteLLA:      newLLA,
			ListenPort:     peer.WgListenPort,
			WgInterface:    peer.WgInterfaceName,
			MTU:            newMTU,
			WgPreSharedKey: peer.WgPreSharedKey,
		})
		if addErr != nil {
			peerLog.WithError(addErr).WithField("peer_id", peerID).Error("Update: peer.add failed, rolling back")
			h.hub.SendCommand(peer.NodeID, ws.TypePeerAdd, ws.PeerAddPayload{
				PeerID:         peerID,
				ASN:            asn,
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
			peerLog.WithField("peer_id", peerID).Error("Update: peer.add rejected, rolling back")
			h.hub.SendCommand(peer.NodeID, ws.TypePeerAdd, ws.PeerAddPayload{
				PeerID:         peerID,
				ASN:            asn,
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
		peerLog.WithError(err).WithField("peer_id", peerID).Error("Update: DB update failed")
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to update peer")
		return
	}

	operator := fmt.Sprintf("AS%d", asn)
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
	h.audit.Log(ctx, "peer.user_update", operator, &peerID, changes)

	peerLog.WithFields(logrus.Fields{
		"peer_id": peerID,
		"asn":     asn,
		"changes": changes,
	}).Info("peer updated by user")

	JSON(w, http.StatusOK, map[string]string{"status": "updated"})
}
