package ws

import (
	"encoding/json"
	"time"

	"github.com/akaere/autopeer-center/internal/crypto"
	"github.com/gorilla/websocket"
)

// handleFlapKeyInit performs the X25519 key exchange for a flap agent. It
// mirrors Hub.handleKeyInit for node agents: it pins the agent's public key on
// first connect (TOFU), rejects a changed key with "reset_required", replies
// with the center public key + nonce (in plaintext), then enables encryption
// and marks the connection authenticated so it becomes queryable.
func (h *FlapHub) handleFlapKeyInit(fc *FlapConn, msg Message) {
	if h.centerKeyPair == nil {
		flog.Error("flap: center key pair not initialised")
		fc.Close()
		return
	}

	var payload KeyInitPayload
	if err := decodePayload(msg, &payload); err != nil || payload.Pubkey == "" {
		flog.WithField("agent_id", fc.AgentID).Error("flap key.init missing or invalid pubkey")
		fc.Close()
		return
	}

	agentPub, err := crypto.PublicKeyFromHex(payload.Pubkey)
	if err != nil {
		flog.WithError(err).WithField("agent_id", fc.AgentID).Error("flap key.init: invalid agent pubkey")
		fc.Close()
		return
	}

	shared, err := crypto.DeriveSharedSecret(h.centerKeyPair.PrivateKey, agentPub)
	if err != nil {
		flog.WithError(err).WithField("agent_id", fc.AgentID).Error("flap key.init: ECDH failed")
		fc.Close()
		return
	}

	nonce, err := crypto.NewNonce()
	if err != nil {
		flog.WithError(err).WithField("agent_id", fc.AgentID).Error("flap key.init: nonce generation failed")
		fc.Close()
		return
	}

	encKey, err := crypto.DeriveEncKey(shared, nonce)
	if err != nil {
		flog.WithError(err).WithField("agent_id", fc.AgentID).Error("flap key.init: derive enc key failed")
		fc.Close()
		return
	}

	ctx := h.baseCtx()
	existing, ok := h.registry.PubkeyFor(ctx, fc.AgentID)
	if !ok {
		flog.WithField("agent_id", fc.AgentID).Error("flap key.init: agent lookup failed")
		fc.Close()
		return
	}

	if existing != "" && existing != payload.Pubkey {
		flog.WithField("agent_id", fc.AgentID).Error("flap key.init rejected: different pubkey already pinned")
		h.writeFlapPlain(fc, Message{Type: TypeKeyInitAck, Error: "reset_required"})
		fc.Close()
		return
	}

	if existing == "" {
		if _, err := h.registry.PinPubkey(ctx, fc.AgentID, payload.Pubkey); err != nil {
			flog.WithError(err).WithField("agent_id", fc.AgentID).Error("flap key.init: pin pubkey failed")
			fc.Close()
			return
		}
		flog.WithField("agent_id", fc.AgentID).Info("flap key.init: pinned agent pubkey (TOFU)")
	}

	ack := Message{
		Type: TypeKeyInitAck,
		Payload: KeyInitAckPayload{
			Pubkey: crypto.PubKeyHex(h.centerKeyPair.PublicKey),
			Nonce:  nonce,
		},
	}
	if !h.writeFlapPlain(fc, ack) {
		fc.Close()
		return
	}

	fc.EnableEncryption(encKey)
	h.Activate(fc)
	flog.WithField("agent_id", fc.AgentID).Info("flap key exchange complete, encryption enabled")
}

// writeFlapPlain writes a single message to the agent unencrypted, bypassing the
// send queue. Used only for the key.init_ack, which must reach the agent before
// either side switches to encrypted framing.
func (h *FlapHub) writeFlapPlain(fc *FlapConn, msg Message) bool {
	data, err := json.Marshal(msg)
	if err != nil {
		flog.WithError(err).WithField("agent_id", fc.AgentID).Error("flap: marshal key.init_ack failed")
		return false
	}
	fc.writeMu.Lock()
	fc.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	err = fc.Conn.WriteMessage(websocket.TextMessage, data)
	fc.writeMu.Unlock()
	if err != nil {
		flog.WithError(err).WithField("agent_id", fc.AgentID).Error("flap: write key.init_ack failed")
		return false
	}
	return true
}
