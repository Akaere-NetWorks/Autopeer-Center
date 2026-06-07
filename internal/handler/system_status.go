package handler

import (
	"context"
	"fmt"
	"net/http"
	"runtime"
	"strconv"
	"time"

	"github.com/akaere/autopeer-center/internal/cache"
	"github.com/akaere/autopeer-center/internal/config"
	"github.com/akaere/autopeer-center/internal/crypto"
	"github.com/akaere/autopeer-center/internal/queue"
	"github.com/akaere/autopeer-center/internal/redisx"
	"github.com/akaere/autopeer-center/internal/service"
	"github.com/akaere/autopeer-center/internal/ws"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sirupsen/logrus"
	"github.com/uptrace/bun"
)

var sysStatusLog = logrus.WithField("pkg", "handler.system_status")

type BuildInfo struct {
	CommitHash string `json:"commit_hash"`
	BuildDate  string `json:"build_date"`
	Version    string `json:"version"`
	GoVersion  string `json:"go_version"`
}

type SystemStatusHandler struct {
	pool      *pgxpool.Pool
	db        *bun.DB
	hub       *ws.Hub
	cache     *cache.Cache
	cfg       *config.Config
	redis     *redisx.Client
	queue     *queue.Queue
	lockInfo  LockStatus
	build     BuildInfo
	startedAt time.Time
	centerKP  *crypto.KeyPair
}

func NewSystemStatusHandler(
	pool *pgxpool.Pool,
	db *bun.DB,
	hub *ws.Hub,
	c *cache.Cache,
	cfg *config.Config,
	redisClient *redisx.Client,
	q *queue.Queue,
	lockInfo LockStatus,
	build BuildInfo,
	startedAt time.Time,
	centerKP *crypto.KeyPair,
) *SystemStatusHandler {
	return &SystemStatusHandler{
		pool:      pool,
		db:        db,
		hub:       hub,
		cache:     c,
		cfg:       cfg,
		redis:     redisClient,
		queue:     q,
		lockInfo:  lockInfo,
		build:     build,
		startedAt: startedAt,
		centerKP:  centerKP,
	}
}

type LockStatus struct {
	Enabled   bool   `json:"enabled"`
	Available bool   `json:"available"`
	Backend   string `json:"backend"`
}

func (h *SystemStatusHandler) Get(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	build := h.build
	build.GoVersion = runtime.Version()

	uptime := int64(time.Since(h.startedAt).Seconds())

	centerPubKey := ""
	if h.centerKP != nil && h.centerKP.PublicKey != nil {
		centerPubKey = crypto.PubKeyHex(h.centerKP.PublicKey)
	}

	pingStart := time.Now()
	if err := h.pool.Ping(ctx); err != nil {
		ErrorJSON(w, r, http.StatusServiceUnavailable, "db_unavailable", "Database ping failed")
		return
	}
	pingMs := float64(time.Since(pingStart).Microseconds()) / 1000.0

	sqlDB := h.db.DB
	dbStats := sqlDB.Stats()

	var migrations []map[string]interface{}
	rows, err := h.pool.Query(ctx, `SELECT version, applied_at FROM schema_migrations ORDER BY version`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var version string
			var appliedAt time.Time
			if rows.Scan(&version, &appliedAt) == nil {
				migrations = append(migrations, map[string]interface{}{
					"version":    version,
					"applied_at": appliedAt,
				})
			}
		}
	}

	hubStat := h.hub.Stat()

	bucketStats, _ := h.cache.BucketStats()
	activeAlerts, _ := h.cache.ActiveAlerts()

	alertCounts := map[string]int{
		"bgp_down":        0,
		"bgp_fail":        0,
		"handshake_stale": 0,
		"node_offline":    0,
		"latency_high":    0,
		"latency_active":  0,
	}
	for _, a := range activeAlerts {
		alertCounts[a.Type]++
	}

	reqStats := h.queryRequestStats(r.Context())
	redisStatus := h.redisStatus()
	queueStatus := h.queueStatus()

	bucketKeys := map[string][]cache.CacheKeyEntry{}
	for _, name := range []string{cache.BucketAlerts, cache.BucketRegistry, cache.BucketRateLimit, cache.BucketSettings} {
		keys, err := h.cache.ListBucketKeys(name, 100)
		if err != nil {
			sysStatusLog.WithError(err).Warnf("failed to list bucket keys for %s", name)
			keys = []cache.CacheKeyEntry{}
		}
		bucketKeys[name] = keys
	}

	JSON(w, http.StatusOK, map[string]interface{}{
		"build": build,
		"process": map[string]interface{}{
			"started_at":        h.startedAt,
			"uptime_secs":       uptime,
			"listen_addr":       h.cfg.ListenAddr,
			"external_url":      h.cfg.ExternalURL,
			"center_public_key": centerPubKey,
		},
		"database": map[string]interface{}{
			"ping_ms":          pingMs,
			"open_conns":       dbStats.OpenConnections,
			"in_use":           dbStats.InUse,
			"idle":             dbStats.Idle,
			"max_open_conns":   dbStats.MaxOpenConnections,
			"wait_count":       dbStats.WaitCount,
			"wait_duration_ms": dbStats.WaitDuration.Milliseconds(),
			"migrations":       migrations,
			"migration_count":  len(migrations),
		},
		"hub":   hubStat,
		"redis": redisStatus,
		"queue": queueStatus,
		"email": h.emailStatus(),
		"lock":  h.lockInfo,
		"cache": map[string]interface{}{
			"backend":          bucketStats.Backend,
			"redis_available":  bucketStats.RedisAvailable,
			"redis_last_error": bucketStats.RedisLastError,
			"fallback_enabled": bucketStats.FallbackEnabled,
			"file_size_bytes":  bucketStats.FileSizeBytes,
			"buckets":          bucketStats.Counts,
			"active_alerts":    activeAlerts,
			"bucket_keys":      bucketKeys,
		},
		"alerts":      alertCounts,
		"request_log": reqStats,
	})
}

