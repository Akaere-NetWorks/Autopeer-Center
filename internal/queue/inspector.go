package queue

import (
	"fmt"
	"strings"
	"time"

	"github.com/hibiken/asynq"
)

const maxPayloadDisplayLen = 512

// Inspector wraps asynq.Inspector for read-only monitoring and controlled write operations.
type Inspector struct {
	insp    *asynq.Inspector
	enabled bool
}

// NewInspector creates a new Inspector from the given Redis URL.
// If the URL is empty or invalid, returns a disabled Inspector.
func NewInspector(redisURL string) *Inspector {
	redisURL = strings.TrimSpace(redisURL)
	if redisURL == "" {
		return &Inspector{}
	}
	opt, err := asynq.ParseRedisURI(redisURL)
	if err != nil {
		log.WithError(err).Warn("invalid Redis URL for asynq inspector; monitor disabled")
		return &Inspector{}
	}
	return &Inspector{
		insp:    asynq.NewInspector(opt),
		enabled: true,
	}
}

// Close closes the underlying asynq inspector connection.
func (ins *Inspector) Close() error {
	if ins == nil || !ins.enabled || ins.insp == nil {
		return nil
	}
	return ins.insp.Close()
}

// Enabled returns true if the inspector is operational.
func (ins *Inspector) Enabled() bool {
	return ins != nil && ins.enabled
}

// Queues returns the list of all queue names.
func (ins *Inspector) Queues() ([]string, error) {
	return ins.insp.Queues()
}

// GetQueueInfo returns a snapshot of the named queue.
func (ins *Inspector) GetQueueInfo(qname string) (*QueueSnapshot, error) {
	info, err := ins.insp.GetQueueInfo(qname)
	if err != nil {
		return nil, err
	}
	return &QueueSnapshot{
		Name:           qname,
		Paused:         info.Paused,
		Size:           info.Size,
		Pending:        info.Pending,
		Active:         info.Active,
		Scheduled:      info.Scheduled,
		Retry:          info.Retry,
		Archived:       info.Archived,
		Completed:      info.Completed,
		Processed:      info.Processed,
		Failed:         info.Failed,
		ProcessedTotal: info.ProcessedTotal,
		FailedTotal:    info.FailedTotal,
		LatencyMs:      info.Latency.Milliseconds(),
		MemoryUsage:    int64(info.MemoryUsage),
	}, nil
}

// ListPendingTasks returns a paginated snapshot of pending tasks in the given queue.
func (ins *Inspector) ListPendingTasks(qname string, page, size int) (*TaskListSnapshot, error) {
	tasks, err := ins.insp.ListPendingTasks(qname, asynq.Page(page), asynq.PageSize(size))
	if err != nil {
		return nil, err
	}
	result := &TaskListSnapshot{Page: page, Size: size}
	for _, t := range tasks {
		result.Tasks = append(result.Tasks, taskInfoToSnapshot(t))
	}
	result.Total = len(result.Tasks) // asynq returns the full page; approximate
	if result.Total == size {
		// Might be more — get accurate count from queue info
		info, ierr := ins.insp.GetQueueInfo(qname)
		if ierr == nil {
			result.Total = info.Pending
		}
	}
	return result, nil
}

// ListActiveTasks returns a paginated snapshot of active tasks in the given queue.
func (ins *Inspector) ListActiveTasks(qname string, page, size int) (*TaskListSnapshot, error) {
	tasks, err := ins.insp.ListActiveTasks(qname, asynq.Page(page), asynq.PageSize(size))
	if err != nil {
		return nil, err
	}
	result := &TaskListSnapshot{Page: page, Size: size}
	for _, t := range tasks {
		result.Tasks = append(result.Tasks, taskInfoToSnapshot(t))
	}
	result.Total = len(result.Tasks)
	if result.Total == size {
		info, ierr := ins.insp.GetQueueInfo(qname)
		if ierr == nil {
			result.Total = info.Active
		}
	}
	return result, nil
}

