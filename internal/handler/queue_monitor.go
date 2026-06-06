package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/akaere/autopeer-center/internal/queue"
	"github.com/go-chi/chi/v5"
	"github.com/hibiken/asynq"
	"github.com/sirupsen/logrus"
)

var qmLog = logrus.WithField("pkg", "handler.queue_monitor")

type QueueMonitorHandler struct {
	queue     *queue.Queue
	inspector *queue.Inspector
	readonly  bool
}

func NewQueueMonitorHandler(q *queue.Queue, insp *queue.Inspector, readonly bool) *QueueMonitorHandler {
	return &QueueMonitorHandler{queue: q, inspector: insp, readonly: readonly}
}

// Overview returns a general overview of the queue system: queue status, all
// queue snapshots, running servers, and scheduler entries.
func (h *QueueMonitorHandler) Overview(w http.ResponseWriter, r *http.Request) {
	if h.inspector == nil || !h.inspector.Enabled() {
		ErrorJSON(w, r, http.StatusServiceUnavailable, "queue_monitor_unavailable", "Asynq inspector is not available")
		return
	}

	result := map[string]interface{}{
		"enabled":   h.queue != nil && h.queue.Enabled(),
		"available": h.queue != nil && h.queue.Available(),
	}

	queues, err := h.inspector.Queues()
	if err != nil {
		handleInspectorError(w, r, err)
		return
	}

	queueSnapshots := make([]*queue.QueueSnapshot, 0, len(queues))
	for _, qname := range queues {
		snap, err := h.inspector.GetQueueInfo(qname)
		if err != nil {
			qmLog.WithError(err).WithField("queue", qname).Warn("failed to get queue info")
			continue
		}
		queueSnapshots = append(queueSnapshots, snap)
	}
	result["queues"] = queueSnapshots

	servers, err := h.inspector.Servers()
	if err != nil {
		qmLog.WithError(err).Warn("failed to list servers")
	} else {
		result["servers"] = servers
	}

	entries, err := h.inspector.SchedulerEntries()
	if err != nil {
		qmLog.WithError(err).Warn("failed to list scheduler entries")
	} else {
		result["scheduler_entries"] = entries
	}

	JSON(w, http.StatusOK, result)
}

// ListQueues returns a full snapshot for every known queue.
func (h *QueueMonitorHandler) ListQueues(w http.ResponseWriter, r *http.Request) {
	if h.inspector == nil || !h.inspector.Enabled() {
		ErrorJSON(w, r, http.StatusServiceUnavailable, "queue_monitor_unavailable", "Asynq inspector is not available")
		return
	}

	queues, err := h.inspector.Queues()
	if err != nil {
		handleInspectorError(w, r, err)
		return
	}

	snapshots := make([]*queue.QueueSnapshot, 0, len(queues))
	for _, qname := range queues {
		snap, err := h.inspector.GetQueueInfo(qname)
		if err != nil {
			qmLog.WithError(err).WithField("queue", qname).Warn("failed to get queue info")
			continue
		}
		snapshots = append(snapshots, snap)
	}

	JSON(w, http.StatusOK, snapshots)
}

// GetQueue returns the snapshot and daily history for a single queue.
func (h *QueueMonitorHandler) GetQueue(w http.ResponseWriter, r *http.Request) {
	if h.inspector == nil || !h.inspector.Enabled() {
		ErrorJSON(w, r, http.StatusServiceUnavailable, "queue_monitor_unavailable", "Asynq inspector is not available")
		return
	}

	qname := chi.URLParam(r, "queueName")
	if qname == "" {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "Queue name is required")
		return
	}

	snap, err := h.inspector.GetQueueInfo(qname)
	if err != nil {
		handleInspectorError(w, r, err)
		return
	}

	days := 30
	if d := r.URL.Query().Get("days"); d != "" {
		if parsed, err := strconv.Atoi(d); err == nil && parsed > 0 {
			days = parsed
		}
	}

	history, err := h.inspector.History(qname, days)
	if err != nil {
		qmLog.WithError(err).WithField("queue", qname).Warn("failed to get queue history")
		history = nil
	}

	JSON(w, http.StatusOK, map[string]interface{}{
		"queue":   snap,
		"history": history,
	})
}

