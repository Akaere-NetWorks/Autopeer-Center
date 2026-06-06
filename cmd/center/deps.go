package main

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/akaere/autopeer-center/internal/cache"
	"github.com/akaere/autopeer-center/internal/config"
	"github.com/akaere/autopeer-center/internal/crypto"
	"github.com/akaere/autopeer-center/internal/endpoint"
	"github.com/akaere/autopeer-center/internal/handler"
	"github.com/akaere/autopeer-center/internal/latency"
	"github.com/akaere/autopeer-center/internal/lock"
	"github.com/akaere/autopeer-center/internal/middleware"
	"github.com/akaere/autopeer-center/internal/model"
	"github.com/akaere/autopeer-center/internal/queue"
	"github.com/akaere/autopeer-center/internal/redisx"
	"github.com/akaere/autopeer-center/internal/repository"
	"github.com/akaere/autopeer-center/internal/service"
	"github.com/akaere/autopeer-center/internal/ws"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/uptrace/bun"
)

// AppDeps holds all wired-up dependencies needed by the router and lifecycle management.
type AppDeps struct {
	// Config & networking
	Cfg         *config.Config
	TrustedNets []*net.IPNet

	// Repositories exposed for middleware and main lifecycle use
	AuthRepo    repository.AuthRepository
	MetricsRepo repository.MetricsRepository
	MCPRepo     repository.MCPRepository
	NodeRepo    repository.NodeRepository
	PeerRepo    repository.PeerRepository
	CleanupRepo repository.CleanupRepository

	// HTTP handlers
	AuthHandler         *handler.AuthHandler
	NodeHandler         *handler.NodeHandler
	PeerHandler         *handler.PeerHandler
	LookingGlassHandler *handler.LookingGlassHandler
	AdminHandler        *handler.AdminHandler
	AgentHandler        *handler.AgentHandler
	BotHandler          *handler.BotHandler
	RegistryHandler     *handler.RegistryHandler
	StatsHandler        *handler.StatsHandler
	MCPKeyHandler       *handler.MCPKeyHandler
	AdminMCPKeyHandler  *handler.AdminMCPKeyHandler
	MCPHandler          *handler.MCPHandler
	AdminMCPHandler     *handler.AdminMCPHandler
	EmailPrefHandler    *handler.EmailPreferencesHandler
	PasskeyHandler      *handler.PasskeyHandler
	TelegramBindHandler *handler.TelegramBindingHandler
	SystemStatusHandler *handler.SystemStatusHandler
	QueueMonitorHandler *handler.QueueMonitorHandler

	// Background services
	Hub             *ws.Hub
	Q               *queue.Queue
	Inspector       *queue.Inspector
	AuditSvc        *service.AuditService
	Locker          lock.Locker
	LatencyChecker  *latency.Checker
	EndpointChecker *endpoint.Checker

	// Shutdown hooks
	StopMCPAudit      func()
	StopAdminMCPAudit func()
}

