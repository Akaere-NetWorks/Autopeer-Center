UPDATE nodes SET agent_state = 'offline' WHERE online = false AND agent_state = 'online';
ALTER TABLE nodes ALTER COLUMN agent_state SET DEFAULT 'offline';
