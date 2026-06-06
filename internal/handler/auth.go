package handler

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"math/big"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/clearsign"
	"github.com/akaere/autopeer-center/internal/cache"
	"github.com/akaere/autopeer-center/internal/config"
	"github.com/akaere/autopeer-center/internal/middleware"
	"github.com/akaere/autopeer-center/internal/model"
	"github.com/akaere/autopeer-center/internal/repository"
	"github.com/akaere/autopeer-center/internal/service"
	"github.com/go-chi/chi/v5"
	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/bcrypt"
)

var authLog = logrus.WithField("pkg", "handler.auth")

var turnstileHTTPClient = &http.Client{Timeout: 10 * time.Second}

type AuthHandler struct {
	authRepo  repository.AuthRepository
	adminRepo repository.AdminRepository
	nodeRepo  repository.NodeRepository
	cfg       *config.Config
	registry  *service.RegistryService
	email     *service.EmailService
	audit     *service.AuditService
	cache     *cache.Cache
	rateMu    sync.Mutex
	admin     *AdminHandler
}

func NewAuthHandler(authRepo repository.AuthRepository, adminRepo repository.AdminRepository, nodeRepo repository.NodeRepository, cfg *config.Config, registry *service.RegistryService, email *service.EmailService, audit *service.AuditService, c *cache.Cache) *AuthHandler {
	return &AuthHandler{
		authRepo:  authRepo,
		adminRepo: adminRepo,
		nodeRepo:  nodeRepo,
		cfg:       cfg,
		registry:  registry,
		email:     email,
		audit:     audit,
		cache:     c,
	}
}

func (h *AuthHandler) SetAdminHandler(a *AdminHandler) {
	h.admin = a
}

type requestCodeReq struct {
	ASN                 int64  `json:"asn"`
	CFTurnstileResponse string `json:"cf_turnstile_response"`
}

type verifyCodeReq struct {
	ASN  int64  `json:"asn"`
	Code string `json:"code"`
}

type adminLoginReq struct {
	Email               string `json:"email"`
	Password            string `json:"password"`
	CFTurnstileResponse string `json:"cf_turnstile_response"`
}

type authResponse struct {
	Token            string             `json:"token"`
	AccessToken      string             `json:"access_token"`
	ExpiresIn        int64              `json:"expires_in"`
	RefreshExpiresIn int64              `json:"refresh_expires_in,omitempty"`
	ASN              int64              `json:"asn,omitempty"`
	Session          *model.AuthSession `json:"session,omitempty"`
	User             *authUserResponse  `json:"user,omitempty"`
}

type authUserResponse struct {
	Role  string `json:"role"`
	ASN   *int64 `json:"asn,omitempty"`
	Email string `json:"email,omitempty"`
}

type deviceCodeReq struct {
	ClientName string   `json:"client_name"`
	DeviceName string   `json:"device_name"`
	Version    string   `json:"version"`
	Scopes     []string `json:"scopes"`
}

type deviceCodeResp struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete,omitempty"`
	ExpiresIn               int64  `json:"expires_in"`
	Interval                int64  `json:"interval"`
}

