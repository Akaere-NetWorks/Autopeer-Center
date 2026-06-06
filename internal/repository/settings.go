package repository

import (
	"context"

	"github.com/akaere/autopeer-center/internal/model"
	"github.com/uptrace/bun"
)

type SettingsRepository interface {
	GetSiteSetting(ctx context.Context, key string) (string, error)
	SetSiteSetting(ctx context.Context, key, value string) error
	GetAllSiteSettings(ctx context.Context) ([]*model.SiteSetting, error)
	GetNotificationSetting(ctx context.Context, key string) (string, error)
	GetNotificationThreshold(ctx context.Context, key string) (string, error)
	GetAllNotificationSettings(ctx context.Context) ([]*model.NotificationSetting, error)
	UpdateNotificationSetting(ctx context.Context, key, value, threshold string) error
}

type bunSettingsRepository struct {
	baseRepo
}

func NewSettingsRepository(db *bun.DB) SettingsRepository {
	return &bunSettingsRepository{baseRepo{db: db}}
}

func (r *bunSettingsRepository) GetSiteSetting(ctx context.Context, key string) (string, error) {
	var s model.SiteSetting
	err := r.db.NewSelect().Model(&s).Where("setting_key = ?", key).Scan(ctx)
	return s.SettingValue, err
}

func (r *bunSettingsRepository) SetSiteSetting(ctx context.Context, key, value string) error {
	_, err := r.db.NewUpdate().
		Model((*model.SiteSetting)(nil)).
		Set("setting_value = ?", value).
		Set("updated_at = now()").
		Where("setting_key = ?", key).
		Exec(ctx)
	return err
}

func (r *bunSettingsRepository) GetAllSiteSettings(ctx context.Context) ([]*model.SiteSetting, error) {
	settings := make([]*model.SiteSetting, 0)
	err := r.db.NewSelect().Model(&settings).OrderExpr("id ASC").Scan(ctx)
	return settings, err
}

func (r *bunSettingsRepository) GetNotificationSetting(ctx context.Context, key string) (string, error) {
	var s model.NotificationSetting
	err := r.db.NewSelect().Model(&s).Where("setting_key = ?", key).Scan(ctx)
	return s.SettingValue, err
}

func (r *bunSettingsRepository) GetNotificationThreshold(ctx context.Context, key string) (string, error) {
	var s model.NotificationSetting
	err := r.db.NewSelect().Model(&s).Where("setting_key = ?", key).Scan(ctx)
	return s.ThresholdValue, err
}

func (r *bunSettingsRepository) GetAllNotificationSettings(ctx context.Context) ([]*model.NotificationSetting, error) {
	settings := make([]*model.NotificationSetting, 0)
	err := r.db.NewSelect().Model(&settings).OrderExpr("id ASC").Scan(ctx)
	return settings, err
}

func (r *bunSettingsRepository) UpdateNotificationSetting(ctx context.Context, key, value, threshold string) error {
	q := r.db.NewUpdate().
		Model((*model.NotificationSetting)(nil)).
		Set("updated_at = now()").
		Where("setting_key = ?", key)

	if value != "" {
		q = q.Set("setting_value = ?", value)
	}
	if threshold != "" {
		q = q.Set("threshold_value = ?", threshold)
	}

	_, err := q.Exec(ctx)
	return err
}
