package service

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// This file renders the email templates to plain text on the Go side. It is used
// only by the SMTP transport (the API transport renders remotely in the worker).
// The subjects and visible text are faithful ports of the React templates in
// email-api/src/templates/*.tsx — keep them in sync when those change.

// renderText renders a template name + vars to a plain-text subject and body.
// ok is false when no Go renderer is registered for the given template.
func renderText(template string, vars map[string]interface{}) (subject, body string, ok bool) {
	r, found := textRenderers[template]
	if !found {
		return "", "", false
	}
	subject, body = r(varMap(vars))
	return subject, withFooter(body), true
}

// fallbackText renders an unknown template as a generic key/value dump so that
// SendRaw with a template the Go side does not know still delivers something.
func fallbackText(template string, vars map[string]interface{}) (subject, body string) {
	var b textBuilder
	b.line("AutoPeer notification")
	b.blank()
	if template != "" {
		b.kv("Template", template)
	}
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	m := varMap(vars)
	for _, k := range keys {
		b.kv(k, m.str(k))
	}
	return "AutoPeer notification", withFooter(b.String())
}

type textRenderer func(m varMap) (subject, body string)

var textRenderers = map[string]textRenderer{
	"verification-code":       renderVerificationCode,
	"peer-submitted":          renderPeerSubmitted,
	"peer-approved":           renderPeerApproved,
	"peer-rejected":           renderPeerRejected,
	"peer-suspended":          renderPeerSuspended,
	"peer-unsuspended":        renderPeerUnsuspended,
	"peer-deleted":            renderPeerDeleted,
	"peer-bgp-down":           renderPeerBGPDown,
	"peer-bgp-recovered":      renderPeerBGPRecovered,
	"node-offline":            renderNodeOffline,
	"agent-updated":           renderAgentUpdated,
	"peer-handshake-stale":    renderPeerHandshakeStale,
	"peer-latency-high":       renderPeerLatencyHigh,
	"peer-latency-recovered":  renderPeerLatencyRecovered,
	"peer-mtu-updated":        renderPeerMTUUpdated,
	"peer-endpoint-mismatch":  renderPeerEndpointMismatch,
	"peer-endpoint-recovered": renderPeerEndpointRecovered,
	"test-email":              renderTestEmail,
}

// ── helpers ──────────────────────────────────────────────────────────────────

// varMap wraps the vars map with safe, type-tolerant accessors. Numeric values
// may arrive as int/int64 (Go callers) or float64 (JSON round-trip); strings may
// be pre-formatted by the callers.
type varMap map[string]interface{}

