# Admin Operations API

Admin-only operational endpoints: system status / diagnostics and the asynq queue monitor. Every endpoint in this document requires a Bearer JWT with `role=admin` (see [authentication](../authentication.md)). All paths are under `/api/v1`, so the full URL is `https://your-center.example.com/api/v1/...`.

Base URL: `https://your-center.example.com`

---

# System Status

Diagnostics for the running center process: build info, database/Redis/queue health, cache state, alert counts, and request statistics; plus database table sizing and manual time-series rotation helpers.

## `GET /api/v1/admin/system-status`

Returns a full diagnostic snapshot of the running center: build info, process/uptime, database pool and migrations, WebSocket hub, Redis, asynq queue, lock backend, cache buckets, active-alert counts, and recent request statistics.

- **Auth:** Bearer JWT (admin)
- **Path parameters:** None
- **Query parameters:** None
- **Request body:** None
- **Response `200`:** A single JSON object. Top-level keys:

  | Field | Type | Description |
  |---|---|---|
  | `build` | object | Build info: `commit_hash`, `build_date`, `version`, `go_version` (all string; `go_version` is the runtime Go version). |
  | `process` | object | `started_at` (string, RFC3339), `uptime_secs` (integer), `listen_addr` (string), `external_url` (string), `center_public_key` (string, hex; empty if no key pair). |
  | `database` | object | `ping_ms` (number), `open_conns` (integer), `in_use` (integer), `idle` (integer), `max_open_conns` (integer), `wait_count` (integer), `wait_duration_ms` (integer), `migrations` (array of objects with `version` string and `applied_at` string), `migration_count` (integer). |
  | `hub` | object | WebSocket hub stats (`ws.HubStat`): `connected_agents` (integer), `pending_auth_count` (integer), `bot_connected` (boolean), `bot_count` (integer), `pending_commands` (object: node ID → pending-command count). |
  | `redis` | object | `enabled` (boolean), `available` (boolean), `last_error` (string), `key_prefix` (string), `required` (boolean), `configured` (boolean). |
  | `queue` | object | `enabled` (boolean), `available` (boolean), `backend` (string, always `"asynq"`), `concurrency` (integer), `queues` (object: parsed weighted queues), `backoff_until` (string RFC3339, or `null` when not backing off). |
  | `lock` | object | `enabled` (boolean), `available` (boolean), `backend` (string). |
  | `cache` | object | `backend` (string), `redis_available` (boolean), `redis_last_error` (string), `fallback_enabled` (boolean), `file_size_bytes` (integer), `buckets` (object: bucket name → count), `active_alerts` (array), `bucket_keys` (object: bucket name → array of key entries, for the `alerts`, `registry`, `rate_limit`, and `settings` buckets). |
  | `alerts` | object | Counts per alert type: `bgp_down`, `bgp_fail`, `handshake_stale`, `node_offline`, `latency_high`, `latency_active` (all integer). |
  | `request_log` | object | `last_5min_count` (integer), `last_hour_count` (integer), `error_rate_5xx` (number, percent), `p95_duration_ms` (number). |

  ```json
  {
    "build": {
      "commit_hash": "a1b2c3d",
      "build_date": "2026-06-01T00:00:00Z",
      "version": "1.4.0",
      "go_version": "go1.25.0"
    },
    "process": {
      "started_at": "2026-06-07T08:00:00Z",
      "uptime_secs": 3600,
      "listen_addr": ":8080",
      "external_url": "https://your-center.example.com",
      "center_public_key": "9f1c2a...e7"
    },
    "database": {
      "ping_ms": 0.842,
      "open_conns": 4,
      "in_use": 1,
      "idle": 3,
      "max_open_conns": 20,
      "wait_count": 0,
      "wait_duration_ms": 0,
      "migrations": [
        { "version": "0001_init", "applied_at": "2026-05-01T12:00:00Z" }
      ],
      "migration_count": 1
    },
    "hub": {
      "connected_agents": 2,
      "pending_auth_count": 0,
      "bot_connected": true,
      "bot_count": 1,
      "pending_commands": { "node-1": 0 }
    },
    "redis": {
      "enabled": true,
      "available": true,
      "last_error": "",
      "key_prefix": "autopeer:center:",
      "required": false,
      "configured": true
    },
    "queue": {
      "enabled": true,
      "available": true,
      "backend": "asynq",
      "concurrency": 10,
      "queues": { "critical": 6, "default": 3, "low": 1 },
      "backoff_until": null
    },
    "lock": { "enabled": true, "available": true, "backend": "redis" },
    "cache": {
      "backend": "redis",
      "redis_available": true,
      "redis_last_error": "",
      "fallback_enabled": true,
      "file_size_bytes": 65536,
      "buckets": { "alerts": 3, "registry": 120 },
      "active_alerts": [],
      "bucket_keys": { "alerts": [], "registry": [], "rate_limit": [], "settings": [] }
    },
    "alerts": {
      "bgp_down": 0,
      "bgp_fail": 0,
      "handshake_stale": 0,
      "node_offline": 0,
      "latency_high": 0,
      "latency_active": 0
    },
    "request_log": {
      "last_5min_count": 42,
      "last_hour_count": 530,
      "error_rate_5xx": 0.18,
      "p95_duration_ms": 24.5
    }
  }
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 503 | `db_unavailable` | The database ping failed. |

- **Source:** `SystemStatusHandler.Get` — `internal/handler/system_status.go`

## `GET /api/v1/admin/system-status/db-tables`

Reports on-disk size, estimated row counts, pending-rotation counts, and (for `peer_metrics`) TimescaleDB compression stats for the request-log and time-series tables.

- **Auth:** Bearer JWT (admin)
- **Path parameters:** None
- **Query parameters:**

  | Name | Type | Required | Description |
  |---|---|---|---|
  | `days` | integer | No | Retention horizon used to compute pending-rotation counts. Defaults to `30`. Only applied when between `1` and `3650` (inclusive); otherwise the default is used. |

- **Request body:** None
- **Response `200`:**

  | Field | Type | Description |
  |---|---|---|
  | `days` | integer | The effective horizon used for pending-rotation counts. |
  | `request_logs` | object | `total_size_bytes` (integer), `row_estimate` (integer), `pending_count` (integer — rows older than `days`). |
  | `peer_metrics` | object | `total_size_bytes` (integer), `row_estimate` (integer), `pending_count` (integer — chunks older than `days`), `total_chunks` (integer), `number_compressed_chunks` (integer), `before_compression_bytes` (integer), `after_compression_bytes` (integer). |
  | `node_metrics` | object | `total_size_bytes` (integer), `row_estimate` (integer), `pending_count` (integer — chunks older than `days`). |

  ```json
  {
    "days": 30,
    "request_logs": {
      "total_size_bytes": 10485760,
      "row_estimate": 250000,
      "pending_count": 12000
    },
    "peer_metrics": {
      "total_size_bytes": 52428800,
      "row_estimate": 1800000,
      "pending_count": 4,
      "total_chunks": 60,
      "number_compressed_chunks": 50,
      "before_compression_bytes": 40000000,
      "after_compression_bytes": 8000000
    },
    "node_metrics": {
      "total_size_bytes": 2097152,
      "row_estimate": 90000,
      "pending_count": 2
    }
  }
  ```

- **Errors:** None (individual query failures are logged and surfaced as zeroed fields; the handler always returns `200`).
- **Source:** `SystemStatusHandler.GetDBTables` — `internal/handler/system_status.go`

## `POST /api/v1/admin/system-status/rotate-tables`

Manually rotates (prunes) the named tables: deletes `request_logs` rows older than `days`, and drops TimescaleDB chunks older than `days` for `peer_metrics` / `node_metrics`.

- **Auth:** Bearer JWT (admin)
- **Path parameters:** None
- **Query parameters:** None
- **Request body:**

  | Field | Type | Required | Description |
  |---|---|---|---|
  | `tables` | array of string | Yes | Tables to rotate. Each value must be one of `request_logs`, `peer_metrics`, `node_metrics`. Must be non-empty. |
  | `days` | integer | Yes | Retention horizon. Rows/chunks older than this many days are removed. Must be between `1` and `3650`. |

  ```json
  {
    "tables": ["request_logs", "peer_metrics"],
    "days": 30
  }
  ```

- **Response `200`:**

  | Field | Type | Description |
  |---|---|---|
  | `results` | object | Map keyed by table name. For `request_logs`: `{ "deleted_count": <integer> }`. For `peer_metrics` / `node_metrics`: `{ "dropped_chunks": <integer> }`. If a table's rotation errored, the value is `{ "error": "<message>" }` instead. |

  ```json
  {
    "results": {
      "request_logs": { "deleted_count": 12000 },
      "peer_metrics": { "dropped_chunks": 4 }
    }
  }
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 400 | `invalid_request` | The JSON body could not be decoded. |
  | 400 | `no_tables` | `tables` is empty. |
  | 400 | `invalid_days` | `days` is below 1 or above 3650. |
  | 400 | `invalid_table` | A value in `tables` is not one of the allowed tables. |

