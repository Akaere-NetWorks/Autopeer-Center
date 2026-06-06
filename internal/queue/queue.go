package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/akaere/autopeer-center/internal/config"
	"github.com/akaere/autopeer-center/internal/model"
	"github.com/akaere/autopeer-center/internal/repository"
	"github.com/hibiken/asynq"
	"github.com/sirupsen/logrus"
)

const (
	TypeRequestLog        = "request_log:write"
	TypeMCPAuditLog       = "mcp_audit_log:write"
	TypeCleanupDaily      = "cleanup:daily"
	TypeCleanupTimeout    = "cleanup:timeout_updating"
	TypeLatencyCheckAll   = "latency:check_all"
	TypeLatencyCheckPeer  = "latency:check_peer"
	TypeEndpointCheckAll  = "endpoint:check_all"
)

var log = logrus.WithField("pkg", "queue")

type Queue struct {
	enabled      atomic.Bool
	backoffUntil atomic.Int64
	client       *asynq.Client
	server       *asynq.Server
	mux          *asynq.ServeMux
	scheduler    *asynq.Scheduler
}

func New(cfg *config.Config, redisURL string, redisAvailable bool) *Queue {
	if cfg == nil || !cfg.AsynqEnabled {
		return &Queue{}
	}
	if !redisAvailable || strings.TrimSpace(redisURL) == "" {
		log.Warn("ASYNQ_ENABLED=true but Redis is unavailable; queue disabled")
		return &Queue{}
	}
	opt, err := asynq.ParseRedisURI(redisURL)
	if err != nil {
		log.WithError(err).Warn("invalid Redis URL for asynq; queue disabled")
		return &Queue{}
	}
	q := &Queue{client: asynq.NewClient(opt), mux: asynq.NewServeMux()}
	q.enabled.Store(true)
	concurrency := cfg.AsynqConcurrency
	if concurrency <= 0 {
		concurrency = 10
	}
	q.server = asynq.NewServer(opt, asynq.Config{Concurrency: concurrency, Queues: parseQueues(cfg.AsynqQueues)})
	return q
}

func (q *Queue) Enabled() bool { return q != nil && q.enabled.Load() }

func (q *Queue) Available() bool {
	return q.Enabled() && time.Now().UnixNano() >= q.backoffUntil.Load()
}

func (q *Queue) BackoffUntil() time.Time {
	if q == nil {
		return time.Time{}
	}
	ns := q.backoffUntil.Load()
	if ns <= 0 {
		return time.Time{}
	}
	return time.Unix(0, ns)
}

func ParseQueues(raw string) map[string]int { return parseQueues(raw) }

func (q *Queue) Start(ctx context.Context) error {
	if !q.Enabled() || q.server == nil || q.mux == nil {
		return nil
	}
	go func() {
		<-ctx.Done()
		q.server.Shutdown()
	}()
	go func() {
		if err := q.server.Run(q.mux); err != nil {
			q.enabled.Store(false)
			log.WithError(err).Error("asynq server stopped with error")
		}
	}()
	log.Info("asynq queue started")
	return nil
}

func (q *Queue) Shutdown(context.Context) error {
	if q == nil {
		return nil
	}
	if q.scheduler != nil {
		q.scheduler.Shutdown()
	}
	if q.server != nil {
		q.enabled.Store(false)
		q.server.Shutdown()
	}
	if q.client != nil {
		return q.client.Close()
	}
	return nil
}

