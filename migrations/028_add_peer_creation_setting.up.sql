INSERT INTO site_settings (setting_key, setting_value, description) VALUES
    ('peer_creation_enabled', 'true', 'Allow users to create new peers')
ON CONFLICT (setting_key) DO NOTHING;
