CREATE TABLE mcp_sessions (
    id               UUID PRIMARY KEY,
    asn              BIGINT,
    key_id           UUID REFERENCES user_mcp_keys(id) ON DELETE SET NULL,
    is_admin         BOOLEAN NOT NULL DEFAULT false,
    admin_key_id     UUID,
    connected_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    disconnected_at  TIMESTAMPTZ,
    last_ping_at     TIMESTAMPTZ
);

CREATE INDEX idx_mcp_sessions_asn ON mcp_sessions(asn);
CREATE INDEX idx_mcp_sessions_connected_at ON mcp_sessions(connected_at);
CREATE INDEX idx_mcp_sessions_is_admin ON mcp_sessions(is_admin);
