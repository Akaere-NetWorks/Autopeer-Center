# MCP & Assistant API

The MCP (Model Context Protocol) surface lets AI assistants and agentic tooling
inspect and operate on the peering platform through a capability-scoped tool set.
There are two MCP servers (user and admin), each speaking JSON-RPC 2.0 over an
SSE transport, plus a set of REST endpoints for managing MCP keys, reading audit
logs, and driving the in-product assistant approval flow.

Base URL: `https://your-center.example.com` (WebSocket/SSE over the same host).

For the platform's JWT/session model and how it differs from MCP keys, see
[../authentication.md](../authentication.md). For the SSE/WebSocket conventions
used elsewhere, see [../websocket-protocol.md](../websocket-protocol.md). For
environment configuration (e.g. `JWT_SECRET`, which keys the assistant approval
tokens), see [../configuration.md](../configuration.md).

---

## Concepts

### Two MCP servers

| Server | SSE / message path | Authentication | Audience |
|---|---|---|---|
| User MCP | `/api/v1/mcp` | User MCP key (`ap_mcp_` prefix) | Tools scoped to a single ASN |
| Admin MCP | `/api/v1/admin/mcp` | Admin MCP key (`ap_mcp_admin_` prefix) | Read-only tools across the whole platform |

Both servers share the same JSON-RPC transport, capability model, and
audit-logging pipeline. They are authenticated by dedicated MCP API keys, **not**
by JWTs. Key *management* and the audit/assistant REST endpoints, by contrast,
require a normal JWT (you cannot use an MCP key to mint or revoke MCP keys).

### Key formats

MCP keys are bearer tokens distinguished by a fixed prefix:

| Key type | Prefix | Stored prefix length | Used on |
|---|---|---|---|
| User MCP key | `ap_mcp_` | first 16 chars | `/api/v1/mcp` |
| Admin MCP key | `ap_mcp_admin_` | first 20 chars | `/api/v1/admin/mcp` |

A key is the prefix followed by 32 random bytes, hex-encoded. Only a SHA-256 hash
of the full key is stored server-side; the plaintext `key` is returned **once**,
at creation time, and can never be retrieved again. Each key also stores a short
`key_prefix` (the leading characters) for display/lookup.

The auth middleware is strict about prefixes so the two surfaces can never be
confused: `/api/v1/mcp` accepts only `ap_mcp_` keys and explicitly **rejects**
`ap_mcp_admin_` keys; `/api/v1/admin/mcp` accepts only `ap_mcp_admin_` keys; a
JWT in the `Authorization` header is not accepted on the MCP transport endpoints.
Keys that are expired (`expires_at` in the past) or revoked (`revoked_at` set) are
rejected with `401`. Each successful use asynchronously updates the key's
`last_used_at`.

### Capability model

Every tool declares a required **capability** string. A key grants a set of
capabilities; `tools/list` returns only the tools the key can use, and
`tools/call` returns `forbidden` (`-32003`) for any tool whose capability the key
lacks (the forbidden attempt is still audited). If a key is created with no
explicit capabilities, a default set is applied. Requested capabilities are
sanitized against the allow-list: unknown or duplicate entries are dropped, and
if nothing valid remains, the defaults are used. Creating a key with a capability
outside the allow-list returns `400 invalid_capability`.

**User capabilities** (allow-list in `mcp_capabilities.go`):

| Capability | Default? | Grants |
|---|:---:|---|
| `read:nodes` | yes | List peering nodes you can peer with. |
| `read:peers` | yes | List/inspect your peers, operations, and creation status. |
| `read:metrics` | yes | Read peer summaries and time-series metrics. |
| `read:audit` | | Read your ASN's MCP audit logs. |
| `read:preferences` | | Reserved for reading preferences. |
| `write:peer:create` | | Create a pending peer. |
| `write:peer:update_pending` | | Update a pending/rejected peer. |
| `write:peer:cancel_pending` | | Cancel a pending/rejected peer. |
| `write:operation:create` | | Request admin review for changes to active/suspended peers. |
| `write:preferences` | | Reserved for writing preferences. |

**Admin capabilities** (allow-list in `mcp_capabilities.go`):

| Capability | Default? | Grants |
|---|:---:|---|
| `admin:read:topology` | yes | List nodes and node statistics. |
| `admin:read:peers` | yes | List/inspect peers across all ASNs; list ASNs. |
| `admin:read:metrics` | yes | Read peer time-series metrics. |
| `admin:read:audit` | yes | Read platform-wide MCP audit logs. |
| `admin:read:mcp_keys` | yes | List user MCP key metadata and MCP sessions. |
| `admin:read:settings` | yes | Read site, notification, and bot settings metadata. |
| `admin:read:system` | | Read system stats, releases, and bot stats/blocked users. |
| `admin:read:sensitive` | | Read sensitive peer fields (endpoint, email, keys, addresses). |

All admin MCP tools are read-only; there are no admin write tools.

### Audit logging & argument sanitization

Every MCP and assistant tool call is written to an MCP audit log capturing the
session ID, principal (ASN + key ID for user calls; admin ID + admin key ID for
admin calls), tool name, required capability, mode (`read`, `sync_write`, or
`operation`), the (sanitized) arguments, the call duration, a success flag, and
any error message. Forbidden attempts are logged with a `forbidden` error.

