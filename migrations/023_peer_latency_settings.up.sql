INSERT INTO notification_settings (setting_key, setting_value, threshold_value, description) VALUES
    ('peer_latency_high_enabled', 'true', '', 'Enable latency anomaly alert for peers'),
    ('peer_latency_high_threshold_ms', 'true', '200', 'Absolute RTT threshold in ms to consider abnormal'),
    ('peer_latency_high_multiplier', 'true', '2.0', 'Relative multiplier over baseline RTT to consider abnormal'),
    ('peer_latency_high_sustained_count', 'true', '2', 'Consecutive abnormal readings required to trigger sustained alert'),
    ('peer_latency_high_count_threshold', 'true', '20', 'Abnormal reading count in sliding window to trigger count-based alert'),
    ('peer_latency_high_window_minutes', 'true', '60', 'Sliding window size in minutes for count-based alert'),
    ('peer_latency_high_notify_admin', 'true', '', 'Also notify admin when a latency alert is triggered'),
    ('peer_latency_high_cooldown_hours', 'true', '24', 'Cooldown period in hours after alert before re-alerting'),
    ('peer_latency_recovered_enabled', 'true', '', 'Notify when latency returns to normal after an alert')
ON CONFLICT (setting_key) DO NOTHING;
