package middleware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/akaere/autopeer-center/internal/apiversion"
	"github.com/akaere/autopeer-center/internal/config"
	"github.com/akaere/autopeer-center/internal/repository"
	"github.com/felixge/httpsnoop"
	"github.com/getsentry/sentry-go"
	"github.com/getsentry/sentry-go/attribute"
	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

var log = logrus.WithField("pkg", "middleware")

type contextKey string

const (
	ContextKeyRequestID       contextKey = "request_id"
	ContextKeyRole            contextKey = "role"
	ContextKeyASN             contextKey = "asn"
	ContextKeyAdminID         contextKey = "admin_id"
	ContextKeyEmail           contextKey = "email"
	ContextKeySessionID       contextKey = "session_id"
	ContextKeyMCPKeyID        contextKey = "mcp_key_id"
	ContextKeyAdminMCPKeyID   contextKey = "admin_mcp_key_id"
	ContextKeyMCPCapabilities contextKey = "mcp_capabilities"
	ContextKeyAPIVersion      contextKey = "api_version"
	contextKeyPanicMarker     contextKey = "panic_marker"
)

type panicMarker struct {
	recovered bool
}

func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := uuid.New().String()
		log.WithField("id", id).Debug("RequestID generated")
		ctx := context.WithValue(r.Context(), ContextKeyRequestID, id)
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func BodyLimit(limit int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, limit)
			}
			next.ServeHTTP(w, r)
		})
	}
}

func GetRequestID(ctx context.Context) string {
	if id, ok := ctx.Value(ContextKeyRequestID).(string); ok {
		return id
	}
	return ""
}

// GetAPIVersion returns the resolved Autopeer-Version for the request, or "" if
// the APIVersion middleware did not run (e.g. unversioned routes).
func GetAPIVersion(ctx context.Context) string {
	if v, ok := ctx.Value(ContextKeyAPIVersion).(string); ok {
		return v
	}
	return ""
}

