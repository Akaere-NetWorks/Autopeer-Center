INSERT INTO site_settings (setting_key, setting_value, description)
VALUES ('auto_approve_peers', 'false', 'Automatically approve new peer requests without manual admin review')
ON CONFLICT (setting_key) DO NOTHING;
