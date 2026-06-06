package handler

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/akaere/autopeer-center/internal/cache"
	"github.com/akaere/autopeer-center/internal/config"
	"github.com/akaere/autopeer-center/internal/middleware"
	"github.com/akaere/autopeer-center/internal/model"
	"github.com/akaere/autopeer-center/internal/repository"
	"github.com/akaere/autopeer-center/internal/service"
	"github.com/go-chi/chi/v5"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/sirupsen/logrus"
)

var passkeyLog = logrus.WithField("pkg", "handler.passkey")

type PasskeyHandler struct {
	passkeyRepo repository.PasskeyRepository
	authRepo    repository.AuthRepository
	webAuthn    *webauthn.WebAuthn
	cfg         *config.Config
	audit       *service.AuditService
	registry    *service.RegistryService
	cache       *cache.Cache
	// admin must be set via SetAdminHandler before the HTTP server starts;
	// it is never modified after that, so no further synchronisation is needed.
	admin  *AdminHandler
	rateMu sync.Mutex
}

func NewPasskeyHandler(
	passkeyRepo repository.PasskeyRepository,
	authRepo repository.AuthRepository,
	cfg *config.Config,
	audit *service.AuditService,
	registry *service.RegistryService,
	c *cache.Cache,
) (*PasskeyHandler, error) {
	wconfig := &webauthn.Config{
		RPDisplayName: "Auto Peer",
		RPID:          cfg.WebAuthnRPID,
		RPOrigins:     []string{cfg.WebAuthnOrigin},
	}
	wa, err := webauthn.New(wconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create webauthn instance: %w", err)
	}
	return &PasskeyHandler{
		passkeyRepo: passkeyRepo,
		authRepo:    authRepo,
		webAuthn:    wa,
		cfg:         cfg,
		audit:       audit,
		registry:    registry,
		cache:       c,
	}, nil
}

// SetAdminHandler wires in the AdminHandler for site-setting lookups.
// Must be called before the HTTP server starts accepting requests.
func (h *PasskeyHandler) SetAdminHandler(a *AdminHandler) {
	h.admin = a
}

// checkIPRate is an independent per-IP sliding-window counter for passkey
// endpoints. It uses a key prefix unique to passkey so that passkey and
// email/GPG counters are tracked separately.
func (h *PasskeyHandler) checkIPRate(key string, limit int) bool {
	h.rateMu.Lock()
	defer h.rateMu.Unlock()

	now := time.Now()
	cutoff := now.Add(-1 * time.Minute)

	type ipEntry struct {
		Times []time.Time `json:"times"`
	}
	var entry ipEntry
	h.cache.Get(cache.BucketRateLimit, cache.IPKey(cache.RateLimitIP, key), &entry)

	fresh := make([]time.Time, 0, len(entry.Times))
	for _, t := range entry.Times {
		if t.After(cutoff) {
			fresh = append(fresh, t)
		}
	}

	if len(fresh) >= limit {
		entry.Times = fresh
		h.cache.Put(cache.BucketRateLimit, cache.IPKey(cache.RateLimitIP, key), entry, 2*time.Minute)
		return false
	}

	entry.Times = append(fresh, now)
	h.cache.Put(cache.BucketRateLimit, cache.IPKey(cache.RateLimitIP, key), entry, 2*time.Minute)
	return true
}

// verifyTurnstile reuses the package-level verifyTurnstileToken helper.
func (h *PasskeyHandler) verifyTurnstile(r *http.Request, token string) bool {
	if h.cfg.TurnstileSecretKey == "" {
		return true
	}
	if token == "" {
		return false
	}
	clientIP := getClientIP(r)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	ok, err := verifyTurnstileToken(ctx, h.cfg.TurnstileSecretKey, token, clientIP)
	if err != nil {
		passkeyLog.WithError(err).Warn("turnstile verification error")
		return false
	}
	return ok
}

// passkeyUser implements webauthn.User for a DN42 user identified by ASN
type passkeyUser struct {
	asn         int64
	credentials []webauthn.Credential
}

func (u *passkeyUser) WebAuthnID() []byte {
	return []byte(fmt.Sprintf("asn:%d", u.asn))
}

func (u *passkeyUser) WebAuthnName() string {
	return fmt.Sprintf("AS%d", u.asn)
}

