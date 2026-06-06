package apiversion

import (
	"reflect"
	"testing"
)

func TestVersionsOrderedAndLatest(t *testing.T) {
	vs := Versions()
	if len(vs) == 0 {
		t.Fatal("no versions registered")
	}
	for i := 1; i < len(vs); i++ {
		if !(vs[i-1] < vs[i]) {
			t.Errorf("versions not strictly ascending at %d: %q !< %q", i, vs[i-1], vs[i])
		}
	}
	if Latest() != vs[len(vs)-1] {
		t.Errorf("Latest() = %q, want last element %q", Latest(), vs[len(vs)-1])
	}
}

func TestResolve(t *testing.T) {
	cases := []struct {
		in      string
		wantVer string
		wantOK  bool
	}{
		{"", Latest(), true},
		{"   ", Latest(), true},
		{"2025-01-01", "2025-01-01", true},
		{"  2026-06-06  ", "2026-06-06", true},
		{"1999-01-01", "", false},
		{"garbage", "", false},
	}
	for _, c := range cases {
		gotVer, gotOK := Resolve(c.in)
		if gotVer != c.wantVer || gotOK != c.wantOK {
			t.Errorf("Resolve(%q) = (%q, %v), want (%q, %v)", c.in, gotVer, gotOK, c.wantVer, c.wantOK)
		}
	}
}

func peerObj() map[string]any {
	return map[string]any{
		"id":                        "p1",
		"remote_asn":                float64(4242420123),
		"endpoint_mismatch_since":   "2026-06-01T00:00:00Z",
		"bgp_suspended_by_endpoint": true,
	}
}

func TestDowngradeSingleObject(t *testing.T) {
	// Latest target: identity, fields preserved.
	obj := peerObj()
	Downgrade(ResourcePeer, Latest(), obj, "")
	if _, ok := obj["endpoint_mismatch_since"]; !ok {
		t.Error("latest should keep endpoint_mismatch_since")
	}
	if _, ok := obj["bgp_suspended_by_endpoint"]; !ok {
		t.Error("latest should keep bgp_suspended_by_endpoint")
	}

	// Baseline target: both new fields stripped, others intact.
	obj = peerObj()
	Downgrade(ResourcePeer, "2025-01-01", obj, "")
	if _, ok := obj["endpoint_mismatch_since"]; ok {
		t.Error("baseline should strip endpoint_mismatch_since")
	}
	if _, ok := obj["bgp_suspended_by_endpoint"]; ok {
		t.Error("baseline should strip bgp_suspended_by_endpoint")
	}
	if obj["id"] != "p1" || obj["remote_asn"] != float64(4242420123) {
		t.Errorf("baseline mutated unrelated fields: %+v", obj)
	}
}

func TestDowngradeBareList(t *testing.T) {
	list := []any{peerObj(), peerObj()}
	Downgrade(ResourcePeer, "2025-01-01", list, "")
	for i, el := range list {
		obj := el.(map[string]any)
		if _, ok := obj["endpoint_mismatch_since"]; ok {
			t.Errorf("element %d not stripped", i)
		}
		if _, ok := obj["bgp_suspended_by_endpoint"]; ok {
			t.Errorf("element %d not stripped", i)
		}
	}
}

func TestDowngradeWrapperMap(t *testing.T) {
	wrapper := map[string]any{
		"peers": []any{peerObj(), peerObj()},
		"total": float64(2),
		"page":  float64(1),
	}
	Downgrade(ResourcePeer, "2025-01-01", wrapper, "peers")
	if wrapper["total"] != float64(2) || wrapper["page"] != float64(1) {
		t.Errorf("wrapper meta fields mutated: %+v", wrapper)
	}
	for i, el := range wrapper["peers"].([]any) {
		obj := el.(map[string]any)
		if _, ok := obj["bgp_suspended_by_endpoint"]; ok {
			t.Errorf("peers[%d] not stripped", i)
		}
	}
}

func TestDowngradeNestedSingleObjectViaListKey(t *testing.T) {
	// Admin GET wraps the peer under "peer".
	wrapper := map[string]any{
		"peer":    peerObj(),
		"metrics": map[string]any{"rx": float64(1)},
	}
	Downgrade(ResourcePeer, "2025-01-01", wrapper, "peer")
	peer := wrapper["peer"].(map[string]any)
	if _, ok := peer["endpoint_mismatch_since"]; ok {
		t.Error("nested peer not stripped")
	}
	if !reflect.DeepEqual(wrapper["metrics"], map[string]any{"rx": float64(1)}) {
		t.Error("sibling metrics mutated")
	}
}

func TestDowngradeMissingFieldsNoPanic(t *testing.T) {
	obj := map[string]any{"id": "p2"} // no version-specific fields
	Downgrade(ResourcePeer, "2025-01-01", obj, "")
	if obj["id"] != "p2" {
		t.Error("unexpected mutation")
	}
}

func TestChangesNewestFirstAndThreshold(t *testing.T) {
	// Register synthetic changes to assert ordering and the Version>target rule
	// without depending on the production registry size.
	saved := changes
	t.Cleanup(func() { changes = saved })
	changes = nil

	var order []string
	mk := func(v string) VersionChange {
		return VersionChange{Version: v, Resource: ResourcePeer, Apply: func(map[string]any) {
			order = append(order, v)
		}}
	}
	// Register out of order on purpose.
	register(mk("2025-06-01"))
	register(mk("2026-01-01"))
	register(mk("2025-01-01"))

	order = nil
	Downgrade(ResourcePeer, "2025-06-01", map[string]any{}, "")
	// Only changes with Version > "2025-06-01" apply: 2026-01-01. (Equal not applied.)
	if !reflect.DeepEqual(order, []string{"2026-01-01"}) {
		t.Errorf("threshold/order wrong: %v", order)
	}

	order = nil
	Downgrade(ResourcePeer, "2024-01-01", map[string]any{}, "")
	// All three apply, newest first.
	if !reflect.DeepEqual(order, []string{"2026-01-01", "2025-06-01", "2025-01-01"}) {
		t.Errorf("newest-first order wrong: %v", order)
	}
}
