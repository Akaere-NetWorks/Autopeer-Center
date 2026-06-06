ALTER TABLE bot_settings ADD COLUMN IF NOT EXISTS setting_value_hash TEXT;

UPDATE bot_settings SET setting_value_hash = '' WHERE setting_key = 'bot_auth_token';
