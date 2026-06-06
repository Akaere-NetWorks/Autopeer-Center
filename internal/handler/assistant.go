package handler

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/akaere/autopeer-center/internal/config"
	"github.com/akaere/autopeer-center/internal/middleware"
	"github.com/google/uuid"
)

var assistantReadOnlyTools = map[string]struct{}{
	"nodes_list":           {},
	"peer_creation_status": {},
	"peer_list":            {},
	"peer_summary":         {},
	"peer_get":             {},
	"peer_get_metrics":     {},
}

type AssistantToolCallRequest struct {
	ToolName       string          `json:"tool_name"`
	Arguments      json.RawMessage `json:"arguments"`
	ConversationID string          `json:"conversation_id"`
	ApprovalToken  string          `json:"approval_token"`
}

type assistantApprovalPayload struct {
	ASN            int64           `json:"asn"`
	ConversationID string          `json:"conversation_id"`
	ToolName       string          `json:"tool_name"`
	Arguments      json.RawMessage `json:"arguments"`
	ExpiresAt      int64           `json:"expires_at"`
	Nonce          string          `json:"nonce"`
}

type assistantToolError struct {
	code    string
	message string
}

func (e assistantToolError) Error() string {
	return e.message
}

func assistantError(code, message string) error {
	return assistantToolError{code: code, message: message}
}

func (h *MCPHandler) HandleAssistantToolApproval(w http.ResponseWriter, r *http.Request) {
	asn := middleware.GetASN(r.Context())
	if asn == 0 {
		ErrorJSON(w, r, http.StatusUnauthorized, "unauthorized", "User authentication is required")
		return
	}
	var req AssistantToolCallRequest
	if err := DecodeJSON(r, &req); err != nil {
		ErrorJSON(w, r, http.StatusBadRequest, "invalid_body", "Invalid request body")
		return
	}
	if _, ok := assistantReadOnlyTools[req.ToolName]; !ok {
		ErrorJSON(w, r, http.StatusForbidden, "tool_not_allowed", "This assistant can only request approved read-only tools")
		return
	}
	if len(req.Arguments) == 0 {
		req.Arguments = json.RawMessage(`{}`)
	}
	if err := validateAssistantToolArgs(req.ToolName, req.Arguments); err != nil {
		ErrorJSON(w, r, http.StatusBadRequest, "invalid_arguments", err.Error())
		return
	}
	if req.ConversationID == "" {
		req.ConversationID = "assistant-" + uuid.NewString()
	}
	payload := assistantApprovalPayload{
		ASN:            asn,
		ConversationID: req.ConversationID,
		ToolName:       req.ToolName,
		Arguments:      compactJSON(req.Arguments),
		ExpiresAt:      time.Now().Add(2 * time.Minute).Unix(),
		Nonce:          uuid.NewString(),
	}
	token, err := h.signAssistantApproval(payload)
	if err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "approval_failed", "Failed to create approval token")
		return
	}
	h.assistantApprovals.Store(payload.Nonce, payload.ExpiresAt)
	JSON(w, http.StatusCreated, map[string]interface{}{"approval_token": token, "expires_at": payload.ExpiresAt})
}

func (h *MCPHandler) HandleAssistantToolCall(w http.ResponseWriter, r *http.Request) {
	asn := middleware.GetASN(r.Context())
	if asn == 0 {
		ErrorJSON(w, r, http.StatusUnauthorized, "unauthorized", "User authentication is required")
		return
	}

	var req AssistantToolCallRequest
	if err := DecodeJSON(r, &req); err != nil {
		ErrorJSON(w, r, http.StatusBadRequest, "invalid_body", "Invalid request body")
		return
	}
	if req.ToolName == "" {
		ErrorJSON(w, r, http.StatusBadRequest, "tool_required", "tool_name is required")
		return
	}
	if _, ok := assistantReadOnlyTools[req.ToolName]; !ok {
		ErrorJSON(w, r, http.StatusForbidden, "tool_not_allowed", "This assistant can only call approved read-only tools")
		return
	}
	if len(req.Arguments) == 0 {
		req.Arguments = json.RawMessage(`{}`)
	}
	if err := validateAssistantToolArgs(req.ToolName, req.Arguments); err != nil {
		ErrorJSON(w, r, http.StatusBadRequest, "invalid_arguments", err.Error())
		return
	}
	approval, err := h.verifyAssistantApproval(req.ApprovalToken)
	if err != nil {
		ErrorJSON(w, r, http.StatusForbidden, "approval_invalid", "Approval token is invalid or expired")
		return
	}
	if approval.ASN != asn || approval.ToolName != req.ToolName || approval.ConversationID != req.ConversationID || string(approval.Arguments) != string(compactJSON(req.Arguments)) {
		ErrorJSON(w, r, http.StatusForbidden, "approval_mismatch", "Approval token does not match this tool call")
		return
	}
	if !h.consumeAssistantApproval(approval.Nonce) {
		ErrorJSON(w, r, http.StatusForbidden, "approval_reused", "Approval token has already been used")
		return
	}

	data, err := h.callAssistantReadOnlyTool(r, asn, req.ToolName, req.Arguments)
	resultOK := err == nil
	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	}

	sessionID := req.ConversationID
	if sessionID == "" {
		sessionID = "assistant-" + uuid.NewString()
	}
	h.writeAuditLog(sessionID, asn, "", "assistant."+req.ToolName, sanitizeArgs(req.ToolName, req.Arguments), resultOK, errMsg)

	if err != nil {
		if toolErr, ok := err.(assistantToolError); ok {
			ErrorJSON(w, r, http.StatusBadRequest, toolErr.code, toolErr.message)
			return
		}
		ErrorJSON(w, r, http.StatusUnprocessableEntity, "tool_failed", "Assistant tool call failed")
		return
	}

	JSON(w, http.StatusOK, map[string]interface{}{
		"tool_name":       req.ToolName,
		"conversation_id": sessionID,
		"result":          data,
	})
}

