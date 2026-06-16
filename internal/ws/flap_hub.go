package ws

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/akaere/autopeer-center/internal/crypto"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/singleflight"
)

var flog = logrus.WithField("pkg", "ws.flaphub")

const (
	flapRPCTimeout    = 15 * time.Second
	flapCacheTTL      = 3 * time.Second
	flapPollInterval  = 5 * time.Second
	flapSubBuffer     = 8
)

// FlapSSEEvent is one server-sent event delivered to a public subscriber.
type FlapSSEEvent struct {
	Type string          `json:"-"`
	Data json.RawMessage `json:"-"`
}

type cachedSnapshot struct {
	snap FlapSnapshot
	at   time.Time
}

// FlapHub manages live flap-agent connections (keyed by agent id), relays public
// snapshot/detail/metrics requests to them via RPC with a short cache, and fans
// out a 5s SSE poll to subscribers.
type FlapHub struct {
	mu    sync.RWMutex
	conns map[string]*FlapConn

	registry      FlapRegistry
	centerKeyPair *crypto.KeyPair

	cacheMu sync.Mutex
	cache   map[string]cachedSnapshot
	sf      singleflight.Group

	subsMu      sync.Mutex
	subscribers map[string]map[chan FlapSSEEvent]struct{}
	pollCancel  map[string]context.CancelFunc
}

func NewFlapHub(registry FlapRegistry, centerKeyPair *crypto.KeyPair) *FlapHub {
	return &FlapHub{
		conns:         make(map[string]*FlapConn),
		registry:      registry,
		centerKeyPair: centerKeyPair,
		cache:         make(map[string]cachedSnapshot),
		subscribers:   make(map[string]map[chan FlapSSEEvent]struct{}),
		pollCancel:    make(map[string]context.CancelFunc),
	}
}

// baseCtx is the background context used for registry writes that outlive a
// single request (handshake, touch-seen).
func (h *FlapHub) baseCtx() context.Context { return context.Background() }

// NewConn wraps a freshly-upgraded agent connection. The connection is NOT yet
// queryable: it becomes so only after Activate, once the key exchange succeeds.
func (h *FlapHub) NewConn(agentID string, conn *websocket.Conn) *FlapConn {
	flog.WithField("agent_id", agentID).Info("flap agent connected, awaiting key exchange")
	return newFlapConn(agentID, conn)
}

// Activate registers an authenticated connection as the live, queryable
// connection for its agent id, displacing any previous one.
func (h *FlapHub) Activate(fc *FlapConn) {
	h.mu.Lock()
	if existing, ok := h.conns[fc.AgentID]; ok && existing != fc {
		existing.Close()
	}
	h.conns[fc.AgentID] = fc
	h.mu.Unlock()
	fc.setAuthed()
	flog.WithField("agent_id", fc.AgentID).Info("flap agent authenticated")
}

func (h *FlapHub) Unregister(agentID string, fc *FlapConn) {
	h.mu.Lock()
	if cur, ok := h.conns[agentID]; ok && cur == fc {
		delete(h.conns, agentID)
	}
	h.mu.Unlock()
	fc.Close()
	h.cacheMu.Lock()
	delete(h.cache, agentID)
	h.cacheMu.Unlock()
	flog.WithField("agent_id", agentID).Info("flap agent disconnected")
}

func (h *FlapHub) getConn(agentID string) *FlapConn {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.conns[agentID]
}

// Agent describes a connected flap agent for the public listing.
type Agent struct {
	AgentID      string           `json:"agent_id"`
	Capabilities FlapCapabilities `json:"capabilities"`
	Online       bool             `json:"online"`
}

// Agents returns the connected agents and their capabilities.
func (h *FlapHub) Agents() []Agent {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]Agent, 0, len(h.conns))
	for id, fc := range h.conns {
		out = append(out, Agent{AgentID: id, Capabilities: fc.Capabilities(), Online: true})
	}
	return out
}

// rpc sends a query and waits for the correlated reply.
func (h *FlapHub) rpc(ctx context.Context, agentID, msgType string, payload interface{}) (Message, error) {
	fc := h.getConn(agentID)
	if fc == nil {
		return Message{}, fmt.Errorf("flap agent %q not connected", agentID)
	}

	reqID := uuid.New().String()
	msg := Message{Type: msgType, ID: reqID, Payload: payload}
	data, err := json.Marshal(msg)
	if err != nil {
		return Message{}, err
	}

	respCh := make(chan Message, 1)
	fc.mu.Lock()
	fc.pending[reqID] = respCh
	fc.mu.Unlock()
	defer func() {
		fc.mu.Lock()
		delete(fc.pending, reqID)
		fc.mu.Unlock()
	}()

	select {
	case fc.sendCh <- data:
	default:
		return Message{}, fmt.Errorf("flap agent %q send buffer full", agentID)
	}

	select {
	case resp := <-respCh:
		if resp.Success != nil && !*resp.Success {
			return resp, fmt.Errorf("flap agent error: %s", resp.Error)
		}
		return resp, nil
	case <-ctx.Done():
		return Message{}, ctx.Err()
	case <-fc.closeCh:
		return Message{}, fmt.Errorf("flap agent disconnected during request")
	}
}

