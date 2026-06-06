package repository

import (
	"context"

	"github.com/akaere/autopeer-center/internal/model"
	"github.com/uptrace/bun"
)

type MCPRepository interface {
	CreateUserKey(ctx context.Context, key *model.MCPKey) error
	GetUserKeyByHash(ctx context.Context, hash string) (*model.MCPKey, error)
	ListUserKeys(ctx context.Context, asn int64) ([]*model.MCPKey, error)
	ListAllUserKeys(ctx context.Context, asn int64) ([]*model.MCPKey, error)
	DeleteUserKey(ctx context.Context, id string, asn int64) error
	TouchUserKey(ctx context.Context, id string) error
	CountUserKeys(ctx context.Context, asn int64) (int, error)
	GetUserKeyByID(ctx context.Context, id string) (*model.MCPKey, error)
	ForceDeleteUserKey(ctx context.Context, id string) (*model.MCPKey, error)

	CreateAdminKey(ctx context.Context, key *model.AdminMCPKey) error
	GetAdminKeyByHash(ctx context.Context, hash string) (*model.AdminMCPKey, error)
	ListAdminKeys(ctx context.Context, adminID string) ([]*model.AdminMCPKey, error)
	DeleteAdminKey(ctx context.Context, id, adminID string) error
	TouchAdminKey(ctx context.Context, id string) error
	CountAdminKeys(ctx context.Context, adminID string) (int, error)

	CreateSession(ctx context.Context, s *model.MCPSession) error
	CloseSession(ctx context.Context, id string) error
	PingSession(ctx context.Context, id string) error
	ListSessions(ctx context.Context, limit int) ([]*model.MCPSession, error)

	LogAudit(ctx context.Context, entry *model.MCPAuditLog) error
	ListAuditLogs(ctx context.Context, params ListParams) ([]*model.MCPAuditLog, int, error)
	ListAuditLogsByASN(ctx context.Context, asn int64, limit int) ([]*model.MCPAuditLog, error)

	GetIdempotency(ctx context.Context, actorType, keyID, adminKeyID, toolName, idemKey string) (*model.MCPIdempotencyKey, error)
	CreateIdempotency(ctx context.Context, entry *model.MCPIdempotencyKey) error
	CompleteIdempotency(ctx context.Context, id, status, resourceType, resourceID string, response interface{}, errorCode, errorMessage string) error
	CreateOperation(ctx context.Context, op *model.MCPOperation) error
	ListOperationsByASN(ctx context.Context, asn int64, limit int) ([]*model.MCPOperation, error)
	GetOperationByASN(ctx context.Context, asn int64, id string) (*model.MCPOperation, error)
}

type bunMCPRepository struct {
	baseRepo
}

func NewMCPRepository(db *bun.DB) MCPRepository {
	return &bunMCPRepository{baseRepo{db: db}}
}

func (r *bunMCPRepository) CreateUserKey(ctx context.Context, key *model.MCPKey) error {
	_, err := r.db.NewInsert().Model(key).Returning("id, asn, name, key_prefix, capabilities, expires_at, last_used_at, revoked_at, scope_version, created_at").Exec(ctx)
	return err
}

func (r *bunMCPRepository) GetUserKeyByHash(ctx context.Context, hash string) (*model.MCPKey, error) {
	var k model.MCPKey
	err := r.db.NewSelect().Model(&k).Where("key_hash = ?", hash).Scan(ctx)
	if err != nil {
		return nil, err
	}
	return &k, nil
}

func (r *bunMCPRepository) ListUserKeys(ctx context.Context, asn int64) ([]*model.MCPKey, error) {
	keys := make([]*model.MCPKey, 0)
	err := r.db.NewSelect().Model(&keys).Where("asn = ?", asn).OrderExpr("created_at DESC").Scan(ctx)
	return keys, err
}

func (r *bunMCPRepository) ListAllUserKeys(ctx context.Context, asn int64) ([]*model.MCPKey, error) {
	q := r.db.NewSelect().Model((*model.MCPKey)(nil)).OrderExpr("created_at DESC")
	if asn > 0 {
		q = q.Where("asn = ?", asn)
	}
	keys := make([]*model.MCPKey, 0)
	err := q.Scan(ctx, &keys)
	return keys, err
}

