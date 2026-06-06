package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/akaere/autopeer-center/internal/model"
	"github.com/uptrace/bun"
)

type AuthRepository interface {
	CreateLoginCode(ctx context.Context, code *model.UserLoginCode) error
	GetLatestValidLoginCode(ctx context.Context, asn int64) (*model.UserLoginCode, error)
	MarkLoginCodeUsed(ctx context.Context, id string) (bool, error)
	IncrementLoginCodeFailures(ctx context.Context, id string) error
	GetEmailPreference(ctx context.Context, asn int64) (*model.PeerEmailPreference, error)
	UpsertEmailPreference(ctx context.Context, pref *model.PeerEmailPreference) error
	GetEmailLevel(ctx context.Context, asn int64) (int, error)
	ListNotificationPreferences(ctx context.Context, asn int64, channel string) ([]*model.PeerNotificationPreference, error)
	UpsertNotificationPreferences(ctx context.Context, asn int64, channel string, prefs []*model.PeerNotificationPreference) error
	GetNotificationPreferenceState(ctx context.Context, asn int64) (*model.PeerNotificationPreferenceState, error)
	UpsertNotificationPreferenceState(ctx context.Context, state *model.PeerNotificationPreferenceState) error
	CreateGPGChallenge(ctx context.Context, ch *model.GPGChallenge) error
	GetGPGChallenge(ctx context.Context, id string, asn int64) (*model.GPGChallenge, error)
	MarkGPGChallengeUsed(ctx context.Context, id string) (bool, error)
	CreateAuthSession(ctx context.Context, session *model.AuthSession) error
	GetAuthSession(ctx context.Context, id string) (*model.AuthSession, error)
	GetAuthSessionByRefreshHash(ctx context.Context, hash string) (*model.AuthSession, error)
	GetAuthSessionByPreviousRefreshHash(ctx context.Context, hash string) (*model.AuthSession, error)
	RotateAuthSessionRefresh(ctx context.Context, id, oldHash, newHash string) (bool, error)
	TouchAuthSession(ctx context.Context, id string) error
	RevokeAuthSession(ctx context.Context, id, reason string) (bool, error)
	RevokeChildAuthSessions(ctx context.Context, parentID, reason string) error
	ListUserAuthSessions(ctx context.Context, asn int64) ([]*model.AuthSession, error)
	ListAdminAuthSessions(ctx context.Context, adminID string) ([]*model.AuthSession, error)
	CreateAuthDeviceGrant(ctx context.Context, grant *model.AuthDeviceGrant) error
	GetAuthDeviceGrantByUserCodeHash(ctx context.Context, userCodeHash string) (*model.AuthDeviceGrant, error)
	AuthorizeAuthDeviceGrant(ctx context.Context, userCodeHash, decision string, asn *int64, email, sessionID, ip, userAgent string) (*model.AuthDeviceGrant, error)
	PollAuthDeviceGrant(ctx context.Context, deviceCodeHash string, interval time.Duration) (*model.AuthDeviceGrant, error)
	ExchangeAuthDeviceGrant(ctx context.Context, deviceCodeHash string, session *model.AuthSession) (*model.AuthDeviceGrant, error)
}

type bunAuthRepository struct {
	baseRepo
}

func NewAuthRepository(db *bun.DB) AuthRepository {
	return &bunAuthRepository{baseRepo{db: db}}
}

func (r *bunAuthRepository) CreateLoginCode(ctx context.Context, code *model.UserLoginCode) error {
	_, err := r.db.NewInsert().Model(code).Exec(ctx)
	return err
}

