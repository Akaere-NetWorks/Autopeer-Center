package ws

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
)

type BotConn struct {
	ID            string
	Conn          *websocket.Conn
	mu            sync.Mutex
	writeMu       sync.Mutex
	pending       map[string]chan Message
	sendCh        chan []byte
	closeCh       chan struct{}
	closeOnce     sync.Once
	authenticated bool
}

func NewBotConn(conn *websocket.Conn) *BotConn {
	return &BotConn{
		ID:      uuid.New().String(),
		Conn:    conn,
		pending: make(map[string]chan Message),
		sendCh:  make(chan []byte, 64),
		closeCh: make(chan struct{}),
	}
}

func (bc *BotConn) Close() {
	bc.closeOnce.Do(func() {
		close(bc.closeCh)
		bc.Conn.Close()
	})
}

func (bc *BotConn) DeliverResponse(msg Message) {
	bc.mu.Lock()
	ch, ok := bc.pending[msg.ID]
	bc.mu.Unlock()

	if ok {
		select {
		case ch <- msg:
		default:
		}
	}
}

func (bc *BotConn) WritePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	defer bc.Close()

	for {
		select {
		case data := <-bc.sendCh:
			bc.writeMu.Lock()
			bc.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := bc.Conn.WriteMessage(websocket.TextMessage, data); err != nil {
				bc.writeMu.Unlock()
				log.WithError(err).WithField("bot_id", bc.ID).Error("bot ws write error")
				return
			}
			bc.writeMu.Unlock()
		case <-ticker.C:
			bc.writeMu.Lock()
			bc.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := bc.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				bc.writeMu.Unlock()
				return
			}
			bc.writeMu.Unlock()
		case <-bc.closeCh:
			return
		}
	}
}

