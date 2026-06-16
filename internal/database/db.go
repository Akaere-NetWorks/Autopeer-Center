package database

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/sirupsen/logrus"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
)

var log = logrus.WithField("pkg", "database")

func maskDatabaseURL(databaseURL string) string {
	u, err := url.Parse(databaseURL)
	if err != nil {
		return "***"
	}
	if u.User != nil {
		u.User = url.UserPassword(u.User.Username(), "***")
	}
	return u.Scheme + "://" + u.Host
}

func Connect(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	log.WithField("database_url", maskDatabaseURL(databaseURL)).Debug("Connect entry")

	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		log.WithError(err).Error("parse database config failed")
		return nil, fmt.Errorf("parse database url: %w", err)
	}

	config.MaxConns = 20
	config.MinConns = 2
	config.MaxConnLifetime = 30 * time.Minute
	config.MaxConnIdleTime = 5 * time.Minute

	// Bound how long any statement waits to acquire a lock. Without this, a
	// SELECT ... FOR UPDATE (e.g. the atlas_credits row lock during measurement
	// creation) blocks indefinitely behind a stuck/idle-in-transaction session,
	// hanging the request instead of failing fast. Only caps lock waits, not
	// query runtime, so long-running reads/migrations are unaffected. Respect an
	// explicit lock_timeout already present in DATABASE_URL.
	if config.ConnConfig.RuntimeParams == nil {
		config.ConnConfig.RuntimeParams = map[string]string{}
	}
	if _, ok := config.ConnConfig.RuntimeParams["lock_timeout"]; !ok {
		config.ConnConfig.RuntimeParams["lock_timeout"] = "10000" // milliseconds
	}

	log.WithFields(logrus.Fields{
		"max_conns":         20,
		"min_conns":         2,
		"max_conn_lifetime": "30m",
		"max_conn_idle":     "5m",
	}).Debug("pool configuration")

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		log.WithError(err).Error("create pool failed")
		return nil, fmt.Errorf("create pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		log.WithError(err).Error("database ping failed")
		return nil, fmt.Errorf("ping database: %w", err)
	}

	log.Debug("database ping successful")
	return pool, nil
}

func NewBunDB(pool *pgxpool.Pool) *bun.DB {
	sqldb := stdlib.OpenDBFromPool(pool)
	return bun.NewDB(sqldb, pgdialect.New())
}
