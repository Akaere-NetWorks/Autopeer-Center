CREATE TABLE auth_device_grants (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_code_hash TEXT NOT NULL UNIQUE,
    user_code_hash TEXT NOT NULL UNIQUE,
    client_name TEXT NOT NULL,
    device_name TEXT,
    version TEXT,
    scopes TEXT[] NOT NULL DEFAULT ARRAY['user'],
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'denied', 'exchanged')),
    approved_asn BIGINT,
    approved_email TEXT,
    approved_by_session_id UUID REFERENCES auth_sessions(id) ON DELETE SET NULL,
    exchanged_session_id UUID REFERENCES auth_sessions(id) ON DELETE SET NULL,
    request_ip INET,
    request_user_agent TEXT,
    approve_ip INET,
    approve_user_agent TEXT,
    poll_count INT NOT NULL DEFAULT 0,
    last_polled_at TIMESTAMPTZ,
    next_poll_after TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL,
    approved_at TIMESTAMPTZ,
    denied_at TIMESTAMPTZ,
    exchanged_at TIMESTAMPTZ
);

CREATE INDEX auth_device_grants_status_expires_idx ON auth_device_grants (status, expires_at);
CREATE INDEX auth_device_grants_created_idx ON auth_device_grants (created_at);
CREATE INDEX auth_device_grants_user_code_idx ON auth_device_grants (user_code_hash);
