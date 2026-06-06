CREATE TABLE IF NOT EXISTS node_metrics (
    time TIMESTAMPTZ NOT NULL DEFAULT now(),
    node_id UUID NOT NULL REFERENCES nodes(id),
    mem_alloc_mb BIGINT,
    mem_sys_mb BIGINT,
    num_goroutine INT,
    uptime_secs BIGINT
);
SELECT create_hypertable('node_metrics', 'time', if_not_exists => true);
CREATE INDEX IF NOT EXISTS idx_node_metrics_node_time ON node_metrics (node_id, time DESC);
SELECT add_retention_policy('node_metrics', INTERVAL '30 days', if_not_exists => true);
