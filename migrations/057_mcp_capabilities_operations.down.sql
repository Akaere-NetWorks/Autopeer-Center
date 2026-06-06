DROP INDEX IF EXISTS idx_mcp_operations_status;
DROP INDEX IF EXISTS idx_mcp_operations_asn;
DROP TABLE IF EXISTS mcp_operations;
DROP TABLE IF EXISTS mcp_idempotency_keys;

ALTER TABLE mcp_audit_logs
    DROP COLUMN IF EXISTS user_agent,
    DROP COLUMN IF EXISTS client_ip,
    DROP COLUMN IF EXISTS duration_ms,
    DROP COLUMN IF EXISTS request_hash,
    DROP COLUMN IF EXISTS idempotency_key,
    DROP COLUMN IF EXISTS resource_id,
    DROP COLUMN IF EXISTS resource_type,
    DROP COLUMN IF EXISTS mode,
    DROP COLUMN IF EXISTS capability,
    DROP COLUMN IF EXISTS admin_key_id,
    DROP COLUMN IF EXISTS key_id;

ALTER TABLE admin_mcp_keys
    DROP COLUMN IF EXISTS scope_version,
    DROP COLUMN IF EXISTS revoked_at,
    DROP COLUMN IF EXISTS capabilities;

ALTER TABLE user_mcp_keys
    DROP COLUMN IF EXISTS scope_version,
    DROP COLUMN IF EXISTS revoked_at,
    DROP COLUMN IF EXISTS capabilities;
