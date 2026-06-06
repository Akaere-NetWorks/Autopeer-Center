package handler

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/akaere/autopeer-center/internal/middleware"
	"github.com/akaere/autopeer-center/internal/model"
	"github.com/akaere/autopeer-center/internal/repository"
	"github.com/akaere/autopeer-center/internal/service"
	"github.com/go-chi/chi/v5"
)

const adminMCPKeyMax = 10

type AdminMCPKeyHandler struct {
	mcp      repository.MCPRepository
	auditSvc *service.AuditService
}

func NewAdminMCPKeyHandler(mcp repository.MCPRepository, auditSvc *service.AuditService) *AdminMCPKeyHandler {
	return &AdminMCPKeyHandler{mcp: mcp, auditSvc: auditSvc}
}

func (h *AdminMCPKeyHandler) ListAdminKeys(w http.ResponseWriter, r *http.Request) {
	adminID := middleware.GetAdminID(r.Context())
	keys, err := h.mcp.ListAdminKeys(r.Context(), adminID)
	if err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "db_error", "Failed to list admin MCP keys")
		return
	}
	JSON(w, http.StatusOK, keys)
}

func (h *AdminMCPKeyHandler) CreateAdminKey(w http.ResponseWriter, r *http.Request) {
	adminID := middleware.GetAdminID(r.Context())

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
	if err := validateCapabilities(req.Capabilities, allowedAdminMCPCapabilities); err != nil {
		ErrorJSON(w, r, http.StatusBadRequest, "invalid_capability", err.Error())
		return
	}

	count, err := h.mcp.CountAdminKeys(r.Context(), adminID)
	if err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "db_error", "Failed to count admin MCP keys")
		return
	}
	if count >= adminMCPKeyMax {
		ErrorJSON(w, r, http.StatusUnprocessableEntity, "limit_reached",
			"You have reached the maximum of 10 admin MCP keys. Revoke an existing key first.")
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
	plaintext := "ap_mcp_admin_" + hex.EncodeToString(rawBytes)

	sum := sha256.Sum256([]byte(plaintext))
	keyHash := hex.EncodeToString(sum[:])
	keyPrefix := plaintext[:20]

	key := &model.AdminMCPKey{
		AdminID:      adminID,
		Name:         req.Name,
		KeyHash:      keyHash,
		KeyPrefix:    keyPrefix,
		Capabilities: sanitizeCapabilities(req.Capabilities, allowedAdminMCPCapabilities, defaultAdminMCPCapabilities),
		ExpiresAt:    expiresAt,
	}
	if err := h.mcp.CreateAdminKey(r.Context(), key); err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "db_error", "Failed to create admin MCP key")
		return
	}

	JSON(w, http.StatusCreated, map[string]interface{}{
		"id":           key.ID,
		"admin_id":     key.AdminID,
		"name":         key.Name,
		"key":          plaintext,
		"key_prefix":   key.KeyPrefix,
		"capabilities": key.Capabilities,
		"expires_at":   key.ExpiresAt,
		"last_used_at": key.LastUsedAt,
		"created_at":   key.CreatedAt,
	})
}

func (h *AdminMCPKeyHandler) DeleteAdminKey(w http.ResponseWriter, r *http.Request) {
	adminID := middleware.GetAdminID(r.Context())
	id := chi.URLParam(r, "id")

	if err := h.mcp.DeleteAdminKey(r.Context(), id, adminID); err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "db_error", "Failed to revoke admin MCP key")
		return
	}
	JSON(w, http.StatusOK, map[string]string{"message": "Admin MCP key revoked"})
}

func (h *AdminMCPKeyHandler) ListUserMCPKeys(w http.ResponseWriter, r *http.Request) {
	asnStr := r.URL.Query().Get("asn")

	asnVal := int64(0)
	if asnStr != "" {
		parsed, parseErr := strconv.ParseInt(asnStr, 10, 64)
		if parseErr != nil {
			ErrorJSON(w, r, http.StatusBadRequest, "invalid_asn", "ASN must be a number")
			return
		}
		asnVal = parsed
	}
	keys, err := h.mcp.ListAllUserKeys(r.Context(), asnVal)
	if err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "db_error", "Failed to list user MCP keys")
		return
	}
	JSON(w, http.StatusOK, keys)
}