func (u *passkeyUser) WebAuthnDisplayName() string {
	return fmt.Sprintf("AS%d", u.asn)
}

func (u *passkeyUser) WebAuthnCredentials() []webauthn.Credential {
	return u.credentials
}

// dbCredsToWebAuthn converts stored DB credentials to webauthn.Credential slice
func dbCredsToWebAuthn(dbCreds []*model.PasskeyCredential) []webauthn.Credential {
	creds := make([]webauthn.Credential, 0, len(dbCreds))
	for _, dc := range dbCreds {
		idBytes, err := base64.RawURLEncoding.DecodeString(dc.CredentialID)
		if err != nil {
			passkeyLog.WithField("credential_id", dc.CredentialID).Warn("skipping credential with invalid base64 ID")
			continue
		}
		aaguidHex := strings.ReplaceAll(dc.AAGUID, "-", "")
		aaguidBytes, _ := hex.DecodeString(aaguidHex)
		cred := webauthn.Credential{
			ID:        idBytes,
			PublicKey: dc.PublicKey,
			Authenticator: webauthn.Authenticator{
				AAGUID:    aaguidBytes,
				SignCount: uint32(dc.SignCount),
			},
		}
		creds = append(creds, cred)
	}
	return creds
}

// GET /api/v1/passkey/status
// Requires: RequireUserWithSessions
func (h *PasskeyHandler) Status(w http.ResponseWriter, r *http.Request) {
	asn := middleware.GetASN(r.Context())
	if asn == 0 {
		ErrorJSON(w, r, http.StatusUnauthorized, "unauthorized", "Not authenticated")
		return
	}
	count, err := h.passkeyRepo.CountCredentialsByASN(r.Context(), asn)
	if err != nil {
		passkeyLog.WithError(err).Error("failed to count passkey credentials")
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to query passkey status")
		return
	}
	JSON(w, http.StatusOK, map[string]interface{}{
		"has_passkey": count > 0,
		"count":       count,
	})
}

// POST /api/v1/passkey/register/begin
// Requires: RequireUserWithSessions
func (h *PasskeyHandler) RegisterBegin(w http.ResponseWriter, r *http.Request) {
	asn := middleware.GetASN(r.Context())
	if asn == 0 {
		ErrorJSON(w, r, http.StatusUnauthorized, "unauthorized", "Not authenticated")
		return
	}

	dbCreds, err := h.passkeyRepo.GetCredentialsByASN(r.Context(), asn)
	if err != nil {
		passkeyLog.WithError(err).Error("failed to load existing credentials")
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to load credentials")
		return
	}

	user := &passkeyUser{
		asn:         asn,
		credentials: dbCredsToWebAuthn(dbCreds),
	}

	options, sessionData, err := h.webAuthn.BeginRegistration(user)
	if err != nil {
		passkeyLog.WithError(err).Error("BeginRegistration failed")
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to begin passkey registration")
		return
	}

	sdBytes, err := json.Marshal(sessionData)
	if err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to serialize session data")
		return
	}
	if err := h.passkeyRepo.SaveWebAuthnSession(r.Context(), asn, "register", string(sdBytes)); err != nil {
		passkeyLog.WithError(err).Error("SaveWebAuthnSession failed")
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to save session")
		return
	}

	NoStore(w)
	JSON(w, http.StatusOK, options)
}