Before persistence, secret-bearing argument fields are stripped: `remote_pubkey`,
`remote_endpoint`, `contact_email`, `token`, `secret`, `password`,
`signed_message`, and `approval_token`. Audit writes are best-effort-but-never-
dropped: enqueued to the background job queue when available, otherwise written
synchronously.

---

# MCP transport (JSON-RPC over SSE)

The transport is the standard MCP "HTTP + SSE" pattern, split across two HTTP
methods on the same path. The flow is identical for the user (`/api/v1/mcp`) and
admin (`/api/v1/admin/mcp`) servers; only the auth scheme and tool set differ.

1. **Open the SSE stream** with `GET`, sending the MCP key in `Authorization`.
   The server replies `text/event-stream` and immediately emits an `endpoint`
   event whose data is the message URL (with a generated `session_id`).
2. **Send JSON-RPC requests** with `POST` to that message URL. Each POST returns
   `202 Accepted`; the JSON-RPC **response is delivered back over the SSE
   stream** as an `event: message` frame, correlated by the request `id`.

The session is bound to the authenticated principal: a POST whose `session_id`
does not belong to the same ASN (user server) or admin (admin server) as the
caller is rejected with `404`.

## `GET /api/v1/mcp`

Open the user MCP SSE stream and obtain a session message endpoint.

- **Auth:** MCP key (`ap_mcp_<...>`).
- **Path parameters:** None.
- **Query parameters:** None.
- **Request body:** None.
- **Response `200`:** an SSE stream (`Content-Type: text/event-stream`). The
  first frame is an `endpoint` event carrying the message URL for this session;
  thereafter the server sends `: keepalive` comment lines every 25 seconds and
  delivers every JSON-RPC response as a `message` event.

  ```
  event: endpoint
  data: /api/v1/mcp?session_id=11111111-2222-3333-4444-555555555555

  : keepalive

  event: message
  data: {"jsonrpc":"2.0","id":1,"result":{"tools":[ ... ]}}
  ```

- **Errors:**

  | Status | Body | Condition |
  |---|---|---|
  | 401 | `{"error":"unauthorized","message":"Missing MCP API key"}` | No `Authorization: Bearer` header. |
  | 401 | `{"error":"unauthorized","message":"Invalid MCP API key format"}` | Key has the `ap_mcp_admin_` prefix, or lacks the `ap_mcp_` prefix. |
  | 401 | `{"error":"unauthorized","message":"Invalid or revoked MCP API key"}` | Key hash not found. |
  | 401 | `{"error":"unauthorized","message":"MCP API key has expired"}` | `expires_at` in the past. |
  | 401 | `{"error":"unauthorized","message":"MCP API key has been revoked"}` | `revoked_at` set. |
  | 500 | `SSE not supported` (plain text) | Server response writer cannot flush. |

- **Source:** `MCPHandler.HandleSSE` — `internal/handler/mcp.go` (auth middleware `RequireMCPKey` in `internal/middleware/middleware.go`).

## `POST /api/v1/mcp`

Submit a single JSON-RPC 2.0 request for an open user MCP session; the response
arrives over the SSE stream.

- **Auth:** MCP key (`ap_mcp_<...>`).
- **Path parameters:** None.
- **Query parameters:**

  | Name | Type | Required | Description |
  |---|---|:---:|---|
  | `session_id` | string | yes | The `session_id` from the `endpoint` SSE event of the matching `GET /api/v1/mcp` stream. |

- **Request body:** a single JSON-RPC 2.0 request envelope (decoded into
  `jsonRPCRequest`).

  | Field | Type | Required | Description |
  |---|---|:---:|---|
  | `jsonrpc` | string | yes | Must be `"2.0"`. |
  | `id` | string/integer/null | no | Request id echoed back on the response. |
  | `method` | string | yes | One of `initialize`, `tools/list`, `tools/call`. |
  | `params` | object | no | Method parameters. For `tools/call`: `{"name": "<tool>", "arguments": { ... }}`. |

  ```json
  {
    "jsonrpc": "2.0",
    "id": 1,
    "method": "tools/call",
    "params": {
      "name": "peer_list",
      "arguments": {}
    }
  }
  ```

- **Response `202`:** empty body. The POST is acknowledged immediately; the
  JSON-RPC response (or error) is written back over the SSE stream as a `message`
  event. A `tools/call` result follows the MCP content convention — the tool's
  JSON output is serialized into a single `text` content block:

  ```json
  {
    "jsonrpc": "2.0",
    "id": 1,
    "result": {
      "content": [
        { "type": "text", "text": "[{\"id\":\"...\",\"status\":\"active\"}]" }
      ]
    }
  }
  ```

- **JSON-RPC methods:**

  | Method | Behavior |
  |---|---|
  | `initialize` | Returns `protocolVersion` `"2024-11-05"`, `serverInfo` `{"name":"AutoPeer MCP","version":"1.0.0"}`, and an empty `capabilities.tools` object. |
  | `tools/list` | Returns `{"tools":[...]}` listing only the tools this key's capabilities allow. |
  | `tools/call` | Invokes a tool by `name` with `arguments`; returns a `text` content block or a JSON-RPC error. |

