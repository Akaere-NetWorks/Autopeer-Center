# Account API

User-facing account endpoints: email/notification preferences, Telegram binding and its notification preferences, the on-demand looking-glass, and the user's own audit log. All endpoints require a user Bearer JWT and act on the authenticated user's own ASN. See [authentication](../authentication.md) for how to obtain a token.

Base URL: `https://your-center.example.com`

---

# Email & notification preferences

Source file: `internal/handler/email_preferences.go`

There are two parallel models here:

- The legacy **email level** (`0`–`3`) — a single coarse dial (`GET`/`PUT /user/email-preferences`).
- The **explicit per-notification catalog** for the `email` channel (`GET`/`PUT /user/notification-preferences`). When a user has never saved explicit preferences they are in "legacy" mode and the catalog state is derived from their email level.

## `GET /api/v1/user/email-preferences`

Returns the user's current email level and the catalog of available levels.

- **Auth:** Bearer JWT (user)
- **Path parameters:** None
- **Query parameters:** None
- **Request body:** None
- **Response `200`:**

  | Field | Type | Description |
  |---|---|---|
  | `email_level` | integer | Current level for this ASN (`0`–`3`). |
  | `levels` | array | Static catalog of selectable levels (objects with `level`, `name`, `description`). |

  ```json
  {
    "email_level": 2,
    "levels": [
      { "level": 0, "name": "none", "description": "Do not receive any emails" },
      { "level": 1, "name": "urgent", "description": "Only critical changes (peer approved/rejected/suspended/deleted)" },
      { "level": 2, "name": "general", "description": "Critical + general (adds BGP alerts, handshake stale, peer submitted)" },
      { "level": 3, "name": "all", "description": "All emails including real-time monitoring (latency alerts)" }
    ]
  }
  ```

- **Errors:** None. (A repository error yields a zero `email_level`; the handler does not emit an error response.)
- **Source:** `EmailPreferencesHandler.Get` — `internal/handler/email_preferences.go`

## `PUT /api/v1/user/email-preferences`

Sets the user's coarse email level.

- **Auth:** Bearer JWT (user)
- **Path parameters:** None
- **Query parameters:** None
- **Request body:**

  | Field | Type | Required | Description |
  |---|---|---|---|
  | `email_level` | integer | Yes | New level; must be between `0` and `3` inclusive. |

  ```json
  { "email_level": 1 }
  ```

