package handler

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/akaere/autopeer-center/internal/middleware"
	"github.com/akaere/autopeer-center/internal/model"
	"github.com/akaere/autopeer-center/internal/repository"
	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/bcrypt"
)

type botSetting struct {
	Key         string `json:"key"`
	Value       string `json:"value"`
	Description string `json:"description"`
}

type botBlockedUser struct {
	ID        int64  `json:"id"`
	TgUserID  int64  `json:"tg_user_id"`
	Username  string `json:"username"`
	Reason    string `json:"reason"`
	BlockedBy string `json:"blocked_by"`
	BlockedAt string `json:"blocked_at"`
}

func (h *AdminHandler) GetBotSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	settings, err := h.bot.LoadAllSettings(ctx)
	if err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to fetch bot settings")
		return
	}

	result := make([]botSetting, 0, len(settings))
	for _, s := range settings {
		result = append(result, botSetting{
			Key:         s.SettingKey,
			Value:       s.SettingValue,
			Description: s.Description,
		})
	}
	JSON(w, http.StatusOK, map[string]interface{}{"settings": result})
}

func (h *AdminHandler) UpdateBotSetting(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	adminEmail := middleware.GetEmail(ctx)

	var body struct {
		Key   string  `json:"key"`
		Value *string `json:"value"`
	}
	if err := DecodeJSON(r, &body); err != nil || body.Key == "" || body.Value == nil {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "key and value are required")
		return
	}

	sensitiveKeys := map[string]bool{"bot_auth_token": true}

	adminLog.WithFields(logrus.Fields{"key": body.Key, "admin": adminEmail}).Debug("UpdateBotSetting")

	if _, err := h.bot.GetSetting(ctx, body.Key); err != nil {
		ErrorJSON(w, r, http.StatusNotFound, "not_found", "Setting key not found")
		return
	}

	if err := h.bot.UpdateSetting(ctx, body.Key, *body.Value); err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to update bot setting")
		return
	}

	auditVal := *body.Value
	if sensitiveKeys[body.Key] {
		auditVal = "[REDACTED]"
	}
	h.auditSvc.Log(ctx, "bot.setting.update", adminEmail, nil, map[string]interface{}{
		"key": body.Key, "value": auditVal,
	})

	h.hub.LoadBotSettings()
	h.hub.PushSettingsToBots()

	JSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

func (h *AdminHandler) GetBotStats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	totalCommands, uniqueUsers, commands, err := h.bot.GetCommandCounts(ctx)
	if err != nil {
		adminLog.WithError(err).Error("GetBotStats: command counts query failed")
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to fetch bot stats")
		return
	}

	lastTime, _ := h.bot.GetLastCommandAt(ctx)
	var lastCommandAt *string
	if lastTime != nil {
		s := lastTime.UTC().Format(time.RFC3339)
		lastCommandAt = &s
	}

	type cmdStat struct {
		Command string `json:"command"`
		Count   int    `json:"count"`
	}
	cmdBreakdown := make([]cmdStat, 0, len(commands))
	for cmd, cnt := range commands {
		cmdBreakdown = append(cmdBreakdown, cmdStat{Command: cmd, Count: cnt})
	}

	botConnected := h.hub.HasBotConnections()

	JSON(w, http.StatusOK, map[string]interface{}{
		"total_commands":   totalCommands,
		"unique_users":     uniqueUsers,
		"last_command_at":  lastCommandAt,
		"command_breakdown": cmdBreakdown,
		"bot_connected":    botConnected,
	})
}

func (h *AdminHandler) ListBotBlockedUsers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	users, err := h.bot.ListBlockedUsers(ctx)
	if err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to fetch blocked users")
		return
	}

	result := make([]botBlockedUser, 0, len(users))
	for _, u := range users {
		result = append(result, botBlockedUser{
			ID:        int64(u.ID),
			TgUserID:  u.TgUserID,
			Username:  u.Username,
			Reason:    u.Reason,
			BlockedBy: u.BlockedBy,
			BlockedAt: u.BlockedAt.UTC().Format(time.RFC3339),
		})
	}
	JSON(w, http.StatusOK, map[string]interface{}{"blocked_users": result})
}

