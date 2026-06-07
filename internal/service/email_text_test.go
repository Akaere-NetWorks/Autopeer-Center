package service

import (
	"strings"
	"testing"
)

// minimalVars returns a vars map covering the union of keys the renderers read,
// so every template renders without panicking.
func minimalVars() map[string]interface{} {
	return map[string]interface{}{
		"asn":              int64(4242420000),
		"code":             "123456",
		"nodeName":         "node-a",
		"nodeLocation":     "Tokyo",
		"ourPublicIp":      "203.0.113.1",
		"ourWgPubkey":      "PUBKEY=",
		"ourLla":           "fe80::1",
		"listenPort":       51820, // int, as the caller passes it
		"remoteWgPubkey":   "REMOTE=",
		"remoteLla":        "fe80::2",
		"remoteEndpoint":   "198.51.100.1:51820",
		"wgInterfaceName":  "dn42_20000",
		"createdAt":        "2026-06-07T00:00:00Z",
		"reason":           "incomplete config",
		"bgpState":         "Active",
		"location":         "Tokyo",
		"offlineSince":     "2026-06-07T00:00:00Z",
		"oldVersion":       "1.0.0",
		"newVersion":       "1.1.0",
		"lastHandshake":    "5m ago",
		"currentRtt":       "120.0",
		"baselineRtt":      "20.0",
		"triggerCondition": "sustained",
		"anomalyCount":     7,
		"sustainedCount":   5,
		"oldMtu":           "1420",
		"newMtu":           "1280",
		"endpoint":         "198.51.100.1:51820",
		"actual":           "198.51.100.9:51820",
		"duration":         15,
		"message":          "hello",
		"recipient":        "user@example.test",
		"timestamp":        "2026-06-07T00:00:00Z",
	}
}

func TestRenderTextSubjects(t *testing.T) {
	want := map[string]string{
		"verification-code":       "AutoPeer verification code – AS4242420000",
		"peer-submitted":          "Peering request submitted – AS4242420000",
		"peer-approved":           "Peering activated – AS4242420000",
		"peer-rejected":           "Peering request declined – AS4242420000",
		"peer-suspended":          "Peering suspended – AS4242420000",
		"peer-unsuspended":        "Peering restored – AS4242420000",
		"peer-deleted":            "Peering removed – AS4242420000",
		"peer-bgp-down":           "BGP down – AS4242420000",
		"peer-bgp-recovered":      "BGP recovered – AS4242420000",
		"node-offline":            "Node offline – node-a",
		"agent-updated":           "Agent updated – node-a",
		"peer-handshake-stale":    "WireGuard handshake stale – AS4242420000",
		"peer-latency-high":       "Latency anomaly detected – AS4242420000",
		"peer-latency-recovered":  "Latency recovered – AS4242420000",
		"peer-mtu-updated":        "MTU updated – AS4242420000 on node-a",
		"peer-endpoint-mismatch":  "Endpoint Mismatch Detected – AS4242420000",
		"peer-endpoint-recovered": "Endpoint Recovered – AS4242420000",
		"test-email":              "Email test – 2026-06-07T00:00:00Z",
	}

	vars := minimalVars()
	for tmpl, wantSubject := range want {
		subject, body, ok := renderText(tmpl, vars)
		if !ok {
			t.Errorf("%s: renderText returned ok=false", tmpl)
			continue
		}
		if subject != wantSubject {
			t.Errorf("%s: subject = %q, want %q", tmpl, subject, wantSubject)
		}
		if strings.TrimSpace(body) == "" {
			t.Errorf("%s: empty body", tmpl)
		}
		if !strings.Contains(body, "Akaere Networks AutoPeer") {
			t.Errorf("%s: body missing footer", tmpl)
		}
	}

	if len(textRenderers) != len(want) {
		t.Errorf("renderer count = %d, want %d", len(textRenderers), len(want))
	}
}

func TestRenderPeerApprovedConfigBlocks(t *testing.T) {
	_, body, ok := renderText("peer-approved", minimalVars())
	if !ok {
		t.Fatal("renderText peer-approved ok=false")
	}
	for _, marker := range []string{
		"[Interface]",
		"[Peer]",
		"Address = fe80::2/64",
		"Endpoint = 203.0.113.1:51820",
		"protocol bgp dn42_4242420000 from dnpeers {",
		"neighbor fe80::1 % 'dn42_20000' as 4242420000;",
	} {
		if !strings.Contains(body, marker) {
			t.Errorf("peer-approved body missing %q\n---\n%s", marker, body)
		}
	}
}

func TestRenderTextUnknownTemplate(t *testing.T) {
	if _, _, ok := renderText("does-not-exist", minimalVars()); ok {
		t.Error("expected ok=false for unknown template")
	}
}

func TestFallbackText(t *testing.T) {
	subject, body := fallbackText("mystery", map[string]interface{}{"foo": "bar", "n": int64(3)})
	if subject != "AutoPeer notification" {
		t.Errorf("subject = %q", subject)
	}
	if !strings.Contains(body, "foo: bar") || !strings.Contains(body, "n: 3") {
		t.Errorf("fallback body missing kv dump: %s", body)
	}
}

func TestVarMapStr(t *testing.T) {
	m := varMap{"i": 42, "i64": int64(7), "f": float64(1500), "ffrac": 1.5, "s": "x"}
	cases := map[string]string{"i": "42", "i64": "7", "f": "1500", "ffrac": "1.5", "s": "x", "missing": ""}
	for k, want := range cases {
		if got := m.str(k); got != want {
			t.Errorf("str(%q) = %q, want %q", k, got, want)
		}
	}
}
