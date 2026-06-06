package repository

import (
	"context"

	"github.com/uptrace/bun"
)

type ListParams struct {
	Status   string
	NodeID   string
	ASN      int64
	Search   string
	Page     int
	PageSize int
}

type UpdateOption func(map[string]interface{})

func WithRejectReason(reason string) UpdateOption {
	return func(m map[string]interface{}) {
		m["reject_reason"] = reason
	}
}

func applyPagination(q *bun.SelectQuery, params ListParams) *bun.SelectQuery {
	if params.Page < 1 {
		params.Page = 1
	}
	if params.PageSize < 1 {
		params.PageSize = 20
	}
	if params.PageSize > 200 {
		params.PageSize = 200
	}
	return q.Limit(params.PageSize).Offset((params.Page - 1) * params.PageSize)
}

type baseRepo struct {
	db *bun.DB
}

func (r *baseRepo) RunInTx(ctx context.Context, fn func(ctx context.Context, tx bun.Tx) error) error {
	return r.db.RunInTx(ctx, nil, fn)
}
