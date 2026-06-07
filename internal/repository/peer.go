package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/akaere/autopeer-center/internal/model"
	"github.com/uptrace/bun"
)

type PeerRepository interface {
	GetByID(ctx context.Context, id string) (*model.Peer, error)
	GetByIDWithNode(ctx context.Context, id string) (*PeerWithNodeResult, error)
	GetByIDAndASN(ctx context.Context, id string, asn int64) (*model.Peer, error)
	GetByIDAndASNWithNode(ctx context.Context, id string, asn int64) (*PeerDetailResult, error)
	ListByASN(ctx context.Context, asn int64) ([]*PeerListResult, error)
	ListByNode(ctx context.Context, nodeID string) ([]*model.Peer, error)
	ListActiveByNode(ctx context.Context, nodeID string) ([]*PeerContactResult, error)
	List(ctx context.Context, params ListParams) ([]*AdminPeerListResult, int, error)
	ListAll(ctx context.Context) ([]*model.Peer, error)
	Create(ctx context.Context, p *model.Peer) error
	UpdateStatus(ctx context.Context, id, status string, opts ...UpdateOption) error
	UpdateFields(ctx context.Context, id string, fields map[string]interface{}) error
	UpdateContactEmail(ctx context.Context, id, email string) error
	Delete(ctx context.Context, id string) error
	ExistsByNodeAndASN(ctx context.Context, nodeID string, asn int64) (bool, error)
	ExistsByNodeAndPort(ctx context.Context, nodeID string, port int) (bool, error)
	CountPendingByASN(ctx context.Context, asn int64) (int, error)
	ImportPeer(ctx context.Context, p *model.Peer) (string, error)
	ImportPeerFull(ctx context.Context, p *model.Peer, overwrite bool) (string, error)
	SummaryByASN(ctx context.Context, asn int64) ([]*PeerSummaryResult, error)
	ListActivePeersForEndpointCheck(ctx context.Context) ([]PeerEndpointCheck, error)
	SetEndpointMismatchSince(ctx context.Context, peerID string, since *time.Time) error
	SetBGPSuspendedByEndpoint(ctx context.Context, peerID string, suspended bool) error
	ListPeersSuspendedByEndpoint(ctx context.Context) ([]PeerEndpointCheck, error)
}

// flatPeerBase holds all peer columns explicitly so bun can map by column name
// when scanning flat JOINed queries (no embedded struct ambiguity).
type flatPeerBase struct {
	ID                 string    `bun:"id"`
	NodeID             string    `bun:"node_id"`
	RemoteASN          int64     `bun:"remote_asn"`
	RemotePubkey       string    `bun:"remote_pubkey"`
	RemoteEndpoint     string    `bun:"remote_endpoint"`
	RemoteLLA          string    `bun:"remote_lla"`
	ContactEmail       string    `bun:"contact_email"`
	WgListenPort       int       `bun:"wg_listen_port"`
	WgInterfaceName    string    `bun:"wg_interface_name"`
	WgManaged          bool      `bun:"wg_managed"`
	Status             string    `bun:"status"`
	RejectReason            *string   `bun:"reject_reason"`
	EndpointMismatchSince   *time.Time `bun:"endpoint_mismatch_since"`
	BGPSuspendedByEndpoint  bool      `bun:"bgp_suspended_by_endpoint"`
	CreatedAt               time.Time `bun:"created_at"`
	UpdatedAt          time.Time `bun:"updated_at"`
	MTU                *int      `bun:"mtu"`
	WgPreSharedKey     *string   `bun:"wg_preshared_key"`
	BgpProtoName       string    `bun:"bgp_proto_name"`
	BirdConfigFilename string    `bun:"bird_config_filename"`
}