// POST /api/v1/passkey/register/finish
// Requires: RequireUserWithSessions
func (h *PasskeyHandler) RegisterFinish(w http.ResponseWriter, r *http.Request) {
	asn := middleware.GetASN(r.Context())
	if asn == 0 {
		ErrorJSON(w, r, http.StatusUnauthorized, "unauthorized", "Not authenticated")
		return
	}

	sdStr, err := h.passkeyRepo.GetWebAuthnSession(r.Context(), asn, "register")
	if err != nil {
		ErrorJSON(w, r, http.StatusBadRequest, "session_not_found", "Registration session not found or expired. Please try again.")
		return
	}

	var sessionData webauthn.SessionData
	if err := json.Unmarshal([]byte(sdStr), &sessionData); err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to deserialize session data")
		return
	}

	dbCreds, err := h.passkeyRepo.GetCredentialsByASN(r.Context(), asn)
	if err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to load credentials")
		return
	}

	user := &passkeyUser{
		asn:         asn,
		credentials: dbCredsToWebAuthn(dbCreds),
	}

	credential, err := h.webAuthn.FinishRegistration(user, sessionData, r)
	if err != nil {
		passkeyLog.WithError(err).Warn("FinishRegistration failed")
		ErrorJSON(w, r, http.StatusBadRequest, "registration_failed", "Passkey registration failed: "+err.Error())
		return
	}

	_ = h.passkeyRepo.DeleteWebAuthnSession(r.Context(), asn, "register")

	aaguidHex := hex.EncodeToString(credential.Authenticator.AAGUID)

	cred := &model.PasskeyCredential{
		ASN:          asn,
		CredentialID: base64.RawURLEncoding.EncodeToString(credential.ID),
		PublicKey:    credential.PublicKey,
		AAGUID:       aaguidHex,
		SignCount:    int64(credential.Authenticator.SignCount),
		Name:         "Passkey",
	}

	if err := h.passkeyRepo.CreateCredential(r.Context(), cred); err != nil {
		passkeyLog.WithError(err).Error("CreateCredential failed")
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to save passkey credential")
		return
	}

	if h.audit != nil {
		h.audit.Log(r.Context(), "user.passkey.register", fmt.Sprintf("AS%d", asn), nil, map[string]interface{}{
			"asn":           asn,
			"credential_id": cred.ID,
		})
	}

	JSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"id":      cred.ID,
		"name":    cred.Name,
	})
}

// GET /api/v1/user/passkeys
// Requires: RequireUserWithSessions
func (h *PasskeyHandler) List(w http.ResponseWriter, r *http.Request) {
	asn := middleware.GetASN(r.Context())
	if asn == 0 {
		ErrorJSON(w, r, http.StatusUnauthorized, "unauthorized", "Not authenticated")
		return
	}
	creds, err := h.passkeyRepo.GetCredentialsByASN(r.Context(), asn)
	if err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to list passkeys")
		return
	}
	type passkeyInfo struct {
		ID         string  `json:"id"`
		Name       string  `json:"name"`
		AAGUID     string  `json:"aaguid"`
		CreatedAt  string  `json:"created_at"`
		LastUsedAt *string `json:"last_used_at,omitempty"`
	}
	result := make([]passkeyInfo, 0, len(creds))
	for _, c := range creds {
		info := passkeyInfo{
			ID:        c.ID,
			Name:      c.Name,
			AAGUID:    c.AAGUID,
			CreatedAt: c.CreatedAt.Format("2006-01-02T15:04:05Z"),
		}
		if c.LastUsedAt != nil {
			s := c.LastUsedAt.Format("2006-01-02T15:04:05Z")
			info.LastUsedAt = &s
		}
		result = append(result, info)
	}
	JSON(w, http.StatusOK, map[string]interface{}{"passkeys": result})
}

// DELETE /api/v1/user/passkeys/{id}
// Requires: RequireUserWithSessions
func (h *PasskeyHandler) Delete(w http.ResponseWriter, r *http.Request) {
	asn := middleware.GetASN(r.Context())
	if asn == 0 {
		ErrorJSON(w, r, http.StatusUnauthorized, "unauthorized", "Not authenticated")
		return
	}
	id := chi.URLParam(r, "id")
	if id == "" {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "Missing passkey ID")
		return
	}
	if err := h.passkeyRepo.DeleteCredential(r.Context(), id, asn); err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to delete passkey")
		return
	}
	JSON(w, http.StatusOK, map[string]interface{}{"success": true})
}

