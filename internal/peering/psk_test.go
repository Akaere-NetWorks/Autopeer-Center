package peering

import (
	"encoding/base64"
	"testing"
)

func TestGeneratePSK(t *testing.T) {
	a, err := GeneratePSK()
	if err != nil {
		t.Fatalf("GeneratePSK error: %v", err)
	}
	// A WireGuard PSK is 32 bytes; std base64 of 32 bytes is 44 chars ending in '='.
	raw, err := base64.StdEncoding.DecodeString(a)
	if err != nil {
		t.Fatalf("PSK is not valid base64: %v", err)
	}
	if len(raw) != 32 {
		t.Errorf("PSK decoded to %d bytes, want 32", len(raw))
	}
	b, err := GeneratePSK()
	if err != nil {
		t.Fatalf("GeneratePSK error: %v", err)
	}
	if a == b {
		t.Error("two generated PSKs are identical; expected randomness")
	}
}

func TestBuildPendingPeerEnablePSK(t *testing.T) {
	base := Input{
		ASN:      4242420123,
		NodeID:   "node-1",
		Pubkey:   "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMN=",
		Endpoint: "192.0.2.1:51820",
		LLA:      "fe80::1",
	}

	// Without EnablePSK the peer must have no pre-shared key.
	p, err := BuildPendingPeer(base)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.WgPreSharedKey != nil {
		t.Errorf("expected nil PSK when not enabled, got %q", *p.WgPreSharedKey)
	}

	// With EnablePSK the peer must carry a freshly generated key.
	withPSK := base
	withPSK.EnablePSK = true
	p2, err := BuildPendingPeer(withPSK)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p2.WgPreSharedKey == nil || *p2.WgPreSharedKey == "" {
		t.Fatal("expected a generated PSK when enabled")
	}
	if raw, err := base64.StdEncoding.DecodeString(*p2.WgPreSharedKey); err != nil || len(raw) != 32 {
		t.Errorf("generated PSK is not a 32-byte base64 key: %v", err)
	}
}