// Snapshot returns the agent's current snapshot, served from a short cache so
// concurrent public requests collapse to a single RPC per agent per window.
func (h *FlapHub) Snapshot(ctx context.Context, agentID string) (FlapSnapshot, error) {
	h.cacheMu.Lock()
	if c, ok := h.cache[agentID]; ok && time.Since(c.at) < flapCacheTTL {
		h.cacheMu.Unlock()
		return c.snap, nil
	}
	h.cacheMu.Unlock()

	v, err, _ := h.sf.Do(agentID, func() (interface{}, error) {
		rctx, cancel := context.WithTimeout(ctx, flapRPCTimeout)
		defer cancel()
		resp, err := h.rpc(rctx, agentID, TypeFlapQuery, nil)
		if err != nil {
			return FlapSnapshot{}, err
		}
		var snap FlapSnapshot
		if err := decodePayload(resp, &snap); err != nil {
			return FlapSnapshot{}, err
		}
		h.cacheMu.Lock()
		h.cache[agentID] = cachedSnapshot{snap: snap, at: time.Now()}
		h.cacheMu.Unlock()
		return snap, nil
	})
	if err != nil {
		return FlapSnapshot{}, err
	}
	return v.(FlapSnapshot), nil
}

// PrefixDetail returns the path history for a prefix on a given agent.
func (h *FlapHub) PrefixDetail(ctx context.Context, agentID, prefix string) (FlapPrefixDetail, error) {
	rctx, cancel := context.WithTimeout(ctx, flapRPCTimeout)
	defer cancel()
	resp, err := h.rpc(rctx, agentID, TypeFlapPrefixQuery, FlapPrefixQueryPayload{Prefix: prefix})
	if err != nil {
		return FlapPrefixDetail{}, err
	}
	var detail FlapPrefixDetail
	if err := decodePayload(resp, &detail); err != nil {
		return FlapPrefixDetail{}, err
	}
	return detail, nil
}

// Metrics returns the headline gauge data for a given agent.
func (h *FlapHub) Metrics(ctx context.Context, agentID string) (FlapMetric, error) {
	rctx, cancel := context.WithTimeout(ctx, flapRPCTimeout)
	defer cancel()
	resp, err := h.rpc(rctx, agentID, TypeFlapMetricsQuery, nil)
	if err != nil {
		return FlapMetric{}, err
	}
	var m FlapMetric
	if err := decodePayload(resp, &m); err != nil {
		return FlapMetric{}, err
	}
	return m, nil
}

// Subscribe registers an SSE channel for an agent, starting the poll loop on the
// first subscriber. Unsubscribe stops it when the last leaves.
func (h *FlapHub) Subscribe(agentID string) chan FlapSSEEvent {
	ch := make(chan FlapSSEEvent, flapSubBuffer)
	h.subsMu.Lock()
	defer h.subsMu.Unlock()
	subs, ok := h.subscribers[agentID]
	if !ok {
		subs = make(map[chan FlapSSEEvent]struct{})
		h.subscribers[agentID] = subs
	}
	subs[ch] = struct{}{}
	if len(subs) == 1 {
		ctx, cancel := context.WithCancel(context.Background())
		h.pollCancel[agentID] = cancel
		go h.pollLoop(ctx, agentID)
	}
	return ch
}

func (h *FlapHub) Unsubscribe(agentID string, ch chan FlapSSEEvent) {
	h.subsMu.Lock()
	defer h.subsMu.Unlock()
	subs, ok := h.subscribers[agentID]
	if !ok {
		return
	}
	delete(subs, ch)
	close(ch)
	if len(subs) == 0 {
		delete(h.subscribers, agentID)
		if cancel, ok := h.pollCancel[agentID]; ok {
			cancel()
			delete(h.pollCancel, agentID)
		}
	}
}

func (h *FlapHub) pollLoop(ctx context.Context, agentID string) {
	ticker := time.NewTicker(flapPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			snap, err := h.Snapshot(ctx, agentID)
			if err != nil {
				continue
			}
			data, err := json.Marshal(snap)
			if err != nil {
				continue
			}
			h.broadcast(agentID, FlapSSEEvent{Type: "u", Data: data})
		}
	}
}

func (h *FlapHub) broadcast(agentID string, ev FlapSSEEvent) {
	h.subsMu.Lock()
	subs := h.subscribers[agentID]
	channels := make([]chan FlapSSEEvent, 0, len(subs))
	for ch := range subs {
		channels = append(channels, ch)
	}
	h.subsMu.Unlock()
	for _, ch := range channels {
		select {
		case ch <- ev:
		default:
		}
	}
}

// Shutdown closes all flap agent connections.
func (h *FlapHub) Shutdown() {
	h.mu.Lock()
	conns := make([]*FlapConn, 0, len(h.conns))
	for _, fc := range h.conns {
		conns = append(conns, fc)
	}
	h.mu.Unlock()
	for _, fc := range conns {
		msg := websocket.FormatCloseMessage(websocket.CloseGoingAway, "server shutting down")
		fc.Conn.WriteMessage(websocket.CloseMessage, msg)
		fc.Close()
	}
}
