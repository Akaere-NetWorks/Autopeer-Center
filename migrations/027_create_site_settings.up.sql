CREATE TABLE IF NOT EXISTS site_settings (
    id              SERIAL PRIMARY KEY,
    setting_key     TEXT UNIQUE NOT NULL,
    setting_value   TEXT NOT NULL DEFAULT 'true',
    description     TEXT NOT NULL DEFAULT '',
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO site_settings (setting_key, setting_value, description) VALUES
    ('user_login_enabled', 'true', 'User login portal open')
ON CONFLICT (setting_key) DO NOTHING;
