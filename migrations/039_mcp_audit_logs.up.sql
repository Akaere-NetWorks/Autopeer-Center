CREATE TABLE mcp_audit_logs (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id  UUID,
    asn         BIGINT,
    admin_id    TEXT,
    tool_name   TEXT NOT NULL,
    args        JSONB,
    result_ok   BOOLEAN NOT NULL,
    error_msg   TEXT,
    called_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_mcp_audit_logs_asn ON mcp_audit_logs(asn);
CREATE INDEX idx_mcp_audit_logs_called_at ON mcp_audit_logs(called_at);
CREATE INDEX idx_mcp_audit_logs_tool_name ON mcp_audit_logs(tool_name);
CREATE INDEX idx_mcp_audit_logs_admin_id ON mcp_audit_logs(admin_id);
