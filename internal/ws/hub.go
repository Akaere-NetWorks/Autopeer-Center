package ws

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/akaere/autopeer-center/internal/cache"
	"github.com/akaere/autopeer-center/internal/crypto"
	"github.com/akaere/autopeer-center/internal/lock"
	"github.com/akaere/autopeer-center/internal/model"
	"github.com/akaere/autopeer-center/internal/repository"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
)

var log = logrus.WithField("pkg", "ws.hub")

type Hub struct {
	mu               sync.RWMutex
	conns            map[string]*AgentConn
	botMu            sync.RWMutex
	botConns         map[string]*BotConn
	botAuthToken     string
	botAuthTokenHash string
	botSem           chan struct{}

	nodes       repository.NodeRepository
	peers       repository.PeerRepository
	metrics     repository.MetricsRepository
	bot         repository.BotRepository
	botBindings repository.BotBindingRepository
	audit       repository.AuditRepository
	stats       repository.StatsRepository
	cache       *cache.Cache

	centerKeyPair *crypto.KeyPair

	nodeUpd map[string]*nodeUpdEntry

	adminEmails         []string
	sendEmail           func(to, template string, vars map[string]interface{})
	getSetting          func(key string) string
	getThreshold        func(key string) string
	getEmailLevel               func(asn int64) int
	notificationAllowed         func(asn int64, key string) bool
	telegramNotificationAllowed func(asn int64, key string) bool

	lookupASNEmail    func(asn int64) (string, error)
	sendVerifyCode    func(to string, asn int64, code string) error
	createLoginCode   func(asn int64, email, codeHash string, expiresAt time.Time) error
	getValidLoginCode func(ctx context.Context, asn int64) (*model.UserLoginCode, error)
	markLoginCodeUsed func(ctx context.Context, id string) (bool, error)
	incrCodeFailures  func(ctx context.Context, id string) error

	locker lock.Locker
}

// SetLocker wires a distributed locker used to serialize peer state mutations
// (e.g. bot-initiated create/delete) so concurrent operations can't race.
func (h *Hub) SetLocker(l lock.Locker) { h.locker = l }

func NewHub(
	nodes repository.NodeRepository,
	peers repository.PeerRepository,
	metrics repository.MetricsRepository,
	bot repository.BotRepository,
	botBindings repository.BotBindingRepository,
	audit repository.AuditRepository,
	stats repository.StatsRepository,
	c *cache.Cache,
) *Hub {
	return &Hub{
		conns:       make(map[string]*AgentConn),
		botConns:    make(map[string]*BotConn),
		nodes:       nodes,
		peers:       peers,
		metrics:     metrics,
		bot:         bot,
		botBindings: botBindings,
		audit:       audit,
		stats:       stats,
		cache:       c,
		nodeUpd:     make(map[string]*nodeUpdEntry),
		botSem:      make(chan struct{}, 64),
	}
}

func (h *Hub) SetBotAuthToken(token string) {
	h.botAuthToken = token
}

func (h *Hub) LoadBotSettings() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	settings, err := h.bot.LoadAllSettings(ctx)
	if err != nil {
		log.WithError(err).Warn("failed to load bot settings")
		return
	}

	for _, s := range settings {
		if s.SettingKey == "bot_auth_token" {
			if s.SettingValueHash != nil && *s.SettingValueHash != "" {
				h.botAuthToken = ""
				h.botAuthTokenHash = *s.SettingValueHash
			} else if s.SettingValue != "" {
				h.botAuthToken = s.SettingValue
				h.botAuthTokenHash = ""
			}
		}
	}
	log.Info("bot settings loaded from database")
}

func (h *Hub) GetBotSetting(key string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	setting, err := h.bot.GetSetting(ctx, key)
	if err != nil {
		return ""
	}

	if key == "bot_auth_token" {
		if setting.SettingValueHash != nil && *setting.SettingValueHash != "" {
			return *setting.SettingValueHash
		}
	}
	return setting.SettingValue
}

func (h *Hub) PushSettingsToBots() {
	settings := h.loadAllBotSettings()
	if len(settings) == 0 {
		return
	}
	delete(settings, "bot_auth_token")

	h.botMu.RLock()
	for _, bc := range h.botConns {
		h.sendToBot(bc, TypeBotSettingsUpdate, "", BotSettingsPayload{Settings: settings})
	}
	h.botMu.RUnlock()
}

func (h *Hub) loadAllBotSettings() map[string]string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	settings, err := h.bot.LoadAllSettings(ctx)
	if err != nil {
		return nil
	}

	m := make(map[string]string)
	for _, s := range settings {
		k := s.SettingKey
		v := s.SettingValue
		if k == "bot_auth_token" {
			v = "[REDACTED]"
		}
		m[k] = v
	}
	return m
}

