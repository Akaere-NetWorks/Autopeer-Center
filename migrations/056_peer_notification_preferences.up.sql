CREATE TABLE peer_notification_preferences (
    asn              BIGINT NOT NULL,
    channel          TEXT NOT NULL,
    notification_key TEXT NOT NULL,
    enabled          BOOLEAN NOT NULL DEFAULT false,
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (asn, channel, notification_key)
);

CREATE TABLE peer_notification_preference_states (
    asn                  BIGINT PRIMARY KEY,
    seen_catalog_version INTEGER NOT NULL DEFAULT 0,
    wizard_completed_at  TIMESTAMPTZ,
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);
