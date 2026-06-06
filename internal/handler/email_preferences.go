package handler

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/akaere/autopeer-center/internal/middleware"
	"github.com/akaere/autopeer-center/internal/model"
	"github.com/akaere/autopeer-center/internal/repository"
	"github.com/akaere/autopeer-center/internal/service"
	"github.com/sirupsen/logrus"
)

var emailPrefLog = logrus.WithField("pkg", "handler.emailpref")

type EmailPreferencesHandler struct {
	authRepo     repository.AuthRepository
	gpgAvailable func(asn int64) bool
}

func NewEmailPreferencesHandler(authRepo repository.AuthRepository) *EmailPreferencesHandler {
	return &EmailPreferencesHandler{authRepo: authRepo}
}

func (h *EmailPreferencesHandler) SetGPGAvailabilityFunc(fn func(asn int64) bool) {
	h.gpgAvailable = fn
}

func GetEmailLevelForASN(authRepo repository.AuthRepository, asn int64) int {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	level, _ := authRepo.GetEmailLevel(ctx, asn)
	return level
}

func IsNotificationEnabledForASN(authRepo repository.AuthRepository, asn int64, key string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	option, ok := service.NotificationOptionByKey(key)
	if !ok {
		return false
	}
	prefs, err := authRepo.ListNotificationPreferences(ctx, asn, option.Channel)
	if err == nil && len(prefs) > 0 {
		for _, pref := range prefs {
			if pref.NotificationKey == key {
				return pref.Enabled
			}
		}
		return false
	}
	level, _ := authRepo.GetEmailLevel(ctx, asn)
	if key == service.NotificationAuthLoginCode {
		return true
	}
	return option.LegacyLevel > 0 && level >= option.LegacyLevel
}

func (h *EmailPreferencesHandler) Get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	asn := middleware.GetASN(ctx)
	emailPrefLog.WithField("asn", asn).Debug("GetEmailPreferences entry")

	level, _ := h.authRepo.GetEmailLevel(ctx, asn)

	JSON(w, http.StatusOK, map[string]interface{}{
		"email_level": level,
		"levels": []map[string]interface{}{
			{"level": 0, "name": "none", "description": "Do not receive any emails"},
			{"level": 1, "name": "urgent", "description": "Only critical changes (peer approved/rejected/suspended/deleted)"},
			{"level": 2, "name": "general", "description": "Critical + general (adds BGP alerts, handshake stale, peer submitted)"},
			{"level": 3, "name": "all", "description": "All emails including real-time monitoring (latency alerts)"},
		},
	})
}

type notificationPreferenceOptionResponse struct {
	service.NotificationOption
	Enabled bool `json:"enabled"`
	IsNew   bool `json:"is_new"`
}

func (h *EmailPreferencesHandler) GetNotifications(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	asn := middleware.GetASN(ctx)

	level, _ := h.authRepo.GetEmailLevel(ctx, asn)
	prefs, err := h.authRepo.ListNotificationPreferences(ctx, asn, "email")
	if err != nil {
		emailPrefLog.WithError(err).WithField("asn", asn).Error("ListNotificationPreferences failed")
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to load notification preferences")
		return
	}
	state, err := h.authRepo.GetNotificationPreferenceState(ctx, asn)
	if err != nil && err != sql.ErrNoRows {
		emailPrefLog.WithError(err).WithField("asn", asn).Error("GetNotificationPreferenceState failed")
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to load notification preference state")
		return
	}

	seenVersion := 0
	if state != nil {
		seenVersion = state.SeenCatalogVersion
	}
	prefByKey := map[string]bool{}
	for _, pref := range prefs {
		prefByKey[pref.NotificationKey] = pref.Enabled
	}
	legacyMode := len(prefs) == 0
	legacyEnabled := keySet(service.LegacyEnabledNotificationKeys(level))
	options := make([]notificationPreferenceOptionResponse, 0)
	hasUnseenOptions := false
	for _, option := range service.NotificationOptionsForChannel("email") {
		enabled := false
		if legacyMode {
			enabled = legacyEnabled[option.Key]
		} else {
			enabled = prefByKey[option.Key]
		}
		isNew := option.IntroducedVersion > seenVersion
		if isNew {
			hasUnseenOptions = true
			if !legacyMode {
				enabled = prefByKey[option.Key]
			}
		}
		options = append(options, notificationPreferenceOptionResponse{NotificationOption: option, Enabled: enabled, IsNew: isNew})
	}

	JSON(w, http.StatusOK, map[string]interface{}{
		"mode":                    map[bool]string{true: "legacy", false: "explicit"}[legacyMode],
		"email_level":             level,
		"seen_catalog_version":    seenVersion,
		"current_catalog_version": service.NotificationCatalogVersion,
		"needs_wizard":            legacyMode || hasUnseenOptions,
		"has_unseen_options":      hasUnseenOptions,
		"options":                 options,
		"presets":                 service.NotificationPresets(),
	})
}