func (h *SystemStatusHandler) redisStatus() map[string]interface{} {
	enabled := h.cfg != nil && h.cfg.RedisURL != ""
	lastError := ""
	available := false
	if h.redis != nil {
		lastError = h.redis.LastError()
		if enabled {
			if err := h.redis.Ping(context.Background(), time.Second); err != nil {
				lastError = err.Error()
			} else {
				available = true
				lastError = ""
			}
		}
	}
	return map[string]interface{}{
		"enabled":    enabled,
		"available":  available,
		"last_error": lastError,
		"key_prefix": h.cfg.RedisKeyPrefix,
		"required":   h.cfg.RedisRequired,
		"configured": enabled,
	}
}

func (h *SystemStatusHandler) queueStatus() map[string]interface{} {
	backoffUntil := time.Time{}
	if h.queue != nil {
		backoffUntil = h.queue.BackoffUntil()
	}
	var backoff any
	if !backoffUntil.IsZero() {
		if backoffUntil.After(time.Now()) {
			backoff = backoffUntil
		}
	}
	return map[string]interface{}{
		"enabled":       h.queue != nil && h.queue.Enabled(),
		"available":     h.queue != nil && h.queue.Available(),
		"backend":       "asynq",
		"concurrency":   h.cfg.AsynqConcurrency,
		"queues":        queue.ParseQueues(h.cfg.AsynqQueues),
		"backoff_until": backoff,
	}
}

// emailStatus reports the active email backend (api or smtp) for the admin UI.
func (h *SystemStatusHandler) emailStatus() map[string]interface{} {
	provider := service.ActiveBackend(h.cfg.EmailConfig())
	out := map[string]interface{}{"backend": provider}
	switch provider {
	case "smtp":
		out["configured"] = h.cfg.SMTPHost != "" && h.cfg.SMTPFrom != ""
		out["host"] = h.cfg.SMTPHost
		out["from"] = h.cfg.SMTPFrom
		out["tls"] = h.cfg.SMTPTLS
	default: // api
		out["configured"] = h.cfg.EmailAPIURL != ""
	}
	return out
}

type requestStatsResult struct {
	Last5MinCount int     `json:"last_5min_count"`
	LastHourCount int     `json:"last_hour_count"`
	ErrorRate5xx  float64 `json:"error_rate_5xx"`
	P95DurationMs float64 `json:"p95_duration_ms"`
}

