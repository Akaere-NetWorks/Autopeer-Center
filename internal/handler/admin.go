package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/akaere/autopeer-center/internal/cache"
	"github.com/akaere/autopeer-center/internal/lock"
	"github.com/akaere/autopeer-center/internal/repository"
	"github.com/akaere/autopeer-center/internal/service"
	"github.com/akaere/autopeer-center/internal/ws"
	"github.com/sirupsen/logrus"
)

var adminLog = logrus.WithField("pkg", "handler.admin")

type testEmailRequest struct {
	To      string `json:"to"`
	Message string `json:"message"`
}

type AdminHandler struct {
	peers    repository.PeerRepository
	nodes    repository.NodeRepository
	metrics  repository.MetricsRepository
	settings repository.SettingsRepository
	auditSvc *service.AuditService
	bot      repository.BotRepository
	releases repository.ReleaseRepository
	stats    repository.StatsRepository
	email    *service.EmailService
	hub      *ws.Hub
	c        *cache.Cache
	registry *service.RegistryService
	locker   lock.Locker
}

func NewAdminHandler(
	peers repository.PeerRepository,
	nodes repository.NodeRepository,
	metrics repository.MetricsRepository,
	settings repository.SettingsRepository,
	auditSvc *service.AuditService,
	bot repository.BotRepository,
	releases repository.ReleaseRepository,
	stats repository.StatsRepository,
	email *service.EmailService,
	hub *ws.Hub,
	c *cache.Cache,
	registry *service.RegistryService,
	locker lock.Locker,
) *AdminHandler {
	return &AdminHandler{
		peers:    peers,
		nodes:    nodes,
		metrics:  metrics,
		settings: settings,
		auditSvc: auditSvc,
		bot:      bot,
		releases: releases,
		stats:    stats,
		email:    email,
		hub:      hub,
		c:        c,
		registry: registry,
		locker:   locker,
	}
}

func (h *AdminHandler) refreshContactEmail(storedEmail string, asn int64) string {
	if h.registry == nil {
		return storedEmail
	}
	fresh, err := h.registry.LookupASNEmail(asn)
	if err != nil {
		return storedEmail
	}
	return fresh
}

func (h *AdminHandler) GetSettingValue(key string) string {
	var val string
	if ok, _ := h.c.Get(cache.BucketSettings, cache.SettingKey(cache.SettingVal, key), &val); ok {
		return val
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	val, _ = h.settings.GetNotificationSetting(ctx, key)
	h.c.Put(cache.BucketSettings, cache.SettingKey(cache.SettingVal, key), val, 30*time.Second)
	adminLog.WithFields(logrus.Fields{"key": key, "value": val}).Debug("GetSettingValue")
	return val
}

func (h *AdminHandler) GetSettingThreshold(key string) string {
	var val string
	if ok, _ := h.c.Get(cache.BucketSettings, cache.SettingKey(cache.SettingThr, key), &val); ok {
		return val
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	val, _ = h.settings.GetNotificationThreshold(ctx, key)
	h.c.Put(cache.BucketSettings, cache.SettingKey(cache.SettingThr, key), val, 30*time.Second)
	adminLog.WithFields(logrus.Fields{"key": key, "value": val}).Debug("GetSettingThreshold")
	return val
}

func (h *AdminHandler) GetSiteSettingValue(key string) string {
	var val string
	if ok, _ := h.c.Get(cache.BucketSettings, cache.SettingKey(cache.SiteSettingVal, key), &val); ok {
		return val
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	val, _ = h.settings.GetSiteSetting(ctx, key)
	h.c.Put(cache.BucketSettings, cache.SettingKey(cache.SiteSettingVal, key), val, 30*time.Second)
	adminLog.WithFields(logrus.Fields{"key": key, "value": val}).Debug("GetSiteSettingValue")
	return val
}

func (h *AdminHandler) TestEmail(w http.ResponseWriter, r *http.Request) {
	var req testEmailRequest
	if err := DecodeJSON(r, &req); err != nil {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "Invalid JSON body")
		return
	}
	if req.To == "" {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "Missing required field: to")
		return
	}
	message := req.Message
	if message == "" {
		message = "This is a test email."
	}
	vars := map[string]interface{}{
		"message":   message,
		"recipient": req.To,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}
	if err := h.email.SendRaw(req.To, "test-email", vars); err != nil {
		adminLog.WithError(err).WithField("to", req.To).Warn("test email send failed")
	}
	adminLog.WithField("to", req.To).Info("test email sent")
	JSON(w, http.StatusOK, map[string]interface{}{
		"status":  "sent",
		"message": "Test email sent to " + req.To,
	})
}