// ListTasks returns a paginated list of tasks for the given queue and state.
func (h *QueueMonitorHandler) ListTasks(w http.ResponseWriter, r *http.Request) {
	if h.inspector == nil || !h.inspector.Enabled() {
		ErrorJSON(w, r, http.StatusServiceUnavailable, "queue_monitor_unavailable", "Asynq inspector is not available")
		return
	}

	qname := chi.URLParam(r, "queueName")
	if qname == "" {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "Queue name is required")
		return
	}

	state := r.URL.Query().Get("state")
	if state == "" {
		state = "pending"
	}
	page, size := parsePageParams(r)

	var tasks *queue.TaskListSnapshot
	var err error

	switch state {
	case "pending":
		tasks, err = h.inspector.ListPendingTasks(qname, page, size)
	case "active":
		tasks, err = h.inspector.ListActiveTasks(qname, page, size)
	case "scheduled":
		tasks, err = h.inspector.ListScheduledTasks(qname, page, size)
	case "retry":
		tasks, err = h.inspector.ListRetryTasks(qname, page, size)
	case "archived":
		tasks, err = h.inspector.ListArchivedTasks(qname, page, size)
	case "completed":
		tasks, err = h.inspector.ListCompletedTasks(qname, page, size)
	default:
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "Invalid task state: "+state)
		return
	}

	if err != nil {
		handleInspectorError(w, r, err)
		return
	}

	JSON(w, http.StatusOK, tasks)
}

// GetTask returns the full details for a single task.
func (h *QueueMonitorHandler) GetTask(w http.ResponseWriter, r *http.Request) {
	if h.inspector == nil || !h.inspector.Enabled() {
		ErrorJSON(w, r, http.StatusServiceUnavailable, "queue_monitor_unavailable", "Asynq inspector is not available")
		return
	}

	qname := chi.URLParam(r, "queueName")
	taskID := chi.URLParam(r, "taskID")
	if qname == "" || taskID == "" {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "Queue name and task ID are required")
		return
	}

	task, err := h.inspector.GetTaskInfo(qname, taskID)
	if err != nil {
		handleInspectorError(w, r, err)
		return
	}

	JSON(w, http.StatusOK, task)
}

// ListServers returns all running asynq servers with their active workers.
func (h *QueueMonitorHandler) ListServers(w http.ResponseWriter, r *http.Request) {
	if h.inspector == nil || !h.inspector.Enabled() {
		ErrorJSON(w, r, http.StatusServiceUnavailable, "queue_monitor_unavailable", "Asynq inspector is not available")
		return
	}

	servers, err := h.inspector.Servers()
	if err != nil {
		handleInspectorError(w, r, err)
		return
	}

	JSON(w, http.StatusOK, servers)
}

// ListSchedulerEntries returns all registered scheduler entries.
func (h *QueueMonitorHandler) ListSchedulerEntries(w http.ResponseWriter, r *http.Request) {
	if h.inspector == nil || !h.inspector.Enabled() {
		ErrorJSON(w, r, http.StatusServiceUnavailable, "queue_monitor_unavailable", "Asynq inspector is not available")
		return
	}

	entries, err := h.inspector.SchedulerEntries()
	if err != nil {
		handleInspectorError(w, r, err)
		return
	}

	JSON(w, http.StatusOK, entries)
}

// ListSchedulerEvents returns paginated enqueue events for a scheduler entry.
func (h *QueueMonitorHandler) ListSchedulerEvents(w http.ResponseWriter, r *http.Request) {
	if h.inspector == nil || !h.inspector.Enabled() {
		ErrorJSON(w, r, http.StatusServiceUnavailable, "queue_monitor_unavailable", "Asynq inspector is not available")
		return
	}

	entryID := chi.URLParam(r, "entryID")
	if entryID == "" {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "Entry ID is required")
		return
	}

	page, size := parsePageParams(r)

	events, err := h.inspector.ListSchedulerEnqueueEvents(entryID, page, size)
	if err != nil {
		handleInspectorError(w, r, err)
		return
	}

	JSON(w, http.StatusOK, events)
}