type deviceRequestResp struct {
	UserCode   string    `json:"user_code"`
	ClientName string    `json:"client_name"`
	DeviceName string    `json:"device_name,omitempty"`
	Version    string    `json:"version,omitempty"`
	Scopes     []string  `json:"scopes"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

type deviceAuthorizeReq struct {
	UserCode string `json:"user_code"`
	Decision string `json:"decision"`
}

type deviceTokenReq struct {
	GrantType  string `json:"grant_type"`
	DeviceCode string `json:"device_code"`
}

func (h *AuthHandler) RequestCode(w http.ResponseWriter, r *http.Request) {
	if h.admin != nil && h.admin.GetSiteSettingValue("user_login_enabled") != "true" {
		ErrorJSON(w, r, http.StatusForbidden, "login_disabled", "User login is currently disabled")
		return
	}

	var req requestCodeReq
	if err := DecodeJSON(r, &req); err != nil {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "Invalid JSON body")
		return
	}

	if req.ASN <= 0 {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "Invalid ASN")
		return
	}

	if !h.verifyTurnstile(r, req.CFTurnstileResponse) {
		ErrorJSON(w, r, http.StatusForbidden, "turnstile_failed", "Bot verification failed. Please try again.")
		return
	}

	authLog.WithFields(logrus.Fields{"asn": req.ASN, "client_ip": getClientIP(r)}).Debug("request code")

	var lastCheck time.Time
	if ok, _ := h.cache.Get(cache.BucketRateLimit, cache.ASNKey(cache.RateLimitASN, req.ASN), &lastCheck); ok {
		if time.Since(lastCheck) < 60*time.Second {
			authLog.WithFields(logrus.Fields{"asn": req.ASN}).Debug("ASN rate limit hit")
			ErrorJSON(w, r, http.StatusTooManyRequests, "rate_limited", "Please wait 60 seconds before requesting a new code")
			return
		}
	}

	clientIP := getClientIP(r)
	if !h.checkIPRate(clientIP, 5) {
		authLog.WithField("ip", clientIP).Debug("IP rate limit hit")
		ErrorJSON(w, r, http.StatusTooManyRequests, "rate_limited", "Too many requests from this IP")
		return
	}

	email, err := h.registry.LookupASNEmail(req.ASN)
	if err != nil {
		authLog.WithField("asn", req.ASN).Debug("ASN not found in registry")
		ErrorJSON(w, r, http.StatusNotFound, "asn_not_found", "ASN not found in DN42 registry")
		return
	}

	authLog.WithFields(logrus.Fields{"asn": req.ASN, "masked_email": service.MaskEmail(email)}).Debug("registry lookup result")

	code, err := generateCode()
	if err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to generate verification code")
		return
	}

	authLog.WithField("asn", req.ASN).Debug("verification code generated")

	codeHash, err := bcrypt.GenerateFromPassword([]byte(code), bcrypt.DefaultCost)
	if err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to process verification code")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	err = h.authRepo.CreateLoginCode(ctx, &model.UserLoginCode{
		ASN:       req.ASN,
		Email:     email,
		CodeHash:  string(codeHash),
		ExpiresAt: time.Now().Add(10 * time.Minute),
	})
	if err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to store verification code")
		return
	}

	authLog.WithField("asn", req.ASN).Debug("code stored in database")

	if err := h.email.SendVerificationCode(email, req.ASN, code); err != nil {
		authLog.WithError(err).WithField("email", service.MaskEmail(email)).Error("failed to send verification email")
		ErrorJSON(w, r, http.StatusInternalServerError, "email_error", "Failed to send verification email")
		return
	}

	authLog.WithField("asn", req.ASN).Debug("verification email sent")

	h.cache.Put(cache.BucketRateLimit, cache.ASNKey(cache.RateLimitASN, req.ASN), time.Now(), 2*time.Minute)

	JSON(w, http.StatusOK, map[string]interface{}{
		"masked_email": service.MaskEmail(email),
		"expires_in":   600,
	})
}

func (h *AuthHandler) VerifyCode(w http.ResponseWriter, r *http.Request) {
	if h.admin != nil && h.admin.GetSiteSettingValue("user_login_enabled") != "true" {
		ErrorJSON(w, r, http.StatusForbidden, "login_disabled", "User login is currently disabled")
		return
	}

	var req verifyCodeReq
	if err := DecodeJSON(r, &req); err != nil {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "Invalid JSON body")
		return
	}

	if req.ASN <= 0 || req.Code == "" {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "ASN and code are required")
		return
	}

	clientIP := getClientIP(r)
	if !h.checkIPRate(clientIP, 10) {
		ErrorJSON(w, r, http.StatusTooManyRequests, "rate_limited", "Too many attempts from this IP")
		return
	}

	var lastASNVerify time.Time
	if ok, _ := h.cache.Get(cache.BucketRateLimit, cache.PeerKey(cache.RateLimitASN, fmt.Sprintf("verify:%d", req.ASN)), &lastASNVerify); ok {
		if time.Since(lastASNVerify) < 3*time.Second {
			ErrorJSON(w, r, http.StatusTooManyRequests, "rate_limited", "Too many verification attempts, please wait")
			return
		}
	}

	authLog.WithField("asn", req.ASN).Debug("verify code")

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	loginCode, err := h.authRepo.GetLatestValidLoginCode(ctx, req.ASN)
	if err != nil {
		authLog.WithField("asn", req.ASN).Debug("no valid code found for ASN")
		ErrorJSON(w, r, http.StatusUnauthorized, "invalid_code", "Invalid or expired verification code")
		return
	}

	authLog.WithFields(logrus.Fields{"asn": req.ASN, "code_id": loginCode.ID}).Debug("code found")

	if err := bcrypt.CompareHashAndPassword([]byte(loginCode.CodeHash), []byte(req.Code)); err != nil {
		if err := h.authRepo.IncrementLoginCodeFailures(ctx, loginCode.ID); err != nil {
			authLog.WithError(err).WithField("code_id", loginCode.ID).Warn("failed to increment failed_attempts")
		}
		h.cache.Put(cache.BucketRateLimit, cache.PeerKey(cache.RateLimitASN, fmt.Sprintf("verify:%d", req.ASN)), time.Now(), 2*time.Minute)
		authLog.WithFields(logrus.Fields{"asn": req.ASN, "code_id": loginCode.ID, "failed_attempts": loginCode.FailedAttempts + 1}).Debug("failed attempt increment")
		ErrorJSON(w, r, http.StatusUnauthorized, "invalid_code", "Invalid or expired verification code")
		return
	}

	ok, err := h.authRepo.MarkLoginCodeUsed(ctx, loginCode.ID)
	if err != nil || !ok {
		ErrorJSON(w, r, http.StatusUnauthorized, "invalid_code", "code already used")
		return
	}

	resp, err := h.createUserSession(ctx, w, r, req.ASN, loginCode.Email, "email")
	if err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to generate token")
		return
	}

	authLog.WithField("asn", req.ASN).Debug("JWT signed for user")

	h.audit.Log(ctx, "user.login", fmt.Sprintf("AS%d", req.ASN), nil, map[string]interface{}{"asn": req.ASN})

	authLog.WithField("asn", req.ASN).Info("successful login")

	JSON(w, http.StatusOK, resp)
}

func (h *AuthHandler) AdminLogin(w http.ResponseWriter, r *http.Request) {
	var req adminLoginReq
	if err := DecodeJSON(r, &req); err != nil {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "Invalid JSON body")
		return
	}

	if req.Email == "" || req.Password == "" {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "Email and password are required")
		return
	}

	if !h.verifyTurnstile(r, req.CFTurnstileResponse) {
		ErrorJSON(w, r, http.StatusForbidden, "turnstile_failed", "Bot verification failed. Please try again.")
		return
	}

	authLog.WithField("email", service.MaskEmail(req.Email)).Debug("admin login")

	clientIP := getClientIP(r)
	if !h.checkIPRate("admin:"+clientIP, 5) {
		authLog.WithField("ip", clientIP).Debug("admin login rate limit hit")
		ErrorJSON(w, r, http.StatusTooManyRequests, "rate_limited", "Too many login attempts, please try again later")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	admin, err := h.adminRepo.GetByEmail(ctx, req.Email)
	if err != nil {
		authLog.WithField("email", service.MaskEmail(req.Email)).Debug("admin not found in DB")
		ErrorJSON(w, r, http.StatusUnauthorized, "invalid_credentials", "Invalid email or password")
		return
	}

	authLog.WithField("email", service.MaskEmail(req.Email)).Debug("admin DB lookup successful")

	if err := bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(req.Password)); err != nil {
		authLog.WithField("email", service.MaskEmail(req.Email)).Debug("password comparison failed")
		ErrorJSON(w, r, http.StatusUnauthorized, "invalid_credentials", "Invalid email or password")
		return
	}

	resp, err := h.createAdminSession(ctx, w, r, admin.ID, req.Email)
	if err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to generate token")
		return
	}

	authLog.WithField("email", service.MaskEmail(req.Email)).Debug("JWT signed for admin")

	h.audit.Log(ctx, "admin.login", req.Email, nil, nil)

	authLog.WithField("email", service.MaskEmail(req.Email)).Info("successful admin login")

	JSON(w, http.StatusOK, resp)
}

func generateCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

func generateRefreshToken() (string, string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	raw := hex.EncodeToString(b)
	hash := hashRefreshToken(raw)
	return raw, hash, nil
}

const (
	deviceGrantTTL     = 10 * time.Minute
	devicePollInterval = 5 * time.Second
	deviceUserAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
)

func generateDeviceCode(secret string) (string, string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	raw := "ap_dev_" + base64.RawURLEncoding.EncodeToString(b)
	return raw, hashDeviceSecret(secret, "device", raw), nil
}

func generateDeviceUserCode(secret string) (string, string, error) {
	var b strings.Builder
	max := big.NewInt(int64(len(deviceUserAlphabet)))
	for i := 0; i < 8; i++ {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", "", err
		}
		b.WriteByte(deviceUserAlphabet[n.Int64()])
	}
	normalized := b.String()
	display := normalized[:4] + "-" + normalized[4:]
	return display, hashDeviceSecret(secret, "user", normalized), nil
}

func normalizeDeviceUserCode(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	code = strings.ReplaceAll(code, "-", "")
	code = strings.ReplaceAll(code, " ", "")
	return code
}

func validDeviceUserCode(code string) bool {
	if len(code) != 8 {
		return false
	}
	for _, ch := range code {
		if !strings.ContainsRune(deviceUserAlphabet, ch) {
			return false
		}
	}
	return true
}

func normalizeDeviceScopes(scopes []string) ([]string, error) {
	if len(scopes) == 0 {
		return []string{"user"}, nil
	}
	if len(scopes) != 1 {
		return nil, fmt.Errorf("exactly one scope is supported")
	}
	scope := strings.ToLower(strings.TrimSpace(scopes[0]))
	switch scope {
	case "user", "admin":
		return []string{scope}, nil
	default:
		return nil, fmt.Errorf("unsupported scope")
	}
}

func deviceGrantScope(grant *model.AuthDeviceGrant) string {
	for _, raw := range grant.Scopes {
		scope := strings.ToLower(strings.TrimSpace(raw))
		if scope == "admin" {
			return "admin"
		}
		if scope == "user" {
			return "user"
		}
	}
	return "user"
}

func hashDeviceSecret(secret, kind, value string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("autopeer-device-auth:"))
	mac.Write([]byte(kind))
	mac.Write([]byte(":"))
	mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func frontendBaseURL(r *http.Request, cfg *config.Config) string {
	var fallbackOrigin string
	for _, rawOrigin := range strings.Split(cfg.CORSOrigin, ",") {
		origin := strings.TrimRight(strings.TrimSpace(rawOrigin), "/")
		if origin == "" || origin == "*" {
			continue
		}
		if strings.EqualFold(origin, "https://www.example.com") {
			return origin
		}
		if fallbackOrigin == "" {
			fallbackOrigin = origin
		}
	}
	if fallbackOrigin != "" {
		return fallbackOrigin
	}
	proto := "https"
	if r.TLS == nil && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "http") {
		proto = "http"
	}
	host := r.Host
	if forwardedHost := strings.TrimSpace(r.Header.Get("X-Forwarded-Host")); forwardedHost != "" {
		host = forwardedHost
	}
	return proto + "://" + host
}

func deviceAuthorizeClaims(w http.ResponseWriter, r *http.Request, cfg *config.Config) (*middleware.Claims, bool) {
	claims, err := middleware.ExtractClaims(cfg, r)
	if err != nil {
		ErrorJSON(w, r, http.StatusUnauthorized, "unauthorized", "Invalid or missing token")
		return nil, true
	}
	if claims.Role != "user" && claims.Role != "admin" {
		ErrorJSON(w, r, http.StatusForbidden, "forbidden", "A user or admin session is required to authorize TUI devices")
		return nil, true
	}
	if claims.LoginMethod == "impersonation" || claims.ImpersonatedByAdminID != "" || claims.ParentSessionID != "" {
		ErrorJSON(w, r, http.StatusForbidden, "forbidden", "Impersonation sessions cannot authorize TUI devices")
		return nil, true
	}
	return claims, false
}

func deviceGrantAuthorizeForbidden(w http.ResponseWriter, r *http.Request, claims *middleware.Claims, grant *model.AuthDeviceGrant) bool {
	scope := deviceGrantScope(grant)
	if scope == "admin" && claims.Role != "admin" {
		ErrorJSON(w, r, http.StatusForbidden, "forbidden", "Only admin sessions can authorize admin TUI devices")
		return true
	}
	if scope == "user" && claims.Role != "user" {
		ErrorJSON(w, r, http.StatusForbidden, "forbidden", "Only user sessions can authorize user TUI devices")
		return true
	}
	return false
}

func hashRefreshToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func setRefreshCookie(w http.ResponseWriter, r *http.Request, token string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     "refresh_token",
		Value:    token,
		Path:     "/api/v1/auth",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"),
		SameSite: http.SameSiteLaxMode,
	})
}

func clearRefreshCookie(w http.ResponseWriter, r *http.Request) {
	setRefreshCookie(w, r, "", -1)
}

func requestDeviceName(r *http.Request) string {
	ua := strings.TrimSpace(r.UserAgent())
	if ua == "" {
		return "Unknown device"
	}
	if len(ua) > 80 {
		return ua[:80]
	}
	return ua
}

func (h *AuthHandler) createUserSession(ctx context.Context, w http.ResponseWriter, r *http.Request, asn int64, email, loginMethod string) (*authResponse, error) {
	rawRefresh, refreshHash, err := generateRefreshToken()
	if err != nil {
		return nil, err
	}
	expiresAt := time.Now().Add(h.cfg.UserRefreshTokenTTL)
	session := &model.AuthSession{
		SubjectType:      "user",
		ASN:              &asn,
		Email:            email,
		RefreshTokenHash: refreshHash,
		UserAgent:        r.UserAgent(),
		IPAddress:        middleware.RealIP(r, nil),
		DeviceName:       requestDeviceName(r),
		LoginMethod:      loginMethod,
		ExpiresAt:        expiresAt,
	}
	if err := h.authRepo.CreateAuthSession(ctx, session); err != nil {
		return nil, err
	}
	token, err := middleware.SignUserSessionJWT(h.cfg, asn, email, session.ID, loginMethod, h.cfg.UserAccessTokenTTL)
	if err != nil {
		return nil, err
	}
	setRefreshCookie(w, r, rawRefresh, int(h.cfg.UserRefreshTokenTTL.Seconds()))
	return &authResponse{Token: token, AccessToken: token, ExpiresIn: int64(h.cfg.UserAccessTokenTTL.Seconds()), RefreshExpiresIn: int64(h.cfg.UserRefreshTokenTTL.Seconds()), ASN: asn, Session: session, User: &authUserResponse{Role: "user", ASN: &asn, Email: email}}, nil
}

func (h *AuthHandler) createDeviceUserSession(ctx context.Context, w http.ResponseWriter, r *http.Request, grant *model.AuthDeviceGrant) (*authResponse, error) {
	if grant.ApprovedASN == nil || grant.ApprovedEmail == "" {
		return nil, fmt.Errorf("device grant has no approved user")
	}
	rawRefresh, refreshHash, err := generateRefreshToken()
	if err != nil {
		return nil, err
	}
	expiresAt := time.Now().Add(h.cfg.UserRefreshTokenTTL)
	deviceName := strings.TrimSpace(grant.DeviceName)
	if deviceName == "" {
		deviceName = "AutoPeer TUI"
	}
	session := &model.AuthSession{
		SubjectType:      "user",
		ASN:              grant.ApprovedASN,
		Email:            grant.ApprovedEmail,
		RefreshTokenHash: refreshHash,
		UserAgent:        r.UserAgent(),
		IPAddress:        middleware.RealIP(r, nil),
		DeviceName:       deviceName,
		LoginMethod:      "device_cli",
		ExpiresAt:        expiresAt,
	}
	if _, err := h.authRepo.ExchangeAuthDeviceGrant(ctx, grant.DeviceCodeHash, session); err != nil {
		return nil, err
	}
	token, err := middleware.SignUserSessionJWT(h.cfg, *grant.ApprovedASN, grant.ApprovedEmail, session.ID, "device_cli", h.cfg.UserAccessTokenTTL)
	if err != nil {
		return nil, err
	}
	setRefreshCookie(w, r, rawRefresh, int(h.cfg.UserRefreshTokenTTL.Seconds()))
	return &authResponse{Token: token, AccessToken: token, ExpiresIn: int64(h.cfg.UserAccessTokenTTL.Seconds()), RefreshExpiresIn: int64(h.cfg.UserRefreshTokenTTL.Seconds()), ASN: *grant.ApprovedASN, Session: session, User: &authUserResponse{Role: "user", ASN: grant.ApprovedASN, Email: grant.ApprovedEmail}}, nil
}

func (h *AuthHandler) createDeviceAdminSession(ctx context.Context, w http.ResponseWriter, r *http.Request, grant *model.AuthDeviceGrant) (*authResponse, error) {
	if grant.ApprovedBySessionID == nil || grant.ApprovedEmail == "" {
		return nil, fmt.Errorf("device grant has no approved admin")
	}
	approver, err := h.authRepo.GetAuthSession(ctx, *grant.ApprovedBySessionID)
	if err != nil {
		return nil, err
	}
	if approver.SubjectType != "admin" || approver.AdminID == nil || approver.RevokedAt != nil || time.Now().After(approver.ExpiresAt) {
		return nil, fmt.Errorf("approving admin session is inactive")
	}
	rawRefresh, refreshHash, err := generateRefreshToken()
	if err != nil {
		return nil, err
	}
	expiresAt := time.Now().Add(h.cfg.AdminRefreshTokenTTL)
	deviceName := strings.TrimSpace(grant.DeviceName)
	if deviceName == "" {
		deviceName = "AutoPeer TUI"
	}
	session := &model.AuthSession{
		SubjectType:      "admin",
		AdminID:          approver.AdminID,
		Email:            approver.Email,
		RefreshTokenHash: refreshHash,
		UserAgent:        r.UserAgent(),
		IPAddress:        middleware.RealIP(r, nil),
		DeviceName:       deviceName,
		LoginMethod:      "device_cli",
		ExpiresAt:        expiresAt,
	}
	if _, err := h.authRepo.ExchangeAuthDeviceGrant(ctx, grant.DeviceCodeHash, session); err != nil {
		return nil, err
	}
	token, err := middleware.SignAdminSessionJWT(h.cfg, *approver.AdminID, approver.Email, session.ID, h.cfg.AdminAccessTokenTTL)
	if err != nil {
		return nil, err
	}
	setRefreshCookie(w, r, rawRefresh, int(h.cfg.AdminRefreshTokenTTL.Seconds()))
	return &authResponse{Token: token, AccessToken: token, ExpiresIn: int64(h.cfg.AdminAccessTokenTTL.Seconds()), RefreshExpiresIn: int64(h.cfg.AdminRefreshTokenTTL.Seconds()), Session: session, User: &authUserResponse{Role: "admin", Email: approver.Email}}, nil
}

func (h *AuthHandler) createAdminSession(ctx context.Context, w http.ResponseWriter, r *http.Request, adminID, email string) (*authResponse, error) {
	rawRefresh, refreshHash, err := generateRefreshToken()
	if err != nil {
		return nil, err
	}
	expiresAt := time.Now().Add(h.cfg.AdminRefreshTokenTTL)
	session := &model.AuthSession{
		SubjectType:      "admin",
		AdminID:          &adminID,
		Email:            email,
		RefreshTokenHash: refreshHash,
		UserAgent:        r.UserAgent(),
		IPAddress:        middleware.RealIP(r, nil),
		DeviceName:       requestDeviceName(r),
		LoginMethod:      "password",
		ExpiresAt:        expiresAt,
	}
	if err := h.authRepo.CreateAuthSession(ctx, session); err != nil {
		return nil, err
	}
	token, err := middleware.SignAdminSessionJWT(h.cfg, adminID, email, session.ID, h.cfg.AdminAccessTokenTTL)
	if err != nil {
		return nil, err
	}
	setRefreshCookie(w, r, rawRefresh, int(h.cfg.AdminRefreshTokenTTL.Seconds()))
	return &authResponse{Token: token, AccessToken: token, ExpiresIn: int64(h.cfg.AdminAccessTokenTTL.Seconds()), RefreshExpiresIn: int64(h.cfg.AdminRefreshTokenTTL.Seconds()), Session: session, User: &authUserResponse{Role: "admin", Email: email}}, nil
}

func (h *AuthHandler) createImpersonationSession(ctx context.Context, w http.ResponseWriter, r *http.Request, asn int64, email, adminID, parentSessionID string, persistRefresh bool) (*authResponse, error) {
	rawRefresh, refreshHash, err := generateRefreshToken()
	if err != nil {
		return nil, err
	}
	session := &model.AuthSession{
		SubjectType:      "impersonation",
		ASN:              &asn,
		Email:            email,
		AdminID:          &adminID,
		ParentSessionID:  &parentSessionID,
		RefreshTokenHash: refreshHash,
		UserAgent:        r.UserAgent(),
		IPAddress:        middleware.RealIP(r, nil),
		DeviceName:       requestDeviceName(r),
		LoginMethod:      "impersonation",
		ExpiresAt:        time.Now().Add(h.cfg.ImpersonationTokenTTL),
	}
	if err := h.authRepo.CreateAuthSession(ctx, session); err != nil {
		return nil, err
	}
	token, err := middleware.SignImpersonationJWT(h.cfg, asn, email, session.ID, adminID, parentSessionID)
	if err != nil {
		return nil, err
	}
	if persistRefresh {
		setRefreshCookie(w, r, rawRefresh, int(h.cfg.ImpersonationTokenTTL.Seconds()))
	}
	return &authResponse{Token: token, AccessToken: token, ExpiresIn: int64(h.cfg.ImpersonationTokenTTL.Seconds()), ASN: asn, Session: session, User: &authUserResponse{Role: "user", ASN: &asn, Email: email}}, nil
}

func getClientIP(r *http.Request) string {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	return host
}

func (h *AuthHandler) checkIPRate(ip string, limit int) bool {
	h.rateMu.Lock()
	defer h.rateMu.Unlock()

	now := time.Now()
	cutoff := now.Add(-1 * time.Minute)

	type ipEntry struct {
		Times []time.Time `json:"times"`
	}
	var entry ipEntry
	h.cache.Get(cache.BucketRateLimit, cache.IPKey(cache.RateLimitIP, ip), &entry)

	fresh := make([]time.Time, 0, len(entry.Times))
	for _, t := range entry.Times {
		if t.After(cutoff) {
			fresh = append(fresh, t)
		}
	}

	authLog.WithFields(logrus.Fields{"ip": ip, "current_count": len(fresh), "limit": limit}).Debug("checkIPRate")

	if len(fresh) >= limit {
		entry.Times = fresh
		h.cache.Put(cache.BucketRateLimit, cache.IPKey(cache.RateLimitIP, ip), entry, 2*time.Minute)
		return false
	}

	entry.Times = append(fresh, now)
	h.cache.Put(cache.BucketRateLimit, cache.IPKey(cache.RateLimitIP, ip), entry, 2*time.Minute)
	return true
}

type requestGPGChallengeReq struct {
	ASN                 int64  `json:"asn"`
	CFTurnstileResponse string `json:"cf_turnstile_response"`
}

type verifyGPGReq struct {
	ASN           int64  `json:"asn"`
	ChallengeID   string `json:"challenge_id"`
	SignedMessage string `json:"signed_message"`
	PublicKey     string `json:"public_key,omitempty"`
}

type checkGPGAvailabilityReq struct {
	ASN int64 `json:"asn"`
}

func (h *AuthHandler) CheckGPGAvailability(w http.ResponseWriter, r *http.Request) {
	var req checkGPGAvailabilityReq
	if err := DecodeJSON(r, &req); err != nil {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "Invalid JSON body")
		return
	}
	if req.ASN <= 0 {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "Invalid ASN")
		return
	}

	fingerprints, err := h.registry.LookupMntnerGPGFingerprints(req.ASN)
	if err != nil || len(fingerprints) == 0 {
		JSON(w, http.StatusOK, map[string]interface{}{"available": false})
		return
	}
	JSON(w, http.StatusOK, map[string]interface{}{"available": true})
}

func (h *AuthHandler) DeviceCode(w http.ResponseWriter, r *http.Request) {
	NoStore(w)
	if !h.checkIPRate(getClientIP(r), 20) {
		ErrorJSON(w, r, http.StatusTooManyRequests, "rate_limited", "Too many device authorization requests")
		return
	}
	var req deviceCodeReq
	if err := DecodeJSON(r, &req); err != nil {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "Invalid JSON body")
		return
	}
	clientName := firstNonEmptyString(req.ClientName, "autopeer-tui")
	if len(clientName) > 80 {
		clientName = clientName[:80]
	}
	deviceName := firstNonEmptyString(req.DeviceName, requestDeviceName(r))
	if len(deviceName) > 120 {
		deviceName = deviceName[:120]
	}
	version := strings.TrimSpace(req.Version)
	if len(version) > 40 {
		version = version[:40]
	}
	scopes, err := normalizeDeviceScopes(req.Scopes)
	if err != nil {
		ErrorJSON(w, r, http.StatusBadRequest, "unsupported_scope", "Only one user or admin scope is supported")
		return
	}
	deviceCode, deviceHash, err := generateDeviceCode(h.cfg.JWTSecret)
	if err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to generate device code")
		return
	}
	userCode, userHash, err := generateDeviceUserCode(h.cfg.JWTSecret)
	if err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to generate user code")
		return
	}
	grant := &model.AuthDeviceGrant{
		DeviceCodeHash:   deviceHash,
		UserCodeHash:     userHash,
		ClientName:       clientName,
		DeviceName:       deviceName,
		Version:          version,
		Scopes:           scopes,
		Status:           "pending",
		RequestIP:        middleware.RealIP(r, nil),
		RequestUserAgent: r.UserAgent(),
		ExpiresAt:        time.Now().Add(deviceGrantTTL),
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := h.authRepo.CreateAuthDeviceGrant(ctx, grant); err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to store device authorization request")
		return
	}
	verificationURI := frontendBaseURL(r, h.cfg) + "/cli/activate"
	JSON(w, http.StatusOK, deviceCodeResp{
		DeviceCode:              deviceCode,
		UserCode:                userCode,
		VerificationURI:         verificationURI,
		VerificationURIComplete: verificationURI + "?user_code=" + url.QueryEscape(userCode),
		ExpiresIn:               int64(deviceGrantTTL.Seconds()),
		Interval:                int64(devicePollInterval.Seconds()),
	})
}

func (h *AuthHandler) DeviceRequest(w http.ResponseWriter, r *http.Request) {
	NoStore(w)
	claims, forbidden := deviceAuthorizeClaims(w, r, h.cfg)
	if forbidden {
		return
	}
	normalized := normalizeDeviceUserCode(r.URL.Query().Get("user_code"))
	if !validDeviceUserCode(normalized) {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "Invalid device code")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	grant, err := h.authRepo.GetAuthDeviceGrantByUserCodeHash(ctx, hashDeviceSecret(h.cfg.JWTSecret, "user", normalized))
	if err != nil || time.Now().After(grant.ExpiresAt) {
		ErrorJSON(w, r, http.StatusNotFound, "device_request_not_found", "Device authorization request not found or expired")
		return
	}
	if deviceGrantAuthorizeForbidden(w, r, claims, grant) {
		return
	}
	JSON(w, http.StatusOK, deviceRequestResp{
		UserCode:   normalized[:4] + "-" + normalized[4:],
		ClientName: grant.ClientName,
		DeviceName: grant.DeviceName,
		Version:    grant.Version,
		Scopes:     grant.Scopes,
		Status:     grant.Status,
		CreatedAt:  grant.CreatedAt,
		ExpiresAt:  grant.ExpiresAt,
	})
}

func (h *AuthHandler) DeviceAuthorize(w http.ResponseWriter, r *http.Request) {
	NoStore(w)
	claims, forbidden := deviceAuthorizeClaims(w, r, h.cfg)
	if forbidden {
		return
	}
	var req deviceAuthorizeReq
	if err := DecodeJSON(r, &req); err != nil {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "Invalid JSON body")
		return
	}
	normalized := normalizeDeviceUserCode(req.UserCode)
	if !validDeviceUserCode(normalized) {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "Invalid device code")
		return
	}
	decision := strings.ToLower(strings.TrimSpace(req.Decision))
	switch decision {
	case "approve":
		decision = "approved"
	case "deny", "denied":
		decision = "denied"
	default:
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "Decision must be approve or deny")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	userCodeHash := hashDeviceSecret(h.cfg.JWTSecret, "user", normalized)
	existingGrant, err := h.authRepo.GetAuthDeviceGrantByUserCodeHash(ctx, userCodeHash)
	if err != nil || time.Now().After(existingGrant.ExpiresAt) {
		ErrorJSON(w, r, http.StatusNotFound, "device_request_not_found", "Device authorization request not found or expired")
		return
	}
	if deviceGrantAuthorizeForbidden(w, r, claims, existingGrant) {
		return
	}
	var approvedASN *int64
	if deviceGrantScope(existingGrant) == "user" {
		asn := middleware.GetASN(r.Context())
		approvedASN = &asn
	}
	grant, err := h.authRepo.AuthorizeAuthDeviceGrant(ctx, userCodeHash, decision, approvedASN, middleware.GetEmail(r.Context()), middleware.GetSessionID(r.Context()), middleware.RealIP(r, nil), r.UserAgent())
	if err != nil {
		ErrorJSON(w, r, http.StatusConflict, "device_request_inactive", "Device authorization request is not pending or has expired")
		return
	}
	operator := middleware.GetEmail(r.Context())
	if claims.Role == "user" {
		operator = fmt.Sprintf("AS%d", middleware.GetASN(r.Context()))
	}
	h.audit.Log(ctx, "auth.device_authorize", operator, nil, map[string]interface{}{
		"grant_id": grant.ID,
		"decision": decision,
		"client":   grant.ClientName,
		"device":   grant.DeviceName,
		"scope":    deviceGrantScope(grant),
	})
	JSON(w, http.StatusOK, map[string]interface{}{"ok": true, "status": grant.Status})
}

func (h *AuthHandler) DeviceToken(w http.ResponseWriter, r *http.Request) {
	NoStore(w)
	if !h.checkIPRate(getClientIP(r), 60) {
		w.Header().Set("Retry-After", fmt.Sprintf("%d", int(devicePollInterval.Seconds()*2)))
		ErrorJSON(w, r, http.StatusTooManyRequests, "slow_down", "Slow down device authorization polling")
		return
	}
	var req deviceTokenReq
	if err := DecodeJSON(r, &req); err != nil {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "Invalid JSON body")
		return
	}
	if req.GrantType != "urn:ietf:params:oauth:grant-type:device_code" || strings.TrimSpace(req.DeviceCode) == "" {
		ErrorJSON(w, r, http.StatusBadRequest, "unsupported_grant_type", "Invalid device authorization grant")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	grant, err := h.authRepo.PollAuthDeviceGrant(ctx, hashDeviceSecret(h.cfg.JWTSecret, "device", strings.TrimSpace(req.DeviceCode)), devicePollInterval)
	if err != nil {
		ErrorJSON(w, r, http.StatusBadRequest, "expired_token", "Invalid or expired device code")
		return
	}
	if time.Now().After(grant.ExpiresAt) {
		ErrorJSON(w, r, http.StatusBadRequest, "expired_token", "Device code expired")
		return
	}
	switch grant.Status {
	case "pending":
		ErrorJSON(w, r, http.StatusBadRequest, "authorization_pending", "Authorization is still pending")
		return
	case "denied":
		ErrorJSON(w, r, http.StatusBadRequest, "access_denied", "Device authorization was denied")
		return
	case "exchanged":
		ErrorJSON(w, r, http.StatusBadRequest, "expired_token", "Device code was already exchanged")
		return
	case "approved":
		var resp *authResponse
		if deviceGrantScope(grant) == "admin" {
			resp, err = h.createDeviceAdminSession(ctx, w, r, grant)
		} else {
			resp, err = h.createDeviceUserSession(ctx, w, r, grant)
		}
		if err != nil {
			ErrorJSON(w, r, http.StatusUnauthorized, "expired_token", "Device code could not be exchanged")
			return
		}
		operator := resp.User.Email
		if resp.User.Role == "user" {
			operator = fmt.Sprintf("AS%d", resp.ASN)
		}
		h.audit.Log(ctx, "auth.device_exchange", operator, nil, map[string]interface{}{
			"grant_id": grant.ID,
			"session":  resp.Session.ID,
			"client":   grant.ClientName,
			"device":   grant.DeviceName,
			"scope":    deviceGrantScope(grant),
		})
		JSON(w, http.StatusOK, resp)
		return
	default:
		ErrorJSON(w, r, http.StatusBadRequest, "expired_token", "Device authorization request is inactive")
	}
}

func (h *AuthHandler) RequestGPGChallenge(w http.ResponseWriter, r *http.Request) {
	if h.admin != nil && h.admin.GetSiteSettingValue("user_login_enabled") != "true" {
		ErrorJSON(w, r, http.StatusForbidden, "login_disabled", "User login is currently disabled")
		return
	}

	var req requestGPGChallengeReq
	if err := DecodeJSON(r, &req); err != nil {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "Invalid JSON body")
		return
	}

	if req.ASN <= 0 {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "Invalid ASN")
		return
	}

	if !h.verifyTurnstile(r, req.CFTurnstileResponse) {
		ErrorJSON(w, r, http.StatusForbidden, "turnstile_failed", "Bot verification failed. Please try again.")
		return
	}

	var lastCheck time.Time
	if ok, _ := h.cache.Get(cache.BucketRateLimit, cache.ASNKey(cache.RateLimitASN, req.ASN), &lastCheck); ok {
		if time.Since(lastCheck) < 60*time.Second {
			ErrorJSON(w, r, http.StatusTooManyRequests, "rate_limited", "Please wait 60 seconds before requesting a new challenge")
			return
		}
	}

	clientIP := getClientIP(r)
	if !h.checkIPRate(clientIP, 5) {
		ErrorJSON(w, r, http.StatusTooManyRequests, "rate_limited", "Too many requests from this IP")
		return
	}

	authLog.WithFields(logrus.Fields{"asn": req.ASN, "client_ip": clientIP}).Debug("request GPG challenge")

	fingerprints, err := h.registry.LookupMntnerGPGFingerprints(req.ASN)
	if err != nil {
		authLog.WithError(err).WithField("asn", req.ASN).Debug("failed to lookup GPG fingerprints")
		ErrorJSON(w, r, http.StatusNotFound, "asn_not_found", "ASN not found in DN42 registry")
		return
	}

	if len(fingerprints) == 0 {
		ErrorJSON(w, r, http.StatusNotFound, "gpg_not_supported", "No GPG key found in your mntner object")
		return
	}

	challengeBytes := make([]byte, 32)
	if _, err := rand.Read(challengeBytes); err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to generate challenge")
		return
	}
	challengeText := hex.EncodeToString(challengeBytes)

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	ch := &model.GPGChallenge{
		ASN:          req.ASN,
		Fingerprints: fingerprints,
		Challenge:    challengeText,
		ExpiresAt:    time.Now().Add(10 * time.Minute),
	}
	if err := h.authRepo.CreateGPGChallenge(ctx, ch); err != nil {
		authLog.WithError(err).WithField("asn", req.ASN).Error("failed to store GPG challenge")
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to store challenge")
		return
	}

	h.cache.Put(cache.BucketRateLimit, cache.ASNKey(cache.RateLimitASN, req.ASN), time.Now(), 2*time.Minute)

	JSON(w, http.StatusOK, map[string]interface{}{
		"challenge_id":   ch.ID,
		"challenge_text": challengeText,
		"expires_in":     600,
	})
}

func (h *AuthHandler) VerifyGPGSignature(w http.ResponseWriter, r *http.Request) {
	if h.admin != nil && h.admin.GetSiteSettingValue("user_login_enabled") != "true" {
		ErrorJSON(w, r, http.StatusForbidden, "login_disabled", "User login is currently disabled")
		return
	}

	var req verifyGPGReq
	if err := DecodeJSON(r, &req); err != nil {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "Invalid JSON body")
		return
	}

	if req.ASN <= 0 || req.ChallengeID == "" || req.SignedMessage == "" {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "ASN, challenge_id, and signed_message are required")
		return
	}

	clientIP := getClientIP(r)
	if !h.checkIPRate(clientIP, 10) {
		ErrorJSON(w, r, http.StatusTooManyRequests, "rate_limited", "Too many attempts from this IP")
		return
	}

	authLog.WithFields(logrus.Fields{"asn": req.ASN, "challenge_id": req.ChallengeID}).Debug("verify GPG signature")

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	gpgChallenge, err := h.authRepo.GetGPGChallenge(ctx, req.ChallengeID, req.ASN)
	if err != nil {
		authLog.WithError(err).WithFields(logrus.Fields{
			"asn": req.ASN, "challenge_id": req.ChallengeID,
		}).Debug("no valid GPG challenge found")
		ErrorJSON(w, r, http.StatusUnauthorized, "invalid_challenge", "Invalid or expired challenge")
		return
	}

	block, rest := clearsign.Decode([]byte(req.SignedMessage))
	if block == nil {
		if bytes.Contains(rest, []byte("BEGIN PGP SIGNATURE")) {
			block, _ = tryDecodeDetachedSignature(req.SignedMessage, gpgChallenge.Challenge)
		}
		if block == nil {
			ErrorJSON(w, r, http.StatusBadRequest, "invalid_signature", "Could not parse GPG signed message. Use: echo \"<challenge>\" | gpg --clearsign")
			return
		}
	}

	plaintext := strings.TrimSpace(string(block.Bytes))
	expected := strings.TrimSpace(gpgChallenge.Challenge)
	if plaintext != expected {
		authLog.WithFields(logrus.Fields{
			"got":      plaintext,
			"expected": expected,
		}).Debug("challenge text mismatch")
		ErrorJSON(w, r, http.StatusUnauthorized, "invalid_signature", "Signed text does not match the challenge")
		return
	}

	signerFingerprint, keySource, err := h.verifySignatureWithKeyring(block, gpgChallenge.Fingerprints, req.PublicKey)
	if err != nil {
		authLog.WithError(err).Debug("GPG signature verification failed")
		ErrorJSON(w, r, http.StatusUnauthorized, "invalid_signature", err.Error())
		return
	}

	ok, err := h.authRepo.MarkGPGChallengeUsed(ctx, req.ChallengeID)
	if err != nil || !ok {
		ErrorJSON(w, r, http.StatusUnauthorized, "invalid_challenge", "Challenge already used")
		return
	}

	email, err := h.registry.LookupASNEmail(req.ASN)
	if err != nil {
		email = ""
	}

	resp, err := h.createUserSession(ctx, w, r, req.ASN, email, "gpg")
	if err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to generate token")
		return
	}

	h.audit.Log(ctx, "user.login_gpg", fmt.Sprintf("AS%d", req.ASN), nil, map[string]interface{}{
		"asn":         req.ASN,
		"fingerprint": signerFingerprint,
		"key_source":  keySource,
	})

	authLog.WithFields(logrus.Fields{
		"asn":         req.ASN,
		"fingerprint": signerFingerprint,
	}).Info("successful GPG login")

	JSON(w, http.StatusOK, resp)
}

func (h *AuthHandler) verifySignatureWithKeyring(block *clearsign.Block, expectedFingerprints []string, userPublicKey string) (string, string, error) {
	if strings.TrimSpace(userPublicKey) != "" {
		entities, err := openpgp.ReadArmoredKeyRing(strings.NewReader(userPublicKey))
		if err != nil {
			entities, err = openpgp.ReadKeyRing(strings.NewReader(userPublicKey))
			if err != nil {
				return "", "", fmt.Errorf("failed to parse the GPG public key you provided: %w", err)
			}
		}
		if len(entities) == 0 {
			return "", "", fmt.Errorf("the GPG public key you provided contains no valid keys")
		}
		signer, err := block.VerifySignature(openpgp.EntityList(entities), nil)
		if err != nil {
			return "", "", fmt.Errorf("cryptographic signature verification failed with your provided key: %w", err)
		}
		signerFingerprint := fmt.Sprintf("%x", signer.PrimaryKey.Fingerprint)
		matched := false
		for _, fp := range expectedFingerprints {
			normalizedDB := strings.ToLower(strings.ReplaceAll(fp, " ", ""))
			if normalizedDB == strings.ToLower(signerFingerprint) {
				matched = true
				break
			}
		}
		if !matched {
			return "", "", fmt.Errorf("the GPG key you provided (fingerprint: %s) does not match any fingerprint in your mntner object", signerFingerprint)
		}
		return signerFingerprint, "user_provided", nil
	}

	var keyring openpgp.EntityList
	var keyFetchErrors []string

	for _, fp := range expectedFingerprints {
		pubKeyData, err := h.registry.FetchGPGPublicKey(fp)
		if err != nil {
			authLog.WithError(err).WithField("fingerprint", fp).Debug("failed to fetch public key")
			keyFetchErrors = append(keyFetchErrors, fp)
			continue
		}
		entities, err := openpgp.ReadArmoredKeyRing(bytes.NewReader(pubKeyData))
		if err != nil {
			authLog.WithError(err).WithField("fingerprint", fp).Debug("failed to parse armored key, trying unarmored")
			entities, err = openpgp.ReadKeyRing(bytes.NewReader(pubKeyData))
			if err != nil {
				authLog.WithError(err).WithField("fingerprint", fp).Debug("failed to parse key block either way")
				keyFetchErrors = append(keyFetchErrors, fp)
				continue
			}
		}
		keyring = append(keyring, entities...)
	}

	if len(keyring) == 0 {
		return "", "", fmt.Errorf("your GPG public key could not be found on any public keyserver. You can also paste your public key directly on the verification page. Fingerprints checked: %s", strings.Join(expectedFingerprints, ", "))
	}

	signer, err := block.VerifySignature(keyring, nil)
	if err != nil {
		return "", "", fmt.Errorf("cryptographic signature verification failed: %w", err)
	}

	signerFingerprint := fmt.Sprintf("%x", signer.PrimaryKey.Fingerprint)

	matched := false
	for _, fp := range expectedFingerprints {
		normalizedDB := strings.ToLower(strings.ReplaceAll(fp, " ", ""))
		if normalizedDB == strings.ToLower(signerFingerprint) {
			matched = true
			break
		}
	}

	if !matched {
		return "", "", fmt.Errorf("GPG key fingerprint does not match your mntner object")
	}

	return signerFingerprint, "keyserver", nil
}

func tryDecodeDetachedSignature(signedMsg string, challenge string) (*clearsign.Block, error) {
	parts := strings.SplitN(signedMsg, "-----BEGIN PGP SIGNATURE-----", 2)
	if len(parts) < 2 {
		return nil, fmt.Errorf("no signature block found")
	}
	wrapped := "-----BEGIN PGP SIGNED MESSAGE-----\nHash: SHA256\n\n" + challenge + "\n-----BEGIN PGP SIGNATURE-----" + parts[1]
	block, _ := clearsign.Decode([]byte(wrapped))
	return block, nil
}

func (h *AuthHandler) verifyTurnstile(r *http.Request, token string) bool {
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
		authLog.WithError(err).Warn("turnstile verification error")
		return false
	}
	if !ok {
		authLog.WithField("ip", clientIP).Debug("turnstile verification failed")
	}
	return ok
}

func verifyTurnstileToken(ctx context.Context, secretKey, token, remoteIP string) (bool, error) {
	form := url.Values{}
	form.Set("secret", secretKey)
	form.Set("response", token)
	if remoteIP != "" {
		form.Set("remoteip", remoteIP)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://challenges.cloudflare.com/turnstile/v0/siteverify",
		strings.NewReader(form.Encode()))
	if err != nil {
		return false, fmt.Errorf("create turnstile request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := turnstileHTTPClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("turnstile http request: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Success    bool     `json:"success"`
		ErrorCodes []string `json:"error-codes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false, fmt.Errorf("decode turnstile response: %w", err)
	}

	if !result.Success {
		authLog.WithField("error_codes", result.ErrorCodes).Debug("turnstile siteverify failure")
	}
	return result.Success, nil
}

