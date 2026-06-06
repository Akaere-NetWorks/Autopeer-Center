package handler

import (
	"context"
	"time"

	"github.com/akaere/autopeer-center/internal/lock"
)

const peerLockTTL = 30 * time.Second

// acquirePeerLock takes a short-lived distributed lock for a peer mutation.
// It returns a release func and whether the key is currently contended (busy).
// On locker error it fails open (proceeds without the lock) so a Redis outage
// does not block admin/user operations; a genuinely held lock returns busy=true.
//
// Callers should treat busy=true as a transient 409 ("operation in progress")
// and always defer release().
func acquirePeerLock(ctx context.Context, locker lock.Locker, key string) (release func(), busy bool) {
	noop := func() {}
	if locker == nil {
		return noop, false
	}
	lease, ok, err := locker.Acquire(ctx, key, peerLockTTL)
	if err != nil {
		peerLog.WithError(err).WithField("key", key).Warn("peer lock acquire failed; proceeding without lock")
		return noop, false
	}
	if !ok {
		return noop, true
	}
	return func() { _ = lease.Release(context.Background()) }, false
}