func (r *bunMCPRepository) DeleteUserKey(ctx context.Context, id string, asn int64) error {
	_, err := r.db.NewDelete().Model((*model.MCPKey)(nil)).Where("id = ? AND asn = ?", id, asn).Exec(ctx)
	return err
}

func (r *bunMCPRepository) TouchUserKey(ctx context.Context, id string) error {
	_, err := r.db.NewUpdate().
		Model((*model.MCPKey)(nil)).
		Set("last_used_at = NOW()").
		Where("id = ?", id).
		Exec(ctx)
	return err
}

func (r *bunMCPRepository) CountUserKeys(ctx context.Context, asn int64) (int, error) {
	return r.db.NewSelect().Model((*model.MCPKey)(nil)).Where("asn = ?", asn).Count(ctx)
}

func (r *bunMCPRepository) GetUserKeyByID(ctx context.Context, id string) (*model.MCPKey, error) {
	var k model.MCPKey
	err := r.db.NewSelect().Model(&k).Where("id = ?", id).Scan(ctx)
	if err != nil {
		return nil, err
	}
	return &k, nil
}

func (r *bunMCPRepository) ForceDeleteUserKey(ctx context.Context, id string) (*model.MCPKey, error) {
	var k model.MCPKey
	err := r.db.NewDelete().
		Model(&k).
		Where("id = ?", id).
		Returning("asn, name").
		Scan(ctx)
	if err != nil {
		return nil, err
	}
	return &k, nil
}

func (r *bunMCPRepository) CreateAdminKey(ctx context.Context, key *model.AdminMCPKey) error {
	_, err := r.db.NewInsert().Model(key).Returning("id, admin_id, name, key_prefix, capabilities, expires_at, last_used_at, revoked_at, scope_version, created_at").Exec(ctx)
	return err
}

func (r *bunMCPRepository) GetAdminKeyByHash(ctx context.Context, hash string) (*model.AdminMCPKey, error) {
	var k model.AdminMCPKey
	err := r.db.NewSelect().Model(&k).Where("key_hash = ?", hash).Scan(ctx)
	if err != nil {
		return nil, err
	}
	return &k, nil
}

func (r *bunMCPRepository) ListAdminKeys(ctx context.Context, adminID string) ([]*model.AdminMCPKey, error) {
	keys := make([]*model.AdminMCPKey, 0)
	err := r.db.NewSelect().Model(&keys).Where("admin_id = ?", adminID).OrderExpr("created_at DESC").Scan(ctx)
	return keys, err
}

func (r *bunMCPRepository) DeleteAdminKey(ctx context.Context, id, adminID string) error {
	_, err := r.db.NewDelete().Model((*model.AdminMCPKey)(nil)).Where("id = ? AND admin_id = ?", id, adminID).Exec(ctx)
	return err
}

func (r *bunMCPRepository) TouchAdminKey(ctx context.Context, id string) error {
	_, err := r.db.NewUpdate().
		Model((*model.AdminMCPKey)(nil)).
		Set("last_used_at = NOW()").
		Where("id = ?", id).
		Exec(ctx)
	return err
}

func (r *bunMCPRepository) CountAdminKeys(ctx context.Context, adminID string) (int, error) {
	return r.db.NewSelect().Model((*model.AdminMCPKey)(nil)).Where("admin_id = ?", adminID).Count(ctx)
}

func (r *bunMCPRepository) CreateSession(ctx context.Context, s *model.MCPSession) error {
	_, err := r.db.NewInsert().Model(s).Exec(ctx)
	return err
}

func (r *bunMCPRepository) CloseSession(ctx context.Context, id string) error {
	_, err := r.db.NewUpdate().
		Model((*model.MCPSession)(nil)).
		Set("disconnected_at = NOW()").
		Where("id = ?", id).
		Exec(ctx)
	return err
}

func (r *bunMCPRepository) PingSession(ctx context.Context, id string) error {
	_, err := r.db.NewUpdate().
		Model((*model.MCPSession)(nil)).
		Set("last_ping_at = NOW()").
		Where("id = ?", id).
		Exec(ctx)
	return err
}

