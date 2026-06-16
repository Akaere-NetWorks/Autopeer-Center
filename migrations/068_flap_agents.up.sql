-- flap_agents holds the allowlist of flapalerted-agent identities that may
-- connect to the flap-agent WebSocket. Previously this lived in the
-- FLAP_AGENT_TOKENS / FLAP_AGENT_PUBKEYS environment variables; it is now
-- managed by admins in the dashboard and persisted here.
--
-- Authentication mirrors the node agent: the connection is bootstrapped with a
-- bearer token, then upgraded to an X25519 key exchange. The first public key
-- presented by an agent is pinned (TOFU) in agent_pubkey; mismatches are
-- rejected until an admin clears it.
CREATE TABLE flap_agents (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id      TEXT NOT NULL UNIQUE,
    name          TEXT NOT NULL DEFAULT '',
    description   TEXT NOT NULL DEFAULT '',
    token         TEXT NOT NULL,
    agent_pubkey  TEXT NOT NULL DEFAULT '',
    enabled       BOOLEAN NOT NULL DEFAULT true,
    version       TEXT NOT NULL DEFAULT '',
    last_seen_at  TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_flap_agents_token ON flap_agents(token);
