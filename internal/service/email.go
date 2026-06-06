package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/sirupsen/logrus"
)

var emailLog = logrus.WithField("pkg", "service.email")

// EmailLevel defines the user's email notification preference.
//   0 = no emails
//   1 = urgent only (peer approved/rejected/suspended/unsuspended/deleted)
//   2 = urgent + general (adds peer-submitted, bgp alerts, handshake stale)
//   3 = all (adds latency alerts)
const (
	EmailLevelNone    = 0
	EmailLevelUrgent  = 1
	EmailLevelGeneral = 2
	EmailLevelAll     = 3
)

type EmailService struct {
	apiURL                string
	apiKey                string
	httpClient            *http.Client
	emailLevelFn          func(asn int64) int // returns 0 if not set or asn not found
	notificationAllowedFn func(asn int64, key string) bool
}

func NewEmailService(apiURL, apiKey string) *EmailService {
	return &EmailService{
		apiURL: apiURL,
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// SetEmailLevelFn registers a function that returns the email level for a given ASN.
// Called before each user-targeted send to enforce per-user preferences.
func (s *EmailService) SetEmailLevelFn(fn func(asn int64) int) {
	s.emailLevelFn = fn
}

func (s *EmailService) SetNotificationAllowedFn(fn func(asn int64, key string) bool) {
	s.notificationAllowedFn = fn
}

// getEmailLevel returns the configured email level for an ASN (default 0 if not set).
func (s *EmailService) getEmailLevel(asn int64) int {
	if s.emailLevelFn == nil {
		return EmailLevelNone
	}
	return s.emailLevelFn(asn)
}

// allowedLevel returns true if the ASN's email level meets the required minimum.
func (s *EmailService) allowedLevel(asn int64, required int) bool {
	return s.getEmailLevel(asn) >= required
}

func (s *EmailService) allowedNotification(asn int64, key string, legacyRequired int) bool {
	if s.notificationAllowedFn != nil {
		return s.notificationAllowedFn(asn, key)
	}
	return s.allowedLevel(asn, legacyRequired)
}

type emailRequest struct {
	To       string      `json:"to"`
	Template string      `json:"template"`
	Vars     interface{} `json:"vars"`
}

type emailResponse struct {
	Success   bool   `json:"success"`
	MessageID string `json:"messageId"`
	Error     string `json:"error"`
	Message   string `json:"message"`
}

func (s *EmailService) SendVerificationCode(to string, asn int64, code string) error {
	if s.notificationAllowedFn != nil && !s.notificationAllowedFn(asn, NotificationAuthLoginCode) {
		emailLog.WithFields(logrus.Fields{"template": "verification-code", "asn": asn}).Debug("email suppressed by user notification preference")
		return nil
	}
	emailLog.WithFields(logrus.Fields{
		"template": "verification-code",
		"to":       MaskEmail(to),
		"asn":      asn,
	}).Debug("template send initiated")
	return s.send(to, "verification-code", map[string]interface{}{
		"asn":  asn,
		"code": code,
	})
}

func (s *EmailService) SendPeerSubmitted(to string, asn int64, nodeName, nodeLocation, ourWgPubkey, ourLla, ourPublicIp, createdAt string) error {
	if !s.allowedNotification(asn, NotificationPeerSubmitted, EmailLevelGeneral) {
		emailLog.WithFields(logrus.Fields{"template": "peer-submitted", "asn": asn}).Debug("email suppressed by user level preference")
		return nil
	}
	emailLog.WithFields(logrus.Fields{
		"template": "peer-submitted",
		"to":       MaskEmail(to),
		"asn":      asn,
	}).Debug("template send initiated")
	return s.send(to, "peer-submitted", map[string]interface{}{
		"asn":          asn,
		"nodeName":     nodeName,
		"nodeLocation": nodeLocation,
		"ourWgPubkey":  ourWgPubkey,
		"ourLla":       ourLla,
		"ourPublicIp":  ourPublicIp,
		"createdAt":    createdAt,
	})
}

func (s *EmailService) SendPeerApproved(to string, vars map[string]interface{}) error {
	asn, _ := vars["asn"].(int64)
	if !s.allowedNotification(asn, NotificationPeerApproved, EmailLevelUrgent) {
		emailLog.WithFields(logrus.Fields{"template": "peer-approved", "asn": asn}).Debug("email suppressed by user level preference")
		return nil
	}
	emailLog.WithFields(logrus.Fields{
		"template": "peer-approved",
		"to":       MaskEmail(to),
	}).Debug("template send initiated")
	return s.send(to, "peer-approved", vars)
}

func (s *EmailService) SendPeerRejected(to string, asn int64, nodeName, reason string) error {
	if !s.allowedNotification(asn, NotificationPeerRejected, EmailLevelUrgent) {
		emailLog.WithFields(logrus.Fields{"template": "peer-rejected", "asn": asn}).Debug("email suppressed by user level preference")
		return nil
	}
	emailLog.WithFields(logrus.Fields{
		"template": "peer-rejected",
		"to":       MaskEmail(to),
		"asn":      asn,
	}).Debug("template send initiated")
	return s.send(to, "peer-rejected", map[string]interface{}{
		"asn":      asn,
		"nodeName": nodeName,
		"reason":   reason,
	})
}

func (s *EmailService) SendRaw(to, template string, vars map[string]interface{}) error {
	emailLog.WithFields(logrus.Fields{
		"template": template,
		"to":       MaskEmail(to),
	}).Debug("template send initiated")
	return s.send(to, template, vars)
}

func (s *EmailService) SendPeerSuspended(to string, asn int64, nodeName, reason string) error {
	if !s.allowedNotification(asn, NotificationPeerSuspended, EmailLevelUrgent) {
		emailLog.WithFields(logrus.Fields{"template": "peer-suspended", "asn": asn}).Debug("email suppressed by user level preference")
		return nil
	}
	emailLog.WithFields(logrus.Fields{
		"template": "peer-suspended",
		"to":       MaskEmail(to),
		"asn":      asn,
	}).Debug("template send initiated")
	return s.send(to, "peer-suspended", map[string]interface{}{
		"asn": asn, "nodeName": nodeName, "reason": reason,
	})
}

func (s *EmailService) SendPeerUnsuspended(to string, asn int64, nodeName string) error {
	if !s.allowedNotification(asn, NotificationPeerUnsuspended, EmailLevelUrgent) {
		emailLog.WithFields(logrus.Fields{"template": "peer-unsuspended", "asn": asn}).Debug("email suppressed by user level preference")
		return nil
	}
	emailLog.WithFields(logrus.Fields{
		"template": "peer-unsuspended",
		"to":       MaskEmail(to),
		"asn":      asn,
	}).Debug("template send initiated")
	return s.send(to, "peer-unsuspended", map[string]interface{}{
		"asn": asn, "nodeName": nodeName,
	})
}

func (s *EmailService) SendPeerDeleted(to string, asn int64, nodeName string) error {
	if !s.allowedNotification(asn, NotificationPeerDeleted, EmailLevelUrgent) {
		emailLog.WithFields(logrus.Fields{"template": "peer-deleted", "asn": asn}).Debug("email suppressed by user level preference")
		return nil
	}
	emailLog.WithFields(logrus.Fields{
		"template": "peer-deleted",
		"to":       MaskEmail(to),
		"asn":      asn,
	}).Debug("template send initiated")
	return s.send(to, "peer-deleted", map[string]interface{}{
		"asn": asn, "nodeName": nodeName,
	})
}

func (s *EmailService) SendPeerBGPDown(to string, asn int64, nodeName, bgpState string) error {
	if !s.allowedNotification(asn, NotificationPeerBGPDown, EmailLevelGeneral) {
		emailLog.WithFields(logrus.Fields{"template": "peer-bgp-down", "asn": asn}).Debug("email suppressed by user level preference")
		return nil
	}
	emailLog.WithFields(logrus.Fields{
		"template": "peer-bgp-down",
		"to":       MaskEmail(to),
		"asn":      asn,
	}).Debug("template send initiated")
	return s.send(to, "peer-bgp-down", map[string]interface{}{
		"asn": asn, "nodeName": nodeName, "bgpState": bgpState,
	})
}

func (s *EmailService) SendPeerBGPRecovered(to string, asn int64, nodeName string) error {
	if !s.allowedNotification(asn, NotificationPeerBGPRecovered, EmailLevelGeneral) {
		emailLog.WithFields(logrus.Fields{"template": "peer-bgp-recovered", "asn": asn}).Debug("email suppressed by user level preference")
		return nil
	}
	emailLog.WithFields(logrus.Fields{
		"template": "peer-bgp-recovered",
		"to":       MaskEmail(to),
		"asn":      asn,
	}).Debug("template send initiated")
	return s.send(to, "peer-bgp-recovered", map[string]interface{}{
		"asn": asn, "nodeName": nodeName,
	})
}

func (s *EmailService) SendPeerHandshakeStale(to string, asn int64, nodeName, lastHandshake string) error {
	if !s.allowedNotification(asn, NotificationPeerHandshakeStale, EmailLevelGeneral) {
		emailLog.WithFields(logrus.Fields{"template": "peer-handshake-stale", "asn": asn}).Debug("email suppressed by user level preference")
		return nil
	}
	emailLog.WithFields(logrus.Fields{
		"template": "peer-handshake-stale",
		"to":       MaskEmail(to),
		"asn":      asn,
	}).Debug("template send initiated")
	return s.send(to, "peer-handshake-stale", map[string]interface{}{
		"asn": asn, "nodeName": nodeName, "lastHandshake": lastHandshake,
	})
}

func (s *EmailService) SendNodeOffline(adminEmails []string, nodeName, location, offlineSince string) {
	for _, email := range adminEmails {
		if email == "" {
			continue
		}
		emailLog.WithFields(logrus.Fields{
			"template": "node-offline",
			"to":       MaskEmail(email),
		}).Debug("template send initiated")
		s.send(email, "node-offline", map[string]interface{}{
			"nodeName": nodeName, "location": location, "offlineSince": offlineSince,
		})
	}
}

func (s *EmailService) SendPeerLatencyHigh(to string, asn int64, vars map[string]interface{}) error {
	if !s.allowedNotification(asn, NotificationPeerLatencyHigh, EmailLevelAll) {
		emailLog.WithFields(logrus.Fields{"template": "peer-latency-high", "asn": asn}).Debug("email suppressed by user level preference")
		return nil
	}
	emailLog.WithFields(logrus.Fields{
		"template": "peer-latency-high",
		"to":       MaskEmail(to),
	}).Debug("template send initiated")
	return s.send(to, "peer-latency-high", vars)
}

func (s *EmailService) SendPeerLatencyRecovered(to string, asn int64, vars map[string]interface{}) error {
	if !s.allowedNotification(asn, NotificationPeerLatencyRecovered, EmailLevelAll) {
		emailLog.WithFields(logrus.Fields{"template": "peer-latency-recovered", "asn": asn}).Debug("email suppressed by user level preference")
		return nil
	}
	emailLog.WithFields(logrus.Fields{
		"template": "peer-latency-recovered",
		"to":       MaskEmail(to),
	}).Debug("template send initiated")
	return s.send(to, "peer-latency-recovered", vars)
}

func (s *EmailService) SendAgentUpdated(adminEmails []string, nodeName, oldVersion, newVersion string) {
	for _, email := range adminEmails {
		if email == "" {
			continue
		}
		emailLog.WithFields(logrus.Fields{
			"template": "agent-updated",
			"to":       MaskEmail(email),
		}).Debug("template send initiated")
		s.send(email, "agent-updated", map[string]interface{}{
			"nodeName": nodeName, "oldVersion": oldVersion, "newVersion": newVersion,
		})
	}
}

func (s *EmailService) SendPeerMTUUpdated(to string, asn int64, nodeName string, oldMtu, newMtu *int) error {
	if !s.allowedNotification(asn, NotificationPeerMTUUpdated, EmailLevelUrgent) {
		emailLog.WithFields(logrus.Fields{"template": "peer-mtu-updated", "asn": asn}).Debug("email suppressed by user level preference")
		return nil
	}
	var om, nm string
	if oldMtu != nil {
		om = fmt.Sprintf("%d", *oldMtu)
	}
	if newMtu != nil {
		nm = fmt.Sprintf("%d", *newMtu)
	}
	emailLog.WithFields(logrus.Fields{
		"template": "peer-mtu-updated",
		"to":       MaskEmail(to),
		"asn":      asn,
	}).Debug("template send initiated")
	return s.send(to, "peer-mtu-updated", map[string]interface{}{
		"asn": asn, "nodeName": nodeName, "oldMtu": om, "newMtu": nm,
	})
}

var errEmailNotConfigured = fmt.Errorf("email service not configured: EMAIL_API_URL is empty")

func (s *EmailService) send(to, template string, vars interface{}) error {
	if s.apiURL == "" {
		emailLog.WithFields(logrus.Fields{
			"template": template,
			"to":       MaskEmail(to),
		}).Warn("email skipped: EMAIL_API_URL not configured")
		return errEmailNotConfigured
	}

	body, err := json.Marshal(emailRequest{
		To:       to,
		Template: template,
		Vars:     vars,
	})
	if err != nil {
		emailLog.WithError(err).Error("marshal email request failed")
		return fmt.Errorf("marshal email request: %w", err)
	}

	req, err := http.NewRequest("POST", s.apiURL+"/send", bytes.NewReader(body))
	if err != nil {
		emailLog.WithError(err).Error("create email request failed")
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", s.apiKey)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		emailLog.WithError(err).WithFields(logrus.Fields{
			"template": template,
			"to":       MaskEmail(to),
		}).Error("send email HTTP request failed")
		return fmt.Errorf("send email: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		emailLog.WithError(err).Error("read email response failed")
		return fmt.Errorf("read response: %w", err)
	}

	emailLog.WithFields(logrus.Fields{
		"template": template,
		"to":       MaskEmail(to),
		"status":   resp.StatusCode,
		"success":  resp.StatusCode == http.StatusOK,
	}).Debug("email API response")

	if resp.StatusCode != http.StatusOK {
		var errResp emailResponse
		json.Unmarshal(respBody, &errResp)
		emailLog.WithFields(logrus.Fields{
			"template": template,
			"to":       MaskEmail(to),
			"status":   resp.StatusCode,
			"message":  errResp.Message,
		}).Error("email API returned error")
		return fmt.Errorf("email API error (%d): %s", resp.StatusCode, errResp.Message)
	}

	return nil
}
