package repository

import (
	"context"
	"time"

	"github.com/uptrace/bun"
)

type StatsRepository interface {
	Public(ctx context.Context) (*PublicStats, error)
	PublicNodeStats(ctx context.Context) ([]*NodePublicStats, error)
	Admin(ctx context.Context) (*AdminStats, error)
	AdminListNodes(ctx context.Context) ([]*AdminNodeListItem, error)
}

type AdminNodeListItem struct {
	ID            string    `bun:"id"`
	Name          string    `bun:"name"`
	Location      string    `bun:"location"`
	PublicIP      string    `bun:"public_ip"`
	OurASN        int64     `bun:"our_asn"`
	OurLLA        string    `bun:"our_lla"`
	OurWgPubkey   string    `bun:"our_wg_pubkey"`
	BirdPeerDir   string    `bun:"bird_peer_dir"`
	WgDir         string    `bun:"wg_dir"`
	WgPortPrefix  int       `bun:"wg_port_prefix"`
	Online        bool      `bun:"online"`
	Enabled       bool      `bun:"enabled"`
	AgentVersion  string    `bun:"agent_version"`
	AgentState    string    `bun:"agent_state"`
	CreatedAt     time.Time `bun:"created_at"`
	AuthMode      string    `bun:"auth_mode"`
	ActivePeers   int       `bun:"active_peers"`
	AvgRttMs      *float64  `bun:"avg_rtt_ms"`
	LastSeen      *string   `bun:"last_seen"`
	TotalRxMB     *float64  `bun:"total_rx_mb"`
	TotalTxMB     *float64  `bun:"total_tx_mb"`
	MemAllocMB    *int64    `bun:"mem_alloc_mb"`
	MemSysMB      *int64    `bun:"mem_sys_mb"`
	NumGoroutine  *int      `bun:"num_goroutine"`
	UptimeSecs    *int64    `bun:"uptime_secs"`
}

type PublicStats struct {
	ActivePeers int     `bun:"active_peers"`
	OnlineNodes int     `bun:"online_nodes"`
	RxBytes24h  int64   `bun:"rx_bytes_24h"`
	TxBytes24h  int64   `bun:"tx_bytes_24h"`
	AvgRtt      float64 `bun:"avg_rtt"`
}

type NodePublicStats struct {
	ID           string   `bun:"id"`
	Name         string   `bun:"name"`
	Location     string   `bun:"location"`
	Online       bool     `bun:"online"`
	ActivePeers  int      `bun:"active_peers"`
	PendingPeers int      `bun:"pending_peers"`
	AvgRtt       *float64 `bun:"avg_rtt"`
	RxBytes24h   int64    `bun:"rx_bytes_24h"`
	TxBytes24h   int64    `bun:"tx_bytes_24h"`
}

type AdminStats struct {
	ActivePeers    int `bun:"active"`
	PendingPeers   int `bun:"pending"`
	SuspendedPeers int `bun:"suspended"`
	RejectedPeers  int `bun:"rejected"`
	OnlineNodes    int `bun:"online_nodes"`
	TotalNodes     int `bun:"total_nodes"`
	PeersLast24h   int `bun:"peers_last_24h"`
}

type bunStatsRepository struct {
	baseRepo
}

func NewStatsRepository(db *bun.DB) StatsRepository {
	return &bunStatsRepository{baseRepo{db: db}}
}

func (r *bunStatsRepository) Public(ctx context.Context) (*PublicStats, error) {
	var s PublicStats
	err := r.db.NewRaw(`
		SELECT
			(SELECT COUNT(*) FROM nodes WHERE online = true) as online_nodes,
			(SELECT COUNT(*) FROM peers WHERE status = 'active') as active_peers,
			(SELECT COALESCE(SUM(pm.rx_delta), 0) FROM (
				SELECT peer_id, (MAX(rx_bytes) - MIN(rx_bytes)) AS rx_delta
				FROM peer_metrics WHERE time > now() - INTERVAL '24 hours' GROUP BY peer_id
			) pm) as rx_bytes_24h,
			(SELECT COALESCE(SUM(pm.tx_delta), 0) FROM (
				SELECT peer_id, (MAX(tx_bytes) - MIN(tx_bytes)) AS tx_delta
				FROM peer_metrics WHERE time > now() - INTERVAL '24 hours' GROUP BY peer_id
			) pm) as tx_bytes_24h,
			(SELECT COALESCE(AVG(rtt_ms), 0) FROM (
				SELECT DISTINCT ON (peer_id) rtt_ms FROM peer_metrics
				WHERE rtt_ms IS NOT NULL AND time > now() - INTERVAL '10 minutes'
				ORDER BY peer_id, time DESC
			) sub) as avg_rtt
	`).Scan(ctx, &s)
	return &s, err
}

