package handler

import (
	"context"
	"encoding/json"
	"time"

	"github.com/akaere/autopeer-center/internal/model"
	"github.com/akaere/autopeer-center/internal/queue"
	"github.com/akaere/autopeer-center/internal/repository"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

type auditEntry struct {
	sessionID      string
	asn            int64
	adminID        string
	keyID          string
	adminKeyID     string
	toolName       string
	args           interface{}
	capability     string
	mode           string
	resourceType   string
	resourceID     string
	idempotencyKey *string
	requestHash    string
	durationMs     int64
	clientIP       string
	userAgent      string
	resultOK       bool
	errMsg         string
}

type mcpSessionDB struct {
	mcp repository.MCPRepository
	q   *queue.Queue
	log *logrus.Entry
}

func newMCPSessionDB(mcp repository.MCPRepository, q *queue.Queue, logger *logrus.Entry) (*mcpSessionDB, func()) {
	s := &mcpSessionDB{
		mcp: mcp,
		q:   q,
		log: logger,
	}
	return s, func() {}
}

func (s *mcpSessionDB) buildAuditLog(e auditEntry) *model.MCPAuditLog {
	argsJSON, _ := json.Marshal(e.args)
	var sessionArg, adminArg, errArg *string
	// session_id is a UUID column (FK to mcp_sessions). Some callers — notably the
	// assistant tool path — pass a client conversation id (e.g. "conv_…"), which is
	// not a session UUID; persisting it raised "invalid input syntax for type uuid"
	// (22P02) and dropped the audit row. Only store the value when it is a real UUID.
	if e.sessionID != "" {
		if _, err := uuid.Parse(e.sessionID); err == nil {
			sessionArg = &e.sessionID
		}
	}
	if e.adminID != "" {
		adminArg = &e.adminID
	}
	var keyArg, adminKeyArg, capArg, resourceTypeArg, resourceIDArg, requestHashArg *string
	if e.keyID != "" {
		keyArg = &e.keyID
	}
	if e.adminKeyID != "" {
		adminKeyArg = &e.adminKeyID
	}
	if e.capability != "" {
		capArg = &e.capability
	}
	if e.resourceType != "" {
		resourceTypeArg = &e.resourceType
	}
	if e.resourceID != "" {
		resourceIDArg = &e.resourceID
	}
	if e.requestHash != "" {
		requestHashArg = &e.requestHash
	}
	var durationArg *int64
	if e.durationMs > 0 {
		durationArg = &e.durationMs
	}
	if e.errMsg != "" {
		errArg = &e.errMsg
	}
	var asnArg *int64
	if e.asn != 0 {
		asnArg = &e.asn
	}
	return &model.MCPAuditLog{
		SessionID:      sessionArg,
		ASN:            asnArg,
		AdminID:        adminArg,
		KeyID:          keyArg,
		AdminKeyID:     adminKeyArg,
		ToolName:       e.toolName,
		Args:           argsJSON,
		Capability:     capArg,
		Mode:           e.mode,
		ResourceType:   resourceTypeArg,
		ResourceID:     resourceIDArg,
		IdempotencyKey: e.idempotencyKey,
		RequestHash:    requestHashArg,
		DurationMs:     durationArg,
		ClientIP:       e.clientIP,
		UserAgent:      e.userAgent,
		ResultOK:       e.resultOK,
		ErrorMsg:       errArg,
	}
}

func (s *mcpSessionDB) writeAuditLog(sessionID string, asn int64, adminID, toolName string, args interface{}, resultOK bool, errMsg string) {
	e := auditEntry{
		sessionID: sessionID,
		asn:       asn,
		adminID:   adminID,
		toolName:  toolName,
		args:      args,
		mode:      "read",
		resultOK:  resultOK,
		errMsg:    errMsg,
	}
	s.writeAuditLogDetailed(e)
}

func (s *mcpSessionDB) writeAuditLogDetailed(e auditEntry) {
	if e.mode == "" {
		e.mode = "read"
	}
	entry := s.buildAuditLog(e)

	// Try Asynq first
	if s.q != nil && s.q.Available() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := s.q.EnqueueMCPAuditLog(ctx, entry)
		cancel()
		if err == nil {
			return
		}
		s.log.WithError(err).Warn("failed to enqueue mcp audit log, falling back to sync DB write")
	}

	// Fallback: synchronous DB write (never drop logs)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.mcp.LogAudit(ctx, entry); err != nil {
		s.log.WithError(err).Warn("failed to write mcp_audit_log")
	}
}

func (s *mcpSessionDB) insertMCPSession(sessionID string, asn int64, keyID string, isAdmin bool, adminKeyID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var keyIDArg, adminKeyIDArg *string
	if keyID != "" {
		keyIDArg = &keyID
	}
	if adminKeyID != "" {
		adminKeyIDArg = &adminKeyID
	}
	var asnArg *int64
	if asn != 0 {
		asnArg = &asn
	}
	sess := &model.MCPSession{
		ID:         sessionID,
		ASN:        asnArg,
		KeyID:      keyIDArg,
		IsAdmin:    isAdmin,
		AdminKeyID: adminKeyIDArg,
	}
	if err := s.mcp.CreateSession(ctx, sess); err != nil {
		s.log.WithError(err).Warn("failed to insert mcp_session")
	}
}

func (s *mcpSessionDB) closeMCPSession(sessionID string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.mcp.CloseSession(ctx, sessionID)
	}()
}

func (s *mcpSessionDB) pingMCPSession(sessionID string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = s.mcp.PingSession(ctx, sessionID)
	}()
}
