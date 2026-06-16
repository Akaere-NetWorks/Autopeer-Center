package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/akaere/autopeer-center/internal/ws"
	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
)

var flapLog = logrus.WithField("pkg", "handler.flap")

// FlapHandler serves the public, unauthenticated flap endpoints and accepts
// flapalerted-agent WebSocket connections on a dedicated endpoint.
type FlapHandler struct {
	hub      *ws.FlapHub
	registry ws.FlapRegistry
	upgrader websocket.Upgrader
}

func NewFlapHandler(hub *ws.FlapHub, registry ws.FlapRegistry, allowedOrigins string) *FlapHandler {
	originSet := make(map[string]bool)
	for _, o := range strings.Split(allowedOrigins, ",") {
		o = strings.TrimRight(strings.TrimSpace(o), "/")
		if o != "" {
			originSet[o] = true
		}
	}
	return &FlapHandler{
		hub:      hub,
		registry: registry,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				origin := strings.TrimRight(r.Header.Get("Origin"), "/")
				if origin == "" {
					// Non-browser agent clients send a token header instead.
					return r.Header.Get("X-Flap-Token") != ""
				}
				return originSet[origin]
			},
		},
	}
}

// HandleWebSocket upgrades and serves a flapalerted-agent connection.
func (h *FlapHandler) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get("X-Flap-Token")
	if token == "" {
		http.Error(w, "missing flap token", http.StatusUnauthorized)
		return
	}
	agentID, ok := h.registry.ValidateToken(r.Context(), token)
	if !ok {
		http.Error(w, "invalid flap token", http.StatusUnauthorized)
		return
	}
	// If the client also provided an id, it must match the token-bound id.
	if provided := flapAgentID(r); provided != "" && provided != agentID {
		http.Error(w, "agent id does not match token", http.StatusForbidden)
		return
	}

	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		flapLog.WithError(err).Warn("flap ws upgrade failed")
		return
	}
	flapLog.WithField("agent_id", agentID).Info("flap agent websocket connected")
	fc := h.hub.NewConn(agentID, conn)
	go fc.WritePump()
	fc.ReadPump(h.hub)
}

func flapAgentID(r *http.Request) string {
	if id := r.Header.Get("X-Flap-Agent-ID"); id != "" {
		return id
	}
	return r.URL.Query().Get("agent_id")
}

// ListAgents returns the connected flap agents and their capabilities.
func (h *FlapHandler) ListAgents(w http.ResponseWriter, r *http.Request) {
	JSON(w, http.StatusOK, map[string]interface{}{"agents": h.hub.Agents()})
}

// Snapshot returns a single agent's snapshot (?agent=) or a merged view of all.
func (h *FlapHandler) Snapshot(w http.ResponseWriter, r *http.Request) {
	agent := strings.TrimSpace(r.URL.Query().Get("agent"))
	if agent != "" {
		snap, err := h.hub.Snapshot(r.Context(), agent)
		if err != nil {
			// Offline / unknown agent is not an error to the public UI.
			JSON(w, http.StatusOK, emptySnapshot())
			return
		}
		JSON(w, http.StatusOK, snap)
		return
	}
	JSON(w, http.StatusOK, h.mergedSnapshot(r))
}

// Prefix returns the per-prefix path history detail.
func (h *FlapHandler) Prefix(w http.ResponseWriter, r *http.Request) {
	prefix := strings.TrimSpace(r.URL.Query().Get("prefix"))
	if prefix == "" {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "prefix query parameter is required")
		return
	}
	agent := strings.TrimSpace(r.URL.Query().Get("agent"))
	if agent == "" {
		agent = h.firstAgent()
	}
	if agent == "" {
		JSON(w, http.StatusOK, ws.FlapPrefixDetail{Found: false, Prefix: prefix, PathHistory: []ws.FlapPathInfo{}})
		return
	}
	detail, err := h.hub.PrefixDetail(r.Context(), agent, prefix)
	if err != nil {
		JSON(w, http.StatusOK, ws.FlapPrefixDetail{Found: false, Prefix: prefix, PathHistory: []ws.FlapPathInfo{}})
		return
	}
	JSON(w, http.StatusOK, detail)
}

// Metrics returns the headline gauge metric for an agent or summed across all.
func (h *FlapHandler) Metrics(w http.ResponseWriter, r *http.Request) {
	agent := strings.TrimSpace(r.URL.Query().Get("agent"))
	if agent != "" {
		m, err := h.hub.Metrics(r.Context(), agent)
		if err != nil {
			JSON(w, http.StatusOK, ws.FlapMetric{})
			return
		}
		JSON(w, http.StatusOK, m)
		return
	}
	JSON(w, http.StatusOK, h.mergedSnapshot(r).Metric)
}

