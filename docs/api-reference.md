# API Reference

This is the complete REST/WebSocket reference for the AutoPeer **center** control
plane (`github.com/akaere/autopeer-center`, binary `center`). The AutoPeer
frontend is not open-sourced, so this document is the authoritative API contract.

All HTTP routes are mounted by `cmd/center/routes.go`. Unless stated otherwise,
every path below is relative to your deployment origin, e.g.
`https://your-center.example.com`. WebSocket endpoints use the `wss://` scheme
(e.g. `wss://your-center.example.com`).

Related docs:

- [Authentication](./authentication.md) — sessions, JWTs, MCP keys, device flow, agent/bot tokens.
- [WebSocket protocol](./websocket-protocol.md) — agent/bot message frames and handshakes.

---

## Conventions

### Versioning

- The URL prefix `/api/v1` is the only versioned URL prefix in the OSS build.
- Response payloads additionally use **Stripe-style header versioning** via the
  `Autopeer-Version` header. This is orthogonal to the URL `/v1`. Clients pin a
  dated version (`YYYY-MM-DD`); an absent or empty header resolves to the latest
  version. The middleware echoes the resolved version back in the
  `Autopeer-Version` response header and returns `400 invalid_api_version` for an
  unknown version. Only handlers that opt in (peer list/get endpoints today)
  transform their output; everything else is unaffected. Error bodies are never
  versioned.

### Common response headers

| Header | Direction | Description |
|---|---|---|
| `X-Request-ID` | response | Per-request correlation ID. Set on every response; mirrored in the error body as `request_id`. |
| `Autopeer-Version` | request / response | Requested (in) and resolved (out) API version. Set on every `/api/v1` response. |

CORS exposes both headers via `Access-Control-Expose-Headers`. The global body
limit for request payloads is 1 MiB.

### Standard error body

All handler errors share one JSON shape:

```json
{
  "error": "bad_request",
  "message": "Invalid JSON body",
  "request_id": "a1b2c3d4-..."
}
```

- `error` — a stable machine-readable error code (e.g. `bad_request`,
  `unauthorized`, `forbidden`, `peer_not_found`, `rate_limited`,
  `operation_in_progress`, `invalid_api_version`).
- `message` — a human-readable description.
- `request_id` — the value of `X-Request-ID` for this response.

The router itself returns the same envelope for unmatched paths and methods:

```json
{"error":"not_found","message":"The requested resource does not exist"}
```
```json
{"error":"method_not_allowed","message":"Method not allowed"}
```

### Auth legend

| Auth | How it is supplied | Meaning |
|---|---|---|
| **public** | none | No authentication. |
| **RequireUser** | `Authorization: Bearer <user JWT>` (+ session) | A valid **user** session (DN42 ASN identity). |
| **RequireAdmin** | `Authorization: Bearer <admin JWT>` (+ session) | A valid **admin** session. |
| **RequireAny** | `Authorization: Bearer <user or admin JWT>` (+ session) | Any authenticated session (user or admin). |
| **MCPKey** | `Authorization: Bearer ap_mcp_...` | A user MCP API key (prefix `ap_mcp_`). JWTs are rejected on these routes. |
| **AdminMCPKey** | `Authorization: Bearer ap_mcp_admin_...` | An admin MCP API key (prefix `ap_mcp_admin_`). |
| **agent-token** | `X-Agent-Token` header (or `X-Node-ID` for key-auth) | A node agent identifying itself. |
| **bot-token** | in-band `bot.auth` frame after WS upgrade | The shared bot token, sent over the WebSocket after the origin-checked handshake. |

JWTs are HS256 (`JWT_SECRET`). See [Authentication](./authentication.md) for
session/refresh/impersonation/device-code details. MCP keys cannot be used to
manage MCP keys — those routes require a JWT session.

---

## Health / utility

| Method | Path | Auth | Purpose |
|---|---|---|---|
| GET | `/health` | public | Liveness probe; returns `{"status":"ok"}` (JSON). |
| GET | `/healthz` | public | Liveness probe; returns `OK` (plain text). |

Unmatched routes return `404 not_found`; unsupported methods return
`405 method_not_allowed` (see error envelopes above).

---

## Public (`/api/v1`, no auth)

