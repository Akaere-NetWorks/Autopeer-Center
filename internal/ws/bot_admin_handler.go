package ws

import (
	"context"
	"encoding/json"
	"time"

	"github.com/akaere/autopeer-center/internal/repository"
)

func (h *Hub) handleBotAdminPeers(bc *BotConn, msg Message) {
	var payload BotAdminPeersPayload
	if data, err := json.Marshal(msg.Payload); err == nil {
		json.Unmarshal(data, &payload)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	params := repository.ListParams{
		Status:   payload.Status,
		Page:     payload.Page,
		PageSize: 10,
	}
	if params.Page < 1 {
		params.Page = 1
	}

	peers, total, err := h.peers.List(ctx, params)
	if err != nil {
		h.sendToBot(bc, TypeBotAdminPeersResult, msg.ID, BotAdminPeersResultPayload{Error: "query failed"})
		return
	}

	result := make([]BotAdminPeerInfo, 0, len(peers))
	for _, p := range peers {
		result = append(result, BotAdminPeerInfo{
			ID:             p.ID,
			NodeName:       p.NodeName,
			NodeLocation:   p.NodeLocation,
			RemoteASN:      p.RemoteASN,
			Status:         p.Status,
			ContactEmail:   p.ContactEmail,
			RejectReason:   p.RejectReason,
			LatestRtt:      p.LatestRtt,
			LatestBGPState: p.LatestBGPState,
			CreatedAt:      p.CreatedAt.Format(time.RFC3339),
		})
	}

	h.sendToBot(bc, TypeBotAdminPeersResult, msg.ID, BotAdminPeersResultPayload{
		Peers: result, Total: total, Page: params.Page,
	})
}

func (h *Hub) handleBotAdminPeerAction(bc *BotConn, msg Message) {
	var payload BotAdminPeerActionPayload
	if data, err := json.Marshal(msg.Payload); err == nil {
		json.Unmarshal(data, &payload)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	switch payload.Action {
	case "approve":
		h.handleBotAdminApprove(bc, msg.ID, ctx, payload)
	case "reject":
		h.handleBotAdminReject(bc, msg.ID, ctx, payload)
	case "suspend":
		h.handleBotAdminSuspend(bc, msg.ID, ctx, payload)
	case "unsuspend":
		h.handleBotAdminUnsuspend(bc, msg.ID, ctx, payload)
	default:
		h.sendToBot(bc, TypeBotAdminPeerActionResult, msg.ID, BotAdminPeerActionResultPayload{
			Success: false, Error: "unknown action: " + payload.Action,
		})
	}
}

func (h *Hub) handleBotAdminApprove(bc *BotConn, msgID string, ctx context.Context, payload BotAdminPeerActionPayload) {
	peer, err := h.peers.GetByIDWithNode(ctx, payload.PeerID)
	if err != nil || peer == nil {
		h.sendToBot(bc, TypeBotAdminPeerActionResult, msgID, BotAdminPeerActionResultPayload{Success: false, Error: "peer not found"})
		return
	}
	if peer.Status != "pending" {
		h.sendToBot(bc, TypeBotAdminPeerActionResult, msgID, BotAdminPeerActionResultPayload{Success: false, Error: "peer is not pending"})
		return
	}

	if !h.IsOnline(peer.NodeID) {
		h.sendToBot(bc, TypeBotAdminPeerActionResult, msgID, BotAdminPeerActionResultPayload{Success: false, Error: "node is offline"})
		return
	}

	resp, err := h.SendCommand(peer.NodeID, TypePeerAdd, PeerAddPayload{
		PeerID:         peer.ID,
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
		h.sendToBot(bc, TypeBotAdminPeerActionResult, msgID, BotAdminPeerActionResultPayload{Success: false, Error: err.Error()})
		return
	}
	if resp.Success != nil && !*resp.Success {
		h.sendToBot(bc, TypeBotAdminPeerActionResult, msgID, BotAdminPeerActionResultPayload{Success: false, Error: resp.Error})
		return
	}

	if err := h.peers.UpdateStatus(ctx, peer.ID, "active"); err != nil {
		h.SendCommand(peer.NodeID, TypePeerRemove, PeerRemovePayload{PeerID: peer.ID, ASN: peer.RemoteASN})
		h.sendToBot(bc, TypeBotAdminPeerActionResult, msgID, BotAdminPeerActionResultPayload{Success: false, Error: "db update failed"})
		return
	}

	h.sendToBot(bc, TypeBotAdminPeerActionResult, msgID, BotAdminPeerActionResultPayload{
		Success: true, Message: "peer approved",
	})

	go h.NotifyPeerEvent(peer.RemoteASN, "peer.approved", map[string]interface{}{
		"peer_id": peer.ID, "nodeName": peer.NodeName,
	})
}

func (h *Hub) handleBotAdminReject(bc *BotConn, msgID string, ctx context.Context, payload BotAdminPeerActionPayload) {
	peer, err := h.peers.GetByIDWithNode(ctx, payload.PeerID)
	if err != nil || peer == nil {
		h.sendToBot(bc, TypeBotAdminPeerActionResult, msgID, BotAdminPeerActionResultPayload{Success: false, Error: "peer not found"})
		return
	}
	if peer.Status != "pending" {
		h.sendToBot(bc, TypeBotAdminPeerActionResult, msgID, BotAdminPeerActionResultPayload{Success: false, Error: "peer is not pending"})
		return
	}

	if err := h.peers.UpdateStatus(ctx, peer.ID, "rejected", repository.WithRejectReason(payload.Reason)); err != nil {
		h.sendToBot(bc, TypeBotAdminPeerActionResult, msgID, BotAdminPeerActionResultPayload{Success: false, Error: "db update failed"})
		return
	}

	h.sendToBot(bc, TypeBotAdminPeerActionResult, msgID, BotAdminPeerActionResultPayload{
		Success: true, Message: "peer rejected",
	})

	go h.NotifyPeerEvent(peer.RemoteASN, "peer.rejected", map[string]interface{}{
		"peer_id": peer.ID, "nodeName": peer.NodeName, "reason": payload.Reason,
	})
}

func (h *Hub) handleBotAdminSuspend(bc *BotConn, msgID string, ctx context.Context, payload BotAdminPeerActionPayload) {
	peer, err := h.peers.GetByID(ctx, payload.PeerID)
	if err != nil || peer == nil {
		h.sendToBot(bc, TypeBotAdminPeerActionResult, msgID, BotAdminPeerActionResultPayload{Success: false, Error: "peer not found"})
		return
	}
	if peer.Status != "active" {
		h.sendToBot(bc, TypeBotAdminPeerActionResult, msgID, BotAdminPeerActionResultPayload{Success: false, Error: "peer is not active"})
		return
	}

	if h.IsOnline(peer.NodeID) {
		h.SendCommand(peer.NodeID, TypePeerRemove, PeerRemovePayload{PeerID: peer.ID, ASN: peer.RemoteASN})
	}

	if err := h.peers.UpdateStatus(ctx, peer.ID, "suspended"); err != nil {
		h.sendToBot(bc, TypeBotAdminPeerActionResult, msgID, BotAdminPeerActionResultPayload{Success: false, Error: "db update failed"})
		return
	}

	h.ClearPeerAlerts(peer.ID)
	h.sendToBot(bc, TypeBotAdminPeerActionResult, msgID, BotAdminPeerActionResultPayload{
		Success: true, Message: "peer suspended",
	})

	go h.NotifyPeerEvent(peer.RemoteASN, "peer.suspended", map[string]interface{}{
		"peer_id": peer.ID, "reason": payload.Reason,
	})
}

func (h *Hub) handleBotAdminUnsuspend(bc *BotConn, msgID string, ctx context.Context, payload BotAdminPeerActionPayload) {
	peer, err := h.peers.GetByID(ctx, payload.PeerID)
	if err != nil || peer == nil {
		h.sendToBot(bc, TypeBotAdminPeerActionResult, msgID, BotAdminPeerActionResultPayload{Success: false, Error: "peer not found"})
		return
	}
	if peer.Status != "suspended" {
		h.sendToBot(bc, TypeBotAdminPeerActionResult, msgID, BotAdminPeerActionResultPayload{Success: false, Error: "peer is not suspended"})
		return
	}

	if h.IsOnline(peer.NodeID) {
		h.SendCommand(peer.NodeID, TypePeerAdd, PeerAddPayload{
			PeerID:         peer.ID,
			ASN:            peer.RemoteASN,
			RemoteEndpoint: peer.RemoteEndpoint,
			RemoteWgPubkey: peer.RemotePubkey,
			RemoteLLA:      peer.RemoteLLA,
			ListenPort:     peer.WgListenPort,
			WgInterface:    peer.WgInterfaceName,
			MTU:            peer.MTU,
			WgPreSharedKey: peer.WgPreSharedKey,
		})
	}

	if err := h.peers.UpdateStatus(ctx, peer.ID, "active"); err != nil {
		h.sendToBot(bc, TypeBotAdminPeerActionResult, msgID, BotAdminPeerActionResultPayload{Success: false, Error: "db update failed"})
		return
	}

	h.sendToBot(bc, TypeBotAdminPeerActionResult, msgID, BotAdminPeerActionResultPayload{
		Success: true, Message: "peer unsuspended",
	})

	go h.NotifyPeerEvent(peer.RemoteASN, "peer.unsuspended", map[string]interface{}{
		"peer_id": peer.ID,
	})
}

func (h *Hub) handleBotAdminNodes(bc *BotConn, msg Message) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	nodes, err := h.nodes.ListEnabled(ctx)
	if err != nil {
		h.sendToBot(bc, TypeBotAdminNodesResult, msg.ID, BotAdminNodesResultPayload{Error: "query failed"})
		return
	}

	result := make([]BotAdminNodeInfo, 0, len(nodes))
	for _, n := range nodes {
		peerList, _ := h.peers.ListByNode(ctx, n.ID)
		peerCount := 0
		if peerList != nil {
			peerCount = len(peerList)
		}
		result = append(result, BotAdminNodeInfo{
			ID:           n.ID,
			Name:         n.Name,
			Location:     n.Location,
			ASN:          n.OurASN,
			Online:       n.Online,
			AgentVersion: n.AgentVersion,
			AgentState:   n.AgentState,
			PeerCount:    peerCount,
		})
	}

	h.sendToBot(bc, TypeBotAdminNodesResult, msg.ID, BotAdminNodesResultPayload{Nodes: result})
}

func (h *Hub) handleBotAdminNodeDetail(bc *BotConn, msg Message) {
	var payload BotAdminNodeDetailPayload
	if data, err := json.Marshal(msg.Payload); err == nil {
		json.Unmarshal(data, &payload)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	node, err := h.nodes.GetByID(ctx, payload.NodeID)
	if err != nil || node == nil {
		h.sendToBot(bc, TypeBotAdminNodeDetailResult, msg.ID, BotAdminNodeDetailResultPayload{Found: false})
		return
	}

	peerList, _ := h.peers.ListByNode(ctx, node.ID)
	peerCount := 0
	if peerList != nil {
		peerCount = len(peerList)
	}

	h.sendToBot(bc, TypeBotAdminNodeDetailResult, msg.ID, BotAdminNodeDetailResultPayload{
		Found: true,
		Node: &BotAdminNodeInfo{
			ID:           node.ID,
			Name:         node.Name,
			Location:     node.Location,
			ASN:          node.OurASN,
			Online:       node.Online,
			AgentVersion: node.AgentVersion,
			AgentState:   node.AgentState,
			PeerCount:    peerCount,
		},
	})
}

func (h *Hub) handleBotAdminNodeUpdate(bc *BotConn, msg Message) {
	var payload BotAdminNodeUpdatePayload
	if data, err := json.Marshal(msg.Payload); err == nil {
		json.Unmarshal(data, &payload)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	node, err := h.nodes.GetByID(ctx, payload.NodeID)
	if err != nil || node == nil {
		h.sendToBot(bc, TypeBotAdminNodeUpdateResult, msg.ID, BotAdminNodeUpdateResultPayload{Success: false, Error: "node not found"})
		return
	}

	if !h.IsOnline(node.ID) {
		h.sendToBot(bc, TypeBotAdminNodeUpdateResult, msg.ID, BotAdminNodeUpdateResultPayload{Success: false, Error: "node is offline"})
		return
	}

	resp, err := h.SendCommand(node.ID, TypeAgentUpdate, nil)
	if err != nil {
		h.sendToBot(bc, TypeBotAdminNodeUpdateResult, msg.ID, BotAdminNodeUpdateResultPayload{Success: false, Error: err.Error()})
		return
	}
	if resp.Success != nil && !*resp.Success {
		h.sendToBot(bc, TypeBotAdminNodeUpdateResult, msg.ID, BotAdminNodeUpdateResultPayload{Success: false, Error: resp.Error})
		return
	}

	h.sendToBot(bc, TypeBotAdminNodeUpdateResult, msg.ID, BotAdminNodeUpdateResultPayload{
		Success: true, Message: "update triggered",
	})
}

func (h *Hub) handleBotAdminAudit(bc *BotConn, msg Message) {
	var payload BotAdminAuditPayload
	if data, err := json.Marshal(msg.Payload); err == nil {
		json.Unmarshal(data, &payload)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if payload.Page < 1 {
		payload.Page = 1
	}

	entries, total, err := h.audit.ListFiltered(ctx, "", "", payload.Page, 10)
	if err != nil {
		h.sendToBot(bc, TypeBotAdminAuditResult, msg.ID, BotAdminAuditResultPayload{Error: "query failed"})
		return
	}

	result := make([]BotAuditEntry, 0, len(entries))
	for _, e := range entries {
		result = append(result, BotAuditEntry{
			ID:        e.ID,
			Action:    e.Action,
			Operator:  e.Operator,
			TargetID:  func() string { if e.TargetID != nil { return *e.TargetID }; return "" }(),
			CreatedAt: e.CreatedAt.Format(time.RFC3339),
		})
	}

	h.sendToBot(bc, TypeBotAdminAuditResult, msg.ID, BotAdminAuditResultPayload{
		Entries: result, Total: total, Page: payload.Page,
	})
}
