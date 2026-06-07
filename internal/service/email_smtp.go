package service

import (
	"fmt"
	"time"

	"github.com/akaere/autopeer-center/internal/config"
	"github.com/sirupsen/logrus"
	mail "github.com/wneessen/go-mail"
)

// smtpTransport sends plain-text email via an SMTP server. Unlike the API
// transport (which renders remotely), it renders templates locally with
// renderText and delivers a single text/plain body. It supports plain-text
// email only — by design there is no HTML alternative part.
type smtpTransport struct {
	host     string
	port     int
	username string
	password string
	fromAddr string
	fromName string
	tlsMode  string // "starttls" | "tls" | "none"
}

func newSMTPTransport(cfg config.EmailConfig) *smtpTransport {
	return &smtpTransport{
		host:     cfg.SMTPHost,
		port:     cfg.SMTPPort,
		username: cfg.SMTPUsername,
		password: cfg.SMTPPassword,
		fromAddr: cfg.SMTPFrom,
		fromName: cfg.SMTPFromName,
		tlsMode:  cfg.SMTPTLS,
	}
}

func (t *smtpTransport) send(to, template string, vars map[string]interface{}) error {
	if t.host == "" {
		emailLog.WithFields(logrus.Fields{
			"template": template,
			"to":       MaskEmail(to),
		}).Warn("email skipped: SMTP_HOST not configured")
		return fmt.Errorf("smtp not configured: SMTP_HOST is empty")
	}

	subject, body, ok := renderText(template, vars)
	if !ok {
		emailLog.WithField("template", template).Warn("smtp: no text renderer, using generic fallback")
		subject, body = fallbackText(template, vars)
	}

	msg := mail.NewMsg()
	if err := msg.FromFormat(t.fromName, t.fromAddr); err != nil {
		emailLog.WithError(err).WithField("from", t.fromAddr).Error("smtp: invalid From address")
		return fmt.Errorf("smtp from: %w", err)
	}
	if err := msg.To(to); err != nil {
		emailLog.WithError(err).WithField("to", MaskEmail(to)).Error("smtp: invalid To address")
		return fmt.Errorf("smtp to: %w", err)
	}
	msg.Subject(subject)
	msg.SetBodyString(mail.TypeTextPlain, body)

	opts := []mail.Option{
		mail.WithPort(t.port),
		mail.WithTimeout(10 * time.Second),
	}
	switch t.tlsMode {
	case "tls":
		opts = append(opts, mail.WithSSL())
	case "none":
		opts = append(opts, mail.WithTLSPolicy(mail.NoTLS))
	default: // "starttls"
		opts = append(opts, mail.WithTLSPolicy(mail.TLSMandatory))
	}
	if t.username != "" {
		opts = append(opts,
			mail.WithSMTPAuth(mail.SMTPAuthAutoDiscover),
			mail.WithUsername(t.username),
			mail.WithPassword(t.password),
		)
	}

	client, err := mail.NewClient(t.host, opts...)
	if err != nil {
		emailLog.WithError(err).WithFields(logrus.Fields{
			"template": template,
			"to":       MaskEmail(to),
		}).Error("smtp: create client failed")
		return fmt.Errorf("smtp client: %w", err)
	}

	if err := client.DialAndSend(msg); err != nil {
		emailLog.WithError(err).WithFields(logrus.Fields{
			"template": template,
			"to":       MaskEmail(to),
		}).Error("smtp: send failed")
		return fmt.Errorf("smtp send: %w", err)
	}

	emailLog.WithFields(logrus.Fields{
		"template": template,
		"to":       MaskEmail(to),
		"success":  true,
	}).Debug("smtp send complete")
	return nil
}