// ListScheduledTasks returns a paginated snapshot of scheduled tasks in the given queue.
func (ins *Inspector) ListScheduledTasks(qname string, page, size int) (*TaskListSnapshot, error) {
	tasks, err := ins.insp.ListScheduledTasks(qname, asynq.Page(page), asynq.PageSize(size))
	if err != nil {
		return nil, err
	}
	result := &TaskListSnapshot{Page: page, Size: size}
	for _, t := range tasks {
		result.Tasks = append(result.Tasks, taskInfoToSnapshot(t))
	}
	result.Total = len(result.Tasks)
	if result.Total == size {
		info, ierr := ins.insp.GetQueueInfo(qname)
		if ierr == nil {
			result.Total = info.Scheduled
		}
	}
	return result, nil
}

// ListRetryTasks returns a paginated snapshot of retry tasks in the given queue.
func (ins *Inspector) ListRetryTasks(qname string, page, size int) (*TaskListSnapshot, error) {
	tasks, err := ins.insp.ListRetryTasks(qname, asynq.Page(page), asynq.PageSize(size))
	if err != nil {
		return nil, err
	}
	result := &TaskListSnapshot{Page: page, Size: size}
	for _, t := range tasks {
		result.Tasks = append(result.Tasks, taskInfoToSnapshot(t))
	}
	result.Total = len(result.Tasks)
	if result.Total == size {
		info, ierr := ins.insp.GetQueueInfo(qname)
		if ierr == nil {
			result.Total = info.Retry
		}
	}
	return result, nil
}

// ListArchivedTasks returns a paginated snapshot of archived tasks in the given queue.
func (ins *Inspector) ListArchivedTasks(qname string, page, size int) (*TaskListSnapshot, error) {
	tasks, err := ins.insp.ListArchivedTasks(qname, asynq.Page(page), asynq.PageSize(size))
	if err != nil {
		return nil, err
	}
	result := &TaskListSnapshot{Page: page, Size: size}
	for _, t := range tasks {
		result.Tasks = append(result.Tasks, taskInfoToSnapshot(t))
	}
	result.Total = len(result.Tasks)
	if result.Total == size {
		info, ierr := ins.insp.GetQueueInfo(qname)
		if ierr == nil {
			result.Total = info.Archived
		}
	}
	return result, nil
}

// ListCompletedTasks returns a paginated snapshot of completed tasks in the given queue.
func (ins *Inspector) ListCompletedTasks(qname string, page, size int) (*TaskListSnapshot, error) {
	tasks, err := ins.insp.ListCompletedTasks(qname, asynq.Page(page), asynq.PageSize(size))
	if err != nil {
		return nil, err
	}
	result := &TaskListSnapshot{Page: page, Size: size}
	for _, t := range tasks {
		result.Tasks = append(result.Tasks, taskInfoToSnapshot(t))
	}
	result.Total = len(result.Tasks)
	if result.Total == size {
		info, ierr := ins.insp.GetQueueInfo(qname)
		if ierr == nil {
			result.Total = info.Completed
		}
	}
	return result, nil
}

// GetTaskInfo returns a single task's details.
func (ins *Inspector) GetTaskInfo(qname, id string) (*TaskSnapshot, error) {
	info, err := ins.insp.GetTaskInfo(qname, id)
	if err != nil {
		return nil, err
	}
	snap := taskInfoToSnapshot(info)
	return &snap, nil
}

// Servers returns a snapshot list of all running asynq servers.
func (ins *Inspector) Servers() ([]*ServerSnapshot, error) {
	servers, err := ins.insp.Servers()
	if err != nil {
		return nil, err
	}
	result := make([]*ServerSnapshot, 0, len(servers))
	for _, s := range servers {
		snap := &ServerSnapshot{
			ID:             s.ID,
			Host:           s.Host,
			PID:            s.PID,
			Concurrency:    s.Concurrency,
			Queues:         s.Queues,
			StrictPriority: s.StrictPriority,
			StartedAt:      s.Started.Format(time.RFC3339),
			Status:         s.Status,
		}
		for _, w := range s.ActiveWorkers {
			snap.ActiveWorkers = append(snap.ActiveWorkers, WorkerSnapshot{
				TaskID:      w.TaskID,
				TaskType:    w.TaskType,
				TaskPayload: truncatePayload(w.TaskPayload),
				Queue:       w.Queue,
				StartedAt:   w.Started.Format(time.RFC3339),
				Deadline:    w.Deadline.Format(time.RFC3339),
			})
		}
		result = append(result, snap)
	}
	return result, nil
}