func (r *bunStatsRepository) PublicNodeStats(ctx context.Context) ([]*NodePublicStats, error) {
	results := make([]*NodePublicStats, 0)
	err := r.db.NewRaw(`
		SELECT n.id, n.name, n.location, n.online,
		       COUNT(p.id) FILTER (WHERE p.status = 'active') as active_peers,
		       COUNT(p.id) FILTER (WHERE p.status = 'pending') as pending_peers,
		       (SELECT AVG(rtt_ms) FROM (
		           SELECT DISTINCT ON (pm.peer_id) rtt_ms FROM peer_metrics pm
		           WHERE pm.peer_id IN (SELECT id FROM peers WHERE node_id = n.id AND status = 'active')
		           AND rtt_ms IS NOT NULL AND pm.time > now() - INTERVAL '10 minutes'
		           ORDER BY pm.peer_id, pm.time DESC
		       ) sub) as avg_rtt,
		       COALESCE((SELECT SUM(delta) FROM (
		           SELECT GREATEST(MAX(rx_bytes) - MIN(rx_bytes), 0) AS delta
		           FROM peer_metrics
		           WHERE peer_id IN (SELECT id FROM peers WHERE node_id = n.id)
		           AND time > now() - INTERVAL '24 hours'
		           GROUP BY peer_id
		       ) per_peer), 0) as rx_bytes_24h,
		       COALESCE((SELECT SUM(delta) FROM (
		           SELECT GREATEST(MAX(tx_bytes) - MIN(tx_bytes), 0) AS delta
		           FROM peer_metrics
		           WHERE peer_id IN (SELECT id FROM peers WHERE node_id = n.id)
		           AND time > now() - INTERVAL '24 hours'
		           GROUP BY peer_id
		       ) per_peer), 0) as tx_bytes_24h
		FROM nodes n
		LEFT JOIN peers p ON p.node_id = n.id
		WHERE n.enabled = true
		GROUP BY n.id, n.name, n.location, n.online
		ORDER BY n.name
	`).Scan(ctx, &results)
	return results, err
}

func (r *bunStatsRepository) Admin(ctx context.Context) (*AdminStats, error) {
	var s AdminStats
	err := r.db.NewRaw(`
		SELECT
			COUNT(*) FILTER (WHERE status = 'active') as active,
			COUNT(*) FILTER (WHERE status = 'pending') as pending,
			COUNT(*) FILTER (WHERE status = 'suspended') as suspended,
			COUNT(*) FILTER (WHERE status = 'rejected') as rejected
		FROM peers
	`).Scan(ctx, &s.ActivePeers, &s.PendingPeers, &s.SuspendedPeers, &s.RejectedPeers)
	if err != nil {
		return nil, err
	}

	err = r.db.NewRaw(`
		SELECT COUNT(*) FILTER (WHERE online = true) as online_nodes,
		       COUNT(*) as total_nodes
		FROM nodes
	`).Scan(ctx, &s.OnlineNodes, &s.TotalNodes)
	if err != nil {
		return nil, err
	}

	err = r.db.NewRaw(`
		SELECT COUNT(*) FROM peers WHERE created_at > now() - INTERVAL '24 hours'
	`).Scan(ctx, &s.PeersLast24h)
	return &s, err
}