// APIVersion negotiates the Stripe-style API version from the Autopeer-Version
// request header. An absent/empty header resolves to the latest version; a
// known version is used as-is; an unknown version is rejected with 400. The
// resolved version is stored in the request context (read by handlers via
// JSONVersioned) and echoed back in the Autopeer-Version response header.
//
// Must be mounted after RequestID — it reads the request ID for the error body.
// It writes the 400 body by hand (mirroring handler.ErrorJSON's shape) because
// middleware cannot import handler without an import cycle.
func APIVersion(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resolved, ok := apiversion.Resolve(r.Header.Get("Autopeer-Version"))
		if !ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error":      "invalid_api_version",
				"message":    "Unknown Autopeer-Version: " + strings.TrimSpace(r.Header.Get("Autopeer-Version")),
				"request_id": GetRequestID(r.Context()),
			})
			return
		}
		w.Header().Set("Autopeer-Version", resolved)
		ctx := context.WithValue(r.Context(), ContextKeyAPIVersion, resolved)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func CORS(allowedOrigins string) func(http.Handler) http.Handler {
	allowAll := false
	originSet := map[string]bool{}
	for _, o := range strings.Split(allowedOrigins, ",") {
		o = strings.TrimSpace(o)
		o = strings.TrimRight(o, "/")
		if o == "*" {
			allowAll = true
		} else if o != "" {
			originSet[o] = true
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reqOrigin := strings.TrimRight(r.Header.Get("Origin"), "/")
			log.WithFields(logrus.Fields{
				"origin":         reqOrigin,
				"origin_allowed": allowAll || originSet[reqOrigin],
			}).Debug("CORS origin check")
			if allowAll || originSet[reqOrigin] {
				w.Header().Set("Access-Control-Allow-Origin", reqOrigin)
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Set("Vary", "Origin")
			}
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Autopeer-Version")
			w.Header().Set("Access-Control-Expose-Headers", "X-Request-ID, Autopeer-Version")
			w.Header().Set("Access-Control-Max-Age", "86400")

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				if marker, ok := r.Context().Value(contextKeyPanicMarker).(*panicMarker); ok {
					marker.recovered = true
				}
				hub := sentry.GetHubFromContext(r.Context())
				if hub == nil {
					hub = sentry.CurrentHub()
				}
				hub.WithScope(func(scope *sentry.Scope) {
					scope.SetLevel(sentry.LevelError)
					hub.RecoverWithContext(r.Context(), err)
				})
				log.WithFields(logrus.Fields{
					"error": err,
					"stack": string(debug.Stack()),
				}).Error("panic recovered")
				http.Error(w, `{"error":"internal_error","message":"Internal server error"}`, http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type Claims struct {
	Role                  string `json:"role"`
	ASN                   int64  `json:"asn,omitempty"`
	Email                 string `json:"email"`
	SessionID             string `json:"sid,omitempty"`
	TokenID               string `json:"jti,omitempty"`
	Type                  string `json:"typ,omitempty"`
	LoginMethod           string `json:"login_method,omitempty"`
	ImpersonatedByAdminID string `json:"impersonated_by_admin_id,omitempty"`
	ParentSessionID       string `json:"parent_sid,omitempty"`
	jwt.RegisteredClaims
}

func SignUserJWT(cfg *config.Config, asn int64, email string) (string, error) {
	return SignUserSessionJWT(cfg, asn, email, "", "", cfg.UserAccessTokenTTL)
}

func SignUserSessionJWT(cfg *config.Config, asn int64, email, sessionID, loginMethod string, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		ttl = cfg.UserAccessTokenTTL
	}
	now := time.Now()
	claims := Claims{
		Role:        "user",
		ASN:         asn,
		Email:       email,
		SessionID:   sessionID,
		TokenID:     uuid.NewString(),
		Type:        "access",
		LoginMethod: loginMethod,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   fmt.Sprintf("%d", asn),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	log.WithField("asn", asn).Debug("SignUserJWT signing")
	return token.SignedString([]byte(cfg.JWTSecret))
}

func SignAdminJWT(cfg *config.Config, adminID, email string) (string, error) {
	return SignAdminSessionJWT(cfg, adminID, email, "", cfg.AdminAccessTokenTTL)
}

func SignAdminSessionJWT(cfg *config.Config, adminID, email, sessionID string, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		ttl = cfg.AdminAccessTokenTTL
	}
	now := time.Now()
	claims := Claims{
		Role:      "admin",
		Email:     email,
		SessionID: sessionID,
		TokenID:   uuid.NewString(),
		Type:      "access",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   adminID,
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	log.WithField("admin_id", adminID).Debug("SignAdminJWT signing")
	return token.SignedString([]byte(cfg.JWTSecret))
}

func SignImpersonationJWT(cfg *config.Config, asn int64, email, sessionID, adminID, parentSessionID string) (string, error) {
	ttl := cfg.ImpersonationTokenTTL
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	now := time.Now()
	claims := Claims{
		Role:                  "user",
		ASN:                   asn,
		Email:                 email,
		SessionID:             sessionID,
		TokenID:               uuid.NewString(),
		Type:                  "access",
		LoginMethod:           "impersonation",
		ImpersonatedByAdminID: adminID,
		ParentSessionID:       parentSessionID,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   fmt.Sprintf("%d", asn),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	log.WithFields(logrus.Fields{"asn": asn, "admin_id": adminID}).Debug("SignImpersonationJWT signing")
	return token.SignedString([]byte(cfg.JWTSecret))
}

func parseJWT(cfg *config.Config, tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(cfg.JWTSecret), nil
	})
	if err != nil {
		log.WithError(err).Warn("parseJWT failed")
		return nil, err
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		log.Warn("parseJWT invalid token claims")
		return nil, fmt.Errorf("invalid token")
	}
	if claims.Type != "" && claims.Type != "access" {
		return nil, fmt.Errorf("invalid token type")
	}
	return claims, nil
}

func RequireUserWithSessions(cfg *config.Config, authRepo repository.AuthRepository) func(http.Handler) http.Handler {
	return requireAuth(cfg, authRepo, "user", false)
}

func RequireAnyAuthWithSessions(cfg *config.Config, authRepo repository.AuthRepository) func(http.Handler) http.Handler {
	return requireAuth(cfg, authRepo, "", true)
}

func RequireAdminWithSessions(cfg *config.Config, authRepo repository.AuthRepository) func(http.Handler) http.Handler {
	return requireAuth(cfg, authRepo, "admin", false)
}

func requireAuth(cfg *config.Config, authRepo repository.AuthRepository, role string, any bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, err := ExtractClaims(cfg, r)
			if err != nil {
				log.WithError(err).Warn("auth rejected")
				http.Error(w, `{"error":"unauthorized","message":"Invalid or missing token"}`, http.StatusUnauthorized)
				return
			}
			if !any && claims.Role != role {
				http.Error(w, `{"error":"forbidden","message":"Access denied"}`, http.StatusForbidden)
				return
			}
			if any && claims.Role != "user" && claims.Role != "admin" {
				http.Error(w, `{"error":"forbidden","message":"Authentication required"}`, http.StatusForbidden)
				return
			}
			if err := validateSession(r.Context(), authRepo, claims); err != nil {
				log.WithError(err).WithField("session_id", claims.SessionID).Warn("session rejected")
				http.Error(w, `{"error":"unauthorized","message":"Session expired or revoked"}`, http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r.WithContext(contextWithClaims(r.Context(), claims)))
		})
	}
}

func validateSession(ctx context.Context, authRepo repository.AuthRepository, claims *Claims) error {
	if claims.SessionID == "" {
		return nil
	}
	session, err := authRepo.GetAuthSession(ctx, claims.SessionID)
	if err != nil {
		return err
	}
	if session.RevokedAt != nil || time.Now().After(session.ExpiresAt) {
		return fmt.Errorf("session inactive")
	}
	if claims.Role == "admin" && session.SubjectType != "admin" {
		return fmt.Errorf("session role mismatch")
	}
	if claims.Role == "user" && session.SubjectType != "user" && session.SubjectType != "impersonation" {
		return fmt.Errorf("session role mismatch")
	}
	if session.SubjectType == "impersonation" && session.ParentSessionID != nil {
		parent, err := authRepo.GetAuthSession(ctx, *session.ParentSessionID)
		if err != nil {
			return err
		}
		if parent.RevokedAt != nil || time.Now().After(parent.ExpiresAt) {
			return fmt.Errorf("parent session inactive")
		}
	}
	go func() {
		updateCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = authRepo.TouchAuthSession(updateCtx, claims.SessionID)
	}()
	return nil
}

func contextWithClaims(ctx context.Context, claims *Claims) context.Context {
	ctx = context.WithValue(ctx, ContextKeyRole, claims.Role)
	ctx = context.WithValue(ctx, ContextKeyASN, claims.ASN)
	ctx = context.WithValue(ctx, ContextKeyEmail, claims.Email)
	ctx = context.WithValue(ctx, ContextKeySessionID, claims.SessionID)
	if claims.Role == "admin" {
		ctx = context.WithValue(ctx, ContextKeyAdminID, claims.Subject)
	}
	return ctx
}

func RequireUser(cfg *config.Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, err := ExtractClaims(cfg, r)
			if err != nil {
				log.WithError(err).Warn("RequireUser: auth rejected")
				http.Error(w, `{"error":"unauthorized","message":"Invalid or missing token"}`, http.StatusUnauthorized)
				return
			}
			if claims.Role != "user" {
				log.WithField("role", claims.Role).Warn("RequireUser: forbidden (wrong role)")
				http.Error(w, `{"error":"forbidden","message":"User access required"}`, http.StatusForbidden)
				return
			}
			log.WithFields(logrus.Fields{
				"role":  claims.Role,
				"asn":   claims.ASN,
				"email": claims.Email,
			}).Info("RequireUser: auth success")
			ctx := r.Context()
			ctx = context.WithValue(ctx, ContextKeyRole, claims.Role)
			ctx = context.WithValue(ctx, ContextKeyASN, claims.ASN)
			ctx = context.WithValue(ctx, ContextKeyEmail, claims.Email)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func RequireAnyAuth(cfg *config.Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, err := ExtractClaims(cfg, r)
			if err != nil {
				log.WithError(err).Warn("RequireAnyAuth: auth rejected")
				http.Error(w, `{"error":"unauthorized","message":"Invalid or missing token"}`, http.StatusUnauthorized)
				return
			}
			if claims.Role != "user" && claims.Role != "admin" {
				log.WithField("role", claims.Role).Warn("RequireAnyAuth: forbidden")
				http.Error(w, `{"error":"forbidden","message":"Authentication required"}`, http.StatusForbidden)
				return
			}
			log.WithFields(logrus.Fields{
				"role":  claims.Role,
				"asn":   claims.ASN,
				"email": claims.Email,
			}).Info("RequireAnyAuth: auth success")
			ctx := r.Context()
			ctx = context.WithValue(ctx, ContextKeyRole, claims.Role)
			ctx = context.WithValue(ctx, ContextKeyASN, claims.ASN)
			ctx = context.WithValue(ctx, ContextKeyEmail, claims.Email)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func RequireAdmin(cfg *config.Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, err := ExtractClaims(cfg, r)
			if err != nil {
				log.WithError(err).Warn("RequireAdmin: auth rejected")
				http.Error(w, `{"error":"unauthorized","message":"Invalid or missing token"}`, http.StatusUnauthorized)
				return
			}
			if claims.Role != "admin" {
				log.WithField("role", claims.Role).Warn("RequireAdmin: forbidden (not admin)")
				http.Error(w, `{"error":"forbidden","message":"Admin access required"}`, http.StatusForbidden)
				return
			}
			log.WithFields(logrus.Fields{
				"role":     claims.Role,
				"admin_id": claims.Subject,
				"email":    claims.Email,
			}).Info("RequireAdmin: auth success")
			ctx := r.Context()
			ctx = context.WithValue(ctx, ContextKeyRole, claims.Role)
			ctx = context.WithValue(ctx, ContextKeyAdminID, claims.Subject)
			ctx = context.WithValue(ctx, ContextKeyEmail, claims.Email)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func ExtractClaims(cfg *config.Config, r *http.Request) (*Claims, error) {
	auth := r.Header.Get("Authorization")
	if auth == "" || !strings.HasPrefix(auth, "Bearer ") {
		log.Debug("extractClaims: missing bearer token")
		return nil, fmt.Errorf("missing bearer token")
	}
	tokenStr := strings.TrimPrefix(auth, "Bearer ")
	return parseJWT(cfg, tokenStr)
}

func GetASN(ctx context.Context) int64 {
	if asn, ok := ctx.Value(ContextKeyASN).(int64); ok {
		return asn
	}
	return 0
}

func GetEmail(ctx context.Context) string {
	if email, ok := ctx.Value(ContextKeyEmail).(string); ok {
		return email
	}
	return ""
}

func GetAdminID(ctx context.Context) string {
	if id, ok := ctx.Value(ContextKeyAdminID).(string); ok {
		return id
	}
	return ""
}

func GetSessionID(ctx context.Context) string {
	if id, ok := ctx.Value(ContextKeySessionID).(string); ok {
		return id
	}
	return ""
}

func GetRole(ctx context.Context) string {
	if role, ok := ctx.Value(ContextKeyRole).(string); ok {
		return role
	}
	return ""
}

func GetMCPKeyID(ctx context.Context) string {
	if id, ok := ctx.Value(ContextKeyMCPKeyID).(string); ok {
		return id
	}
	return ""
}

func GetAdminMCPKeyID(ctx context.Context) string {
	if id, ok := ctx.Value(ContextKeyAdminMCPKeyID).(string); ok {
		return id
	}
	return ""
}

func GetMCPCapabilities(ctx context.Context) []string {
	if caps, ok := ctx.Value(ContextKeyMCPCapabilities).([]string); ok {
		return caps
	}
	return nil
}

// RequireMCPKey validates an MCP API key (ap_mcp_...) from the Authorization header.
// It only accepts keys with the ap_mcp_ prefix — JWT tokens are explicitly rejected.
// On success it injects ContextKeyASN and ContextKeyRole="user" into the request context.
func RequireMCPKey(mcpRepo repository.MCPRepository) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth := r.Header.Get("Authorization")
			if auth == "" || !strings.HasPrefix(auth, "Bearer ") {
				http.Error(w, `{"error":"unauthorized","message":"Missing MCP API key"}`, http.StatusUnauthorized)
				return
			}
			rawKey := strings.TrimPrefix(auth, "Bearer ")
			if strings.HasPrefix(rawKey, "ap_mcp_admin_") {
				http.Error(w, `{"error":"unauthorized","message":"Invalid MCP API key format"}`, http.StatusUnauthorized)
				return
			}
			if !strings.HasPrefix(rawKey, "ap_mcp_") {
				http.Error(w, `{"error":"unauthorized","message":"Invalid MCP API key format"}`, http.StatusUnauthorized)
				return
			}

			hash := sha256.Sum256([]byte(rawKey))
			keyHash := hex.EncodeToString(hash[:])

			ctx := r.Context()
			key, err := mcpRepo.GetUserKeyByHash(ctx, keyHash)
			if err != nil {
				log.WithError(err).Debug("RequireMCPKey: key not found")
				http.Error(w, `{"error":"unauthorized","message":"Invalid or revoked MCP API key"}`, http.StatusUnauthorized)
				return
			}

			if key.ExpiresAt != nil && time.Now().After(*key.ExpiresAt) {
				http.Error(w, `{"error":"unauthorized","message":"MCP API key has expired"}`, http.StatusUnauthorized)
				return
			}
			if key.RevokedAt != nil {
				http.Error(w, `{"error":"unauthorized","message":"MCP API key has been revoked"}`, http.StatusUnauthorized)
				return
			}

			go func() {
				updateCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				_ = mcpRepo.TouchUserKey(updateCtx, key.ID)
			}()

			ctx = context.WithValue(ctx, ContextKeyRole, "user")
			ctx = context.WithValue(ctx, ContextKeyASN, key.ASN)
			ctx = context.WithValue(ctx, ContextKeyMCPKeyID, key.ID)
			ctx = context.WithValue(ctx, ContextKeyMCPCapabilities, key.Capabilities)
			log.WithFields(logrus.Fields{"asn": key.ASN, "key_id": key.ID}).Debug("RequireMCPKey: auth success")
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireAdminMCPKey validates an admin MCP API key (ap_mcp_admin_...) from the Authorization header.
// On success it injects ContextKeyAdminID, ContextKeyRole="admin", and ContextKeyAdminMCPKeyID into context.
func RequireAdminMCPKey(mcpRepo repository.MCPRepository) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth := r.Header.Get("Authorization")
			if auth == "" || !strings.HasPrefix(auth, "Bearer ") {
				http.Error(w, `{"error":"unauthorized","message":"Missing admin MCP API key"}`, http.StatusUnauthorized)
				return
			}
			rawKey := strings.TrimPrefix(auth, "Bearer ")
			if !strings.HasPrefix(rawKey, "ap_mcp_admin_") {
				http.Error(w, `{"error":"unauthorized","message":"Invalid admin MCP API key format"}`, http.StatusUnauthorized)
				return
			}

			hash := sha256.Sum256([]byte(rawKey))
			keyHash := hex.EncodeToString(hash[:])

			ctx := r.Context()
			key, err := mcpRepo.GetAdminKeyByHash(ctx, keyHash)
			if err != nil {
				log.WithError(err).Debug("RequireAdminMCPKey: key not found")
				http.Error(w, `{"error":"unauthorized","message":"Invalid or revoked admin MCP API key"}`, http.StatusUnauthorized)
				return
			}

			if key.ExpiresAt != nil && time.Now().After(*key.ExpiresAt) {
				http.Error(w, `{"error":"unauthorized","message":"Admin MCP API key has expired"}`, http.StatusUnauthorized)
				return
			}
			if key.RevokedAt != nil {
				http.Error(w, `{"error":"unauthorized","message":"Admin MCP API key has been revoked"}`, http.StatusUnauthorized)
				return
			}

			go func() {
				updateCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				_ = mcpRepo.TouchAdminKey(updateCtx, key.ID)
			}()

			ctx = context.WithValue(ctx, ContextKeyRole, "admin")
			ctx = context.WithValue(ctx, ContextKeyAdminID, key.AdminID)
			ctx = context.WithValue(ctx, ContextKeyAdminMCPKeyID, key.ID)
			ctx = context.WithValue(ctx, ContextKeyMCPCapabilities, key.Capabilities)
			log.WithFields(logrus.Fields{"admin_id": key.AdminID, "key_id": key.ID}).Debug("RequireAdminMCPKey: auth success")
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func RequestLog(writeLog func(method, path string, status int, ip string, durationMs int64), trustedNets []*net.IPNet) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			marker := &panicMarker{}
			r = r.WithContext(context.WithValue(r.Context(), contextKeyPanicMarker, marker))
			metrics := httpsnoop.CaptureMetrics(next, w, r)

			ip := RealIP(r, trustedNets)

			method := r.Method
			path := r.URL.Path
			status := metrics.Code
			durationMs := metrics.Duration.Milliseconds()

			log.WithFields(logrus.Fields{
				"method":   method,
				"path":     path,
				"status":   status,
				"duration": durationMs,
			}).Info("RequestLog")

			recordSentryHTTPRequest(r, method, path, status, durationMs, IsLongLivedRequest(r), marker.recovered)

			go writeLog(method, path, status, ip, durationMs)
		})
	}
}

