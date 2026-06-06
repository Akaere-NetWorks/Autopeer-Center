ALTER TABLE peer_metrics
  DROP COLUMN IF EXISTS routes_imported,
  DROP COLUMN IF EXISTS routes_exported,
  DROP COLUMN IF EXISTS bgp_uptime_secs;
