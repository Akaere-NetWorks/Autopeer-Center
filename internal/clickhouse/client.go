// Package clickhouse holds the optional ClickHouse-backed store for DN42 traffic
// sampling analytics. It is wired in only when CLICKHOUSE_URL is configured; when
// absent, center runs normally and the traffic-analytics feature is disabled
// end-to-end (reports dropped, API returns 503, frontend hides the panel).
package clickhouse

import (
	"context"
	"fmt"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/sirupsen/logrus"
)

var log = logrus.WithField("pkg", "clickhouse")

// Connect parses a clickhouse:// DSN, opens a native-protocol connection and
// pings it. A non-nil error means the store must be treated as unavailable
// (the caller decides fatal-vs-disable, mirroring the optional Redis pattern).
func Connect(ctx context.Context, dsn string) (driver.Conn, error) {
	opts, err := clickhouse.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse clickhouse dsn: %w", err)
	}
	conn, err := clickhouse.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("open clickhouse: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := conn.Ping(pingCtx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("ping clickhouse: %w", err)
	}
	log.Info("clickhouse connected")
	return conn, nil
}
