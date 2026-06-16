# Admin API

Admin-only endpoints for site/notification settings, audit logs, platform statistics, test email, and Telegram-bot management. Every endpoint in this document requires a **Bearer JWT (admin)** access token (`Authorization: Bearer <access_token>`, role `admin`). See [../authentication.md](../authentication.md) for how to obtain one.

Base URL: `https://your-center.example.com`

All routes are mounted under the `/api/v1` group, which also runs the API-version middleware (echoes the `Autopeer-Version` header; `400 invalid_api_version` for an unknown value). See [./versioning.md](./versioning.md).

---

# Notification settings

## `GET /api/v1/admin/notifications`

Returns all configured notification settings (alert toggles and their thresholds).

- **Auth:** Bearer JWT (admin)
- **Path parameters:** None.
- **Query parameters:** None.
- **Request body:** None.
- **Response `200`:** An object with a `settings` array. Each element:

  | Field | Type | Description |
  |---|---|---|
  | `key` | string | Setting key. |
  | `value` | string | Current value (e.g. `"true"`/`"false"`). |
  | `threshold_value` | string | Threshold associated with the setting. |
  | `description` | string | Human-readable description. |

  ```json
  {
    "settings": [
      {
        "key": "alert_node_offline",
        "value": "true",
        "threshold_value": "300",
        "description": "Notify when a node goes offline"
      }
    ]
  }
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 500 | `internal_error` | Failed to fetch settings. |

- **Source:** `AdminHandler.GetNotificationSettings` — `internal/handler/admin_audit.go`

## `PUT /api/v1/admin/notifications`

Updates the value and/or threshold of a single notification setting.

- **Auth:** Bearer JWT (admin)
- **Path parameters:** None.
- **Query parameters:** None.
- **Request body:** The decoded struct is:

  ```go
  struct {
      Key            string  `json:"key"`
      Value          *string `json:"value"`
      ThresholdValue *string `json:"threshold_value"`
  }
  ```

  | Field | Type | Required | Description |
  |---|---|---|---|
  | `key` | string | Yes | The setting key to update. |
  | `value` | string (nullable) | No | New value. Omit/`null` to leave unchanged (treated as `""` when updating). |
  | `threshold_value` | string (nullable) | No | New threshold. Omit/`null` to leave unchanged (treated as `""` when updating). |

  At least one of `value` or `threshold_value` must be present (non-`null`).

  ```json
  {
    "key": "alert_node_offline",
    "value": "false",
    "threshold_value": "600"
  }
  ```

- **Response `200`:**

  | Field | Type | Description |
  |---|---|---|
  | `status` | string | Always `"updated"`. |

  ```json
  { "status": "updated" }
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 400 | `bad_request` | Invalid JSON body or `key` is empty (`key is required`). |
  | 400 | `bad_request` | Both `value` and `threshold_value` are `null`/absent (`nothing to update`). |
  | 500 | `internal_error` | Failed to update setting (`Failed to update setting`). |

- **Source:** `AdminHandler.UpdateNotificationSetting` — `internal/handler/admin_audit.go`

---

# Audit logs

## `GET /api/v1/admin/audit`

Returns a paginated list of admin audit-log entries, optionally filtered by action and operator.

- **Auth:** Bearer JWT (admin)
- **Path parameters:** None.
- **Query parameters:**

  | Name | Type | Required | Description |
  |---|---|---|---|
  | `action` | string | No | Filter by action name (e.g. `notification.update`). |
  | `operator` | string | No | Filter by operator (e.g. the admin email). |
  | `page` | integer | No | Page number; only applied when parseable and `> 0`. Default `1`. |
  | `per_page` | integer | No | Items per page; only applied when parseable, `> 0`, and `<= 200`. Default `25`. |