// SchedulerEntries returns a snapshot list of all scheduler entries.
func (ins *Inspector) SchedulerEntries() ([]*SchedulerEntrySnapshot, error) {
	entries, err := ins.insp.SchedulerEntries()
	if err != nil {
		return nil, err
	}
	result := make([]*SchedulerEntrySnapshot, 0, len(entries))
	for _, e := range entries {
		result = append(result, &SchedulerEntrySnapshot{
			ID:          e.ID,
			Spec:        e.Spec,
			TaskType:    e.Task.Type(),
			TaskPayload: truncatePayload(e.Task.Payload()),
			Next:        e.Next.Format(time.RFC3339),
			Prev:        e.Prev.Format(time.RFC3339),
		})
	}
	return result, nil
}

// ListSchedulerEnqueueEvents returns paginated enqueue events for a scheduler entry.
func (ins *Inspector) ListSchedulerEnqueueEvents(entryID string, page, size int) ([]*EnqueueEventSnapshot, error) {
	events, err := ins.insp.ListSchedulerEnqueueEvents(entryID, asynq.Page(page), asynq.PageSize(size))
	if err != nil {
		return nil, err
	}
	result := make([]*EnqueueEventSnapshot, 0, len(events))
	for _, e := range events {
		result = append(result, &EnqueueEventSnapshot{
			TaskID:     e.TaskID,
			EnqueuedAt: e.EnqueuedAt.Format(time.RFC3339),
		})
	}
	return result, nil
}

// History returns daily stats for the given queue over the specified number of days.
func (ins *Inspector) History(qname string, days int) ([]*DailyStatsSnapshot, error) {
	stats, err := ins.insp.History(qname, days)
	if err != nil {
		return nil, err
	}
	result := make([]*DailyStatsSnapshot, 0, len(stats))
	for _, s := range stats {
		result = append(result, &DailyStatsSnapshot{
			Queue:     s.Queue,
			Processed: s.Processed,
			Failed:    s.Failed,
			Date:      s.Date.Format("2006-01-02"),
		})
	}
	return result, nil
}

// DeleteTask deletes a single task by queue name and task ID.
func (ins *Inspector) DeleteTask(qname, id string) error {
	return ins.insp.DeleteTask(qname, id)
}

// RunTask moves a task to the pending state for immediate execution.
func (ins *Inspector) RunTask(qname, id string) error {
	return ins.insp.RunTask(qname, id)
}

// ArchiveTask archives a task.
func (ins *Inspector) ArchiveTask(qname, id string) error {
	return ins.insp.ArchiveTask(qname, id)
}

// DeleteAllTasks deletes all tasks in the given state for the named queue.
func (ins *Inspector) DeleteAllTasks(qname string, state string) error {
	var err error
	switch state {
	case "pending":
		_, err = ins.insp.DeleteAllPendingTasks(qname)
	case "active":
		_, err = ins.insp.DeleteAllPendingTasks(qname) // active tasks cannot be bulk-deleted; fallback to pending
	case "scheduled":
		_, err = ins.insp.DeleteAllScheduledTasks(qname)
	case "retry":
		_, err = ins.insp.DeleteAllRetryTasks(qname)
	case "archived":
		_, err = ins.insp.DeleteAllArchivedTasks(qname)
	case "completed":
		_, err = ins.insp.DeleteAllCompletedTasks(qname)
	default:
		return fmt.Errorf("unknown task state: %s", state)
	}
	return err
}

// PauseQueue pauses the named queue.
func (ins *Inspector) PauseQueue(qname string) error {
	return ins.insp.PauseQueue(qname)
}

// UnpauseQueue resumes the named queue.
func (ins *Inspector) UnpauseQueue(qname string) error {
	return ins.insp.UnpauseQueue(qname)
}

// CancelProcessing sends a cancellation signal to the active task with the given ID.
func (ins *Inspector) CancelProcessing(taskID string) error {
	return ins.insp.CancelProcessing(taskID)
}

// --- Snapshot types (JSON-friendly, no asynq internal types exposed) ---