- **Errors (transport-level, returned on the POST as plain JSON / status):**

  | Status | Body | Condition |
  |---|---|---|
  | 400 | `{"error":"missing session_id"}` | `session_id` query param absent. |
  | 404 | `{"error":"session not found or expired"}` | Session unknown, or its ASN does not match the caller. |
  | 429 | `{"error":"too many concurrent requests"}` | More than 5 in-flight requests for this session. |
  | 401 | (see `GET /api/v1/mcp`) | MCP key auth failure (raised by the `RequireMCPKey` middleware). |

  A request body that is not valid JSON does **not** fail the POST: it returns
  `202` and the `-32700 Parse error` is delivered over the SSE stream (see below).

- **Errors (JSON-RPC, delivered over SSE):**

  | `code` | `message` | Condition |
  |---|---|---|
  | -32700 | `Parse error` | Request body is not valid JSON. |
  | -32601 | `Method not found: <m>` | Unknown JSON-RPC method. |
  | -32601 | `Unknown tool: <name>` | `tools/call` for a tool that does not exist. |
  | -32602 | `Invalid params` | `tools/call` `params` cannot be unmarshalled. |
  | -32602 | `invalid arguments` | A tool's `arguments` fail strict decoding (unknown field, wrong type). |
  | -32602 | `idempotency_key must be 16 to 128 characters` | Write/operation tool with a missing/short/long idempotency key. |
  | -32003 | `forbidden` | The key lacks the tool's required capability (audited). |
  | -32009 | `idempotency_conflict` | Idempotency key reused with different arguments. |
  | -32029 | `operation_in_progress` | Idempotency key reused while the first call is still processing. |
  | -32000 | `<tool error message>` | Tool execution failed (e.g. validation, `peer not found`), or replay of a previously failed idempotent call. |

- **Concurrency & timeouts:** each session allows at most **5** concurrent
  in-flight requests (`429` beyond that); each dispatched call has a **30 second**
  processing timeout; the SSE outbound buffer holds 64 messages and drops the
  newest message if a client cannot keep up.

- **Source:** `MCPHandler.HandleMessage` — `internal/handler/mcp.go` (dispatch/tool handlers in `internal/handler/mcp.go`, `internal/handler/mcp_user_tools.go`).

### User tools

Exposed through `tools/list` and `tools/call` on `/api/v1/mcp`. Read tools have
no side effects. Write/operation tools mutate state and **require** an
`idempotency_key` argument (16–128 chars).

| Tool | Capability | Mode | Description |
|---|---|---|---|
| `nodes_list` | `read:nodes` | read | List all available DN42 BGP peering nodes you can peer with. |
| `peer_creation_status` | `read:peers` | read | Whether new peer creation is currently enabled. |
| `peer_list` | `read:peers` | read | List all peers belonging to your ASN. |
| `peer_summary` | `read:metrics` | read | Per-peer latest RTT, BGP state, handshake time. |
| `peer_get` | `read:peers` | read | Full details of one of your peers (with node info). |
| `peer_get_metrics` | `read:metrics` | read | Time-series metrics for a peer (`hours` 1–720, default 24). |
| `mcp_audit_logs_list` | `read:audit` | read | Recent MCP audit logs for your ASN (`limit` 1–200, default 50). |
| `operations_list` | `read:peers` | read | Pending MCP operations for your ASN (`limit` 1–200, default 50). |
| `operation_get` | `read:peers` | read | Get one pending operation by ID. |
| `peer_create` | `write:peer:create` | sync_write | Create a peer in `pending` status awaiting approval. |
| `peer_update_pending` | `write:peer:update_pending` | sync_write | Update WireGuard params of a pending/rejected peer. |
| `peer_cancel_pending` | `write:peer:cancel_pending` | sync_write | Cancel (delete) a pending/rejected peer. |
| `peer_update_operation_create` | `write:operation:create` | operation | Request admin review to change an active/suspended peer. |
| `peer_delete_operation_create` | `write:operation:create` | operation | Request admin review to delete an active/suspended peer. |

Behavioral notes (enforced in the tool handlers):

- `peer_create` requires `node_id`, `remote_pubkey`, `remote_endpoint`,
  `remote_lla`, and `idempotency_key`; `mtu` is optional. It validates the
  WireGuard public key (Base64, ≥ 40 chars), endpoint (`IP:Port` or
  `hostname:Port`), and link-local address (must be in `fe80::/10`, e.g.
  `fe80::1`); optional MTU is 576–9000. It also rejects creation when disabled by
  the administrator, and enforces one peer per node per ASN, a 10-pending-request
  cap, and per-node WireGuard port uniqueness. New peers always start `pending`.
- `peer_update_pending` / `peer_cancel_pending` operate **only** on `pending` or
  `rejected` peers; `peer_update_pending` requires at least one of
  `remote_pubkey`, `remote_endpoint`, `remote_lla`, `mtu`.
- For `active`/`suspended` peers, direct mutation is rejected; the
  `*_operation_create` tools instead file an operation in `pending_admin_review`
  for an administrator to act on out-of-band.

**Idempotency (user write tools):** the server records the `idempotency_key` plus
a canonical SHA-256 hash of the request arguments (excluding the key) so retries
are safe. Replaying a succeeded request returns the stored result; reusing a key
with different arguments returns `idempotency_conflict` (`-32009`); reusing a key
while the first call is still processing returns `operation_in_progress`
(`-32029`).

---

## `GET /api/v1/admin/mcp`

Open the admin MCP SSE stream and obtain a session message endpoint.