func (h *Hub) SetCenterKeyPair(kp *crypto.KeyPair) {
	h.centerKeyPair = kp
	log.Info("center key pair set")
}

func (h *Hub) SetNotifier(adminEmails []string, sendEmail func(to, template string, vars map[string]interface{}), getSetting func(key string) string, getThreshold func(key string) string) {
	h.adminEmails = adminEmails
	h.sendEmail = sendEmail
	h.getSetting = getSetting
	h.getThreshold = getThreshold
}

func (h *Hub) SetEmailLevelFn(fn func(asn int64) int) {
	h.getEmailLevel = fn
}

func (h *Hub) SetNotificationAllowedFn(fn func(asn int64, key string) bool) {
	h.notificationAllowed = fn
}

func (h *Hub) SetTelegramNotificationAllowedFn(fn func(asn int64, key string) bool) {
	h.telegramNotificationAllowed = fn
}

func (h *Hub) SetASNEmailLookup(fn func(asn int64) (string, error)) {
	h.lookupASNEmail = fn
}

func (h *Hub) SetVerifyCodeSender(fn func(to string, asn int64, code string) error) {
	h.sendVerifyCode = fn
}

func (h *Hub) SetLoginCodeStore(
	createFn func(asn int64, email, codeHash string, expiresAt time.Time) error,
	getFn func(ctx context.Context, asn int64) (*model.UserLoginCode, error),
	markUsedFn func(ctx context.Context, id string) (bool, error),
	incrFailFn func(ctx context.Context, id string) error,
) {
	h.createLoginCode = createFn
	h.getValidLoginCode = getFn
	h.markLoginCodeUsed = markUsedFn
	h.incrCodeFailures = incrFailFn
}

func (h *Hub) Shutdown() {
	log.Info("shutting down hub, closing all agent connections")
	h.mu.Lock()
	conns := make([]*AgentConn, 0, len(h.conns))
	for _, ac := range h.conns {
		conns = append(conns, ac)
	}
	h.mu.Unlock()

	for _, ac := range conns {
		msg := websocket.FormatCloseMessage(websocket.CloseGoingAway, "server shutting down")
		ac.Conn.WriteMessage(websocket.CloseMessage, msg)
		ac.Close()
	}

	h.botMu.Lock()
	bots := make([]*BotConn, 0, len(h.botConns))
	for _, bc := range h.botConns {
		bots = append(bots, bc)
	}
	h.botMu.Unlock()

	for _, bc := range bots {
		msg := websocket.FormatCloseMessage(websocket.CloseGoingAway, "server shutting down")
		bc.Conn.WriteMessage(websocket.CloseMessage, msg)
		bc.Close()
	}
}

func (h *Hub) Register(nodeID string, conn *websocket.Conn) *AgentConn {
	h.mu.Lock()
	defer h.mu.Unlock()

	log.WithField("node_id", nodeID).Debug("Register (pending auth)")

	if existing, ok := h.conns[nodeID]; ok {
		existing.Close()
		delete(h.conns, nodeID)
	}

	ac := &AgentConn{
		NodeID:      nodeID,
		Conn:        conn,
		pending:     make(map[string]chan Message),
		sendCh:      make(chan []byte, 64),
		closeCh:     make(chan struct{}),
		pendingAuth: true,
	}

	if old, ok := h.nodeUpd[nodeID]; ok {
		close(old.done)
	}
	entry := &nodeUpdEntry{ch: make(chan bool, 4), done: make(chan struct{})}
	h.nodeUpd[nodeID] = entry
	go h.drainNodeUpdates(nodeID, entry.ch, entry.done, entry)

	return ac
}

func (h *Hub) Activate(ac *AgentConn) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if !ac.pendingAuth {
		return
	}
	ac.pendingAuth = false

	if existing, ok := h.conns[ac.NodeID]; ok && existing != ac {
		existing.Close()
	}
	h.conns[ac.NodeID] = ac

	if entry, ok := h.nodeUpd[ac.NodeID]; ok {
		select {
		case entry.ch <- true:
		default:
		}
	}

	log.WithField("node_id", ac.NodeID).Info("agent activated in hub")
}

func (h *Hub) Unregister(nodeID string, ac *AgentConn) {
	h.mu.Lock()
	defer h.mu.Unlock()

	log.WithField("node_id", nodeID).Debug("Unregister")

	if cur, ok := h.conns[nodeID]; ok && cur == ac {
		cur.Close()
		delete(h.conns, nodeID)
	}

	if ac.pendingAuth {
		ac.Close()
	}

	if entry, ok := h.nodeUpd[nodeID]; ok {
		select {
		case entry.ch <- false:
		default:
			log.WithField("node_id", nodeID).Warn("nodeUpd channel full, using direct DB update")
			offCtx, offCancel := context.WithTimeout(context.Background(), 5*time.Second)
			h.nodes.SetAgentState(offCtx, nodeID, "offline")
			h.notifyNodeOffline(offCtx, nodeID, true)
			offCancel()
			close(entry.done)
			delete(h.nodeUpd, nodeID)
		}
	}
}

