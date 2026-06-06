ALTER TABLE user_mcp_keys
    ADD COLUMN capabilities TEXT[] NOT NULL DEFAULT ARRAY['read:nodes','read:peers','read:metrics']::TEXT[],
    ADD COLUMN revoked_at TIMESTAMPTZ,
    ADD COLUMN scope_version INTEGER NOT NULL DEFAULT 1;

ALTER TABLE admin_mcp_keys
    ADD COLUMN capabilities TEXT[] NOT NULL DEFAULT ARRAY['admin:read:topology','admin:read:peers','admin:read:metrics','admin:read:audit','admin:read:mcp_keys','admin:read:settings','admin:read:system']::TEXT[],
    ADD COLUMN revoked_at TIMESTAMPTZ,
    ADD COLUMN scope_version INTEGER NOT NULL DEFAULT 1;

ALTER TABLE mcp_audit_logs
    ADD COLUMN key_id UUID,
    ADD COLUMN admin_key_id UUID,
    ADD COLUMN capability TEXT,
    ADD COLUMN mode TEXT NOT NULL DEFAULT 'read',
    ADD COLUMN resource_type TEXT,
    ADD COLUMN resource_id TEXT,
    ADD COLUMN idempotency_key TEXT,
    ADD COLUMN request_hash TEXT,
    ADD COLUMN duration_ms BIGINT,
    ADD COLUMN client_ip TEXT,
    ADD COLUMN user_agent TEXT;

CREATE TABLE mcp_idempotency_keys (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_type      TEXT NOT NULL,
    key_id          UUID,
    admin_key_id    UUID,
    asn             BIGINT,
    admin_id        TEXT,
    tool_name       TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    request_hash    TEXT NOT NULL,
    status          TEXT NOT NULL,
    resource_type   TEXT,
    resource_id     TEXT,
    response_json   JSONB,
    error_code      TEXT,
    error_message   TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (actor_type, key_id, tool_name, idempotency_key),
    UNIQUE (actor_type, admin_key_id, tool_name, idempotency_key)
);

CREATE TABLE mcp_operations (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_type      TEXT NOT NULL,
    key_id          UUID,
    admin_key_id    UUID,
    asn             BIGINT,
    admin_id        TEXT,
    operation_type  TEXT NOT NULL,
    resource_type   TEXT NOT NULL,
    resource_id     TEXT NOT NULL,
    requested_args  JSONB NOT NULL DEFAULT '{}'::jsonb,
    status          TEXT NOT NULL DEFAULT 'pending_admin_review',
    idempotency_key TEXT NOT NULL,
    request_hash    TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_mcp_operations_asn ON mcp_operations(asn, created_at DESC);
CREATE INDEX idx_mcp_operations_status ON mcp_operations(status, created_at DESC);