type QueueSnapshot struct {
	Name           string `json:"name"`
	Paused         bool   `json:"paused"`
	Size           int    `json:"size"`
	Pending        int    `json:"pending"`
	Active         int    `json:"active"`
	Scheduled      int    `json:"scheduled"`
	Retry          int    `json:"retry"`
	Archived       int    `json:"archived"`
	Completed      int    `json:"completed"`
	Processed      int    `json:"processed"`
	Failed         int    `json:"failed"`
	ProcessedTotal int    `json:"processed_total"`
	FailedTotal    int    `json:"failed_total"`
	LatencyMs      int64  `json:"latency_ms"`
	MemoryUsage    int64  `json:"memory_usage_bytes"`
}

type TaskSnapshot struct {
	ID            string `json:"id"`
	Type          string `json:"type"`
	Payload       string `json:"payload"`
	State         string `json:"state"`
	Queue         string `json:"queue"`
	RetryCount    int    `json:"retry_count"`
	MaxRetry      int    `json:"max_retry"`
	RetriedAt     string `json:"retried_at,omitempty"`
	LastFailedAt  string `json:"last_failed_at,omitempty"`
	LastError     string `json:"last_error,omitempty"`
	NextProcessAt string `json:"next_process_at,omitempty"`
	CompletedAt   string `json:"completed_at,omitempty"`
	Result        string `json:"result,omitempty"`
	Timeout       int    `json:"timeout_seconds"`
	Deadline      string `json:"deadline,omitempty"`
	StartedAt     string `json:"started_at,omitempty"`
	WorkerID      string `json:"worker_id,omitempty"`
}

type TaskListSnapshot struct {
	Tasks []TaskSnapshot `json:"tasks"`
	Total int            `json:"total_count"`
	Page  int            `json:"page"`
	Size  int            `json:"size"`
}

type ServerSnapshot struct {
	ID             string           `json:"id"`
	Host           string           `json:"host"`
	PID            int              `json:"pid"`
	Concurrency    int              `json:"concurrency"`
	Queues         map[string]int   `json:"queues"`
	StrictPriority bool             `json:"strict_priority"`
	StartedAt      string           `json:"started_at"`
	ActiveWorkers  []WorkerSnapshot `json:"active_workers"`
	Status         string           `json:"status"`
}

type WorkerSnapshot struct {
	TaskID      string `json:"task_id"`
	TaskType    string `json:"task_type"`
	TaskPayload string `json:"task_payload"`
	Queue       string `json:"queue"`
	StartedAt   string `json:"started_at"`
	Deadline    string `json:"deadline"`
}

type SchedulerEntrySnapshot struct {
	ID          string `json:"id"`
	Spec        string `json:"spec"`
	TaskType    string `json:"task_type"`
	TaskPayload string `json:"task_payload"`
	Next        string `json:"next"`
	Prev        string `json:"prev"`
}

type EnqueueEventSnapshot struct {
	TaskID     string `json:"task_id"`
	EnqueuedAt string `json:"enqueued_at"`
}

type DailyStatsSnapshot struct {
	Queue     string `json:"queue"`
	Processed int    `json:"processed"`
	Failed    int    `json:"failed"`
	Date      string `json:"date"`
}

// --- helpers ---

func truncatePayload(b []byte) string {
	s := string(b)
	if len(s) > maxPayloadDisplayLen {
		return s[:maxPayloadDisplayLen] + "...(truncated)"
	}
	return s
}

func taskInfoToSnapshot(t *asynq.TaskInfo) TaskSnapshot {
	return TaskSnapshot{
		ID:            t.ID,
		Type:          t.Type,
		Payload:       truncatePayload(t.Payload),
		State:         t.State.String(),
		Queue:         t.Queue,
		RetryCount:    t.Retried,
		MaxRetry:      t.MaxRetry,
		LastFailedAt:  formatTime(t.LastFailedAt),
		LastError:     t.LastErr,
		NextProcessAt: formatTime(t.NextProcessAt),
		CompletedAt:   formatTime(t.CompletedAt),
		Result:        string(t.Result),
		Timeout:       int(t.Timeout.Seconds()),
		Deadline:      formatTime(t.Deadline),
	}
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}