func (r *bunStatsRepository) AdminListNodes(ctx context.Context) ([]*AdminNodeListItem, error) {
	results := make([]*AdminNodeListItem, 0)
	err := r.db.NewRaw(`
		SELECT
			n.id, n.name, n.location, n.public_ip, n.our_asn, n.our_lla, n.our_wg_pubkey,
			n.bird_peer_dir, n.wg_dir, n.wg_port_prefix, n.online, n.enabled,
			COALESCE(n.agent_version,'') as agent_version, COALESCE(n.agent_state,'offline') as agent_state, n.created_at,
			CASE WHEN n.agent_pubkey != '' THEN 'keypair' ELSE 'token' END as auth_mode,
			COALESCE(peer_counts.active_peers, 0) as active_peers,
			rtt_stats.avg_rtt_ms,
			traffic.total_rx_mb,
			traffic.total_tx_mb,
			nm.mem_alloc_mb, nm.mem_sys_mb, nm.num_goroutine, nm.uptime_secs,
			TO_CHAR(n.agent_state_changed_at, 'YYYY-MM-DD"T"HH24:MI:SS"Z"') as last_seen
		FROM nodes n
		LEFT JOIN (
			SELECT node_id, COUNT(*) as active_peers
			FROM peers WHERE status = 'active'
			GROUP BY node_id
		) peer_counts ON n.id = peer_counts.node_id
		LEFT JOIN (
			SELECT p.node_id, AVG(pm.rtt_ms) as avg_rtt_ms
			FROM peer_metrics pm
			JOIN peers p ON pm.peer_id = p.id
			WHERE pm.time > now() - INTERVAL '10 minutes' AND pm.rtt_ms > 0
			GROUP BY p.node_id
		) rtt_stats ON n.id = rtt_stats.node_id
		LEFT JOIN (
			SELECT node_id,
				SUM(rx_delta) / 1048576.0 as total_rx_mb,
				SUM(tx_delta) / 1048576.0 as total_tx_mb
			FROM (
				SELECT p.node_id,
					GREATEST(MAX(pm.rx_bytes) - MIN(pm.rx_bytes), 0) AS rx_delta,
					GREATEST(MAX(pm.tx_bytes) - MIN(pm.tx_bytes), 0) AS tx_delta
				FROM peer_metrics pm
				JOIN peers p ON pm.peer_id = p.id
				WHERE pm.time > now() - INTERVAL '24 hours'
				GROUP BY p.node_id, pm.peer_id
			) per_peer
			GROUP BY node_id
		) traffic ON n.id = traffic.node_id
		LEFT JOIN LATERAL (
			SELECT mem_alloc_mb, mem_sys_mb, num_goroutine, uptime_secs
			FROM node_metrics
			WHERE node_id = n.id
			ORDER BY time DESC LIMIT 1
		) nm ON true
		ORDER BY n.name
	`).Scan(ctx, &results)
	return results, err
}

type LatencyRepository interface {
	ListActivePeersWithNode(ctx context.Context) ([]*ActivePeerWithNode, error)
	GetPeerRttPercentile(ctx context.Context, peerID string) (*float64, error)
	GetPeerRecentRtt(ctx context.Context, peerID string, windowMinutes int) ([]*RttPoint, error)
}

type ActivePeerWithNode struct {
	PeerID       string `bun:"peer_id"`
	ContactEmail string `bun:"contact_email"`
	RemoteASN    int64  `bun:"remote_asn"`
	NodeName     string `bun:"node_name"`
}

type RttPoint struct {
	Time  bun.NullTime `bun:"time"`
	RttMs *float64     `bun:"rtt_ms"`
}

type bunLatencyRepository struct {
	baseRepo
}

func NewLatencyRepository(db *bun.DB) LatencyRepository {
	return &bunLatencyRepository{baseRepo{db: db}}
}

func (r *bunLatencyRepository) ListActivePeersWithNode(ctx context.Context) ([]*ActivePeerWithNode, error) {
	results := make([]*ActivePeerWithNode, 0)
	err := r.db.NewRaw(`
		SELECT p.id as peer_id, p.contact_email, p.remote_asn, n.name as node_name
		FROM peers p JOIN nodes n ON p.node_id = n.id
		WHERE p.status = 'active' AND n.online = true
	`).Scan(ctx, &results)
	return results, err
}

func (r *bunLatencyRepository) GetPeerRttPercentile(ctx context.Context, peerID string) (*float64, error) {
	var result *float64
	err := r.db.NewRaw(`
		SELECT PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY rtt_ms)
		FROM peer_metrics
		WHERE peer_id = ? AND rtt_ms IS NOT NULL AND rtt_ms > 0
		  AND time > now() - interval '24 hours' AND time < now() - interval '5 minutes'
	`, peerID).Scan(ctx, &result)
	return result, err
}

func (r *bunLatencyRepository) GetPeerRecentRtt(ctx context.Context, peerID string, windowMinutes int) ([]*RttPoint, error) {
	points := make([]*RttPoint, 0)
	err := r.db.NewRaw(`
		SELECT time, rtt_ms FROM peer_metrics
		WHERE peer_id = ? AND rtt_ms IS NOT NULL AND rtt_ms > 0
		  AND time > now() - (? * interval '1 minute')
		ORDER BY time
	`, peerID, windowMinutes).Scan(ctx, &points)
	return points, err
}