- **Response `200`:**

  | Field | Type | Description |
  |---|---|---|
  | `email_level` | integer | The level that was saved. |

  ```json
  { "email_level": 1 }
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 400 | `bad_request` | Request body is not valid JSON. |
  | 400 | `bad_request` | `email_level` is `< 0` or `> 3`. |
  | 500 | `internal_error` | Failed to persist the preference. |

- **Source:** `EmailPreferencesHandler.Update` — `internal/handler/email_preferences.go`

## `GET /api/v1/user/notification-preferences`

Returns the explicit per-notification catalog for the `email` channel, with each option's current enabled state and whether a setup wizard is needed.

- **Auth:** Bearer JWT (user)
- **Path parameters:** None
- **Query parameters:** None
- **Request body:** None
- **Response `200`:**

  | Field | Type | Description |
  |---|---|---|
  | `mode` | string | `"legacy"` if the user has no saved explicit preferences (state derived from email level), otherwise `"explicit"`. |
  | `email_level` | integer | The user's current legacy email level. |
  | `seen_catalog_version` | integer | The catalog version the user has acknowledged (`0` if never saved). |
  | `current_catalog_version` | integer | The server's current notification catalog version. |
  | `needs_wizard` | boolean | True when in legacy mode or there are unseen options. |
  | `has_unseen_options` | boolean | True when an option's `introduced_version` exceeds `seen_catalog_version`. |
  | `options` | array | Notification options (see below) for the `email` channel. |
  | `presets` | array | Selectable presets (objects with `level`, `name`, `description`, `enabled_keys`). |

  Each entry in `options` is a notification option plus per-user flags:

  | Field | Type | Description |
  |---|---|---|
  | `key` | string | Stable notification key (e.g. `peer_approved`). |
  | `channel` | string | Always `"email"` for this endpoint. |
  | `group` | string | UI grouping, e.g. `account`, `peer_lifecycle`, `connectivity`, `monitoring`. |
  | `name` | string | Human-readable name. |
  | `description` | string | Human-readable description. |
  | `legacy_level` | integer | Email level at which this notification was historically enabled. |
  | `introduced_version` | integer | Catalog version that introduced this option. |
  | `kind` | string | `normal`, `critical`, or `required`. |
  | `requires_disable_confirmation` | boolean | If true, disabling requires explicit confirmation on update. |
  | `enabled` | boolean | Whether this option is currently enabled for the user. |
  | `is_new` | boolean | Whether this option's `introduced_version` exceeds `seen_catalog_version`. |

  ```json
  {
    "mode": "explicit",
    "email_level": 2,
    "seen_catalog_version": 1,
    "current_catalog_version": 1,
    "needs_wizard": false,
    "has_unseen_options": false,
    "options": [
      {
        "key": "auth_login_code",
        "channel": "email",
        "group": "account",
        "name": "Login verification code",
        "description": "Email code used to sign in.",
        "legacy_level": 0,
        "introduced_version": 1,
        "kind": "required",
        "requires_disable_confirmation": true,
        "enabled": true,
        "is_new": false
      },
      {
        "key": "peer_approved",
        "channel": "email",
        "group": "peer_lifecycle",
        "name": "Peer approved",
        "description": "A peering request was approved.",
        "legacy_level": 1,
        "introduced_version": 1,
        "kind": "normal",
        "requires_disable_confirmation": false,
        "enabled": true,
        "is_new": false
      }
    ],
    "presets": [
      { "level": 0, "name": "none", "description": "Disable optional business notifications.", "enabled_keys": ["auth_login_code"] },
      { "level": 1, "name": "urgent", "description": "Important peer lifecycle changes.", "enabled_keys": ["auth_login_code", "peer_approved", "peer_rejected", "peer_suspended", "peer_unsuspended", "peer_deleted", "peer_mtu_updated"] },
      { "level": 2, "name": "general", "description": "Peer lifecycle and connectivity issues.", "enabled_keys": ["auth_login_code", "peer_submitted", "peer_approved", "peer_rejected", "peer_suspended", "peer_unsuspended", "peer_deleted", "peer_bgp_down", "peer_bgp_recovered", "peer_handshake_stale", "peer_mtu_updated", "peer_endpoint_mismatch", "peer_endpoint_recovered"] },
      { "level": 3, "name": "all", "description": "All notification types that exist today.", "enabled_keys": ["auth_login_code", "peer_submitted", "peer_approved", "peer_rejected", "peer_suspended", "peer_unsuspended", "peer_deleted", "peer_bgp_down", "peer_bgp_recovered", "peer_handshake_stale", "peer_latency_high", "peer_latency_recovered", "peer_mtu_updated", "peer_endpoint_mismatch", "peer_endpoint_recovered"] }
    ]
  }
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 500 | `internal_error` | Failed to load notification preferences. |
  | 500 | `internal_error` | Failed to load notification preference state. |

- **Source:** `EmailPreferencesHandler.GetNotifications` — `internal/handler/email_preferences.go`

## `PUT /api/v1/user/notification-preferences`

Saves the explicit set of enabled `email` notifications, with guard rails for disabling critical/required notifications.

- **Auth:** Bearer JWT (user)
- **Path parameters:** None
- **Query parameters:** None
- **Request body:**

  | Field | Type | Required | Description |
  |---|---|---|---|
  | `enabled_keys` | array (string) | No | Notification keys to enable; any catalog key not present is treated as disabled. |
  | `confirmed_disabled_critical_keys` | array (string) | No | Keys the user confirms disabling even though they require disable confirmation. |
  | `seen_catalog_version` | integer | No | Catalog version the user has now seen; bumped to the server's current version if lower. |
  | `wizard_completed` | boolean | No | Marks the setup wizard as completed (records a completion timestamp). |

  ```json
  {
    "enabled_keys": ["auth_login_code", "peer_approved", "peer_rejected"],
    "confirmed_disabled_critical_keys": [],
    "seen_catalog_version": 1,
    "wizard_completed": true
  }
  ```