- **Auth:** admin MCP key (`ap_mcp_admin_<...>`).
- **Path parameters:** None.
- **Query parameters:** None.
- **Request body:** None.
- **Response `200`:** an SSE stream identical in shape to the user stream, but the
  `endpoint` event points at `/api/v1/admin/mcp?session_id=<uuid>`.

  ```
  event: endpoint
  data: /api/v1/admin/mcp?session_id=11111111-2222-3333-4444-555555555555
  ```

- **Errors:**

  | Status | Body | Condition |
  |---|---|---|
  | 401 | `{"error":"unauthorized","message":"Missing admin MCP API key"}` | No `Authorization: Bearer` header. |
  | 401 | `{"error":"unauthorized","message":"Invalid admin MCP API key format"}` | Key lacks the `ap_mcp_admin_` prefix. |
  | 401 | `{"error":"unauthorized","message":"Invalid or revoked admin MCP API key"}` | Key hash not found. |
  | 401 | `{"error":"unauthorized","message":"Admin MCP API key has expired"}` | `expires_at` in the past. |
  | 401 | `{"error":"unauthorized","message":"Admin MCP API key has been revoked"}` | `revoked_at` set. |
  | 500 | `SSE not supported` (plain text) | Server response writer cannot flush. |

- **Source:** `AdminMCPHandler.HandleSSE` — `internal/handler/admin_mcp.go` (auth middleware `RequireAdminMCPKey`).

## `POST /api/v1/admin/mcp`

Submit a single JSON-RPC 2.0 request for an open admin MCP session.

- **Auth:** admin MCP key (`ap_mcp_admin_<...>`).
- **Path parameters:** None.
- **Query parameters:**

  | Name | Type | Required | Description |
  |---|---|:---:|---|
  | `session_id` | string | yes | The `session_id` from the matching `GET /api/v1/admin/mcp` stream. |

- **Request body:** a JSON-RPC 2.0 request envelope (same shape as the user
  server; `initialize`, `tools/list`, `tools/call`).
- **Response `202`:** empty body; the JSON-RPC response is delivered over the SSE
  stream. `initialize` returns `serverInfo` `{"name":"AutoPeer Admin MCP","version":"1.0.0"}`.
- **Errors:** same transport-level and JSON-RPC error tables as
  `POST /api/v1/mcp`, except admin auth uses the admin-key error messages and the
  `404 session not found` check is against the caller's admin ID. Admin tools are
  read-only, so the idempotency error codes (`-32009`, `-32029`) and the
  idempotency-key validation error do not apply.
- **Concurrency & timeouts:** identical to the user server (5 concurrent, 30 s
  timeout, 64-message buffer).
- **Source:** `AdminMCPHandler.HandleMessage` — `internal/handler/admin_mcp.go`.

### Admin tools

All admin tools are read-only and gated by an `admin:read:*` capability. They
fall into the following categories:

| Category (capability) | Tools |
|---|---|
| Topology (`admin:read:topology`) | `nodes_list`, `nodes_stats` |
| Peers (`admin:read:peers`) | `peers_list_all`, `peers_list_by_asn`, `peer_get`, `users_list` |
| Sensitive peer detail (`admin:read:sensitive`) | `peer_get_sensitive` |
| Metrics (`admin:read:metrics`) | `peer_metrics` |
| MCP keys & sessions (`admin:read:mcp_keys`) | `mcp_keys_list`, `mcp_sessions_list` |
| Audit (`admin:read:audit`) | `audit_logs_list` |
| Settings (`admin:read:settings`) | `site_settings_get`, `notification_settings_get`, `bot_settings_get` |
| System (`admin:read:system`) | `releases_list`, `bot_stats_get`, `bot_blocked_list`, `system_stats_get` |

`peer_get_sensitive` is a separate capability from `admin:read:peers` so that
peer read access can be granted without exposing endpoints, contact emails,
public keys, or node addresses.

---

# MCP key management

These REST endpoints are authenticated with a **JWT** (not an MCP key). None of
them call `JSONVersioned`, so their responses are not affected by the
`Autopeer-Version` header.

## `GET /api/v1/user/mcp-keys`

List your ASN's MCP keys (metadata only; the secret is never returned).