func (r *bunMCPRepository) ListSessions(ctx context.Context, limit int) ([]*model.MCPSession, error) {
	sessions := make([]*model.MCPSession, 0)
	err := r.db.NewSelect().Model(&sessions).OrderExpr("connected_at DESC").Limit(limit).Scan(ctx)
	return sessions, err
}

func (r *bunMCPRepository) LogAudit(ctx context.Context, entry *model.MCPAuditLog) error {
	_, err := r.db.NewInsert().Model(entry).Exec(ctx)
	return err
}

func (r *bunMCPRepository) ListAuditLogs(ctx context.Context, params ListParams) ([]*model.MCPAuditLog, int, error) {
	q := r.db.NewSelect().Model((*model.MCPAuditLog)(nil))
	if params.ASN > 0 {
		q = q.Where("asn = ?", params.ASN)
	}
	if params.Search != "" {
		q = q.Where("tool_name = ?", params.Search)
	}

	total, err := q.Count(ctx)
	if err != nil {
		return nil, 0, err
	}

	logs := make([]*model.MCPAuditLog, 0)
	err = q.OrderExpr("called_at DESC").Apply(func(sq *bun.SelectQuery) *bun.SelectQuery {
		return applyPagination(sq, params)
	}).Scan(ctx, &logs)
	return logs, total, err
}

func (r *bunMCPRepository) ListAuditLogsByASN(ctx context.Context, asn int64, limit int) ([]*model.MCPAuditLog, error) {
	logs := make([]*model.MCPAuditLog, 0)
	err := r.db.NewSelect().Model(&logs).Where("asn = ?", asn).OrderExpr("called_at DESC").Limit(limit).Scan(ctx)
	return logs, err
}

func (r *bunMCPRepository) GetIdempotency(ctx context.Context, actorType, keyID, adminKeyID, toolName, idemKey string) (*model.MCPIdempotencyKey, error) {
	var entry model.MCPIdempotencyKey
	q := r.db.NewSelect().Model(&entry).
		Where("actor_type = ?", actorType).
		Where("tool_name = ?", toolName).
		Where("idempotency_key = ?", idemKey)
	if actorType == "admin" {
		q = q.Where("admin_key_id = ?", adminKeyID)
	} else {
		q = q.Where("key_id = ?", keyID)
	}
	if err := q.Scan(ctx); err != nil {
		return nil, err
	}
	return &entry, nil
}

func (r *bunMCPRepository) CreateIdempotency(ctx context.Context, entry *model.MCPIdempotencyKey) error {
	_, err := r.db.NewInsert().Model(entry).Returning("id, created_at, updated_at").Exec(ctx)
	return err
}

func (r *bunMCPRepository) CompleteIdempotency(ctx context.Context, id, status, resourceType, resourceID string, response interface{}, errorCode, errorMessage string) error {
	q := r.db.NewUpdate().Model((*model.MCPIdempotencyKey)(nil)).
		Set("status = ?", status).
		Set("updated_at = NOW()").
		Set("response_json = ?", response).
		Set("error_code = ?", errorCode).
		Set("error_message = ?", errorMessage).
		Where("id = ?", id)
	if resourceType != "" {
		q = q.Set("resource_type = ?", resourceType)
	}
	if resourceID != "" {
		q = q.Set("resource_id = ?", resourceID)
	}
	_, err := q.Exec(ctx)
	return err
}

func (r *bunMCPRepository) CreateOperation(ctx context.Context, op *model.MCPOperation) error {
	_, err := r.db.NewInsert().Model(op).Returning("id, created_at, updated_at").Exec(ctx)
	return err
}

func (r *bunMCPRepository) ListOperationsByASN(ctx context.Context, asn int64, limit int) ([]*model.MCPOperation, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	operations := make([]*model.MCPOperation, 0)
	err := r.db.NewSelect().Model(&operations).Where("asn = ?", asn).OrderExpr("created_at DESC").Limit(limit).Scan(ctx)
	return operations, err
}

func (r *bunMCPRepository) GetOperationByASN(ctx context.Context, asn int64, id string) (*model.MCPOperation, error) {
	var op model.MCPOperation
	err := r.db.NewSelect().Model(&op).Where("asn = ?", asn).Where("id = ?", id).Scan(ctx)
	if err != nil {
		return nil, err
	}
	return &op, nil
}
