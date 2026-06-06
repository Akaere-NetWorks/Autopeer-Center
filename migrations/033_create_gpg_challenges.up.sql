CREATE TABLE gpg_challenges (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    asn         BIGINT NOT NULL,
    fingerprints TEXT[] NOT NULL,
    challenge   TEXT NOT NULL,
    expires_at  TIMESTAMPTZ NOT NULL,
    used        BOOLEAN NOT NULL DEFAULT false,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_gpg_challenges_asn_active ON gpg_challenges (asn) WHERE used = false;
