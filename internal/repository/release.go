package repository

import (
	"context"

	"github.com/akaere/autopeer-center/internal/model"
	"github.com/uptrace/bun"
)

type ReleaseRepository interface {
	List(ctx context.Context) ([]*model.AgentRelease, error)
	Create(ctx context.Context, r *model.AgentRelease) error
	Delete(ctx context.Context, version, os, arch string) (string, error)
	GetByVersion(ctx context.Context, version, os, arch string) (*model.AgentRelease, error)
}

type bunReleaseRepository struct {
	baseRepo
}

func NewReleaseRepository(db *bun.DB) ReleaseRepository {
	return &bunReleaseRepository{baseRepo{db: db}}
}

func (r *bunReleaseRepository) List(ctx context.Context) ([]*model.AgentRelease, error) {
	releases := make([]*model.AgentRelease, 0)
	err := r.db.NewSelect().Model(&releases).OrderExpr("uploaded_at DESC").Scan(ctx)
	return releases, err
}

func (r *bunReleaseRepository) Create(ctx context.Context, rel *model.AgentRelease) error {
	_, err := r.db.NewInsert().
		Model(rel).
		On("CONFLICT (version, os, arch) DO NOTHING").
		Exec(ctx)
	return err
}

func (r *bunReleaseRepository) Delete(ctx context.Context, version, os, arch string) (string, error) {
	var rel model.AgentRelease
	err := r.db.NewSelect().
		Model(&rel).
		Column("path").
		Where("version = ? AND os = ? AND arch = ?", version, os, arch).
		Scan(ctx)
	if err != nil {
		return "", err
	}
	_, err = r.db.NewDelete().
		Model((*model.AgentRelease)(nil)).
		Where("version = ? AND os = ? AND arch = ?", version, os, arch).
		Exec(ctx)
	return rel.Path, err
}

func (r *bunReleaseRepository) GetByVersion(ctx context.Context, version, os, arch string) (*model.AgentRelease, error) {
	var rel model.AgentRelease
	err := r.db.NewSelect().
		Model(&rel).
		Where("version = ? AND os = ? AND arch = ?", version, os, arch).
		Scan(ctx)
	if err != nil {
		return nil, err
	}
	return &rel, nil
}
