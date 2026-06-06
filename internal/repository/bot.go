package repository

import (
	"context"
	"time"

	"github.com/akaere/autopeer-center/internal/model"
	"github.com/uptrace/bun"
)

type BotRepository interface {
	LoadAllSettings(ctx context.Context) ([]*model.BotSetting, error)
	GetSetting(ctx context.Context, key string) (*model.BotSetting, error)
	UpdateSetting(ctx context.Context, key, value string) error
	UpdateSettingWithHash(ctx context.Context, key, hash string) error
	ResetToken(ctx context.Context, hash, currentValue string) error
	GetLastCommandAt(ctx context.Context) (*time.Time, error)

	InsertCommandStat(ctx context.Context, stat *model.BotCommandStat) error
	GetCommandCounts(ctx context.Context) (total int, uniqueUsers int, commands map[string]int, err error)
	ListCommandStats(ctx context.Context, params ListParams) ([]*model.BotCommandStat, int, error)

	ListBlockedUsers(ctx context.Context) ([]*model.BotBlockedUser, error)
	BlockUser(ctx context.Context, u *model.BotBlockedUser) error
	UnblockUser(ctx context.Context, id string) error
}

type bunBotRepository struct {
	baseRepo
}

func NewBotRepository(db *bun.DB) BotRepository {
	return &bunBotRepository{baseRepo{db: db}}
}

func (r *bunBotRepository) LoadAllSettings(ctx context.Context) ([]*model.BotSetting, error) {
	settings := make([]*model.BotSetting, 0)
	err := r.db.NewSelect().Model(&settings).OrderExpr("id ASC").Scan(ctx)
	return settings, err
}

func (r *bunBotRepository) GetSetting(ctx context.Context, key string) (*model.BotSetting, error) {
	var s model.BotSetting
	err := r.db.NewSelect().Model(&s).Where("setting_key = ?", key).Scan(ctx)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *bunBotRepository) UpdateSetting(ctx context.Context, key, value string) error {
	_, err := r.db.NewUpdate().
		Model((*model.BotSetting)(nil)).
		Set("setting_value = ?", value).
		Set("updated_at = now()").
		Where("setting_key = ?", key).
		Exec(ctx)
	return err
}

func (r *bunBotRepository) UpdateSettingWithHash(ctx context.Context, key, hash string) error {
	_, err := r.db.NewUpdate().
		Model((*model.BotSetting)(nil)).
		Set("setting_value_hash = ?", hash).
		Set("updated_at = now()").
		Where("setting_key = ?", key).
		Exec(ctx)
	return err
}

func (r *bunBotRepository) ResetToken(ctx context.Context, hash, currentValue string) error {
	_, err := r.db.NewUpdate().
		Model((*model.BotSetting)(nil)).
		Set("setting_value_hash = ?", hash).
		Set("setting_value = ''").
		Where("setting_key = 'bot_auth_token'").
		Where("setting_value = ?", currentValue).
		Exec(ctx)
	return err
}

func (r *bunBotRepository) InsertCommandStat(ctx context.Context, stat *model.BotCommandStat) error {
	_, err := r.db.NewInsert().Model(stat).Exec(ctx)
	return err
}

func (r *bunBotRepository) GetCommandCounts(ctx context.Context) (total int, uniqueUsers int, commands map[string]int, err error) {
	total, err = r.db.NewSelect().Model((*model.BotCommandStat)(nil)).Count(ctx)
	if err != nil {
		return
	}

	var users []struct {
		TgUserID int64 `bun:"tg_user_id"`
	}
	err = r.db.NewSelect().
		Model((*model.BotCommandStat)(nil)).
		ColumnExpr("DISTINCT tg_user_id").
		Scan(ctx, &users)
	if err != nil {
		return
	}
	uniqueUsers = len(users)

	var cmdCounts []struct {
		Command string `bun:"command"`
		Count   int    `bun:"cnt"`
	}
	err = r.db.NewRaw(
		"SELECT command, COUNT(*) as cnt FROM bot_command_stats GROUP BY command ORDER BY cnt DESC",
	).Scan(ctx, &cmdCounts)
	if err != nil {
		return
	}
	commands = make(map[string]int)
	for _, c := range cmdCounts {
		commands[c.Command] = c.Count
	}
	return
}

func (r *bunBotRepository) ListCommandStats(ctx context.Context, params ListParams) ([]*model.BotCommandStat, int, error) {
	total, err := r.db.NewSelect().Model((*model.BotCommandStat)(nil)).Count(ctx)
	if err != nil {
		return nil, 0, err
	}

	stats := make([]*model.BotCommandStat, 0)
	err = r.db.NewSelect().Model(&stats).
		OrderExpr("created_at DESC").
		Apply(func(sq *bun.SelectQuery) *bun.SelectQuery {
			return applyPagination(sq, params)
		}).Scan(ctx)
	return stats, total, err
}

func (r *bunBotRepository) ListBlockedUsers(ctx context.Context) ([]*model.BotBlockedUser, error) {
	users := make([]*model.BotBlockedUser, 0)
	err := r.db.NewSelect().Model(&users).OrderExpr("blocked_at DESC").Scan(ctx)
	return users, err
}

func (r *bunBotRepository) BlockUser(ctx context.Context, u *model.BotBlockedUser) error {
	_, err := r.db.NewInsert().
		Model(u).
		On("CONFLICT (tg_user_id) DO UPDATE").
		Set("username = EXCLUDED.username").
		Set("reason = EXCLUDED.reason").
		Set("blocked_by = EXCLUDED.blocked_by").
		Set("blocked_at = now()").
		Exec(ctx)
	return err
}

func (r *bunBotRepository) UnblockUser(ctx context.Context, id string) error {
	_, err := r.db.NewDelete().Model((*model.BotBlockedUser)(nil)).Where("id = ?", id).Exec(ctx)
	return err
}

func (r *bunBotRepository) GetLastCommandAt(ctx context.Context) (*time.Time, error) {
	var t *time.Time
	err := r.db.NewRaw(`SELECT MAX(created_at) FROM bot_command_stats`).Scan(ctx, &t)
	return t, err
}