| Method | Path | Auth | Purpose |
|---|---|---|---|
| GET | `/api/v1/nodes` | public | List public-facing nodes (peering locations). |
| GET | `/api/v1/registry/asn/{asn}` | public | Look up DN42 registry data for an ASN (e.g. `4242420000`). |
| GET | `/api/v1/stats` | public | Public service statistics. |
| GET | `/api/v1/user/peers/creation-status` | public | Whether peer self-service creation is currently enabled (`{"enabled":bool}`). |
| GET | `/api/v1/auth/user/login-status` | public | Whether user login is currently enabled. |
| GET | `/api/v1/auth/turnstile-config` | public | Cloudflare Turnstile site key/config for the login forms. |

---

## Auth — public flows (`/api/v1/auth`, no auth)

These bootstrap a session. Most are rate-limited per-IP and per-ASN, and the
email/password/GPG login flows require a valid Turnstile token when configured.

| Method | Path | Auth | Purpose |
|---|---|---|---|
| POST | `/api/v1/auth/user/request-code` | public | Send an email verification code to an ASN's registry contact. Body: `{asn, cf_turnstile_response}`. |
| POST | `/api/v1/auth/user/verify-code` | public | Exchange `{asn, code}` for a user session (returns access token + sets refresh cookie). |
| POST | `/api/v1/auth/user/request-gpg-challenge` | public | Issue a GPG challenge for an ASN whose mntner has a PGP key. Body: `{asn, cf_turnstile_response}`. |
| POST | `/api/v1/auth/user/verify-gpg` | public | Verify a clearsigned/detached GPG challenge and start a user session. Body: `{asn, challenge_id, signed_message, public_key?}`. |
| POST | `/api/v1/auth/user/check-gpg-availability` | public | Report whether GPG login is available for an ASN (`{available:bool}`). |
| POST | `/api/v1/auth/admin/login` | public | Admin email/password login. Body: `{email, password, cf_turnstile_response}`. |
| POST | `/api/v1/auth/user/passkey/begin` | public | Begin a passkey (WebAuthn) assertion/login. |
| POST | `/api/v1/auth/user/passkey/finish` | public | Finish a passkey assertion and start a session. |
| POST | `/api/v1/auth/refresh` | public (refresh cookie) | Rotate the refresh token (HttpOnly cookie scoped to `/api/v1/auth`) and mint a new access token. |
| POST | `/api/v1/auth/device/code` | public | OAuth-style device-authorization grant: returns `device_code`, `user_code`, `verification_uri`, `interval`. |
| POST | `/api/v1/auth/device/token` | public | Poll for the device grant result; on `approved` returns a session. Grant type `urn:ietf:params:oauth:grant-type:device_code`. |

A successful login response (`authResponse`) looks like:

```json
{
  "token": "<jwt>",
  "access_token": "<jwt>",
  "expires_in": 3600,
  "refresh_expires_in": 2592000,
  "asn": 4242420000,
  "session": { "...": "AuthSession" },
  "user": { "role": "user", "asn": 4242420000, "email": "admin@example.com" }
}
```

---

## Auth — any authenticated (`/api/v1/auth`)

| Method | Path | Auth | Purpose |
|---|---|---|---|
| POST | `/api/v1/auth/logout` | RequireAny | Revoke the current session and clear the refresh cookie. |
| GET | `/api/v1/auth/device/request` | RequireAny | Look up a pending device grant by `user_code` (for the approval screen). |
| POST | `/api/v1/auth/device/authorize` | RequireAny | Approve/deny a device grant. Body: `{user_code, decision}` (`approve`/`deny`). Impersonation sessions cannot authorize devices. |

---

## User — peers & account (`/api/v1/user`, RequireUser)

| Method | Path | Auth | Purpose |
|---|---|---|---|
| GET | `/api/v1/user/peers` | RequireUser | List the caller's peers (scoped to their ASN). Versioned response. |
| POST | `/api/v1/user/peers` | RequireUser | Create a peer (status `pending`, or `active` if auto-approve is on). Body: `{node_id, remote_pubkey, remote_endpoint, remote_lla, mtu?, enable_psk?}`. |
| GET | `/api/v1/user/peers/summary` | RequireUser | Latest per-peer metric summary (RTT, BGP state, handshake, BGP uptime). |
| GET | `/api/v1/user/peers/{id}` | RequireUser | Get one of the caller's peers (with node details). Versioned response. |
| GET | `/api/v1/user/peers/{id}/metrics` | RequireUser | Time-series metrics for a peer. Query: `hours` (1–720, default 24). |
| PUT | `/api/v1/user/peers/{id}` | RequireUser | Update endpoint/pubkey/LLA/MTU of a `pending` or `active` peer (reconfigures the agent for active peers). |
| DELETE | `/api/v1/user/peers/{id}` | RequireUser | Delete a peer; tears down the agent config first for `active` peers. |
| POST | `/api/v1/user/looking-glass/run` | RequireUser | Run a looking-glass query against a node. |
| GET | `/api/v1/user/audit` | RequireUser | List the caller's own audit-log entries. |

