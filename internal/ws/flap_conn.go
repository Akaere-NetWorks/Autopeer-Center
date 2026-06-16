package ws

import (
	"crypto/cipher"
	"encoding/json"
	"sync"
	"time"

	"github.com/akaere/autopeer-center/internal/crypto"
	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
)

// FlapConn is a live connection to a flapalerted-agent. It mirrors AgentConn:
// the connection is bootstrapped with a bearer token at the HTTP layer, then an
// X25519 key exchange (key.init) pins the agent's public key and enables
// ChaCha20-Poly1305 framing. Until that handshake completes the connection is
// pendingAuth and is not registered as queryable in the hub.
type FlapConn struct {
	AgentID string
	Conn    *websocket.Conn

	mu        sync.Mutex
	writeMu   sync.Mutex
	pending   map[string]chan Message
	sendCh    chan []byte
	closeCh   chan struct{}
	closeOnce sync.Once

	encrypted bool
	aead      cipher.AEAD
	authed    bool

	capsMu sync.RWMutex
	caps   FlapCapabilities
}

func newFlapConn(agentID string, conn *websocket.Conn) *FlapConn {
	return &FlapConn{
		AgentID: agentID,
		Conn:    conn,
		pending: make(map[string]chan Message),
		sendCh:  make(chan []byte, 32),
		closeCh: make(chan struct{}),
	}
}

func (fc *FlapConn) Close() {
	fc.closeOnce.Do(func() {
		close(fc.closeCh)
		fc.Conn.Close()
	})
}

func (fc *FlapConn) EnableEncryption(sessionKey crypto.SessionKey) {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	aead, err := crypto.NewAEAD(sessionKey)
	if err != nil {
		flog.WithError(err).WithField("agent_id", fc.AgentID).Error("flap: failed to create AEAD")
		return
	}
	fc.aead = aead
	fc.encrypted = true
	flog.WithField("agent_id", fc.AgentID).Info("flap: encryption enabled")
}

func (fc *FlapConn) setAuthed() {
	fc.mu.Lock()
	fc.authed = true
	fc.mu.Unlock()
}

func (fc *FlapConn) isAuthed() bool {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	return fc.authed
}

func (fc *FlapConn) Capabilities() FlapCapabilities {
	fc.capsMu.RLock()
	defer fc.capsMu.RUnlock()
	return fc.caps
}

func (fc *FlapConn) setCapabilities(c FlapCapabilities) {
	fc.capsMu.Lock()
	fc.caps = c
	fc.capsMu.Unlock()
}

func (fc *FlapConn) deliverResponse(msg Message) {
	fc.mu.Lock()
	ch, ok := fc.pending[msg.ID]
	fc.mu.Unlock()
	if ok {
		select {
		case ch <- msg:
		default:
		}
	}
}

func (fc *FlapConn) WritePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	defer fc.Close()

	for {
		select {
		case data := <-fc.sendCh:
			fc.writeMu.Lock()
			fc.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))

			var msgType int
			var wireData []byte
			var err error

			fc.mu.Lock()
			if fc.encrypted && fc.aead != nil {
				wireData, err = crypto.Encrypt(fc.aead, data)
				msgType = websocket.BinaryMessage
			} else {
				wireData = data
				msgType = websocket.TextMessage
			}
			fc.mu.Unlock()

			if err != nil {
				fc.writeMu.Unlock()
				flog.WithError(err).WithField("agent_id", fc.AgentID).Error("flap ws encrypt error")
				return
			}
			if err := fc.Conn.WriteMessage(msgType, wireData); err != nil {
				fc.writeMu.Unlock()
				flog.WithError(err).WithField("agent_id", fc.AgentID).Error("flap ws write error")
				return
			}
			fc.writeMu.Unlock()
		case <-ticker.C:
			fc.writeMu.Lock()
			fc.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := fc.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				fc.writeMu.Unlock()
				return
			}
			fc.writeMu.Unlock()
		case <-fc.closeCh:
			return
		}
	}
}

func (fc *FlapConn) ReadPump(h *FlapHub) {
	defer h.Unregister(fc.AgentID, fc)

	fc.Conn.SetReadLimit(1 << 20)
	fc.Conn.SetReadDeadline(time.Now().Add(90 * time.Second))
	fc.Conn.SetPongHandler(func(string) error {
		fc.Conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		return nil
	})

	for {
		msgType, data, err := fc.Conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				flog.WithError(err).WithField("agent_id", fc.AgentID).Debug("flap ws read error")
			}
			return
		}
		fc.Conn.SetReadDeadline(time.Now().Add(90 * time.Second))

		fc.mu.Lock()
		isEncrypted := fc.encrypted && fc.aead != nil
		aead := fc.aead
		fc.mu.Unlock()

		if isEncrypted {
			if msgType != websocket.BinaryMessage {
				flog.WithField("agent_id", fc.AgentID).Warn("flap: expected binary message in encrypted mode")
				continue
			}
			plain, decErr := crypto.Decrypt(aead, data)
			if decErr != nil {
				flog.WithError(decErr).WithField("agent_id", fc.AgentID).Error("flap ws decrypt error")
				continue
			}
			data = plain
		}

		var msg Message
		if err := json.Unmarshal(data, &msg); err != nil {
			flog.WithError(err).WithField("agent_id", fc.AgentID).Warn("flap ws unmarshal error")
			continue
		}

		// The key exchange must complete before any other message is honoured.
		if !fc.isAuthed() && msg.Type != TypeKeyInit {
			flog.WithFields(logrus.Fields{"agent_id": fc.AgentID, "msg_type": msg.Type}).Warn("flap ws message before key exchange, ignoring")
			continue
		}

		switch msg.Type {
		case TypeKeyInit:
			h.handleFlapKeyInit(fc, msg)
		case TypeFlapRegister:
			var p FlapRegisterPayload
			if decodePayload(msg, &p) == nil {
				fc.setCapabilities(p.Capabilities)
				h.registry.TouchSeen(h.baseCtx(), fc.AgentID, p.Capabilities.Version)
				flog.WithFields(logrus.Fields{"agent_id": fc.AgentID, "version": p.Capabilities.Version}).Info("flap agent registered")
			}
		case TypeFlapSnapshot, TypeFlapPrefix, TypeFlapMetrics:
			fc.deliverResponse(msg)
		default:
			flog.WithFields(logrus.Fields{"agent_id": fc.AgentID, "msg_type": msg.Type}).Debug("flap ws unknown message type")
		}
	}
}

// decodePayload re-marshals the decoded Message.Payload into a typed struct.
func decodePayload(msg Message, v any) error {
	raw, err := json.Marshal(msg.Payload)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}
