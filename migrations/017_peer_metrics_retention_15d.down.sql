SELECT remove_retention_policy('peer_metrics', if_exists => true);
SELECT add_retention_policy('peer_metrics', INTERVAL '7 days');