func (h *AuthHandler) GetTurnstileConfig(w http.ResponseWriter, r *http.Request) {
	enabled := h.cfg.TurnstileSiteKey != "" && h.cfg.TurnstileSecretKey != ""
	JSON(w, http.StatusOK, map[string]interface{}{
		"enabled":  enabled,
		"site_key": h.cfg.TurnstileSiteKey,
	})
}

func (h *AuthHandler) LoginStatus(w http.ResponseWriter, r *http.Request) {
	enabled := true
	if h.admin != nil {
		enabled = h.admin.GetSiteSettingValue("user_login_enabled") == "true"
	}
	JSON(w, http.StatusOK, map[string]interface{}{"enabled": enabled})
}

func (h *AuthHandler) AdminLoginAs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	adminEmail := middleware.GetEmail(ctx)
	adminID := middleware.GetAdminID(ctx)
	parentSessionID := middleware.GetSessionID(ctx)
	if adminID == "" || parentSessionID == "" {
		ErrorJSON(w, r, http.StatusUnauthorized, "session_required", "Session-backed admin login is required")
		return
	}

	var body struct {
		ASN     int64 `json:"asn"`
		Persist bool  `json:"persist"`
	}
	if err := DecodeJSON(r, &body); err != nil {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "Invalid JSON body")
		return
	}
	if body.ASN <= 0 {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "Invalid ASN")
		return
	}

	email, err := h.registry.LookupASNEmail(body.ASN)
	if err != nil {
		ErrorJSON(w, r, http.StatusNotFound, "asn_not_found", "ASN not found in DN42 registry")
		return
	}

	resp, err := h.createImpersonationSession(ctx, w, r, body.ASN, email, adminID, parentSessionID, body.Persist)
	if err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to generate token")
		return
	}

	h.audit.Log(ctx, "admin.login_as", adminEmail, nil, map[string]interface{}{
		"asn": body.ASN, "email": service.MaskEmail(email),
	})

	authLog.WithFields(logrus.Fields{"admin": adminEmail, "asn": body.ASN}).Info("admin login-as")

	JSON(w, http.StatusOK, resp)
}

