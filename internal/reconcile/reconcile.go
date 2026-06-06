// Package reconcile keeps each online agent's WireGuard/BIRD state in sync with
// the center database. State can diverge when a center→agent command succeeds
// but the follow-up DB write fails (or vice versa), or when an agent is offline
// during a peer transition and so never receives the add/remove. Previously such
// divergence required manual cleanup — the peer handlers literally logged
// "manual cleanup required". This worker makes convergence automatic.
//
// Mechanism: the center periodically pushes each online node its full set of
// active peers (the same payload the agent requests on reconnect). The agent's
// peers.sync handler then converges itself — recreating any missing
// WireGuard/BIRD config and removing peers that are no longer active (stale or
// orphaned). The database is the single source of truth; the agent is made to
// match it. Manually-configured peers are unaffected because the agent only
// tears down peers it tracks as autopeer-managed.
package reconcile

import (
	"context"
	"time"

	"github.com/akaere/autopeer-center/internal/lock"
	"github.com/akaere/autopeer-center/internal/repository"
	"github.com/akaere/autopeer-center/internal/service"
	"github.com/akaere/autopeer-center/internal/ws"
	"github.com/sirupsen/logrus"
)

var log = logrus.WithField("pkg", "reconcile")

const (
	// lockKey ensures only one center instance runs a reconcile pass at a time.
	lockKey = "reconcile:run"
	// lockTTL bounds a single pass; renewed via lock.WithRenewal.
	lockTTL = 5 * time.Minute
	// startupDelay lets agents (re)connect before the first pass.
	startupDelay = 90 * time.Second
)

// Worker periodically reconciles agent state with the database.
type Worker struct {
	hub      *ws.Hub
	nodes    repository.NodeRepository
	audit    *service.AuditService
	locker   lock.Locker
	interval time.Duration
}

// Start launches the reconcile loop until ctx is cancelled. It is a no-op (with
// a log line) when interval <= 0.
//
// The peers repository is accepted for signature stability/testing but the
// per-node active list is built inside the hub (TriggerPeersSync), so it is not
// used directly here.
func Start(ctx context.Context, hub *ws.Hub, _ repository.PeerRepository, nodes repository.NodeRepository, audit *service.AuditService, locker lock.Locker, interval time.Duration) {
	if interval <= 0 {
		log.Info("reconcile worker disabled (interval <= 0)")
		return
	}
	w := &Worker{hub: hub, nodes: nodes, audit: audit, locker: locker, interval: interval}
	go w.run(ctx)
}

func (w *Worker) run(ctx context.Context) {
	log.WithField("interval", w.interval.String()).Info("reconcile worker started")
	select {
	case <-ctx.Done():
		return
	case <-time.After(startupDelay):
	}
	w.tick(ctx)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.tick(ctx)
		}
	}
}

// tick runs a single reconcile pass under the distributed lock so only one
// center instance acts at a time.
func (w *Worker) tick(ctx context.Context) {
	if w.locker == nil {
		w.runPass(ctx)
		return
	}
	lease, ok, err := w.locker.Acquire(ctx, lockKey, lockTTL)
	if err != nil {
		log.WithError(err).Warn("reconcile: lock acquire failed; skipping pass")
		return
	}
	if !ok {
		log.Debug("reconcile: another instance holds the lock; skipping pass")
		return
	}
	defer lease.Release(context.Background())
	_ = lock.WithRenewal(ctx, lease, lockTTL, func(runCtx context.Context) {
		w.runPass(runCtx)
	})
}

func (w *Worker) runPass(ctx context.Context) {
	nodes, err := w.nodes.ListEnabled(ctx)
	if err != nil {
		log.WithError(err).Error("reconcile: failed to list nodes")
		return
	}
	synced := 0
	for _, node := range nodes {
		if ctx.Err() != nil {
			return
		}
		if !w.hub.TriggerPeersSync(node.ID) {
			// Offline or not yet authenticated — nothing to converge right now.
			continue
		}
		synced++
		log.WithFields(logrus.Fields{"node_id": node.ID, "node_name": node.Name}).Debug("reconcile: pushed active-peer sync")
		_ = w.audit.Log(context.Background(), "node.reconcile.sync", "system:reconcile", nil, map[string]interface{}{
			"node_id":   node.ID,
			"node_name": node.Name,
		})
	}
	log.WithFields(logrus.Fields{"nodes_synced": synced, "nodes_total": len(nodes)}).Info("reconcile pass complete")
}
