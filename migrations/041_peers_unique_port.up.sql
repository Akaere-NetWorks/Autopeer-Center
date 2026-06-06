-- H-6: Add UNIQUE constraint on (node_id, wg_listen_port) to prevent two peers
-- sharing a port on the same node when concurrent requests race past the
-- application-level EXISTS check.
ALTER TABLE peers
    ADD CONSTRAINT uq_peers_node_port UNIQUE (node_id, wg_listen_port);