func (h *AuthHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("refresh_token")
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		ErrorJSON(w, r, http.StatusUnauthorized, "unauthorized", "Missing refresh token")
		return
	}
	oldHash := hashRefreshToken(cookie.Value)
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	session, err := h.authRepo.GetAuthSessionByRefreshHash(ctx, oldHash)
	if err != nil {
		if reused, reuseErr := h.authRepo.GetAuthSessionByPreviousRefreshHash(ctx, oldHash); reuseErr == nil {
			_, _ = h.authRepo.RevokeAuthSession(ctx, reused.ID, "refresh_reuse_detected")
			_ = h.authRepo.RevokeChildAuthSessions(ctx, reused.ID, "refresh_reuse_detected")
			h.audit.Log(ctx, "auth.refresh_reuse", reused.ID, nil, map[string]interface{}{
				"session_id": reused.ID,
				"ip":         middleware.RealIP(r, nil),
				"user_agent": r.UserAgent(),
			})
		}
		clearRefreshCookie(w, r)
		ErrorJSON(w, r, http.StatusUnauthorized, "unauthorized", "Invalid refresh token")
		return
	}
	rawRefresh, newHash, err := generateRefreshToken()
	if err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to rotate token")
		return
	}

	var token string
	var expiresIn int64
	var refreshTTL time.Duration
	switch session.SubjectType {
	case "admin":
		if session.AdminID == nil {
			ErrorJSON(w, r, http.StatusUnauthorized, "unauthorized", "Invalid admin session")
			return
		}
		token, err = middleware.SignAdminSessionJWT(h.cfg, *session.AdminID, session.Email, session.ID, h.cfg.AdminAccessTokenTTL)
		expiresIn = int64(h.cfg.AdminAccessTokenTTL.Seconds())
		refreshTTL = h.cfg.AdminRefreshTokenTTL
	case "user":
		if session.ASN == nil {
			ErrorJSON(w, r, http.StatusUnauthorized, "unauthorized", "Invalid user session")
			return
		}
		token, err = middleware.SignUserSessionJWT(h.cfg, *session.ASN, session.Email, session.ID, session.LoginMethod, h.cfg.UserAccessTokenTTL)
		expiresIn = int64(h.cfg.UserAccessTokenTTL.Seconds())
		refreshTTL = h.cfg.UserRefreshTokenTTL
	case "impersonation":
		if session.ASN == nil || session.AdminID == nil || session.ParentSessionID == nil {
			ErrorJSON(w, r, http.StatusUnauthorized, "unauthorized", "Invalid impersonation session")
			return
		}
		token, err = middleware.SignImpersonationJWT(h.cfg, *session.ASN, session.Email, session.ID, *session.AdminID, *session.ParentSessionID)
		expiresIn = int64(h.cfg.ImpersonationTokenTTL.Seconds())
		refreshTTL = h.cfg.ImpersonationTokenTTL
	default:
		ErrorJSON(w, r, http.StatusUnauthorized, "unauthorized", "Session cannot be refreshed")
		return
	}
	if err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to generate token")
		return
	}
	ok, err := h.authRepo.RotateAuthSessionRefresh(ctx, session.ID, oldHash, newHash)
	if err != nil || !ok {
		clearRefreshCookie(w, r)
		ErrorJSON(w, r, http.StatusUnauthorized, "unauthorized", "Refresh token was already used")
		return
	}
	setRefreshCookie(w, r, rawRefresh, int(refreshTTL.Seconds()))
	if refreshed, err := h.authRepo.GetAuthSession(ctx, session.ID); err == nil {
		session = refreshed
	}
	resp := &authResponse{
		Token:            token,
		AccessToken:      token,
		ExpiresIn:        expiresIn,
		RefreshExpiresIn: int64(time.Until(session.ExpiresAt).Seconds()),
		Session:          session,
	}
	if session.ASN != nil {
		resp.ASN = *session.ASN
	}
	switch session.SubjectType {
	case "admin":
		resp.User = &authUserResponse{Role: "admin", Email: session.Email}
	case "user", "impersonation":
		resp.User = &authUserResponse{Role: "user", ASN: session.ASN, Email: session.Email}
	}
	JSON(w, http.StatusOK, resp)
}

