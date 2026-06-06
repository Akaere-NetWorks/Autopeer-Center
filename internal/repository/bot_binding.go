package repository

import (
	"context"
	"time"

	"github.com/akaere/autopeer-center/internal/model"
	"github.com/uptrace/bun"
)

type BotBindingRepository interface {
	GetByTgUserID(ctx context.Context, tgUserID int64) (*model.BotUserBinding, error)
	GetByASN(ctx context.Context, asn int64) (*model.BotUserBinding, error)
	Create(ctx context.Context, b *model.BotUserBinding) error
	Delete(ctx context.Context, tgUserID int64) error
	SetAdmin(ctx context.Context, tgUserID int64, isAdmin bool) error

	CreateWebBindToken(ctx context.Context, token string, asn int64, expiresAt time.Time) error
	GetWebBindToken(ctx context.Context, token string) (*model.BotWebBindToken, error)
	MarkWebBindTokenUsed(ctx context.Context, token string) error
}

type bunBotBindingRepository struct {
	baseRepo
}

func NewBotBindingRepository(db *bun.DB) BotBindingRepository {
	return &bunBotBindingRepository{baseRepo{db: db}}
}

func (r *bunBotBindingRepository) GetByTgUserID(ctx context.Context, tgUserID int64) (*model.BotUserBinding, error) {
	var b model.BotUserBinding
	err := r.db.NewSelect().Model(&b).Where("tg_user_id = ?", tgUserID).Scan(ctx)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

func (r *bunBotBindingRepository) GetByASN(ctx context.Context, asn int64) (*model.BotUserBinding, error) {
	var b model.BotUserBinding
	err := r.db.NewSelect().Model(&b).Where("asn = ?", asn).Scan(ctx)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

func (r *bunBotBindingRepository) Create(ctx context.Context, b *model.BotUserBinding) error {
	_, err := r.db.NewInsert().Model(b).
		On("CONFLICT (tg_user_id) DO UPDATE").
		Set("tg_username = EXCLUDED.tg_username").
		Set("asn = EXCLUDED.asn").
		Set("bound_at = NOW()").
		Set("bound_via = EXCLUDED.bound_via").
		Exec(ctx)
	return err
}

func (r *bunBotBindingRepository) Delete(ctx context.Context, tgUserID int64) error {
	_, err := r.db.NewDelete().Model((*model.BotUserBinding)(nil)).Where("tg_user_id = ?", tgUserID).Exec(ctx)
	return err
}

func (r *bunBotBindingRepository) SetAdmin(ctx context.Context, tgUserID int64, isAdmin bool) error {
	_, err := r.db.NewUpdate().
		Model((*model.BotUserBinding)(nil)).
		Set("is_admin = ?", isAdmin).
		Where("tg_user_id = ?", tgUserID).
		Exec(ctx)
	return err
}

func (r *bunBotBindingRepository) CreateWebBindToken(ctx context.Context, token string, asn int64, expiresAt time.Time) error {
	t := &model.BotWebBindToken{
		Token:     token,
		ASN:       asn,
		ExpiresAt: expiresAt,
	}
	_, err := r.db.NewInsert().Model(t).Exec(ctx)
	return err
}

func (r *bunBotBindingRepository) GetWebBindToken(ctx context.Context, token string) (*model.BotWebBindToken, error) {
	var t model.BotWebBindToken
	err := r.db.NewSelect().Model(&t).Where("token = ?", token).Scan(ctx)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (r *bunBotBindingRepository) MarkWebBindTokenUsed(ctx context.Context, token string) error {
	_, err := r.db.NewUpdate().
		Model((*model.BotWebBindToken)(nil)).
		Set("used = true").
		Where("token = ?", token).
		Exec(ctx)
	return err
}