// QueueHistory returns daily stats for the given queue over the specified number of days.
func (h *QueueMonitorHandler) QueueHistory(w http.ResponseWriter, r *http.Request) {
	if h.inspector == nil || !h.inspector.Enabled() {
		ErrorJSON(w, r, http.StatusServiceUnavailable, "queue_monitor_unavailable", "Asynq inspector is not available")
		return
	}

	qname := chi.URLParam(r, "queueName")
	if qname == "" {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "Queue name is required")
		return
	}

	days := 30
	if d := r.URL.Query().Get("days"); d != "" {
		if parsed, err := strconv.Atoi(d); err == nil && parsed > 0 {
			days = parsed
		}
	}

	stats, err := h.inspector.History(qname, days)
	if err != nil {
		handleInspectorError(w, r, err)
		return
	}

	JSON(w, http.StatusOK, stats)
}

// ---------------------------------------------------------------------------
// Write operations - all check h.readonly first
// ---------------------------------------------------------------------------

// DeleteTask deletes a single task by queue name and task ID.
func (h *QueueMonitorHandler) DeleteTask(w http.ResponseWriter, r *http.Request) {
	if h.readonly {
		ErrorJSON(w, r, http.StatusForbidden, "readonly", "Queue monitor is in read-only mode")
		return
	}
	if h.inspector == nil || !h.inspector.Enabled() {
		ErrorJSON(w, r, http.StatusServiceUnavailable, "queue_monitor_unavailable", "Asynq inspector is not available")
		return
	}

	qname := chi.URLParam(r, "queueName")
	taskID := chi.URLParam(r, "taskID")
	if qname == "" || taskID == "" {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "Queue name and task ID are required")
		return
	}

	if err := h.inspector.DeleteTask(qname, taskID); err != nil {
		handleInspectorError(w, r, err)
		return
	}

	JSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// RunTask moves a task to the pending state for immediate execution.
func (h *QueueMonitorHandler) RunTask(w http.ResponseWriter, r *http.Request) {
	if h.readonly {
		ErrorJSON(w, r, http.StatusForbidden, "readonly", "Queue monitor is in read-only mode")
		return
	}
	if h.inspector == nil || !h.inspector.Enabled() {
		ErrorJSON(w, r, http.StatusServiceUnavailable, "queue_monitor_unavailable", "Asynq inspector is not available")
		return
	}

	qname := chi.URLParam(r, "queueName")
	taskID := chi.URLParam(r, "taskID")
	if qname == "" || taskID == "" {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "Queue name and task ID are required")
		return
	}

	if err := h.inspector.RunTask(qname, taskID); err != nil {
		handleInspectorError(w, r, err)
		return
	}

	JSON(w, http.StatusOK, map[string]string{"status": "enqueued"})
}

// ArchiveTask archives a task by queue name and task ID.
func (h *QueueMonitorHandler) ArchiveTask(w http.ResponseWriter, r *http.Request) {
	if h.readonly {
		ErrorJSON(w, r, http.StatusForbidden, "readonly", "Queue monitor is in read-only mode")
		return
	}
	if h.inspector == nil || !h.inspector.Enabled() {
		ErrorJSON(w, r, http.StatusServiceUnavailable, "queue_monitor_unavailable", "Asynq inspector is not available")
		return
	}

	qname := chi.URLParam(r, "queueName")
	taskID := chi.URLParam(r, "taskID")
	if qname == "" || taskID == "" {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "Queue name and task ID are required")
		return
	}

	if err := h.inspector.ArchiveTask(qname, taskID); err != nil {
		handleInspectorError(w, r, err)
		return
	}

	JSON(w, http.StatusOK, map[string]string{"status": "archived"})
}

