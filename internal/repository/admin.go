package repository

import (
	"context"

	"github.com/akaere/autopeer-center/internal/model"
	"github.com/uptrace/bun"
)

type AdminRepository interface {
	GetByEmail(ctx context.Context, email string) (*model.Admin, error)
	Upsert(ctx context.Context, a *model.Admin) error
}

type bunAdminRepository struct {
	baseRepo
}

func NewAdminRepository(db *bun.DB) AdminRepository {
	return &bunAdminRepository{baseRepo{db: db}}
}

func (r *bunAdminRepository) GetByEmail(ctx context.Context, email string) (*model.Admin, error) {
	var a model.Admin
	err := r.db.NewSelect().Model(&a).Where("email = ?", email).Scan(ctx)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func (r *bunAdminRepository) Upsert(ctx context.Context, a *model.Admin) error {
	_, err := r.db.NewInsert().
		Model(a).
		On("CONFLICT (email) DO UPDATE").
		Set("password_hash = EXCLUDED.password_hash").
		Exec(ctx)
	return err
}
