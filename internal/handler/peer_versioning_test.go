package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/akaere/autopeer-center/internal/apiversion"
	"github.com/akaere/autopeer-center/internal/middleware"
)

type testPeerResp struct {
	ID                     string     `json:"id"`
	Status                 string     `json:"status"`
	EndpointMismatchSince  *time.Time `json:"endpoint_mismatch_since,omitempty"`
	BGPSuspendedByEndpoint bool       `json:"bgp_suspended_by_endpoint"`
}

type testPeerPSKResp struct {
	ID             string  `json:"id"`
	Status         string  `json:"status"`
	WgPreSharedKey *string `json:"wg_preshared_key,omitempty"`
}

func reqWithVersion(v string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/user/peers", nil)
	if v != "" {
		r = r.WithContext(context.WithValue(r.Context(), middleware.ContextKeyAPIVersion, v))
	}
	return r
}

func TestJSONVersionedLatestKeepsFields(t *testing.T) {
	ts := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	p := testPeerResp{ID: "p1", Status: "active", EndpointMismatchSince: &ts, BGPSuspendedByEndpoint: true}

	rec := httptest.NewRecorder()
	JSONVersioned(rec, reqWithVersion(apiversion.Latest()), http.StatusOK, apiversion.ResourcePeer, "", p)

	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	if _, ok := got["endpoint_mismatch_since"]; !ok {
		t.Error("latest must keep endpoint_mismatch_since")
	}
	if _, ok := got["bgp_suspended_by_endpoint"]; !ok {
		t.Error("latest must keep bgp_suspended_by_endpoint")
	}
}

func TestJSONVersionedNoContextDefaultsLatest(t *testing.T) {
	p := testPeerResp{ID: "p1", Status: "active", BGPSuspendedByEndpoint: true}
	rec := httptest.NewRecorder()
	JSONVersioned(rec, reqWithVersion(""), http.StatusOK, apiversion.ResourcePeer, "", p)

	var got map[string]any
	json.Unmarshal(rec.Body.Bytes(), &got)
	if _, ok := got["bgp_suspended_by_endpoint"]; !ok {
		t.Error("missing context must default to latest (keep field)")
	}
}

func TestJSONVersionedOldStripsBareObject(t *testing.T) {
	ts := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	p := testPeerResp{ID: "p1", Status: "active", EndpointMismatchSince: &ts, BGPSuspendedByEndpoint: true}

	rec := httptest.NewRecorder()
	JSONVersioned(rec, reqWithVersion("2025-01-01"), http.StatusOK, apiversion.ResourcePeer, "", p)

	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	if _, ok := got["endpoint_mismatch_since"]; ok {
		t.Error("old version must strip endpoint_mismatch_since")
	}
	if _, ok := got["bgp_suspended_by_endpoint"]; ok {
		t.Error("old version must strip bgp_suspended_by_endpoint")
	}
	if got["id"] != "p1" || got["status"] != "active" {
		t.Errorf("old version mutated unrelated fields: %+v", got)
	}
}

func TestJSONVersionedPSK(t *testing.T) {
	psk := "ZmFrZS1wcmVzaGFyZWQta2V5LWZvci10ZXN0aW5nLTAwMD0="
	p := testPeerPSKResp{ID: "p1", Status: "active", WgPreSharedKey: &psk}

	// Latest must keep wg_preshared_key.
	rec := httptest.NewRecorder()
	JSONVersioned(rec, reqWithVersion(apiversion.Latest()), http.StatusOK, apiversion.ResourcePeer, "", p)
	var latest map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &latest); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	if _, ok := latest["wg_preshared_key"]; !ok {
		t.Error("latest must keep wg_preshared_key")
	}

	// The version just before wg_preshared_key was introduced must strip it.
	rec = httptest.NewRecorder()
	JSONVersioned(rec, reqWithVersion("2026-06-06"), http.StatusOK, apiversion.ResourcePeer, "", p)
	var old map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &old); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	if _, ok := old["wg_preshared_key"]; ok {
		t.Error("2026-06-06 must strip wg_preshared_key")
	}
	if old["id"] != "p1" || old["status"] != "active" {
		t.Errorf("strip mutated unrelated fields: %+v", old)
	}
}

func TestJSONVersionedOldStripsWrapperList(t *testing.T) {
	p := testPeerResp{ID: "p1", BGPSuspendedByEndpoint: true}
	payload := map[string]any{
		"peers":    []testPeerResp{p, p},
		"total":    2,
		"page":     1,
		"per_page": 20,
	}

	rec := httptest.NewRecorder()
	JSONVersioned(rec, reqWithVersion("2025-01-01"), http.StatusOK, apiversion.ResourcePeer, "peers", payload)

	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	if got["total"] != float64(2) {
		t.Errorf("wrapper total mutated: %v", got["total"])
	}
	for i, el := range got["peers"].([]any) {
		obj := el.(map[string]any)
		if _, ok := obj["bgp_suspended_by_endpoint"]; ok {
			t.Errorf("peers[%d] not stripped", i)
		}
	}
}