// str returns the string form of key; missing/nil yields "".
func (m varMap) str(key string) string {
	switch x := m[key].(type) {
	case nil:
		return ""
	case string:
		return x
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		// Integers carried as float64 (JSON) should not use scientific notation.
		if x == float64(int64(x)) {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	default:
		return fmt.Sprintf("%v", x)
	}
}

// asn renders the asn field as "AS<n>".
func (m varMap) asn() string {
	return "AS" + m.str("asn")
}

// timepoints returns the latency-high timepoints as (time, rtt_ms) pairs.
func (m varMap) timepoints() []struct{ Time, RTT string } {
	raw, ok := m["timepoints"].([]interface{})
	if !ok {
		return nil
	}
	out := make([]struct{ Time, RTT string }, 0, len(raw))
	for _, item := range raw {
		tp, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		pm := varMap(tp)
		out = append(out, struct{ Time, RTT string }{pm.str("time"), pm.str("rtt_ms")})
	}
	return out
}

// textBuilder accumulates plain-text lines.
type textBuilder struct{ b strings.Builder }

func (t *textBuilder) line(s string) { t.b.WriteString(s); t.b.WriteByte('\n') }
func (t *textBuilder) blank()        { t.b.WriteByte('\n') }
func (t *textBuilder) kv(label, value string) {
	t.line("  " + label + ": " + value)
}
func (t *textBuilder) String() string {
	return strings.TrimRight(t.b.String(), "\n")
}

// withFooter appends the standard footer used by the HTML layout.
func withFooter(body string) string {
	return body + "\n\n—\nAutomated notification from Akaere Networks AutoPeer.\nautopeer.su · noc@akae.re · AS213605\n"
}

// ── renderers ────────────────────────────────────────────────────────────────

func renderVerificationCode(m varMap) (string, string) {
	subject := "AutoPeer verification code – " + m.asn()
	var b textBuilder
	b.line("Verify your sign in")
	b.blank()
	b.line("You are signing in to Akaere Networks AutoPeer as " + m.asn() + ".")
	b.line("Enter this verification code to continue:")
	b.blank()
	b.line("    " + m.str("code"))
	b.blank()
	b.line("This code expires in 10 minutes.")
	b.line("If you did not request this code, you can ignore this email. No changes will be made to your account.")
	return subject, b.String()
}

func renderPeerSubmitted(m varMap) (string, string) {
	subject := "Peering request submitted – " + m.asn()
	var b textBuilder
	b.line("Peering request submitted")
	b.blank()
	b.line("Your peering request for " + m.asn() + " has been submitted and is waiting for review.")
	b.blank()
	b.line("Your request")
	b.kv("Your ASN", m.asn())
	b.kv("Target node", m.str("nodeName")+" ("+m.str("nodeLocation")+")")
	b.kv("Submitted at", m.str("createdAt"))
	b.blank()
	b.line("Prepare your configuration")
	b.line("While you wait for approval, you can prepare your WireGuard configuration using our node details:")
	b.kv("Our public IP", m.str("ourPublicIp"))
	b.kv("Our WG public key", m.str("ourWgPubkey"))
	b.kv("Our link-local address", m.str("ourLla"))
	b.blank()
	b.line("You will receive another email after the request is reviewed.")
	return subject, b.String()
}

func renderPeerApproved(m varMap) (string, string) {
	asn := m.asn()
	subject := "Peering activated – " + asn

	wgConfig := strings.Join([]string{
		"[Interface]",
		"PrivateKey = <YOUR_PRIVATE_KEY>",
		"Address = " + m.str("remoteLla") + "/64",
		"Table = off",
		"",
		"[Peer]",
		"PublicKey = " + m.str("ourWgPubkey"),
		"AllowedIPs = 10.0.0.0/8, 172.16.0.0/12, fd00::/8, fe80::/10",
		"Endpoint = " + m.str("ourPublicIp") + ":" + m.str("listenPort"),
		"PersistentKeepalive = 25",
	}, "\n")

	birdConfig := strings.Join([]string{
		"protocol bgp dn42_" + m.str("asn") + " from dnpeers {",
		"    neighbor " + m.str("ourLla") + " % '" + m.str("wgInterfaceName") + "' as " + m.str("asn") + ";",
		"}",
	}, "\n")

	var b textBuilder
	b.line("Peering activated")
	b.line(asn + " · " + m.str("nodeName") + " (" + m.str("nodeLocation") + ")")
	b.blank()
	b.line("Our connection details")
	b.kv("ASN", asn)
	b.kv("Public IP", m.str("ourPublicIp"))
	b.kv("WireGuard pubkey", m.str("ourWgPubkey"))
	b.kv("Listen port", m.str("listenPort"))
	b.kv("Link-local address", m.str("ourLla"))
	b.kv("WG interface", m.str("wgInterfaceName"))
	b.blank()
	b.line("WireGuard configuration")
	b.line("Add this to your WireGuard configuration. Replace <YOUR_PRIVATE_KEY> with your private key.")
	b.blank()
	b.line(wgConfig)
	b.blank()
	b.line("BIRD configuration")
	b.line("Add this to your BIRD BGP configuration.")
	b.blank()
	b.line(birdConfig)
	return subject, b.String()
}

func renderPeerRejected(m varMap) (string, string) {
	subject := "Peering request declined – " + m.asn()
	var b textBuilder
	b.line("Peering request declined")
	b.blank()
	b.line("Your peering request for " + m.asn() + " on " + m.str("nodeName") + " has been reviewed and was not approved.")
	b.blank()
	b.line("Reason: " + m.str("reason"))
	b.blank()
	b.line("For questions about this review, contact noc@akae.re.")
	return subject, b.String()
}

func renderPeerSuspended(m varMap) (string, string) {
	subject := "Peering suspended – " + m.asn()
	var b textBuilder
	b.line("Peering suspended")
	b.blank()
	b.line("Your peering session for " + m.asn() + " on " + m.str("nodeName") + " has been suspended.")
	if reason := m.str("reason"); reason != "" {
		b.blank()
		b.line("Reason: " + reason)
	}
	b.blank()
	b.line("For questions about this suspension, contact noc@akae.re.")
	return subject, b.String()
}

func renderPeerUnsuspended(m varMap) (string, string) {
	subject := "Peering restored – " + m.asn()
	var b textBuilder
	b.line("Peering restored")
	b.line(m.asn() + " · " + m.str("nodeName"))
	b.blank()
	b.line("Your peering session for " + m.asn() + " on " + m.str("nodeName") + " has been restored and is now active again.")
	b.line("No further action is required from you.")
	return subject, b.String()
}

func renderPeerDeleted(m varMap) (string, string) {
	subject := "Peering removed – " + m.asn()
	var b textBuilder
	b.line("Peering removed")
	b.blank()
	b.line("Your peering session for " + m.asn() + " on " + m.str("nodeName") + " has been removed.")
	b.line("New peering requests can be submitted from autopeer.su.")
	return subject, b.String()
}

func renderPeerBGPDown(m varMap) (string, string) {
	subject := "BGP down – " + m.asn()
	var b textBuilder
	b.line("BGP session down")
	b.blank()
	b.line("The BGP session for " + m.asn() + " on " + m.str("nodeName") + " has gone down.")
	b.blank()
	b.line("Current state: " + m.str("bgpState"))
	b.blank()
	b.line("We detected the issue and will continue monitoring. If the session recovers, we will send a follow-up email.")
	b.blank()
	b.line("Troubleshooting steps")
	b.line("  1. Check your WireGuard tunnel is up.")
	b.line("  2. Verify your BIRD configuration matches our details.")
	b.line("  3. Check that your firewall allows our IP and port.")
	b.blank()
	b.line("If the session remains down, contact noc@akae.re.")
	return subject, b.String()
}

func renderPeerBGPRecovered(m varMap) (string, string) {
	subject := "BGP recovered – " + m.asn()
	var b textBuilder
	b.line("BGP recovered")
	b.line(m.asn() + " · " + m.str("nodeName"))
	b.blank()
	b.line("The BGP session for " + m.asn() + " on " + m.str("nodeName") + " has been re-established.")
	b.line("Peering is now active again. No further action is required from you.")
	return subject, b.String()
}

func renderNodeOffline(m varMap) (string, string) {
	subject := "Node offline – " + m.str("nodeName")
	var b textBuilder
	b.line("Node offline")
	b.blank()
	b.line("Node " + m.str("nodeName") + " (" + m.str("location") + ") has gone offline.")
	b.blank()
	b.line("Offline since: " + m.str("offlineSince"))
	b.blank()
	b.line("All peering sessions on this node are affected. If this persists, please investigate the node's network connectivity and agent status.")
	return subject, b.String()
}

func renderAgentUpdated(m varMap) (string, string) {
	subject := "Agent updated – " + m.str("nodeName")
	var b textBuilder
	b.line("Agent updated")
	b.line(m.str("nodeName"))
	b.blank()
	b.kv("Node", m.str("nodeName"))
	b.kv("Previous version", m.str("oldVersion"))
	b.kv("New version", m.str("newVersion"))
	b.blank()
	b.line("The agent has been updated and is running normally.")
	return subject, b.String()
}

func renderPeerHandshakeStale(m varMap) (string, string) {
	subject := "WireGuard handshake stale – " + m.asn()
	var b textBuilder
	b.line("WireGuard handshake stale")
	b.blank()
	b.line("The WireGuard tunnel for " + m.asn() + " on " + m.str("nodeName") + " has not had a recent handshake.")
	b.blank()
	b.line("Last handshake: " + m.str("lastHandshake"))
	b.blank()
	b.line("This may indicate that your peer is not reachable. Please check:")
	b.line("  1. Your WireGuard interface is up.")
	b.line("  2. Your endpoint is reachable from our node.")
	b.line("  3. PersistentKeepalive is set to 25 in your config.")
	b.blank()
	b.line("If the tunnel remains stale, contact noc@akae.re.")
	return subject, b.String()
}

func renderPeerLatencyHigh(m varMap) (string, string) {
	subject := "Latency anomaly detected – " + m.asn()

	sustained := m.str("sustainedCount")
	anomaly := m.str("anomalyCount")
	var triggerText string
	switch m.str("triggerCondition") {
	case "sustained":
		triggerText = "Sustained abnormal latency for " + sustained + " consecutive readings (~" + sustained + " minutes)"
	case "count":
		triggerText = anomaly + " abnormal readings in the last 60 minutes"
	default:
		triggerText = sustained + " consecutive abnormal readings AND " + anomaly + " total anomalies in the last 60 minutes"
	}

	baseline := m.str("baselineRtt")
	baselineDisplay := baseline + " ms"
	if baseline == "0.0" {
		baselineDisplay = "N/A (no history)"
	}

	var b textBuilder
	b.line("Latency anomaly detected")
	b.blank()
	b.line("The WireGuard tunnel for " + m.asn() + " on " + m.str("nodeName") + " is experiencing abnormally high latency.")
	b.blank()
	b.line("Current RTT: " + m.str("currentRtt") + " ms · Baseline: " + baselineDisplay)
	b.blank()
	b.kv("Trigger", triggerText)
	b.kv("Anomaly count", anomaly+" readings")
	b.kv("Consecutive", sustained+" readings")
	b.blank()
	b.line("Anomalous readings (latest 10)")
	points := m.timepoints()
	if len(points) > 10 {
		points = points[len(points)-10:]
	}
	if len(points) == 0 {
		b.line("  No individual timepoints available.")
	} else {
		for _, tp := range points {
			b.kv(tp.Time, tp.RTT+" ms")
		}
	}
	b.blank()
	b.line("We will continue monitoring. If latency returns to normal, you will receive a recovery notification.")
	b.blank()
	b.line("Troubleshooting steps")
	b.line("  1. Check for network congestion or routing changes.")
	b.line("  2. Verify your WireGuard endpoint is reachable.")
	b.line("  3. Check your ISP or upstream for any known issues.")
	b.blank()
	b.line("If latency remains high, contact noc@akae.re.")
	return subject, b.String()
}

func renderPeerLatencyRecovered(m varMap) (string, string) {
	subject := "Latency recovered – " + m.asn()
	baseline := m.str("baselineRtt")
	baselineDisplay := baseline + " ms"
	if baseline == "0.0" {
		baselineDisplay = "N/A (no history)"
	}
	var b textBuilder
	b.line("Latency recovered")
	b.line(m.asn() + " · " + m.str("nodeName"))
	b.blank()
	b.line("The WireGuard tunnel latency for " + m.asn() + " on " + m.str("nodeName") + " has returned to normal levels.")
	b.blank()
	b.kv("Current RTT", m.str("currentRtt")+" ms")
	b.kv("Baseline RTT", baselineDisplay)
	b.blank()
	b.line("No further action is required from you.")
	return subject, b.String()
}

func renderPeerMTUUpdated(m varMap) (string, string) {
	subject := "MTU updated – " + m.asn() + " on " + m.str("nodeName")
	mtuDisplay := func(key string) string {
		if v := m.str(key); v != "" {
			return v
		}
		return "default (not set)"
	}
	var b textBuilder
	b.line("WireGuard MTU updated")
	b.blank()
	b.line("The MTU for your peering session on " + m.str("nodeName") + " has been updated by the network administrator.")
	b.blank()
	b.kv("ASN", m.asn())
	b.kv("Node", m.str("nodeName"))
	b.kv("Previous MTU", mtuDisplay("oldMtu"))
	b.kv("New MTU", mtuDisplay("newMtu"))
	b.blank()
	b.line("Your WireGuard configuration on this node will be automatically reconfigured with the new MTU value. You do not need to take any action.")
	b.line("For questions about this change, contact noc@akae.re.")
	return subject, b.String()
}

func renderPeerEndpointMismatch(m varMap) (string, string) {
	subject := "Endpoint Mismatch Detected – " + m.asn()
	var b textBuilder
	b.line("Endpoint Verification Failed")
	b.blank()
	b.line("The actual WireGuard endpoint for your peering with " + m.str("nodeName") + " (" + m.asn() + ") does not match the endpoint you configured.")
	b.blank()
	b.kv("Your Configured Endpoint", m.str("endpoint"))
	b.kv("Actual Detected Endpoint", m.str("actual"))
	b.kv("Mismatch Duration", m.str("duration")+" minutes")
	b.blank()
	b.line("The WireGuard tunnel and BGP session remain active. Please verify your endpoint configuration. If you are using a dynamic IP or DDNS, ensure it is up to date.")
	b.line("You will receive another notification once the endpoint matches again.")
	return subject, b.String()
}

func renderPeerEndpointRecovered(m varMap) (string, string) {
	subject := "Endpoint Recovered – " + m.asn()
	var b textBuilder
	b.line("Endpoint Verification Restored")
	b.blank()
	b.line("The endpoint for your peering with " + m.str("nodeName") + " (" + m.asn() + ") now matches the configured value.")
	b.blank()
	b.kv("Configured Endpoint", m.str("endpoint"))
	b.kv("Actual Endpoint", m.str("actual"))
	return subject, b.String()
}

func renderTestEmail(m varMap) (string, string) {
	subject := "Email test – " + m.str("timestamp")
	var b textBuilder
	b.line("Email test")
	b.blank()
	b.line("This is a test email sent from the AutoPeer admin panel. If you received this message, the email delivery pipeline is working correctly.")
	b.blank()
	b.line("  " + m.str("message"))
	b.blank()
	b.kv("Recipient", m.str("recipient"))
	b.kv("Sent at", m.str("timestamp"))
	b.blank()
	b.line("This email was generated for testing purposes only. No action is required.")
	return subject, b.String()
}
