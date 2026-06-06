package config

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

var isWindows = runtime.GOOS == "windows"

var log = logrus.WithField("pkg", "config")

var global *Config

type Config struct {
	DatabaseURL            string
	JWTSecret              string
	RedisURL               string
	RedisRequired          bool
	RedisKeyPrefix         string
	AsynqEnabled           bool
	AsynqConcurrency       int
	AsynqQueues            string
	AsynqReadonlyMonitor   bool
	AsynqMonitorPageSize   int
	CacheLocalFallback     bool
	DN42RegistryToken      string
	EmailAPIURL            string
	EmailAPIKey            string
	AdminInitialEmail      string
	AdminInitialPassword   string
	ListenAddr             string
	CORSOrigin             string
	AgentReleaseDir        string
	ExternalURL            string
	AdminNotifyEmails      string
	CacheDir               string
	CenterKeyPath          string
	TrustedProxyCIDR       string
	TurnstileSiteKey       string
	TurnstileSecretKey     string
	IPInfoToken            string
	SentryDSN              string
	SentryEnvironment      string
	SentryRelease          string
	SentryTracesSampleRate float64
	SentryDebug            bool
	SentryEnableMetrics    bool
	UserAccessTokenTTL     time.Duration
	AdminAccessTokenTTL    time.Duration
	UserRefreshTokenTTL    time.Duration
	AdminRefreshTokenTTL   time.Duration
	ImpersonationTokenTTL  time.Duration
	TelegramBotUsername    string
	WebAuthnRPID           string
	WebAuthnOrigin         string
	ReconcileEnabled       bool
	ReconcileInterval      time.Duration
}

