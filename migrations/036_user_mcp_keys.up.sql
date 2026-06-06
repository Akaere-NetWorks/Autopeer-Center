CREATE TABLE user_mcp_keys (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    asn          BIGINT NOT NULL,
    name         TEXT NOT NULL,
    key_hash     TEXT NOT NULL UNIQUE,
    key_prefix   TEXT NOT NULL,
    expires_at   TIMESTAMPTZ,
    last_used_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_user_mcp_keys_asn ON user_mcp_keys(asn);
