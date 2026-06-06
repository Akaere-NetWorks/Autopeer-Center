INSERT INTO site_settings (setting_key, setting_value, description) VALUES
('endpoint_verification_enabled', 'true', 'Enable automatic endpoint verification'),
('endpoint_mismatch_threshold_minutes', '30', 'Minutes of endpoint mismatch before auto-suspend BGP')
ON CONFLICT (setting_key) DO NOTHING;