func (h *EmailPreferencesHandler) UpdateNotifications(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	asn := middleware.GetASN(ctx)

	var body struct {
		EnabledKeys                   []string `json:"enabled_keys"`
		ConfirmedDisabledCriticalKeys []string `json:"confirmed_disabled_critical_keys"`
		SeenCatalogVersion            int      `json:"seen_catalog_version"`
		WizardCompleted               bool     `json:"wizard_completed"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "Invalid JSON body")
		return
	}

	enabled := keySet(body.EnabledKeys)
	confirmed := keySet(body.ConfirmedDisabledCriticalKeys)
	confirmationRequired := []string{}
	preconditionFailed := []string{}
	prefs := make([]*model.PeerNotificationPreference, 0)
	for _, option := range service.NotificationOptionsForChannel("email") {
		isEnabled := enabled[option.Key]
		if option.RequiresDisableConfirmation && !isEnabled && !confirmed[option.Key] {
			confirmationRequired = append(confirmationRequired, option.Key)
		}
		if option.Kind == service.NotificationKindRequired && !isEnabled && !h.hasAlternativeRequiredCapability(asn, option.Key) {
			preconditionFailed = append(preconditionFailed, option.Key)
		}
		prefs = append(prefs, &model.PeerNotificationPreference{ASN: asn, Channel: option.Channel, NotificationKey: option.Key, Enabled: isEnabled})
	}
	if len(confirmationRequired) > 0 {
		JSON(w, http.StatusBadRequest, map[string]interface{}{"error": "confirmation_required", "message": "Disabling critical notifications requires confirmation.", "keys": confirmationRequired})
		return
	}
	if len(preconditionFailed) > 0 {
		JSON(w, http.StatusPreconditionFailed, map[string]interface{}{"error": "precondition_failed", "message": "This notification cannot be disabled until another required capability is available.", "keys": preconditionFailed})
		return
	}

	if err := h.authRepo.UpsertNotificationPreferences(ctx, asn, "email", prefs); err != nil {
		emailPrefLog.WithError(err).WithField("asn", asn).Error("UpsertNotificationPreferences failed")
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to update notification preferences")
		return
	}
	seenVersion := body.SeenCatalogVersion
	if seenVersion < service.NotificationCatalogVersion {
		seenVersion = service.NotificationCatalogVersion
	}
	state := &model.PeerNotificationPreferenceState{ASN: asn, SeenCatalogVersion: seenVersion}
	if body.WizardCompleted {
		state.WizardCompletedAt = time.Now()
	}
	if err := h.authRepo.UpsertNotificationPreferenceState(ctx, state); err != nil {
		emailPrefLog.WithError(err).WithField("asn", asn).Error("UpsertNotificationPreferenceState failed")
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to update notification preference state")
		return
	}

	JSON(w, http.StatusOK, map[string]interface{}{"status": "ok", "enabled_keys": body.EnabledKeys, "seen_catalog_version": seenVersion})
}

func (h *EmailPreferencesHandler) hasAlternativeRequiredCapability(asn int64, key string) bool {
	if key == service.NotificationAuthLoginCode && h.gpgAvailable != nil {
		return h.gpgAvailable(asn)
	}
	return false
}

func keySet(keys []string) map[string]bool {
	set := make(map[string]bool, len(keys))
	for _, key := range keys {
		set[key] = true
	}
	return set
}

func (h *EmailPreferencesHandler) Update(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	asn := middleware.GetASN(ctx)

	var body struct {
		EmailLevel int `json:"email_level"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "Invalid JSON body")
		return
	}
	if body.EmailLevel < 0 || body.EmailLevel > 3 {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "email_level must be between 0 and 3")
		return
	}

	emailPrefLog.WithFields(logrus.Fields{"asn": asn, "level": body.EmailLevel}).Debug("UpdateEmailPreferences entry")

	err := h.authRepo.UpsertEmailPreference(ctx, &model.PeerEmailPreference{ASN: asn, EmailLevel: body.EmailLevel})
	if err != nil {
		emailPrefLog.WithError(err).WithField("asn", asn).Error("UpdateEmailPreferences: db upsert failed")
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to update email preferences")
		return
	}

	emailPrefLog.WithFields(logrus.Fields{"asn": asn, "level": body.EmailLevel}).Info("email preferences updated")

	JSON(w, http.StatusOK, map[string]interface{}{
		"email_level": body.EmailLevel,
	})
}
