package repository

import (
	"context"
	"time"

	"github.com/akaere/autopeer-center/internal/model"
	"github.com/uptrace/bun"
)

type MetricsRepository interface {
	InsertPeerMetric(ctx context.Context, m *model.PeerMetric) error
	InsertPeerMetricFromHeartbeat(ctx context.Context, peerID, nodeID string, m *model.PeerMetric) error
	InsertNodeMetric(ctx context.Context, m *model.NodeMetric) error
	InsertBirdMetric(ctx context.Context, nodeID, iface, state string, routesImported, routesExported, routesPreferred, uptimeSecs int) error
	QueryPeerMetrics(ctx context.Context, peerID string, hours int) ([]*model.PeerMetric, error)
	GetLatestPeerMetric(ctx context.Context, peerID string) (*model.PeerMetric, error)
	GetLatestPeerMetricSummary(ctx context.Context, peerID string) (*PeerMetricSummary, error)
	LogRequest(ctx context.Context, entry *model.RequestLog) error
}

type PeerMetricSummary struct {
	RttMs             *float64
	BGPState          *string
	LastHandshake     *time.Time
	BGPUptimeSecs     *int
	WgActualEndpoint  *string
}

type bunMetricsRepository struct {
	baseRepo
}

func NewMetricsRepository(db *bun.DB) MetricsRepository {
	return &bunMetricsRepository{baseRepo{db: db}}
}

func (r *bunMetricsRepository) InsertPeerMetric(ctx context.Context, m *model.PeerMetric) error {
	_, err := r.db.NewInsert().Model(m).Exec(ctx)
	return err
}

func (r *bunMetricsRepository) InsertPeerMetricFromHeartbeat(ctx context.Context, peerID, nodeID string, m *model.PeerMetric) error {
	_, err := r.db.NewRaw(
		`INSERT INTO peer_metrics (time, peer_id, rx_bytes, tx_bytes, bgp_state, rtt_ms, last_handshake, routes_imported, routes_exported, routes_preferred, bgp_uptime_secs, wg_actual_endpoint)
		 SELECT now(), p.id, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
		 FROM peers p WHERE p.id = ? AND p.node_id = ?`,
		m.RxBytes, m.TxBytes, m.BGPState,
		m.RttMs, m.LastHandshake, m.RoutesImported,
		m.RoutesExported, m.RoutesPreferred, m.BGPUptimeSecs,
		m.WgActualEndpoint,
		peerID, nodeID,
	).Exec(ctx)
	return err
}

func (r *bunMetricsRepository) InsertNodeMetric(ctx context.Context, m *model.NodeMetric) error {
	_, err := r.db.NewInsert().Model(m).Exec(ctx)
	return err
}

func (r *bunMetricsRepository) InsertBirdMetric(ctx context.Context, nodeID, iface, state string, routesImported, routesExported, routesPreferred, uptimeSecs int) error {
	_, err := r.db.NewRaw(
		`INSERT INTO peer_metrics (time, peer_id, bgp_state, routes_imported, routes_exported, routes_preferred, bgp_uptime_secs)
		 SELECT now(), p.id, ?, ?, ?, ?, ?
		 FROM peers p WHERE p.node_id = ? AND p.wg_interface_name = ? AND p.status = 'active'
		 LIMIT 1`,
		state, routesImported, routesExported, routesPreferred, uptimeSecs,
		nodeID, iface,
	).Exec(ctx)
	return err
}

func (r *bunMetricsRepository) QueryPeerMetrics(ctx context.Context, peerID string, hours int) ([]*model.PeerMetric, error) {
	metrics := make([]*model.PeerMetric, 0)
	err := r.db.NewSelect().
		Model(&metrics).
		Where("peer_id = ?", peerID).
		Where("time > now() - make_interval(hours => ?)", hours).
		OrderExpr("time ASC").
		Scan(ctx)
	return metrics, err
}

func (r *bunMetricsRepository) GetLatestPeerMetric(ctx context.Context, peerID string) (*model.PeerMetric, error) {
	var m model.PeerMetric
	err := r.db.NewSelect().
		Model(&m).
		Where("peer_id = ?", peerID).
		Where("time > now() - interval '10 minutes'").
		OrderExpr("time DESC").
		Limit(1).
		Scan(ctx)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (r *bunMetricsRepository) GetLatestPeerMetricSummary(ctx context.Context, peerID string) (*PeerMetricSummary, error) {
	var s PeerMetricSummary
	err := r.db.NewRaw(
		`SELECT rtt_ms, bgp_state, last_handshake, bgp_uptime_secs, wg_actual_endpoint
		 FROM peer_metrics
		 WHERE peer_id = ? AND time > now() - INTERVAL '10 minutes'
		 ORDER BY time DESC LIMIT 1`, peerID,
	).Scan(ctx, &s.RttMs, &s.BGPState, &s.LastHandshake, &s.BGPUptimeSecs, &s.WgActualEndpoint)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *bunMetricsRepository) LogRequest(ctx context.Context, entry *model.RequestLog) error {
	_, err := r.db.NewInsert().Model(entry).Exec(ctx)
	return err
}