// POST /api/v1/auth/user/passkey/begin  (public — user provides ASN)
//
// Rate-limiting mirrors RequestCode:
//   - Per-IP sliding window (10 req/min, key "pk:begin:<ip>") guards against botnet abuse.
//   - Per-ASN 60-second cooldown prevents targeted spam.
//   - Turnstile blocks automated bots.
func (h *PasskeyHandler) LoginBegin(w http.ResponseWriter, r *http.Request) {
	if h.admin != nil && h.admin.GetSiteSettingValue("user_login_enabled") != "true" {
		ErrorJSON(w, r, http.StatusForbidden, "login_disabled", "User login is currently disabled")
		return
	}

	var req struct {
		ASN                 int64  `json:"asn"`
		CFTurnstileResponse string `json:"cf_turnstile_response"`
	}
	if err := DecodeJSON(r, &req); err != nil || req.ASN == 0 {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "Invalid request body: asn is required")
		return
	}

	// IP rate limit — separate key from LoginFinish so begin-flooding cannot
	// exhaust the finish quota.
	clientIP := getClientIP(r)
	if !h.checkIPRate("pk:begin:"+clientIP, 10) {
		ErrorJSON(w, r, http.StatusTooManyRequests, "rate_limited", "Too many requests from this IP")
		return
	}

	if !h.verifyTurnstile(r, req.CFTurnstileResponse) {
		ErrorJSON(w, r, http.StatusForbidden, "turnstile_failed", "Bot verification failed. Please try again.")
		return
	}

	// Per-ASN cooldown (60 s) — same pattern as RequestCode
	var lastBegin time.Time
	if ok, _ := h.cache.Get(cache.BucketRateLimit, cache.ASNKey(cache.RateLimitASN, req.ASN), &lastBegin); ok {
		if time.Since(lastBegin) < 60*time.Second {
			ErrorJSON(w, r, http.StatusTooManyRequests, "rate_limited", "Please wait before requesting a new passkey challenge")
			return
		}
	}

	dbCreds, err := h.passkeyRepo.GetCredentialsByASN(r.Context(), req.ASN)
	if err != nil || len(dbCreds) == 0 {
		// Generic error — do not reveal whether this ASN has passkeys registered,
		// to avoid passive enumeration of which ASNs use passkey auth.
		ErrorJSON(w, r, http.StatusBadRequest, "passkey_not_available", "Passkey authentication is not available for this ASN")
		return
	}

	user := &passkeyUser{
		asn:         req.ASN,
		credentials: dbCredsToWebAuthn(dbCreds),
	}

	options, sessionData, err := h.webAuthn.BeginLogin(user)
	if err != nil {
		passkeyLog.WithError(err).Error("BeginLogin failed")
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to begin passkey login")
		return
	}

	sdBytes, err := json.Marshal(sessionData)
	if err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to serialize session data")
		return
	}
	if err := h.passkeyRepo.SaveWebAuthnSession(r.Context(), req.ASN, "login", string(sdBytes)); err != nil {
		passkeyLog.WithError(err).Error("SaveWebAuthnSession failed")
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to save session")
		return
	}

	// Record ASN stamp after successful begin (mirrors RequestCode line 225)
	h.cache.Put(cache.BucketRateLimit, cache.ASNKey(cache.RateLimitASN, req.ASN), time.Now(), 2*time.Minute)

	NoStore(w)
	JSON(w, http.StatusOK, options)
}