func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if sid := middleware.GetSessionID(r.Context()); sid != "" {
		if session, err := h.authRepo.GetAuthSession(ctx, sid); err == nil && session.SubjectType == "impersonation" && session.ParentSessionID != nil {
			_, _ = h.authRepo.RevokeAuthSession(ctx, sid, "impersonation_logout")
		} else {
			_, _ = h.authRepo.RevokeAuthSession(ctx, sid, "logout")
			_ = h.authRepo.RevokeChildAuthSessions(ctx, sid, "parent_logout")
			clearRefreshCookie(w, r)
		}
	} else {
		clearRefreshCookie(w, r)
	}
	JSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *AuthHandler) ListUserDevices(w http.ResponseWriter, r *http.Request) {
	if middleware.GetSessionID(r.Context()) == "" {
		ErrorJSON(w, r, http.StatusUnauthorized, "session_required", "Session-backed login is required")
		return
	}
	if claims, err := middleware.ExtractClaims(h.cfg, r); err == nil && claims.LoginMethod == "impersonation" {
		ErrorJSON(w, r, http.StatusForbidden, "forbidden", "Impersonation sessions cannot manage user devices")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	sessions, err := h.authRepo.ListUserAuthSessions(ctx, middleware.GetASN(r.Context()))
	if err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to list devices")
		return
	}
	JSON(w, http.StatusOK, sessions)
}

