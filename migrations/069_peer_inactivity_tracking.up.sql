ALTER TABLE peers ADD COLUMN last_active_at TIMESTAMPTZ;
ALTER TABLE peers ADD COLUMN inactivity_warning_stage SMALLINT NOT NULL DEFAULT 0;
UPDATE peers SET last_active_at = now() WHERE status = 'active';
CREATE INDEX idx_peers_last_active_at ON peers (last_active_at) WHERE status = 'active';