func (h *AdminMCPKeyHandler) ForceDeleteUserMCPKey(w http.ResponseWriter, r *http.Request) {
	adminID := middleware.GetAdminID(r.Context())
	id := chi.URLParam(r, "id")

	key, err := h.mcp.ForceDeleteUserKey(r.Context(), id)
	if err != nil {
		ErrorJSON(w, r, http.StatusNotFound, "not_found", "User MCP key not found")
		return
	}

	keyIDStr := id
	h.auditSvc.Log(r.Context(), "admin_revoke_user_mcp_key", adminID, &keyIDStr, map[string]interface{}{
		"asn":      key.ASN,
		"key_name": key.Name,
	})

	JSON(w, http.StatusOK, map[string]string{"message": "User MCP key revoked"})
}

func ListMCPAuditLogs(mcpRepo repository.MCPRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		asn := middleware.GetASN(r.Context())
		logs, err := mcpRepo.ListAuditLogsByASN(r.Context(), asn, 200)
		if err != nil {
			ErrorJSON(w, r, http.StatusInternalServerError, "db_error", "Failed to list audit logs")
			return
		}

		type auditEntry struct {
			ID        string          `json:"id"`
			SessionID *string         `json:"session_id,omitempty"`
			ASN       *int64          `json:"asn,omitempty"`
			AdminID   *string         `json:"admin_id,omitempty"`
			ToolName  string          `json:"tool_name"`
			Args      json.RawMessage `json:"args,omitempty"`
			ResultOK  bool            `json:"result_ok"`
			ErrorMsg  *string         `json:"error_msg,omitempty"`
			CalledAt  time.Time       `json:"called_at"`
		}
		result := make([]auditEntry, 0, len(logs))
		for _, l := range logs {
			var argsJSON json.RawMessage
			if l.Args != nil {
				argsJSON, _ = json.Marshal(l.Args)
			}
			result = append(result, auditEntry{
				ID:        l.ID,
				SessionID: l.SessionID,
				ASN:       l.ASN,
				AdminID:   l.AdminID,
				ToolName:  l.ToolName,
				Args:      argsJSON,
				ResultOK:  l.ResultOK,
				ErrorMsg:  l.ErrorMsg,
				CalledAt:  l.CalledAt,
			})
		}
		JSON(w, http.StatusOK, result)
	}
}

func AdminListMCPAuditLogs(mcpRepo repository.MCPRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		asnStr := q.Get("asn")
		tool := q.Get("tool")
		pageStr := q.Get("page")
		perPageStr := q.Get("per_page")

		page, perPage := 1, 50
		if p, err := strconv.Atoi(pageStr); err == nil && p >= 1 {
			page = p
		}
		if pp, err := strconv.Atoi(perPageStr); err == nil && pp >= 1 && pp <= 100 {
			perPage = pp
		}

		params := repository.ListParams{
			Page:     page,
			PageSize: perPage,
		}
		if asnStr != "" {
			asnVal, parseErr := strconv.ParseInt(asnStr, 10, 64)
			if parseErr != nil {
				ErrorJSON(w, r, http.StatusBadRequest, "invalid_asn", "ASN must be a number")
				return
			}
			params.ASN = asnVal
		}
		if tool != "" {
			params.Search = tool
		}

		logs, _, err := mcpRepo.ListAuditLogs(r.Context(), params)
		if err != nil {
			ErrorJSON(w, r, http.StatusInternalServerError, "db_error", "Failed to list audit logs")
			return
		}

		type auditEntry struct {
			ID        string          `json:"id"`
			SessionID *string         `json:"session_id,omitempty"`
			ASN       *int64          `json:"asn,omitempty"`
			AdminID   *string         `json:"admin_id,omitempty"`
			ToolName  string          `json:"tool_name"`
			Args      json.RawMessage `json:"args,omitempty"`
			ResultOK  bool            `json:"result_ok"`
			ErrorMsg  *string         `json:"error_msg,omitempty"`
			CalledAt  time.Time       `json:"called_at"`
		}
		result := make([]auditEntry, 0, len(logs))
		for _, l := range logs {
			var argsJSON json.RawMessage
			if l.Args != nil {
				argsJSON, _ = json.Marshal(l.Args)
			}
			result = append(result, auditEntry{
				ID:        l.ID,
				SessionID: l.SessionID,
				ASN:       l.ASN,
				AdminID:   l.AdminID,
				ToolName:  l.ToolName,
				Args:      argsJSON,
				ResultOK:  l.ResultOK,
				ErrorMsg:  l.ErrorMsg,
				CalledAt:  l.CalledAt,
			})
		}
		JSON(w, http.StatusOK, map[string]interface{}{
			"logs":     result,
			"page":     page,
			"per_page": perPage,
		})
	}
}
