-- H-5: Add FK from mcp_sessions.admin_key_id to admin_mcp_keys(id).
-- Previously admin_key_id was a bare UUID with no referential integrity.
ALTER TABLE mcp_sessions
    ADD CONSTRAINT fk_mcp_sessions_admin_key_id
    FOREIGN KEY (admin_key_id) REFERENCES admin_mcp_keys(id) ON DELETE SET NULL;