func (h *AuthHandler) RevokeUserDevice(w http.ResponseWriter, r *http.Request) {
	if middleware.GetSessionID(r.Context()) == "" {
		ErrorJSON(w, r, http.StatusUnauthorized, "session_required", "Session-backed login is required")
		return
	}
	if claims, err := middleware.ExtractClaims(h.cfg, r); err == nil && claims.LoginMethod == "impersonation" {
		ErrorJSON(w, r, http.StatusForbidden, "forbidden", "Impersonation sessions cannot manage user devices")
		return
	}
	id := chi.URLParam(r, "id")
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	session, err := h.authRepo.GetAuthSession(ctx, id)
	if err != nil || session.ASN == nil || *session.ASN != middleware.GetASN(r.Context()) || session.SubjectType != "user" {
		ErrorJSON(w, r, http.StatusNotFound, "device_not_found", "Device not found")
		return
	}
	_, err = h.authRepo.RevokeAuthSession(ctx, id, "user_revoked")
	if err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to revoke device")
		return
	}
	if id == middleware.GetSessionID(r.Context()) {
		clearRefreshCookie(w, r)
	}
	JSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *AuthHandler) RevokeOtherUserDevices(w http.ResponseWriter, r *http.Request) {
	if middleware.GetSessionID(r.Context()) == "" {
		ErrorJSON(w, r, http.StatusUnauthorized, "session_required", "Session-backed login is required")
		return
	}
	if claims, err := middleware.ExtractClaims(h.cfg, r); err == nil && claims.LoginMethod == "impersonation" {
		ErrorJSON(w, r, http.StatusForbidden, "forbidden", "Impersonation sessions cannot manage user devices")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	sessions, err := h.authRepo.ListUserAuthSessions(ctx, middleware.GetASN(r.Context()))
	if err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to list devices")
		return
	}
	current := middleware.GetSessionID(r.Context())
	for _, session := range sessions {
		if session.ID == current {
			continue
		}
		_, _ = h.authRepo.RevokeAuthSession(ctx, session.ID, "user_revoked_other")
	}
	JSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *AuthHandler) ListAdminDevices(w http.ResponseWriter, r *http.Request) {
	if middleware.GetSessionID(r.Context()) == "" {
		ErrorJSON(w, r, http.StatusUnauthorized, "session_required", "Session-backed login is required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	sessions, err := h.authRepo.ListAdminAuthSessions(ctx, middleware.GetAdminID(r.Context()))
	if err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to list devices")
		return
	}
	JSON(w, http.StatusOK, sessions)
}

func (h *AuthHandler) RevokeAdminDevice(w http.ResponseWriter, r *http.Request) {
	if middleware.GetSessionID(r.Context()) == "" {
		ErrorJSON(w, r, http.StatusUnauthorized, "session_required", "Session-backed login is required")
		return
	}
	id := chi.URLParam(r, "id")
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	session, err := h.authRepo.GetAuthSession(ctx, id)
	adminID := middleware.GetAdminID(r.Context())
	if err != nil || session.AdminID == nil || *session.AdminID != adminID || session.SubjectType != "admin" {
		ErrorJSON(w, r, http.StatusNotFound, "device_not_found", "Device not found")
		return
	}
	_, err = h.authRepo.RevokeAuthSession(ctx, id, "admin_revoked")
	if err != nil {
		ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Failed to revoke device")
		return
	}
	_ = h.authRepo.RevokeChildAuthSessions(ctx, id, "parent_revoked")
	if id == middleware.GetSessionID(r.Context()) {
		clearRefreshCookie(w, r)
	}
	JSON(w, http.StatusOK, map[string]bool{"ok": true})
}