- **Auth:** Bearer JWT (user).
- **Path parameters:** None.
- **Query parameters:** None.
- **Request body:** None.
- **Response `200`:** a bare JSON array of `MCPKey` objects (`key_hash` is
  `json:"-"` and never serialized).

  | Field | Type | Description |
  |---|---|---|
  | `id` | string | Key UUID. |
  | `asn` | integer | Owning ASN. |
  | `name` | string | Display name. |
  | `key_prefix` | string | Leading 16 chars of the key. |
  | `capabilities` | array | Granted capability strings. |
  | `expires_at` | string (nullable) | RFC 3339 expiry, omitted if unset. |
  | `last_used_at` | string (nullable) | RFC 3339 last use, omitted if never used. |
  | `revoked_at` | string (nullable) | RFC 3339 revocation, omitted if active. |
  | `scope_version` | integer | Capability scope version. |
  | `created_at` | string | RFC 3339 creation time. |

  ```json
  [
    {
      "id": "11111111-2222-3333-4444-555555555555",
      "asn": 4242420000,
      "name": "my-assistant",
      "key_prefix": "ap_mcp_0a1b2c3d",
      "capabilities": ["read:nodes", "read:peers", "read:metrics"],
      "expires_at": "2027-01-01T00:00:00Z",
      "last_used_at": "2026-06-07T09:30:00Z",
      "scope_version": 1,
      "created_at": "2026-06-01T12:00:00Z"
    }
  ]
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 500 | `db_error` | Failed to list MCP keys. |

- **Source:** `MCPKeyHandler.List` — `internal/handler/mcpkey.go`

## `POST /api/v1/user/mcp-keys`

Create a new user MCP key. The plaintext key is returned once and cannot be
retrieved again.

- **Auth:** Bearer JWT (user).
- **Path parameters:** None.
- **Query parameters:** None.
- **Request body:**

  | Field | Type | Required | Description |
  |---|---|:---:|---|
  | `name` | string | yes | Display name, 1–64 characters. |
  | `expires_at` | string (nullable) | no | RFC 3339 expiry; must be in the future. |
  | `capabilities` | array | no | Capability strings from the user allow-list; defaults applied if omitted/empty. |

  ```json
  {
    "name": "my-assistant",
    "expires_at": "2027-01-01T00:00:00Z",
    "capabilities": ["read:nodes", "read:peers", "read:metrics"]
  }
  ```

- **Response `201`:** the created key as a JSON object, including the one-time
  `key` plaintext.

  | Field | Type | Description |
  |---|---|---|
  | `id` | string | Key UUID. |
  | `asn` | integer | Owning ASN. |
  | `name` | string | Display name. |
  | `key` | string | Plaintext key (`ap_mcp_<hex>`) — shown only here. |
  | `key_prefix` | string | Leading 16 chars of the key. |
  | `capabilities` | array | Granted (sanitized) capabilities. |
  | `expires_at` | string (nullable) | RFC 3339 expiry, or `null`. |
  | `last_used_at` | string (nullable) | `null` at creation. |
  | `created_at` | string | RFC 3339 creation time. |

  ```json
  {
    "id": "11111111-2222-3333-4444-555555555555",
    "asn": 4242420000,
    "name": "my-assistant",
    "key": "ap_mcp_0a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f9",
    "key_prefix": "ap_mcp_0a1b2c3d",
    "capabilities": ["read:nodes", "read:peers", "read:metrics"],
    "expires_at": "2027-01-01T00:00:00Z",
    "last_used_at": null,
    "created_at": "2026-06-07T12:00:00Z"
  }
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 400 | `invalid_body` | Request body is not valid JSON. |
  | 400 | `name_required` | `name` is empty. |
  | 400 | `name_too_long` | `name` exceeds 64 characters. |
  | 400 | `invalid_capability` | A requested capability is not in the user allow-list. |
  | 400 | `invalid_expires_at` | `expires_at` is not RFC 3339. |
  | 400 | `expires_in_past` | `expires_at` is in the past. |
  | 422 | `limit_reached` | You already have 10 keys (the per-ASN maximum). |
  | 500 | `db_error` | Failed to count or create the key. |
  | 500 | `rand_error` | Failed to generate the key bytes. |

- **Source:** `MCPKeyHandler.Create` — `internal/handler/mcpkey.go`

## `DELETE /api/v1/user/mcp-keys/{id}`

Revoke one of your MCP keys.

- **Auth:** Bearer JWT (user).
- **Path parameters:**

  | Name | Type | Description |
  |---|---|---|
  | `id` | string | Key UUID to revoke. |

- **Query parameters:** None.
- **Request body:** None.
- **Response `200`:**

  ```json
  { "message": "MCP key revoked" }
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 500 | `db_error` | Failed to revoke the key. |

- **Source:** `MCPKeyHandler.Delete` — `internal/handler/mcpkey.go`

## `GET /api/v1/admin/mcp-keys`

List the calling admin's own admin MCP keys (metadata only).

- **Auth:** Bearer JWT (admin).
- **Path parameters:** None.
- **Query parameters:** None.
- **Request body:** None.
- **Response `200`:** a bare JSON array of `AdminMCPKey` objects (same fields as
  the user list, but with `admin_id` instead of `asn`; `key_hash` is `json:"-"`
  and never serialized).

  ```json
  [
    {
      "id": "aaaa1111-2222-3333-4444-555555555555",
      "admin_id": "9f1c0d2e-3a4b-5c6d-7e8f-90a1b2c3d4e5",
      "name": "ops-admin-key",
      "key_prefix": "ap_mcp_admin_0a1b",
      "capabilities": ["admin:read:topology", "admin:read:peers"],
      "expires_at": null,
      "last_used_at": "2026-06-07T09:30:00Z",
      "scope_version": 1,
      "created_at": "2026-06-01T12:00:00Z"
    }
  ]
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 500 | `db_error` | Failed to list admin MCP keys. |

- **Source:** `AdminMCPKeyHandler.ListAdminKeys` — `internal/handler/admin_mcpkey.go`

## `POST /api/v1/admin/mcp-keys`

Create an admin MCP key for the calling admin. The plaintext key is returned
once.

