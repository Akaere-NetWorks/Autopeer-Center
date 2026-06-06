CREATE INDEX IF NOT EXISTS idx_audit_logs_peer_asn_detail
    ON audit_logs ((detail->>'asn'))
    WHERE action LIKE 'peer.%';

CREATE INDEX IF NOT EXISTS idx_peers_remote_asn_id
    ON peers (remote_asn, id);
