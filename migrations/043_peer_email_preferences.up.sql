CREATE TABLE peer_email_preferences (
    asn        BIGINT PRIMARY KEY,
    email_level INTEGER NOT NULL DEFAULT 0 CHECK (email_level BETWEEN 0 AND 3),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