- **Auth:** Bearer JWT (admin).
- **Path parameters:** None.
- **Query parameters:** None.
- **Request body:** identical shape to user key creation; `capabilities` is
  validated against the **admin** allow-list.

  | Field | Type | Required | Description |
  |---|---|:---:|---|
  | `name` | string | yes | Display name, 1–64 characters. |
  | `expires_at` | string (nullable) | no | RFC 3339 expiry; must be in the future. |
  | `capabilities` | array | no | Admin capability strings; defaults applied if omitted/empty. |

  ```json
  {
    "name": "ops-admin-key",
    "capabilities": ["admin:read:topology", "admin:read:peers", "admin:read:metrics"]
  }
  ```

- **Response `201`:** the created admin key as a JSON object with its one-time
  `key`.

  | Field | Type | Description |
  |---|---|---|
  | `id` | string | Key UUID. |
  | `admin_id` | string | Owning admin ID. |
  | `name` | string | Display name. |
  | `key` | string | Plaintext key (`ap_mcp_admin_<hex>`) — shown only here. |
  | `key_prefix` | string | Leading 20 chars of the key. |
  | `capabilities` | array | Granted (sanitized) capabilities. |
  | `expires_at` | string (nullable) | RFC 3339 expiry, or `null`. |
  | `last_used_at` | string (nullable) | `null` at creation. |
  | `created_at` | string | RFC 3339 creation time. |

  ```json
  {
    "id": "aaaa1111-2222-3333-4444-555555555555",
    "admin_id": "9f1c0d2e-3a4b-5c6d-7e8f-90a1b2c3d4e5",
    "name": "ops-admin-key",
    "key": "ap_mcp_admin_0a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f9",
    "key_prefix": "ap_mcp_admin_0a1b",
    "capabilities": ["admin:read:topology", "admin:read:peers", "admin:read:metrics"],
    "expires_at": null,
    "last_used_at": null,
    "created_at": "2026-06-07T12:00:00Z"
  }
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 400 | `invalid_body` | Request body is not valid JSON. |
  | 400 | `name_required` | `name` is empty. |
  | 400 | `name_too_long` | `name` exceeds 64 characters. |
  | 400 | `invalid_capability` | A requested capability is not in the admin allow-list. |
  | 400 | `invalid_expires_at` | `expires_at` is not RFC 3339. |
  | 400 | `expires_in_past` | `expires_at` is in the past. |
  | 422 | `limit_reached` | You already have 10 admin keys (the per-admin maximum). |
  | 500 | `db_error` | Failed to count or create the key. |
  | 500 | `rand_error` | Failed to generate the key bytes. |

- **Source:** `AdminMCPKeyHandler.CreateAdminKey` — `internal/handler/admin_mcpkey.go`

## `DELETE /api/v1/admin/mcp-keys/{id}`

Revoke one of the calling admin's own admin MCP keys.

- **Auth:** Bearer JWT (admin).
- **Path parameters:**

  | Name | Type | Description |
  |---|---|---|
  | `id` | string | Admin MCP key UUID to revoke. |

- **Query parameters:** None.
- **Request body:** None.
- **Response `200`:**

  ```json
  { "message": "Admin MCP key revoked" }
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 500 | `db_error` | Failed to revoke the admin key. |

- **Source:** `AdminMCPKeyHandler.DeleteAdminKey` — `internal/handler/admin_mcpkey.go`

## `GET /api/v1/admin/user-mcp-keys`

List all users' MCP keys platform-wide (metadata only), optionally filtered by
ASN.

- **Auth:** Bearer JWT (admin).
- **Path parameters:** None.
- **Query parameters:**

  | Name | Type | Required | Description |
  |---|---|:---:|---|
  | `asn` | integer | no | Filter to a single ASN; omit (or empty) to list all. |

- **Request body:** None.
- **Response `200`:** a bare JSON array of `MCPKey` objects (same fields as
  `GET /api/v1/user/mcp-keys`, across all ASNs).

  ```json
  [
    {
      "id": "11111111-2222-3333-4444-555555555555",
      "asn": 4242420000,
      "name": "my-assistant",
      "key_prefix": "ap_mcp_0a1b2c3d",
      "capabilities": ["read:nodes", "read:peers"],
      "expires_at": null,
      "last_used_at": "2026-06-07T09:30:00Z",
      "scope_version": 1,
      "created_at": "2026-06-01T12:00:00Z"
    }
  ]
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 400 | `invalid_asn` | `asn` query value is not a number. |
  | 500 | `db_error` | Failed to list user MCP keys. |

- **Source:** `AdminMCPKeyHandler.ListUserMCPKeys` — `internal/handler/admin_mcpkey.go`

## `DELETE /api/v1/admin/user-mcp-keys/{id}`

Force-revoke any user's MCP key (recorded in the admin audit log).

- **Auth:** Bearer JWT (admin).
- **Path parameters:**

  | Name | Type | Description |
  |---|---|---|
  | `id` | string | User MCP key UUID to force-revoke. |

- **Query parameters:** None.
- **Request body:** None.
- **Response `200`:**

  ```json
  { "message": "User MCP key revoked" }
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 404 | `not_found` | No user MCP key with that ID. |

- **Source:** `AdminMCPKeyHandler.ForceDeleteUserMCPKey` — `internal/handler/admin_mcpkey.go`

---

# MCP audit logs

## `GET /api/v1/user/mcp-audit-logs`

List recent MCP audit entries for your ASN (latest 200).

