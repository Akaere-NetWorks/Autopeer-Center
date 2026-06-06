CREATE TABLE bot_web_bind_tokens (
    id         SERIAL PRIMARY KEY,
    token      TEXT NOT NULL UNIQUE,
    asn        BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL,
    used       BOOLEAN NOT NULL DEFAULT false
);