func (r *bunAuthRepository) GetLatestValidLoginCode(ctx context.Context, asn int64) (*model.UserLoginCode, error) {
	var c model.UserLoginCode
	err := r.db.NewSelect().
		Model(&c).
		Where("asn = ?", asn).
		Where("used = false").
		Where("expires_at > now()").
		Where("failed_attempts < 5").
		OrderExpr("created_at DESC").
		Limit(1).
		Scan(ctx)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *bunAuthRepository) MarkLoginCodeUsed(ctx context.Context, id string) (bool, error) {
	res, err := r.db.NewUpdate().
		Model((*model.UserLoginCode)(nil)).
		Set("used = true").
		Where("id = ?", id).
		Where("used = false").
		Exec(ctx)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (r *bunAuthRepository) IncrementLoginCodeFailures(ctx context.Context, id string) error {
	_, err := r.db.NewUpdate().
		Model((*model.UserLoginCode)(nil)).
		Set("failed_attempts = failed_attempts + 1").
		Where("id = ?", id).
		Exec(ctx)
	return err
}

func (r *bunAuthRepository) GetEmailPreference(ctx context.Context, asn int64) (*model.PeerEmailPreference, error) {
	var p model.PeerEmailPreference
	err := r.db.NewSelect().Model(&p).Where("asn = ?", asn).Scan(ctx)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *bunAuthRepository) UpsertEmailPreference(ctx context.Context, pref *model.PeerEmailPreference) error {
	_, err := r.db.NewInsert().
		Model(pref).
		On("CONFLICT (asn) DO UPDATE").
		Set("email_level = EXCLUDED.email_level").
		Set("updated_at = now()").
		Exec(ctx)
	return err
}

func (r *bunAuthRepository) GetEmailLevel(ctx context.Context, asn int64) (int, error) {
	var p model.PeerEmailPreference
	err := r.db.NewSelect().Model(&p).Where("asn = ?", asn).Scan(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return 0, err
	}
	return p.EmailLevel, nil
}

func (r *bunAuthRepository) ListNotificationPreferences(ctx context.Context, asn int64, channel string) ([]*model.PeerNotificationPreference, error) {
	prefs := make([]*model.PeerNotificationPreference, 0)
	err := r.db.NewSelect().
		Model(&prefs).
		Where("asn = ?", asn).
		Where("channel = ?", channel).
		Scan(ctx)
	return prefs, err
}

func (r *bunAuthRepository) UpsertNotificationPreferences(ctx context.Context, asn int64, channel string, prefs []*model.PeerNotificationPreference) error {
	return r.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewDelete().
			Model((*model.PeerNotificationPreference)(nil)).
			Where("asn = ?", asn).
			Where("channel = ?", channel).
			Exec(ctx); err != nil {
			return err
		}
		if len(prefs) == 0 {
			return nil
		}
		_, err := tx.NewInsert().Model(&prefs).Exec(ctx)
		return err
	})
}

func (r *bunAuthRepository) GetNotificationPreferenceState(ctx context.Context, asn int64) (*model.PeerNotificationPreferenceState, error) {
	var state model.PeerNotificationPreferenceState
	err := r.db.NewSelect().Model(&state).Where("asn = ?", asn).Scan(ctx)
	if err != nil {
		return nil, err
	}
	return &state, nil
}

func (r *bunAuthRepository) UpsertNotificationPreferenceState(ctx context.Context, state *model.PeerNotificationPreferenceState) error {
	_, err := r.db.NewInsert().
		Model(state).
		On("CONFLICT (asn) DO UPDATE").
		Set("seen_catalog_version = EXCLUDED.seen_catalog_version").
		Set("wizard_completed_at = EXCLUDED.wizard_completed_at").
		Set("updated_at = now()").
		Exec(ctx)
	return err
}

func (r *bunAuthRepository) CreateGPGChallenge(ctx context.Context, ch *model.GPGChallenge) error {
	_, err := r.db.NewInsert().Model(ch).Returning("id").Exec(ctx)
	return err
}

func (r *bunAuthRepository) GetGPGChallenge(ctx context.Context, id string, asn int64) (*model.GPGChallenge, error) {
	var ch model.GPGChallenge
	err := r.db.NewSelect().
		Model(&ch).
		Where("id = ?", id).
		Where("asn = ?", asn).
		Where("used = false").
		Where("expires_at > now()").
		Scan(ctx)
	if err != nil {
		return nil, err
	}
	return &ch, nil
}

