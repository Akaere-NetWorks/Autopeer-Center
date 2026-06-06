package repository

import (
	"context"

	"github.com/akaere/autopeer-center/internal/model"
	"github.com/uptrace/bun"
)

type NodeRepository interface {
	GetByID(ctx context.Context, id string) (*model.Node, error)
	GetByAgentToken(ctx context.Context, token string) (*model.Node, error)
	List(ctx context.Context) ([]*model.Node, error)
	ListEnabled(ctx context.Context) ([]*model.Node, error)
	Create(ctx context.Context, n *model.Node) error
	Update(ctx context.Context, n *model.Node) error
	DeleteWithPeers(ctx context.Context, nodeID string) error
	SetOnline(ctx context.Context, nodeID string, online bool) error
	SetAgentState(ctx context.Context, nodeID, state string) error
	SetAgentVersion(ctx context.Context, nodeID, version string) error
	SetAgentToken(ctx context.Context, nodeID, token string) error
	ResetAgentPubkey(ctx context.Context, nodeID string) error
	SetAgentPubkey(ctx context.Context, nodeID, pubkey string) error
	GetAgentPubkey(ctx context.Context, nodeID string) (string, error)
	GetField(ctx context.Context, nodeID, field string) (string, error)
	CountEnabled(ctx context.Context) (int, error)
	CountOnline(ctx context.Context) (int, error)
	MarkStaleUpdating(ctx context.Context) error
}

type bunNodeRepository struct {
	baseRepo
}

func NewNodeRepository(db *bun.DB) NodeRepository {
	return &bunNodeRepository{baseRepo{db: db}}
}

func (r *bunNodeRepository) GetByID(ctx context.Context, id string) (*model.Node, error) {
	var n model.Node
	err := r.db.NewSelect().Model(&n).Where("id = ?", id).Scan(ctx)
	if err != nil {
		return nil, err
	}
	return &n, nil
}

func (r *bunNodeRepository) GetByAgentToken(ctx context.Context, token string) (*model.Node, error) {
	var n model.Node
	err := r.db.NewSelect().Model(&n).Where("agent_token = ?", token).Where("enabled = true").Scan(ctx)
	if err != nil {
		return nil, err
	}
	return &n, nil
}

func (r *bunNodeRepository) List(ctx context.Context) ([]*model.Node, error) {
	nodes := make([]*model.Node, 0)
	err := r.db.NewSelect().Model(&nodes).OrderExpr("name ASC").Scan(ctx)
	return nodes, err
}

func (r *bunNodeRepository) ListEnabled(ctx context.Context) ([]*model.Node, error) {
	nodes := make([]*model.Node, 0)
	err := r.db.NewSelect().Model(&nodes).Where("enabled = true").OrderExpr("name ASC").Scan(ctx)
	return nodes, err
}

func (r *bunNodeRepository) Create(ctx context.Context, n *model.Node) error {
	_, err := r.db.NewInsert().Model(n).Returning("id").Exec(ctx)
	return err
}

func (r *bunNodeRepository) Update(ctx context.Context, n *model.Node) error {
	_, err := r.db.NewUpdate().Model(n).Where("id = ?", n.ID).Exec(ctx)
	return err
}

func (r *bunNodeRepository) DeleteWithPeers(ctx context.Context, nodeID string) error {
	return r.RunInTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewDelete().Model((*model.Peer)(nil)).Where("node_id = ?", nodeID).Exec(ctx); err != nil {
			return err
		}
		_, err := tx.NewDelete().Model((*model.Node)(nil)).Where("id = ?", nodeID).Exec(ctx)
		return err
	})
}

func (r *bunNodeRepository) SetOnline(ctx context.Context, nodeID string, online bool) error {
	_, err := r.db.NewUpdate().
		Model((*model.Node)(nil)).
		Set("online = ?", online).
		Where("id = ?", nodeID).
		Exec(ctx)
	return err
}

func (r *bunNodeRepository) SetAgentState(ctx context.Context, nodeID, state string) error {
	_, err := r.db.NewUpdate().
		Model((*model.Node)(nil)).
		Set("agent_state = ?", state).
		Set("agent_state_changed_at = now()").
		Set("online = ?", state == "online").
		Where("id = ?", nodeID).
		Exec(ctx)
	return err
}

func (r *bunNodeRepository) SetAgentVersion(ctx context.Context, nodeID, version string) error {
	_, err := r.db.NewUpdate().
		Model((*model.Node)(nil)).
		Set("agent_version = ?", version).
		Set("agent_state = 'online'").
		Set("online = true").
		Where("id = ?", nodeID).
		Exec(ctx)
	return err
}

func (r *bunNodeRepository) SetAgentToken(ctx context.Context, nodeID, token string) error {
	_, err := r.db.NewUpdate().
		Model((*model.Node)(nil)).
		Set("agent_token = ?", token).
		Where("id = ?", nodeID).
		Exec(ctx)
	return err
}

func (r *bunNodeRepository) ResetAgentPubkey(ctx context.Context, nodeID string) error {
	_, err := r.db.NewUpdate().
		Model((*model.Node)(nil)).
		Set("agent_pubkey = ''").
		Where("id = ?", nodeID).
		Exec(ctx)
	return err
}

func (r *bunNodeRepository) SetAgentPubkey(ctx context.Context, nodeID, pubkey string) error {
	_, err := r.db.NewUpdate().
		Model((*model.Node)(nil)).
		Set("agent_pubkey = ?", pubkey).
		Where("id = ?", nodeID).
		Where("agent_pubkey = '' OR agent_pubkey IS NULL").
		Exec(ctx)
	return err
}

func (r *bunNodeRepository) GetAgentPubkey(ctx context.Context, nodeID string) (string, error) {
	var n model.Node
	err := r.db.NewSelect().Model(&n).Column("agent_pubkey").Where("id = ?", nodeID).Where("enabled = true").Scan(ctx)
	return n.AgentPubkey, err
}

func (r *bunNodeRepository) GetField(ctx context.Context, nodeID, field string) (string, error) {
	var n model.Node
	err := r.db.NewSelect().Model(&n).Column(field).Where("id = ?", nodeID).Scan(ctx)
	if err != nil {
		return "", err
	}
	switch field {
	case "name":
		return n.Name, nil
	case "location":
		return n.Location, nil
	case "agent_version":
		return n.AgentVersion, nil
	case "agent_state":
		return n.AgentState, nil
	default:
		return "", nil
	}
}

func (r *bunNodeRepository) CountEnabled(ctx context.Context) (int, error) {
	return r.db.NewSelect().Model((*model.Node)(nil)).Where("enabled = true").Count(ctx)
}

func (r *bunNodeRepository) CountOnline(ctx context.Context) (int, error) {
	return r.db.NewSelect().Model((*model.Node)(nil)).Where("online = true").Where("enabled = true").Count(ctx)
}

func (r *bunNodeRepository) MarkStaleUpdating(ctx context.Context) error {
	_, err := r.db.NewUpdate().
		Model((*model.Node)(nil)).
		Set("agent_state = 'offline'").
		Set("online = false").
		Where("agent_state = 'updating'").
		Where("agent_state_changed_at < now() - INTERVAL '5 minutes'").
		Exec(ctx)
	return err
}
