CREATE TABLE passkey_credentials (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    asn           BIGINT NOT NULL,
    credential_id TEXT NOT NULL UNIQUE,
    public_key    BYTEA NOT NULL,
    aaguid        TEXT NOT NULL DEFAULT '',
    sign_count    BIGINT NOT NULL DEFAULT 0,
    name          TEXT NOT NULL DEFAULT 'Passkey',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at  TIMESTAMPTZ
);
CREATE INDEX idx_passkey_credentials_asn ON passkey_credentials(asn);