func recordSentryHTTPRequest(r *http.Request, method, path string, status int, durationMs int64, longLived bool, recoveredPanic bool) {
	route := sentryRouteName(r, path)
	updateSentryTransactionName(r, method, route)

	statusClass := strconv.Itoa(status/100) + "xx"
	attrs := []attribute.Builder{
		attribute.String("method", method),
		attribute.String("route", route),
		attribute.Int("status_code", status),
		attribute.String("status_class", statusClass),
		attribute.String("long_lived", strconv.FormatBool(longLived)),
	}

	meter := sentry.NewMeter(r.Context())
	meter.Count("http.server.requests", 1, sentry.WithAttributes(attrs...))
	if !longLived {
		meter.Distribution("http.server.duration", float64(durationMs), sentry.WithUnit(sentry.UnitMillisecond), sentry.WithAttributes(attrs...))
	}

	if status < http.StatusInternalServerError {
		return
	}

	meter.Count("http.server.errors", 1, sentry.WithAttributes(attrs...))
	if recoveredPanic {
		return
	}
	captureSentryHTTPError(r, method, path, route, status, durationMs)
}

func captureSentryHTTPError(r *http.Request, method, path, route string, status int, durationMs int64) {
	hub := sentry.GetHubFromContext(r.Context())
	if hub == nil {
		hub = sentry.CurrentHub()
	}

	hub.WithScope(func(scope *sentry.Scope) {
		scope.SetLevel(sentry.LevelError)
		scope.SetRequest(r)
		scope.SetTag("http.method", method)
		scope.SetTag("http.route", route)
		scope.SetTag("http.status_code", strconv.Itoa(status))
		if requestID := GetRequestID(r.Context()); requestID != "" {
			scope.SetContext("request", sentry.Context{"id": requestID})
		}
		scope.SetFingerprint([]string{"http-5xx", route, strconv.Itoa(status)})
		scope.SetContext("http", sentry.Context{
			"method":      method,
			"path":        path,
			"route":       route,
			"status":      status,
			"duration_ms": durationMs,
		})
		hub.CaptureMessage("HTTP request returned 5xx")
	})
}

