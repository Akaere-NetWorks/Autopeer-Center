CREATE TABLE peer_metrics (
    time TIMESTAMPTZ NOT NULL,
    peer_id UUID NOT NULL,
    rx_bytes BIGINT,
    tx_bytes BIGINT,
    uptime_seconds INTEGER,
    bgp_state TEXT
);

SELECT create_hypertable('peer_metrics', 'time');

ALTER TABLE peer_metrics SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'peer_id',
    timescaledb.compress_orderby = 'time DESC'
);

SELECT add_compression_policy('peer_metrics', INTERVAL '1 day', if_not_exists => true);
SELECT add_retention_policy('peer_metrics', INTERVAL '15 days', if_not_exists => true);
