ALTER TABLE node_metrics DROP CONSTRAINT IF EXISTS node_metrics_node_id_fkey;
ALTER TABLE node_metrics ADD CONSTRAINT node_metrics_node_id_fkey
    FOREIGN KEY (node_id) REFERENCES nodes(id);

ALTER TABLE peer_metrics DROP CONSTRAINT IF EXISTS peer_metrics_peer_id_fkey;
ALTER TABLE peer_metrics ADD CONSTRAINT peer_metrics_peer_id_fkey
    FOREIGN KEY (peer_id) REFERENCES peers(id);