- **Request body:** None.
- **Response `200`:** A paginated wrapper. Each `logs` element:

  | Field | Type | Description |
  |---|---|---|
  | `id` | string | Audit-log entry ID. |
  | `action` | string | Action name. |
  | `operator` | string | Who performed the action. |
  | `target_id` | string | Affected target ID; **omitted** when absent (`*string` with `omitempty`). |
  | `detail` | object | Arbitrary action detail; **omitted** when absent (`omitempty`). |
  | `created_at` | string | RFC 3339 UTC timestamp. |

  Wrapper fields: `logs` (array), `total` (integer), `page` (integer), `per_page` (integer).

  ```json
  {
    "logs": [
      {
        "id": "a1b2c3d4",
        "action": "notification.update",
        "operator": "admin@example.com",
        "detail": { "key": "alert_node_offline", "value": "false" },
        "created_at": "2026-06-07T12:00:00Z"
      }
    ],
    "total": 1,
    "page": 1,
    "per_page": 25
  }
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 500 | `internal_error` | Failed to fetch audit logs. |

- **Source:** `AdminHandler.ListAuditLogs` — `internal/handler/admin_audit.go`

---

# Site settings

## `GET /api/v1/admin/settings`

Returns all site-wide settings.

- **Auth:** Bearer JWT (admin)
- **Path parameters:** None.
- **Query parameters:** None.
- **Request body:** None.
- **Response `200`:** An object with a `settings` array. Each element:

  | Field | Type | Description |
  |---|---|---|
  | `key` | string | Setting key. |
  | `value` | string | Current value. |
  | `description` | string | Human-readable description. |

  ```json
  {
    "settings": [
      {
        "key": "site_title",
        "value": "Autopeer",
        "description": "Title shown in the UI"
      }
    ]
  }
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 500 | `internal_error` | Failed to fetch site settings. |

- **Source:** `AdminHandler.GetSiteSettings` — `internal/handler/admin_audit.go`

## `PUT /api/v1/admin/settings`

Updates a single site setting.

- **Auth:** Bearer JWT (admin)
- **Path parameters:** None.
- **Query parameters:** None.
- **Request body:** The decoded struct is:

  ```go
  struct {
      Key   string  `json:"key"`
      Value *string `json:"value"`
  }
  ```

  | Field | Type | Required | Description |
  |---|---|---|---|
  | `key` | string | Yes | The setting key to update. |
  | `value` | string (nullable) | Yes | The new value. Must be present (non-`null`). |

  ```json
  {
    "key": "site_title",
    "value": "My DN42 Peering"
  }
  ```

- **Response `200`:**

  | Field | Type | Description |
  |---|---|---|
  | `status` | string | Always `"updated"`. |

  ```json
  { "status": "updated" }
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 400 | `bad_request` | Invalid JSON body, `key` empty, or `value` is `null`/absent (`key and value are required`). |
  | 500 | `internal_error` | Failed to update site setting. |

- **Source:** `AdminHandler.UpdateSiteSetting` — `internal/handler/admin_audit.go`

---

# Statistics

## `GET /api/v1/admin/stats`

Returns aggregate platform statistics for the admin dashboard.

- **Auth:** Bearer JWT (admin)
- **Path parameters:** None.
- **Query parameters:** None.
- **Request body:** None.
- **Response `200`:**

  | Field | Type | Description |
  |---|---|---|
  | `peers_active` | integer | Number of active peers. |
  | `peers_pending` | integer | Number of pending peers. |
  | `peers_suspended` | integer | Number of suspended peers. |
  | `peers_rejected` | integer | Number of rejected peers. |
  | `nodes_online` | integer | Number of online nodes. |
  | `nodes_total` | integer | Total number of nodes. |
  | `new_today` | integer | Peers created in the last 24h. |
  | `traffic_analytics_enabled` | boolean | Whether the optional ClickHouse-backed traffic-analytics feature is configured (`CLICKHOUSE_URL` set). Frontends use this to decide whether to render the traffic panels and call the `.../traffic` endpoints. |

  ```json
  {
    "peers_active": 120,
    "peers_pending": 4,
    "peers_suspended": 2,
    "peers_rejected": 7,
    "nodes_online": 8,
    "nodes_total": 9,
    "new_today": 3,
    "traffic_analytics_enabled": false
  }
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 500 | `internal_error` | Failed to fetch admin stats. |

- **Source:** `StatsHandler.Admin` — `internal/handler/stats.go`