func (f flatPeerBase) ToPeer() model.Peer {
	return model.Peer{
		ID:                 f.ID,
		NodeID:             f.NodeID,
		RemoteASN:          f.RemoteASN,
		RemotePubkey:       f.RemotePubkey,
		RemoteEndpoint:     f.RemoteEndpoint,
		RemoteLLA:          f.RemoteLLA,
		ContactEmail:       f.ContactEmail,
		WgListenPort:       f.WgListenPort,
		WgInterfaceName:    f.WgInterfaceName,
		WgManaged:          f.WgManaged,
		Status:             f.Status,
		RejectReason:            f.RejectReason,
		EndpointMismatchSince:   f.EndpointMismatchSince,
		BGPSuspendedByEndpoint:  f.BGPSuspendedByEndpoint,
		CreatedAt:               f.CreatedAt,
		UpdatedAt:          f.UpdatedAt,
		MTU:                f.MTU,
		WgPreSharedKey:     f.WgPreSharedKey,
		BgpProtoName:       f.BgpProtoName,
		BirdConfigFilename: f.BirdConfigFilename,
	}
}

type PeerWithNodeResult struct {
	flatPeerBase
	NodeName     string `bun:"node_name"`
	NodeLocation string `bun:"node_location"`
}

func (r *PeerWithNodeResult) Peer() model.Peer { return r.flatPeerBase.ToPeer() }

type PeerDetailResult struct {
	flatPeerBase
	NodeName        string `bun:"node_name"`
	NodeLocation    string `bun:"node_location"`
	NodePublicIP    string `bun:"node_public_ip"`
	NodeOurLLA      string `bun:"node_our_lla"`
	NodeOurWgPubkey string `bun:"node_our_wg_pubkey"`
}

func (r *PeerDetailResult) Peer() model.Peer { return r.flatPeerBase.ToPeer() }

type PeerListResult struct {
	flatPeerBase
	NodeName     string `bun:"node_name"`
	NodeLocation string `bun:"node_location"`
}

func (r *PeerListResult) Peer() model.Peer { return r.flatPeerBase.ToPeer() }

type PeerContactResult struct {
	PeerID string `bun:"id"`
	Email  string `bun:"contact_email"`
	Name   string `bun:"name"`
}

type AdminPeerListResult struct {
	flatPeerBase
	NodeName              string    `bun:"node_name"`
	NodeLocation          string    `bun:"node_location"`
	LatestRtt             *float64  `bun:"rtt_ms"`
	LatestBGPState        *string   `bun:"bgp_state"`
	RxBytes24h            int64     `bun:"rx_delta"`
	TxBytes24h            int64     `bun:"tx_delta"`
	EndpointMismatchSince *time.Time `bun:"endpoint_mismatch_since"`
	BGPSuspendedByEndpoint bool      `bun:"bgp_suspended_by_endpoint"`
}

func (r *AdminPeerListResult) Peer() model.Peer { return r.flatPeerBase.ToPeer() }

type PeerSummaryResult struct {
	PeerID          string     `bun:"id"`
	LatestRtt       *float64   `bun:"rtt_ms"`
	LatestBGPState  *string    `bun:"bgp_state"`
	LatestHandshake *time.Time `bun:"last_handshake"`
	LatestBGPUptime *int       `bun:"bgp_uptime_secs"`
}

type PeerEndpointCheck struct {
	PeerID                 string
	NodeID                 string
	RemoteASN              int64
	RemoteEndpoint         string
	WgActualEndpoint       string
	BgpProtoName           string
	ContactEmail           string
	NodeName               string
	EndpointMismatchSince  *time.Time
	BGPSuspendedByEndpoint bool
}

type bunPeerRepository struct {
	baseRepo
}

func NewPeerRepository(db *bun.DB) PeerRepository {
	return &bunPeerRepository{baseRepo{db: db}}
}

