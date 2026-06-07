package service

import (
	"encoding/json"
	"fmt"

	"github.com/akaere/autopeer-center/internal/config"
	"github.com/sirupsen/logrus"
)

var emailLog = logrus.WithField("pkg", "service.email")

// EmailLevel defines the user's email notification preference.
//
//	0 = no emails
//	1 = urgent only (peer approved/rejected/suspended/unsuspended/deleted)
//	2 = urgent + general (adds peer-submitted, bgp alerts, handshake stale)
//	3 = all (adds latency alerts)
const (
	EmailLevelNone    = 0
	EmailLevelUrgent  = 1
	EmailLevelGeneral = 2
	EmailLevelAll     = 3
)

// emailTransport delivers one rendered message to one recipient. The SMTP
// transport (smtpTransport) is always available and renders templates to plain
// text locally; the optional HTTP API transport (registered via newAPITransport
// in email_api.go) renders remotely. The transport decides how to render.
type emailTransport interface {
	send(to, template string, vars map[string]interface{}) error
}

type EmailService struct {
	transport             emailTransport
	emailLevelFn          func(asn int64) int // returns 0 if not set or asn not found
	notificationAllowedFn func(asn int64, key string) bool
}

// newAPITransport builds the HTTP API transport. It is registered by the API
// backend implementation (email_api.go) in its init. When that file is absent
// (the open-source build, which is SMTP-only) it stays nil and the service
// falls back to SMTP regardless of the configured provider.
var newAPITransport func(cfg config.EmailConfig) emailTransport

// NewEmailService builds the email service with the backend selected by
// cfg.Provider ("smtp" → SMTP plain-text; anything else → HTTP API when that
// backend is compiled in, otherwise SMTP).
func NewEmailService(cfg config.EmailConfig) *EmailService {
	var t emailTransport
	if cfg.Provider != "smtp" && newAPITransport != nil {
		t = newAPITransport(cfg)
	} else {
		t = newSMTPTransport(cfg)
	}
	return &EmailService{transport: t}
}

// ActiveBackend reports which transport NewEmailService(cfg) would select:
// "api" or "smtp". The admin system-status endpoint uses it to display the
// live backend.
func ActiveBackend(cfg config.EmailConfig) string {
	if cfg.Provider != "smtp" && newAPITransport != nil {
		return "api"
	}
	return "smtp"
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

// send normalizes vars to a map and dispatches to the configured transport.
func (s *EmailService) send(to, template string, vars interface{}) error {
	return s.transport.send(to, template, toVarMap(vars))
}

// toVarMap coerces the vars passed by the public Send* methods into a map.
// All callers already pass map[string]interface{}, which is the fast path; the
// JSON round-trip is a defensive fallback for any future non-map caller and
// keeps API-mode marshalling lossless.
func toVarMap(vars interface{}) map[string]interface{} {
	if vars == nil {
		return map[string]interface{}{}
	}
	if m, ok := vars.(map[string]interface{}); ok {
		return m
	}
	b, err := json.Marshal(vars)
	if err != nil {
		return map[string]interface{}{}
	}
	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		return map[string]interface{}{}
	}
	return m
}
