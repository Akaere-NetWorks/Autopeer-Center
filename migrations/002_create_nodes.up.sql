CREATE TABLE nodes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    location TEXT NOT NULL,
    agent_token TEXT NOT NULL,
    public_ip TEXT NOT NULL,
    our_asn BIGINT NOT NULL DEFAULT 4242420000,
    our_lla TEXT NOT NULL,
    our_wg_pubkey TEXT NOT NULL,
    bird_peer_dir TEXT NOT NULL DEFAULT '/etc/bird/dn42/peer/',
    wg_dir TEXT NOT NULL DEFAULT '/etc/wireguard/',
    wg_port_prefix INTEGER NOT NULL DEFAULT 5,
    online BOOLEAN NOT NULL DEFAULT false,
    enabled BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ DEFAULT now()
);