func (r *bunPeerRepository) GetByID(ctx context.Context, id string) (*model.Peer, error) {
	var p model.Peer
	err := r.db.NewSelect().Model(&p).Where("id = ?", id).Scan(ctx)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *bunPeerRepository) GetByIDWithNode(ctx context.Context, id string) (*PeerWithNodeResult, error) {
	var result PeerWithNodeResult
	err := r.db.NewRaw(`
		SELECT p.id, p.node_id, p.remote_asn, p.remote_pubkey, p.remote_endpoint, p.remote_lla,
		       p.contact_email, p.wg_listen_port, p.wg_interface_name, p.wg_managed, p.status, p.reject_reason,
		       p.endpoint_mismatch_since, p.bgp_suspended_by_endpoint,
		       p.created_at, p.updated_at, p.mtu, p.wg_preshared_key, p.bgp_proto_name, p.bird_config_filename,
		       n.name AS node_name, n.location AS node_location
		FROM peers p JOIN nodes n ON p.node_id = n.id
		WHERE p.id = ?`, id,
	).Scan(ctx, &result)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (r *bunPeerRepository) GetByIDAndASN(ctx context.Context, id string, asn int64) (*model.Peer, error) {
	var p model.Peer
	err := r.db.NewSelect().Model(&p).Where("id = ? AND remote_asn = ?", id, asn).Scan(ctx)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *bunPeerRepository) GetByIDAndASNWithNode(ctx context.Context, id string, asn int64) (*PeerDetailResult, error) {
	var result PeerDetailResult
	err := r.db.NewRaw(`
		SELECT p.id, p.node_id, p.remote_asn, p.remote_pubkey, p.remote_endpoint, p.remote_lla,
		       p.contact_email, p.wg_listen_port, p.wg_interface_name, p.wg_managed, p.status, p.reject_reason,
		       p.endpoint_mismatch_since, p.bgp_suspended_by_endpoint,
		       p.created_at, p.updated_at, p.mtu, p.wg_preshared_key, p.bgp_proto_name, p.bird_config_filename,
		       n.name AS node_name, n.location AS node_location,
		       n.public_ip AS node_public_ip, n.our_lla AS node_our_lla, n.our_wg_pubkey AS node_our_wg_pubkey
		FROM peers p JOIN nodes n ON p.node_id = n.id
		WHERE p.id = ? AND p.remote_asn = ?`, id, asn,
	).Scan(ctx, &result)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (r *bunPeerRepository) ListByASN(ctx context.Context, asn int64) ([]*PeerListResult, error) {
	results := make([]*PeerListResult, 0)
	err := r.db.NewRaw(`
		SELECT p.id, p.node_id, p.remote_asn, p.remote_pubkey, p.remote_endpoint, p.remote_lla,
		       p.contact_email, p.wg_listen_port, p.wg_interface_name, p.wg_managed, p.status, p.reject_reason,
		       p.endpoint_mismatch_since, p.bgp_suspended_by_endpoint,
		       p.created_at, p.updated_at, p.mtu, p.wg_preshared_key, p.bgp_proto_name, p.bird_config_filename,
		       n.name AS node_name, n.location AS node_location
		FROM peers p JOIN nodes n ON p.node_id = n.id
		WHERE p.remote_asn = ? ORDER BY p.created_at DESC`, asn,
	).Scan(ctx, &results)
	return results, err
}

func (r *bunPeerRepository) ListByNode(ctx context.Context, nodeID string) ([]*model.Peer, error) {
	peers := make([]*model.Peer, 0)
	err := r.db.NewSelect().Model(&peers).Where("node_id = ?", nodeID).Scan(ctx)
	return peers, err
}

func (r *bunPeerRepository) ListActiveByNode(ctx context.Context, nodeID string) ([]*PeerContactResult, error) {
	results := make([]*PeerContactResult, 0)
	err := r.db.NewRaw(`
		SELECT p.id, p.contact_email, n.name
		FROM peers p JOIN nodes n ON p.node_id = n.id
		WHERE p.node_id = ? AND p.status = 'active'`, nodeID,
	).Scan(ctx, &results)
	return results, err
}

func (r *bunPeerRepository) List(ctx context.Context, params ListParams) ([]*AdminPeerListResult, int, error) {
	if params.Page < 1 {
		params.Page = 1
	}
	if params.PageSize < 1 {
		params.PageSize = 20
	}

	where := " WHERE 1=1"
	var args []interface{}

	if params.Status != "" {
		where += " AND p.status = ?"
		args = append(args, params.Status)
	}
	if params.ASN > 0 {
		where += " AND p.remote_asn = ?"
		args = append(args, params.ASN)
	}
	if params.NodeID != "" {
		where += " AND p.node_id = ?"
		args = append(args, params.NodeID)
	}

	var total int
	countArgs := make([]interface{}, len(args))
	copy(countArgs, args)
	err := r.db.NewRaw(
		"SELECT COUNT(*) FROM peers p JOIN nodes n ON p.node_id = n.id"+where, countArgs...,
	).Scan(ctx, &total)
	if err != nil {
		return nil, 0, err
	}

	dataQuery := `SELECT p.id, p.node_id, p.remote_asn, p.remote_pubkey, p.remote_endpoint, p.remote_lla,
	                     p.contact_email, p.wg_listen_port, p.wg_interface_name, p.wg_managed, p.status, p.reject_reason,
	                     p.created_at, p.updated_at, p.mtu, p.wg_preshared_key, p.bgp_proto_name, p.bird_config_filename,
	                     p.endpoint_mismatch_since, p.bgp_suspended_by_endpoint,
	                     n.name AS node_name, n.location AS node_location,
	                     pm_latest.rtt_ms, pm_latest.bgp_state,
	                     COALESCE(pm_24h.rx_delta, 0) as rx_delta, COALESCE(pm_24h.tx_delta, 0) as tx_delta
	              FROM peers p
	              JOIN nodes n ON p.node_id = n.id
	              LEFT JOIN LATERAL (
	                  SELECT rtt_ms, bgp_state
	                  FROM peer_metrics
	                  WHERE peer_id = p.id
	                  ORDER BY time DESC LIMIT 1
	              ) pm_latest ON true
	              LEFT JOIN LATERAL (
	                  SELECT (MAX(rx_bytes) - MIN(rx_bytes)) AS rx_delta,
	                         (MAX(tx_bytes) - MIN(tx_bytes)) AS tx_delta
	                  FROM peer_metrics
	                  WHERE peer_id = p.id AND time > now() - INTERVAL '24 hours'
	              ) pm_24h ON true` + where + " ORDER BY p.created_at DESC LIMIT ? OFFSET ?"

	dataArgs := append(args, params.PageSize, (params.Page-1)*params.PageSize)

	var results []*AdminPeerListResult
	err = r.db.NewRaw(dataQuery, dataArgs...).Scan(ctx, &results)
	return results, total, err
}

func (r *bunPeerRepository) ListAll(ctx context.Context) ([]*model.Peer, error) {
	peers := make([]*model.Peer, 0)
	err := r.db.NewSelect().Model(&peers).Order("created_at ASC").Scan(ctx)
	return peers, err
}

func (r *bunPeerRepository) Create(ctx context.Context, p *model.Peer) error {
	_, err := r.db.NewInsert().Model(p).Returning("id").Exec(ctx)
	return err
}

func (r *bunPeerRepository) UpdateStatus(ctx context.Context, id, status string, opts ...UpdateOption) error {
	fields := map[string]interface{}{
		"status":     status,
		"updated_at": "now()",
	}
	for _, opt := range opts {
		opt(fields)
	}

	q := r.db.NewUpdate().Model((*model.Peer)(nil)).Where("id = ?", id)
	for k, v := range fields {
		if v == "now()" {
			q = q.Set("? = now()", bun.Safe(k))
		} else {
			q = q.Set("? = ?", bun.Safe(k), v)
		}
	}
	_, err := q.Exec(ctx)
	return err
}

func (r *bunPeerRepository) UpdateFields(ctx context.Context, id string, fields map[string]interface{}) error {
	q := r.db.NewUpdate().Model((*model.Peer)(nil)).Where("id = ?", id)
	for k, v := range fields {
		if v == "now()" {
			q = q.Set("? = now()", bun.Safe(k))
		} else {
			q = q.Set("? = ?", bun.Safe(k), v)
		}
	}
	_, err := q.Exec(ctx)
	return err
}

func (r *bunPeerRepository) UpdateContactEmail(ctx context.Context, id, email string) error {
	_, err := r.db.NewUpdate().
		Model((*model.Peer)(nil)).
		Set("contact_email = ?", email).
		Set("updated_at = now()").
		Where("id = ?", id).
		Exec(ctx)
	return err
}

func (r *bunPeerRepository) Delete(ctx context.Context, id string) error {
	_, err := r.db.NewDelete().Model((*model.Peer)(nil)).Where("id = ?", id).Exec(ctx)
	return err
}

func (r *bunPeerRepository) ExistsByNodeAndASN(ctx context.Context, nodeID string, asn int64) (bool, error) {
	return r.db.NewSelect().
		Model((*model.Peer)(nil)).
		Where("node_id = ? AND remote_asn = ?", nodeID, asn).
		Exists(ctx)
}

func (r *bunPeerRepository) ExistsByNodeAndPort(ctx context.Context, nodeID string, port int) (bool, error) {
	return r.db.NewSelect().
		Model((*model.Peer)(nil)).
		Where("node_id = ? AND wg_listen_port = ?", nodeID, port).
		Exists(ctx)
}

func (r *bunPeerRepository) CountPendingByASN(ctx context.Context, asn int64) (int, error) {
	return r.db.NewSelect().
		Model((*model.Peer)(nil)).
		Where("remote_asn = ? AND status = 'pending'", asn).
		Count(ctx)
}

func (r *bunPeerRepository) ImportPeer(ctx context.Context, p *model.Peer) (string, error) {
	err := r.db.NewRaw(`
		INSERT INTO peers (node_id, remote_asn, remote_pubkey, remote_endpoint, remote_lla, contact_email,
		                   wg_listen_port, wg_interface_name, bgp_proto_name, bird_config_filename, wg_managed, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'active', now(), now())
		ON CONFLICT (node_id, remote_asn) DO NOTHING
		RETURNING id`,
		p.NodeID, p.RemoteASN, p.RemotePubkey, p.RemoteEndpoint, p.RemoteLLA,
		p.ContactEmail, p.WgListenPort, p.WgInterfaceName, p.BgpProtoName, p.BirdConfigFilename, p.WgManaged,
	).Scan(ctx, &p.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return p.ID, nil
}

// ImportPeerFull inserts a peer from an external JSON dump, preserving all
// columns including status, mtu and wg_preshared_key. It keys on the
// (node_id, remote_asn) unique constraint. When overwrite is false an existing
// peer is left untouched and "skipped" is returned. When overwrite is true an
// existing peer's mutable columns are updated and "overwritten" is returned. A
// freshly inserted row returns "imported". The peer's ID is populated on the
// passed struct in all non-skipped cases.
func (r *bunPeerRepository) ImportPeerFull(ctx context.Context, p *model.Peer, overwrite bool) (string, error) {
	status := p.Status
	if status == "" {
		status = "pending"
	}

	if !overwrite {
		err := r.db.NewRaw(`
			INSERT INTO peers (node_id, remote_asn, remote_pubkey, remote_endpoint, remote_lla, contact_email,
			                   wg_listen_port, wg_interface_name, bgp_proto_name, bird_config_filename, wg_managed,
			                   mtu, wg_preshared_key, status, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, now(), now())
			ON CONFLICT (node_id, remote_asn) DO NOTHING
			RETURNING id`,
			p.NodeID, p.RemoteASN, p.RemotePubkey, p.RemoteEndpoint, p.RemoteLLA, p.ContactEmail,
			p.WgListenPort, p.WgInterfaceName, p.BgpProtoName, p.BirdConfigFilename, p.WgManaged,
			p.MTU, p.WgPreSharedKey, status,
		).Scan(ctx, &p.ID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return "skipped", nil
			}
			return "", err
		}
		return "imported", nil
	}

	var inserted bool
	err := r.db.NewRaw(`
		INSERT INTO peers (node_id, remote_asn, remote_pubkey, remote_endpoint, remote_lla, contact_email,
		                   wg_listen_port, wg_interface_name, bgp_proto_name, bird_config_filename, wg_managed,
		                   mtu, wg_preshared_key, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, now(), now())
		ON CONFLICT (node_id, remote_asn) DO UPDATE SET
			remote_pubkey = EXCLUDED.remote_pubkey,
			remote_endpoint = EXCLUDED.remote_endpoint,
			remote_lla = EXCLUDED.remote_lla,
			contact_email = EXCLUDED.contact_email,
			wg_listen_port = EXCLUDED.wg_listen_port,
			wg_interface_name = EXCLUDED.wg_interface_name,
			bgp_proto_name = EXCLUDED.bgp_proto_name,
			bird_config_filename = EXCLUDED.bird_config_filename,
			wg_managed = EXCLUDED.wg_managed,
			mtu = EXCLUDED.mtu,
			wg_preshared_key = EXCLUDED.wg_preshared_key,
			status = EXCLUDED.status,
			updated_at = now()
		RETURNING id, (xmax = 0) AS inserted`,
		p.NodeID, p.RemoteASN, p.RemotePubkey, p.RemoteEndpoint, p.RemoteLLA, p.ContactEmail,
		p.WgListenPort, p.WgInterfaceName, p.BgpProtoName, p.BirdConfigFilename, p.WgManaged,
		p.MTU, p.WgPreSharedKey, status,
	).Scan(ctx, &p.ID, &inserted)
	if err != nil {
		return "", err
	}
	if inserted {
		return "imported", nil
	}
	return "overwritten", nil
}

func (r *bunPeerRepository) SummaryByASN(ctx context.Context, asn int64) ([]*PeerSummaryResult, error) {
	results := make([]*PeerSummaryResult, 0)
	err := r.db.NewRaw(`
		SELECT DISTINCT ON (p.id)
		  p.id,
		  pm.rtt_ms,
		  pm.bgp_state,
		  pm.last_handshake,
		  pm.bgp_uptime_secs
		FROM peers p
		LEFT JOIN peer_metrics pm ON pm.peer_id = p.id
		WHERE p.remote_asn = ?
		ORDER BY p.id, pm.time DESC`, asn,
	).Scan(ctx, &results)
	return results, err
}

func (r *bunPeerRepository) ListActivePeersForEndpointCheck(ctx context.Context) ([]PeerEndpointCheck, error) {
	var results []PeerEndpointCheck
	err := r.db.NewRaw(`
		SELECT
			p.id AS peer_id,
			p.node_id,
			p.remote_asn,
			p.remote_endpoint,
			p.bgp_proto_name,
			p.contact_email,
			n.name AS node_name,
			p.endpoint_mismatch_since,
			p.bgp_suspended_by_endpoint,
			(SELECT pm.wg_actual_endpoint
			 FROM peer_metrics pm
			 WHERE pm.peer_id = p.id AND pm.wg_actual_endpoint IS NOT NULL AND pm.wg_actual_endpoint != ''
			 ORDER BY pm.time DESC LIMIT 1) AS wg_actual_endpoint
		FROM peers p
		JOIN nodes n ON n.id = p.node_id
		WHERE p.status = 'active'
		  AND p.remote_endpoint IS NOT NULL AND p.remote_endpoint != ''
		ORDER BY p.id
	`).Scan(ctx, &results)
	return results, err
}

func (r *bunPeerRepository) SetEndpointMismatchSince(ctx context.Context, peerID string, since *time.Time) error {
	_, err := r.db.NewRaw(
		`UPDATE peers SET endpoint_mismatch_since = ? WHERE id = ?`,
		since, peerID,
	).Exec(ctx)
	return err
}

func (r *bunPeerRepository) SetBGPSuspendedByEndpoint(ctx context.Context, peerID string, suspended bool) error {
	_, err := r.db.NewRaw(
		`UPDATE peers SET bgp_suspended_by_endpoint = ? WHERE id = ?`,
		suspended, peerID,
	).Exec(ctx)
	return err
}

func (r *bunPeerRepository) ListPeersSuspendedByEndpoint(ctx context.Context) ([]PeerEndpointCheck, error) {
	var results []PeerEndpointCheck
	err := r.db.NewRaw(`
		SELECT
			p.id AS peer_id,
			p.node_id,
			p.remote_asn,
			p.remote_endpoint,
			p.bgp_proto_name,
			p.contact_email,
			n.name AS node_name,
			p.endpoint_mismatch_since,
			p.bgp_suspended_by_endpoint
		FROM peers p
		JOIN nodes n ON n.id = p.node_id
		WHERE p.bgp_suspended_by_endpoint = true
		  AND p.status = 'active'
		ORDER BY p.id
	`).Scan(ctx, &results)
	return results, err
}
