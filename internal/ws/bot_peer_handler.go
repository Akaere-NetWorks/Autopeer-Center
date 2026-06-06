package ws

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/akaere/autopeer-center/internal/peering"
)

func (h *Hub) NotifyPeerEvent(asn int64, event string, data map[string]interface{}) {
	if h.botBindings == nil {
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		binding, err := h.botBindings.GetByASN(ctx, asn)
		if err != nil || binding == nil {
			return
		}

		h.NotifyBotUser(binding.TgUserID, event, data)
	}()
}

func (h *Hub) handleBotUserPeers(bc *BotConn, msg Message) {
	var payload BotUserPeersPayload
	if data, err := json.Marshal(msg.Payload); err == nil {
		json.Unmarshal(data, &payload)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	binding, err := h.botBindings.GetByTgUserID(ctx, payload.TgUserID)
	if err != nil || binding == nil {
		h.sendToBot(bc, TypeBotUserPeersResult, msg.ID, BotUserPeersResultPayload{})
		return
	}

	peers, err := h.peers.ListByASN(ctx, binding.ASN)
	if err != nil {
		h.sendToBot(bc, TypeBotUserPeersResult, msg.ID, BotUserPeersResultPayload{})
		return
	}

	result := make([]BotPeerInfo, 0, len(peers))
	for _, p := range peers {
		result = append(result, BotPeerInfo{
			ID:           p.ID,
			NodeID:       p.NodeID,
			NodeName:     p.NodeName,
			NodeLocation: p.NodeLocation,
			RemoteASN:    p.RemoteASN,
			Status:       p.Status,
			RejectReason: p.RejectReason,
			CreatedAt:    p.CreatedAt.Format(time.RFC3339),
			WgListenPort: p.WgListenPort,
		})
	}

	h.sendToBot(bc, TypeBotUserPeersResult, msg.ID, BotUserPeersResultPayload{Peers: result})
}

func (h *Hub) handleBotUserPeerDetail(bc *BotConn, msg Message) {
	var payload BotUserPeerDetailPayload
	if data, err := json.Marshal(msg.Payload); err == nil {
		json.Unmarshal(data, &payload)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	binding, err := h.botBindings.GetByTgUserID(ctx, payload.TgUserID)
	if err != nil || binding == nil {
		h.sendToBot(bc, TypeBotUserPeerDetailResult, msg.ID, BotUserPeerDetailResultPayload{Error: "not bound"})
		return
	}

	detail, err := h.peers.GetByIDAndASNWithNode(ctx, payload.PeerID, binding.ASN)
	if err != nil {
		h.sendToBot(bc, TypeBotUserPeerDetailResult, msg.ID, BotUserPeerDetailResultPayload{Found: false})
		return
	}

	summary, _ := h.metrics.GetLatestPeerMetricSummary(ctx, payload.PeerID)

	pd := &BotPeerDetail{
		ID:              detail.ID,
		NodeID:          detail.NodeID,
		NodeName:        detail.NodeName,
		NodeLocation:    detail.NodeLocation,
		NodePublicIP:    detail.NodePublicIP,
		NodeOurLLA:      detail.NodeOurLLA,
		NodeOurWgPubkey: detail.NodeOurWgPubkey,
		RemoteASN:       detail.RemoteASN,
		RemotePubkey:    detail.RemotePubkey,
		RemoteEndpoint:  detail.RemoteEndpoint,
		RemoteLLA:       detail.RemoteLLA,
		ContactEmail:    detail.ContactEmail,
		WgListenPort:    detail.WgListenPort,
		WgInterfaceName: detail.WgInterfaceName,
		Status:          detail.Status,
		RejectReason:    detail.RejectReason,
		MTU:             detail.MTU,
		CreatedAt:       detail.CreatedAt.Format(time.RFC3339),
	}
	if summary != nil {
		pd.LatestRtt = summary.RttMs
		pd.LatestBGPState = summary.BGPState
		if summary.LastHandshake != nil {
			s := summary.LastHandshake.Format(time.RFC3339)
			pd.LatestHandshake = &s
		}
	}

	h.sendToBot(bc, TypeBotUserPeerDetailResult, msg.ID, BotUserPeerDetailResultPayload{Found: true, Peer: pd})
}

func (h *Hub) handleBotUserPeerCreate(bc *BotConn, msg Message) {
	var payload BotUserPeerCreatePayload
	if data, err := json.Marshal(msg.Payload); err == nil {
		json.Unmarshal(data, &payload)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	binding, err := h.botBindings.GetByTgUserID(ctx, payload.TgUserID)
	if err != nil || binding == nil {
		h.sendToBot(bc, TypeBotUserPeerCreateResult, msg.ID, BotUserPeerCreateResultPayload{Success: false, Error: "not bound"})
		return
	}

	node, err := h.nodes.GetByID(ctx, payload.NodeID)
	if err != nil || !node.Enabled {
		h.sendToBot(bc, TypeBotUserPeerCreateResult, msg.ID, BotUserPeerCreateResultPayload{Success: false, Error: "node not found or disabled"})
		return
	}

	// Build through the shared peering rules so the bot computes the SAME
	// interface name and port as the HTTP/MCP paths and rejects incomplete
	// requests instead of persisting a peer the agent can never configure.
	peer, err := peering.BuildPendingPeer(peering.Input{
		ASN:        binding.ASN,
		NodeID:     payload.NodeID,
		PortPrefix: node.WgPortPrefix,
		Pubkey:     payload.RemotePubkey,
		Endpoint:   payload.RemoteEndpoint,
		LLA:        payload.RemoteLLA,
		MTU:        payload.MTU,
	})
	if err != nil {
		h.sendToBot(bc, TypeBotUserPeerCreateResult, msg.ID, BotUserPeerCreateResultPayload{Success: false, Error: err.Error()})
		return
	}

	// Serialize concurrent creates for the same (node, ASN) so two requests
	// can't both pass the existence check and race on insert.
	release, busy := h.acquirePeerLock(ctx, fmt.Sprintf("peer-create:%s:%d", payload.NodeID, binding.ASN))
	if busy {
		h.sendToBot(bc, TypeBotUserPeerCreateResult, msg.ID, BotUserPeerCreateResultPayload{Success: false, Error: "another peer operation is in progress, please retry"})
		return
	}
	defer release()

	exists, _ := h.peers.ExistsByNodeAndASN(ctx, payload.NodeID, binding.ASN)
	if exists {
		h.sendToBot(bc, TypeBotUserPeerCreateResult, msg.ID, BotUserPeerCreateResultPayload{Success: false, Error: "peer already exists for this node"})
		return
	}

	if h.lookupASNEmail != nil {
		peer.ContactEmail, _ = h.lookupASNEmail(binding.ASN)
	}

	if err := h.peers.Create(ctx, peer); err != nil {
		h.sendToBot(bc, TypeBotUserPeerCreateResult, msg.ID, BotUserPeerCreateResultPayload{Success: false, Error: "create failed (port or peer conflict)"})
		return
	}

	h.sendToBot(bc, TypeBotUserPeerCreateResult, msg.ID, BotUserPeerCreateResultPayload{
		Success: true, PeerID: peer.ID,
	})
}

func (h *Hub) handleBotUserPeerDelete(bc *BotConn, msg Message) {
	var payload BotUserPeerDeletePayload
	if data, err := json.Marshal(msg.Payload); err == nil {
		json.Unmarshal(data, &payload)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	binding, err := h.botBindings.GetByTgUserID(ctx, payload.TgUserID)
	if err != nil || binding == nil {
		h.sendToBot(bc, TypeBotUserPeerDeleteResult, msg.ID, BotUserPeerDeleteResultPayload{Success: false, Error: "not bound"})
		return
	}

	peer, err := h.peers.GetByIDAndASN(ctx, payload.PeerID, binding.ASN)
	if err != nil || peer == nil {
		h.sendToBot(bc, TypeBotUserPeerDeleteResult, msg.ID, BotUserPeerDeleteResultPayload{Success: false, Error: "peer not found"})
		return
	}

	release, busy := h.acquirePeerLock(ctx, "peer:"+peer.ID)
	if busy {
		h.sendToBot(bc, TypeBotUserPeerDeleteResult, msg.ID, BotUserPeerDeleteResultPayload{Success: false, Error: "another peer operation is in progress, please retry"})
		return
	}
	defer release()

	if peer.Status == "active" {
		if h.GetConn(peer.NodeID) != nil {
			h.SendCommand(peer.NodeID, TypePeerRemove, PeerRemovePayload{PeerID: peer.ID, ASN: peer.RemoteASN})
		}
	}

	if err := h.peers.Delete(ctx, peer.ID); err != nil {
		h.sendToBot(bc, TypeBotUserPeerDeleteResult, msg.ID, BotUserPeerDeleteResultPayload{Success: false, Error: "delete failed"})
		return
	}

	h.ClearPeerAlerts(peer.ID)
	h.sendToBot(bc, TypeBotUserPeerDeleteResult, msg.ID, BotUserPeerDeleteResultPayload{Success: true})
}

func (h *Hub) handleBotUserPeerMetrics(bc *BotConn, msg Message) {
	var payload BotUserPeerMetricsPayload
	if data, err := json.Marshal(msg.Payload); err == nil {
		json.Unmarshal(data, &payload)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	binding, err := h.botBindings.GetByTgUserID(ctx, payload.TgUserID)
	if err != nil || binding == nil {
		h.sendToBot(bc, TypeBotUserPeerMetricsResult, msg.ID, BotUserPeerMetricsResultPayload{Error: "not bound"})
		return
	}

	_, err = h.peers.GetByIDAndASN(ctx, payload.PeerID, binding.ASN)
	if err != nil {
		h.sendToBot(bc, TypeBotUserPeerMetricsResult, msg.ID, BotUserPeerMetricsResultPayload{Found: false})
		return
	}

	metrics, err := h.metrics.QueryPeerMetrics(ctx, payload.PeerID, 24)
	if err != nil {
		h.sendToBot(bc, TypeBotUserPeerMetricsResult, msg.ID, BotUserPeerMetricsResultPayload{Found: true})
		return
	}

	pts := make([]BotPeerMetricPt, 0, len(metrics))
	for _, m := range metrics {
		ts := ""
		if !m.Time.IsZero() {
			ts = m.Time.Format(time.RFC3339)
		}
		pts = append(pts, BotPeerMetricPt{
			Time:     ts,
			RttMs:    m.RttMs,
			BGPState: m.BGPState,
			RxBytes:  m.RxBytes,
			TxBytes:  m.TxBytes,
		})
	}

	h.sendToBot(bc, TypeBotUserPeerMetricsResult, msg.ID, BotUserPeerMetricsResultPayload{Found: true, Metrics: pts})
}
