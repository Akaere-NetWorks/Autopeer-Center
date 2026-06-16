package clickhouse

import (
	"context"
	"fmt"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// ClickHouse does not use center's Postgres migration framework. Schema is
// applied idempotently on startup via CREATE TABLE IF NOT EXISTS. Later schema
// evolution should use standalone `ALTER TABLE ... ADD COLUMN IF NOT EXISTS`.

// traffic_samples: one row per interface per window. Map columns carry the fixed
// protocol/size-histogram dimensions (sumMap merges them at query time); scalar
// totals stay in their own columns for plain sum().
const ddlTrafficSamples = `
CREATE TABLE IF NOT EXISTS traffic_samples (
  time             DateTime,
  node_id          String,
  peer_id          String,
  asn              UInt32,
  sample_ratio     Float64,
  sampled_packets  UInt64,
  sampled_bytes    UInt64,
  v4_packets       UInt64,
  v6_packets       UInt64,
  v4_bytes         UInt64,
  v6_bytes         UInt64,
  proto_pkts       Map(String, UInt64),
  proto_bytes      Map(String, UInt64),
  size_buckets     Map(String, UInt64)
) ENGINE = MergeTree
PARTITION BY toYYYYMMDD(time)
ORDER BY (node_id, peer_id, time)
TTL time + INTERVAL 15 DAY`

// traffic_top: one row per Top-N entry per interface per window. Higher row count
// → shorter TTL than traffic_samples.
const ddlTrafficTop = `
CREATE TABLE IF NOT EXISTS traffic_top (
  time    DateTime,
  node_id String,
  peer_id String,
  asn     UInt32,
  kind    UInt8,
  label   String,
  pkts    UInt64,
  bytes   UInt64
) ENGINE = MergeTree
PARTITION BY toYYYYMMDD(time)
ORDER BY (node_id, kind, time, label)
TTL time + INTERVAL 7 DAY`

// EnsureSchema creates the analytics tables if they do not yet exist.
func EnsureSchema(ctx context.Context, conn driver.Conn) error {
	for _, ddl := range []string{ddlTrafficSamples, ddlTrafficTop} {
		if err := conn.Exec(ctx, ddl); err != nil {
			return fmt.Errorf("ensure clickhouse schema: %w", err)
		}
	}
	log.Info("clickhouse traffic schema ready")
	return nil
}
