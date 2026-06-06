-- peers table: endpoint mismatch tracking fields
ALTER TABLE peers ADD COLUMN IF NOT EXISTS endpoint_mismatch_since TIMESTAMPTZ;
ALTER TABLE peers ADD COLUMN IF NOT EXISTS bgp_suspended_by_endpoint BOOLEAN DEFAULT FALSE;

-- peer_metrics: actual wg endpoint reported by agent
ALTER TABLE peer_metrics ADD COLUMN IF NOT EXISTS wg_actual_endpoint TEXT;
