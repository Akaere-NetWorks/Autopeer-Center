CREATE TABLE IF NOT EXISTS bot_settings (
    id              SERIAL PRIMARY KEY,
    setting_key     TEXT UNIQUE NOT NULL,
    setting_value   TEXT NOT NULL DEFAULT '',
    description     TEXT NOT NULL DEFAULT '',
    updated_at      TIMESTAMPTZ DEFAULT now()
);

INSERT INTO bot_settings (setting_key, setting_value, description) VALUES
    ('bot_enabled', 'true', 'Enable or disable the Telegram bot'),
    ('bot_name', 'AutoPeer Bot', 'Display name for the bot'),
    ('bot_auth_token', '', 'Authentication token for bot WebSocket connection'),
    ('bot_require_auth', 'false', 'Require users to be in allowed list'),
    ('bot_allowed_users', '', 'Comma-separated Telegram user IDs allowed to use bot'),
    ('bot_notify_chat_id', '', 'Telegram chat ID for system notifications'),
    ('bot_ping_enabled', 'true', 'Enable /ping command'),
    ('bot_trace_enabled', 'true', 'Enable /trace command'),
    ('bot_status_enabled', 'true', 'Enable /status command'),
    ('bot_nodes_enabled', 'true', 'Enable /nodes command')
ON CONFLICT (setting_key) DO NOTHING;

CREATE TABLE IF NOT EXISTS bot_blocked_users (
    id          SERIAL PRIMARY KEY,
    tg_user_id  BIGINT UNIQUE NOT NULL,
    username    TEXT NOT NULL DEFAULT '',
    reason      TEXT NOT NULL DEFAULT '',
    blocked_by  TEXT NOT NULL DEFAULT '',
    blocked_at  TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE IF NOT EXISTS bot_command_stats (
    id          SERIAL PRIMARY KEY,
    command     TEXT NOT NULL,
    tg_user_id  BIGINT NOT NULL,
    username    TEXT NOT NULL DEFAULT '',
    chat_id     BIGINT NOT NULL,
    created_at  TIMESTAMPTZ DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_bot_command_stats_command ON bot_command_stats (command);
CREATE INDEX IF NOT EXISTS idx_bot_command_stats_created ON bot_command_stats (created_at);
CREATE INDEX IF NOT EXISTS idx_bot_command_stats_user ON bot_command_stats (tg_user_id);