- **Response `200`:**

  | Field | Type | Description |
  |---|---|---|
  | `status` | string | `"ok"`. |
  | `enabled_keys` | array (string) | Echo of the submitted `enabled_keys`. |
  | `seen_catalog_version` | integer | The seen catalog version that was persisted (raised to the server's current version if the submitted value was lower). |

  ```json
  {
    "status": "ok",
    "enabled_keys": ["auth_login_code", "peer_approved", "peer_rejected"],
    "seen_catalog_version": 1
  }
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 400 | `bad_request` | Request body is not valid JSON. |
  | 400 | `confirmation_required` | One or more options that require disable confirmation are being disabled without being listed in `confirmed_disabled_critical_keys`. Body also includes `keys` (array of offending keys) and `message`. |
  | 412 | `precondition_failed` | A `required`-kind notification is being disabled and no alternative capability is available (e.g. disabling `auth_login_code` without GPG login available). Body also includes `keys` and `message`. |
  | 500 | `internal_error` | Failed to update notification preferences. |
  | 500 | `internal_error` | Failed to update notification preference state. |

  > Note: the `400 confirmation_required` and `412 precondition_failed` bodies are written directly (not via the standard `ErrorJSON` helper) and therefore contain `error`, `message`, and `keys` but no `request_id`. Examples:
  >
  > ```json
  > { "error": "confirmation_required", "message": "Disabling critical notifications requires confirmation.", "keys": ["auth_login_code"] }
  > ```
  >
  > ```json
  > { "error": "precondition_failed", "message": "This notification cannot be disabled until another required capability is available.", "keys": ["auth_login_code"] }
  > ```

- **Source:** `EmailPreferencesHandler.UpdateNotifications` — `internal/handler/email_preferences.go`

---

# Telegram binding

Source file: `internal/handler/telegram_binding.go`

Binding links an ASN to a Telegram account. The flow is: request a one-time bind token, which produces a `t.me` deep link; opening it in the Telegram bot completes the binding out-of-band. Telegram notification preferences are opt-in and only configurable once bound.

## `GET /api/v1/user/telegram/binding`

Returns whether the ASN is bound to a Telegram account, plus the bot username.

- **Auth:** Bearer JWT (user)
- **Path parameters:** None
- **Query parameters:** None
- **Request body:** None
- **Response `200` (bound):**

  | Field | Type | Description |
  |---|---|---|
  | `bound` | boolean | `true`. |
  | `tg_username` | string | Bound Telegram username. |
  | `bound_at` | string (RFC3339) | When the binding was created. |
  | `bound_via` | string | How the binding was created (e.g. via web deep link or in-bot). |
  | `bot_username` | string | Configured Telegram bot username (may be empty if not configured). |

  ```json
  {
    "bound": true,
    "tg_username": "exampleuser",
    "bound_at": "2026-06-07T12:00:00Z",
    "bound_via": "web",
    "bot_username": "ExampleAutopeerBot"
  }
  ```

- **Response `200` (not bound):**

  ```json
  {
    "bound": false,
    "bot_username": "ExampleAutopeerBot"
  }
  ```

  > Not-bound is the same `200` response whenever no binding exists for the ASN (any lookup failure other than a cancelled request context is treated as "not bound"). A cancelled request context returns no body.

- **Errors:** None.
- **Source:** `TelegramBindingHandler.GetBinding` — `internal/handler/telegram_binding.go`

## `POST /api/v1/user/telegram/bind-token`

Generates a one-time bind token and returns a Telegram deep link to complete binding. The token expires 10 minutes after issuance.

- **Auth:** Bearer JWT (user)
- **Path parameters:** None
- **Query parameters:** None
- **Request body:** None (not read)
- **Response `200`:**

  | Field | Type | Description |
  |---|---|---|
  | `deeplink` | string | `https://t.me/<bot>?start=bind_<token>` link to open in Telegram. |
  | `expires_at` | string (RFC3339) | Token expiry (10 minutes from issuance). |

  ```json
  {
    "deeplink": "https://t.me/ExampleAutopeerBot?start=bind_3f1c2a...",
    "expires_at": "2026-06-07T12:10:00Z"
  }
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 503 | `not_configured` | The Telegram bot is not configured (empty bot username). |
  | 409 | `already_bound` | This ASN is already bound to a Telegram account; unbind first. |
  | 500 | `internal_error` | Failed to generate the random token. |
  | 500 | `internal_error` | Failed to store the bind token. |

- **Source:** `TelegramBindingHandler.CreateBindToken` — `internal/handler/telegram_binding.go`

## `DELETE /api/v1/user/telegram/binding`

Removes the Telegram binding for the ASN.

- **Auth:** Bearer JWT (user)
- **Path parameters:** None
- **Query parameters:** None
- **Request body:** None
- **Response `200`:**

  | Field | Type | Description |
  |---|---|---|
  | `status` | string | `"unbound"`. |

  ```json
  { "status": "unbound" }
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 404 | `not_bound` | No Telegram binding exists for this account. |
  | 500 | `internal_error` | Failed to delete the binding. |

- **Source:** `TelegramBindingHandler.DeleteBinding` — `internal/handler/telegram_binding.go`

## `GET /api/v1/user/telegram/notification-preferences`

Returns the Telegram notification catalog and the user's enabled state, or a `bound: false` stub if no Telegram account is bound.

- **Auth:** Bearer JWT (user)
- **Path parameters:** None
- **Query parameters:** None
- **Request body:** None
- **Response `200` (bound):**

  | Field | Type | Description |
  |---|---|---|
  | `bound` | boolean | `true`. |
  | `channel` | string | `"telegram"`. |
  | `options` | array | Notification options for the `telegram` channel. |
  | `presets` | array | Telegram presets (objects with `level`, `name`, `description`, `enabled_keys`). |

  Each `options` entry has the same notification-option fields documented under [notification-preferences](#get-apiv1usernotification-preferences) (`key`, `channel`, `group`, `name`, `description`, `legacy_level`, `introduced_version`, `kind`, `requires_disable_confirmation`) plus `enabled` (boolean). There is **no** `is_new` field on this channel. Telegram notifications are opt-in: every option reads as disabled until preferences are saved.

  ```json
  {
    "bound": true,
    "channel": "telegram",
    "options": [
      {
        "key": "peer_approved",
        "channel": "telegram",
        "group": "peer_lifecycle",
        "name": "Peer approved",
        "description": "A peering request was approved.",
        "legacy_level": 0,
        "introduced_version": 2,
        "kind": "normal",
        "requires_disable_confirmation": false,
        "enabled": true
      }
    ],
    "presets": [
      { "level": 0, "name": "none", "description": "Disable all Telegram notifications.", "enabled_keys": [] },
      { "level": 1, "name": "important", "description": "Peer lifecycle changes.", "enabled_keys": ["peer_submitted", "peer_approved", "peer_rejected", "peer_suspended", "peer_unsuspended", "peer_deleted"] },
      { "level": 2, "name": "all", "description": "All Telegram notifications.", "enabled_keys": ["peer_submitted", "peer_approved", "peer_rejected", "peer_suspended", "peer_unsuspended", "peer_deleted", "peer_bgp_down", "peer_bgp_recovered", "peer_handshake_stale", "peer_latency_high", "peer_latency_recovered", "peer_endpoint_mismatch", "peer_endpoint_recovered"] }
    ]
  }
  ```

- **Response `200` (not bound):**

  ```json
  {
    "bound": false,
    "channel": "telegram"
  }
  ```

  > The not-bound stub is returned whenever the binding lookup fails or returns no binding, so an unbound caller never sees an error here.

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 500 | `internal_error` | Failed to load Telegram notification preferences. |

- **Source:** `TelegramBindingHandler.GetTelegramNotifications` — `internal/handler/telegram_binding.go`

## `PUT /api/v1/user/telegram/notification-preferences`

Saves the explicit set of enabled Telegram notifications. Requires a bound Telegram account.

- **Auth:** Bearer JWT (user)
- **Path parameters:** None
- **Query parameters:** None
- **Request body:**

  | Field | Type | Required | Description |
  |---|---|---|---|
  | `enabled_keys` | array (string) | No | Telegram notification keys to enable; any catalog key not present is disabled. |

  ```json
  { "enabled_keys": ["peer_approved", "peer_rejected", "peer_bgp_down"] }
  ```

- **Response `200`:**

  | Field | Type | Description |
  |---|---|---|
  | `status` | string | `"ok"`. |
  | `enabled_keys` | array (string) | Echo of the submitted `enabled_keys`. |

  ```json
  {
    "status": "ok",
    "enabled_keys": ["peer_approved", "peer_rejected", "peer_bgp_down"]
  }
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 412 | `not_bound` | No Telegram account is bound; bind first. (Checked before the body is decoded.) |
  | 400 | `bad_request` | Request body is not valid JSON. |
  | 500 | `internal_error` | Failed to update Telegram notification preferences. |

- **Source:** `TelegramBindingHandler.UpdateTelegramNotifications` — `internal/handler/telegram_binding.go`

---

# Looking glass

Source file: `internal/handler/lookingglass.go`

## `POST /api/v1/user/looking-glass/run`

Runs an on-demand network diagnostic (ping, traceroute, MTR, or BGP route lookup) synchronously against a node's agent.

- **Auth:** Bearer JWT (user)
- **Rate limiting:** Per-user (keyed by the caller's `AS<asn>`), sustained 1 query / 10s with a burst of 3 (token-bucket, `rate.Limit(0.1)` / burst `3`). Cheap validation runs first; the limiter is checked after validation but before any work against the node. Over budget returns `429 rate_limited`.
- **Path parameters:** None
- **Query parameters:** None
- **Request body:** (struct `lookingGlassRequest`)

  | Field | Type | Required | Description |
  |---|---|---|---|
  | `node_id` | string | Yes | ID of the node whose agent runs the query. Trimmed; must be non-empty. |
  | `type` | string | Yes | Query type (trimmed, lowercased). One of: `ping`, `traceroute` (alias `trace`), `mtr`, `bgp_route` (aliases `bgp`, `route`). |
  | `target` | string | Yes | Target host/IP/prefix. Trimmed; must be non-empty and at most 255 characters. |

  ```json
  {
    "node_id": "node-fra-1",
    "type": "ping",
    "target": "fe80::1"
  }
  ```

- **Response `200`:**

  | Field | Type | Description |
  |---|---|---|
  | `type` | string | The submitted query type after trimming and lowercasing (may be an alias such as `trace`, not the canonical name). |
  | `node_id` | string | The node ID that ran the query. |
  | `node_name` | string | The node's display name. |
  | `target` | string | The (trimmed) query target. |
  | `result` | object | The agent's raw result payload for the query type (shape depends on `type`). |

  ```json
  {
    "type": "ping",
    "node_id": "node-fra-1",
    "node_name": "Frankfurt 1",
    "target": "fe80::1",
    "result": { "output": "..." }
  }
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 400 | `invalid_request` | Request body is not valid JSON. |
  | 400 | `invalid_request` | `node_id` is empty. |
  | 400 | `invalid_request` | `target` is empty. |
  | 400 | `invalid_request` | `target` exceeds 255 characters. |
  | 400 | `invalid_request` | `type` is not one of the supported query types. |
  | 429 | `rate_limited` | Per-user looking-glass rate limit exceeded. |
  | 404 | `not_found` | Node does not exist or is disabled. |
  | 503 | `node_offline` | The node's agent is not currently connected. |
  | 503 | `agent_error` | Failed to reach the agent (message includes the underlying error). |
  | 400 | `query_failed` | The agent ran the query but reported failure (message is the agent's error). |

- **Source:** `LookingGlassHandler.RunQuery` — `internal/handler/lookingglass.go`

See [websocket-protocol](../websocket-protocol.md) for how the center relays the query to the node agent.

---

# Audit log

Source file: `internal/handler/admin_audit.go`

## `GET /api/v1/user/audit`

Returns the authenticated user's own audit log entries (scoped to their ASN), paginated and optionally filtered by action.

- **Auth:** Bearer JWT (user)
- **Path parameters:** None
- **Query parameters:**

  | Name | Type | Required | Description |
  |---|---|---|---|
  | `action` | string | No | Filter by action string (e.g. `looking_glass.query`). |
  | `page` | integer | No | 1-based page number. Defaults to `1`; non-positive or invalid values are ignored (default kept). |
  | `per_page` | integer | No | Page size. Defaults to `25`; only values `> 0` and `<= 200` are accepted (anything else keeps the default). |

- **Request body:** None
- **Response `200`:**

  | Field | Type | Description |
  |---|---|---|
  | `logs` | array | Audit entries (see below). |
  | `total` | integer | Total matching entries for this ASN. |
  | `page` | integer | The page that was returned. |
  | `per_page` | integer | The page size that was applied. |

  Each entry in `logs`:

  | Field | Type | Description |
  |---|---|---|
  | `id` | string | Audit entry ID. |
  | `action` | string | Action name. |
  | `operator` | string | Who performed the action (e.g. `AS4242420000`). |
  | `target_id` | string (nullable) | Affected resource ID; omitted when null. |
  | `detail` | object (nullable) | Action-specific details; omitted when null. |
  | `created_at` | string (RFC3339) | When the action occurred (UTC). |

  ```json
  {
    "logs": [
      {
        "id": "a1b2c3d4",
        "action": "looking_glass.query",
        "operator": "AS4242420000",
        "target_id": "node-fra-1",
        "detail": { "type": "ping", "target": "fe80::1" },
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

- **Source:** `AdminHandler.ListUserAuditLogs` — `internal/handler/admin_audit.go`
