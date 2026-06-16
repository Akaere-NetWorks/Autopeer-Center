DROP INDEX IF EXISTS idx_peers_last_active_at;
ALTER TABLE peers DROP COLUMN IF EXISTS inactivity_warning_stage;
ALTER TABLE peers DROP COLUMN IF EXISTS last_active_at;
