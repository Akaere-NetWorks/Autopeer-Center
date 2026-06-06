-- Enable compression on peer_metrics (segment by peer_id, order by time)
ALTER TABLE peer_metrics SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'peer_id',
    timescaledb.compress_orderby = 'time DESC'
);

SELECT remove_compression_policy('peer_metrics', if_exists => true);
SELECT add_compression_policy('peer_metrics', INTERVAL '1 day');

-- Change retention from 15 days to 7 days
SELECT remove_retention_policy('peer_metrics', if_exists => true);
SELECT add_retention_policy('peer_metrics', INTERVAL '7 days');
