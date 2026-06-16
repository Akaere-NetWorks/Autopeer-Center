package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/akaere/autopeer-center/internal/cache"
	"github.com/akaere/autopeer-center/internal/cleanup"
	"github.com/akaere/autopeer-center/internal/clickhouse"
	"github.com/akaere/autopeer-center/internal/config"
	"github.com/akaere/autopeer-center/internal/crypto"
	"github.com/akaere/autopeer-center/internal/database"
	"github.com/akaere/autopeer-center/internal/lock"
	"github.com/akaere/autopeer-center/internal/middleware"
	"github.com/akaere/autopeer-center/internal/reconcile"
	"github.com/akaere/autopeer-center/internal/repository"
	"github.com/akaere/autopeer-center/internal/redisx"
	chdriver "github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/getsentry/sentry-go"
	sentryhttp "github.com/getsentry/sentry-go/http"
	"github.com/go-chi/chi/v5"
	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
)

var (
	log        = logrus.WithField("pkg", "main")
	startedAt  = time.Now()
	CommitHash = "dev"
	BuildDate  = "unknown"
	Version    = "dev"
)

func main() {
	_ = godotenv.Load()

	cfg, err := config.Load()
	if err != nil {
		log.WithError(err).Fatal("failed to load config")
	}
	sentryEnabled := setupSentry(cfg)
	if sentryEnabled {
		defer sentry.Flush(2 * time.Second)
	}

	log.WithFields(logrus.Fields{
		"listen_addr": cfg.ListenAddr,
		"cors_origin": cfg.CORSOrigin,
		"email_api":   cfg.EmailAPIURL,
	}).Debug("config loaded")

	ctx := context.Background()
	startupTransaction := sentry.StartTransaction(ctx, "center.startup", sentry.WithTransactionSource(sentry.SourceTask))
	ctx = startupTransaction.Context()
	defer startupTransaction.Finish()

	// ── Database ──────────────────────────────────────────────────────────────
	dbSpan := sentry.StartSpan(ctx, "db.connect")
	db, err := database.Connect(dbSpan.Context(), cfg.DatabaseURL)
	if err != nil {
		dbSpan.Status = sentry.SpanStatusInternalError
		dbSpan.Finish()
		log.WithError(err).Fatal("failed to connect to database")
	}
	dbSpan.Status = sentry.SpanStatusOK
	dbSpan.Finish()
	defer db.Close()

	log.Debug("database connected")
	runMigrations(ctx, db)

	if sentryEnabled && cfg.SentryEnableMetrics {
		dbMetricsCtx, dbMetricsCancel := context.WithCancel(ctx)
		defer dbMetricsCancel()
		goWithSentry("sentry.db_pool_metrics", func() {
			reportDBPoolMetrics(dbMetricsCtx, db)
		})
	}

	// ── BunDB + admin bootstrap ───────────────────────────────────────────────
	bunDB := database.NewBunDB(db)
	adminRepo := repository.NewAdminRepository(bunDB)
	bootstrapAdmin(ctx, adminRepo, cfg)

	// ── Redis + cache + distributed lock ─────────────────────────────────────
	redisClient, err := redisx.New(cfg.RedisURL)
	if err != nil {
		if cfg.RedisRequired {
			log.WithError(err).Fatal("failed to connect to required redis")
		}
		log.WithError(err).Warn("redis unavailable, using local fallback")
	}
	if redisClient != nil && redisClient.Enabled() {
		defer redisClient.Close()
	}
	var redisRaw *redis.Client
	if redisClient != nil {
		redisRaw = redisClient.Client()
	}

	var locker lock.Locker = lock.NewLocalLocker()
	if redisClient != nil && redisClient.Available() {
		redisRaw = redisClient.Client()
		locker = lock.NewRedisLocker(redisRaw, cfg.RedisKeyPrefix)
	} else if cfg.RedisURL != "" {
		log.Warn("redis distributed locks unavailable; using process-local lock fallback; singleton jobs are not guaranteed across replicas")
	}

	c, err := cache.OpenWithConfig(cfg.CacheDir, cfg, redisRaw)
	if err != nil {
		log.WithError(err).Fatal("failed to open cache")
	}
	defer c.Close()

	// ── ClickHouse (optional: traffic analytics) ──────────────────────────────
	// Mirrors the optional-Redis pattern: unset → feature disabled; configured
	// but unreachable → fatal when CLICKHOUSE_REQUIRED, else warn-and-disable.
	var chConn chdriver.Conn
	if cfg.ClickHouseURL == "" {
		log.Info("traffic analytics disabled (CLICKHOUSE_URL unset)")
	} else if chConn, err = clickhouse.Connect(ctx, cfg.ClickHouseURL); err != nil {
		if cfg.ClickHouseRequired {
			log.WithError(err).Fatal("failed to connect to required clickhouse")
		}
		log.WithError(err).Warn("clickhouse unavailable, traffic analytics disabled")
		chConn = nil
	} else if err = clickhouse.EnsureSchema(ctx, chConn); err != nil {
		if cfg.ClickHouseRequired {
			log.WithError(err).Fatal("failed to ensure required clickhouse schema")
		}
		log.WithError(err).Warn("clickhouse schema setup failed, traffic analytics disabled")
		_ = chConn.Close()
		chConn = nil
	}
	if chConn != nil {
		defer chConn.Close()
	}

	// ── Center key pair ───────────────────────────────────────────────────────
	centerKP, err := loadOrGenerateCenterKeyPair(cfg.CenterKeyPath)
	if err != nil {
		log.WithError(err).Fatal("failed to load/generate center key pair")
	}
	log.WithField("pubkey_prefix", crypto.PubKeyHex(centerKP.PublicKey)[:16]).Info("center key pair ready")

	// ── Dependency wiring ─────────────────────────────────────────────────────
	deps, err := BuildDeps(ctx, cfg, db, bunDB, redisClient, c, locker, centerKP, chConn)
	if err != nil {
		log.WithError(err).Fatal("failed to initialize dependencies")
	}
	log.Debug("services initialized")

	// ── Router ────────────────────────────────────────────────────────────────
	r := chi.NewRouter()
	setupRoutes(r, deps)

	// ── Queue lifecycle ───────────────────────────────────────────────────────
	if err := deps.Q.Start(ctx); err != nil {
		log.WithError(err).Warn("failed to start queue server")
	}
	deps.Q.InitScheduler(cfg.RedisURL)
	deps.Q.RegisterPeriodicTasks()
	deps.Q.EnqueueInitialTick()
	go func() {
		if err := deps.Q.StartScheduler(); err != nil {
			log.WithError(err).Warn("asynq scheduler failed")
		}
	}()

	// Fallback: run in-process schedulers when Asynq is not available
	if !deps.Q.Enabled() {
		log.Warn("Asynq disabled — running in-process schedulers as fallback")

		cleanupCtx, cleanupCancel := context.WithCancel(ctx)
		defer cleanupCancel()
		cleanup.StartWithLocker(cleanupCtx, deps.CleanupRepo, deps.NodeRepo, deps.Locker)

		latCtx, latCancel := context.WithCancel(ctx)
		defer latCancel()
		goWithSentry("latency_checker", func() {
			deps.LatencyChecker.Run(latCtx)
		})

		epCtx, epCancel := context.WithCancel(ctx)
		defer epCancel()
		goWithSentry("endpoint_checker", func() {
			deps.EndpointChecker.Run(epCtx)
		})

		if deps.Cfg.InactivitySweepEnabled {
			inactCtx, inactCancel := context.WithCancel(ctx)
			defer inactCancel()
			goWithSentry("inactivity_sweep", func() {
				deps.InactivityWorker.Run(inactCtx)
			})
		}

	}

	// ── Reconcile worker ──────────────────────────────────────────────────────
	// Runs independently of Asynq; the distributed locker makes it safe across
	// multiple center instances.
	if cfg.ReconcileEnabled {
		reconcileCtx, reconcileCancel := context.WithCancel(ctx)
		defer reconcileCancel()
		reconcile.Start(reconcileCtx, deps.Hub, deps.PeerRepo, deps.NodeRepo, deps.AuditSvc, deps.Locker, cfg.ReconcileInterval)
	} else {
		log.Info("reconcile worker disabled via RECONCILE_ENABLED=false")
	}

	// ── HTTP server ───────────────────────────────────────────────────────────
	routerHandler := http.Handler(r)
	serverHandler := routerHandler
	if sentryEnabled {
		sentryHandler := sentryhttp.New(sentryhttp.Options{Repanic: true})
		serverHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if middleware.IsLongLivedRequest(r) {
				routerHandler.ServeHTTP(w, r)
				return
			}
			sentryHandler.Handle(routerHandler).ServeHTTP(w, r)
		})
	}

	srv := &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      serverHandler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 0, // 0 = no timeout; SSE connections require long-lived writes
		IdleTimeout:  60 * time.Second,
	}

	goWithSentry("http_server", func() {
		log.WithField("addr", cfg.ListenAddr).Info("center API listening")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			sentry.CaptureException(err)
			sentry.Flush(2 * time.Second)
			log.WithError(err).Fatal("server error")
		}
	})

	// ── Graceful shutdown ─────────────────────────────────────────────────────
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Info("shutting down")

	deps.Hub.Shutdown()
	deps.FlapHub.Shutdown()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			// Long-lived SSE/stream connections (flap/atlas/MCP) don't drain within
			// the grace period; force them closed. This is expected on shutdown, not
			// an error condition — don't report it to Sentry.
			log.Warn("graceful shutdown deadline exceeded; forcing connection close")
			_ = srv.Close()
		} else {
			sentry.CaptureException(err)
			log.WithError(err).Warn("server shutdown failed")
		}
	}
	if deps.Inspector != nil {
		deps.Inspector.Close()
	}
	if err := deps.Q.Shutdown(shutdownCtx); err != nil {
		log.WithError(err).Warn("queue shutdown failed")
	}
	deps.StopMCPAudit()
	deps.StopAdminMCPAudit()
}
