package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/akaere/autopeer-center/internal/config"
	"github.com/redis/go-redis/v9"
	bolt "go.etcd.io/bbolt"
)

const (
	BucketAlerts    = "alerts"
	BucketRegistry  = "registry"
	BucketRateLimit = "rate_limit"
	BucketSettings  = "settings"
)

type Cache struct {
	db       *bolt.DB
	redis    *redis.Client
	prefix   string
	fallback bool
}

func Open(path string) (*Cache, error) { return openWithConfig(path, nil, nil) }
func OpenWithConfig(path string, cfg *config.Config, redisClient *redis.Client) (*Cache, error) {
	return openWithConfig(path, cfg, redisClient)
}
func openWithConfig(path string, cfg *config.Config, redisClient *redis.Client) (*Cache, error) {
	c := &Cache{fallback: true}
	if cfg != nil {
		c.prefix = cfg.RedisKeyPrefix
		c.fallback = cfg.CacheLocalFallback
	}
	c.redis = redisClient
	if c.redis != nil {
		if c.fallback {
			if err := removeStaleLocalCache(path); err != nil {
				return nil, err
			}
		}
		return c, nil
	}
	if c.redis == nil && !c.fallback {
		return nil, fmt.Errorf("cache has no backend: redis unavailable and local fallback disabled")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	db, err := bolt.Open(path, 0600, &bolt.Options{Timeout: 3 * time.Second})
	if err != nil {
		return nil, err
	}
	if err = db.Update(func(tx *bolt.Tx) error {
		for _, b := range []string{BucketAlerts, BucketRegistry, BucketRateLimit, BucketSettings} {
			if _, e := tx.CreateBucketIfNotExists([]byte(b)); e != nil {
				return e
			}
		}
		return nil
	}); err != nil {
		db.Close()
		return nil, err
	}
	c.db = db
	return c, nil
}

func removeStaleLocalCache(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.IsDir() {
		slog.Warn("cache path is a directory; skipping stale local cache removal", "path", path)
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
func (c *Cache) Close() error {
	if c == nil || c.db == nil {
		return nil
	}
	return c.db.Close()
}
func (c *Cache) Put(bucket, key string, value interface{}, ttl time.Duration) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	var redisErr error
	var redisOK bool
	if c.redis != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		redisErr = c.redis.Set(ctx, c.redisKey(bucket, key), data, ttl).Err()
		cancel()
		redisOK = redisErr == nil
	}
	var localErr error
	if c.fallback && c.db != nil {
		localErr = c.putLocal(bucket, key, value, data, ttl)
		if localErr != nil && redisOK {
			slog.Warn("failed to mirror cache value to local fallback", "bucket", bucket, "key", key, "error", localErr)
		}
	}
	if redisErr != nil && !c.fallback {
		return redisErr
	}
	if !redisOK && localErr != nil {
		return localErr
	}
	if c.redis == nil && (!c.fallback || c.db == nil) {
		return fmt.Errorf("cache has no available backend")
	}
	return nil
}
func (c *Cache) Get(bucket, key string, dest interface{}) (bool, error) {
	if c.redis != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		raw, err := c.redis.Get(ctx, c.redisKey(bucket, key)).Bytes()
		if err == nil {
			if json.Unmarshal(raw, dest) == nil {
				if c.fallback && c.db != nil {
					ttl, ttlErr := c.redis.PTTL(ctx, c.redisKey(bucket, key)).Result()
					if ttlErr == nil && ttl > 0 {
						_ = c.putLocalRaw(bucket, key, raw, ttl)
					}
				}
				cancel()
				return true, nil
			}
			cancel()
			return false, nil
		}
		cancel()
		if err == redis.Nil {
			return false, nil
		}
		if err != redis.Nil && !c.fallback {
			return false, err
		}
	}
	if c.db == nil {
		return false, nil
	}
	var found bool
	err := c.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return nil
		}
		raw := b.Get([]byte(key))
		if raw == nil {
			return nil
		}
		var env struct {
			Value     json.RawMessage `json:"v"`
			ExpiresAt int64           `json:"e"`
		}
		if json.Unmarshal(raw, &env) == nil && env.Value != nil {
			if env.ExpiresAt > 0 && time.Now().Unix() > env.ExpiresAt {
				return nil
			}
			found = true
			return json.Unmarshal(env.Value, dest)
		}
		if json.Unmarshal(raw, dest) == nil {
			found = true
		}
		return nil
	})
	return found, err
}
func (c *Cache) Delete(bucket, key string) error {
	var redisErr error
	if c.redis != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		redisErr = c.redis.Del(ctx, c.redisKey(bucket, key)).Err()
		cancel()
	}
	if c.db == nil {
		return redisErr
	}
	localErr := c.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return nil
		}
		return b.Delete([]byte(key))
	})
	if redisErr != nil {
		return redisErr
	}
	return localErr
}
func (c *Cache) deleteLocal(bucket, key string) error {
	return c.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return nil
		}
		return b.Delete([]byte(key))
	})
}
func (c *Cache) DeleteByPrefix(bucket, prefix string) error {
	var redisErr error
	if c.redis != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		pattern := c.redisKey(bucket, prefix) + "*"
		iter := c.redis.Scan(ctx, 0, pattern, 100).Iterator()
		var keys []string
		for iter.Next(ctx) {
			keys = append(keys, iter.Val())
			if len(keys) >= 1000 {
				if err := c.redis.Del(ctx, keys...).Err(); err != nil {
					redisErr = err
				}
				keys = keys[:0]
			}
		}
		if err := iter.Err(); err != nil {
			redisErr = err
		}
		if len(keys) > 0 {
			if err := c.redis.Del(ctx, keys...).Err(); err != nil {
				redisErr = err
			}
		}
		cancel()
	}
	if c.db == nil {
		return redisErr
	}
	localErr := c.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return nil
		}
		cur := b.Cursor()
		for k, _ := cur.First(); k != nil; k, _ = cur.Next() {
			if strings.HasPrefix(string(k), prefix) {
				_ = b.Delete(k)
			}
		}
		return nil
	})
	if redisErr != nil {
		return redisErr
	}
	return localErr
}
func (c *Cache) deleteLocalByPrefix(bucket, prefix string) error {
	return c.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return nil
		}
		cur := b.Cursor()
		for k, _ := cur.First(); k != nil; k, _ = cur.Next() {
			if strings.HasPrefix(string(k), prefix) {
				_ = b.Delete(k)
			}
		}
		return nil
	})
}
func (c *Cache) GetInt(bucket, key string) (int, bool) {
	if c.redis != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		raw, err := c.redis.Get(ctx, c.redisKey(bucket, key)).Result()
		cancel()
		if err == nil {
			var direct int
			if _, scanErr := fmt.Sscanf(raw, "%d", &direct); scanErr == nil {
				return direct, true
			}
			var decoded int
			if json.Unmarshal([]byte(raw), &decoded) == nil {
				return decoded, true
			}
			return 0, false
		}
		if err == redis.Nil || !c.fallback {
			return 0, false
		}
	}
	var v int
	if c.db == nil {
		return 0, false
	}
	ok, _ := c.getLocal(bucket, key, &v)
	return v, ok
}
func (c *Cache) IncInt(bucket, key string, ttl time.Duration) (int, error) {
	if c.redis != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		val, err := c.redis.Eval(ctx, incrScript, []string{c.redisKey(bucket, key)}, ttl.Milliseconds()).Int()
		cancel()
		if err == nil {
			if c.fallback && c.db != nil {
				_ = c.putLocal(bucket, key, val, mustJSON(val), ttl)
			}
			return val, nil
		}
		if !c.fallback {
			return 0, err
		}
	}
	v := 0
	if c.db == nil {
		return 0, fmt.Errorf("cache has no available backend")
	}
	err := c.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return fmt.Errorf("bucket %q not found", bucket)
		}
		raw := b.Get([]byte(key))
		if raw != nil {
			var env struct {
				Value     json.RawMessage `json:"v"`
				ExpiresAt int64           `json:"e"`
			}
			if json.Unmarshal(raw, &env) == nil && env.Value != nil {
				if env.ExpiresAt == 0 || time.Now().Unix() <= env.ExpiresAt {
					_ = json.Unmarshal(env.Value, &v)
				}
			} else {
				_ = json.Unmarshal(raw, &v)
			}
		}
		v++
		if ttl > 0 {
			return b.Put([]byte(key), mustJSON(map[string]any{"v": v, "e": time.Now().Add(ttl).Unix()}))
		}
		return b.Put([]byte(key), mustJSON(v))
	})
	if err != nil {
		return 0, err
	}
	return v, nil
}

