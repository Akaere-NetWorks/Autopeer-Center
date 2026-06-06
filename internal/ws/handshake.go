package ws

import (
	"context"
	"encoding/json"
	"time"

	"github.com/akaere/autopeer-center/internal/crypto"
	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
)

func (h *Hub) handleKeyInit(ac *AgentConn, msg Message) {
	log.WithField("node_id", ac.NodeID).Info("handleKeyInit")

	var payload KeyInitPayload
	if data, err := json.Marshal(msg.Payload); err != nil {
		log.WithError(err).WithField("node_id", ac.NodeID).Error("key.init: marshal payload failed")
		ac.Close()
		return
	} else if err := json.Unmarshal(data, &payload); err != nil {
		log.WithError(err).WithField("node_id", ac.NodeID).Error("key.init: unmarshal payload failed")
		ac.Close()
		return
	}

	if payload.Pubkey == "" {
		log.WithField("node_id", ac.NodeID).Error("key.init missing pubkey")
		return
	}

	if h.centerKeyPair == nil {
		log.Error("center key pair not initialized")
		return
	}

	agentPub, err := crypto.PublicKeyFromHex(payload.Pubkey)
	if err != nil {
		log.WithError(err).WithField("node_id", ac.NodeID).Error("invalid agent pubkey")
		return
	}

	shared, err := crypto.DeriveSharedSecret(h.centerKeyPair.PrivateKey, agentPub)
	if err != nil {
		log.WithError(err).WithField("node_id", ac.NodeID).Error("ECDH failed")
		return
	}

	authNonce, err := crypto.NewNonce()
	if err != nil {
		log.WithError(err).WithField("node_id", ac.NodeID).Error("generate nonce failed")
		return
	}

	encKey, err := crypto.DeriveEncKey(shared, authNonce)
	if err != nil {
		log.WithError(err).WithField("node_id", ac.NodeID).Error("derive enc key failed")
		return
	}

	checkCtx, checkCancel := context.WithTimeout(context.Background(), 5*time.Second)
	node, err := h.nodes.GetByID(checkCtx, ac.NodeID)
	checkCancel()
	if err != nil {
		log.WithError(err).WithField("node_id", ac.NodeID).Error("query node for existing pubkey failed")
		return
	}

	existingPubkey := node.AgentPubkey
	if existingPubkey != "" && existingPubkey != payload.Pubkey {
		log.WithField("node_id", ac.NodeID).Error("key.init rejected: different pubkey already registered")
		h.notifyPubkeyReset(ac.NodeID)
		errData, _ := json.Marshal(Message{
			Type:  TypeKeyInitAck,
			Error: "reset_required",
		})
		ac.writeMu.Lock()
		ac.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		ac.Conn.WriteMessage(websocket.TextMessage, errData)
		ac.writeMu.Unlock()
		ac.Close()
		return
	}

	if existingPubkey == "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := h.nodes.SetAgentPubkey(ctx, ac.NodeID, payload.Pubkey)
		cancel()
		if err != nil {
			log.WithError(err).WithField("node_id", ac.NodeID).Error("store pubkey failed")
			return
		}
	} else {
		log.WithField("node_id", ac.NodeID).Info("key.init idempotent: pubkey already registered with same value")
	}

	ackMsg := Message{
		Type: TypeKeyInitAck,
		Payload: KeyInitAckPayload{
			Pubkey: crypto.PubKeyHex(h.centerKeyPair.PublicKey),
			Nonce:  authNonce,
		},
	}
	ackData, _ := json.Marshal(ackMsg)

	ac.writeMu.Lock()
	ac.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := ac.Conn.WriteMessage(websocket.TextMessage, ackData); err != nil {
		ac.writeMu.Unlock()
		log.WithError(err).WithField("node_id", ac.NodeID).Error("write key.init_ack failed")
		return
	}
	ac.writeMu.Unlock()

	ac.EnableEncryption(encKey)
	h.Activate(ac)
	log.WithField("node_id", ac.NodeID).Info("key exchange complete, encryption enabled")
}