- **Auth:** Bearer JWT (user).
- **Path parameters:** None.
- **Query parameters:** None.
- **Request body:** None.
- **Response `200`:** a bare JSON array of audit entries.

  | Field | Type | Description |
  |---|---|---|
  | `id` | string | Audit entry UUID. |
  | `session_id` | string (nullable) | MCP session ID, omitted if absent. |
  | `asn` | integer (nullable) | ASN, omitted if absent. |
  | `admin_id` | string (nullable) | Admin ID, omitted for user calls. |
  | `tool_name` | string | Tool that was called. |
  | `args` | object (raw JSON) | Sanitized arguments, omitted if none. |
  | `result_ok` | boolean | Whether the call succeeded. |
  | `error_msg` | string (nullable) | Error message, omitted on success. |
  | `called_at` | string | RFC 3339 call time. |

  ```json
  [
    {
      "id": "audit-1111-2222-3333-4444-555555555555",
      "session_id": "11111111-2222-3333-4444-555555555555",
      "asn": 4242420000,
      "tool_name": "peer_list",
      "result_ok": true,
      "called_at": "2026-06-07T09:30:00Z"
    }
  ]
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 500 | `db_error` | Failed to list audit logs. |

- **Source:** `ListMCPAuditLogs` — `internal/handler/admin_mcpkey.go`

## `GET /api/v1/admin/mcp-audit-logs`

List platform-wide MCP audit entries, filterable and paginated.

- **Auth:** Bearer JWT (admin).
- **Path parameters:** None.
- **Query parameters:**

  | Name | Type | Required | Description |
  |---|---|:---:|---|
  | `asn` | integer | no | Filter to a single ASN. |
  | `tool` | string | no | Filter by tool name (substring search). |
  | `page` | integer | no | 1-based page number; default 1 (values < 1 ignored). |
  | `per_page` | integer | no | Page size 1–100; default 50 (out-of-range ignored). |

- **Request body:** None.
- **Response `200`:** a wrapper object with the entries plus echoed pagination.

  | Field | Type | Description |
  |---|---|---|
  | `logs` | array | Audit entries (same shape as the user endpoint). |
  | `page` | integer | Resolved page number. |
  | `per_page` | integer | Resolved page size. |

  ```json
  {
    "logs": [
      {
        "id": "audit-1111-2222-3333-4444-555555555555",
        "session_id": "11111111-2222-3333-4444-555555555555",
        "asn": 4242420000,
        "tool_name": "peer_list",
        "result_ok": true,
        "called_at": "2026-06-07T09:30:00Z"
      }
    ],
    "page": 1,
    "per_page": 50
  }
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 400 | `invalid_asn` | `asn` query value is not a number. |
  | 500 | `db_error` | Failed to list audit logs. |

- **Source:** `AdminListMCPAuditLogs` — `internal/handler/admin_mcpkey.go`

---

# Assistant flow

A lighter-weight in-product assistant flow, authenticated with a normal **user
JWT** (not an MCP key) and restricted to a small set of **read-only** tools with
an explicit per-call approval handshake. Only these tools are callable through it:
`nodes_list`, `peer_creation_status`, `peer_list`, `peer_summary`, `peer_get`,
`peer_get_metrics`. Any other tool name is rejected with `403 tool_not_allowed`.

