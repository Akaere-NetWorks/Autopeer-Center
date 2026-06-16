package inactivity

import (
	"context"
	"time"

	"github.com/akaere/autopeer-center/internal/lock"
	"github.com/akaere/autopeer-center/internal/repository"
	"github.com/akaere/autopeer-center/internal/service"
	"github.com/akaere/autopeer-center/internal/ws"
	"github.com/getsentry/sentry-go"
	"github.com/sirupsen/logrus"
)

var log = logrus.WithField("pkg", "inactivity")

type Config struct {
	Enabled        bool
	WarnFirstDays  int // 45
	WarnSecondDays int // 50
	DeleteDays     int // 60
	DryRun         bool
}

type Worker struct {
	peers         repository.PeerRepository
	hub           *ws.Hub
	audit         *service.AuditService
	locker        lock.Locker
	cfg           Config
	sendEmail     func(to, template string, vars map[string]interface{})
	getBotBinding func(ctx context.Context, asn int64) (int64, bool)
}

func NewWorker(peers repository.PeerRepository, hub *ws.Hub, audit *service.AuditService, locker lock.Locker, cfg Config) *Worker {
	if cfg.WarnFirstDays <= 0 {
		cfg.WarnFirstDays = 45
	}
	if cfg.WarnSecondDays <= 0 {
		cfg.WarnSecondDays = 50
	}
	if cfg.DeleteDays <= 0 {
		cfg.DeleteDays = 60
	}
	return &Worker{
		peers:  peers,
		hub:    hub,
		audit:  audit,
		locker: locker,
		cfg:    cfg,
	}
}

func (w *Worker) SetEmailSender(fn func(to, template string, vars map[string]interface{})) {
	w.sendEmail = fn
}

func (w *Worker) SetBotBindingLookup(fn func(ctx context.Context, asn int64) (int64, bool)) {
	w.getBotBinding = fn
}

// RunOnce executes a single sweep. Intended as the asynq handler entry point.
func (w *Worker) RunOnce(ctx context.Context) error {
	withLock(ctx, w.locker, "inactivity:run", time.Hour, func(context.Context) {
		w.sweep(ctx)
	})
	return nil
}

// Run starts the in-process goroutine loop (fallback when asynq is unavailable).
func (w *Worker) Run(ctx context.Context) {
	defer capturePanic("inactivity.sweep")
	log.Info("inactivity worker started")

	// Run once on entry, then every 24h.
	withLock(ctx, w.locker, "inactivity:run", time.Hour, func(context.Context) {
		w.sweep(ctx)
	})
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Info("inactivity worker stopped")
			return
		case <-ticker.C:
			withLock(ctx, w.locker, "inactivity:run", time.Hour, func(context.Context) {
				w.sweep(ctx)
			})
		}
	}
}

func (w *Worker) sweep(ctx context.Context) {
	if !w.cfg.Enabled {
		log.Debug("inactivity sweep disabled")
		return
	}

	peers, err := w.peers.ListInactiveActivePeers(ctx)
	if err != nil {
		log.WithError(err).Error("list inactive active peers failed")
		return
	}

	for _, peer := range peers {
		days := peer.DaysInactive
		pLog := log.WithFields(logrus.Fields{
			"peer_id": peer.PeerID,
			"asn":     peer.RemoteASN,
			"days":    days,
			"stage":   peer.Stage,
		})

		switch {
		case days >= float64(w.cfg.DeleteDays):
			// Only send the final warning if we haven't already reached stage 3.
			if peer.Stage < 3 {
				pLog.Info("peer past delete threshold, sending final warning")
				w.sendWarning(ctx, peer, 60)
				if !w.cfg.DryRun {
					if err := w.peers.SetInactivityWarningStage(ctx, peer.PeerID, 3); err != nil {
						pLog.WithError(err).Error("set inactivity warning stage to 3 failed")
					}
				}
			}
			pLog.Info("deleting peer for inactivity")
			w.deletePeer(ctx, peer)

		case days >= float64(w.cfg.WarnSecondDays):
			if peer.Stage < 2 {
				pLog.Info("sending 50-day inactivity warning")
				w.sendWarning(ctx, peer, 50)
				if !w.cfg.DryRun {
					if err := w.peers.SetInactivityWarningStage(ctx, peer.PeerID, 2); err != nil {
						pLog.WithError(err).Error("set inactivity warning stage to 2 failed")
					}
				}
			}

		case days >= float64(w.cfg.WarnFirstDays):
			if peer.Stage < 1 {
				pLog.Info("sending 45-day inactivity warning")
				w.sendWarning(ctx, peer, 45)
				if !w.cfg.DryRun {
					if err := w.peers.SetInactivityWarningStage(ctx, peer.PeerID, 1); err != nil {
						pLog.WithError(err).Error("set inactivity warning stage to 1 failed")
					}
				}
			}
		}
	}
}