// DeleteAllTasks deletes all tasks in a given state for the named queue.
func (h *QueueMonitorHandler) DeleteAllTasks(w http.ResponseWriter, r *http.Request) {
	if h.readonly {
		ErrorJSON(w, r, http.StatusForbidden, "readonly", "Queue monitor is in read-only mode")
		return
	}
	if h.inspector == nil || !h.inspector.Enabled() {
		ErrorJSON(w, r, http.StatusServiceUnavailable, "queue_monitor_unavailable", "Asynq inspector is not available")
		return
	}

	qname := chi.URLParam(r, "queueName")
	if qname == "" {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "Queue name is required")
		return
	}

	state := r.URL.Query().Get("state")
	if state == "" {
		state = "pending"
	}

	if err := h.inspector.DeleteAllTasks(qname, state); err != nil {
		handleInspectorError(w, r, err)
		return
	}

	JSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// PauseQueue pauses the named queue.
func (h *QueueMonitorHandler) PauseQueue(w http.ResponseWriter, r *http.Request) {
	if h.readonly {
		ErrorJSON(w, r, http.StatusForbidden, "readonly", "Queue monitor is in read-only mode")
		return
	}
	if h.inspector == nil || !h.inspector.Enabled() {
		ErrorJSON(w, r, http.StatusServiceUnavailable, "queue_monitor_unavailable", "Asynq inspector is not available")
		return
	}

	qname := chi.URLParam(r, "queueName")
	if qname == "" {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "Queue name is required")
		return
	}

	if err := h.inspector.PauseQueue(qname); err != nil {
		handleInspectorError(w, r, err)
		return
	}

	JSON(w, http.StatusOK, map[string]string{"status": "paused"})
}

// ResumeQueue resumes (unpauses) the named queue.
func (h *QueueMonitorHandler) ResumeQueue(w http.ResponseWriter, r *http.Request) {
	if h.readonly {
		ErrorJSON(w, r, http.StatusForbidden, "readonly", "Queue monitor is in read-only mode")
		return
	}
	if h.inspector == nil || !h.inspector.Enabled() {
		ErrorJSON(w, r, http.StatusServiceUnavailable, "queue_monitor_unavailable", "Asynq inspector is not available")
		return
	}

	qname := chi.URLParam(r, "queueName")
	if qname == "" {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "Queue name is required")
		return
	}

	if err := h.inspector.UnpauseQueue(qname); err != nil {
		handleInspectorError(w, r, err)
		return
	}

	JSON(w, http.StatusOK, map[string]string{"status": "resumed"})
}

// CancelActiveTask sends a cancellation signal to the active task with the given ID.
func (h *QueueMonitorHandler) CancelActiveTask(w http.ResponseWriter, r *http.Request) {
	if h.readonly {
		ErrorJSON(w, r, http.StatusForbidden, "readonly", "Queue monitor is in read-only mode")
		return
	}
	if h.inspector == nil || !h.inspector.Enabled() {
		ErrorJSON(w, r, http.StatusServiceUnavailable, "queue_monitor_unavailable", "Asynq inspector is not available")
		return
	}

	taskID := chi.URLParam(r, "taskID")
	if taskID == "" {
		ErrorJSON(w, r, http.StatusBadRequest, "bad_request", "Task ID is required")
		return
	}

	if err := h.inspector.CancelProcessing(taskID); err != nil {
		handleInspectorError(w, r, err)
		return
	}

	JSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// parsePageParams extracts page and size from query parameters.
// Defaults: page=1, size=20.
func parsePageParams(r *http.Request) (page, size int) {
	page = 1
	size = 20
	if p := r.URL.Query().Get("page"); p != "" {
		if n, err := strconv.Atoi(p); err == nil && n > 0 {
			page = n
		}
	}
	if s := r.URL.Query().Get("size"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			size = n
		}
	}
	return
}

// handleInspectorError maps common asynq inspector errors to HTTP status codes
// and writes an appropriate error response.
func handleInspectorError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, asynq.ErrQueueNotFound) || errors.Is(err, asynq.ErrTaskNotFound) {
		ErrorJSON(w, r, http.StatusNotFound, "not_found", err.Error())
		return
	}
	qmLog.WithError(err).Error("inspector operation failed")
	ErrorJSON(w, r, http.StatusInternalServerError, "internal_error", "Internal server error")
}
