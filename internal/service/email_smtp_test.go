package service

import (
	"bufio"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/akaere/autopeer-center/internal/config"
)

// fakeSMTPServer speaks just enough SMTP (no TLS, no auth) to accept a single
// message and capture the raw DATA payload. It lets us verify the smtpTransport
// end-to-end without an external mail server.
func fakeSMTPServer(t *testing.T) (addr string, captured chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	captured = make(chan string, 1)
	go func() {
		defer ln.Close()
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		r := bufio.NewReader(conn)
		w := conn

		write := func(s string) { _, _ = w.Write([]byte(s + "\r\n")) }
		write("220 fake ESMTP")
		var data strings.Builder
		inData := false
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			if inData {
				if strings.TrimRight(line, "\r\n") == "." {
					inData = false
					write("250 OK queued")
					captured <- data.String()
					continue
				}
				data.WriteString(line)
				continue
			}
			cmd := strings.ToUpper(strings.TrimSpace(line))
			switch {
			case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
				write("250-fake")
				write("250 SIZE 10485760")
			case strings.HasPrefix(cmd, "MAIL FROM"), strings.HasPrefix(cmd, "RCPT TO"):
				write("250 OK")
			case strings.HasPrefix(cmd, "DATA"):
				write("354 End data with <CR><LF>.<CR><LF>")
				inData = true
			case strings.HasPrefix(cmd, "QUIT"):
				write("221 Bye")
				return
			default:
				write("250 OK")
			}
		}
	}()
	return ln.Addr().String(), captured
}

func TestSMTPTransportSendsPlainText(t *testing.T) {
	addr, captured := fakeSMTPServer(t)
	host, portStr, _ := net.SplitHostPort(addr)
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}

	tr := newSMTPTransport(config.EmailConfig{
		Provider:     "smtp",
		SMTPHost:     host,
		SMTPPort:     port,
		SMTPFrom:     "noreply@example.test",
		SMTPFromName: "AutoPeer",
		SMTPTLS:      "none",
	})

	if err := tr.send("user@example.test", "verification-code", map[string]interface{}{
		"asn":  int64(4242420000),
		"code": "654321",
	}); err != nil {
		t.Fatalf("send: %v", err)
	}

	select {
	case msg := <-captured:
		for _, want := range []string{
			"Subject:", "verification code", "654321", "AS4242420000",
			"Content-Type: text/plain",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("message missing %q\n---\n%s", want, msg)
			}
		}
		// Plain-text only: there must be no HTML alternative part.
		if strings.Contains(strings.ToLower(msg), "text/html") {
			t.Errorf("expected plain-text only, found text/html part\n---\n%s", msg)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for captured message")
	}
}