func (w *Worker) sendWarning(ctx context.Context, peer repository.InactivePeer, level int) {
	vars := map[string]interface{}{
		"asn":          peer.RemoteASN,
		"nodeName":     peer.NodeName,
		"daysInactive": int(peer.DaysInactive),
		"level":        level,
	}

	pLog := log.WithFields(logrus.Fields{
		"peer_id": peer.PeerID,
		"asn":     peer.RemoteASN,
		"level":   level,
	})

	// Mandatory email (bypasses all preference gates).
	if w.sendEmail != nil && peer.ContactEmail != "" {
		if w.cfg.DryRun {
			pLog.WithField("to", peer.ContactEmail).Info("dry-run: would send inactivity warning email")
		} else {
			pLog.WithField("to", peer.ContactEmail).Info("sending inactivity warning email")
			w.sendEmail(peer.ContactEmail, "peer-inactive-warning", vars)
		}
	}

	// Mandatory Telegram notification (bypasses preference gates).
	if w.getBotBinding != nil {
		tgUserID, ok := w.getBotBinding(ctx, peer.RemoteASN)
		if ok {
			if w.cfg.DryRun {
				pLog.WithField("tg_user_id", tgUserID).Info("dry-run: would send inactivity warning telegram")
			} else {
				pLog.WithField("tg_user_id", tgUserID).Info("sending inactivity warning telegram")
				w.hub.NotifyBotUser(tgUserID, "peer.inactive_warning", vars)
			}
		}
	}
}

func (w *Worker) deletePeer(ctx context.Context, peer repository.InactivePeer) {
	pLog := log.WithFields(logrus.Fields{
		"peer_id": peer.PeerID,
		"asn":     peer.RemoteASN,
	})

	if w.cfg.DryRun {
		pLog.Info("dry-run: would delete peer for inactivity")
		return
	}

	// Guarded delete: only removes the row if the peer is still at the final
	// warning stage. MarkPeersActive resets the stage to 0 on a fresh handshake,
	// so a peer that re-activated between the sweep snapshot and here — or while
	// earlier peers in this loop were being processed — is left intact and its
	// tunnel is NOT torn down. Deleting before the agent teardown avoids sending
	// peer.remove for a peer that is live again.
	deleted, err := w.peers.DeleteInactive(ctx, peer.PeerID)
	if err != nil {
		pLog.WithError(err).Error("delete inactive peer failed")
		return
	}
	if !deleted {
		pLog.Info("peer re-activated before deletion; skipping")
		return
	}

	// Best-effort agent teardown. DB is the source of truth; the reconcile worker
	// converges the agent's config to match, so a failure here is non-fatal.
	if _, err := w.hub.SendCommand(peer.NodeID, ws.TypePeerRemove, ws.PeerRemovePayload{
		PeerID: peer.PeerID,
		ASN:    peer.RemoteASN,
	}); err != nil {
		pLog.WithError(err).Warn("agent peer remove command failed (reconcile will converge)")
	}
	pLog.Info("peer deleted for inactivity")

	w.audit.Log(ctx, "peer.inactivity_delete", "system:inactivity_sweep", &peer.PeerID, map[string]interface{}{
		"asn":          peer.RemoteASN,
		"nodeName":     peer.NodeName,
		"daysInactive": int(peer.DaysInactive),
	})

	w.hub.ClearPeerAlerts(peer.PeerID)
}

// withLock acquires the named lock and executes fn. With a nil locker (single
// instance) fn runs directly. On acquire failure or when another instance holds
// the lock, the pass is SKIPPED (not run unprotected): this sweep is destructive
// (deletes peers, sends mandatory emails) so double-execution must be avoided.
func withLock(ctx context.Context, locker lock.Locker, key string, ttl time.Duration, fn func(context.Context)) {
	if locker == nil {
		// No distributed lock infrastructure (single-instance fallback); run directly.
		fn(ctx)
		return
	}
	lease, ok, err := locker.Acquire(ctx, key, ttl)
	if err != nil {
		// Do NOT fall back to running: this sweep deletes peers and sends
		// mandatory emails, so a Redis blip that makes every replica fail
		// Acquire would double-execute (duplicate emails / duplicate deletes).
		// Skip this pass; the next daily tick retries.
		log.WithError(err).WithField("lock", key).Warn("failed to acquire inactivity lock; skipping sweep")
		return
	}
	if !ok {
		log.WithField("lock", key).Debug("inactivity sweep already running on another instance; skipping")
		return
	}
	defer lease.Release(context.Background())
	if err := lock.WithRenewal(ctx, lease, ttl, fn); err != nil {
		log.WithError(err).WithField("lock", key).Warn("inactivity lock renewal failed")
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
		// Do not re-panic: this runs in a background goroutine, and re-panicking
		// would crash the whole center process (HTTP API, WS hub, all workers).
		// The Sentry report above is sufficient for observability.
	}
}