// BuildDeps constructs and wires all application dependencies from the provided
// infrastructure primitives (DB, Redis, cache, locker, key pair).
func BuildDeps(
	ctx context.Context,
	cfg *config.Config,
	db *pgxpool.Pool,
	bunDB *bun.DB,
	redisClient *redisx.Client,
	c *cache.Cache,
	locker lock.Locker,
	centerKP *crypto.KeyPair,
) (*AppDeps, error) {
	// ── Repositories ────────────────────────────────────────────────────────
	adminRepo := repository.NewAdminRepository(bunDB)
	auditRepo := repository.NewAuditRepository(bunDB)
	authRepo := repository.NewAuthRepository(bunDB)
	nodeRepo := repository.NewNodeRepository(bunDB)
	mcpRepo := repository.NewMCPRepository(bunDB)
	peerRepo := repository.NewPeerRepository(bunDB)
	metricsRepo := repository.NewMetricsRepository(bunDB)
	settingsRepo := repository.NewSettingsRepository(bunDB)
	botRepo := repository.NewBotRepository(bunDB)
	botBindingRepo := repository.NewBotBindingRepository(bunDB)
	releaseRepo := repository.NewReleaseRepository(bunDB)
	statsRepo := repository.NewStatsRepository(bunDB)
	passkeyRepo := repository.NewPasskeyRepository(bunDB)
	cleanupRepo := repository.NewCleanupRepository(bunDB)

	// ── Services ─────────────────────────────────────────────────────────────
	registry := service.NewRegistryService(cfg.DN42RegistryToken, c)
	emailSvc := service.NewEmailService(cfg.EmailAPIURL, cfg.EmailAPIKey)
	auditSvc := service.NewAuditService(auditRepo)

	// ── WebSocket hub ────────────────────────────────────────────────────────
	hub := ws.NewHub(nodeRepo, peerRepo, metricsRepo, botRepo, botBindingRepo, auditRepo, statsRepo, c)
	hub.SetLocker(locker)
	hub.LoadBotSettings()

	trustedNets := middleware.ParseTrustedProxies(cfg.TrustedProxyCIDR)

	// ── HTTP handlers ────────────────────────────────────────────────────────
	authHandler := handler.NewAuthHandler(authRepo, adminRepo, nodeRepo, cfg, registry, emailSvc, auditSvc, c)
	nodeHandler := handler.NewNodeHandler(nodeRepo)
	peerHandler := handler.NewPeerHandler(peerRepo, nodeRepo, metricsRepo, registry, emailSvc, auditSvc, hub, locker)
	lookingGlassHandler := handler.NewLookingGlassHandler(hub, nodeRepo, auditSvc)
	adminHandler := handler.NewAdminHandler(peerRepo, nodeRepo, metricsRepo, settingsRepo, auditSvc, botRepo, releaseRepo, statsRepo, emailSvc, hub, c, registry, locker)
	authHandler.SetAdminHandler(adminHandler)
	peerHandler.SetAdminHandler(adminHandler)
	agentHandler := handler.NewAgentHandler(nodeRepo, releaseRepo, hub, centerKP, cfg.CORSOrigin)
	botHandler := handler.NewBotHandler(hub, cfg.CORSOrigin)
	registryHandler := handler.NewRegistryHandler(registry, c, cfg, trustedNets)
	statsHandler := handler.NewStatsHandler(repository.NewStatsRepository(bunDB))
	mcpKeyHandler := handler.NewMCPKeyHandler(mcpRepo)
	adminMCPKeyHandler := handler.NewAdminMCPKeyHandler(mcpRepo, auditSvc)
	emailPrefHandler := handler.NewEmailPreferencesHandler(authRepo)
	emailPrefHandler.SetGPGAvailabilityFunc(func(asn int64) bool {
		fingerprints, err := registry.LookupMntnerGPGFingerprints(asn)
		return err == nil && len(fingerprints) > 0
	})
	passkeyHandler, err := handler.NewPasskeyHandler(passkeyRepo, authRepo, cfg, auditSvc, registry, c)
	if err != nil {
		return nil, fmt.Errorf("failed to create passkey handler: %w", err)
	}
	passkeyHandler.SetAdminHandler(adminHandler)
	telegramBindHandler := handler.NewTelegramBindingHandler(botBindingRepo, authRepo, cfg.TelegramBotUsername)

	// ── Queue + MCP handlers ─────────────────────────────────────────────────
	q := queue.New(cfg, cfg.RedisURL, redisClient != nil && redisClient.Available())

	mcpHandler, stopMCPAudit := handler.NewMCPHandler(mcpRepo, peerRepo, nodeRepo, metricsRepo, settingsRepo, hub, q)
	adminMCPHandler, stopAdminMCPAudit := handler.NewAdminMCPHandler(mcpRepo, peerRepo, nodeRepo, metricsRepo, settingsRepo, statsRepo, releaseRepo, botRepo, q)

	q.RegisterRequestLogHandler(metricsRepo)
	q.RegisterMCPAuditLogHandler(mcpRepo)

	lockInfo := handler.LockStatus{Enabled: true, Backend: "local", Available: false}
	if _, ok := locker.(*lock.RedisLocker); ok {
		lockInfo.Backend = "redis"
		lockInfo.Available = redisClient != nil && redisClient.Available()
	}

	systemStatusHandler := handler.NewSystemStatusHandler(db, bunDB, hub, c, cfg, redisClient, q, lockInfo, handler.BuildInfo{
		CommitHash: CommitHash,
		BuildDate:  BuildDate,
		Version:    Version,
	}, startedAt, centerKP)

	var inspector *queue.Inspector
	if q.Enabled() {
		inspector = queue.NewInspector(cfg.RedisURL)
	}
	queueMonitorHandler := handler.NewQueueMonitorHandler(q, inspector, cfg.AsynqReadonlyMonitor)

	// ── Cross-cutting notification wiring ────────────────────────────────────
	adminNotifyEmails := parseAdminNotifyEmails(cfg.AdminNotifyEmails)

	hub.SetNotifier(adminNotifyEmails,
		func(to, template string, vars map[string]interface{}) {
			emailSvc.SendRaw(to, template, vars)
		},
		func(key string) string {
			return adminHandler.GetSettingValue(key)
		},
		func(key string) string {
			return adminHandler.GetSettingThreshold(key)
		},
	)

	emailLevelFn := func(asn int64) int {
		return handler.GetEmailLevelForASN(authRepo, asn)
	}
	notificationAllowedFn := func(asn int64, key string) bool {
		return handler.IsNotificationEnabledForASN(authRepo, asn, key)
	}
	telegramNotificationAllowedFn := func(asn int64, key string) bool {
		return handler.IsTelegramNotificationEnabledForASN(authRepo, asn, key)
	}
	emailSvc.SetEmailLevelFn(emailLevelFn)
	emailSvc.SetNotificationAllowedFn(notificationAllowedFn)
	hub.SetEmailLevelFn(emailLevelFn)
	hub.SetNotificationAllowedFn(notificationAllowedFn)
	hub.SetTelegramNotificationAllowedFn(telegramNotificationAllowedFn)

	hub.SetASNEmailLookup(func(asn int64) (string, error) {
		return registry.LookupASNEmail(asn)
	})
	hub.SetVerifyCodeSender(func(to string, asn int64, code string) error {
		return emailSvc.SendVerificationCode(to, asn, code)
	})
	hub.SetLoginCodeStore(
		func(asn int64, email, codeHash string, expiresAt time.Time) error {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return authRepo.CreateLoginCode(ctx, &model.UserLoginCode{
				ASN:       asn,
				Email:     email,
				CodeHash:  codeHash,
				ExpiresAt: expiresAt,
			})
		},
		func(ctx context.Context, asn int64) (*model.UserLoginCode, error) {
			return authRepo.GetLatestValidLoginCode(ctx, asn)
		},
		func(ctx context.Context, id string) (bool, error) {
			return authRepo.MarkLoginCodeUsed(ctx, id)
		},
		func(ctx context.Context, id string) error {
			return authRepo.IncrementLoginCodeFailures(ctx, id)
		},
	)

	// ── Latency checker ───────────────────────────────────────────────────────
	latencyChecker := latency.NewChecker(repository.NewLatencyRepository(bunDB), peerRepo, c)
	latencyChecker.SetNotifier(adminNotifyEmails,
		func(to, template string, vars map[string]interface{}) {
			emailSvc.SendRaw(to, template, vars)
		},
		func(key string) string {
			return adminHandler.GetSettingValue(key)
		},
		func(key string) string {
			return adminHandler.GetSettingThreshold(key)
		},
	)
	latencyChecker.SetEmailLevelFn(emailLevelFn)
	latencyChecker.SetNotificationAllowedFn(notificationAllowedFn)
	latencyChecker.SetBotNotifierFn(func(asn int64, notifKey string, event string, data map[string]interface{}) {
		hub.NotifyPeerBot(asn, notifKey, event, data)
	})

	// ── Endpoint checker ──────────────────────────────────────────────────────
	endpointChecker := endpoint.NewChecker(peerRepo, hub, c)
	endpointChecker.SetNotifier(
		func(to, template string, vars map[string]interface{}) {
			emailSvc.SendRaw(to, template, vars)
		},
		func(key string) string {
			return adminHandler.GetSiteSettingValue(key)
		},
	)
	endpointChecker.SetEmailLevelFn(emailLevelFn)
	endpointChecker.SetNotificationAllowedFn(notificationAllowedFn)
	endpointChecker.SetBotNotifierFn(func(asn int64, notifKey string, event string, data map[string]interface{}) {
		hub.NotifyPeerBot(asn, notifKey, event, data)
	})

	// ── Queue handler registration ────────────────────────────────────────────
	q.RegisterCleanupHandler(cleanupRepo, nodeRepo)
	q.RegisterLatencyCheckHandler(
		func(ctx context.Context, q *queue.Queue) error {
			return latencyChecker.CheckAll(ctx, q)
		},
		func(ctx context.Context, payload []byte) error {
			return latencyChecker.CheckPeerFromPayload(ctx, payload)
		},
	)
	q.RegisterEndpointCheckHandler(endpointChecker.CheckAll)

	return &AppDeps{
		Cfg:         cfg,
		TrustedNets: trustedNets,

		AuthRepo:    authRepo,
		MetricsRepo: metricsRepo,
		MCPRepo:     mcpRepo,
		NodeRepo:    nodeRepo,
		PeerRepo:    peerRepo,
		CleanupRepo: cleanupRepo,

		AuthHandler:         authHandler,
		NodeHandler:         nodeHandler,
		PeerHandler:         peerHandler,
		LookingGlassHandler: lookingGlassHandler,
		AdminHandler:        adminHandler,
		AgentHandler:        agentHandler,
		BotHandler:          botHandler,
		RegistryHandler:     registryHandler,
		StatsHandler:        statsHandler,
		MCPKeyHandler:       mcpKeyHandler,
		AdminMCPKeyHandler:  adminMCPKeyHandler,
		MCPHandler:          mcpHandler,
		AdminMCPHandler:     adminMCPHandler,
		EmailPrefHandler:    emailPrefHandler,
		PasskeyHandler:      passkeyHandler,
		TelegramBindHandler: telegramBindHandler,
		SystemStatusHandler: systemStatusHandler,
		QueueMonitorHandler: queueMonitorHandler,

		Hub:             hub,
		Q:               q,
		Inspector:       inspector,
		AuditSvc:        auditSvc,
		Locker:          locker,
		LatencyChecker:  latencyChecker,
		EndpointChecker: endpointChecker,

		StopMCPAudit:      stopMCPAudit,
		StopAdminMCPAudit: stopAdminMCPAudit,
	}, nil
}

func parseAdminNotifyEmails(raw string) []string {
	if raw == "" {
		return []string{}
	}
	parts := strings.Split(raw, ",")
	result := make([]string, len(parts))
	for i, p := range parts {
		result[i] = strings.TrimSpace(p)
	}
	return result
}