- **Source:** `SystemStatusHandler.RotateTables` — `internal/handler/system_status.go`

---

# Queue Monitor

Read and (optionally) manage the asynq job queues backing the center. All routes below are mounted under `/api/v1/admin/queue`, so e.g. `GET /overview` is the full path `GET /api/v1/admin/queue/overview`.

Every queue-monitor endpoint requires the asynq inspector to be available; when it is not, the handler returns `503 queue_monitor_unavailable`. Inspector lookups for an unknown queue or task return `404 not_found`; other inspector failures return `500 internal_error`. These three errors are common to most endpoints and are repeated in each error table for completeness.

> **Read-only gate:** The mutating endpoints (the final group below) are **only registered when `ASYNQ_READONLY_MONITOR=false`**. When the monitor is read-only, those routes are not mounted at all and requests fall through to the standard router responses (`404 not_found` / `405 method_not_allowed`). The handlers also defensively re-check the flag and return `403 readonly` if reached. See [configuration](../configuration.md).

## `GET /api/v1/admin/queue/overview`

Returns a combined overview: queue enabled/available status, a snapshot of every queue, running servers, and scheduler entries.

- **Auth:** Bearer JWT (admin)
- **Path parameters:** None
- **Query parameters:** None
- **Request body:** None
- **Response `200`:**

  | Field | Type | Description |
  |---|---|---|
  | `enabled` | boolean | Whether the queue subsystem is enabled. |
  | `available` | boolean | Whether the queue subsystem is currently reachable. |
  | `queues` | array of object | One snapshot per known queue (`queue.QueueSnapshot`; see [`GET /queues`](#get-apiv1adminqueuequeues) for fields). |
  | `servers` | array of object | Running asynq servers (`queue.ServerSnapshot`); key omitted entirely if the listing failed. |
  | `scheduler_entries` | array of object | Registered scheduler entries (`queue.SchedulerEntrySnapshot`); key omitted entirely if the listing failed. |

  ```json
  {
    "enabled": true,
    "available": true,
    "queues": [
      {
        "name": "default",
        "paused": false,
        "size": 3,
        "pending": 2,
        "active": 1,
        "scheduled": 0,
        "retry": 0,
        "archived": 0,
        "completed": 0,
        "processed": 1200,
        "failed": 3,
        "processed_total": 50000,
        "failed_total": 120,
        "latency_ms": 12,
        "memory_usage_bytes": 8192
      }
    ],
    "servers": [
      {
        "id": "center-1:1234:abcd",
        "host": "center-1",
        "pid": 1234,
        "concurrency": 10,
        "queues": { "critical": 6, "default": 3, "low": 1 },
        "strict_priority": false,
        "started_at": "2026-06-07T08:00:00Z",
        "active_workers": [],
        "status": "active"
      }
    ],
    "scheduler_entries": [
      {
        "id": "abc123",
        "spec": "@every 1m",
        "task_type": "metrics:rollup",
        "task_payload": "",
        "next": "2026-06-07T09:01:00Z",
        "prev": "2026-06-07T09:00:00Z"
      }
    ]
  }
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 503 | `queue_monitor_unavailable` | The asynq inspector is not available. |
  | 404 | `not_found` | Inspector reported queue/task not found while listing queues. |
  | 500 | `internal_error` | Any other inspector failure while listing queues. |

- **Source:** `QueueMonitorHandler.Overview` — `internal/handler/queue_monitor.go`

## `GET /api/v1/admin/queue/queues`

Returns a snapshot for every known queue as a bare array.

- **Auth:** Bearer JWT (admin)
- **Path parameters:** None
- **Query parameters:** None
- **Request body:** None
- **Response `200`:** A bare JSON array of queue snapshots (`queue.QueueSnapshot`). Each element has these fields:

  | Field | Type | Description |
  |---|---|---|
  | `name` | string | Queue name. |
  | `paused` | boolean | Whether the queue is paused. |
  | `size` | integer | Total tasks in the queue. |
  | `pending` | integer | Pending task count. |
  | `active` | integer | Active task count. |
  | `scheduled` | integer | Scheduled task count. |
  | `retry` | integer | Retry task count. |
  | `archived` | integer | Archived task count. |
  | `completed` | integer | Completed task count. |
  | `processed` | integer | Tasks processed today. |
  | `failed` | integer | Tasks failed today. |
  | `processed_total` | integer | All-time processed count. |
  | `failed_total` | integer | All-time failed count. |
  | `latency_ms` | integer | Oldest-pending-task latency in milliseconds. |
  | `memory_usage_bytes` | integer | Approximate memory used by the queue. |

  ```json
  [
    {
      "name": "critical",
      "paused": false,
      "size": 0,
      "pending": 0,
      "active": 0,
      "scheduled": 0,
      "retry": 0,
      "archived": 0,
      "completed": 0,
      "processed": 0,
      "failed": 0,
      "processed_total": 0,
      "failed_total": 0,
      "latency_ms": 0,
      "memory_usage_bytes": 0
    },
    {
      "name": "default",
      "paused": false,
      "size": 3,
      "pending": 2,
      "active": 1,
      "scheduled": 0,
      "retry": 0,
      "archived": 0,
      "completed": 0,
      "processed": 1200,
      "failed": 3,
      "processed_total": 50000,
      "failed_total": 120,
      "latency_ms": 12,
      "memory_usage_bytes": 8192
    }
  ]
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 503 | `queue_monitor_unavailable` | The asynq inspector is not available. |
  | 404 | `not_found` | Inspector reported queue/task not found while listing queues. |
  | 500 | `internal_error` | Any other inspector failure while listing queues. |

- **Source:** `QueueMonitorHandler.ListQueues` — `internal/handler/queue_monitor.go`

## `GET /api/v1/admin/queue/queues/{queueName}`

Returns the snapshot for a single queue plus its daily history.

- **Auth:** Bearer JWT (admin)
- **Path parameters:**

  | Name | Type | Description |
  |---|---|---|
  | `queueName` | string | The queue name. |

- **Query parameters:**

  | Name | Type | Required | Description |
  |---|---|---|---|
  | `days` | integer | No | Number of days of daily history to return. Defaults to `30`; only applied when greater than `0`. |

- **Request body:** None
- **Response `200`:**

  | Field | Type | Description |
  |---|---|---|
  | `queue` | object | The queue snapshot (`queue.QueueSnapshot`; same fields as [`GET /queues`](#get-apiv1adminqueuequeues)). |
  | `history` | array of object \| null | Daily history stats (`queue.DailyStatsSnapshot`: `queue`, `processed`, `failed`, `date`); `null` if the history lookup failed. |

  ```json
  {
    "queue": {
      "name": "default",
      "paused": false,
      "size": 3,
      "pending": 2,
      "active": 1,
      "scheduled": 0,
      "retry": 0,
      "archived": 0,
      "completed": 0,
      "processed": 1200,
      "failed": 3,
      "processed_total": 50000,
      "failed_total": 120,
      "latency_ms": 12,
      "memory_usage_bytes": 8192
    },
    "history": [
      { "queue": "default", "processed": 1200, "failed": 3, "date": "2026-06-06" }
    ]
  }
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 503 | `queue_monitor_unavailable` | The asynq inspector is not available. |
  | 400 | `bad_request` | `queueName` path segment is empty. |
  | 404 | `not_found` | The queue does not exist. |
  | 500 | `internal_error` | Any other inspector failure fetching the queue. |

- **Source:** `QueueMonitorHandler.GetQueue` — `internal/handler/queue_monitor.go`

## `GET /api/v1/admin/queue/queues/{queueName}/tasks`

Returns a paginated list of tasks in a given state for the named queue.

- **Auth:** Bearer JWT (admin)
- **Path parameters:**

  | Name | Type | Description |
  |---|---|---|
  | `queueName` | string | The queue name. |

- **Query parameters:**

  | Name | Type | Required | Description |
  |---|---|---|---|
  | `state` | string | No | Task state to list. One of `pending`, `active`, `scheduled`, `retry`, `archived`, `completed`. Defaults to `pending`. |
  | `page` | integer | No | 1-based page number. Defaults to `1`; only applied when greater than `0`. |
  | `size` | integer | No | Page size. Defaults to `20`; only applied when greater than `0`. |

- **Request body:** None
- **Response `200`:** A task list snapshot (`queue.TaskListSnapshot`):

  | Field | Type | Description |
  |---|---|---|
  | `tasks` | array of object | The task snapshots (`queue.TaskSnapshot`). |
  | `total_count` | integer | Total tasks in this state. |
  | `page` | integer | The page number returned. |
  | `size` | integer | The page size used. |

  Each `tasks[]` entry (`queue.TaskSnapshot`) has: `id` (string), `type` (string), `payload` (string, truncated), `state` (string), `queue` (string), `retry_count` (integer), `max_retry` (integer), `timeout_seconds` (integer), and optional (omitted when empty) `retried_at`, `last_failed_at`, `last_error`, `next_process_at`, `completed_at`, `result`, `deadline`, `started_at`, `worker_id` (all string).

  ```json
  {
    "tasks": [
      {
        "id": "task_abc123",
        "type": "metrics:rollup",
        "payload": "{}",
        "state": "pending",
        "queue": "default",
        "retry_count": 0,
        "max_retry": 25,
        "timeout_seconds": 0
      }
    ],
    "total_count": 2,
    "page": 1,
    "size": 20
  }
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 503 | `queue_monitor_unavailable` | The asynq inspector is not available. |
  | 400 | `bad_request` | `queueName` is empty, or `state` is not one of the supported states. |
  | 404 | `not_found` | The queue does not exist. |
  | 500 | `internal_error` | Any other inspector failure listing tasks. |

- **Source:** `QueueMonitorHandler.ListTasks` — `internal/handler/queue_monitor.go`

## `GET /api/v1/admin/queue/queues/{queueName}/tasks/{taskID}`

Returns the full detail for a single task.

- **Auth:** Bearer JWT (admin)
- **Path parameters:**

  | Name | Type | Description |
  |---|---|---|
  | `queueName` | string | The queue name. |
  | `taskID` | string | The task ID. |

- **Query parameters:** None
- **Request body:** None
- **Response `200`:** A single task snapshot (`queue.TaskSnapshot`; same fields as the `tasks[]` entries documented under [`GET .../tasks`](#get-apiv1adminqueuequeuesqueuenametasks)).

  ```json
  {
    "id": "task_abc123",
    "type": "metrics:rollup",
    "payload": "{}",
    "state": "pending",
    "queue": "default",
    "retry_count": 0,
    "max_retry": 25,
    "timeout_seconds": 0
  }
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 503 | `queue_monitor_unavailable` | The asynq inspector is not available. |
  | 400 | `bad_request` | `queueName` or `taskID` is empty. |
  | 404 | `not_found` | The queue or task does not exist. |
  | 500 | `internal_error` | Any other inspector failure fetching the task. |

- **Source:** `QueueMonitorHandler.GetTask` — `internal/handler/queue_monitor.go`

## `GET /api/v1/admin/queue/servers`

Lists all running asynq servers and their active workers.

- **Auth:** Bearer JWT (admin)
- **Path parameters:** None
- **Query parameters:** None
- **Request body:** None
- **Response `200`:** A bare JSON array of server snapshots (`queue.ServerSnapshot`). Each element has: `id` (string), `host` (string), `pid` (integer), `concurrency` (integer), `queues` (object: queue name → weight), `strict_priority` (boolean), `started_at` (string), `active_workers` (array of `queue.WorkerSnapshot`: `task_id`, `task_type`, `task_payload`, `queue`, `started_at`, `deadline`), `status` (string).

  ```json
  [
    {
      "id": "center-1:1234:abcd",
      "host": "center-1",
      "pid": 1234,
      "concurrency": 10,
      "queues": { "critical": 6, "default": 3, "low": 1 },
      "strict_priority": false,
      "started_at": "2026-06-07T08:00:00Z",
      "active_workers": [],
      "status": "active"
    }
  ]
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 503 | `queue_monitor_unavailable` | The asynq inspector is not available. |
  | 404 | `not_found` | Inspector reported not found while listing servers. |
  | 500 | `internal_error` | Any other inspector failure listing servers. |

- **Source:** `QueueMonitorHandler.ListServers` — `internal/handler/queue_monitor.go`

## `GET /api/v1/admin/queue/scheduler`

Lists all registered scheduler entries.

- **Auth:** Bearer JWT (admin)
- **Path parameters:** None
- **Query parameters:** None
- **Request body:** None
- **Response `200`:** A bare JSON array of scheduler entry snapshots (`queue.SchedulerEntrySnapshot`). Each element has: `id` (string), `spec` (string, cron/`@every` spec), `task_type` (string), `task_payload` (string), `next` (string), `prev` (string).

  ```json
  [
    {
      "id": "abc123",
      "spec": "@every 1m",
      "task_type": "metrics:rollup",
      "task_payload": "",
      "next": "2026-06-07T09:01:00Z",
      "prev": "2026-06-07T09:00:00Z"
    }
  ]
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 503 | `queue_monitor_unavailable` | The asynq inspector is not available. |
  | 404 | `not_found` | Inspector reported not found while listing scheduler entries. |
  | 500 | `internal_error` | Any other inspector failure listing scheduler entries. |

- **Source:** `QueueMonitorHandler.ListSchedulerEntries` — `internal/handler/queue_monitor.go`

## `GET /api/v1/admin/queue/scheduler/{entryID}/events`

Returns paginated enqueue events for a single scheduler entry.

- **Auth:** Bearer JWT (admin)
- **Path parameters:**

  | Name | Type | Description |
  |---|---|---|
  | `entryID` | string | The scheduler entry ID. |

- **Query parameters:**

  | Name | Type | Required | Description |
  |---|---|---|---|
  | `page` | integer | No | 1-based page number. Defaults to `1`; only applied when greater than `0`. |
  | `size` | integer | No | Page size. Defaults to `20`; only applied when greater than `0`. |

- **Request body:** None
- **Response `200`:** A bare JSON array of enqueue-event snapshots (`queue.EnqueueEventSnapshot`): `task_id` (string), `enqueued_at` (string).

  ```json
  [
    { "task_id": "task_abc123", "enqueued_at": "2026-06-07T09:00:00Z" }
  ]
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 503 | `queue_monitor_unavailable` | The asynq inspector is not available. |
  | 400 | `bad_request` | `entryID` is empty. |
  | 404 | `not_found` | The scheduler entry does not exist. |
  | 500 | `internal_error` | Any other inspector failure listing events. |

- **Source:** `QueueMonitorHandler.ListSchedulerEvents` — `internal/handler/queue_monitor.go`

## `GET /api/v1/admin/queue/history/{queueName}`

Returns daily stats for the named queue over the requested number of days.

- **Auth:** Bearer JWT (admin)
- **Path parameters:**

  | Name | Type | Description |
  |---|---|---|
  | `queueName` | string | The queue name. |

- **Query parameters:**

  | Name | Type | Required | Description |
  |---|---|---|---|
  | `days` | integer | No | Number of days of history. Defaults to `30`; only applied when greater than `0`. |

- **Request body:** None
- **Response `200`:** A bare JSON array of daily-stats snapshots (`queue.DailyStatsSnapshot`): `queue` (string), `processed` (integer), `failed` (integer), `date` (string).

  ```json
  [
    { "queue": "default", "processed": 1200, "failed": 3, "date": "2026-06-06" },
    { "queue": "default", "processed": 980, "failed": 0, "date": "2026-06-07" }
  ]
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 503 | `queue_monitor_unavailable` | The asynq inspector is not available. |
  | 400 | `bad_request` | `queueName` is empty. |
  | 404 | `not_found` | The queue does not exist. |
  | 500 | `internal_error` | Any other inspector failure fetching history. |

- **Source:** `QueueMonitorHandler.QueueHistory` — `internal/handler/queue_monitor.go`

## Mutating endpoints (registered only when `ASYNQ_READONLY_MONITOR=false`)

The following routes are mounted **only** when `ASYNQ_READONLY_MONITOR=false`. When the monitor is read-only, the routes are not registered (requests get the router's `404 not_found` / `405 method_not_allowed`); each handler also re-checks the flag and returns `403 readonly` if invoked. See [configuration](../configuration.md).

### `DELETE /api/v1/admin/queue/queues/{queueName}/tasks/{taskID}`

Deletes a single task by queue name and task ID.

- **Auth:** Bearer JWT (admin)
- **Path parameters:**

  | Name | Type | Description |
  |---|---|---|
  | `queueName` | string | The queue name. |
  | `taskID` | string | The task ID. |

- **Query parameters:** None
- **Request body:** None
- **Response `200`:**

  | Field | Type | Description |
  |---|---|---|
  | `status` | string | Always `"deleted"`. |

  ```json
  { "status": "deleted" }
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 403 | `readonly` | Queue monitor is in read-only mode. |
  | 503 | `queue_monitor_unavailable` | The asynq inspector is not available. |
  | 400 | `bad_request` | `queueName` or `taskID` is empty. |
  | 404 | `not_found` | The queue or task does not exist. |
  | 500 | `internal_error` | Any other inspector failure. |

- **Source:** `QueueMonitorHandler.DeleteTask` — `internal/handler/queue_monitor.go`

### `POST /api/v1/admin/queue/queues/{queueName}/tasks/{taskID}:run`

Moves a task to the pending state for immediate execution.

- **Auth:** Bearer JWT (admin)
- **Path parameters:**

  | Name | Type | Description |
  |---|---|---|
  | `queueName` | string | The queue name. |
  | `taskID` | string | The task ID. |

- **Query parameters:** None
- **Request body:** None
- **Response `200`:**

  | Field | Type | Description |
  |---|---|---|
  | `status` | string | Always `"enqueued"`. |

  ```json
  { "status": "enqueued" }
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 403 | `readonly` | Queue monitor is in read-only mode. |
  | 503 | `queue_monitor_unavailable` | The asynq inspector is not available. |
  | 400 | `bad_request` | `queueName` or `taskID` is empty. |
  | 404 | `not_found` | The queue or task does not exist. |
  | 500 | `internal_error` | Any other inspector failure. |

- **Source:** `QueueMonitorHandler.RunTask` — `internal/handler/queue_monitor.go`

### `POST /api/v1/admin/queue/queues/{queueName}/tasks/{taskID}:archive`

Archives a task by queue name and task ID.

- **Auth:** Bearer JWT (admin)
- **Path parameters:**

  | Name | Type | Description |
  |---|---|---|
  | `queueName` | string | The queue name. |
  | `taskID` | string | The task ID. |

- **Query parameters:** None
- **Request body:** None
- **Response `200`:**

  | Field | Type | Description |
  |---|---|---|
  | `status` | string | Always `"archived"`. |

  ```json
  { "status": "archived" }
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 403 | `readonly` | Queue monitor is in read-only mode. |
  | 503 | `queue_monitor_unavailable` | The asynq inspector is not available. |
  | 400 | `bad_request` | `queueName` or `taskID` is empty. |
  | 404 | `not_found` | The queue or task does not exist. |
  | 500 | `internal_error` | Any other inspector failure. |

- **Source:** `QueueMonitorHandler.ArchiveTask` — `internal/handler/queue_monitor.go`

### `DELETE /api/v1/admin/queue/queues/{queueName}/tasks:delete_all`

Deletes all tasks in a given state for the named queue.

- **Auth:** Bearer JWT (admin)
- **Path parameters:**

  | Name | Type | Description |
  |---|---|---|
  | `queueName` | string | The queue name. |

- **Query parameters:**

  | Name | Type | Required | Description |
  |---|---|---|---|
  | `state` | string | No | Task state whose tasks are deleted. Defaults to `pending`. |

- **Request body:** None
- **Response `200`:**

  | Field | Type | Description |
  |---|---|---|
  | `status` | string | Always `"deleted"`. |

  ```json
  { "status": "deleted" }
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 403 | `readonly` | Queue monitor is in read-only mode. |
  | 503 | `queue_monitor_unavailable` | The asynq inspector is not available. |
  | 400 | `bad_request` | `queueName` is empty. |
  | 404 | `not_found` | The queue does not exist. |
  | 500 | `internal_error` | Any other inspector failure. |

- **Source:** `QueueMonitorHandler.DeleteAllTasks` — `internal/handler/queue_monitor.go`

### `POST /api/v1/admin/queue/queues/{queueName}:pause`

Pauses the named queue.

- **Auth:** Bearer JWT (admin)
- **Path parameters:**

  | Name | Type | Description |
  |---|---|---|
  | `queueName` | string | The queue name. |

- **Query parameters:** None
- **Request body:** None
- **Response `200`:**

  | Field | Type | Description |
  |---|---|---|
  | `status` | string | Always `"paused"`. |

  ```json
  { "status": "paused" }
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 403 | `readonly` | Queue monitor is in read-only mode. |
  | 503 | `queue_monitor_unavailable` | The asynq inspector is not available. |
  | 400 | `bad_request` | `queueName` is empty. |
  | 404 | `not_found` | The queue does not exist. |
  | 500 | `internal_error` | Any other inspector failure. |

- **Source:** `QueueMonitorHandler.PauseQueue` — `internal/handler/queue_monitor.go`

### `POST /api/v1/admin/queue/queues/{queueName}:resume`

Resumes (unpauses) the named queue.

- **Auth:** Bearer JWT (admin)
- **Path parameters:**

  | Name | Type | Description |
  |---|---|---|
  | `queueName` | string | The queue name. |

- **Query parameters:** None
- **Request body:** None
- **Response `200`:**

  | Field | Type | Description |
  |---|---|---|
  | `status` | string | Always `"resumed"`. |

  ```json
  { "status": "resumed" }
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 403 | `readonly` | Queue monitor is in read-only mode. |
  | 503 | `queue_monitor_unavailable` | The asynq inspector is not available. |
  | 400 | `bad_request` | `queueName` is empty. |
  | 404 | `not_found` | The queue does not exist. |
  | 500 | `internal_error` | Any other inspector failure. |

- **Source:** `QueueMonitorHandler.ResumeQueue` — `internal/handler/queue_monitor.go`

### `POST /api/v1/admin/queue/active-tasks/{taskID}:cancel`

Sends a cancellation signal to the active task with the given ID.

- **Auth:** Bearer JWT (admin)
- **Path parameters:**

  | Name | Type | Description |
  |---|---|---|
  | `taskID` | string | The active task ID. |

- **Query parameters:** None
- **Request body:** None
- **Response `200`:**

  | Field | Type | Description |
  |---|---|---|
  | `status` | string | Always `"cancelled"`. |

  ```json
  { "status": "cancelled" }
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 403 | `readonly` | Queue monitor is in read-only mode. |
  | 503 | `queue_monitor_unavailable` | The asynq inspector is not available. |
  | 400 | `bad_request` | `taskID` is empty. |
  | 404 | `not_found` | The task does not exist. |
  | 500 | `internal_error` | Any other inspector failure. |

- **Source:** `QueueMonitorHandler.CancelActiveTask` — `internal/handler/queue_monitor.go`
