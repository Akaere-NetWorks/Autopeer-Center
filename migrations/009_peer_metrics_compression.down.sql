SELECT remove_compression_policy('peer_metrics', if_exists => true);
ALTER TABLE peer_metrics SET (timescaledb.compress = false);

SELECT remove_retention_policy('peer_metrics', if_exists => true);
SELECT add_retention_policy('peer_metrics', INTERVAL '15 days');