func (h *AdminHandler) BlockBotUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	adminEmail := middleware.GetEmail(ctx)

	var body struct {
		TgUserID int64  `json:"tg_user_id"`
		Username string `json:"username"`
		Reason   string `json:"reason"`
	}
	if err := DecodeJSON(r, &body); err != nil || body.TgUserID == 0 {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "tg_user_id is required")
		return
	}

	if err := h.bot.BlockUser(ctx, &model.BotBlockedUser{
		TgUserID:  body.TgUserID,
		Username:  body.Username,
		Reason:    body.Reason,
		BlockedBy: adminEmail,
	}); err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to block user")
		return
	}

	h.auditSvc.Log(ctx, "bot.user.block", adminEmail, nil, map[string]interface{}{
		"tg_user_id": body.TgUserID, "username": body.Username, "reason": body.Reason,
	})

	h.hub.PushSettingsToBots()

	JSON(w, http.StatusOK, map[string]string{"status": "blocked"})
}

func (h *AdminHandler) UnblockBotUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	adminEmail := middleware.GetEmail(ctx)

	idStr := r.URL.Query().Get("id")
	if idStr == "" {
		idStr = fmt.Sprintf("%v", r.PathValue("id"))
	}

	if err := h.bot.UnblockUser(ctx, idStr); err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to unblock user")
		return
	}

	h.auditSvc.Log(ctx, "bot.user.unblock", adminEmail, nil, map[string]interface{}{"id": idStr})

	h.hub.PushSettingsToBots()

	JSON(w, http.StatusOK, map[string]string{"status": "unblocked"})
}

func (h *AdminHandler) GetBotRecentCommands(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	page := 1
	perPage := 50
	if p, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && p > 0 {
		page = p
	}

	stats, total, err := h.bot.ListCommandStats(ctx, repository.ListParams{
		Page:     page,
		PageSize: perPage,
	})
	if err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to fetch commands")
		return
	}

	type cmdEntry struct {
		ID        string `json:"id"`
		Command   string `json:"command"`
		TgUserID  int64  `json:"tg_user_id"`
		Username  string `json:"username"`
		ChatID    int64  `json:"chat_id"`
		CreatedAt string `json:"created_at"`
	}

	entries := make([]cmdEntry, 0, len(stats))
	for _, s := range stats {
		entries = append(entries, cmdEntry{
			ID:        fmt.Sprintf("%d", s.ID),
			Command:   s.Command,
			TgUserID:  s.TgUserID,
			Username:  s.Username,
			ChatID:    s.ChatID,
			CreatedAt: s.CreatedAt.UTC().Format(time.RFC3339),
		})
	}

	JSON(w, http.StatusOK, map[string]interface{}{
		"commands": entries,
		"total":    total,
		"page":     page,
		"per_page": perPage,
	})
}

func (h *AdminHandler) ResetBotToken(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	adminEmail := middleware.GetEmail(ctx)

	newToken := generateSecureToken(32)
	if newToken == "" {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to generate token")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(newToken), bcrypt.DefaultCost)
	if err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to hash token")
		return
	}

	if err := h.bot.UpdateSetting(ctx, "bot_auth_token", ""); err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to clear token value")
		return
	}
	if err := h.bot.UpdateSettingWithHash(ctx, "bot_auth_token", string(hash)); err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to update token")
		return
	}

	h.auditSvc.Log(ctx, "bot.token.reset", adminEmail, nil, nil)

	h.hub.LoadBotSettings()

	JSON(w, http.StatusOK, map[string]interface{}{
		"status":    "token_updated",
		"new_token": newToken,
	})
}

func generateSecureToken(length int) string {
	b := make([]byte, length)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return fmt.Sprintf("%x", b)
}

func (h *AdminHandler) ExportBotSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	allSettings, err := h.bot.LoadAllSettings(ctx)
	if err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to export settings")
		return
	}

	settings := make(map[string]string)
	for _, s := range allSettings {
		v := s.SettingValue
		if s.SettingKey == "bot_auth_token" {
			v = "[REDACTED]"
		}
		settings[s.SettingKey] = v
	}

	type blockedEntry struct {
		TgUserID int64  `json:"tg_user_id"`
		Username string `json:"username"`
		Reason   string `json:"reason"`
	}
	blockedUsers, _ := h.bot.ListBlockedUsers(ctx)
	blocked := make([]blockedEntry, 0, len(blockedUsers))
	for _, u := range blockedUsers {
		blocked = append(blocked, blockedEntry{
			TgUserID: u.TgUserID,
			Username: u.Username,
			Reason:   u.Reason,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	data, _ := json.Marshal(map[string]interface{}{
		"settings":      settings,
		"blocked_users": blocked,
	})
	w.Write(data)
}