// Stream serves a Server-Sent Events stream of snapshots for one agent.
func (h *FlapHandler) Stream(w http.ResponseWriter, r *http.Request) {
	agent := strings.TrimSpace(r.URL.Query().Get("agent"))
	if agent == "" {
		agent = h.firstAgent()
	}
	if agent == "" {
		ErrorJSON(w, r, http.StatusServiceUnavailable, "no_agent", "no flap agent connected")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		ErrorJSON(w, r, http.StatusInternalServerError, "stream_unsupported", "streaming is not supported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Seed the stream with the current snapshot's stat series, then capabilities.
	if snap, err := h.hub.Snapshot(r.Context(), agent); err == nil {
		for _, s := range snap.Stats {
			if data, err := json.Marshal(s); err == nil {
				fmt.Fprintf(w, "event: c\ndata: %s\n\n", data)
			}
		}
		if data, err := json.Marshal(snap); err == nil {
			fmt.Fprintf(w, "event: ready\ndata: %s\n\n", data)
		}
		flusher.Flush()
	}

	ch := h.hub.Subscribe(agent)
	defer h.hub.Unsubscribe(agent, ch)
	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, ev.Data)
			flusher.Flush()
		}
	}
}

func (h *FlapHandler) firstAgent() string {
	agents := h.hub.Agents()
	if len(agents) == 0 {
		return ""
	}
	ids := make([]string, len(agents))
	for i, a := range agents {
		ids[i] = a.AgentID
	}
	sort.Strings(ids)
	return ids[0]
}

func emptySnapshot() ws.FlapSnapshot {
	return ws.FlapSnapshot{
		Stats:       []ws.FlapStatistic{},
		ActiveFlaps: []ws.FlapSummary{},
		Peers:       []ws.FlapPeerSummary{},
		Sessions:    []ws.FlapSessionInfo{},
	}
}

// mergedSnapshot unions the snapshots of all connected agents.
func (h *FlapHandler) mergedSnapshot(r *http.Request) ws.FlapSnapshot {
	agents := h.hub.Agents()
	merged := emptySnapshot()
	if len(agents) == 0 {
		return merged
	}

	flapByPrefix := make(map[string]ws.FlapSummary)
	peerByASN := make(map[uint32]ws.FlapPeerSummary)
	statsByTime := make(map[int64]ws.FlapStatistic)
	first := true

	for _, a := range agents {
		snap, err := h.hub.Snapshot(r.Context(), a.AgentID)
		if err != nil {
			continue
		}
		if first {
			merged.Capabilities = snap.Capabilities
			first = false
		}
		merged.Metric.ActiveFlapCount += snap.Metric.ActiveFlapCount
		merged.Metric.ActiveFlapTotalPathChangeCount += snap.Metric.ActiveFlapTotalPathChangeCount
		merged.Metric.TotalPathChangeCount += snap.Metric.TotalPathChangeCount
		merged.Metric.AverageRouteChanges90 += snap.Metric.AverageRouteChanges90
		merged.Metric.Sessions += snap.Metric.Sessions
		merged.SessionCount += snap.SessionCount
		merged.Sessions = append(merged.Sessions, snap.Sessions...)
		if snap.GeneratedAt > merged.GeneratedAt {
			merged.GeneratedAt = snap.GeneratedAt
		}

		for _, f := range snap.ActiveFlaps {
			if cur, ok := flapByPrefix[f.Prefix]; !ok || f.TotalCount > cur.TotalCount {
				flapByPrefix[f.Prefix] = f
			}
		}
		for _, p := range snap.Peers {
			if cur, ok := peerByASN[p.ASN]; !ok {
				peerByASN[p.ASN] = p
			} else {
				cur.RateSec += p.RateSec
				cur.RateSecAvg += p.RateSecAvg
				peerByASN[p.ASN] = cur
			}
		}
		for _, s := range snap.Stats {
			if cur, ok := statsByTime[s.Time]; !ok {
				statsByTime[s.Time] = s
			} else {
				cur.Changes += s.Changes
				cur.ListedChanges += s.ListedChanges
				cur.Active += s.Active
				cur.RouteCount += s.RouteCount
				statsByTime[s.Time] = cur
			}
		}
	}

	for _, f := range flapByPrefix {
		merged.ActiveFlaps = append(merged.ActiveFlaps, f)
	}
	sort.Slice(merged.ActiveFlaps, func(i, j int) bool {
		return merged.ActiveFlaps[i].TotalCount > merged.ActiveFlaps[j].TotalCount
	})
	for _, p := range peerByASN {
		merged.Peers = append(merged.Peers, p)
	}
	sort.Slice(merged.Peers, func(i, j int) bool {
		return merged.Peers[i].RateSecAvg > merged.Peers[j].RateSecAvg
	})
	for _, s := range statsByTime {
		merged.Stats = append(merged.Stats, s)
	}
	sort.Slice(merged.Stats, func(i, j int) bool { return merged.Stats[i].Time < merged.Stats[j].Time })

	return merged
}
