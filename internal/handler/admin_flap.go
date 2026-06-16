package handler

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/akaere/autopeer-center/internal/middleware"
	"github.com/akaere/autopeer-center/internal/model"
	"github.com/akaere/autopeer-center/internal/repository"
	"github.com/akaere/autopeer-center/internal/service"
	"github.com/akaere/autopeer-center/internal/ws"
	"github.com/go-chi/chi/v5"
	"github.com/sirupsen/logrus"
)

var adminFlapLog = logrus.WithField("pkg", "handler.admin.flap")

// AdminFlapHandler manages the flapalerted-agent allowlist (flap_agents table),
// the DB-backed replacement for the former FLAP_AGENT_TOKENS / FLAP_AGENT_PUBKEYS
// environment variables.
type AdminFlapHandler struct {
	repo     repository.FlapAgentRepository
	hub      *ws.FlapHub
	auditSvc *service.AuditService
}

func NewAdminFlapHandler(repo repository.FlapAgentRepository, hub *ws.FlapHub, auditSvc *service.AuditService) *AdminFlapHandler {
	return &AdminFlapHandler{repo: repo, hub: hub, auditSvc: auditSvc}
}

type createFlapAgentReq struct {
	AgentID     string `json:"agent_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type updateFlapAgentReq struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	Enabled     *bool   `json:"enabled"`
}

// flapAgentView is the admin-facing shape of a flap agent, augmented with the
// live connection status from the hub.
type flapAgentView struct {
	*model.FlapAgent
	Online    bool `json:"online"`
	HasPubkey bool `json:"has_pubkey"`
}

func generateFlapToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// List returns all configured flap agents with their live online status.
func (h *AdminFlapHandler) List(w http.ResponseWriter, r *http.Request) {
	agents, err := h.repo.List(r.Context())
	if err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to list flap agents")
		return
	}

	online := make(map[string]bool)
	for _, a := range h.hub.Agents() {
		online[a.AgentID] = a.Online
	}

	views := make([]flapAgentView, 0, len(agents))
	for _, a := range agents {
		views = append(views, flapAgentView{
			FlapAgent: a,
			Online:    online[a.AgentID],
			HasPubkey: a.AgentPubkey != "",
		})
	}
	JSON(w, http.StatusOK, map[string]interface{}{"agents": views})
}

// Create registers a new flap agent and returns its bearer token once.
func (h *AdminFlapHandler) Create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	adminEmail := middleware.GetEmail(ctx)

	var req createFlapAgentReq
	if err := DecodeJSON(r, &req); err != nil {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "Invalid JSON body")
		return
	}

	req.AgentID = strings.TrimSpace(req.AgentID)
	if req.AgentID == "" {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "agent_id is required")
		return
	}

	if existing, _ := h.repo.GetByAgentID(ctx, req.AgentID); existing != nil {
		ErrorJSON(w, r, http.StatusConflict, "conflict", "A flap agent with this agent_id already exists")
		return
	}

	token, err := generateFlapToken()
	if err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to generate token")
		return
	}

	agent := &model.FlapAgent{
		AgentID:     req.AgentID,
		Name:        strings.TrimSpace(req.Name),
		Description: strings.TrimSpace(req.Description),
		Token:       token,
		Enabled:     true,
	}
	if err := h.repo.Create(ctx, agent); err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to create flap agent")
		return
	}

	h.auditSvc.Log(ctx, "flap_agent.create", adminEmail, &agent.ID, map[string]interface{}{"agent_id": req.AgentID})
	adminFlapLog.WithField("agent_id", req.AgentID).Info("flap agent created")

	JSON(w, http.StatusCreated, map[string]interface{}{
		"id":       agent.ID,
		"agent_id": agent.AgentID,
		"token":    token,
		"message":  "Save this token - it will not be shown again",
	})
}

// Update changes the editable fields of a flap agent.
func (h *AdminFlapHandler) Update(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	adminEmail := middleware.GetEmail(ctx)

	var req updateFlapAgentReq
	if err := DecodeJSON(r, &req); err != nil {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "Invalid JSON body")
		return
	}

	agent, err := h.repo.GetByID(ctx, id)
	if err != nil {
		ErrorJSON(w, r, http.StatusNotFound, "not_found", "Flap agent not found")
		return
	}

	if req.Name != nil {
		agent.Name = strings.TrimSpace(*req.Name)
	}
	if req.Description != nil {
		agent.Description = strings.TrimSpace(*req.Description)
	}
	if req.Enabled != nil {
		agent.Enabled = *req.Enabled
	}

	if err := h.repo.Update(ctx, agent); err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to update flap agent")
		return
	}

	h.auditSvc.Log(ctx, "flap_agent.update", adminEmail, &id, nil)
	JSON(w, http.StatusOK, agent)
}

// RegenerateToken issues a new bearer token for a flap agent.
func (h *AdminFlapHandler) RegenerateToken(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	adminEmail := middleware.GetEmail(ctx)

	if _, err := h.repo.GetByID(ctx, id); err != nil {
		ErrorJSON(w, r, http.StatusNotFound, "not_found", "Flap agent not found")
		return
	}

	token, err := generateFlapToken()
	if err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to generate token")
		return
	}
	if err := h.repo.SetToken(ctx, id, token); err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to update token")
		return
	}

	h.auditSvc.Log(ctx, "flap_agent.regenerate_token", adminEmail, &id, nil)
	JSON(w, http.StatusOK, map[string]interface{}{
		"token":   token,
		"message": "Save this token - it will not be shown again",
	})
}

// ResetPubkey clears the pinned X25519 public key so the agent re-pins on its
// next connection (used when re-provisioning an agent's key material).
func (h *AdminFlapHandler) ResetPubkey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	adminEmail := middleware.GetEmail(ctx)

	if _, err := h.repo.GetByID(ctx, id); err != nil {
		ErrorJSON(w, r, http.StatusNotFound, "not_found", "Flap agent not found")
		return
	}
	if err := h.repo.ResetPubkey(ctx, id); err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to reset pubkey")
		return
	}

	h.auditSvc.Log(ctx, "flap_agent.reset_pubkey", adminEmail, &id, nil)
	JSON(w, http.StatusOK, map[string]string{"status": "pubkey_cleared"})
}

// Delete removes a flap agent from the allowlist.
func (h *AdminFlapHandler) Delete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	adminEmail := middleware.GetEmail(ctx)

	if _, err := h.repo.GetByID(ctx, id); err != nil {
		ErrorJSON(w, r, http.StatusNotFound, "not_found", "Flap agent not found")
		return
	}
	if err := h.repo.Delete(ctx, id); err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to delete flap agent")
		return
	}

	h.auditSvc.Log(ctx, "flap_agent.delete", adminEmail, &id, nil)
	JSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