---

# Test email

## `POST /api/v1/admin/test/email`

Sends a test email to verify outbound email delivery.

- **Auth:** Bearer JWT (admin)
- **Path parameters:** None.
- **Query parameters:** None.
- **Request body:** The decoded struct is:

  ```go
  struct {
      To      string `json:"to"`
      Message string `json:"message"`
  }
  ```

  | Field | Type | Required | Description |
  |---|---|---|---|
  | `to` | string | Yes | Recipient email address. |
  | `message` | string | No | Custom body text. Defaults to `"This is a test email."` when empty. |

  ```json
  {
    "to": "admin@example.com",
    "message": "Hello from the test endpoint"
  }
  ```

- **Response `200`:**

  | Field | Type | Description |
  |---|---|---|
  | `status` | string | Always `"sent"`. |
  | `message` | string | Confirmation text including the recipient. |

  ```json
  {
    "status": "sent",
    "message": "Test email sent to admin@example.com"
  }
  ```

  Note: the handler returns `200` even if the underlying send fails (the error is logged, not surfaced).

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 400 | `bad_request` | Invalid JSON body (`Invalid JSON body`). |
  | 400 | `bad_request` | `to` is empty (`Missing required field: to`). |

- **Source:** `AdminHandler.TestEmail` — `internal/handler/admin.go`

---

# Telegram-bot management

## `GET /api/v1/admin/bot/settings`

Returns all Telegram-bot settings.

- **Auth:** Bearer JWT (admin)
- **Path parameters:** None.
- **Query parameters:** None.
- **Request body:** None.
- **Response `200`:** An object with a `settings` array. Each element:

  | Field | Type | Description |
  |---|---|---|
  | `key` | string | Setting key. |
  | `value` | string | Current value. |
  | `description` | string | Human-readable description. |

  ```json
  {
    "settings": [
      {
        "key": "bot_enabled",
        "value": "true",
        "description": "Whether the Telegram bot is active"
      }
    ]
  }
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 500 | `internal_error` | Failed to fetch bot settings. |

- **Source:** `AdminHandler.GetBotSettings` — `internal/handler/admin_bot.go`

## `PUT /api/v1/admin/bot/settings`

Updates a single bot setting (the key must already exist) and pushes the new configuration to connected bots.

- **Auth:** Bearer JWT (admin)
- **Path parameters:** None.
- **Query parameters:** None.
- **Request body:** The decoded struct is:

  ```go
  struct {
      Key   string  `json:"key"`
      Value *string `json:"value"`
  }
  ```

  | Field | Type | Required | Description |
  |---|---|---|---|
  | `key` | string | Yes | The setting key to update (must already exist). |
  | `value` | string (nullable) | Yes | New value. Must be present (non-`null`). |

  ```json
  {
    "key": "bot_enabled",
    "value": "false"
  }
  ```

  When `key` is `bot_auth_token`, the value is recorded as `[REDACTED]` in the audit log.

- **Response `200`:**

  | Field | Type | Description |
  |---|---|---|
  | `status` | string | Always `"updated"`. |

  ```json
  { "status": "updated" }
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 400 | `bad_request` | Invalid JSON body, `key` empty, or `value` is `null`/absent (`key and value are required`). |
  | 404 | `not_found` | The setting key does not exist (`Setting key not found`). |
  | 500 | `internal_error` | Failed to update bot setting. |

- **Source:** `AdminHandler.UpdateBotSetting` — `internal/handler/admin_bot.go`

## `POST /api/v1/admin/bot/token/reset`

Generates a new bot auth token, stores its bcrypt hash, and returns the new plaintext token once.

