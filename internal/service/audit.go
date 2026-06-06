package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/akaere/autopeer-center/internal/model"
	"github.com/akaere/autopeer-center/internal/repository"
	"github.com/sirupsen/logrus"
)

var auditLog = logrus.WithField("pkg", "service.audit")

type AuditService struct {
	auditRepo repository.AuditRepository
}

func NewAuditService(auditRepo repository.AuditRepository) *AuditService {
	return &AuditService{auditRepo: auditRepo}
}

func (s *AuditService) Log(ctx context.Context, action, operator string, targetID *string, detail map[string]interface{}) error {
	fields := logrus.Fields{
		"action":   action,
		"operator": operator,
	}
	if targetID != nil {
		fields["target_id"] = *targetID
	}
	auditLog.WithFields(fields).Debug("audit log entry")

	var detailJSON []byte
	var err error
	if detail != nil {
		detailJSON, err = json.Marshal(detail)
		if err != nil {
			return err
		}
	}

	var detailMap map[string]interface{}
	if detailJSON != nil {
		json.Unmarshal(detailJSON, &detailMap)
	}

	entry := &model.AuditLog{
		Action:    action,
		Operator:  operator,
		TargetID:  targetID,
		Detail:    detailMap,
		CreatedAt: time.Now().UTC(),
	}

	if err := s.auditRepo.Log(ctx, entry); err != nil {
		auditLog.WithError(err).WithFields(logrus.Fields{
			"action":   action,
			"operator": operator,
		}).Error("audit log DB insert failed")
		return err
	}
	return nil
}

func (s *AuditService) List(ctx context.Context, action, operator string, page, perPage int) ([]*model.AuditLog, int, error) {
	return s.auditRepo.ListFiltered(ctx, action, operator, page, perPage)
}

func (s *AuditService) ListForASN(ctx context.Context, asn int64, action string, page, perPage int) ([]*model.AuditLog, int, error) {
	return s.auditRepo.ListForASN(ctx, asn, action, page, perPage)
}

func (s *AuditService) ListByTargetID(ctx context.Context, targetID string, limit int) ([]*model.AuditLog, error) {
	return s.auditRepo.ListByTargetID(ctx, targetID, limit)
}
