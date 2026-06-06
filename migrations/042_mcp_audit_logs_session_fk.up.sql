-- L-4: Add FK from mcp_audit_logs.session_id to mcp_sessions(id).
-- Orphaned session_ids were silently accumulating without this constraint.
ALTER TABLE mcp_audit_logs
    ADD CONSTRAINT fk_mcp_audit_logs_session_id
    FOREIGN KEY (session_id) REFERENCES mcp_sessions(id) ON DELETE SET NULL;
