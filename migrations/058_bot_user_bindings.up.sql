CREATE TABLE bot_user_bindings (
    id          SERIAL PRIMARY KEY,
    tg_user_id  BIGINT NOT NULL UNIQUE,
    tg_username TEXT,
    asn         BIGINT NOT NULL,
    is_admin    BOOLEAN NOT NULL DEFAULT false,
    bound_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    bound_via   TEXT NOT NULL DEFAULT 'code'
);

CREATE INDEX idx_bot_user_bindings_asn ON bot_user_bindings(asn);
