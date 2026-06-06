package cleanup

import (
	"context"
	"time"

	"github.com/akaere/autopeer-center/internal/lock"
	"github.com/akaere/autopeer-center/internal/repository"
	"github.com/getsentry/sentry-go"
	"github.com/sirupsen/logrus"
)

var log = logrus.WithField("pkg", "cleanup")

func Start(ctx context.Context, cleanup repository.CleanupRepository, nodes repository.NodeRepository) {
	StartWithLocker(ctx, cleanup, nodes, nil)
}

func StartWithLocker(ctx context.Context, cleanup repository.CleanupRepository, nodes repository.NodeRepository, locker lock.Locker) {
	go func() {
		defer capturePanic("cleanup.timeout_updating")
		log.Debug("starting timeout-updating goroutine")
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				log.Info("timeout-updating goroutine stopped")
				return
			case <-ticker.C:
				withLock(ctx, locker, "cleanup:timeout-updating", 25*time.Second, func(context.Context) { timeoutUpdating(nodes) })
			}
		}
	}()

	go func() {
		defer capturePanic("cleanup.daily")
		log.Debug("starting daily cleanup goroutine")
		withLock(ctx, locker, "cleanup:daily", time.Hour, func(context.Context) { runCleanup(cleanup) })
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				log.Info("daily cleanup goroutine stopped")
				return
			case <-ticker.C:
				withLock(ctx, locker, "cleanup:daily", time.Hour, func(context.Context) { runCleanup(cleanup) })
			}
		}
	}()
}

func withLock(ctx context.Context, locker lock.Locker, key string, ttl time.Duration, fn func(context.Context)) {
	if locker == nil {
		fn(ctx)
		return
	}
	lease, ok, err := locker.Acquire(ctx, key, ttl)
	if err != nil || !ok {
		if err != nil {
			log.WithError(err).WithField("lock", key).Warn("failed to acquire cleanup lock")
			fn(ctx)
		}
		return
	}
	defer lease.Release(context.Background())
	if err := lock.WithRenewal(ctx, lease, ttl, fn); err != nil {
		log.WithError(err).WithField("lock", key).Warn("cleanup lock renewal failed")
	}
}

func capturePanic(name string) {
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
}

func timeoutUpdating(nodes repository.NodeRepository) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	log.WithField("op", "MarkStaleUpdating").Debug("executing timeout update")
	if err := nodes.MarkStaleUpdating(ctx); err != nil {
		log.WithError(err).Error("cleanup updating timeout error")
	}
}

func runCleanup(cleanup repository.CleanupRepository) {
	execCleanup := func(label string, fn func(context.Context) (int, error)) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		log.WithField("op", label).Debug("executing cleanup")
		n, err := fn(ctx)
		if err != nil {
			log.WithError(err).Errorf("cleanup %s error", label)
		} else {
			log.WithField("rows", n).Infof("cleanup %s completed", label)
		}
	}

	execCleanup("audit_logs", cleanup.DeleteExpiredAuditLogs)
	execCleanup("request_logs", cleanup.DeleteExpiredRequestLogs)
	execCleanup("user_login_codes", cleanup.DeleteExpiredLoginCodes)
	execCleanup("gpg_challenges", cleanup.DeleteExpiredGPGChallenges)
	execCleanup("bot_command_stats", cleanup.DeleteExpiredBotCommandStats)
	execCleanup("auth_sessions", cleanup.DeleteExpiredAuthSessions)
	execCleanup("webauthn_sessions", cleanup.DeleteExpiredWebAuthnSessions)
}