The flow is: authorize once (`GET .../assistant/auth`), request a short-lived
signed approval token for a specific tool + arguments
(`POST .../assistant/tools/approval`), then execute the approved call
(`POST .../assistant/tools/call`). Approval tokens are HMAC-SHA256 (keyed off the
server's `JWT_SECRET`) over a payload binding the ASN, conversation ID, tool name,
exact arguments, a nonce, and a 2-minute expiry; they are single-use. Every
assistant call is audited under the tool name `assistant.<tool_name>`.

## `GET /api/v1/user/assistant/auth`

Probe the current user session and echo the caller's identity.

- **Auth:** Bearer JWT (user).
- **Path parameters:** None.
- **Query parameters:** None.
- **Request body:** None.
- **Response `200`:**

  | Field | Type | Description |
  |---|---|---|
  | `ok` | boolean | Always `true` when reached (auth passed). |
  | `role` | string | Caller role (`user`). |
  | `asn` | integer | Caller ASN. |
  | `session_id` | string | Caller session ID. |

  ```json
  {
    "ok": true,
    "role": "user",
    "asn": 4242420000,
    "session_id": "11111111-2222-3333-4444-555555555555"
  }
  ```

- **Errors:** None raised by the handler (auth handled by the route's middleware,
  which returns `401 unauthorized` / `403 forbidden` for bad/absent tokens).
- **Source:** `AssistantAuthCheck` — `internal/handler/assistant_auth.go`

## `POST /api/v1/user/assistant/tools/approval`

Issue a short-lived, signed approval token for a specific read-only tool call.

- **Auth:** Bearer JWT (user).
- **Path parameters:** None.
- **Query parameters:** None.
- **Request body:** (`AssistantToolCallRequest`)

  | Field | Type | Required | Description |
  |---|---|:---:|---|
  | `tool_name` | string | yes | One of the six allowed read-only tools. |
  | `arguments` | object | no | Tool arguments; defaults to `{}`. Validated per tool. |
  | `conversation_id` | string | no | Conversation correlation; auto-generated (`assistant-<uuid>`) if empty. |
  | `approval_token` | string | no | Field exists in the struct but is ignored on this endpoint. |

  ```json
  {
    "tool_name": "peer_get",
    "arguments": { "id": "11111111-2222-3333-4444-555555555555" },
    "conversation_id": "conv-abc123"
  }
  ```

- **Response `201`:**

  | Field | Type | Description |
  |---|---|---|
  | `approval_token` | string | `<base64url-payload>.<base64url-hmac>` token. |
  | `expires_at` | integer | Unix timestamp (2 minutes out). |

  ```json
  {
    "approval_token": "eyJhc24iOjQyNDI0...z5Q.4f1b9c2d8e...",
    "expires_at": 1749294000
  }
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 401 | `unauthorized` | No authenticated ASN (`asn == 0`). |
  | 400 | `invalid_body` | Request body is not valid JSON. |
  | 403 | `tool_not_allowed` | `tool_name` is not one of the allowed read-only tools. |
  | 400 | `invalid_arguments` | Arguments fail per-tool validation. The `message` carries the specific reason (e.g. "Peer id is required", "hours must be between 1 and 720", or an unknown-field/invalid-argument message); the `error` code is always `invalid_arguments`. |
  | 500 | `approval_failed` | Failed to sign the approval token. |

- **Source:** `MCPHandler.HandleAssistantToolApproval` — `internal/handler/assistant.go`

## `POST /api/v1/user/assistant/tools/call`

Execute a previously approved read-only tool call.

- **Auth:** Bearer JWT (user).
- **Path parameters:** None.
- **Query parameters:** None.
- **Request body:** (`AssistantToolCallRequest`)

  | Field | Type | Required | Description |
  |---|---|:---:|---|
  | `tool_name` | string | yes | Must match the approved tool. |
  | `arguments` | object | no | Must match the approved arguments exactly (compared after JSON compaction); defaults to `{}`. |
  | `conversation_id` | string | no | Must match the approval's conversation ID. |
  | `approval_token` | string | yes | The token from the approval endpoint. |

  ```json
  {
    "tool_name": "peer_get",
    "arguments": { "id": "11111111-2222-3333-4444-555555555555" },
    "conversation_id": "conv-abc123",
    "approval_token": "eyJhc24iOjQyNDI0...z5Q.4f1b9c2d8e..."
  }
  ```

- **Response `200`:**

  | Field | Type | Description |
  |---|---|---|
  | `tool_name` | string | The executed tool. |
  | `conversation_id` | string | Conversation ID (generated if it was empty). |
  | `result` | object/array | The tool's JSON result. |

  ```json
  {
    "tool_name": "peer_get",
    "conversation_id": "conv-abc123",
    "result": {
      "id": "11111111-2222-3333-4444-555555555555",
      "node_id": "22222222-3333-4444-5555-666666666666",
      "remote_asn": 4242420000,
      "status": "active",
      "node_name": "node-01",
      "node_location": "example-location"
    }
  }
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 401 | `unauthorized` | No authenticated ASN (`asn == 0`). |
  | 400 | `invalid_body` | Request body is not valid JSON. |
  | 400 | `tool_required` | `tool_name` is empty. |
  | 403 | `tool_not_allowed` | `tool_name` is not one of the allowed read-only tools. |
  | 400 | `invalid_arguments` | Arguments fail per-tool validation (same validator as the approval endpoint; `message` carries the specific reason). |
  | 403 | `approval_invalid` | Approval token missing, malformed, bad HMAC, or expired. |
  | 403 | `approval_mismatch` | Token's bound ASN/tool/conversation/arguments do not match this call. |
  | 403 | `approval_reused` | Token's nonce has already been consumed. |
  | 400 | `invalid_arguments` | Tool execution returned a typed `assistantToolError` (in practice only re-decoding failures, since validation already ran). |
  | 422 | `tool_failed` | Tool execution failed for any other reason (e.g. `peer not found`). |

- **Source:** `MCPHandler.HandleAssistantToolCall` — `internal/handler/assistant.go`

---

## Quick start

```bash
# 1. Mint a user MCP key (JWT auth) — capture the one-time "key" field.
curl -sX POST https://your-center.example.com/api/v1/user/mcp-keys \
  -H "Authorization: Bearer <your-jwt>" \
  -H "Content-Type: application/json" \
  -d '{"name":"my-assistant","capabilities":["read:nodes","read:peers","read:metrics"]}'

# 2. Open the SSE stream with the MCP key; note the session_id from the
#    'endpoint' event.
curl -N https://your-center.example.com/api/v1/mcp \
  -H "Authorization: Bearer ap_mcp_xxxxxxxx..."

# 3. In a second request, POST JSON-RPC to the message endpoint. The response
#    arrives back over the SSE stream from step 2.
curl -sX POST "https://your-center.example.com/api/v1/mcp?session_id=<uuid>" \
  -H "Authorization: Bearer ap_mcp_xxxxxxxx..." \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
```

Most MCP clients implement the SSE + message transport for you; point the client
at `https://your-center.example.com/api/v1/mcp` (or `/api/v1/admin/mcp`) with the
appropriate `ap_mcp_` / `ap_mcp_admin_` bearer key.

## See also

- [../authentication.md](../authentication.md) — JWT sessions vs. MCP keys.
- [./peers.md](./peers.md) — peer lifecycle the user MCP tools operate on.
- [../websocket-protocol.md](../websocket-protocol.md) — streaming transport conventions.
- [../configuration.md](../configuration.md) — `JWT_SECRET` and other settings.