func (q *Queue) EnqueueJSON(ctx context.Context, typ string, payload any) error {
	if !q.Available() || q.client == nil {
		return fmt.Errorf("queue disabled")
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = q.client.EnqueueContext(ctx, asynq.NewTask(typ, data), asynq.Queue("default"), asynq.MaxRetry(5), asynq.Timeout(30*time.Second))
	if err != nil {
		q.backoffUntil.Store(time.Now().Add(30 * time.Second).UnixNano())
	} else {
		q.backoffUntil.Store(0)
	}
	return err
}

func (q *Queue) enqueueTask(ctx context.Context, typ string, payload []byte, queue string, opts ...asynq.Option) error {
	if !q.Available() || q.client == nil {
		return fmt.Errorf("queue disabled")
	}
	allOpts := []asynq.Option{asynq.Queue(queue), asynq.MaxRetry(5), asynq.Timeout(30 * time.Second)}
	allOpts = append(allOpts, opts...)
	_, err := q.client.EnqueueContext(ctx, asynq.NewTask(typ, payload), allOpts...)
	if err != nil {
		q.backoffUntil.Store(time.Now().Add(30 * time.Second).UnixNano())
	} else {
		q.backoffUntil.Store(0)
	}
	return err
}

// --------------- Enqueue helpers ---------------

func (q *Queue) EnqueueRequestLog(ctx context.Context, entry *model.RequestLog) error {
	return q.EnqueueJSON(ctx, TypeRequestLog, entry)
}

func (q *Queue) EnqueueMCPAuditLog(ctx context.Context, entry *model.MCPAuditLog) error {
	return q.EnqueueJSON(ctx, TypeMCPAuditLog, entry)
}

func (q *Queue) EnqueueLatencyCheckPeer(ctx context.Context, p []byte) error {
	return q.enqueueTask(ctx, TypeLatencyCheckPeer, p, "low")
}

// --------------- Handler registration ---------------

func (q *Queue) RegisterRequestLogHandler(metrics repository.MetricsRepository) {
	if !q.Enabled() || q.mux == nil {
		return
	}
	q.mux.HandleFunc(TypeRequestLog, func(ctx context.Context, task *asynq.Task) error {
		var entry model.RequestLog
		if err := json.Unmarshal(task.Payload(), &entry); err != nil {
			return err
		}
		return metrics.LogRequest(ctx, &entry)
	})
}

func (q *Queue) RegisterMCPAuditLogHandler(mcpRepo repository.MCPRepository) {
	if !q.Enabled() || q.mux == nil {
		return
	}
	q.mux.HandleFunc(TypeMCPAuditLog, func(ctx context.Context, task *asynq.Task) error {
		var entry model.MCPAuditLog
		if err := json.Unmarshal(task.Payload(), &entry); err != nil {
			return err
		}
		return mcpRepo.LogAudit(ctx, &entry)
	})
}

func (q *Queue) RegisterCleanupHandler(cleanupRepo repository.CleanupRepository, nodeRepo repository.NodeRepository) {
	if !q.Enabled() || q.mux == nil {
		return
	}

	q.mux.HandleFunc(TypeCleanupDaily, func(ctx context.Context, task *asynq.Task) error {
		execCleanup := func(label string, fn func(context.Context) (int, error)) {
			n, err := fn(ctx)
			if err != nil {
				log.WithError(err).Errorf("cleanup %s error", label)
			} else {
				log.WithField("rows", n).Infof("cleanup %s completed", label)
			}
		}
		execCleanup("audit_logs", cleanupRepo.DeleteExpiredAuditLogs)
		execCleanup("request_logs", cleanupRepo.DeleteExpiredRequestLogs)
		execCleanup("user_login_codes", cleanupRepo.DeleteExpiredLoginCodes)
		execCleanup("gpg_challenges", cleanupRepo.DeleteExpiredGPGChallenges)
		execCleanup("bot_command_stats", cleanupRepo.DeleteExpiredBotCommandStats)
		execCleanup("auth_sessions", cleanupRepo.DeleteExpiredAuthSessions)
		execCleanup("webauthn_sessions", cleanupRepo.DeleteExpiredWebAuthnSessions)
		return nil
	})

	q.mux.HandleFunc(TypeCleanupTimeout, func(ctx context.Context, task *asynq.Task) error {
		if err := nodeRepo.MarkStaleUpdating(ctx); err != nil {
			log.WithError(err).Error("cleanup updating timeout error")
		}
		return nil
	})
}

type LatencyCheckAllFn func(ctx context.Context, q *Queue) error
type LatencyCheckPeerFn func(ctx context.Context, payload []byte) error

type EndpointCheckAllFn func(ctx context.Context) error

func (q *Queue) RegisterEndpointCheckHandler(checkAllFn EndpointCheckAllFn) {
	if !q.Enabled() || q.mux == nil {
		return
	}
	q.mux.HandleFunc(TypeEndpointCheckAll, func(ctx context.Context, task *asynq.Task) error {
		return checkAllFn(ctx)
	})
}

func (q *Queue) RegisterLatencyCheckHandler(checkAllFn LatencyCheckAllFn, checkPeerFn LatencyCheckPeerFn) {
	if !q.Enabled() || q.mux == nil {
		return
	}
	q.mux.HandleFunc(TypeLatencyCheckAll, func(ctx context.Context, task *asynq.Task) error {
		return checkAllFn(ctx, q)
	})
	q.mux.HandleFunc(TypeLatencyCheckPeer, func(ctx context.Context, task *asynq.Task) error {
		return checkPeerFn(ctx, task.Payload())
	})
}

// --------------- Scheduler & periodic tasks ---------------

func (q *Queue) InitScheduler(redisURL string) {
	if !q.Enabled() || strings.TrimSpace(redisURL) == "" {
		return
	}
	opt, err := asynq.ParseRedisURI(redisURL)
	if err != nil {
		log.WithError(err).Warn("failed to parse Redis URL for scheduler")
		return
	}
	q.scheduler = asynq.NewScheduler(opt, nil)
}

type periodicTask struct {
	taskSpec  string
	typ       string
	payload   []byte
	queue     string
	timeout   time.Duration
	retention time.Duration // duration to keep completed tasks in Redis
}

func (q *Queue) RegisterPeriodicTasks() {
	if !q.Enabled() || q.scheduler == nil {
		return
	}

	tasks := []periodicTask{
		{"@daily", TypeCleanupDaily, nil, "default", 120 * time.Second, 24 * time.Hour},
		{"@every 30s", TypeCleanupTimeout, nil, "default", 10 * time.Second, 24 * time.Hour},
		{"@every 1m", TypeLatencyCheckAll, nil, "default", 60 * time.Second, 24 * time.Hour},
		{"@every 15m", TypeEndpointCheckAll, nil, "default", 60 * time.Second, 24 * time.Hour},
	}

	for _, pt := range tasks {
		t := asynq.NewTask(pt.typ, pt.payload)
		opts := []asynq.Option{asynq.Queue(pt.queue), asynq.MaxRetry(3), asynq.Timeout(pt.timeout)}
		if pt.retention > 0 {
			opts = append(opts, asynq.Retention(pt.retention))
		}
		_, err := q.scheduler.Register(pt.taskSpec, t, opts...)
		if err != nil {
			log.WithError(err).WithField("type", pt.typ).Warn("failed to register periodic task")
		} else {
			log.WithFields(logrus.Fields{"type": pt.typ, "spec": pt.taskSpec}).Info("registered periodic task")
		}
	}
}

// EnqueueInitialTick fires all periodic tasks once immediately at startup.
func (q *Queue) EnqueueInitialTick() {
	if !q.Available() {
		return
	}
	initialTasks := []struct {
		typ       string
		queue     string
		timeout   time.Duration
		retention time.Duration // duration to keep completed tasks in Redis
	}{
		{TypeCleanupDaily, "default", 120 * time.Second, 24 * time.Hour},
		{TypeCleanupTimeout, "default", 10 * time.Second, 24 * time.Hour},
		{TypeLatencyCheckAll, "default", 60 * time.Second, 24 * time.Hour},
		{TypeEndpointCheckAll, "default", 60 * time.Second, 24 * time.Hour},
	}
	for _, t := range initialTasks {
		opts := []asynq.Option{asynq.MaxRetry(1), asynq.Timeout(t.timeout)}
		if t.retention > 0 {
			opts = append(opts, asynq.Retention(t.retention))
		}
		if err := q.enqueueTask(context.Background(), t.typ, nil, t.queue, opts...); err != nil {
			log.WithError(err).WithField("type", t.typ).Warn("failed to enqueue initial task")
		}
	}
}

func (q *Queue) StartScheduler() error {
	if !q.Enabled() || q.scheduler == nil {
		return nil
	}
	if err := q.scheduler.Run(); err != nil {
		log.WithError(err).Error("asynq scheduler stopped with error")
		return err
	}
	return nil
}

func parseQueues(raw string) map[string]int {
	queues := map[string]int{"critical": 6, "default": 3, "low": 1}
	if strings.TrimSpace(raw) == "" {
		return queues
	}
	parsed := map[string]int{}
	for _, part := range strings.Split(raw, ",") {
		kv := strings.SplitN(strings.TrimSpace(part), ":", 2)
		if len(kv) != 2 {
			continue
		}
		weight, err := strconv.Atoi(kv[1])
		if err == nil && weight > 0 && kv[0] != "" {
			parsed[kv[0]] = weight
		}
	}
	if len(parsed) == 0 {
		return queues
	}
	return parsed
}
