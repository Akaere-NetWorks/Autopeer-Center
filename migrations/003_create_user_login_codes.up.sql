CREATE TABLE user_login_codes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    asn INTEGER NOT NULL,
    email TEXT NOT NULL,
    code TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    used BOOLEAN NOT NULL DEFAULT false,
    failed_attempts INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ DEFAULT now()
);

CREATE INDEX idx_login_codes_asn ON user_login_codes(asn);