- **Auth:** Bearer JWT (admin)
- **Path parameters:** None.
- **Query parameters:** None.
- **Request body:** None.
- **Response `200`:**

  | Field | Type | Description |
  |---|---|---|
  | `status` | string | Always `"token_updated"`. |
  | `new_token` | string | The newly generated plaintext token (shown only this once). |

  ```json
  {
    "status": "token_updated",
    "new_token": "f1e2d3c4b5a6978869504132231a0bff..."
  }
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 500 | `internal_error` | Failed to generate token (`Failed to generate token`). |
  | 500 | `internal_error` | Failed to hash token (`Failed to hash token`). |
  | 500 | `internal_error` | Failed to clear the existing token value (`Failed to clear token value`). |
  | 500 | `internal_error` | Failed to store the new token hash (`Failed to update token`). |

- **Source:** `AdminHandler.ResetBotToken` — `internal/handler/admin_bot.go`

## `GET /api/v1/admin/bot/stats`

Returns bot usage statistics and current connection state.

- **Auth:** Bearer JWT (admin)
- **Path parameters:** None.
- **Query parameters:** None.
- **Request body:** None.
- **Response `200`:**

  | Field | Type | Description |
  |---|---|---|
  | `total_commands` | integer | Total commands processed. |
  | `unique_users` | integer | Number of distinct Telegram users. |
  | `last_command_at` | string (nullable) | RFC 3339 UTC time of the last command, or `null`. |
  | `command_breakdown` | array | Per-command counts (unordered). |
  | `command_breakdown[].command` | string | Command name. |
  | `command_breakdown[].count` | integer | Times invoked. |
  | `bot_connected` | boolean | Whether a bot is currently connected over WebSocket. |

  ```json
  {
    "total_commands": 532,
    "unique_users": 41,
    "last_command_at": "2026-06-07T11:45:00Z",
    "command_breakdown": [
      { "command": "/peers", "count": 210 },
      { "command": "/status", "count": 120 }
    ],
    "bot_connected": true
  }
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 500 | `internal_error` | Failed to fetch bot stats. |

- **Source:** `AdminHandler.GetBotStats` — `internal/handler/admin_bot.go`

## `GET /api/v1/admin/bot/commands`

Returns a paginated list of recent bot command invocations.

- **Auth:** Bearer JWT (admin)
- **Path parameters:** None.
- **Query parameters:**

  | Name | Type | Required | Description |
  |---|---|---|---|
  | `page` | integer | No | Page number; only applied when parseable and `> 0`. Default `1`. |

  (Page size is fixed at `50`; `per_page` is echoed in the response but is not read from the query.)

- **Request body:** None.
- **Response `200`:** A paginated wrapper. Each `commands` element:

  | Field | Type | Description |
  |---|---|---|
  | `id` | string | Command-record ID. |
  | `command` | string | Command name. |
  | `tg_user_id` | integer | Telegram user ID. |
  | `username` | string | Telegram username. |
  | `chat_id` | integer | Telegram chat ID. |
  | `created_at` | string | RFC 3339 UTC timestamp. |

  Wrapper fields: `commands` (array), `total` (integer), `page` (integer), `per_page` (integer, always `50`).

  ```json
  {
    "commands": [
      {
        "id": "12345",
        "command": "/peers",
        "tg_user_id": 100200300,
        "username": "exampleuser",
        "chat_id": 100200300,
        "created_at": "2026-06-07T11:45:00Z"
      }
    ],
    "total": 1,
    "page": 1,
    "per_page": 50
  }
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 500 | `internal_error` | Failed to fetch commands. |

- **Source:** `AdminHandler.GetBotRecentCommands` — `internal/handler/admin_bot.go`

## `GET /api/v1/admin/bot/blocked`

Returns the list of Telegram users blocked from using the bot.

- **Auth:** Bearer JWT (admin)
- **Path parameters:** None.
- **Query parameters:** None.
- **Request body:** None.
- **Response `200`:** An object with a `blocked_users` array. Each element:

  | Field | Type | Description |
  |---|---|---|
  | `id` | integer | Block-record ID. |
  | `tg_user_id` | integer | Blocked Telegram user ID. |
  | `username` | string | Telegram username. |
  | `reason` | string | Reason for the block. |
  | `blocked_by` | string | Admin who created the block. |
  | `blocked_at` | string | RFC 3339 UTC timestamp. |

  ```json
  {
    "blocked_users": [
      {
        "id": 5,
        "tg_user_id": 100200300,
        "username": "spammer",
        "reason": "abuse",
        "blocked_by": "admin@example.com",
        "blocked_at": "2026-06-07T10:00:00Z"
      }
    ]
  }
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 500 | `internal_error` | Failed to fetch blocked users. |

