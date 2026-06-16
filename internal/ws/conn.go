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

type AgentConn struct {
	NodeID      string
	Conn        *websocket.Conn
	mu          sync.Mutex
	writeMu     sync.Mutex
	pending     map[string]chan Message
	sendCh      chan []byte
	closeCh     chan struct{}
	closeOnce   sync.Once
	pendingAuth bool

	encrypted bool
	aead      cipher.AEAD
}

type nodeUpdEntry struct {
	ch   chan bool
	done chan struct{}
}

func (ac *AgentConn) Close() {
	ac.closeOnce.Do(func() {
		close(ac.closeCh)
		ac.Conn.Close()
	})
}

func (ac *AgentConn) EnableEncryption(sessionKey crypto.SessionKey) {
	ac.mu.Lock()
	defer ac.mu.Unlock()

	var err error
	ac.aead, err = crypto.NewAEAD(sessionKey)
	if err != nil {
		log.WithError(err).WithField("node_id", ac.NodeID).Error("failed to create AEAD")
		return
	}
	ac.encrypted = true
	log.WithField("node_id", ac.NodeID).Info("encryption enabled")
}

func (ac *AgentConn) IsEncrypted() bool {
	ac.mu.Lock()
	defer ac.mu.Unlock()
	return ac.encrypted
}

func (ac *AgentConn) DeliverResponse(msg Message) {
	ac.mu.Lock()
	ch, ok := ac.pending[msg.ID]
	ac.mu.Unlock()

	if ok {
		select {
		case ch <- msg:
		default:
		}
	}
}

func (ac *AgentConn) WritePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	defer ac.Close()

	for {
		select {
		case data := <-ac.sendCh:
			ac.writeMu.Lock()
			ac.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))

			var msgType int
			var wireData []byte
			var err error

			ac.mu.Lock()
			if ac.encrypted && ac.aead != nil {
				wireData, err = crypto.Encrypt(ac.aead, data)
				msgType = websocket.BinaryMessage
			} else {
				wireData = data
				msgType = websocket.TextMessage
			}
			ac.mu.Unlock()

			if err != nil {
				ac.writeMu.Unlock()
				log.WithError(err).WithField("node_id", ac.NodeID).Error("encrypt error")
				return
			}

			if err := ac.Conn.WriteMessage(msgType, wireData); err != nil {
				ac.writeMu.Unlock()
				log.WithError(err).WithField("node_id", ac.NodeID).Error("ws write error")
				return
			}
			ac.writeMu.Unlock()
			log.WithField("node_id", ac.NodeID).Debug("WritePump data sent")
		case <-ticker.C:
			ac.writeMu.Lock()
			ac.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := ac.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				ac.writeMu.Unlock()
				log.WithError(err).WithField("node_id", ac.NodeID).Error("ws ping error")
				return
			}
			ac.writeMu.Unlock()
		case <-ac.closeCh:
			return
		}
	}
}

func (ac *AgentConn) ReadPump(h *Hub) {
	defer h.Unregister(ac.NodeID, ac)

	ac.Conn.SetReadLimit(512 * 1024)
	ac.Conn.SetReadDeadline(time.Now().Add(90 * time.Second))
	ac.Conn.SetPongHandler(func(string) error {
		ac.Conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		return nil
	})

	for {
		msgType, data, err := ac.Conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.WithError(err).WithField("node_id", ac.NodeID).Error("ws read error")
			}
			return
		}

		ac.Conn.SetReadDeadline(time.Now().Add(90 * time.Second))

		ac.mu.Lock()
		isEncrypted := ac.encrypted && ac.aead != nil
		aead := ac.aead
		ac.mu.Unlock()

		if isEncrypted {
			if msgType != websocket.BinaryMessage {
				log.WithField("node_id", ac.NodeID).Warn("expected binary message in encrypted mode")
				continue
			}
			if aead == nil {
				continue
			}
			plain, err := crypto.Decrypt(aead, data)
			if err != nil {
				log.WithError(err).WithField("node_id", ac.NodeID).Error("decrypt error")
				continue
			}
			data = plain
		}

		var msg Message
		if err := json.Unmarshal(data, &msg); err != nil {
			log.WithError(err).WithField("node_id", ac.NodeID).Error("ws unmarshal error")
			continue
		}

		log.WithFields(logrus.Fields{"node_id": ac.NodeID, "msg_type": msg.Type}).Debug("ReadPump message received")

		switch msg.Type {
		case TypeResponse, TypeStatusResp:
			ac.DeliverResponse(msg)

		case TypeHeartbeat:
			h.handleHeartbeat(ac.NodeID, data)

		case TypeTrafficReport:
			h.handleTrafficReport(ac.NodeID, data)

		case TypePeersSync:
			h.handlePeersSync(ac)

		case TypeAgentUpdating:
			h.handleAgentUpdating(ac)

		case TypeKeyInit:
			h.handleKeyInit(ac, msg)

		case TypeKeyAuth:
			h.handleKeyAuth(ac, msg)

		default:
			log.WithFields(logrus.Fields{"msg_type": msg.Type, "node_id": ac.NodeID}).Warn("ws unknown message type")
		}
	}
}
