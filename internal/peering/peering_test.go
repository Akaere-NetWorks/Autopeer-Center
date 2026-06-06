package peering

import "testing"

func TestWgInterfaceName(t *testing.T) {
	// Must match the agent's wg.InterfaceName: dn42<full ASN>, no padding/underscore.
	cases := map[int64]string{
		4242420001: "dn424242420001",
		64512:      "dn4264512",
		1:          "dn421",
	}
	for asn, want := range cases {
		if got := WgInterfaceName(asn); got != want {
			t.Errorf("WgInterfaceName(%d) = %q, want %q", asn, got, want)
		}
	}
}

func TestWgPort(t *testing.T) {
	// Zero prefix falls back to the default; otherwise prefix*10000 + asn%10000.
	if got := WgPort(4242420123, 0); got != DefaultWgPortPrefix*10000+123 {
		t.Errorf("WgPort default prefix = %d", got)
	}
	if got := WgPort(4242420123, 6); got != 60123 {
		t.Errorf("WgPort(prefix=6) = %d, want 60123", got)
	}
}

func TestBuildPendingPeerCanonicalAndComplete(t *testing.T) {
	in := Input{
		ASN:        4242420123,
		NodeID:     "node-1",
		PortPrefix: 5,
		Pubkey:     "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMN=",
		Endpoint:   "192.0.2.1:51820",
		LLA:        "fe80::1",
	}
	p, err := BuildPendingPeer(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.WgInterfaceName != "dn424242420123" {
		t.Errorf("interface = %q", p.WgInterfaceName)
	}
	if p.WgListenPort != 50123 {
		t.Errorf("port = %d, want 50123", p.WgListenPort)
	}
	if !p.WgManaged || p.Status != "pending" {
		t.Errorf("expected managed pending peer, got managed=%v status=%q", p.WgManaged, p.Status)
	}
	// All WG fields the agent needs must be populated — the bug this guards
	// against was creating peers with empty pubkey/endpoint/LLA.
	if p.RemotePubkey == "" || p.RemoteEndpoint == "" || p.RemoteLLA == "" {
		t.Errorf("incomplete peer: pubkey=%q endpoint=%q lla=%q", p.RemotePubkey, p.RemoteEndpoint, p.RemoteLLA)
	}
}

func TestBuildPendingPeerRejectsBadInput(t *testing.T) {
	base := Input{
		ASN:      4242420123,
		NodeID:   "node-1",
		Pubkey:   "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMN=",
		Endpoint: "192.0.2.1:51820",
		LLA:      "fe80::1",
	}
	bad := map[string]func(*Input){
		"empty node":    func(i *Input) { i.NodeID = "" },
		"short pubkey":  func(i *Input) { i.Pubkey = "tooshort" },
		"bad endpoint":  func(i *Input) { i.Endpoint = "not-an-endpoint" },
		"non-LLA":       func(i *Input) { i.LLA = "2001:db8::1" },
		"bad mtu":       func(i *Input) { v := 100; i.MTU = &v },
	}
	for name, mut := range bad {
		in := base
		mut(&in)
		if _, err := BuildPendingPeer(in); err == nil {
			t.Errorf("%s: expected validation error, got nil", name)
		} else if _, ok := err.(*ValidationError); !ok {
			t.Errorf("%s: expected *ValidationError, got %T", name, err)
		}
	}
}
