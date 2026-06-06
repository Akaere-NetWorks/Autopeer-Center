CREATE TABLE peers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    node_id UUID NOT NULL REFERENCES nodes(id),
    remote_asn INTEGER NOT NULL,
    remote_pubkey TEXT NOT NULL,
    remote_endpoint TEXT NOT NULL,
    remote_lla TEXT NOT NULL,
    contact_email TEXT NOT NULL,
    wg_listen_port INTEGER NOT NULL,
    wg_interface_name TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    reject_reason TEXT,
    created_at TIMESTAMPTZ DEFAULT now(),
    updated_at TIMESTAMPTZ DEFAULT now(),
    UNIQUE (node_id, remote_asn)
);