func (h *MCPHandler) consumeAssistantApproval(nonce string) bool {
	if nonce == "" {
		return false
	}
	value, ok := h.assistantApprovals.LoadAndDelete(nonce)
	if !ok {
		return false
	}
	expiresAt, ok := value.(int64)
	return ok && time.Now().Unix() <= expiresAt
}

func (h *MCPHandler) cleanupAssistantApprovals() {
	now := time.Now().Unix()
	h.assistantApprovals.Range(func(key, value interface{}) bool {
		expiresAt, ok := value.(int64)
		if !ok || expiresAt < now {
			h.assistantApprovals.Delete(key)
		}
		return true
	})
}

func compactJSON(raw json.RawMessage) json.RawMessage {
	var v interface{}
	if err := json.Unmarshal(raw, &v); err != nil {
		return raw
	}
	b, err := json.Marshal(v)
	if err != nil {
		return raw
	}
	return json.RawMessage(b)
}

func (h *MCPHandler) approvalSecret() []byte {
	cfg := config.Get()
	if cfg == nil || cfg.JWTSecret == "" {
		return []byte("autopeer-assistant-approval-unconfigured")
	}
	mac := hmac.New(sha256.New, []byte(cfg.JWTSecret))
	mac.Write([]byte("assistant-approval-v1"))
	return mac.Sum(nil)
}

func (h *MCPHandler) signAssistantApproval(payload assistantApprovalPayload) (string, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, h.approvalSecret())
	mac.Write(b)
	sig := mac.Sum(nil)
	return base64.RawURLEncoding.EncodeToString(b) + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

func (h *MCPHandler) verifyAssistantApproval(token string) (*assistantApprovalPayload, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return nil, fmt.Errorf("approval token is required")
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("invalid approval token")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("invalid approval token")
	}
	mac := hmac.New(sha256.New, h.approvalSecret())
	mac.Write(payloadBytes)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return nil, fmt.Errorf("invalid approval token")
	}
	var payload assistantApprovalPayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return nil, fmt.Errorf("invalid approval token")
	}
	if time.Now().Unix() > payload.ExpiresAt {
		return nil, fmt.Errorf("approval token has expired")
	}
	return &payload, nil
}

func decodeAssistantArgs(raw json.RawMessage, dest interface{}) error {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dest); err != nil {
		return assistantError("invalid_arguments", "Invalid tool arguments")
	}
	if dec.More() {
		return assistantError("invalid_arguments", "Invalid tool arguments")
	}
	return nil
}

func validateAssistantToolArgs(name string, raw json.RawMessage) error {
	switch name {
	case "nodes_list", "peer_creation_status", "peer_list", "peer_summary":
		var args struct{}
		return decodeAssistantArgs(raw, &args)
	case "peer_get":
		var args struct {
			ID string `json:"id"`
		}
		if err := decodeAssistantArgs(raw, &args); err != nil {
			return err
		}
		if args.ID == "" {
			return assistantError("id_required", "Peer id is required")
		}
		return nil
	case "peer_get_metrics":
		var args struct {
			ID    string `json:"id"`
			Hours *int   `json:"hours"`
		}
		if err := decodeAssistantArgs(raw, &args); err != nil {
			return err
		}
		if args.ID == "" {
			return assistantError("id_required", "Peer id is required")
		}
		if args.Hours != nil && (*args.Hours < 1 || *args.Hours > 720) {
			return assistantError("hours_out_of_range", "hours must be between 1 and 720")
		}
		return nil
	default:
		return assistantError("unknown_tool", "Unknown tool")
	}
}

func (h *MCPHandler) callAssistantReadOnlyTool(r *http.Request, asn int64, name string, raw json.RawMessage) (interface{}, error) {
	ctx := r.Context()
	switch name {
	case "nodes_list":
		return h.toolNodesList(ctx)
	case "peer_creation_status":
		return h.toolPeerCreationStatus(ctx)
	case "peer_list":
		return h.toolPeerList(ctx, asn)
	case "peer_summary":
		return h.toolPeerSummary(ctx, asn)
	case "peer_get":
		var args struct {
			ID string `json:"id"`
		}
		if err := decodeAssistantArgs(raw, &args); err != nil {
			return nil, err
		}
		return h.toolPeerGet(ctx, asn, args.ID)
	case "peer_get_metrics":
		var args struct {
			ID    string `json:"id"`
			Hours *int   `json:"hours"`
		}
		if err := decodeAssistantArgs(raw, &args); err != nil {
			return nil, err
		}
		hours := 24
		if args.Hours != nil {
			hours = *args.Hours
		}
		return h.toolPeerGetMetrics(ctx, asn, args.ID, hours)
	default:
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
}
