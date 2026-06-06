CREATE TABLE webauthn_sessions (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    asn          BIGINT NOT NULL,
    type         TEXT NOT NULL CHECK (type IN ('register', 'login')),
    session_data TEXT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at   TIMESTAMPTZ NOT NULL DEFAULT now() + interval '5 minutes'
);
CREATE INDEX idx_webauthn_sessions_asn_type ON webauthn_sessions(asn, type);