func (h *SystemStatusHandler) GetDBTables(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	daysStr := r.URL.Query().Get("days")
	days := 30
	if daysStr != "" {
		if d, err := strconv.Atoi(daysStr); err == nil && d >= 1 && d <= 3650 {
			days = d
		}
	}

	type tableSizeRow struct {
		TotalSizeBytes int64 `json:"total_size_bytes"`
		RowEstimate    int64 `json:"row_estimate"`
		PendingCount   int64 `json:"pending_count"`
	}

	requestLogs := tableSizeRow{}
	peerMetrics := tableSizeRow{}
	nodeMetrics := tableSizeRow{}

	tableQueries := []struct {
		table string
		dest  *tableSizeRow
	}{
		{"request_logs", &requestLogs},
		{"peer_metrics", &peerMetrics},
		{"node_metrics", &nodeMetrics},
	}

	for _, q := range tableQueries {
		if err := h.pool.QueryRow(ctx, `
			SELECT pg_total_relation_size($1::regclass),
			       COALESCE((SELECT approximate_row_count FROM approximate_row_count($1::text)), 0)
		`, q.table).Scan(&q.dest.TotalSizeBytes, &q.dest.RowEstimate); err != nil {
			sysStatusLog.WithError(err).Warnf("failed to query size for %s", q.table)
		}
	}

	if err := h.pool.QueryRow(ctx, `SELECT COUNT(*) FROM request_logs WHERE created_at < now() - make_interval(days => $1)`, days).Scan(&requestLogs.PendingCount); err != nil {
		sysStatusLog.WithError(err).Warn("failed to query request_logs pending rotation count")
	}

	for _, ht := range []struct {
		table string
		dest  *tableSizeRow
	}{
		{"peer_metrics", &peerMetrics},
		{"node_metrics", &nodeMetrics},
	} {
		var chunkCount int64
		if err := h.pool.QueryRow(ctx, `SELECT count(*) FROM show_chunks($1::regclass, older_than => make_interval(days => $2))`, ht.table, days).Scan(&chunkCount); err != nil {
			sysStatusLog.WithError(err).Warnf("failed to query %s pending chunks", ht.table)
			chunkCount = 0
		}
		ht.dest.PendingCount = chunkCount
	}

	type compressionStats struct {
		TotalChunks            int   `json:"total_chunks"`
		NumberCompressedChunks int   `json:"number_compressed_chunks"`
		BeforeCompressionBytes int64 `json:"before_compression_bytes"`
		AfterCompressionBytes  int64 `json:"after_compression_bytes"`
	}

	var comp compressionStats
	if err := h.pool.QueryRow(ctx, `
		SELECT COALESCE(total_chunks, 0), COALESCE(number_compressed_chunks, 0),
		       COALESCE(before_compression_total_bytes, 0), COALESCE(after_compression_total_bytes, 0)
		FROM hypertable_compression_stats('peer_metrics')
	`).Scan(&comp.TotalChunks, &comp.NumberCompressedChunks, &comp.BeforeCompressionBytes, &comp.AfterCompressionBytes); err != nil {
		sysStatusLog.WithError(err).Warn("failed to query peer_metrics compression stats")
	}

	JSON(w, http.StatusOK, map[string]interface{}{
		"days": days,
		"request_logs": map[string]interface{}{
			"total_size_bytes": requestLogs.TotalSizeBytes,
			"row_estimate":     requestLogs.RowEstimate,
			"pending_count":    requestLogs.PendingCount,
		},
		"peer_metrics": map[string]interface{}{
			"total_size_bytes":         peerMetrics.TotalSizeBytes,
			"row_estimate":             peerMetrics.RowEstimate,
			"pending_count":            peerMetrics.PendingCount,
			"total_chunks":             comp.TotalChunks,
			"number_compressed_chunks": comp.NumberCompressedChunks,
			"before_compression_bytes": comp.BeforeCompressionBytes,
			"after_compression_bytes":  comp.AfterCompressionBytes,
		},
		"node_metrics": map[string]interface{}{
			"total_size_bytes": nodeMetrics.TotalSizeBytes,
			"row_estimate":     nodeMetrics.RowEstimate,
			"pending_count":    nodeMetrics.PendingCount,
		},
	})
}

