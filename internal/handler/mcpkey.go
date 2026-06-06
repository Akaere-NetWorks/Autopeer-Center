package handler

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/akaere/autopeer-center/internal/middleware"
	"github.com/akaere/autopeer-center/internal/model"
	"github.com/akaere/autopeer-center/internal/repository"
	"github.com/go-chi/chi/v5"
)

const mcpKeyMaxPerASN = 10

type MCPKeyHandler struct {
	mcp repository.MCPRepository
}

func NewMCPKeyHandler(mcp repository.MCPRepository) *MCPKeyHandler {
	return &MCPKeyHandler{mcp: mcp}
}

func (h *MCPKeyHandler) List(w http.ResponseWriter, r *http.Request) {
	asn := middleware.GetASN(r.Context())
	keys, err := h.mcp.ListUserKeys(r.Context(), asn)
	if err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "db_error", "Failed to list MCP keys")
		return
	}
	JSON(w, http.StatusOK, keys)
}

func (h *MCPKeyHandler) Create(w http.ResponseWriter, r *http.Request) {
	asn := middleware.GetASN(r.Context())

	var req struct {
		Name         string   `json:"name"`
		ExpiresAt    *string  `json:"expires_at"`
		Capabilities []string `json:"capabilities"`
	}
	if err := DecodeJSON(r, &req); err != nil {
		ErrorJSON(w, r, http.StatusBadRequest, "invalid_body", "Invalid request body")
		return
	}
	if req.Name == "" {
		ErrorJSON(w, r, http.StatusBadRequest, "name_required", "Key name is required")
		return
	}
	if len(req.Name) > 64 {
		ErrorJSON(w, r, http.StatusBadRequest, "name_too_long", "Key name must be 64 characters or fewer")
		return
	}
	if err := validateCapabilities(req.Capabilities, allowedUserMCPCapabilities); err != nil {
		ErrorJSON(w, r, http.StatusBadRequest, "invalid_capability", err.Error())
		return
	}

	count, err := h.mcp.CountUserKeys(r.Context(), asn)
	if err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "db_error", "Failed to count MCP keys")
		return
	}
	if count >= mcpKeyMaxPerASN {
		ErrorJSON(w, r, http.StatusUnprocessableEntity, "limit_reached",
			"You have reached the maximum of 10 MCP keys. Revoke an existing key first.")
		return
	}

	var expiresAt *time.Time
	if req.ExpiresAt != nil && *req.ExpiresAt != "" {
		t, err := time.Parse(time.RFC3339, *req.ExpiresAt)
		if err != nil {
			ErrorJSON(w, r, http.StatusBadRequest, "invalid_expires_at", "expires_at must be RFC3339 format")
			return
		}
		if t.Before(time.Now()) {
			ErrorJSON(w, r, http.StatusBadRequest, "expires_in_past", "expires_at must be in the future")
			return
		}
		expiresAt = &t
	}

	rawBytes := make([]byte, 32)
	if _, err := rand.Read(rawBytes); err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "rand_error", "Failed to generate key")
		return
	}
	plaintext := "ap_mcp_" + hex.EncodeToString(rawBytes)

	sum := sha256.Sum256([]byte(plaintext))
	keyHash := hex.EncodeToString(sum[:])
	keyPrefix := plaintext[:16]

	key := &model.MCPKey{
		ASN:          asn,
		Name:         req.Name,
		KeyHash:      keyHash,
		KeyPrefix:    keyPrefix,
		Capabilities: sanitizeCapabilities(req.Capabilities, allowedUserMCPCapabilities, defaultUserMCPCapabilities),
		ExpiresAt:    expiresAt,
	}
	if err := h.mcp.CreateUserKey(r.Context(), key); err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "db_error", "Failed to create MCP key")
		return
	}

	JSON(w, http.StatusCreated, map[string]interface{}{
		"id":           key.ID,
		"asn":          key.ASN,
		"name":         key.Name,
		"key":          plaintext,
		"key_prefix":   key.KeyPrefix,
		"capabilities": key.Capabilities,
		"expires_at":   key.ExpiresAt,
		"last_used_at": key.LastUsedAt,
		"created_at":   key.CreatedAt,
	})
}

func (h *MCPKeyHandler) Delete(w http.ResponseWriter, r *http.Request) {
	asn := middleware.GetASN(r.Context())
	id := chi.URLParam(r, "id")

	if err := h.mcp.DeleteUserKey(r.Context(), id, asn); err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "db_error", "Failed to revoke MCP key")
		return
	}
	JSON(w, http.StatusOK, map[string]string{"message": "MCP key revoked"})
}