- **Source:** `AdminHandler.ListBotBlockedUsers` — `internal/handler/admin_bot.go`

## `POST /api/v1/admin/bot/blocked`

Blocks a Telegram user from using the bot.

- **Auth:** Bearer JWT (admin)
- **Path parameters:** None.
- **Query parameters:** None.
- **Request body:** The decoded struct is:

  ```go
  struct {
      TgUserID int64  `json:"tg_user_id"`
      Username string `json:"username"`
      Reason   string `json:"reason"`
  }
  ```

  | Field | Type | Required | Description |
  |---|---|---|---|
  | `tg_user_id` | integer | Yes | Telegram user ID to block (must be non-zero). |
  | `username` | string | No | Telegram username, for reference. |
  | `reason` | string | No | Reason for the block. |

  ```json
  {
    "tg_user_id": 100200300,
    "username": "spammer",
    "reason": "abuse"
  }
  ```

- **Response `200`:**

  | Field | Type | Description |
  |---|---|---|
  | `status` | string | Always `"blocked"`. |

  ```json
  { "status": "blocked" }
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 400 | `bad_request` | Invalid JSON body or `tg_user_id` is `0` (`tg_user_id is required`). |
  | 500 | `internal_error` | Failed to block user. |

- **Source:** `AdminHandler.BlockBotUser` — `internal/handler/admin_bot.go`

## `DELETE /api/v1/admin/bot/blocked/{id}`

Unblocks a previously blocked Telegram user.

- **Auth:** Bearer JWT (admin)
- **Path parameters:**

  | Name | Type | Description |
  |---|---|---|
  | `id` | string | Block-record ID to remove (read via `r.PathValue("id")`). |

- **Query parameters:**

  | Name | Type | Required | Description |
  |---|---|---|---|
  | `id` | string | No | Alternative way to supply the ID; when present and non-empty, the query value takes precedence over the path parameter. |

- **Request body:** None.
- **Response `200`:**

  | Field | Type | Description |
  |---|---|---|
  | `status` | string | Always `"unblocked"`. |

  ```json
  { "status": "unblocked" }
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 500 | `internal_error` | Failed to unblock user. |

- **Source:** `AdminHandler.UnblockBotUser` — `internal/handler/admin_bot.go`

## `GET /api/v1/admin/bot/export`

Exports all bot settings (with the auth token redacted) and the list of blocked users as a single JSON document.

