package repository

import (
	"context"
	"time"

	"github.com/akaere/autopeer-center/internal/model"
	"github.com/uptrace/bun"
)

type PasskeyRepository interface {
	CreateCredential(ctx context.Context, cred *model.PasskeyCredential) error
	GetCredentialsByASN(ctx context.Context, asn int64) ([]*model.PasskeyCredential, error)
	GetCredentialByCredentialID(ctx context.Context, credentialID string) (*model.PasskeyCredential, error)
	UpdateSignCount(ctx context.Context, credentialID string, count int64) error
	TouchCredential(ctx context.Context, credentialID string) error
	DeleteCredential(ctx context.Context, id string, asn int64) error
	CountCredentialsByASN(ctx context.Context, asn int64) (int, error)

	SaveWebAuthnSession(ctx context.Context, asn int64, sessionType, sessionData string) error
	GetWebAuthnSession(ctx context.Context, asn int64, sessionType string) (string, error)
	DeleteWebAuthnSession(ctx context.Context, asn int64, sessionType string) error
}

type bunPasskeyRepository struct {
	baseRepo
}

func NewPasskeyRepository(db *bun.DB) PasskeyRepository {
	return &bunPasskeyRepository{baseRepo{db: db}}
}

func (r *bunPasskeyRepository) CreateCredential(ctx context.Context, cred *model.PasskeyCredential) error {
	_, err := r.db.NewInsert().Model(cred).Exec(ctx)
	return err
}

func (r *bunPasskeyRepository) GetCredentialsByASN(ctx context.Context, asn int64) ([]*model.PasskeyCredential, error) {
	var creds []*model.PasskeyCredential
	err := r.db.NewSelect().
		Model(&creds).
		Where("asn = ?", asn).
		OrderExpr("created_at ASC").
		Scan(ctx)
	if err != nil {
		return nil, err
	}
	return creds, nil
}

func (r *bunPasskeyRepository) GetCredentialByCredentialID(ctx context.Context, credentialID string) (*model.PasskeyCredential, error) {
	var cred model.PasskeyCredential
	err := r.db.NewSelect().
		Model(&cred).
		Where("credential_id = ?", credentialID).
		Scan(ctx)
	if err != nil {
		return nil, err
	}
	return &cred, nil
}

func (r *bunPasskeyRepository) UpdateSignCount(ctx context.Context, credentialID string, count int64) error {
	_, err := r.db.NewUpdate().
		Model((*model.PasskeyCredential)(nil)).
		Set("sign_count = ?", count).
		Where("credential_id = ?", credentialID).
		Exec(ctx)
	return err
}

func (r *bunPasskeyRepository) TouchCredential(ctx context.Context, credentialID string) error {
	now := time.Now()
	_, err := r.db.NewUpdate().
		Model((*model.PasskeyCredential)(nil)).
		Set("last_used_at = ?", now).
		Where("credential_id = ?", credentialID).
		Exec(ctx)
	return err
}

func (r *bunPasskeyRepository) DeleteCredential(ctx context.Context, id string, asn int64) error {
	_, err := r.db.NewDelete().
		Model((*model.PasskeyCredential)(nil)).
		Where("id = ?", id).
		Where("asn = ?", asn).
		Exec(ctx)
	return err
}

func (r *bunPasskeyRepository) CountCredentialsByASN(ctx context.Context, asn int64) (int, error) {
	count, err := r.db.NewSelect().
		Model((*model.PasskeyCredential)(nil)).
		Where("asn = ?", asn).
		Count(ctx)
	return count, err
}

func (r *bunPasskeyRepository) SaveWebAuthnSession(ctx context.Context, asn int64, sessionType, sessionData string) error {
	// Atomically replace any existing session (delete + insert in one transaction
	// to avoid a race condition where two concurrent calls both read "no row" and
	// both attempt to insert, causing a unique-constraint violation).
	return r.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewDelete().
			Model((*model.WebAuthnSession)(nil)).
			Where("asn = ? AND type = ?", asn, sessionType).
			Exec(ctx); err != nil {
			return err
		}
		sess := &model.WebAuthnSession{
			ASN:         asn,
			Type:        sessionType,
			SessionData: sessionData,
			ExpiresAt:   time.Now().Add(5 * time.Minute),
		}
		_, err := tx.NewInsert().Model(sess).Exec(ctx)
		return err
	})
}

func (r *bunPasskeyRepository) GetWebAuthnSession(ctx context.Context, asn int64, sessionType string) (string, error) {
	var sess model.WebAuthnSession
	err := r.db.NewSelect().
		Model(&sess).
		Where("asn = ? AND type = ?", asn, sessionType).
		Where("expires_at > now()").
		OrderExpr("created_at DESC").
		Limit(1).
		Scan(ctx)
	if err != nil {
		return "", err
	}
	return sess.SessionData, nil
}

func (r *bunPasskeyRepository) DeleteWebAuthnSession(ctx context.Context, asn int64, sessionType string) error {
	_, err := r.db.NewDelete().
		Model((*model.WebAuthnSession)(nil)).
		Where("asn = ? AND type = ?", asn, sessionType).
		Exec(ctx)
	return err
}