func Load() (*Config, error) {
	cfg := &Config{
		DatabaseURL:            getEnv("DATABASE_URL", ""),
		JWTSecret:              getEnv("JWT_SECRET", ""),
		RedisURL:               strings.TrimSpace(getEnv("REDIS_URL", "")),
		RedisRequired:          getEnvBool("REDIS_REQUIRED", false),
		RedisKeyPrefix:         getEnv("REDIS_KEY_PREFIX", "autopeer:center:"),
		AsynqEnabled:           getEnvBool("ASYNQ_ENABLED", true),
		AsynqConcurrency:       GetEnvInt("ASYNQ_CONCURRENCY", 10),
		AsynqQueues:            getEnv("ASYNQ_QUEUES", "critical:6,default:3,low:1"),
		AsynqReadonlyMonitor:   getEnvBool("ASYNQ_READONLY_MONITOR", false),
		AsynqMonitorPageSize:   GetEnvInt("ASYNQ_MONITOR_PAGE_SIZE", 20),
		CacheLocalFallback:     getEnvBool("CACHE_LOCAL_FALLBACK", true),
		DN42RegistryToken:      getEnv("DN42_REGISTRY_TOKEN", ""),
		EmailAPIURL:            getEnv("EMAIL_API_URL", ""),
		EmailAPIKey:            getEnv("EMAIL_API_KEY", ""),
		AdminInitialEmail:      getEnv("ADMIN_INITIAL_EMAIL", ""),
		AdminInitialPassword:   getEnv("ADMIN_INITIAL_PASSWORD", ""),
		ListenAddr:             getEnv("LISTEN_ADDR", ":8080"),
		CORSOrigin:             getEnv("CORS_ORIGIN", "http://localhost:8080"),
		AgentReleaseDir:        getEnv("AGENT_RELEASE_DIR", dirDefault("/var/lib/autopeer-center/releases", "./releases")),
		ExternalURL:            getEnv("EXTERNAL_URL", "http://localhost:8080"),
		AdminNotifyEmails:      getEnv("ADMIN_NOTIFY_EMAILS", ""),
		CacheDir:               getEnv("CACHE_DIR", dirDefault("/var/lib/autopeer-center/cache", "./cache")),
		CenterKeyPath:          getEnv("CENTER_KEY_PATH", dirDefault("/var/lib/autopeer-center/center_keys.json", "./center_keys.json")),
		TrustedProxyCIDR:       getEnv("TRUSTED_PROXY_CIDR", ""),
		TurnstileSiteKey:       getEnv("TURNSTILE_SITE_KEY", ""),
		TurnstileSecretKey:     getEnv("TURNSTILE_SECRET_KEY", ""),
		IPInfoToken:            getEnv("IPINFO_TOKEN", ""),
		SentryDSN:              strings.TrimSpace(getEnv("SENTRY_DSN", "")),
		SentryEnvironment:      strings.TrimSpace(getEnv("SENTRY_ENVIRONMENT", "production")),
		SentryRelease:          strings.TrimSpace(getEnv("SENTRY_RELEASE", "")),
		SentryTracesSampleRate: getEnvFloat("SENTRY_TRACES_SAMPLE_RATE", 0),
		SentryDebug:            getEnvBool("SENTRY_DEBUG", false),
		SentryEnableMetrics:    getEnvBool("SENTRY_ENABLE_METRICS", false),
		UserAccessTokenTTL:     getEnvDuration("USER_ACCESS_TOKEN_TTL", time.Hour),
		AdminAccessTokenTTL:    getEnvDuration("ADMIN_ACCESS_TOKEN_TTL", 30*time.Minute),
		UserRefreshTokenTTL:    getEnvDuration("USER_REFRESH_TOKEN_TTL", 30*24*time.Hour),
		AdminRefreshTokenTTL:   getEnvDuration("ADMIN_REFRESH_TOKEN_TTL", 14*24*time.Hour),
		ImpersonationTokenTTL:  getEnvDuration("IMPERSONATION_TOKEN_TTL", 15*time.Minute),
		TelegramBotUsername:    strings.TrimPrefix(strings.TrimSpace(getEnv("TELEGRAM_BOT_USERNAME", "")), "@"),
		WebAuthnRPID:           getEnv("WEBAUTHN_RPID", "localhost"),
		WebAuthnOrigin:         getEnv("WEBAUTHN_ORIGIN", "http://localhost:8080"),
		ReconcileEnabled:       getEnvBool("RECONCILE_ENABLED", true),
		ReconcileInterval:      getEnvDuration("RECONCILE_INTERVAL", 10*time.Minute),
	}

	log.WithFields(logrus.Fields{
		"database_url":              maskPassword(cfg.DatabaseURL),
		"jwt_secret":                "***",
		"email_api_url":             cfg.EmailAPIURL,
		"email_api_key":             "***",
		"listen_addr":               cfg.ListenAddr,
		"cors_origin":               cfg.CORSOrigin,
		"external_url":              cfg.ExternalURL,
		"admin_email":               cfg.AdminInitialEmail,
		"admin_password":            "***",
		"sentry_enabled":            cfg.SentryDSN != "",
		"sentry_environment":        cfg.SentryEnvironment,
		"sentry_traces_sample_rate": cfg.SentryTracesSampleRate,
		"sentry_metrics_enabled":    cfg.SentryEnableMetrics,
	}).Debug("loaded config values")

	if cfg.DatabaseURL == "" {
		log.Error("validation failed: DATABASE_URL is required")
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	log.WithField("field", "DATABASE_URL").Debug("validation check passed")

	if cfg.JWTSecret == "" {
		log.Error("validation failed: JWT_SECRET is required")
		return nil, fmt.Errorf("JWT_SECRET is required")
	}
	if len(cfg.JWTSecret) < 32 {
		log.Errorf("validation failed: JWT_SECRET must be at least 32 characters, got %d", len(cfg.JWTSecret))
		return nil, fmt.Errorf("JWT_SECRET must be at least 32 characters (got %d)", len(cfg.JWTSecret))
	}
	log.WithField("field", "JWT_SECRET").Debug("validation check passed")

	if cfg.SentryDSN != "" {
		if cfg.SentryTracesSampleRate < 0 || cfg.SentryTracesSampleRate > 1 {
			return nil, fmt.Errorf("SENTRY_TRACES_SAMPLE_RATE must be between 0 and 1")
		}
		if cfg.SentryEnvironment == "" {
			cfg.SentryEnvironment = "production"
		}
	}

	log.Info("config loaded successfully")

	if cfg.EmailAPIURL == "" {
		log.Warn("EMAIL_API_URL is empty — email delivery is disabled")
	} else if cfg.EmailAPIKey == "" {
		log.Warn("EMAIL_API_KEY is empty — email requests will be rejected by the API")
	}

	if strings.TrimRight(cfg.ExternalURL, "/") == "http://localhost:8080" {
		log.Warn("EXTERNAL_URL is using the default http://localhost:8080 — agent download URLs may be incorrect in production")
	}

	global = cfg
	return cfg, nil
}

func maskPassword(databaseURL string) string {
	idx := strings.Index(databaseURL, "@")
	if idx == -1 {
		if len(databaseURL) > 0 {
			return "***"
		}
		return ""
	}
	prefixIdx := strings.Index(databaseURL, "://")
	if prefixIdx == -1 {
		return "***"
	}
	return databaseURL[:prefixIdx+3] + "***" + databaseURL[idx:]
}

func Get() *Config {
	return global
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvFloat(key string, fallback float64) float64 {
	if v := os.Getenv(key); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err == nil {
			return f
		}
		log.WithError(err).WithField("key", key).Warn("invalid float environment variable, using fallback")
	}
	return fallback
}

func getEnvBool(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		b, err := strconv.ParseBool(v)
		if err == nil {
			return b
		}
		log.WithError(err).WithField("key", key).Warn("invalid bool environment variable, using fallback")
	}
	return fallback
}

func dirDefault(linux, windows string) string {
	if isWindows {
		return windows
	}
	return linux
}

func getEnvDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		d, err := time.ParseDuration(v)
		if err == nil && d > 0 {
			return d
		}
		if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
		log.WithError(err).WithField("key", key).Warn("invalid duration environment variable, using fallback")
	}
	return fallback
}

func GetEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}
