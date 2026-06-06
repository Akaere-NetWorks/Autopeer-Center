# MCP (Model Context Protocol) Integration

AutoPeer Center exposes a [Model Context Protocol](https://modelcontextprotocol.io/)
server so that AI assistants and agentic tooling can inspect and operate on the
peering platform through a well-defined, capability-scoped tool surface.

There are two independent MCP servers:

| Server | Endpoint | Authentication | Audience |
|---|---|---|---|
| User MCP | `/api/v1/mcp` | User MCP key (`ap_mcp_` prefix) | Tools scoped to a single ASN |
| Admin MCP | `/api/v1/admin/mcp` | Admin MCP key (`ap_mcp_admin_` prefix) | Read-only tools across the whole platform |

Both servers speak JSON-RPC 2.0 over an SSE (Server-Sent Events) transport and
share the same capability and audit-logging model. They are authenticated by
dedicated MCP API keys, **not** by JWTs — see [Authentication](./authentication.md)
for the platform's session/JWT model and how it differs from MCP keys.

For the full HTTP surface (request/response shapes for the key-management and
assistant endpoints referenced below) see the [API Reference](./api-reference.md).

---

## Transport

The transport is the standard MCP "HTTP + SSE" pattern, split across two HTTP
methods on the same path:

1. **Open the SSE stream** — `GET /api/v1/mcp` (or `GET /api/v1/admin/mcp`) with
   the MCP key in the `Authorization: Bearer <key>` header.
   - The server replies with `Content-Type: text/event-stream` and immediately
     emits an `endpoint` event whose data is the message URL to POST to,
     including a generated `session_id`:

     ```
     event: endpoint
     data: /api/v1/mcp?session_id=<uuid>
     ```

   - The stream then stays open. A `: keepalive` comment is sent every 25 seconds
     so proxies do not idle out the connection. All JSON-RPC responses are
     delivered back to the client as `event: message` SSE frames.

2. **Send JSON-RPC requests** — `POST /api/v1/mcp?session_id=<uuid>` (same
   `session_id` from the `endpoint` event), again with the `Authorization`
   header. The request body is a single JSON-RPC 2.0 request object. The POST
   returns `202 Accepted` immediately; the actual JSON-RPC **response is written
   back over the SSE stream**, correlated by the request `id`.

The session is bound to the authenticated principal: a POST whose `session_id`
does not belong to the same ASN (user server) or admin (admin server) as the
caller is rejected with `404`.

### Concurrency and timeouts

- Each session allows at most **5 concurrent in-flight requests**; exceeding
  this returns `429 Too Many Requests` on the POST.
- Each dispatched JSON-RPC call has a **30 second** processing timeout.
- The SSE outbound buffer holds 64 messages; if a client cannot keep up, the
  oldest messages are dropped.

### JSON-RPC methods

Both servers support the following methods:

| Method | Description |
|---|---|
| `initialize` | Returns `protocolVersion` `2024-11-05`, `serverInfo` (`AutoPeer MCP` / `AutoPeer Admin MCP`, version `1.0.0`), and a `capabilities.tools` block. |
| `tools/list` | Lists the tools available to **this key's capabilities** (tools whose required capability is not granted are omitted). |
| `tools/call` | Invokes a tool by `name` with `arguments`. |

Standard JSON-RPC error codes are used (`-32700` parse error, `-32601` method/tool
not found, `-32602` invalid params, `-32003` forbidden). Write tools add a few
domain-specific codes — see [Idempotency](#idempotency-user-write-tools).

A `tools/call` result follows the MCP content convention: the tool's JSON output
is serialized and returned as a single `text` content block.

---

## API keys

MCP keys are bearer tokens, distinguished by a fixed prefix:

| Key type | Prefix | Stored prefix length | Used for |
|---|---|---|---|
| User MCP key | `ap_mcp_` | first 16 chars | `/api/v1/mcp` |
| Admin MCP key | `ap_mcp_admin_` | first 20 chars | `/api/v1/admin/mcp` |

A key is `ap_mcp_` (or `ap_mcp_admin_`) followed by 32 random bytes, hex-encoded.
Only a SHA-256 hash of the full key is stored server-side; the plaintext is
returned **once**, at creation time, and cannot be retrieved again. Each key
also stores a short `key_prefix` (the leading characters) for display/lookup.

### Prefix enforcement

The authentication middleware is strict about prefixes so the two surfaces can
never be confused:

- `/api/v1/mcp` accepts **only** `ap_mcp_` keys and explicitly **rejects**
  `ap_mcp_admin_` keys (an admin key is not a valid user key).
- `/api/v1/admin/mcp` accepts **only** `ap_mcp_admin_` keys.
- A JWT in the `Authorization` header is **not** accepted on the MCP endpoints —
  MCP keys are the only credential.

On a successful user-key auth, the request context is populated with the key's
ASN and capabilities; on a successful admin-key auth, with the admin ID and
capabilities. Keys that are expired (`expires_at` in the past) or revoked
(`revoked_at` set) are rejected with `401`. Each successful use asynchronously
updates the key's `last_used_at` timestamp.

### Creating and managing keys

Key management is done over the regular authenticated HTTP API using a **JWT**
(you cannot use an MCP key to mint or revoke MCP keys). See
[Authentication](./authentication.md) for obtaining a session token.

**User MCP keys** (scoped to the caller's ASN):

| Method & path | Description |
|---|---|
| `GET /api/v1/user/mcp-keys` | List your MCP keys (metadata only). |
| `POST /api/v1/user/mcp-keys` | Create a key. Returns the plaintext `key` once. |
| `DELETE /api/v1/user/mcp-keys/{id}` | Revoke one of your keys. |

`POST` body:

```json
{
  "name": "my-assistant",
  "expires_at": "2027-01-01T00:00:00Z",
  "capabilities": ["read:nodes", "read:peers"]
}
```

- `name` is required and limited to 64 characters.
- `expires_at` is optional (RFC 3339) and must be in the future.
- `capabilities` is optional; if omitted, a default set is granted. Each entry
  is validated against the allow-list (an unknown capability is a `400`).
- A maximum of **10** keys per ASN is enforced.

**Admin MCP keys** (scoped to the creating admin):

| Method & path | Description |
|---|---|
| `GET /api/v1/admin/mcp-keys` | List your admin MCP keys. |
| `POST /api/v1/admin/mcp-keys` | Create an admin MCP key. Returns plaintext once. |
| `DELETE /api/v1/admin/mcp-keys/{id}` | Revoke one of your admin MCP keys. |

The request body is identical in shape to user keys; capabilities are validated
against the **admin** allow-list and a maximum of **10** admin keys per admin
applies.

Admins can also oversee user keys platform-wide:

| Method & path | Description |
|---|---|
| `GET /api/v1/admin/user-mcp-keys?asn=<asn>` | List all user MCP keys (optionally filtered by ASN). |
| `DELETE /api/v1/admin/user-mcp-keys/{id}` | Force-revoke any user's MCP key (recorded in the admin audit log). |

---

## Capabilities

Every tool declares a required **capability** string. A key grants a set of
capabilities; `tools/list` shows only the tools the key can use, and `tools/call`
returns `forbidden` (`-32003`) for any tool whose capability the key lacks (the
forbidden attempt is still audited).

If a key is created with no explicit capabilities, a default set is applied.
Requested capabilities are sanitized against the allow-list: unknown or duplicate
entries are dropped, and if nothing valid remains, the defaults are used.

### User capabilities

| Capability | Default? | Grants |
|---|:---:|---|
| `read:nodes` | yes | List peering nodes you can peer with. |
| `read:peers` | yes | List/inspect your peers, operations, and creation status. |
| `read:metrics` | yes | Read peer summaries and time-series metrics. |
| `read:audit` | | Read your ASN's MCP audit logs. |
| `read:preferences` | | Read your preferences. |
| `write:peer:create` | | Create a pending peer. |
| `write:peer:update_pending` | | Update a pending/rejected peer. |
| `write:peer:cancel_pending` | | Cancel a pending/rejected peer. |
| `write:operation:create` | | Request admin review for changes to active peers. |
| `write:preferences` | | Write your preferences. |

### Admin capabilities

| Capability | Default? | Grants |
|---|:---:|---|
| `admin:read:topology` | yes | List nodes and node statistics. |
| `admin:read:peers` | yes | List/inspect peers across all ASNs; list ASNs. |
| `admin:read:metrics` | yes | Read peer time-series metrics. |
| `admin:read:audit` | yes | Read platform-wide MCP audit logs. |
| `admin:read:mcp_keys` | yes | List user MCP keys metadata and MCP sessions. |
| `admin:read:settings` | yes | Read site, notification, and bot settings metadata. |
| `admin:read:system` | | Read system stats, releases, and bot stats/blocked users. |
| `admin:read:sensitive` | | Read sensitive peer fields (endpoint, email, keys, addresses). |

All admin MCP tools are **read-only**; there are no admin write tools.

---

## User tools

Available under `/api/v1/mcp`. Read tools have no side effects. Write tools
mutate state and require an idempotency key (see below).

### Read tools

| Tool | Capability | Description |
|---|---|---|
| `nodes_list` | `read:nodes` | List enabled peering nodes you can peer with. |
| `peer_creation_status` | `read:peers` | Whether new peer creation is currently enabled. |
| `peer_list` | `read:peers` | List all peers belonging to your ASN. |
| `peer_summary` | `read:metrics` | Per-peer latest RTT, BGP state, handshake time. |
| `peer_get` | `read:peers` | Full details of one of your peers (with node info). |
| `peer_get_metrics` | `read:metrics` | Time-series metrics for a peer (`hours` 1–720, default 24). |
| `mcp_audit_logs_list` | `read:audit` | Recent MCP audit logs for your ASN. |
| `operations_list` | `read:peers` | Pending MCP operations for your ASN. |
| `operation_get` | `read:peers` | Get one pending operation by ID. |

### Write tools

| Tool | Capability | Description |
|---|---|---|
| `peer_create` | `write:peer:create` | Create a peer in `pending` status awaiting approval. |
| `peer_update_pending` | `write:peer:update_pending` | Update WireGuard params of a pending/rejected peer. |
| `peer_cancel_pending` | `write:peer:cancel_pending` | Cancel (delete) a pending/rejected peer. |
| `peer_update_operation_create` | `write:operation:create` | Request admin review to change an active/suspended peer. |
| `peer_delete_operation_create` | `write:operation:create` | Request admin review to delete an active/suspended peer. |

Notes mirroring the platform's [peer lifecycle](./api-reference.md):

- `peer_create` validates the WireGuard public key (Base64, ≥ 40 chars), the
  endpoint (`IP:Port` or `hostname:Port`), and the link-local address (must be
  in `fe80::/10`, e.g. `fe80::1`). Optional MTU is 576–9000. It also enforces
  one peer per node per ASN, a 10-pending-request cap, and per-node port
  uniqueness. New peers always start `pending`.
- `peer_update_pending` / `peer_cancel_pending` operate **only** on `pending` or
  `rejected` peers — active peers are untouched.
- For `active` or `suspended` peers, direct mutation is not allowed; instead the
  `*_operation_create` tools file an operation in `pending_admin_review` status
  for an administrator to approve out-of-band.

### Idempotency (user write tools)

All user write/operation tools require an `idempotency_key` argument (16–128
characters). The server records the key together with a canonical SHA-256 hash
of the request arguments (excluding the key itself) so that retries are safe:

- Replaying a **succeeded** request returns the stored result.
- Reusing a key with **different** arguments returns `idempotency_conflict`
  (`-32009`).
- Reusing a key while the first call is still processing returns
  `operation_in_progress` (`-32029`).

This makes it safe for an assistant to retry a `peer_create` after a timeout
without risking a duplicate peer.

---

## Admin tools

Available under `/api/v1/admin/mcp`. Every admin tool is read-only and gated by
an `admin:read:*` capability. They fall into the following categories:

- **Topology** (`admin:read:topology`) — list all nodes and aggregated node
  statistics (active/pending/total peers per node).
- **Peers** (`admin:read:peers`) — list peers across all ASNs (with status
  filter and pagination), list peers for a given ASN, get a single peer
  summary, and list all ASNs that have at least one peer.
- **Sensitive peer detail** (`admin:read:sensitive`) — fetch a peer's sensitive
  fields (endpoint, contact email, public keys, node addresses). This is a
  separate capability from `admin:read:peers` so that read access can be granted
  without exposing sensitive data.
- **Metrics** (`admin:read:metrics`) — time-series metrics for any peer
  (`hours` 1–720, default 24).
- **MCP keys & sessions** (`admin:read:mcp_keys`) — list user MCP key metadata
  (optionally by ASN) and list recent MCP sessions.
- **Audit** (`admin:read:audit`) — list platform-wide MCP audit logs with
  optional ASN/tool filters and pagination.
- **Settings** (`admin:read:settings`) — read site settings, notification
  settings, and Telegram bot settings metadata (secrets such as the bot token
  are stripped from the response).
- **System** (`admin:read:system`) — administrator system statistics, agent
  release list, and Telegram bot command statistics / blocked-user list.

Sensitive arguments are stripped before any tool call is recorded — see
[Audit logging](#audit-logging).

---

## Assistant tool-approval flow

In addition to the full MCP transport, Center offers a lighter-weight
**assistant** flow under `/api/v1/user/assistant/*`. This is authenticated with a
normal **user JWT** (not an MCP key) and is restricted to a small set of
**read-only** tools, with an explicit per-call approval handshake. It is intended
for an in-product assistant where each tool call should be confirmed before it
runs.

Only these tools are callable through the assistant flow:
`nodes_list`, `peer_creation_status`, `peer_list`, `peer_summary`, `peer_get`,
and `peer_get_metrics`. Any other tool name is rejected with `403 tool_not_allowed`.

### Endpoints

| Method & path | Description |
|---|---|
| `GET /api/v1/user/assistant/auth` | Probe: returns `ok`, the caller's `role`, `asn`, and `session_id`. |
| `POST /api/v1/user/assistant/tools/approval` | Issue a short-lived, signed approval token for a specific tool + arguments. |
| `POST /api/v1/user/assistant/tools/call` | Execute a previously approved tool call. |

### Flow

1. **Authorize once** — `GET .../assistant/auth` confirms the user session and
   echoes back the caller's identity.

2. **Request approval** — `POST .../assistant/tools/approval` with
   `{ "tool_name", "arguments", "conversation_id"? }`. The server validates the
   tool name and arguments, then returns an `approval_token` plus an
   `expires_at`:

   ```json
   { "approval_token": "<base64url-payload>.<base64url-hmac>", "expires_at": 1735689600 }
   ```

   The token is an HMAC-SHA256 (keyed off the server's JWT secret) over a payload
   binding the **ASN, conversation ID, tool name, exact arguments, a nonce, and a
   2-minute expiry**. If `conversation_id` is omitted, one is generated.

3. **Execute** — `POST .../assistant/tools/call` with the same
   `{ "tool_name", "arguments", "conversation_id" }` plus the `approval_token`.
   The server:
   - verifies the HMAC and that the token has not expired;
   - checks the token's bound ASN, tool name, conversation ID, and arguments all
     **match** this call exactly (otherwise `403 approval_mismatch`);
   - enforces **single use** via the nonce — a reused token returns
     `403 approval_reused`;
   - runs the read-only tool and returns `{ "tool_name", "conversation_id", "result" }`.

Expired, unused approval nonces are reaped by a background sweep once per minute.
Every assistant tool call is written to the MCP audit log under the tool name
`assistant.<tool_name>`.

---

## Audit logging

Every MCP tool call — user, admin, and assistant — is recorded to an MCP audit
log. Each entry captures the session ID, the principal (ASN and key ID for user
calls; admin ID and admin key ID for admin calls), the tool name, the required
capability, the mode (`read`, `sync_write`, or `operation`), the (sanitized)
arguments, the call duration, a success flag, and any error message. For write
tools the idempotency key and request hash are also recorded. Sessions
themselves are tracked separately (connect/disconnect/last-ping), keyed by the
same session ID surfaced in the audit entries.

**Forbidden** attempts (calling a tool whose capability the key lacks) are
logged with a `forbidden` error so unauthorized probing is visible.

### Argument sanitization

Before arguments are persisted, secret-bearing fields are stripped: `remote_pubkey`,
`remote_endpoint`, `contact_email`, `token`, `secret`, `password`, `signed_message`,
and `approval_token`. Audit logs therefore never store WireGuard keys, endpoints,
contact emails, or credentials.

### Durability

Audit writes are best-effort-but-never-dropped: they are enqueued to the
background job queue when available and fall back to a synchronous database
write otherwise, so an audit record is always produced.

### Reading audit logs

| Method & path | Auth | Scope |
|---|---|---|
| `GET /api/v1/user/mcp-audit-logs` | User JWT | Your ASN only (latest 200). |
| `GET /api/v1/admin/mcp-audit-logs?asn=&tool=&page=&per_page=` | Admin JWT | Platform-wide, filterable + paginated. |

The same data is also reachable in-protocol via the user `mcp_audit_logs_list`
tool and the admin `audit_logs_list` tool (subject to the relevant capability).

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

---

## See also

- [API Reference](./api-reference.md) — full HTTP endpoint catalog.
- [Authentication](./authentication.md) — JWT sessions vs. MCP keys.