Peer request validation: `remote_pubkey` must be Base64 and ≥40 chars;
`remote_endpoint` must be `IP:Port` or `[IPv6]:Port`; `remote_lla` must be a
link-local address (`fe80::/10`, e.g. `fe80::1`); `mtu` (if set) must be
576–9000.

A peer object (`peerResp`) carries: `id`, `node_id`, `remote_asn`,
`remote_pubkey`, `remote_endpoint`, `remote_lla`, `contact_email`,
`wg_listen_port`, `wg_interface_name`, `wg_managed`, `status`, `reject_reason?`,
`endpoint_mismatch_since?`, `bgp_suspended_by_endpoint`, `created_at`,
`updated_at`, `node_name`, `node_location`, `mtu?`, `wg_preshared_key?` (and on
the single-peer view also `node_public_ip`, `node_our_lla`, `node_our_wg_pubkey`).

### Account — devices

| Method | Path | Auth | Purpose |
|---|---|---|---|
| GET | `/api/v1/user/devices` | RequireUser | List the caller's active sessions/devices. |
| DELETE | `/api/v1/user/devices` | RequireUser | Revoke all of the caller's other sessions. |
| DELETE | `/api/v1/user/devices/{id}` | RequireUser | Revoke a specific session of the caller. |

---

## User — notification & email preferences (`/api/v1/user`, RequireUser)

| Method | Path | Auth | Purpose |
|---|---|---|---|
| GET | `/api/v1/user/email-preferences` | RequireUser | Get the caller's email-delivery preferences. |
| PUT | `/api/v1/user/email-preferences` | RequireUser | Update email-delivery preferences. |
| GET | `/api/v1/user/notification-preferences` | RequireUser | Get per-event notification preferences. |
| PUT | `/api/v1/user/notification-preferences` | RequireUser | Update per-event notification preferences. |

---

## User — Telegram binding (`/api/v1/user/telegram`, RequireUser)

| Method | Path | Auth | Purpose |
|---|---|---|---|
| GET | `/api/v1/user/telegram/binding` | RequireUser | Get the caller's Telegram binding (if any). |
| POST | `/api/v1/user/telegram/bind-token` | RequireUser | Create a one-time token the user redeems in the Telegram bot to bind their ASN. |
| DELETE | `/api/v1/user/telegram/binding` | RequireUser | Remove the Telegram binding. |
| GET | `/api/v1/user/telegram/notification-preferences` | RequireUser | Get Telegram notification preferences. |
| PUT | `/api/v1/user/telegram/notification-preferences` | RequireUser | Update Telegram notification preferences. |

---

## User — MCP keys / assistant (`/api/v1/user`, RequireUser)

MCP-key management itself requires a **JWT session** — you cannot manage MCP keys
with an MCP key.

| Method | Path | Auth | Purpose |
|---|---|---|---|
| GET | `/api/v1/user/mcp-keys` | RequireUser | List the caller's MCP API keys. |
| POST | `/api/v1/user/mcp-keys` | RequireUser | Create an MCP API key (`ap_mcp_...`, returned once). |
| DELETE | `/api/v1/user/mcp-keys/{id}` | RequireUser | Revoke an MCP API key. |
| GET | `/api/v1/user/mcp-audit-logs` | RequireUser | The caller's MCP audit log (own ASN only). |
| GET | `/api/v1/user/assistant/auth` | RequireUser | Assistant authentication check. |
| POST | `/api/v1/user/assistant/tools/approval` | RequireUser | Approve/deny an assistant tool invocation. |
| POST | `/api/v1/user/assistant/tools/call` | RequireUser | Execute an assistant tool call. |

---