func updateSentryTransactionName(r *http.Request, method, route string) {
	transaction := sentry.TransactionFromContext(r.Context())
	if transaction == nil || route == "" {
		return
	}
	transaction.Name = method + " " + route
	transaction.Source = sentry.SourceRoute
}

func IsLongLivedRequest(r *http.Request) bool {
	path := r.URL.Path
	if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") || strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade") {
		return true
	}
	if strings.Contains(path, "/ws") || strings.Contains(path, "/stream") {
		return true
	}
	if r.Method == http.MethodGet && (strings.HasSuffix(path, "/mcp") || strings.Contains(path, "/mcp/")) {
		return true
	}
	return false
}

func sentryRouteName(r *http.Request, fallback string) string {
	if routeCtx := chi.RouteContext(r.Context()); routeCtx != nil {
		if pattern := routeCtx.RoutePattern(); pattern != "" {
			return pattern
		}
	}
	return normalizeMetricPath(fallback)
}

func normalizeMetricPath(path string) string {
	if path == "" || path == "/" {
		return path
	}
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if part == "" {
			continue
		}
		if _, err := uuid.Parse(part); err == nil {
			parts[i] = "{uuid}"
			continue
		}
		if _, err := strconv.ParseInt(part, 10, 64); err == nil {
			parts[i] = "{number}"
		}
	}
	return strings.Join(parts, "/")
}

func RealIP(r *http.Request, trustedNets []*net.IPNet) string {
	if len(trustedNets) > 0 {
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		ip := net.ParseIP(host)
		if ip != nil {
			for _, n := range trustedNets {
				if n.Contains(ip) {
					if xrip := r.Header.Get("X-Real-IP"); xrip != "" {
						return xrip
					}
					if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
						return strings.SplitN(xff, ",", 2)[0]
					}
				}
			}
		}
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	return host
}

func ParseTrustedProxies(cidr string) []*net.IPNet {
	if cidr == "" {
		return nil
	}
	var nets []*net.IPNet
	for _, s := range strings.Split(cidr, ",") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if !strings.Contains(s, "/") {
			s += "/32"
		}
		_, ipNet, err := net.ParseCIDR(s)
		if err != nil {
			continue
		}
		nets = append(nets, ipNet)
	}
	return nets
}
