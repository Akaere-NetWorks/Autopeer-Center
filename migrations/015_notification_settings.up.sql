CREATE TABLE IF NOT EXISTS notification_settings (
    id              SERIAL PRIMARY KEY,
    setting_key     TEXT UNIQUE NOT NULL,
    setting_value   TEXT NOT NULL DEFAULT 'true',
    threshold_value TEXT NOT NULL DEFAULT '',
    description     TEXT NOT NULL DEFAULT '',
    updated_at      TIMESTAMPTZ DEFAULT now()
);

INSERT INTO notification_settings (setting_key, setting_value, threshold_value, description) VALUES
    ('peer_suspended_enabled', 'true', '', 'Notify user when their peer is suspended'),
    ('peer_deleted_enabled', 'true', '', 'Notify user when their peer is deleted'),
    ('peer_bgp_down_enabled', 'true', '3', 'Notify user when BGP goes down. Threshold: consecutive heartbeat failures before alert'),
    ('peer_bgp_recovered_enabled', 'true', '', 'Notify user when BGP recovers after a down alert'),
    ('peer_handshake_stale_enabled', 'false', '5', 'Notify user when WG handshake is stale. Threshold: minutes since last handshake'),
    ('node_offline_enabled', 'true', '3', 'Notify admin when a node goes offline. Threshold: minutes without heartbeat'),
    ('agent_updated_enabled', 'true', '', 'Notify admin when agent self-update completes')
ON CONFLICT (setting_key) DO NOTHING;
