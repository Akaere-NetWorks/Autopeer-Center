package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/akaere/autopeer-center/internal/config"
	"github.com/getsentry/sentry-go"
	"github.com/getsentry/sentry-go/attribute"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sirupsen/logrus"
)

func setupSentry(cfg *config.Config) bool {
	if strings.TrimSpace(cfg.SentryDSN) == "" {
		return false
	}

	release := strings.TrimSpace(cfg.SentryRelease)
	if release == "" {
		release = defaultSentryRelease()
	}

	if err := sentry.Init(sentry.ClientOptions{
		Dsn:              cfg.SentryDSN,
		Environment:      cfg.SentryEnvironment,
		Release:          release,
		Debug:            cfg.SentryDebug,
		AttachStacktrace: true,
		EnableTracing:    cfg.SentryTracesSampleRate > 0,
		TracesSampleRate: cfg.SentryTracesSampleRate,
		DisableMetrics:   !cfg.SentryEnableMetrics,
		SendDefaultPII:   false,
		Tags: map[string]string{
			"service": "autopeer-center",
		},
	}); err != nil {
		log.WithError(err).Warn("sentry initialization failed")
		return false
	}

	log.WithFields(logrus.Fields{
		"environment":        cfg.SentryEnvironment,
		"release":            release,
		"traces_sample_rate": cfg.SentryTracesSampleRate,
		"metrics_enabled":    cfg.SentryEnableMetrics,
	}).Info("sentry initialized")
	return true
}

func defaultSentryRelease() string {
	if Version != "" && Version != "dev" {
		if CommitHash != "" && CommitHash != "dev" {
			return fmt.Sprintf("%s+%s", Version, CommitHash)
		}
		return Version
	}
	if CommitHash != "" && CommitHash != "dev" {
		return CommitHash
	}
	return ""
}

func goWithSentry(name string, fn func()) {
	go func() {
		defer func() {
			if err := recover(); err != nil {
				hub := sentry.CurrentHub()
				hub.WithScope(func(scope *sentry.Scope) {
					scope.SetLevel(sentry.LevelFatal)
					scope.SetTag("goroutine", name)
					scope.SetContext("goroutine", sentry.Context{"name": name})
					hub.RecoverWithContext(context.Background(), err)
				})
				sentry.Flush(2 * time.Second)
				panic(err)
			}
		}()
		fn()
	}()
}

func reportDBPoolMetrics(ctx context.Context, db *pgxpool.Pool) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	record := func() {
		stat := db.Stat()
		meter := sentry.NewMeter(ctx)
		attrs := []attribute.Builder{attribute.String("pool", "center")}
		meter.Gauge("db.pool.acquired_conns", float64(stat.AcquiredConns()), sentry.WithAttributes(attrs...))
		meter.Gauge("db.pool.idle_conns", float64(stat.IdleConns()), sentry.WithAttributes(attrs...))
		meter.Gauge("db.pool.total_conns", float64(stat.TotalConns()), sentry.WithAttributes(attrs...))
		meter.Gauge("db.pool.max_conns", float64(stat.MaxConns()), sentry.WithAttributes(attrs...))
		meter.Gauge("db.pool.constructing_conns", float64(stat.ConstructingConns()), sentry.WithAttributes(attrs...))
	}

	record()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			record()
		}
	}
}
