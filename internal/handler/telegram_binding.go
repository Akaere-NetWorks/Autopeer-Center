package handler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"time"

	"github.com/akaere/autopeer-center/internal/middleware"
	"github.com/akaere/autopeer-center/internal/model"
	"github.com/akaere/autopeer-center/internal/repository"
	"github.com/akaere/autopeer-center/internal/service"
	"github.com/sirupsen/logrus"
)

var telegramBindLog = logrus.WithField("pkg", "handler.telegram_bind")

type TelegramBindingHandler struct {
	botBindings repository.BotBindingRepository
	authRepo    repository.AuthRepository
	botUsername string
}

func NewTelegramBindingHandler(botBindings repository.BotBindingRepository, authRepo repository.AuthRepository, botUsername string) *TelegramBindingHandler {
	return &TelegramBindingHandler{
		botBindings: botBindings,
		authRepo:    authRepo,
		botUsername: botUsername,
	}
}

func (h *TelegramBindingHandler) GetBinding(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	asn := middleware.GetASN(ctx)

	binding, err := h.botBindings.GetByASN(ctx, asn)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		JSON(w, http.StatusOK, map[string]interface{}{
			"bound":        false,
			"bot_username": h.botUsername,
		})
		return
	}

	JSON(w, http.StatusOK, map[string]interface{}{
		"bound":       true,
		"tg_username": binding.TgUsername,
		"bound_at":    binding.BoundAt,
		"bound_via":   binding.BoundVia,
		"bot_username": h.botUsername,
	})
}

func (h *TelegramBindingHandler) CreateBindToken(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	asn := middleware.GetASN(ctx)

	if h.botUsername == "" {
		ErrorJSON(w, r, http.StatusServiceUnavailable, "not_configured", "Telegram bot is not configured")
		return
	}

	existing, _ := h.botBindings.GetByASN(ctx, asn)
	if existing != nil {
		ErrorJSON(w, r, http.StatusConflict, "already_bound", "This ASN is already bound to a Telegram account. Unbind first.")
		return
	}

	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		telegramBindLog.WithError(err).Error("failed to generate bind token")
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to generate token")
		return
	}
	token := hex.EncodeToString(tokenBytes)
	expiresAt := time.Now().Add(10 * time.Minute)

	if err := h.botBindings.CreateWebBindToken(ctx, token, asn, expiresAt); err != nil {
		telegramBindLog.WithError(err).Error("failed to store bind token")
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to create token")
		return
	}

	deeplink := "https://t.me/" + h.botUsername + "?start=bind_" + token

	JSON(w, http.StatusOK, map[string]interface{}{
		"deeplink":   deeplink,
		"expires_at": expiresAt,
	})
}

func (h *TelegramBindingHandler) DeleteBinding(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	asn := middleware.GetASN(ctx)

	binding, err := h.botBindings.GetByASN(ctx, asn)
	if err != nil {
		ErrorJSON(w, r, http.StatusNotFound, "not_bound", "No Telegram binding found for this account")
		return
	}

	if err := h.botBindings.Delete(ctx, binding.TgUserID); err != nil {
		telegramBindLog.WithError(err).Error("failed to delete binding")
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to unbind")
		return
	}

	telegramBindLog.WithFields(logrus.Fields{"asn": asn, "tg_user_id": binding.TgUserID}).Info("telegram binding removed via web")

	JSON(w, http.StatusOK, map[string]interface{}{
		"status": "unbound",
	})
}

// IsTelegramNotificationEnabledForASN checks whether a telegram notification key is enabled
// for the given ASN. Returns false if no preferences have been saved (opt-in default).
func IsTelegramNotificationEnabledForASN(authRepo repository.AuthRepository, asn int64, key string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	prefs, err := authRepo.ListNotificationPreferences(ctx, asn, "telegram")
	if err != nil || len(prefs) == 0 {
		return false // opt-in: disabled by default until user saves preferences
	}
	for _, pref := range prefs {
		if pref.NotificationKey == key {
			return pref.Enabled
		}
	}
	return false
}

type telegramNotificationOptionResponse struct {
	service.NotificationOption
	Enabled bool `json:"enabled"`
}

func (h *TelegramBindingHandler) GetTelegramNotifications(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	asn := middleware.GetASN(ctx)

	binding, err := h.botBindings.GetByASN(ctx, asn)
	if err != nil || binding == nil {
		JSON(w, http.StatusOK, map[string]interface{}{
			"bound":   false,
			"channel": "telegram",
		})
		return
	}

	prefs, err := h.authRepo.ListNotificationPreferences(ctx, asn, "telegram")
	if err != nil {
		telegramBindLog.WithError(err).WithField("asn", asn).Error("ListNotificationPreferences (telegram) failed")
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to load telegram notification preferences")
		return
	}

	prefByKey := map[string]bool{}
	for _, pref := range prefs {
		prefByKey[pref.NotificationKey] = pref.Enabled
	}

	options := make([]telegramNotificationOptionResponse, 0)
	for _, option := range service.NotificationOptionsForChannel("telegram") {
		// opt-in: disabled by default if no prefs saved
		enabled := prefByKey[option.Key]
		options = append(options, telegramNotificationOptionResponse{NotificationOption: option, Enabled: enabled})
	}

	JSON(w, http.StatusOK, map[string]interface{}{
		"bound":   true,
		"channel": "telegram",
		"options": options,
		"presets": service.NotificationPresetsForChannel("telegram"),
	})
}

func (h *TelegramBindingHandler) UpdateTelegramNotifications(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	asn := middleware.GetASN(ctx)

	binding, err := h.botBindings.GetByASN(ctx, asn)
	if err != nil || binding == nil {
		ErrorJSON(w, r, http.StatusPreconditionFailed, "not_bound", "Telegram account not bound. Bind your Telegram account first.")
		return
	}

	var body struct {
		EnabledKeys []string `json:"enabled_keys"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "Invalid JSON body")
		return
	}

	enabled := keySet(body.EnabledKeys)
	prefs := make([]*model.PeerNotificationPreference, 0)
	for _, option := range service.NotificationOptionsForChannel("telegram") {
		prefs = append(prefs, &model.PeerNotificationPreference{
			ASN:             asn,
			Channel:         "telegram",
			NotificationKey: option.Key,
			Enabled:         enabled[option.Key],
		})
	}

	if err := h.authRepo.UpsertNotificationPreferences(ctx, asn, "telegram", prefs); err != nil {
		telegramBindLog.WithError(err).WithField("asn", asn).Error("UpsertNotificationPreferences (telegram) failed")
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to update telegram notification preferences")
		return
	}

	JSON(w, http.StatusOK, map[string]interface{}{"status": "ok", "enabled_keys": body.EnabledKeys})
}
