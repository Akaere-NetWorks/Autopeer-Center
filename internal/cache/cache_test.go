package cache

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/akaere/autopeer-center/internal/config"
	"github.com/redis/go-redis/v9"
)

func TestOpenWithConfig_DisablesBoltWhenRedisAvailable(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "cache")
	if err := os.WriteFile(path, []byte("stale"), 0600); err != nil {
		t.Fatal(err)
	}

	c, err := OpenWithConfig(path, &config.Config{CacheLocalFallback: true}, &redis.Client{})
	if err != nil {
		t.Fatalf("OpenWithConfig returned error: %v", err)
	}
	if c.db != nil {
		t.Fatal("expected bolt db to remain disabled when redis is available")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected stale cache file to be removed, stat err=%v", err)
	}
}

func TestOpenWithConfig_RequiresFallbackWhenRedisUnavailable(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "cache")

	_, err := OpenWithConfig(path, &config.Config{CacheLocalFallback: false}, nil)
	if err == nil {
		t.Fatal("expected error when redis is unavailable and fallback is disabled")
	}
}