func (r *bunAuthRepository) MarkGPGChallengeUsed(ctx context.Context, id string) (bool, error) {
	res, err := r.db.NewUpdate().
		Model((*model.GPGChallenge)(nil)).
		Set("used = true").
		Where("id = ?", id).
		Where("used = false").
		Exec(ctx)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (r *bunAuthRepository) CreateAuthSession(ctx context.Context, session *model.AuthSession) error {
	_, err := r.db.NewInsert().Model(session).Returning("*").Exec(ctx)
	return err
}

func (r *bunAuthRepository) GetAuthSession(ctx context.Context, id string) (*model.AuthSession, error) {
	var session model.AuthSession
	err := r.db.NewSelect().Model(&session).Where("id = ?", id).Scan(ctx)
	if err != nil {
		return nil, err
	}
	return &session, nil
}

func (r *bunAuthRepository) GetAuthSessionByRefreshHash(ctx context.Context, hash string) (*model.AuthSession, error) {
	var session model.AuthSession
	err := r.db.NewSelect().
		Model(&session).
		Where("refresh_token_hash = ?", hash).
		Where("revoked_at IS NULL").
		Where("expires_at > now()").
		Scan(ctx)
	if err != nil {
		return nil, err
	}
	return &session, nil
}

func (r *bunAuthRepository) GetAuthSessionByPreviousRefreshHash(ctx context.Context, hash string) (*model.AuthSession, error) {
	var session model.AuthSession
	err := r.db.NewSelect().
		Model(&session).
		Where("previous_refresh_token_hash = ?", hash).
		Where("previous_refresh_token_valid_until > now()").
		Where("revoked_at IS NULL").
		Where("expires_at > now()").
		Scan(ctx)
	if err != nil {
		return nil, err
	}
	return &session, nil
}

func (r *bunAuthRepository) RotateAuthSessionRefresh(ctx context.Context, id, oldHash, newHash string) (bool, error) {
	res, err := r.db.NewUpdate().
		Model((*model.AuthSession)(nil)).
		Set("refresh_token_hash = ?", newHash).
		Set("previous_refresh_token_hash = ?", oldHash).
		Set("previous_refresh_token_valid_until = now() + INTERVAL '10 seconds'").
		Set("last_used_at = now()").
		Where("id = ?", id).
		Where("refresh_token_hash = ?", oldHash).
		Where("revoked_at IS NULL").
		Where("expires_at > now()").
		Exec(ctx)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (r *bunAuthRepository) TouchAuthSession(ctx context.Context, id string) error {
	_, err := r.db.NewUpdate().
		Model((*model.AuthSession)(nil)).
		Set("last_used_at = now()").
		Where("id = ?", id).
		Where("revoked_at IS NULL").
		Exec(ctx)
	return err
}

func (r *bunAuthRepository) RevokeAuthSession(ctx context.Context, id, reason string) (bool, error) {
	res, err := r.db.NewUpdate().
		Model((*model.AuthSession)(nil)).
		Set("revoked_at = now()").
		Set("revoked_reason = ?", reason).
		Set("refresh_token_hash = NULL").
		Where("id = ?", id).
		Where("revoked_at IS NULL").
		Exec(ctx)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (r *bunAuthRepository) RevokeChildAuthSessions(ctx context.Context, parentID, reason string) error {
	_, err := r.db.NewUpdate().
		Model((*model.AuthSession)(nil)).
		Set("revoked_at = now()").
		Set("revoked_reason = ?", reason).
		Set("refresh_token_hash = NULL").
		Where("parent_session_id = ?", parentID).
		Where("revoked_at IS NULL").
		Exec(ctx)
	return err
}

func (r *bunAuthRepository) ListUserAuthSessions(ctx context.Context, asn int64) ([]*model.AuthSession, error) {
	sessions := make([]*model.AuthSession, 0)
	err := r.db.NewSelect().
		Model(&sessions).
		Where("subject_type = 'user'").
		Where("asn = ?", asn).
		Where("revoked_at IS NULL").
		Where("expires_at > now()").
		OrderExpr("last_used_at DESC NULLS LAST, created_at DESC").
		Scan(ctx)
	return sessions, err
}

func (r *bunAuthRepository) ListAdminAuthSessions(ctx context.Context, adminID string) ([]*model.AuthSession, error) {
	sessions := make([]*model.AuthSession, 0)
	err := r.db.NewSelect().
		Model(&sessions).
		Where("subject_type = 'admin'").
		Where("admin_id = ?", adminID).
		Where("revoked_at IS NULL").
		Where("expires_at > now()").
		OrderExpr("last_used_at DESC NULLS LAST, created_at DESC").
		Scan(ctx)
	return sessions, err
}

func (r *bunAuthRepository) CreateAuthDeviceGrant(ctx context.Context, grant *model.AuthDeviceGrant) error {
	_, err := r.db.NewInsert().Model(grant).Returning("*").Exec(ctx)
	return err
}

func (r *bunAuthRepository) GetAuthDeviceGrantByUserCodeHash(ctx context.Context, userCodeHash string) (*model.AuthDeviceGrant, error) {
	var grant model.AuthDeviceGrant
	err := r.db.NewSelect().Model(&grant).Where("user_code_hash = ?", userCodeHash).Scan(ctx)
	if err != nil {
		return nil, err
	}
	return &grant, nil
}

func (r *bunAuthRepository) AuthorizeAuthDeviceGrant(ctx context.Context, userCodeHash, decision string, asn *int64, email, sessionID, ip, userAgent string) (*model.AuthDeviceGrant, error) {
	var grant model.AuthDeviceGrant
	q := r.db.NewUpdate().
		Model(&grant).
		Where("user_code_hash = ?", userCodeHash).
		Where("status = 'pending'").
		Where("expires_at > now()").
		Set("status = ?", decision).
		Set("approve_user_agent = ?", userAgent)
	if ip != "" {
		q = q.Set("approve_ip = ?", ip)
	} else {
		q = q.Set("approve_ip = NULL")
	}
	switch decision {
	case "approved":
		if asn != nil {
			q = q.Set("approved_asn = ?", *asn)
		} else {
			q = q.Set("approved_asn = NULL")
		}
		q = q.Set("approved_email = ?", email).
			Set("approved_by_session_id = ?", sessionID).
			Set("approved_at = now()")
	case "denied":
		q = q.Set("denied_at = now()")
	default:
		return nil, fmt.Errorf("invalid device grant decision")
	}
	err := q.Returning("*").Scan(ctx)
	if err != nil {
		return nil, err
	}
	return &grant, nil
}

func (r *bunAuthRepository) PollAuthDeviceGrant(ctx context.Context, deviceCodeHash string, interval time.Duration) (*model.AuthDeviceGrant, error) {
	var grant model.AuthDeviceGrant
	nextPoll := time.Now().Add(interval)
	err := r.db.NewUpdate().
		Model(&grant).
		Set("poll_count = poll_count + 1").
		Set("last_polled_at = now()").
		Set("next_poll_after = ?", nextPoll).
		Where("device_code_hash = ?", deviceCodeHash).
		Returning("*").
		Scan(ctx)
	if err != nil {
		return nil, err
	}
	return &grant, nil
}

func (r *bunAuthRepository) ExchangeAuthDeviceGrant(ctx context.Context, deviceCodeHash string, session *model.AuthSession) (*model.AuthDeviceGrant, error) {
	var grant model.AuthDeviceGrant
	err := r.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := tx.NewUpdate().
			Model(&grant).
			Where("device_code_hash = ?", deviceCodeHash).
			Where("status = 'approved'").
			Where("exchanged_at IS NULL").
			Where("expires_at > now()").
			Set("status = 'exchanged'").
			Set("exchanged_at = now()").
			Returning("*").
			Scan(ctx); err != nil {
			return err
		}
		switch session.SubjectType {
		case "user":
			if grant.ApprovedASN == nil || grant.ApprovedEmail == "" {
				return fmt.Errorf("device grant is missing approved user")
			}
		case "admin":
			if session.AdminID == nil || grant.ApprovedBySessionID == nil || grant.ApprovedEmail == "" {
				return fmt.Errorf("device grant is missing approved admin")
			}
		default:
			return fmt.Errorf("unsupported device grant session subject")
		}
		_, err := tx.NewInsert().Model(session).Returning("*").Exec(ctx)
		if err != nil {
			return err
		}
		_, err = tx.NewUpdate().
			Model((*model.AuthDeviceGrant)(nil)).
			Set("exchanged_session_id = ?", session.ID).
			Where("id = ?", grant.ID).
			Exec(ctx)
		grant.ExchangedSessionID = &session.ID
		return err
	})
	if err != nil {
		return nil, err
	}
	return &grant, nil
}