type BucketStatsResult struct {
	Counts          map[string]int `json:"counts"`
	FileSizeBytes   int64          `json:"file_size_bytes"`
	Backend         string         `json:"backend,omitempty"`
	RedisAvailable  bool           `json:"redis_available,omitempty"`
	FallbackEnabled bool           `json:"fallback_enabled,omitempty"`
	RedisLastError  string         `json:"redis_last_error,omitempty"`
}

func (c *Cache) BucketStats() (BucketStatsResult, error) {
	res := BucketStatsResult{Counts: map[string]int{}, Backend: "bolt", FallbackEnabled: c.fallback, RedisAvailable: c.redis != nil}
	if c.redis != nil {
		res.Backend = "redis"
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		if err := c.redis.Ping(ctx).Err(); err != nil {
			res.RedisAvailable = false
			res.RedisLastError = err.Error()
		}
		for _, n := range []string{BucketAlerts, BucketRegistry, BucketRateLimit, BucketSettings} {
			pattern := c.redisKey(n, "") + "*"
			iter := c.redis.Scan(ctx, 0, pattern, 100).Iterator()
			for iter.Next(ctx) && res.Counts[n] < 1000 {
				res.Counts[n]++
			}
			if err := iter.Err(); err != nil && res.RedisLastError == "" {
				res.RedisAvailable = false
				res.RedisLastError = err.Error()
			}
		}
		cancel()
	}
	if c.db == nil {
		return res, nil
	}
	err := c.db.View(func(tx *bolt.Tx) error {
		for _, n := range []string{BucketAlerts, BucketRegistry, BucketRateLimit, BucketSettings} {
			if b := tx.Bucket([]byte(n)); b != nil {
				if c.redis == nil {
					res.Counts[n] = b.Stats().KeyN
				}
			}
		}
		return nil
	})
	if err != nil {
		return res, err
	}
	if info, err := os.Stat(c.db.Path()); err == nil {
		res.FileSizeBytes = info.Size()
	}
	return res, nil
}