func (bc *BotConn) ReadPump(h *Hub) {
	defer func() {
		if bc.authenticated {
			h.UnregisterBot(bc.ID, bc)
		} else {
			bc.Close()
		}
	}()

	bc.Conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	bc.Conn.SetPongHandler(func(string) error {
		bc.Conn.SetReadDeadline(time.Now().Add(120 * time.Second))
		return nil
	})

	for {
		_, data, err := bc.Conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.WithError(err).WithField("bot_id", bc.ID).Error("bot ws read error")
			}
			return
		}

		if !bc.authenticated {
	bc.Conn.SetReadLimit(256 * 1024)
	bc.Conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		} else {
			bc.Conn.SetReadDeadline(time.Now().Add(120 * time.Second))
		}

		var msg Message
		if err := json.Unmarshal(data, &msg); err != nil {
			log.WithError(err).WithField("bot_id", bc.ID).Error("bot ws unmarshal error")
			continue
		}

		log.WithFields(logrus.Fields{"bot_id": bc.ID, "msg_type": msg.Type}).Debug("bot message received")

		if !bc.authenticated {
			if msg.Type == TypeBotAuth {
				h.handleBotAuth(bc, msg)
				if bc.authenticated {
					h.RegisterBot(bc)
					bc.Conn.SetReadDeadline(time.Now().Add(120 * time.Second))
				}
			} else {
				log.WithField("bot_id", bc.ID).Warn("unauthenticated bot message, closing")
				bc.Close()
				return
			}
			continue
		}

		switch msg.Type {
		case TypeBotPing:
			if h.GetBotSetting("bot_ping_enabled") == "true" {
				select {
				case h.botSem <- struct{}{}:
					go func() {
						defer func() { <-h.botSem }()
						h.handleBotPing(bc, msg)
					}()
				default:
					h.sendToBot(bc, TypeBotPingResult, msg.ID, BotPingResult{Target: "", Results: []PingResultEntry{{Success: false, Error: "too many concurrent commands"}}})
				}
			} else {
				h.sendToBot(bc, TypeBotPingResult, msg.ID, BotPingResult{Target: "", Results: []PingResultEntry{{Success: false, Error: "ping command is disabled"}}})
			}
		case TypeBotTrace:
			if h.GetBotSetting("bot_trace_enabled") == "true" {
				select {
				case h.botSem <- struct{}{}:
					go func() {
						defer func() { <-h.botSem }()
						h.handleBotTrace(bc, msg)
					}()
				default:
					h.sendToBot(bc, TypeBotTraceResult, msg.ID, BotTraceResult{Target: "", Results: []TraceResultEntry{{Success: false, Error: "too many concurrent commands"}}})
				}
			} else {
				h.sendToBot(bc, TypeBotTraceResult, msg.ID, BotTraceResult{Target: "", Results: []TraceResultEntry{{Success: false, Error: "trace command is disabled"}}})
			}
		case TypeBotStatus:
			if h.GetBotSetting("bot_status_enabled") == "true" {
				select {
				case h.botSem <- struct{}{}:
					go func() {
						defer func() { <-h.botSem }()
						h.handleBotStatus(bc, msg)
					}()
				default:
					h.sendToBot(bc, TypeBotStatusResult, msg.ID, BotStatusResult{})
				}
			} else {
				h.sendToBot(bc, TypeBotStatusResult, msg.ID, BotStatusResult{})
			}
		case TypeBotNodes:
			if h.GetBotSetting("bot_nodes_enabled") == "true" {
				select {
				case h.botSem <- struct{}{}:
					go func() {
						defer func() { <-h.botSem }()
						h.handleBotNodes(bc, msg)
					}()
				default:
					h.sendToBot(bc, TypeBotNodesResult, msg.ID, BotNodesResult{})
				}
			} else {
				h.sendToBot(bc, TypeBotNodesResult, msg.ID, BotNodesResult{})
			}
		case TypeBotCommandStats:
			select {
			case h.botSem <- struct{}{}:
				go func() {
					defer func() { <-h.botSem }()
					h.handleBotCommandStats(msg)
				}()
			default:
			}
		case TypeBotBindRequest:
			select {
			case h.botSem <- struct{}{}:
				go func() {
					defer func() { <-h.botSem }()
					h.handleBotBindRequest(bc, msg)
				}()
			default:
				h.sendToBot(bc, TypeBotBindResult, msg.ID, BotBindResultPayload{Success: false, Error: "too many concurrent commands"})
			}
		case TypeBotBindVerify:
			select {
			case h.botSem <- struct{}{}:
				go func() {
					defer func() { <-h.botSem }()
					h.handleBotBindVerify(bc, msg)
				}()
			default:
				h.sendToBot(bc, TypeBotBindResult, msg.ID, BotBindResultPayload{Success: false, Error: "too many concurrent commands"})
			}
		case TypeBotWebBind:
			select {
			case h.botSem <- struct{}{}:
				go func() {
					defer func() { <-h.botSem }()
					h.handleBotWebBind(bc, msg)
				}()
			default:
				h.sendToBot(bc, TypeBotBindResult, msg.ID, BotBindResultPayload{Success: false, Error: "too many concurrent commands"})
			}
		case TypeBotUnbind:
			select {
			case h.botSem <- struct{}{}:
				go func() {
					defer func() { <-h.botSem }()
					h.handleBotUnbind(bc, msg)
				}()
			default:
				h.sendToBot(bc, TypeBotUnbindResult, msg.ID, BotBindResultPayload{Success: false, Error: "too many concurrent commands"})
			}
		case TypeBotWhoami:
			select {
			case h.botSem <- struct{}{}:
				go func() {
					defer func() { <-h.botSem }()
					h.handleBotWhoami(bc, msg)
				}()
			default:
				h.sendToBot(bc, TypeBotWhoamiResult, msg.ID, BotWhoamiResultPayload{})
			}
		case TypeBotUserPeers:
			select {
			case h.botSem <- struct{}{}:
				go func() {
					defer func() { <-h.botSem }()
					h.handleBotUserPeers(bc, msg)
				}()
			default:
				h.sendToBot(bc, TypeBotUserPeersResult, msg.ID, BotUserPeersResultPayload{})
			}
		case TypeBotUserPeerDetail:
			select {
			case h.botSem <- struct{}{}:
				go func() {
					defer func() { <-h.botSem }()
					h.handleBotUserPeerDetail(bc, msg)
				}()
			default:
				h.sendToBot(bc, TypeBotUserPeerDetailResult, msg.ID, BotUserPeerDetailResultPayload{})
			}
		case TypeBotUserPeerCreate:
			select {
			case h.botSem <- struct{}{}:
				go func() {
					defer func() { <-h.botSem }()
					h.handleBotUserPeerCreate(bc, msg)
				}()
			default:
				h.sendToBot(bc, TypeBotUserPeerCreateResult, msg.ID, BotUserPeerCreateResultPayload{Success: false, Error: "too many concurrent commands"})
			}
		case TypeBotUserPeerDelete:
			select {
			case h.botSem <- struct{}{}:
				go func() {
					defer func() { <-h.botSem }()
					h.handleBotUserPeerDelete(bc, msg)
				}()
			default:
				h.sendToBot(bc, TypeBotUserPeerDeleteResult, msg.ID, BotUserPeerDeleteResultPayload{Success: false, Error: "too many concurrent commands"})
			}
		case TypeBotUserPeerMetrics:
			select {
			case h.botSem <- struct{}{}:
				go func() {
					defer func() { <-h.botSem }()
					h.handleBotUserPeerMetrics(bc, msg)
				}()
			default:
				h.sendToBot(bc, TypeBotUserPeerMetricsResult, msg.ID, BotUserPeerMetricsResultPayload{})
			}
		case TypeBotUserPeerUpdate:
			select {
			case h.botSem <- struct{}{}:
				go func() {
					defer func() { <-h.botSem }()
					h.handleBotUserPeerUpdate(bc, msg)
				}()
			default:
				h.sendToBot(bc, TypeBotUserPeerUpdateResult, msg.ID, BotUserPeerUpdateResultPayload{Success: false, Error: "too many concurrent commands"})
			}
		case TypeBotAdminPeers:
			select {
			case h.botSem <- struct{}{}:
				go func() {
					defer func() { <-h.botSem }()
					h.handleBotAdminPeers(bc, msg)
				}()
			default:
				h.sendToBot(bc, TypeBotAdminPeersResult, msg.ID, BotAdminPeersResultPayload{})
			}
		case TypeBotAdminPeerAction:
			select {
			case h.botSem <- struct{}{}:
				go func() {
					defer func() { <-h.botSem }()
					h.handleBotAdminPeerAction(bc, msg)
				}()
			default:
				h.sendToBot(bc, TypeBotAdminPeerActionResult, msg.ID, BotAdminPeerActionResultPayload{Success: false, Error: "too many concurrent commands"})
			}
		case TypeBotAdminPeerDetail:
			select {
			case h.botSem <- struct{}{}:
				go func() {
					defer func() { <-h.botSem }()
					h.handleBotAdminPeerDetail(bc, msg)
				}()
			default:
				h.sendToBot(bc, TypeBotAdminPeerDetailResult, msg.ID, BotAdminPeerDetailResultPayload{})
			}
		case TypeBotAdminNodes:
			select {
			case h.botSem <- struct{}{}:
				go func() {
					defer func() { <-h.botSem }()
					h.handleBotAdminNodes(bc, msg)
				}()
			default:
				h.sendToBot(bc, TypeBotAdminNodesResult, msg.ID, BotAdminNodesResultPayload{})
			}
		case TypeBotAdminNodeDetail:
			select {
			case h.botSem <- struct{}{}:
				go func() {
					defer func() { <-h.botSem }()
					h.handleBotAdminNodeDetail(bc, msg)
				}()
			default:
				h.sendToBot(bc, TypeBotAdminNodeDetailResult, msg.ID, BotAdminNodeDetailResultPayload{})
			}
		case TypeBotAdminNodeUpdate:
			select {
			case h.botSem <- struct{}{}:
				go func() {
					defer func() { <-h.botSem }()
					h.handleBotAdminNodeUpdate(bc, msg)
				}()
			default:
				h.sendToBot(bc, TypeBotAdminNodeUpdateResult, msg.ID, BotAdminNodeUpdateResultPayload{Success: false, Error: "too many concurrent commands"})
			}
		case TypeBotAdminAudit:
			select {
			case h.botSem <- struct{}{}:
				go func() {
					defer func() { <-h.botSem }()
					h.handleBotAdminAudit(bc, msg)
				}()
			default:
				h.sendToBot(bc, TypeBotAdminAuditResult, msg.ID, BotAdminAuditResultPayload{})
			}
		default:
			log.WithFields(logrus.Fields{"msg_type": msg.Type, "bot_id": bc.ID}).Warn("unknown bot message type")
		}
	}
}