- **Auth:** Bearer JWT (admin)
- **Path parameters:** None.
- **Query parameters:** None.
- **Request body:** None.
- **Response `200`:** Written directly (`Content-Type: application/json`), not via the versioning helper.

  | Field | Type | Description |
  |---|---|---|
  | `settings` | object | Map of setting key → value (string → string). `bot_auth_token` is replaced with `[REDACTED]`. |
  | `blocked_users` | array | Blocked-user entries. |
  | `blocked_users[].tg_user_id` | integer | Telegram user ID. |
  | `blocked_users[].username` | string | Telegram username. |
  | `blocked_users[].reason` | string | Reason for the block. |

  ```json
  {
    "settings": {
      "bot_enabled": "true",
      "bot_auth_token": "[REDACTED]"
    },
    "blocked_users": [
      { "tg_user_id": 100200300, "username": "spammer", "reason": "abuse" }
    ]
  }
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 500 | `internal_error` | Failed to load bot settings (`Failed to export settings`). |

  (Note: blocked-user lookup errors are ignored; only the settings-load error produces this response.)

- **Source:** `AdminHandler.ExportBotSettings` — `internal/handler/admin_bot.go`

---

# Flap agents

Admin management of the `flapalerted-agent` allowlist (the `flap_agents` table),
the DB-backed replacement for the former `FLAP_AGENT_TOKENS` / `FLAP_AGENT_PUBKEYS`
environment variables. A connecting agent authenticates with the bearer token
issued here, then upgrades to an X25519-encrypted session (see
[../websocket-protocol.md](../websocket-protocol.md) and [./flap.md](./flap.md)).
All endpoints require a **Bearer JWT (admin)**.

## `GET /api/v1/admin/flap/agents`

Lists all configured flap agents with their live connection status.

- **Auth:** Bearer JWT (admin)
- **Request body:** None.
- **Response `200`:** `{ "agents": [ ... ] }` where each agent has:

  | Field | Type | Description |
  |---|---|---|
  | `id` | string (uuid) | Internal record id. |
  | `agent_id` | string | Agent identity / hub key. |
  | `name` / `description` | string | Admin-supplied labels. |
  | `agent_pubkey` | string | TOFU-pinned X25519 public key (hex), or `""` until first connect. |
  | `enabled` | bool | Whether the token is accepted. |
  | `version` | string | Last advertised agent version. |
  | `last_seen_at` | string\|null | RFC3339 timestamp of last register. |
  | `online` | bool | Whether the agent is currently connected. |
  | `has_pubkey` | bool | Convenience flag: `agent_pubkey != ""`. |

- **Source:** `AdminFlapHandler.List` — `internal/handler/admin_flap.go`

## `POST /api/v1/admin/flap/agents`

Registers a new flap agent and returns its bearer token **once**.

- **Auth:** Bearer JWT (admin)
- **Request body:**

  | Field | Type | Required | Description |
  |---|---|---|---|
  | `agent_id` | string | yes | Unique identity; must match the agent's config. |
  | `name` | string | no | Display label. |
  | `description` | string | no | Free-text notes. |

- **Response `201`:** `{ "id", "agent_id", "token", "message" }` — `token` is shown only this once.
- **Errors:** `400 bad_request` (missing `agent_id`), `409 conflict` (duplicate `agent_id`), `500 internal_error`.
- **Source:** `AdminFlapHandler.Create` — `internal/handler/admin_flap.go`

## `PUT /api/v1/admin/flap/agents/{id}`

Updates the editable fields of a flap agent.

- **Auth:** Bearer JWT (admin)
- **Path parameters:** `id` — the flap agent record id.
- **Request body:** any of `name` (string), `description` (string), `enabled` (bool); omitted fields are left unchanged.
- **Response `200`:** the updated agent object.
- **Errors:** `400 bad_request`, `404 not_found`, `500 internal_error`.
- **Source:** `AdminFlapHandler.Update` — `internal/handler/admin_flap.go`

## `POST /api/v1/admin/flap/agents/{id}/regenerate-token`

Issues a new bearer token, invalidating the previous one.

- **Auth:** Bearer JWT (admin)
- **Path parameters:** `id` — the flap agent record id.
- **Response `200`:** `{ "token", "message" }` — shown only this once.
- **Errors:** `404 not_found`, `500 internal_error`.
- **Source:** `AdminFlapHandler.RegenerateToken` — `internal/handler/admin_flap.go`

## `POST /api/v1/admin/flap/agents/{id}/reset-pubkey`

Clears the TOFU-pinned public key so the agent re-pins on its next connection (used when re-provisioning key material).

- **Auth:** Bearer JWT (admin)
- **Path parameters:** `id` — the flap agent record id.
- **Response `200`:** `{ "status": "pubkey_cleared" }`.
- **Errors:** `404 not_found`, `500 internal_error`.
- **Source:** `AdminFlapHandler.ResetPubkey` — `internal/handler/admin_flap.go`

## `DELETE /api/v1/admin/flap/agents/{id}`

Removes a flap agent from the allowlist.

- **Auth:** Bearer JWT (admin)
- **Path parameters:** `id` — the flap agent record id.
- **Response `200`:** `{ "status": "deleted" }`.
- **Errors:** `404 not_found`, `500 internal_error`.
- **Source:** `AdminFlapHandler.Delete` — `internal/handler/admin_flap.go`

---

See also: [./peers.md](./peers.md), [./nodes.md](./nodes.md), [../authentication.md](../authentication.md), [../websocket-protocol.md](../websocket-protocol.md), [../configuration.md](../configuration.md).
