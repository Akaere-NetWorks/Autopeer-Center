ALTER TABLE peers ADD CONSTRAINT peers_node_port_unique UNIQUE (node_id, wg_listen_port);
