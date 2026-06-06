package lock

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

type Lease interface {
	Refresh(ctx context.Context, ttl time.Duration) error
	Release(ctx context.Context) error
}

type Locker interface {
	Acquire(ctx context.Context, key string, ttl time.Duration) (Lease, bool, error)
}

func WithRenewal(ctx context.Context, lease Lease, ttl time.Duration, fn func(context.Context)) error {
	if lease == nil {
		fn(ctx)
		return nil
	}
	if ttl <= 0 {
		fn(ctx)
		return nil
	}
	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()
	renewEvery := ttl / 2
	if renewEvery < time.Second {
		renewEvery = time.Second
	}
	done := make(chan struct{})
	errCh := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(renewEvery)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-runCtx.Done():
				return
			case <-ticker.C:
				refreshCtx, cancel := context.WithTimeout(runCtx, 2*time.Second)
				err := lease.Refresh(refreshCtx, ttl)
				cancel()
				if err != nil {
					runCancel()
					select {
					case errCh <- err:
					default:
					}
					return
				}
			}
		}
	}()
	fn(runCtx)
	close(done)
	select {
	case err := <-errCh:
		return err
	default:
		return nil
	}
}

type RedisLocker struct {
	client *redis.Client
	prefix string
}

func NewRedisLocker(client *redis.Client, prefix string) *RedisLocker {
	return &RedisLocker{client: client, prefix: prefix}
}

func (l *RedisLocker) Acquire(ctx context.Context, key string, ttl time.Duration) (Lease, bool, error) {
	if l == nil || l.client == nil {
		return nil, false, fmt.Errorf("redis locker not configured")
	}
	token, err := randomToken()
	if err != nil {
		return nil, false, err
	}
	fullKey := l.prefix + "lock:" + key
	ok, err := l.client.SetNX(ctx, fullKey, token, ttl).Result()
	if err != nil || !ok {
		return nil, ok, err
	}
	return &redisLease{client: l.client, key: fullKey, token: token}, true, nil
}

type redisLease struct {
	client *redis.Client
	key    string
	token  string
}

var releaseScript = redis.NewScript(`if redis.call("GET", KEYS[1]) == ARGV[1] then return redis.call("DEL", KEYS[1]) else return 0 end`)
var refreshScript = redis.NewScript(`if redis.call("GET", KEYS[1]) == ARGV[1] then return redis.call("PEXPIRE", KEYS[1], ARGV[2]) else return 0 end`)

func (l *redisLease) Refresh(ctx context.Context, ttl time.Duration) error {
	res, err := refreshScript.Run(ctx, l.client, []string{l.key}, l.token, ttl.Milliseconds()).Int()
	if err != nil {
		return err
	}
	if res != 1 {
		return fmt.Errorf("lock lease lost")
	}
	return nil
}

func (l *redisLease) Release(ctx context.Context) error {
	return releaseScript.Run(ctx, l.client, []string{l.key}, l.token).Err()
}

type LocalLocker struct {
	mu    sync.Mutex
	locks map[string]bool
}

func NewLocalLocker() *LocalLocker { return &LocalLocker{locks: map[string]bool{}} }

func (l *LocalLocker) Acquire(ctx context.Context, key string, ttl time.Duration) (Lease, bool, error) {
	if l == nil {
		l = NewLocalLocker()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.locks == nil {
		l.locks = map[string]bool{}
	}
	if l.locks[key] {
		return nil, false, nil
	}
	l.locks[key] = true
	return &localLease{locker: l, key: key}, true, nil
}

type localLease struct {
	locker *LocalLocker
	key    string
}

func (l *localLease) Refresh(context.Context, time.Duration) error { return nil }
func (l *localLease) Release(context.Context) error {
	l.locker.mu.Lock()
	delete(l.locker.locks, l.key)
	l.locker.mu.Unlock()
	return nil
}

func randomToken() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
