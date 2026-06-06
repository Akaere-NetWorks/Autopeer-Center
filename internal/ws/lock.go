package ws

import (
	"context"
	"time"
)

const peerLockTTL = 30 * time.Second

// acquirePeerLock takes a short-lived distributed lock for a peer mutation.
// It returns a release func and whether the key is currently contended (busy).
// On locker error it fails open (proceeds without the lock) so a Redis outage
// does not block bot operations; a genuinely held lock returns busy=true.
func (h *Hub) acquirePeerLock(ctx context.Context, key string) (release func(), busy bool) {
	noop := func() {}
	if h.locker == nil {
		return noop, false
	}
	lease, ok, err := h.locker.Acquire(ctx, key, peerLockTTL)
	if err != nil {
		log.WithError(err).WithField("key", key).Warn("peer lock acquire failed; proceeding without lock")
		return noop, false
	}
	if !ok {
		return noop, true
	}
	return func() { _ = lease.Release(context.Background()) }, false
}