// POST /api/v1/auth/user/passkey/finish?asn=<asn>  (public)
//
// Rate-limiting mirrors VerifyCode:
//   - Per-IP sliding window (20 req/min, key "pk:finish:<ip>") — higher than begin
//     because each finish is strictly preceded by one begin.
//   - Per-ASN 3-second cooldown set on failure to slow brute-force replay attempts.
func (h *PasskeyHandler) LoginFinish(w http.ResponseWriter, r *http.Request) {
	if h.admin != nil && h.admin.GetSiteSettingValue("user_login_enabled") != "true" {
		ErrorJSON(w, r, http.StatusForbidden, "login_disabled", "User login is currently disabled")
		return
	}

	// IP rate limit — separate key from LoginBegin
	clientIP := getClientIP(r)
	if !h.checkIPRate("pk:finish:"+clientIP, 20) {
		ErrorJSON(w, r, http.StatusTooManyRequests, "rate_limited", "Too many requests from this IP")
		return
	}

	var asn int64
	if _, err := fmt.Sscanf(r.URL.Query().Get("asn"), "%d", &asn); err != nil || asn == 0 {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "asn query parameter is required")
		return
	}

	// Per-ASN 3-second cooldown on finish — set only on failure (mirrors VerifyCode)
	var lastFinish time.Time
	if ok, _ := h.cache.Get(cache.BucketRateLimit, cache.PeerKey(cache.RateLimitASN, fmt.Sprintf("pk:finish:%d", asn)), &lastFinish); ok {
		if time.Since(lastFinish) < 3*time.Second {
			ErrorJSON(w, r, http.StatusTooManyRequests, "rate_limited", "Too many authentication attempts, please wait")
			return
		}
	}

	sdStr, err := h.passkeyRepo.GetWebAuthnSession(r.Context(), asn, "login")
	if err != nil {
		ErrorJSON(w, r, http.StatusBadRequest, "session_not_found", "Login session not found or expired. Please try again.")
		return
	}

	var sessionData webauthn.SessionData
	if err := json.Unmarshal([]byte(sdStr), &sessionData); err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to deserialize session data")
		return
	}

	dbCreds, err := h.passkeyRepo.GetCredentialsByASN(r.Context(), asn)
	if err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to load credentials")
		return
	}

	user := &passkeyUser{
		asn:         asn,
		credentials: dbCredsToWebAuthn(dbCreds),
	}

	credential, err := h.webAuthn.FinishLogin(user, sessionData, r)
	if err != nil {
		passkeyLog.WithError(err).Warn("FinishLogin failed")
		// Set per-ASN cooldown on failure to slow replay attempts
		h.cache.Put(cache.BucketRateLimit, cache.PeerKey(cache.RateLimitASN, fmt.Sprintf("pk:finish:%d", asn)), time.Now(), 2*time.Minute)
		ErrorJSON(w, r, http.StatusUnauthorized, "login_failed", "Passkey authentication failed")
		return
	}

	_ = h.passkeyRepo.DeleteWebAuthnSession(r.Context(), asn, "login")

	credIDStr := base64.RawURLEncoding.EncodeToString(credential.ID)
	if err := h.passkeyRepo.UpdateSignCount(r.Context(), credIDStr, int64(credential.Authenticator.SignCount)); err != nil {
		passkeyLog.WithError(err).Warn("UpdateSignCount failed — sign-count may be stale")
	}
	if err := h.passkeyRepo.TouchCredential(r.Context(), credIDStr); err != nil {
		passkeyLog.WithError(err).Warn("TouchCredential failed — last_used_at not updated")
	}

	// Look up email from registry (best-effort; passkey login is still valid without it)
	email := ""
	if h.registry != nil {
		if e, lookupErr := h.registry.LookupASNEmail(asn); lookupErr == nil {
			email = e
		}
	}

	// Generate refresh token — same pattern as AuthHandler.createUserSession
	rawRefresh, refreshHash, err := generateRefreshToken()
	if err != nil {
		passkeyLog.WithError(err).Error("generateRefreshToken failed")
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to create session")
		return
	}

	expiresAt := time.Now().Add(h.cfg.UserRefreshTokenTTL)
	authSession := &model.AuthSession{
		SubjectType:      "user",
		ASN:              &asn,
		Email:            email,
		RefreshTokenHash: refreshHash,
		UserAgent:        r.UserAgent(),
		IPAddress:        middleware.RealIP(r, nil),
		DeviceName:       requestDeviceName(r),
		LoginMethod:      "passkey",
		ExpiresAt:        expiresAt,
	}

	if err := h.authRepo.CreateAuthSession(r.Context(), authSession); err != nil {
		passkeyLog.WithError(err).Error("CreateAuthSession failed")
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to create session")
		return
	}

	token, err := middleware.SignUserSessionJWT(h.cfg, asn, email, authSession.ID, "passkey", h.cfg.UserAccessTokenTTL)
	if err != nil {
		passkeyLog.WithError(err).Error("SignUserSessionJWT failed")
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to sign token")
		return
	}

	setRefreshCookie(w, r, rawRefresh, int(h.cfg.UserRefreshTokenTTL.Seconds()))

	if h.audit != nil {
		h.audit.Log(r.Context(), "user.login.passkey", fmt.Sprintf("AS%d", asn), nil, map[string]interface{}{
			"asn":        asn,
			"session_id": authSession.ID,
		})
	}

	passkeyLog.WithField("asn", asn).Info("successful passkey login")

	NoStore(w)
	JSON(w, http.StatusOK, authResponse{
		Token:            token,
		AccessToken:      token,
		ExpiresIn:        int64(h.cfg.UserAccessTokenTTL.Seconds()),
		RefreshExpiresIn: int64(h.cfg.UserRefreshTokenTTL.Seconds()),
		ASN:              asn,
		Session:          authSession,
		User:             &authUserResponse{Role: "user", ASN: &asn, Email: email},
	})
}
