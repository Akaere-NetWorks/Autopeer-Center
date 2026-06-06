-- TimescaleDB >= 2.1 required: nullable column with no default can be added
-- to a compressed hypertable without decompression (same pattern as migration 008).
ALTER TABLE peer_metrics ADD COLUMN IF NOT EXISTS last_handshake TIMESTAMPTZ;
