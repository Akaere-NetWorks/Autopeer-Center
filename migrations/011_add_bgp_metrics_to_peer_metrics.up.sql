-- TimescaleDB >= 2.1 required: nullable columns with no default can be added
-- to a compressed hypertable without decompression.
ALTER TABLE peer_metrics
  ADD COLUMN IF NOT EXISTS routes_imported INTEGER,
  ADD COLUMN IF NOT EXISTS routes_exported INTEGER,
  ADD COLUMN IF NOT EXISTS bgp_uptime_secs INTEGER;