type CacheKeyEntry struct {
	Key       string `json:"key"`
	ExpiresAt *int64 `json:"expires_at,omitempty"`
	Expired   bool   `json:"expired,omitempty"`
}

func (c *Cache) ListBucketKeys(bucket string, limit int) ([]CacheKeyEntry, error) {
	if limit <= 0 {
		limit = 100
	}
	var entries []CacheKeyEntry
	if c.redis != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		iter := c.redis.Scan(ctx, 0, c.redisKey(bucket, "")+"*", 100).Iterator()
		prefix := c.redisKey(bucket, "")
		for iter.Next(ctx) && len(entries) < limit {
			entries = append(entries, CacheKeyEntry{Key: strings.TrimPrefix(iter.Val(), prefix)})
		}
		err := iter.Err()
		cancel()
		if err == nil || !c.fallback || c.db == nil {
			return entries, err
		}
	}
	if c.db == nil {
		return entries, nil
	}
	err := c.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return fmt.Errorf("bucket %q not found", bucket)
		}
		return b.ForEach(func(k, v []byte) error {
			if len(entries) >= limit {
				return errors.New("limit reached")
			}
			e := CacheKeyEntry{Key: string(k)}
			var env struct {
				ExpiresAt int64 `json:"e"`
			}
			if json.Unmarshal(v, &env) == nil && env.ExpiresAt > 0 {
				e.ExpiresAt = &env.ExpiresAt
				if time.Now().Unix() > env.ExpiresAt {
					e.Expired = true
				}
			}
			entries = append(entries, e)
			return nil
		})
	})
	if err != nil && err.Error() != "limit reached" {
		return nil, err
	}
	return entries, nil
}

