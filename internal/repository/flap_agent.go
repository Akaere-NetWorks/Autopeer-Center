package repository

import (
	"context"
	"time"

	"github.com/akaere/autopeer-center/internal/model"
	"github.com/uptrace/bun"
)

// FlapAgentRepository is the data-access layer for the flapalerted-agent
// allowlist. It is the DB-backed replacement for the former env-var allowlist.
type FlapAgentRepository interface {
	List(ctx context.Context) ([]*model.FlapAgent, error)
	GetByID(ctx context.Context, id string) (*model.FlapAgent, error)
	GetByAgentID(ctx context.Context, agentID string) (*model.FlapAgent, error)
	// GetByToken resolves an enabled agent by its bearer token.
	GetByToken(ctx context.Context, token string) (*model.FlapAgent, error)
	Create(ctx context.Context, a *model.FlapAgent) error
	Update(ctx context.Context, a *model.FlapAgent) error
	SetToken(ctx context.Context, id, token string) error
	// SetPubkey pins the agent's public key (TOFU); it only writes when no key
	// is currently stored, matching the node agent. Returns the rows affected so
	// callers can detect a no-op write.
	SetPubkey(ctx context.Context, agentID, pubkey string) (int64, error)
	ResetPubkey(ctx context.Context, id string) error
	SetEnabled(ctx context.Context, id string, enabled bool) error
	// TouchSeen records the last-seen timestamp and advertised version.
	TouchSeen(ctx context.Context, agentID, version string) error
	Delete(ctx context.Context, id string) error
}

type bunFlapAgentRepository struct {
	baseRepo
}

func NewFlapAgentRepository(db *bun.DB) FlapAgentRepository {
	return &bunFlapAgentRepository{baseRepo{db: db}}
}

func (r *bunFlapAgentRepository) List(ctx context.Context) ([]*model.FlapAgent, error) {
	agents := make([]*model.FlapAgent, 0)
	err := r.db.NewSelect().Model(&agents).OrderExpr("agent_id ASC").Scan(ctx)
	return agents, err
}

func (r *bunFlapAgentRepository) GetByID(ctx context.Context, id string) (*model.FlapAgent, error) {
	var a model.FlapAgent
	err := r.db.NewSelect().Model(&a).Where("id = ?", id).Scan(ctx)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func (r *bunFlapAgentRepository) GetByAgentID(ctx context.Context, agentID string) (*model.FlapAgent, error) {
	var a model.FlapAgent
	err := r.db.NewSelect().Model(&a).Where("agent_id = ?", agentID).Scan(ctx)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func (r *bunFlapAgentRepository) GetByToken(ctx context.Context, token string) (*model.FlapAgent, error) {
	var a model.FlapAgent
	err := r.db.NewSelect().Model(&a).Where("token = ?", token).Where("enabled = true").Scan(ctx)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func (r *bunFlapAgentRepository) Create(ctx context.Context, a *model.FlapAgent) error {
	_, err := r.db.NewInsert().Model(a).Returning("id").Exec(ctx)
	return err
}

func (r *bunFlapAgentRepository) Update(ctx context.Context, a *model.FlapAgent) error {
	_, err := r.db.NewUpdate().
		Model(a).
		Column("name", "description", "enabled").
		Set("updated_at = now()").
		Where("id = ?", a.ID).
		Exec(ctx)
	return err
}

func (r *bunFlapAgentRepository) SetToken(ctx context.Context, id, token string) error {
	_, err := r.db.NewUpdate().
		Model((*model.FlapAgent)(nil)).
		Set("token = ?", token).
		Set("updated_at = now()").
		Where("id = ?", id).
		Exec(ctx)
	return err
}

func (r *bunFlapAgentRepository) SetPubkey(ctx context.Context, agentID, pubkey string) (int64, error) {
	res, err := r.db.NewUpdate().
		Model((*model.FlapAgent)(nil)).
		Set("agent_pubkey = ?", pubkey).
		Set("updated_at = now()").
		Where("agent_id = ?", agentID).
		Where("agent_pubkey = '' OR agent_pubkey IS NULL").
		Exec(ctx)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (r *bunFlapAgentRepository) ResetPubkey(ctx context.Context, id string) error {
	_, err := r.db.NewUpdate().
		Model((*model.FlapAgent)(nil)).
		Set("agent_pubkey = ''").
		Set("updated_at = now()").
		Where("id = ?", id).
		Exec(ctx)
	return err
}

func (r *bunFlapAgentRepository) SetEnabled(ctx context.Context, id string, enabled bool) error {
	_, err := r.db.NewUpdate().
		Model((*model.FlapAgent)(nil)).
		Set("enabled = ?", enabled).
		Set("updated_at = now()").
		Where("id = ?", id).
		Exec(ctx)
	return err
}

func (r *bunFlapAgentRepository) TouchSeen(ctx context.Context, agentID, version string) error {
	_, err := r.db.NewUpdate().
		Model((*model.FlapAgent)(nil)).
		Set("last_seen_at = ?", time.Now()).
		Set("version = ?", version).
		Where("agent_id = ?", agentID).
		Exec(ctx)
	return err
}

func (r *bunFlapAgentRepository) Delete(ctx context.Context, id string) error {
	_, err := r.db.NewDelete().Model((*model.FlapAgent)(nil)).Where("id = ?", id).Exec(ctx)
	return err
}
