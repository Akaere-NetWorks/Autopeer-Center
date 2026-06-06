package main

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/getsentry/sentry-go"
	"github.com/jackc/pgx/v5/pgxpool"
)

func runMigrations(parent context.Context, db *pgxpool.Pool) {
	span := sentry.StartSpan(parent, "db.migrations")
	defer span.Finish()
	ctx := span.Context()

	_, err := db.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`)
	if err != nil {
		span.Status = sentry.SpanStatusInternalError
		log.WithError(err).Fatal("migration: failed to create schema_migrations table")
	}

	_, err = db.Exec(ctx, `SELECT pg_advisory_lock(20260428)`)
	if err != nil {
		span.Status = sentry.SpanStatusInternalError
		log.WithError(err).Fatal("migration: failed to acquire advisory lock")
	}
	defer db.Exec(ctx, `SELECT pg_advisory_unlock(20260428)`)

	matches, err := filepath.Glob("migrations/*.up.sql")
	if err != nil || len(matches) == 0 {
		log.Info("migration: no migration files found, skipping")
		span.Status = sentry.SpanStatusOK
		return
	}
	sort.Strings(matches)

	log.WithField("count", len(matches)).Debug("migration files found")

	for _, path := range matches {
		version := strings.TrimSuffix(filepath.Base(path), ".up.sql")

		var exists bool
		_ = db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)`, version).Scan(&exists)
		if exists {
			continue
		}

		log.WithField("version", version).Debug("processing migration file")

		sqlBytes, err := os.ReadFile(path)
		if err != nil {
			span.Status = sentry.SpanStatusInternalError
			log.WithError(err).WithField("path", path).Fatal("migration: failed to read file")
		}
		sqlStr := string(sqlBytes)

		noTx := strings.Contains(strings.SplitN(sqlStr, "\n", 2)[0], "autopeer:no-transaction")

		if noTx {
			for _, stmt := range strings.Split(sqlStr, ";") {
				var sqlLines []string
				for _, line := range strings.Split(stmt, "\n") {
					trimmed := strings.TrimSpace(line)
					if trimmed != "" && !strings.HasPrefix(trimmed, "--") {
						sqlLines = append(sqlLines, line)
					}
				}
				if len(sqlLines) == 0 {
					continue
				}
				if _, err := db.Exec(ctx, strings.TrimSpace(strings.Join(sqlLines, "\n"))); err != nil {
					span.Status = sentry.SpanStatusInternalError
					log.WithError(err).WithField("path", path).Fatal("migration: failed to apply")
				}
			}
			if _, err := db.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, version); err != nil {
				span.Status = sentry.SpanStatusInternalError
				log.WithError(err).WithField("path", path).Fatal("migration: failed to record")
			}
		} else {
			tx, err := db.Begin(ctx)
			if err != nil {
				span.Status = sentry.SpanStatusInternalError
				log.WithError(err).WithField("path", path).Fatal("migration: failed to begin transaction")
			}

			if _, err := tx.Exec(ctx, sqlStr); err != nil {
				tx.Rollback(ctx)
				span.Status = sentry.SpanStatusInternalError
				log.WithError(err).WithField("path", path).Fatal("migration: failed to apply")
			}

			if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, version); err != nil {
				tx.Rollback(ctx)
				span.Status = sentry.SpanStatusInternalError
				log.WithError(err).WithField("path", path).Fatal("migration: failed to record")
			}

			if err := tx.Commit(ctx); err != nil {
				span.Status = sentry.SpanStatusInternalError
				log.WithError(err).WithField("path", path).Fatal("migration: failed to commit")
			}
		}

		log.WithField("version", version).Info("migration applied")
	}
	span.SetData("migration_count", len(matches))
	span.Status = sentry.SpanStatusOK
	log.Info("migration: all migrations up to date")
}