## User — Passkey / WebAuthn (`/api/v1`, RequireUser)

Login/assertion is in the [public auth flows](#auth--public-flows-apiv1auth-no-auth);
the routes below register and manage credentials for an already-authenticated user.

| Method | Path | Auth | Purpose |
|---|---|---|---|
| GET | `/api/v1/passkey/status` | RequireUser | Whether the caller has any registered passkeys. |
| POST | `/api/v1/passkey/register/begin` | RequireUser | Begin WebAuthn credential registration (returns creation options). |
| POST | `/api/v1/passkey/register/finish` | RequireUser | Complete WebAuthn credential registration. |
| GET | `/api/v1/user/passkeys` | RequireUser | List the caller's registered passkeys. |
| DELETE | `/api/v1/user/passkeys/{id}` | RequireUser | Delete a registered passkey. |

---

## MCP transport (`/api/v1/mcp`, MCPKey)

Model Context Protocol endpoints for the **user** assistant integration. The
`Authorization: Bearer ap_mcp_...` key is required; JWTs are rejected here.

| Method | Path | Auth | Purpose |
|---|---|---|---|
| GET | `/api/v1/mcp` | MCPKey | Open the MCP SSE stream (server→client events). |
| POST | `/api/v1/mcp` | MCPKey | Send an MCP JSON-RPC message (client→server). |

---

## Admin MCP transport (`/api/v1/admin/mcp`, AdminMCPKey)

| Method | Path | Auth | Purpose |
|---|---|---|---|
| GET | `/api/v1/admin/mcp` | AdminMCPKey | Open the admin MCP SSE stream. Requires `ap_mcp_admin_...`. |
| POST | `/api/v1/admin/mcp` | AdminMCPKey | Send an admin MCP JSON-RPC message. |

---

## Admin — peers (`/api/v1/admin/peers`, RequireAdmin)

| Method | Path | Auth | Purpose |
|---|---|---|---|
| GET | `/api/v1/admin/peers` | RequireAdmin | List all peers (filterable). Versioned response. |
| GET | `/api/v1/admin/peers/{id}` | RequireAdmin | Get a peer by ID. Versioned response. |
| GET | `/api/v1/admin/peers/{id}/metrics` | RequireAdmin | Time-series metrics for any peer. |
| POST | `/api/v1/admin/peers/{id}/approve` | RequireAdmin | Approve a pending peer (pushes `peer.add` to the node agent; status → `active`). |
| POST | `/api/v1/admin/peers/{id}/reject` | RequireAdmin | Reject a pending peer with a reason (status → `rejected`). |
| POST | `/api/v1/admin/peers/{id}/suspend` | RequireAdmin | Suspend an active peer (status → `suspended`). |
| POST | `/api/v1/admin/peers/{id}/unsuspend` | RequireAdmin | Re-activate a suspended peer. |
| PUT | `/api/v1/admin/peers/{id}` | RequireAdmin | Update peer fields as admin. |
| PUT | `/api/v1/admin/peers/{id}/contact-email` | RequireAdmin | Override the peer's contact email. |
| DELETE | `/api/v1/admin/peers/{id}` | RequireAdmin | Hard-delete a peer (tears down agent config if active). |

---

## Admin — nodes (`/api/v1/admin/nodes`, RequireAdmin)

| Method | Path | Auth | Purpose |
|---|---|---|---|
| GET | `/api/v1/admin/nodes` | RequireAdmin | List all nodes (with agent/BIRD status). |
| POST | `/api/v1/admin/nodes` | RequireAdmin | Create a node. |
| PUT | `/api/v1/admin/nodes/{id}` | RequireAdmin | Update a node. |
| DELETE | `/api/v1/admin/nodes/{id}` | RequireAdmin | Delete a node. |
| POST | `/api/v1/admin/nodes/{id}/import` | RequireAdmin | Import existing peers from the node agent. |
| POST | `/api/v1/admin/nodes/{id}/bird-refresh` | RequireAdmin | Refresh BIRD/BGP details from the agent. |
| POST | `/api/v1/admin/nodes/{id}/update` | RequireAdmin | Push an agent binary update to the node. |
| POST | `/api/v1/admin/nodes/{id}/rollback` | RequireAdmin | Roll the node agent back to the previous binary. |
| POST | `/api/v1/admin/nodes/{id}/regenerate-token` | RequireAdmin | Rotate the node's `X-Agent-Token`. |
| POST | `/api/v1/admin/nodes/{id}/reset-pubkey` | RequireAdmin | Reset the node's stored agent public key (re-key the ECDH handshake). |

---

## Admin — releases (`/api/v1/admin/releases`, RequireAdmin)

| Method | Path | Auth | Purpose |
|---|---|---|---|
| GET | `/api/v1/admin/releases` | RequireAdmin | List available agent release binaries. |
| POST | `/api/v1/admin/releases` | RequireAdmin | Upload a new agent release. |
| DELETE | `/api/v1/admin/releases/{version}` | RequireAdmin | Delete a release version. |

---

## Admin — settings / notifications / audit / stats (RequireAdmin)

| Method | Path | Auth | Purpose |
|---|---|---|---|
| GET | `/api/v1/admin/notifications` | RequireAdmin | Get notification settings. |
| PUT | `/api/v1/admin/notifications` | RequireAdmin | Update a notification setting. |
| GET | `/api/v1/admin/audit` | RequireAdmin | List audit-log entries (all operators). |
| GET | `/api/v1/admin/stats` | RequireAdmin | Admin dashboard statistics. |
| GET | `/api/v1/admin/settings` | RequireAdmin | Get site settings (e.g. `peer_creation_enabled`, `auto_approve_peers`, `user_login_enabled`). |
| PUT | `/api/v1/admin/settings` | RequireAdmin | Update a single site setting. |
| POST | `/api/v1/admin/test/email` | RequireAdmin | Send a test email through the configured email API. |

---

## Admin — bot management (`/api/v1/admin/bot`, RequireAdmin)

| Method | Path | Auth | Purpose |
|---|---|---|---|
| GET | `/api/v1/admin/bot/settings` | RequireAdmin | Get Telegram bot settings. |
| PUT | `/api/v1/admin/bot/settings` | RequireAdmin | Update a bot setting. |
| POST | `/api/v1/admin/bot/token/reset` | RequireAdmin | Rotate the shared bot token used for the bot WebSocket. |
| GET | `/api/v1/admin/bot/stats` | RequireAdmin | Bot usage statistics. |
| GET | `/api/v1/admin/bot/commands` | RequireAdmin | Recent bot commands. |
| GET | `/api/v1/admin/bot/blocked` | RequireAdmin | List blocked bot users. |
| POST | `/api/v1/admin/bot/blocked` | RequireAdmin | Block a bot user. |
| DELETE | `/api/v1/admin/bot/blocked/{id}` | RequireAdmin | Unblock a bot user. |
| GET | `/api/v1/admin/bot/export` | RequireAdmin | Export bot settings. |

---

## Admin — auth / impersonation / devices (RequireAdmin)

| Method | Path | Auth | Purpose |
|---|---|---|---|
| POST | `/api/v1/admin/auth/login-as` | RequireAdmin | Impersonate a user (mint a short-lived impersonation session). |
| GET | `/api/v1/admin/auth/devices` | RequireAdmin | List the admin's active sessions/devices. |
| DELETE | `/api/v1/admin/auth/devices/{id}` | RequireAdmin | Revoke a specific admin session. |

---

## Admin — MCP key management (RequireAdmin)

| Method | Path | Auth | Purpose |
|---|---|---|---|
| GET | `/api/v1/admin/mcp-keys` | RequireAdmin | List the admin's own admin MCP keys. |
| POST | `/api/v1/admin/mcp-keys` | RequireAdmin | Create an admin MCP key (`ap_mcp_admin_...`, returned once). |
| DELETE | `/api/v1/admin/mcp-keys/{id}` | RequireAdmin | Revoke an admin MCP key. |
| GET | `/api/v1/admin/user-mcp-keys` | RequireAdmin | List all users' MCP keys. |
| DELETE | `/api/v1/admin/user-mcp-keys/{id}` | RequireAdmin | Force-delete any user's MCP key. |
| GET | `/api/v1/admin/mcp-audit-logs` | RequireAdmin | MCP audit log across all ASNs. |

---

## Admin — system status (RequireAdmin)

| Method | Path | Auth | Purpose |
|---|---|---|---|
| GET | `/api/v1/admin/system-status` | RequireAdmin | Service health/system status snapshot. |
| GET | `/api/v1/admin/system-status/db-tables` | RequireAdmin | Per-table database statistics. |
| POST | `/api/v1/admin/system-status/rotate-tables` | RequireAdmin | Rotate/compact time-series tables. |

---

## Admin — queue monitor (`/api/v1/admin/queue`, RequireAdmin)

Asynq (Redis-backed) queue monitoring. Read endpoints are always available;
**mutating endpoints are only registered when `ASYNQ_READONLY_MONITOR=false`.**
When the monitor is read-only, the mutating routes are absent and respond with
`404 not_found`.

| Method | Path | Auth | Purpose |
|---|---|---|---|
| GET | `/api/v1/admin/queue/overview` | RequireAdmin | Aggregate queue overview. |
| GET | `/api/v1/admin/queue/queues` | RequireAdmin | List queues. |
| GET | `/api/v1/admin/queue/queues/{queueName}` | RequireAdmin | Get one queue's stats. |
| GET | `/api/v1/admin/queue/queues/{queueName}/tasks` | RequireAdmin | List tasks in a queue (by state). |
| GET | `/api/v1/admin/queue/queues/{queueName}/tasks/{taskID}` | RequireAdmin | Get a single task. |
| GET | `/api/v1/admin/queue/servers` | RequireAdmin | List worker servers. |
| GET | `/api/v1/admin/queue/scheduler` | RequireAdmin | List scheduler entries. |
| GET | `/api/v1/admin/queue/scheduler/{entryID}/events` | RequireAdmin | List enqueue events for a scheduler entry. |
| GET | `/api/v1/admin/queue/history/{queueName}` | RequireAdmin | Historical stats for a queue. |
| DELETE | `/api/v1/admin/queue/queues/{queueName}/tasks/{taskID}` | RequireAdmin | Delete a task. *(only when `ASYNQ_READONLY_MONITOR=false`)* |
| POST | `/api/v1/admin/queue/queues/{queueName}/tasks/{taskID}:run` | RequireAdmin | Run a task now. *(only when `ASYNQ_READONLY_MONITOR=false`)* |
| POST | `/api/v1/admin/queue/queues/{queueName}/tasks/{taskID}:archive` | RequireAdmin | Archive a task. *(only when `ASYNQ_READONLY_MONITOR=false`)* |
| DELETE | `/api/v1/admin/queue/queues/{queueName}/tasks:delete_all` | RequireAdmin | Delete all tasks in a state. *(only when `ASYNQ_READONLY_MONITOR=false`)* |
| POST | `/api/v1/admin/queue/queues/{queueName}:pause` | RequireAdmin | Pause a queue. *(only when `ASYNQ_READONLY_MONITOR=false`)* |
| POST | `/api/v1/admin/queue/queues/{queueName}:resume` | RequireAdmin | Resume a queue. *(only when `ASYNQ_READONLY_MONITOR=false`)* |
| POST | `/api/v1/admin/queue/active-tasks/{taskID}:cancel` | RequireAdmin | Cancel a running task. *(only when `ASYNQ_READONLY_MONITOR=false`)* |

---

## WebSockets & agent download (`/api/v1`)

| Method | Path | Auth | Purpose |
|---|---|---|---|
| GET | `/api/v1/agent/ws` | agent-token | Node agent WebSocket. The agent presents `X-Agent-Token` (or `X-Node-ID` for key-auth), then performs the ECDH X25519 handshake before being trusted. See [WebSocket protocol](./websocket-protocol.md). |
| GET | `/api/v1/agent/download` | agent-token | Download an agent release binary. Headers: `X-Agent-Token` (or `X-Node-ID`). Query: `version` (required), `os` (default `linux`), `arch` (default `amd64`). |
| GET | `/api/v1/bot/ws` | bot-token | Telegram bot WebSocket. Origin-checked at upgrade, then the bot authenticates in-band with a `bot.auth` frame carrying the shared bot token before any other frame is processed. See [WebSocket protocol](./websocket-protocol.md). |

The agent WebSocket carries `peer.add` / `peer.remove`, `heartbeat`,
`peers.sync`, `agent.update` / `agent.rollback`, and diagnostic frames
(`bird.details`, `peers.import`, `status.request`). Commands are
request/response with a 30-second timeout, correlated by a UUID `id`. Full frame
definitions are in [WebSocket protocol](./websocket-protocol.md).