type AlertEntry struct {
	Type   string `json:"type"`
	Target string `json:"target"`
}

func (c *Cache) ActiveAlerts() ([]AlertEntry, error) {
	var entries []AlertEntry
	prefixMap := map[string]string{AlertBGPDown: "bgp_down", AlertBGPFail: "bgp_fail", AlertHandshake: "handshake_stale", AlertNodeOff: "node_offline", AlertLatency: "latency_high", LatencyActive: "latency_active"}
	if c.redis != nil {
		keys, err := c.ListBucketKeys(BucketAlerts, 1000)
		if err == nil {
			for _, e := range keys {
				for prefix, typeName := range prefixMap {
					if strings.HasPrefix(e.Key, prefix) {
						entries = append(entries, AlertEntry{Type: typeName, Target: e.Key[len(prefix):]})
						break
					}
				}
			}
			return entries, nil
		}
		if !c.fallback {
			return nil, err
		}
	}
	if c.db == nil {
		return entries, nil
	}
	err := c.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(BucketAlerts))
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, v []byte) error {
			key := string(k)
			for prefix, typeName := range prefixMap {
				if strings.HasPrefix(key, prefix) {
					var e struct {
						ExpiresAt int64 `json:"e"`
					}
					if json.Unmarshal(v, &e) == nil && e.ExpiresAt > 0 && time.Now().Unix() > e.ExpiresAt {
						return nil
					}
					entries = append(entries, AlertEntry{Type: typeName, Target: key[len(prefix):]})
					break
				}
			}
			return nil
		})
	})
	return entries, err
}
func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func (c *Cache) redisKey(bucket, key string) string {
	return c.prefix + bucket + ":" + key
}

func (c *Cache) putLocal(bucket, key string, value interface{}, data []byte, ttl time.Duration) error {
	return c.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return nil
		}
		if ttl > 0 {
			return b.Put([]byte(key), mustJSON(map[string]any{"v": value, "e": time.Now().Add(ttl).Unix()}))
		}
		return b.Put([]byte(key), data)
	})
}

func (c *Cache) putLocalRaw(bucket, key string, raw []byte, ttl time.Duration) error {
	return c.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return nil
		}
		if ttl > 0 {
			return b.Put([]byte(key), mustJSON(map[string]any{"v": json.RawMessage(raw), "e": time.Now().Add(ttl).Unix()}))
		}
		return b.Put([]byte(key), raw)
	})
}

func (c *Cache) getLocal(bucket, key string, dest interface{}) (bool, error) {
	var found bool
	err := c.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return nil
		}
		raw := b.Get([]byte(key))
		if raw == nil {
			return nil
		}
		var env struct {
			Value     json.RawMessage `json:"v"`
			ExpiresAt int64           `json:"e"`
		}
		if json.Unmarshal(raw, &env) == nil && env.Value != nil {
			if env.ExpiresAt > 0 && time.Now().Unix() > env.ExpiresAt {
				return nil
			}
			found = true
			return json.Unmarshal(env.Value, dest)
		}
		if json.Unmarshal(raw, dest) == nil {
			found = true
		}
		return nil
	})
	return found, err
}

const incrScript = `
local raw = redis.call("GET", KEYS[1])
local current = 0
if raw then
  local direct = tonumber(raw)
  if direct then
    current = direct
  else
    local ok, decoded = pcall(cjson.decode, raw)
    if ok and type(decoded) == "number" then
      current = decoded
    else
      current = 0
    end
  end
end
local ttl = tonumber(ARGV[1]) or 0
local existingTTL = redis.call("PTTL", KEYS[1])
current = current + 1
if existingTTL > 0 then
  redis.call("SET", KEYS[1], tostring(current), "PX", existingTTL)
elseif ttl > 0 then
  redis.call("SET", KEYS[1], tostring(current), "PX", ttl)
else
  redis.call("SET", KEYS[1], tostring(current))
end
return current
`
