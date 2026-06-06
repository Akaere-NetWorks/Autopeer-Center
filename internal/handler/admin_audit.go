package handler

import (
	"net/http"
	"strconv"
	"time"

	"github.com/akaere/autopeer-center/internal/cache"
	"github.com/akaere/autopeer-center/internal/middleware"
	"github.com/sirupsen/logrus"
)

type notificationSetting struct {
	Key            string `json:"key"`
	Value          string `json:"value"`
	ThresholdValue string `json:"threshold_value"`
	Description    string `json:"description"`
}

type siteSetting struct {
	Key         string `json:"key"`
	Value       string `json:"value"`
	Description string `json:"description"`
}

func (h *AdminHandler) ListAuditLogs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	action := r.URL.Query().Get("action")
	operator := r.URL.Query().Get("operator")

	adminLog.WithFields(logrus.Fields{"action": action, "operator": operator}).Debug("ListAuditLogs entry")

	page := 1
	perPage := 25
	if p, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && p > 0 {
		page = p
	}
	if pp, err := strconv.Atoi(r.URL.Query().Get("per_page")); err == nil && pp > 0 && pp <= 200 {
		perPage = pp
	}

	logs, total, err := h.auditSvc.List(ctx, action, operator, page, perPage)
	if err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to fetch audit logs")
		return
	}

	type auditResp struct {
		ID        string      `json:"id"`
		Action    string      `json:"action"`
		Operator  string      `json:"operator"`
		TargetID  *string     `json:"target_id,omitempty"`
		Detail    interface{} `json:"detail,omitempty"`
		CreatedAt string      `json:"created_at"`
	}

	resp := make([]auditResp, 0, len(logs))
	for _, l := range logs {
		resp = append(resp, auditResp{
			ID:        l.ID,
			Action:    l.Action,
			Operator:  l.Operator,
			TargetID:  l.TargetID,
			Detail:    l.Detail,
			CreatedAt: l.CreatedAt.UTC().Format(time.RFC3339),
		})
	}

	adminLog.WithField("count", len(resp)).Debug("ListAuditLogs returning results")

	JSON(w, http.StatusOK, map[string]interface{}{
		"logs":     resp,
		"total":    total,
		"page":     page,
		"per_page": perPage,
	})
}

func (h *AdminHandler) ListUserAuditLogs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	asn := middleware.GetASN(ctx)
	action := r.URL.Query().Get("action")

	page := 1
	perPage := 25
	if p, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && p > 0 {
		page = p
	}
	if pp, err := strconv.Atoi(r.URL.Query().Get("per_page")); err == nil && pp > 0 && pp <= 200 {
		perPage = pp
	}

	logs, total, err := h.auditSvc.ListForASN(ctx, asn, action, page, perPage)
	if err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to fetch audit logs")
		return
	}

	type auditResp struct {
		ID        string      `json:"id"`
		Action    string      `json:"action"`
		Operator  string      `json:"operator"`
		TargetID  *string     `json:"target_id,omitempty"`
		Detail    interface{} `json:"detail,omitempty"`
		CreatedAt string      `json:"created_at"`
	}

	resp := make([]auditResp, 0, len(logs))
	for _, l := range logs {
		resp = append(resp, auditResp{
			ID:        l.ID,
			Action:    l.Action,
			Operator:  l.Operator,
			TargetID:  l.TargetID,
			Detail:    l.Detail,
			CreatedAt: l.CreatedAt.UTC().Format(time.RFC3339),
		})
	}

	JSON(w, http.StatusOK, map[string]interface{}{
		"logs":     resp,
		"total":    total,
		"page":     page,
		"per_page": perPage,
	})
}

func (h *AdminHandler) GetNotificationSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	settings, err := h.settings.GetAllNotificationSettings(ctx)
	if err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to fetch settings")
		return
	}

	result := make([]notificationSetting, 0, len(settings))
	for _, s := range settings {
		result = append(result, notificationSetting{
			Key:            s.SettingKey,
			Value:          s.SettingValue,
			ThresholdValue: s.ThresholdValue,
			Description:    s.Description,
		})
	}
	JSON(w, http.StatusOK, map[string]interface{}{"settings": result})
}

func (h *AdminHandler) UpdateNotificationSetting(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	adminEmail := middleware.GetEmail(ctx)

	var body struct {
		Key            string  `json:"key"`
		Value          *string `json:"value"`
		ThresholdValue *string `json:"threshold_value"`
	}
	if err := DecodeJSON(r, &body); err != nil || body.Key == "" {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "key is required")
		return
	}

	adminLog.WithFields(logrus.Fields{"key": body.Key, "value": body.Value, "threshold": body.ThresholdValue}).Debug("UpdateNotificationSetting entry")

	if body.Value == nil && body.ThresholdValue == nil {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "nothing to update")
		return
	}

	value := ""
	if body.Value != nil {
		value = *body.Value
	}
	threshold := ""
	if body.ThresholdValue != nil {
		threshold = *body.ThresholdValue
	}

	if err := h.settings.UpdateNotificationSetting(ctx, body.Key, value, threshold); err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to update setting")
		return
	}

	h.c.Delete(cache.BucketSettings, cache.SettingKey(cache.SettingVal, body.Key))
	h.c.Delete(cache.BucketSettings, cache.SettingKey(cache.SettingThr, body.Key))

	h.auditSvc.Log(ctx, "notification.update", adminEmail, nil, map[string]interface{}{
		"key": body.Key, "value": body.Value, "threshold": body.ThresholdValue,
	})

	JSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

func (h *AdminHandler) GetSiteSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	settings, err := h.settings.GetAllSiteSettings(ctx)
	if err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to fetch site settings")
		return
	}

	result := make([]siteSetting, 0, len(settings))
	for _, s := range settings {
		result = append(result, siteSetting{
			Key:         s.SettingKey,
			Value:       s.SettingValue,
			Description: s.Description,
		})
	}
	JSON(w, http.StatusOK, map[string]interface{}{"settings": result})
}

func (h *AdminHandler) UpdateSiteSetting(w http.ResponseWriter, r *http.Request) {
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

	if err := h.settings.SetSiteSetting(ctx, body.Key, *body.Value); err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to update site setting")
		return
	}

	h.c.Delete(cache.BucketSettings, cache.SettingKey(cache.SiteSettingVal, body.Key))

	h.auditSvc.Log(ctx, "site_setting.update", adminEmail, nil, map[string]interface{}{
		"key": body.Key, "value": *body.Value,
	})

	JSON(w, http.StatusOK, map[string]string{"status": "updated"})
}
