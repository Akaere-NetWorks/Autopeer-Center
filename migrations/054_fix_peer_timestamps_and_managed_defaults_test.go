package migrations

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPeerFixMigrationContainsTimestampAndManagedUpdates(t *testing.T) {
	path := filepath.Join("054_fix_peer_timestamps_and_managed_defaults.up.sql")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	sql := string(data)

	checks := []string{
		"created_at = COALESCE(NULLIF(updated_at, '0001-01-01 00:00:00+00'::timestamptz), now())",
		"updated_at = created_at",
		"SET wg_managed = true",
		"WHERE wg_managed = false",
	}
	for _, want := range checks {
		if !strings.Contains(sql, want) {
			t.Fatalf("migration missing %q", want)
		}
	}
}