func (h *SystemStatusHandler) RotateTables(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()

	var req struct {
		Tables []string `json:"tables"`
		Days   int      `json:"days"`
	}
	if err := DecodeJSON(r, &req); err != nil {
		ErrorJSON(w, r, http.StatusBadRequest, "invalid_request", "Invalid JSON body")
		return
	}

	if len(req.Tables) == 0 {
		ErrorJSON(w, r, http.StatusBadRequest, "no_tables", "At least one table is required")
		return
	}
	if req.Days < 1 || req.Days > 3650 {
		ErrorJSON(w, r, http.StatusBadRequest, "invalid_days", "Days must be between 1 and 3650")
		return
	}

	allowed := map[string]bool{"request_logs": true, "peer_metrics": true, "node_metrics": true}
	for _, t := range req.Tables {
		if !allowed[t] {
			ErrorJSON(w, r, http.StatusBadRequest, "invalid_table", fmt.Sprintf("Unknown table: %s", t))
			return
		}
	}

	results := map[string]interface{}{}

	for _, table := range req.Tables {
		switch table {
		case "request_logs":
			var totalDeleted int64
			for {
				tag, err := h.pool.Exec(ctx, `DELETE FROM request_logs WHERE created_at < now() - make_interval(days => $1) LIMIT 10000`, req.Days)
				if err != nil {
					sysStatusLog.WithError(err).Error("failed to rotate request_logs")
					results["request_logs"] = map[string]interface{}{"error": err.Error()}
					break
				}
				affected := tag.RowsAffected()
				totalDeleted += affected
				if affected < 10000 {
					results["request_logs"] = map[string]interface{}{"deleted_count": totalDeleted}
					break
				}
			}
		case "peer_metrics", "node_metrics":
			var droppedCount int
			err := h.pool.QueryRow(ctx, `SELECT count(*) FROM drop_chunks($1::regclass, make_interval(days => $2))`, table, req.Days).Scan(&droppedCount)
			if err != nil {
				sysStatusLog.WithError(err).Errorf("failed to drop chunks for %s", table)
				results[table] = map[string]interface{}{"error": err.Error()}
				continue
			}
			results[table] = map[string]interface{}{"dropped_chunks": droppedCount}
		}
	}

	sysStatusLog.WithField("results", results).Infof("manual table rotation completed (tables=%v, days=%d)", req.Tables, req.Days)

	JSON(w, http.StatusOK, map[string]interface{}{
		"results": results,
	})
}

func (h *SystemStatusHandler) queryRequestStats(parentCtx context.Context) requestStatsResult {
	ctx, cancel := context.WithTimeout(parentCtx, 30*time.Second)
	defer cancel()

	var result requestStatsResult

	if err := h.pool.QueryRow(ctx, `SELECT COUNT(*) FROM request_logs WHERE created_at > NOW() - INTERVAL '5 minutes'`).Scan(&result.Last5MinCount); err != nil {
		sysStatusLog.WithError(err).Warn("failed to query last_5min_count")
	}
	if err := h.pool.QueryRow(ctx, `SELECT COUNT(*) FROM request_logs WHERE created_at > NOW() - INTERVAL '1 hour'`).Scan(&result.LastHourCount); err != nil {
		sysStatusLog.WithError(err).Warn("failed to query last_hour_count")
	}

	var errCount int
	if err := h.pool.QueryRow(ctx, `SELECT COUNT(*) FROM request_logs WHERE created_at > NOW() - INTERVAL '1 hour' AND status_code >= 500`).Scan(&errCount); err != nil {
		sysStatusLog.WithError(err).Warn("failed to query 5xx error count")
	}
	if result.LastHourCount > 0 {
		result.ErrorRate5xx = float64(errCount) / float64(result.LastHourCount) * 100.0
	}

	if err := h.pool.QueryRow(ctx, `SELECT COALESCE(PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY duration_ms), 0) FROM request_logs WHERE created_at > NOW() - INTERVAL '1 hour'`).Scan(&result.P95DurationMs); err != nil {
		sysStatusLog.WithError(err).Warn("failed to query p95 duration")
	}

	return result
}
