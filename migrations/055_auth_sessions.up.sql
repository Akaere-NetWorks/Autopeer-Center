CREATE TABLE auth_sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    subject_type TEXT NOT NULL CHECK (subject_type IN ('user', 'admin', 'impersonation')),
    asn BIGINT,
    admin_id UUID REFERENCES admins(id) ON DELETE CASCADE,
    email TEXT,
    parent_session_id UUID REFERENCES auth_sessions(id) ON DELETE CASCADE,
    refresh_token_hash TEXT UNIQUE,
    previous_refresh_token_hash TEXT UNIQUE,
    previous_refresh_token_valid_until TIMESTAMPTZ,
    user_agent TEXT,
    ip INET,
    device_name TEXT,
    login_method TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    revoked_reason TEXT,
    CHECK (
        (subject_type = 'user' AND asn IS NOT NULL AND admin_id IS NULL)
        OR (subject_type = 'admin' AND admin_id IS NOT NULL AND asn IS NULL AND parent_session_id IS NULL)
        OR (subject_type = 'impersonation' AND asn IS NOT NULL AND admin_id IS NOT NULL AND parent_session_id IS NOT NULL)
    )
);

CREATE INDEX auth_sessions_subject_idx ON auth_sessions (subject_type, asn, admin_id);
CREATE INDEX auth_sessions_parent_idx ON auth_sessions (parent_session_id);
CREATE INDEX auth_sessions_expires_idx ON auth_sessions (expires_at);
CREATE INDEX auth_sessions_revoked_idx ON auth_sessions (revoked_at);