func (h *Hub) handleKeyAuth(ac *AgentConn, msg Message) {
	log.WithField("node_id", ac.NodeID).Info("handleKeyAuth")

	var payload KeyAuthPayload
	if data, err := json.Marshal(msg.Payload); err != nil {
		log.WithError(err).WithField("node_id", ac.NodeID).Error("key.auth: marshal payload failed")
		ac.Close()
		return
	} else if err := json.Unmarshal(data, &payload); err != nil {
		log.WithError(err).WithField("node_id", ac.NodeID).Error("key.auth: unmarshal payload failed")
		ac.Close()
		return
	}

	if payload.NodeID == "" || payload.Pubkey == "" || len(payload.Nonce) == 0 || len(payload.Proof) == 0 {
		log.WithField("node_id", ac.NodeID).Error("key.auth missing required fields")
		ac.Close()
		return
	}

	if h.centerKeyPair == nil {
		log.Error("center key pair not initialized")
		ac.Close()
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	storedPubkey, err := h.nodes.GetAgentPubkey(ctx, payload.NodeID)
	cancel()
	if err != nil || storedPubkey == "" {
		log.WithError(err).WithField("node_id", payload.NodeID).Error("node not found or no pubkey registered")
		ac.Close()
		return
	}

	if payload.NodeID != ac.NodeID {
		log.WithFields(logrus.Fields{"claimed": payload.NodeID, "established": ac.NodeID}).Error("key.auth NodeID mismatch")
		ac.Close()
		return
	}

	if storedPubkey != payload.Pubkey {
		log.WithField("node_id", payload.NodeID).Error("pubkey mismatch")
		ac.Close()
		return
	}

	agentPub, err := crypto.PublicKeyFromHex(payload.Pubkey)
	if err != nil {
		log.WithError(err).WithField("node_id", payload.NodeID).Error("invalid agent pubkey")
		ac.Close()
		return
	}

	shared, err := crypto.DeriveSharedSecret(h.centerKeyPair.PrivateKey, agentPub)
	if err != nil {
		log.WithError(err).WithField("node_id", payload.NodeID).Error("ECDH failed")
		ac.Close()
		return
	}

	if !crypto.VerifyAuthProof(shared, payload.Nonce, payload.Proof) {
		log.WithField("node_id", payload.NodeID).Error("auth proof verification failed")
		ac.Close()
		return
	}

	ephemeralKeyPair, err := crypto.GenerateKeyPair()
	if err != nil {
		log.WithError(err).WithField("node_id", payload.NodeID).Error("generate ephemeral key failed")
		ac.Close()
		return
	}

	sessionShared, err := crypto.DeriveSharedSecret(ephemeralKeyPair.PrivateKey, agentPub)
	if err != nil {
		log.WithError(err).WithField("node_id", payload.NodeID).Error("session ECDH failed")
		ac.Close()
		return
	}

	centerNonce, err := crypto.NewNonce()
	if err != nil {
		log.WithError(err).WithField("node_id", payload.NodeID).Error("generate center nonce failed")
		ac.Close()
		return
	}

	combinedNonce := append(append([]byte{}, payload.Nonce...), centerNonce...)
	sessionKey, err := crypto.DeriveEncKey(sessionShared, combinedNonce)
	if err != nil {
		log.WithError(err).WithField("node_id", payload.NodeID).Error("derive session key failed")
		ac.Close()
		return
	}

	ackMsg := Message{
		Type: TypeKeyAuthAck,
		Payload: KeyAuthAckPayload{
			Pubkey:      crypto.PubKeyHex(ephemeralKeyPair.PublicKey),
			AuthNonce:   payload.Nonce,
			CenterNonce: centerNonce,
		},
	}
	ackData, _ := json.Marshal(ackMsg)

	ac.writeMu.Lock()
	ac.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := ac.Conn.WriteMessage(websocket.TextMessage, ackData); err != nil {
		ac.writeMu.Unlock()
		log.WithError(err).WithField("node_id", payload.NodeID).Error("write key.auth_ack failed")
		ac.Close()
		return
	}
	ac.writeMu.Unlock()

	ac.EnableEncryption(sessionKey)
	h.Activate(ac)
	log.WithField("node_id", ac.NodeID).Info("key auth complete, encryption enabled")
}

func (h *Hub) handlePeersSync(ac *AgentConn) {
	if ac.pendingAuth {
		log.WithField("node_id", ac.NodeID).Warn("peers.sync rejected: agent not authenticated")
		return
	}
	h.pushPeersSync(ac)
}

// TriggerPeersSync pushes the current active-peer list to a node if it is online
// and authenticated, causing the agent to converge its WireGuard/BIRD config to
// the database (recreate missing peers, remove stale ones). Used by the
// reconcile worker. Returns false if the node is offline or not yet authed.
func (h *Hub) TriggerPeersSync(nodeID string) bool {
	ac := h.GetConn(nodeID)
	if ac == nil || ac.pendingAuth {
		return false
	}
	h.pushPeersSync(ac)
	return true
}

// pushPeersSync builds the node's active-peer list and sends it over the agent
// connection. The agent reconciles itself against this list.
func (h *Hub) pushPeersSync(ac *AgentConn) {
	log.WithField("node_id", ac.NodeID).Debug("handlePeersSync")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	peers := make([]PeerSyncEntry, 0)

	allPeers, err := h.peers.ListByNode(ctx, ac.NodeID)
	if err != nil {
		log.WithError(err).WithField("node_id", ac.NodeID).Error("peers.sync query error, sending empty list")
	} else {
		for _, p := range allPeers {
			if p.Status != "active" {
				continue
			}
			peers = append(peers, PeerSyncEntry{
				PeerID:             p.ID,
				ASN:                p.RemoteASN,
				RemoteEndpoint:     p.RemoteEndpoint,
				RemoteWgPubkey:     p.RemotePubkey,
				RemoteLLA:          p.RemoteLLA,
				ListenPort:         p.WgListenPort,
				WgInterface:        p.WgInterfaceName,
				BgpProtoName:       p.BgpProtoName,
				BirdConfigFilename: p.BirdConfigFilename,
				MTU:                p.MTU,
				WgPreSharedKey:     p.WgPreSharedKey,
			})
		}
	}

	payload := PeersSyncPayload{Peers: peers}
	data, err := json.Marshal(Message{
		Type:    TypePeersSync,
		Payload: payload,
	})
	if err != nil {
		log.WithError(err).WithField("node_id", ac.NodeID).Error("handlePeersSync: marshal failed")
		return
	}

	select {
	case ac.sendCh <- data:
	default:
		time.AfterFunc(2*time.Second, func() {
			select {
			case ac.sendCh <- data:
				log.WithField("node_id", ac.NodeID).Info("peers.sync delayed send succeeded")
			default:
				log.WithField("node_id", ac.NodeID).Error("peers.sync send buffer still full after retry, data dropped")
			}
		})
	}

	log.WithFields(logrus.Fields{"peer_count": len(peers), "node_id": ac.NodeID}).Info("peers.sync sent")
}
