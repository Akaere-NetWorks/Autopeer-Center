CREATE TABLE admin_mcp_keys (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    admin_id     TEXT NOT NULL,
    name         TEXT NOT NULL,
    key_hash     TEXT NOT NULL UNIQUE,
    key_prefix   TEXT NOT NULL,
    expires_at   TIMESTAMPTZ,
    last_used_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_admin_mcp_keys_admin_id ON admin_mcp_keys(admin_id);
