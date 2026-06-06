package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/akaere/autopeer-center/internal/apiversion"
)

// withReqID wraps a handler so GetRequestID works inside APIVersion's 400 path.
func withReqID(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), ContextKeyRequestID, "test-req-id")
		h.ServeHTTP(w, r.WithContext(ctx))
	})
}

func TestAPIVersionNoHeaderDefaultsLatest(t *testing.T) {
	var seen string
	var called bool
	h := withReqID(APIVersion(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		seen = GetAPIVersion(r.Context())
		w.WriteHeader(http.StatusOK)
	})))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/x", nil))

	if !called {
		t.Fatal("next handler not called")
	}
	if seen != apiversion.Latest() {
		t.Errorf("context version = %q, want latest %q", seen, apiversion.Latest())
	}
	if got := rec.Header().Get("Autopeer-Version"); got != apiversion.Latest() {
		t.Errorf("echoed header = %q, want %q", got, apiversion.Latest())
	}
}

func TestAPIVersionValidOldHeader(t *testing.T) {
	var seen string
	h := withReqID(APIVersion(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = GetAPIVersion(r.Context())
	})))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/x", nil)
	req.Header.Set("Autopeer-Version", "2025-01-01")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if seen != "2025-01-01" {
		t.Errorf("context version = %q, want 2025-01-01", seen)
	}
	if got := rec.Header().Get("Autopeer-Version"); got != "2025-01-01" {
		t.Errorf("echoed header = %q, want 2025-01-01", got)
	}
}

func TestAPIVersionUnknownHeaderRejected(t *testing.T) {
	called := false
	h := withReqID(APIVersion(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/x", nil)
	req.Header.Set("Autopeer-Version", "1999-01-01")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if called {
		t.Fatal("next handler should not be called for unknown version")
	}
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON body: %v", err)
	}
	if body["error"] != "invalid_api_version" {
		t.Errorf("error = %v, want invalid_api_version", body["error"])
	}
	if body["request_id"] != "test-req-id" {
		t.Errorf("request_id = %v, want test-req-id", body["request_id"])
	}
}