func (h *Hub) drainNodeUpdates(nodeID string, ch chan bool, done chan struct{}, entry *nodeUpdEntry) {
	for {
		select {
		case <-done:
			return
		case online, ok := <-ch:
			if !ok {
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if online {
				h.nodes.SetOnline(ctx, nodeID, true)
			} else {
				h.nodes.SetAgentState(ctx, nodeID, "offline")
				h.notifyNodeOffline(ctx, nodeID, true)
				h.mu.Lock()
				if h.nodeUpd[nodeID] == entry {
					delete(h.nodeUpd, nodeID)
				}
				h.mu.Unlock()
				cancel()
				return
			}
			cancel()
		}
	}
}

func (h *Hub) GetConn(nodeID string) *AgentConn {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.conns[nodeID]
}

func (h *Hub) IsOnline(nodeID string) bool {
	ac := h.GetConn(nodeID)
	return ac != nil && !ac.pendingAuth
}

func (h *Hub) isBGPDownNotified(peerID string) bool {
	var v bool
	ok, _ := h.cache.Get(cache.BucketAlerts, cache.PeerKey(cache.AlertBGPDown, peerID), &v)
	return ok && v
}

func (h *Hub) setBGPDownNotified(peerID string, v bool) {
	if v {
		h.cache.Put(cache.BucketAlerts, cache.PeerKey(cache.AlertBGPDown, peerID), true, 24*time.Hour)
	} else {
		h.cache.Delete(cache.BucketAlerts, cache.PeerKey(cache.AlertBGPDown, peerID))
	}
}

func (h *Hub) incBGPFailCount(peerID string) int {
	v, _ := h.cache.IncInt(cache.BucketAlerts, cache.PeerKey(cache.AlertBGPFail, peerID), 1*time.Hour)
	return v
}

func (h *Hub) resetBGPFailCount(peerID string) {
	h.cache.Delete(cache.BucketAlerts, cache.PeerKey(cache.AlertBGPFail, peerID))
}

func (h *Hub) isHandshakeNotified(peerID string) bool {
	var v bool
	ok, _ := h.cache.Get(cache.BucketAlerts, cache.PeerKey(cache.AlertHandshake, peerID), &v)
	return ok && v
}

func (h *Hub) setHandshakeNotified(peerID string, v bool) {
	if v {
		h.cache.Put(cache.BucketAlerts, cache.PeerKey(cache.AlertHandshake, peerID), true, 24*time.Hour)
	} else {
		h.cache.Delete(cache.BucketAlerts, cache.PeerKey(cache.AlertHandshake, peerID))
	}
}

func (h *Hub) isNodeOfflineNotified(nodeID string) bool {
	var v bool
	ok, _ := h.cache.Get(cache.BucketAlerts, cache.PeerKey(cache.AlertNodeOff, nodeID), &v)
	return ok && v
}

func (h *Hub) setNodeOfflineNotified(nodeID string, v bool) {
	if v {
		h.cache.Put(cache.BucketAlerts, cache.PeerKey(cache.AlertNodeOff, nodeID), true, 24*time.Hour)
	} else {
		h.cache.Delete(cache.BucketAlerts, cache.PeerKey(cache.AlertNodeOff, nodeID))
	}
}

func (h *Hub) ClearPeerAlerts(peerID string) {
	h.cache.Delete(cache.BucketAlerts, cache.PeerKey(cache.AlertBGPDown, peerID))
	h.cache.Delete(cache.BucketAlerts, cache.PeerKey(cache.AlertBGPFail, peerID))
	h.cache.Delete(cache.BucketAlerts, cache.PeerKey(cache.AlertHandshake, peerID))
	h.cache.Delete(cache.BucketAlerts, cache.PeerKey(cache.AlertEndpoint, peerID))
}

func (h *Hub) ClearNodeAlerts(nodeID string) {
	h.cache.Delete(cache.BucketAlerts, cache.PeerKey(cache.AlertNodeOff, nodeID))
}

func (h *Hub) SendCommand(nodeID string, msgType string, payload any) (*Message, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return h.SendCommandContext(ctx, nodeID, msgType, payload)
}

func (h *Hub) SendCommandContext(ctx context.Context, nodeID string, msgType string, payload any) (*Message, error) {
	log.WithFields(logrus.Fields{"node_id": nodeID, "msg_type": msgType}).Debug("SendCommand")
	if ctx == nil {
		ctx = context.Background()
	}

	ac := h.GetConn(nodeID)
	if ac == nil {
		return nil, fmt.Errorf("agent not connected for node %s", nodeID)
	}
	if ac.pendingAuth {
		return nil, fmt.Errorf("agent for node %s has not completed handshake", nodeID)
	}

	reqID := uuid.New().String()
	msg := Message{
		Type:    msgType,
		ID:      reqID,
		Payload: payload,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("marshal message: %w", err)
	}

	respCh := make(chan Message, 1)
	ac.mu.Lock()
	ac.pending[reqID] = respCh
	ac.mu.Unlock()

	defer func() {
		ac.mu.Lock()
		delete(ac.pending, reqID)
		ac.mu.Unlock()
	}()

	select {
	case ac.sendCh <- data:
	default:
		log.WithField("node_id", nodeID).Warn("send buffer full")
		return nil, fmt.Errorf("send buffer full for node %s", nodeID)
	}

	select {
	case resp := <-respCh:
		return &resp, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-ac.closeCh:
		return nil, fmt.Errorf("agent disconnected during command")
	}
}

func (h *Hub) RegisterBot(bc *BotConn) {
	h.botMu.Lock()
	defer h.botMu.Unlock()
	h.botConns[bc.ID] = bc
	log.WithField("bot_id", bc.ID).Info("bot registered")
}

func (h *Hub) UnregisterBot(botID string, bc *BotConn) {
	h.botMu.Lock()
	defer h.botMu.Unlock()

	if cur, ok := h.botConns[botID]; ok && cur == bc {
		cur.Close()
		delete(h.botConns, botID)
	}
	log.WithField("bot_id", botID).Info("bot unregistered")
}

func (h *Hub) HasBotConnections() bool {
	h.botMu.RLock()
	defer h.botMu.RUnlock()
	return len(h.botConns) > 0
}

type HubStat struct {
	ConnectedAgents  int            `json:"connected_agents"`
	PendingAuthCount int            `json:"pending_auth_count"`
	BotConnected     bool           `json:"bot_connected"`
	BotCount         int            `json:"bot_count"`
	PendingCommands  map[string]int `json:"pending_commands"`
}

func (h *Hub) Stat() HubStat {
	h.mu.RLock()
	connected := len(h.conns)
	pendingAuth := 0
	pendingCmds := make(map[string]int, len(h.conns))
	for nodeID, ac := range h.conns {
		if ac.pendingAuth {
			pendingAuth++
		}
		ac.mu.Lock()
		pendingCmds[nodeID] = len(ac.pending)
		ac.mu.Unlock()
	}
	h.mu.RUnlock()

	h.botMu.RLock()
	botCount := len(h.botConns)
	h.botMu.RUnlock()

	return HubStat{
		ConnectedAgents:  connected,
		PendingAuthCount: pendingAuth,
		BotConnected:     botCount > 0,
		BotCount:         botCount,
		PendingCommands:  pendingCmds,
	}
}

func (h *Hub) notifyPubkeyReset(nodeID string) {
	if h.sendEmail == nil || len(h.adminEmails) == 0 {
		return
	}
	for _, email := range h.adminEmails {
		h.sendEmail(email, "agent_key_reset", map[string]interface{}{
			"node_id": nodeID,
		})
	}
}

func (h *Hub) BroadcastToBots(msgType string, payload interface{}) {
	msg := Message{Type: msgType, Payload: payload}
	data, err := json.Marshal(msg)
	if err != nil {
		log.WithError(err).Error("marshal broadcast message")
		return
	}

	h.botMu.RLock()
	defer h.botMu.RUnlock()
	for _, bc := range h.botConns {
		select {
		case bc.sendCh <- data:
		default:
			log.WithField("bot_id", bc.ID).Warn("bot send buffer full during broadcast")
		}
	}
}

func (h *Hub) NotifyBotUser(tgUserID int64, event string, data interface{}) {
	h.BroadcastToBots(TypeBotNotify, BotNotifyPayload{
		Channel: "telegram",
		Target:  BotNotifyTarget{TgUserID: tgUserID},
		Event:   event,
		Data:    data,
	})
}

func (h *Hub) NotifyBotAdmin(event string, data interface{}) {
	h.BroadcastToBots(TypeBotNotify, BotNotifyPayload{
		Channel: "telegram",
		Target:  BotNotifyTarget{AdminChat: true},
		Event:   event,
		Data:    data,
	})
}

// NotifyPeerBot looks up the Telegram binding for the given ASN, checks the
// telegramNotificationAllowed gate, and sends a per-user bot notification.
func (h *Hub) NotifyPeerBot(asn int64, notifKey string, event string, data interface{}) {
	if h.botBindings == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	binding, err := h.botBindings.GetByASN(ctx, asn)
	if err != nil || binding == nil {
		return
	}
	if h.telegramNotificationAllowed != nil && !h.telegramNotificationAllowed(asn, notifKey) {
		return
	}
	h.NotifyBotUser(binding.TgUserID, event, data)
}
