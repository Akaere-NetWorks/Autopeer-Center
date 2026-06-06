package handler

import (
	"net/http/httptest"
	"testing"

	"github.com/akaere/autopeer-center/internal/config"
	"github.com/akaere/autopeer-center/internal/middleware"
	"github.com/akaere/autopeer-center/internal/model"
)

func TestDeviceUserCodeNormalizeAndValidate(t *testing.T) {
	normalized := normalizeDeviceUserCode(" abcd-efgh ")
	if normalized != "ABCDEFGH" {
		t.Fatalf("expected normalized code, got %q", normalized)
	}
	if !validDeviceUserCode(normalized) {
		t.Fatal("expected normalized code to be valid")
	}
	if validDeviceUserCode("ABC1EFGH") {
		t.Fatal("expected ambiguous character 1 to be invalid")
	}
}

func TestDeviceSecretHashDomainSeparated(t *testing.T) {
	secret := "test-secret"
	value := "ABCDEFGH"
	userHash := hashDeviceSecret(secret, "user", value)
	deviceHash := hashDeviceSecret(secret, "device", value)
	if userHash == "" || deviceHash == "" {
		t.Fatal("expected non-empty hashes")
	}
	if userHash == deviceHash {
		t.Fatal("expected user and device hashes to be domain separated")
	}
}

func TestFrontendBaseURLChoosesSingleCanonicalOrigin(t *testing.T) {
	req := httptest.NewRequest("GET", "https://api.example.com/api/v1/auth/device/code", nil)
	cfg := &config.Config{CORSOrigin: "https://example.com,https://www.example.com"}

	got := frontendBaseURL(req, cfg)
	if got != "https://www.example.com" {
		t.Fatalf("expected canonical frontend origin, got %q", got)
	}
}

func TestNormalizeDeviceScopes(t *testing.T) {
	tests := []struct {
		name    string
		scopes  []string
		want    string
		wantErr bool
	}{
		{name: "default user", want: "user"},
		{name: "user", scopes: []string{" user "}, want: "user"},
		{name: "admin", scopes: []string{"ADMIN"}, want: "admin"},
		{name: "multiple", scopes: []string{"user", "admin"}, wantErr: true},
		{name: "unsupported", scopes: []string{"root"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeDeviceScopes(tt.scopes)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != 1 || got[0] != tt.want {
				t.Fatalf("expected %q, got %#v", tt.want, got)
			}
		})
	}
}

func TestDeviceGrantAuthorizeScopeMatchesRole(t *testing.T) {
	adminGrant := &model.AuthDeviceGrant{Scopes: []string{"admin"}}
	userGrant := &model.AuthDeviceGrant{Scopes: []string{"user"}}

	if deviceGrantAuthorizeForbidden(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil), &middleware.Claims{Role: "admin"}, adminGrant) {
		t.Fatal("expected admin to authorize admin grant")
	}
	if deviceGrantAuthorizeForbidden(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil), &middleware.Claims{Role: "user"}, userGrant) {
		t.Fatal("expected user to authorize user grant")
	}
	if !deviceGrantAuthorizeForbidden(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil), &middleware.Claims{Role: "user"}, adminGrant) {
		t.Fatal("expected user to be denied for admin grant")
	}
	if !deviceGrantAuthorizeForbidden(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil), &middleware.Claims{Role: "admin"}, userGrant) {
		t.Fatal("expected admin to be denied for user grant")
	}
}
