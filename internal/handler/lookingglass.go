package handler

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/akaere/autopeer-center/internal/middleware"
	"github.com/akaere/autopeer-center/internal/repository"
	"github.com/akaere/autopeer-center/internal/service"
	"github.com/akaere/autopeer-center/internal/ws"
	"golang.org/x/time/rate"
)

// lgRate / lgBurst bound how often a single user may run looking-glass queries.
// The agent commands are bounded and run synchronously, so a modest per-user
// rate keeps a node from being used as a high-volume probe source.
const (
	lgRate       = rate.Limit(0.1) // 1 query / 10s sustained
	lgBurst      = 3
	lgMaxTarget  = 255
	lgIdleEvict  = 30 * time.Minute
	lgSweepEvery = 10 * time.Minute
)

// LookingGlassHandler exposes on-demand network diagnostics (ping, traceroute,
// MTR, BGP route lookup) that logged-in users run against a node's agent.
type LookingGlassHandler struct {
	hub   *ws.Hub
	nodes repository.NodeRepository
	audit *service.AuditService

	mu        sync.Mutex
	limiters  map[string]*lgLimiterEntry
	lastSweep time.Time
}

type lgLimiterEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

func NewLookingGlassHandler(hub *ws.Hub, nodes repository.NodeRepository, audit *service.AuditService) *LookingGlassHandler {
	return &LookingGlassHandler{
		hub:      hub,
		nodes:    nodes,
		audit:    audit,
		limiters: make(map[string]*lgLimiterEntry),
	}
}

// allow returns true if the given user key is within its rate budget. It lazily
// creates per-user limiters and periodically evicts idle ones.
func (h *LookingGlassHandler) allow(key string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	now := time.Now()
	if now.Sub(h.lastSweep) > lgSweepEvery {
		for k, e := range h.limiters {
			if now.Sub(e.lastSeen) > lgIdleEvict {
				delete(h.limiters, k)
			}
		}
		h.lastSweep = now
	}

	e, ok := h.limiters[key]
	if !ok {
		e = &lgLimiterEntry{limiter: rate.NewLimiter(lgRate, lgBurst)}
		h.limiters[key] = e
	}
	e.lastSeen = now
	return e.limiter.Allow()
}

type lookingGlassRequest struct {
	NodeID string `json:"node_id"`
	Type   string `json:"type"`
	Target string `json:"target"`
}

// RunQuery handles POST /api/v1/user/looking-glass/run.
func (h *LookingGlassHandler) RunQuery(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	asn := middleware.GetASN(ctx)
	operator := fmt.Sprintf("AS%d", asn)

	var req lookingGlassRequest
	if err := DecodeJSON(r, &req); err != nil {
		ErrorJSON(w, r, http.StatusBadRequest, "invalid_request", "Invalid request body")
		return
	}

	req.NodeID = strings.TrimSpace(req.NodeID)
	req.Target = strings.TrimSpace(req.Target)
	req.Type = strings.TrimSpace(strings.ToLower(req.Type))

	if req.NodeID == "" {
		ErrorJSON(w, r, http.StatusBadRequest, "invalid_request", "node_id is required")
		return
	}
	if req.Target == "" {
		ErrorJSON(w, r, http.StatusBadRequest, "invalid_request", "target is required")
		return
	}
	if len(req.Target) > lgMaxTarget {
		ErrorJSON(w, r, http.StatusBadRequest, "invalid_request", "target is too long")
		return
	}

	msgType, payload, ok := lgCommand(req.Type, req.Target)
	if !ok {
		ErrorJSON(w, r, http.StatusBadRequest, "invalid_request", "type must be one of: ping, traceroute, mtr, bgp_route")
		return
	}

	// Rate limit per user (after cheap validation, before doing real work).
	if !h.allow(operator) {
		ErrorJSON(w, r, http.StatusTooManyRequests, "rate_limited", "Too many looking glass queries, please slow down")
		return
	}

	node, err := h.nodes.GetByID(ctx, req.NodeID)
	if err != nil || node == nil {
		ErrorJSON(w, r, http.StatusNotFound, "not_found", "Node not found")
		return
	}
	if !node.Enabled {
		ErrorJSON(w, r, http.StatusNotFound, "not_found", "Node not found")
		return
	}
	if !h.hub.IsOnline(req.NodeID) {
		ErrorJSON(w, r, http.StatusServiceUnavailable, "node_offline", "Node is offline")
		return
	}

	resp, err := h.hub.SendCommand(req.NodeID, msgType, payload)
	if err != nil {
		ErrorJSON(w, r, http.StatusServiceUnavailable, "agent_error", "Failed to reach agent: "+err.Error())
		return
	}
	if resp.Success != nil && !*resp.Success {
		ErrorJSON(w, r, http.StatusBadRequest, "query_failed", resp.Error)
		return
	}

	// Audit the query (best-effort).
	_ = h.audit.Log(ctx, "looking_glass.query", operator, &req.NodeID, map[string]any{
		"type":   req.Type,
		"target": req.Target,
	})

	JSON(w, http.StatusOK, map[string]any{
		"type":      req.Type,
		"node_id":   node.ID,
		"node_name": node.Name,
		"target":    req.Target,
		"result":    resp.Payload,
	})
}

// lgCommand maps a query type + target to the agent message type and payload.
func lgCommand(typ, target string) (string, any, bool) {
	switch typ {
	case "ping":
		return ws.TypeNetworkPing, ws.NetworkPingPayload{Target: target}, true
	case "traceroute", "trace":
		return ws.TypeNetworkTrace, ws.NetworkTracePayload{Target: target}, true
	case "mtr":
		return ws.TypeNetworkMTR, ws.NetworkMTRPayload{Target: target}, true
	case "bgp_route", "bgp", "route":
		return ws.TypeNetworkBGPRoute, ws.NetworkBGPRoutePayload{Target: target}, true
	default:
		return "", nil, false
	}
}
