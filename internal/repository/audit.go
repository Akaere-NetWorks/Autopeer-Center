package repository

import (
	"context"
	"fmt"
	"strconv"

	"github.com/akaere/autopeer-center/internal/model"
	"github.com/uptrace/bun"
)

type AuditRepository interface {
	Log(ctx context.Context, entry *model.AuditLog) error
	List(ctx context.Context, params ListParams) ([]*model.AuditLog, int, error)
	ListFiltered(ctx context.Context, action, operator string, page, perPage int) ([]*model.AuditLog, int, error)
	ListForASN(ctx context.Context, asn int64, action string, page, perPage int) ([]*model.AuditLog, int, error)
	ListByTargetID(ctx context.Context, targetID string, limit int) ([]*model.AuditLog, error)
}

type bunAuditRepository struct {
	baseRepo
}

func NewAuditRepository(db *bun.DB) AuditRepository {
	return &bunAuditRepository{baseRepo{db: db}}
}

func (r *bunAuditRepository) Log(ctx context.Context, entry *model.AuditLog) error {
	_, err := r.db.NewInsert().Model(entry).Exec(ctx)
	return err
}

func (r *bunAuditRepository) List(ctx context.Context, params ListParams) ([]*model.AuditLog, int, error) {
	q := r.db.NewSelect().Model((*model.AuditLog)(nil))

	if params.Search != "" {
		q = q.Where("action LIKE ?", "%"+params.Search+"%")
	}

	total, err := q.Count(ctx)
	if err != nil {
		return nil, 0, err
	}

	logs := make([]*model.AuditLog, 0)
	err = q.OrderExpr("created_at DESC").Apply(func(sq *bun.SelectQuery) *bun.SelectQuery {
		return applyPagination(sq, params)
	}).Scan(ctx, &logs)
	return logs, total, err
}

func (r *bunAuditRepository) ListFiltered(ctx context.Context, action, operator string, page, perPage int) ([]*model.AuditLog, int, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = 25
	}
	if perPage > 200 {
		perPage = 200
	}

	q := r.db.NewSelect().Model((*model.AuditLog)(nil))
	if action != "" {
		q = q.Where("action = ?", action)
	}
	if operator != "" {
		q = q.Where("operator = ?", operator)
	}
	total, err := q.Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	logs2 := make([]*model.AuditLog, 0)
	err = q.OrderExpr("created_at DESC").
		Limit(perPage).
		Offset((page-1)*perPage).
		Scan(ctx, &logs2)
	return logs2, total, err
}

func (r *bunAuditRepository) ListForASN(ctx context.Context, asn int64, action string, page, perPage int) ([]*model.AuditLog, int, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = 25
	}
	if perPage > 200 {
		perPage = 200
	}

	where, args := userAuditWhere(asn, action)

	var total int
	if err := r.db.NewRaw("SELECT COUNT(*) FROM audit_logs "+where, args...).Scan(ctx, &total); err != nil {
		return nil, 0, err
	}

	logs := make([]*model.AuditLog, 0)
	dataArgs := append(args, perPage, (page-1)*perPage)
	err := r.db.NewRaw(`
		SELECT id, action, operator, target_id, detail, created_at
		FROM audit_logs
		`+where+`
		ORDER BY created_at DESC
		LIMIT ? OFFSET ?`, dataArgs...).Scan(ctx, &logs)
	return logs, total, err
}

func userAuditWhere(asn int64, action string) (string, []interface{}) {
	operator := fmt.Sprintf("AS%d", asn)
	asnText := strconv.FormatInt(asn, 10)
	where := `WHERE (
		operator = ?
		OR (action LIKE 'peer.%' AND detail->>'asn' = ?)
		OR (action LIKE 'peer.%' AND target_id IN (SELECT id::text FROM peers WHERE remote_asn = ?))
		OR (action = 'admin.login_as' AND detail->>'asn' = ?)
	)`
	args := []interface{}{operator, asnText, asn, asnText}
	if action != "" {
		where += " AND action = ?"
		args = append(args, action)
	}
	return where, args
}

func (r *bunAuditRepository) ListByTargetID(ctx context.Context, targetID string, limit int) ([]*model.AuditLog, error) {
	if limit < 1 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	logs := make([]*model.AuditLog, 0)
	err := r.db.NewSelect().
		Model((*model.AuditLog)(nil)).
		Where("target_id = ?", targetID).
		OrderExpr("created_at DESC").
		Limit(limit).
		Scan(ctx, &logs)
	return logs, err
}

type CleanupRepository interface {
	DeleteExpiredAuditLogs(ctx context.Context) (int, error)
	DeleteExpiredRequestLogs(ctx context.Context) (int, error)
	DeleteExpiredLoginCodes(ctx context.Context) (int, error)
	DeleteExpiredGPGChallenges(ctx context.Context) (int, error)
	DeleteExpiredBotCommandStats(ctx context.Context) (int, error)
	DeleteExpiredAuthSessions(ctx context.Context) (int, error)
	DeleteExpiredWebAuthnSessions(ctx context.Context) (int, error)
}

type bunCleanupRepository struct {
	baseRepo
}

func NewCleanupRepository(db *bun.DB) CleanupRepository {
	return &bunCleanupRepository{baseRepo{db: db}}
}

func (r *bunCleanupRepository) DeleteExpiredAuditLogs(ctx context.Context) (int, error) {
	res, err := r.db.NewDelete().Model((*model.AuditLog)(nil)).Where("created_at < now() - INTERVAL '30 days'").Exec(ctx)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (r *bunCleanupRepository) DeleteExpiredRequestLogs(ctx context.Context) (int, error) {
	res, err := r.db.NewDelete().Model((*model.RequestLog)(nil)).Where("created_at < now() - INTERVAL '30 days'").Exec(ctx)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (r *bunCleanupRepository) DeleteExpiredLoginCodes(ctx context.Context) (int, error) {
	res, err := r.db.NewDelete().Model((*model.UserLoginCode)(nil)).Where("expires_at < now() - INTERVAL '1 day'").Exec(ctx)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (r *bunCleanupRepository) DeleteExpiredGPGChallenges(ctx context.Context) (int, error) {
	res, err := r.db.NewDelete().Model((*model.GPGChallenge)(nil)).Where("expires_at < now() - INTERVAL '1 day'").Exec(ctx)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (r *bunCleanupRepository) DeleteExpiredBotCommandStats(ctx context.Context) (int, error) {
	res, err := r.db.NewDelete().Model((*model.BotCommandStat)(nil)).Where("created_at < now() - INTERVAL '90 days'").Exec(ctx)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (r *bunCleanupRepository) DeleteExpiredAuthSessions(ctx context.Context) (int, error) {
	res, err := r.db.NewDelete().Model((*model.AuthSession)(nil)).Where("expires_at < now() - INTERVAL '7 days'").Exec(ctx)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (r *bunCleanupRepository) DeleteExpiredWebAuthnSessions(ctx context.Context) (int, error) {
	// 15-minute grace period: matches other short-TTL cleanup methods and prevents
	// deleting rows that are still being used by in-flight Finish requests.
	res, err := r.db.NewDelete().Model((*model.WebAuthnSession)(nil)).Where("expires_at < now() - INTERVAL '15 minutes'").Exec(ctx)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}
