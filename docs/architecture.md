# Architecture

This document is the single "how the whole control plane fits together" reference
for `autopeer-center`. It maps the request path, process startup, package layout,
data stores, and the background machinery, and links out to the focused docs for
wire-level and per-field detail. If you are looking for *where a thing lives* or
*how data moves through the system*, start here.

## Overview

`autopeer-center` is the control plane for an automated [DN42](https://dn42.dev)
peering service. A single Go binary (`center`) exposes HTTP and WebSocket APIs
under `/api/v1`, drives the BGP/WireGuard **peer lifecycle**, and holds **encrypted
WebSocket links** to the node agents that actually install WireGuard + BIRD config
on each physical node. All state lives in **TimescaleDB** — relational tables for
peers, nodes, users, sessions, settings, and audit logs, plus time-series
hypertables for metrics. **Redis is optional**: when present it provides a shared
cache, distributed locks, and an asynq job queue; when absent the center degrades
gracefully to a local bbolt cache, process-local locks, and in-process schedulers.
A Telegram bot connects over its own WebSocket, and an MCP integration exposes
selected operations to AI assistants.

## Request flow

Every HTTP and WebSocket request takes the same path through the chi router and
the global middleware stack before reaching a handler. The `/api/v1` group adds
header-based API versioning, and individual route groups apply auth guards.

```
HTTP / WebSocket client
        │
        ▼
chi router  (cmd/center/routes.go)
        │
        ▼
global middleware
  RealIP (only if TRUSTED_PROXY_CIDR set) · RequestID · BodyLimit (1 MiB)
  · CORS · RequestLog · Recover
        │
        ▼
/api/v1 group
  APIVersion middleware  (Autopeer-Version header negotiation)
        │
        ▼
route guards  (internal/middleware/)
  RequireUser* · RequireAdmin* · RequireAnyAuth* · RequireMCPKey · RequireAdminMCPKey
        │
        ▼
handlers  (internal/handler/)
        │
        ▼
repository / service layers
  internal/repository/  (SQL data access)
  internal/service/     (audit, email, DN42 registry, workflows)
        │
        ├──────────────► pgx pool ──► TimescaleDB (relational tables + hypertables)
        │
        ├──────────────► Redis (cache · distributed locks · asynq queue)   [optional]
        │
        └──────────────► WebSocket hub (internal/ws/)
                            • agent hub — keyed by node_id, encrypted frames
                            • bot hub   — Telegram bot transport
```

Notes on the pipeline:

- **RealIP is conditional.** `chimw.RealIP` is only installed when
  `TRUSTED_PROXY_CIDR` is set; otherwise `X-Real-IP` / `X-Forwarded-For` are
  ignored so a client cannot spoof its source IP for rate limiting.
- **The body limit is 1 MiB** (`BodyLimit(1 << 20)`).
- **`APIVersion` is mounted on the whole `/api/v1` group**, orthogonal to the URL
  path. It resolves the `Autopeer-Version` header (absent/empty ⇒ latest), echoes
  the resolved version back, and returns `400 invalid_api_version` for an unknown
  value. Only handlers that opt in by calling `JSONVersioned` actually transform
  their output. See [api/versioning.md](./api/versioning.md).
- **Route guards** select the auth model per group: `RequireUser*` for user JWTs,
  `RequireAdmin*` for admin JWTs, `RequireAnyAuth*` for either, and the MCP-key
  guards for the MCP endpoints. See [authentication.md](./authentication.md).
- **Request logging is offloaded.** `RequestLog` enqueues each access-log row onto
  the asynq queue when available and falls back to a synchronous DB write
  otherwise.
- **Long-lived connections bypass the per-request write timeout.** The HTTP
  server is configured with `WriteTimeout: 0` so SSE streams and the agent/bot
  WebSockets can stay open; long-lived requests also skip the synchronous Sentry
  wrapper.

## Process startup & wiring

The `cmd/center/` package is the composition root. Startup is sequential and fails
fast — any unrecoverable error logs and exits rather than serving a half-built
process.

| File | Responsibility |
|---|---|
| `cmd/center/main.go` | Entry point and lifecycle: load `.env`, load config, connect DB, run migrations, bootstrap admin, open cache/locks, load the center key pair, build deps, mount the router, start the queue/scheduler and workers, serve HTTP, handle graceful shutdown |
| `cmd/center/deps.go` | The dependency graph: constructs every repository, service, handler, the WebSocket hub, the queue, and the background checkers, and wires cross-cutting callbacks (notifications, email-level lookups) |
| `cmd/center/routes.go` | The chi router: global middleware, utility routes (`/health`, `/healthz`), and the entire `/api/v1` route tree with its guards |
| `cmd/center/migrate.go` | Startup migrations under a Postgres advisory lock (see [database.md](./database.md)) |
| `cmd/center/bootstrap.go` | Upserts the initial admin account from `ADMIN_INITIAL_EMAIL` + `ADMIN_INITIAL_PASSWORD` |
| `cmd/center/sentry.go` | Optional Sentry initialization |

Startup order in `main.go`:

1. **Config & Sentry** — `config.Load()` reads environment variables (from `.env`
   if present); Sentry is initialized if `SENTRY_DSN` is set.
2. **Database** — connect the `pgx` pool, then `runMigrations` applies pending
   `migrations/*.up.sql` under the advisory lock before any traffic is served.
3. **Admin bootstrap** — a Bun ORM adapter is opened over the pool and
   `bootstrapAdmin` upserts the initial admin.
4. **Redis, cache & locks** — connect Redis if `REDIS_URL` is set (fatal only when
   `REDIS_REQUIRED=true`); open the bbolt-backed cache; choose a `RedisLocker` when
   Redis is available, otherwise a process-local `LocalLocker`.
5. **Center key pair** — load or generate the persistent X25519 key pair at
   `CENTER_KEY_PATH` (used for the agent handshake).
6. **Dependency graph** — `BuildDeps` constructs everything else and returns
   `AppDeps`.
7. **Router** — `setupRoutes` mounts middleware and routes.
8. **Queue & schedulers** — the queue server starts, periodic tasks register, and
   an initial tick is enqueued. When asynq is **not** enabled, the center starts
   the equivalent in-process schedulers (cleanup, latency) as a fallback.
9. **Reconcile worker** — started when `RECONCILE_ENABLED` is on; the distributed
   locker makes it safe to run across multiple center replicas.
10. **HTTP server** — listens on `LISTEN_ADDR` (default `:8080`) with
    `WriteTimeout: 0` for long-lived connections.
11. **Graceful shutdown** — on `SIGINT`/`SIGTERM` the hub is shut down, the HTTP
    server drains within a 10-second deadline, and the queue and MCP audit workers
    stop.

## Package layout

The roles below are the source of truth for where functionality lives. No package
is invented here; this mirrors the package table in
[../README.md](../README.md).

| Path | Role |
|---|---|
| `cmd/center/` | Entry point and wiring: config, DB connect, migrations, admin bootstrap, dependency graph, router, queue/scheduler lifecycle, graceful shutdown |
| `internal/config/` | Environment-based configuration loading and validation |
| `internal/database/` | `pgx` connection pool setup and Bun ORM adapter |
| `internal/repository/` | SQL data-access layer (peers, nodes, auth, audit, metrics, settings) |
| `internal/model/` | Shared data types |
| `internal/handler/` | HTTP handlers: auth, peer, node, admin, agent, bot, stats, registry, MCP, telegram-binding, email-prefs, passkey, queue-monitor, system-status |
| `internal/middleware/` | Auth/route guards, CORS, RequestID, BodyLimit, request logging, recover, proxy/IP helpers, context helpers |
| `internal/apiversion/` | Stripe-style, header-negotiated response versioning (`Autopeer-Version`), orthogonal to the URL path |
| `internal/service/` | Audit logging, email sending, DN42 registry lookup, system workflows |
| `internal/peering/` | Single source of truth for turning a peering request into a peer record: interface naming, listen-port allocation, validation |
| `internal/ws/` | WebSocket hub and wire protocol for node agents and the Telegram bot |
| `internal/crypto/` | X25519 ECDH key exchange + ChaCha20-Poly1305 frame encryption primitives |
| `internal/reconcile/` | Background worker that converges each online agent's WireGuard/BIRD state with the database |
| `internal/endpoint/` | Endpoint resolution and comparison (detects when an agent's actual WG endpoint drifts from the configured one) |
| `internal/latency/` | RTT checker and alerting worker |
| `internal/cleanup/` | Housekeeping / expiry workers |
| `internal/queue/` | asynq job queue and periodic scheduler (Redis-backed) |
| `internal/lock/` | Distributed locking — Redis-backed, with a process-local fallback |
| `internal/redisx/` | Redis client wrapper with health-check semantics |
| `internal/cache/` | bbolt-backed persistent cache with an optional Redis layer |
| `internal/whois/` | WHOIS lookups |
| `migrations/` | Numbered SQL migrations, auto-applied on startup |

## Data stores

### TimescaleDB (always required)

The center connects to one TimescaleDB database via the `pgx` pool
(`internal/database/`) using the connection string in `DATABASE_URL`. TimescaleDB
is a PostgreSQL extension, so the store is fully PostgreSQL-compatible. It holds
both:

- **Relational tables** — `admins`, `nodes`, `peers`, `audit_logs`,
  `agent_releases`, auth/session/passkey tables, and settings tables. Ordinary
  PostgreSQL tables with keys and constraints.
- **Time-series hypertables** — `peer_metrics`, `node_metrics`, and `request_logs`.
  These partition rows into time-based chunks with compression and retention
  policies.

See [database.md](./database.md) for the schema, the hypertable definitions, and
the auto-migration mechanism.

### Redis (optional)

When `REDIS_URL` is set, Redis provides three things, each with a graceful
fallback when it is absent:

| Capability | With Redis | Without Redis (fallback) |
|---|---|---|
| Cache (`internal/cache/`, `internal/redisx/`) | Shared Redis cache layer | Local bbolt cache only |
| Distributed locks (`internal/lock/`) | `RedisLocker` — safe singleton jobs across replicas | Process-local `LocalLocker` (no cross-replica guarantee) |
| Job queue (`internal/queue/`) | asynq workers + scheduler | In-process schedulers run the same jobs |

A misconfigured Redis is non-fatal by default; set `REDIS_REQUIRED=true` to make
startup fail when `REDIS_URL` is set but unreachable. See
[configuration.md](./configuration.md) for the Redis, asynq, and cache variables.

## Heartbeats → hypertables

This is the data path that [database.md](./database.md) refers to for "how
heartbeats flow into the metric hypertables."

Each node agent periodically sends a `heartbeat` message over its encrypted
WebSocket. The center ingests it and writes time-series rows to two TimescaleDB
hypertables — there is no separate ingest service; the WebSocket hub writes
directly through the metrics repository.

```
agent (per node)
   │  heartbeat  (encrypted WS frame, internal/ws/)
   │    • node_metrics: mem_alloc_mb, mem_sys_mb, num_goroutine, uptime_secs
   │    • peers[]: peer_id, asn, wg_rx_bytes, wg_tx_bytes, wg_last_handshake,
   │              bgp_state, rtt_ms, routes_imported/exported/preferred,
   │              bgp_uptime_secs, wg_actual_endpoint
   ▼
center  →  handleHeartbeat
   │
   ├─ per peer ─► peer_metrics hypertable   (one row per peer per heartbeat)
   │              byte counters, BGP state, RTT, route counts, last handshake
   │
   └─ per node ─► node_metrics hypertable    (one row per node per heartbeat)
                  memory, goroutine count, uptime
```

What the heartbeat carries and where each part lands:

- **Per-peer telemetry → `peer_metrics`.** For every peer in the heartbeat the
  center writes one row: WireGuard rx/tx byte counters, BGP state, RTT, BGP route
  counts, last handshake time, and the actual observed WireGuard endpoint.
- **Per-node runtime stats → `node_metrics`.** The node-level block (memory
  allocated/system, goroutine count, uptime) becomes one node row per heartbeat.

Both tables are TimescaleDB hypertables, so this stream is **time-series data** —
chunked by time, compressed, and pruned by retention policy for later range
queries (e.g. the per-peer metrics endpoints). A heartbeat also has side effects
beyond metrics: it refreshes the node's reported agent version, marks a
previously-offline node back online, and drives per-peer alerting (BGP-down,
BGP-recovered, stale-handshake) gated by operator settings and notification
preferences. The exact `HeartbeatPayload` fields are documented in
[websocket-protocol.md](./websocket-protocol.md#heartbeat--metrics-hypertables).

## Peer lifecycle

The peering state machine lives across the peer/admin handlers and the agent hub.
Interface naming and port allocation are centralized in `internal/peering/`.

```
user POST /api/v1/user/peers
        │
        ▼
     pending ──── admin approve ──► peer.add → agent ──► active
        │                                                   │
        │                                          user delete (active)
   admin reject                                            │
   (→ rejected,                                   peer.remove → agent
    with reason)                                            │
        │                                              record deleted
   admin suspend (→ suspended) / admin hard-delete
```

1. A user submits `POST /api/v1/user/peers`; the peer record is created in
   `pending` status.
2. An admin approves via `POST /api/v1/admin/peers/{id}/approve`; the center sends
   a `peer.add` command to the owning node's agent, and on success the status
   becomes `active`.
3. A user deletes an active peer; the center sends `peer.remove` to the agent,
   then deletes the DB record.
4. At any point an admin can **reject** (status → `rejected`, with a reason),
   **suspend** (status → `suspended`), or **hard-delete** a peer.

Allocation formulas (computed once and stored on the peer record):

- **WireGuard listen port:** `50000 + (asn % 10000)`
- **Interface name:** `dn42_NNNNN`, where `NNNNN` is `asn % 100000`

The full request/response shapes for these endpoints are in
[api/peers.md](./api/peers.md) and [api/admin.md](./api/admin.md) (see
[api/README.md](./api/README.md) for the index).

## Agent & bot links

Both long-lived clients share a single `Hub` (`internal/ws/`). The agent hub is
keyed by `node_id`; the bot hub is keyed by a generated connection ID. Commands
are request/response, correlated by a UUID `id`, with a 30-second timeout.

- **Agents** connect to `GET /api/v1/agent/ws`. After the WebSocket upgrade the
  agent performs an X25519 ECDH handshake against the center's persistent key pair
  (loaded from `CENTER_KEY_PATH`): `key.init` / `key.init_ack` / `key.auth` /
  `key.auth_ack`. Only after the handshake completes is the agent trusted and
  registered in the hub; from then on application frames are ChaCha20-Poly1305
  encrypted. The agent's public key is stored trust-on-first-use.
- **The Telegram bot** connects to `GET /api/v1/bot/ws` and authenticates with an
  in-band `bot.auth` message carrying a shared bot auth token (no transport-level
  token; an Origin check applies). Bot frames are plaintext JSON.

The full handshake, frame format, message-type tables, and connection-lifecycle
details are in [websocket-protocol.md](./websocket-protocol.md).

## Background workers

The center runs several periodic workers. When asynq is enabled they run as queue
jobs on a schedule; otherwise the equivalent in-process schedulers run them.

| Worker | Package | Role |
|---|---|---|
| Reconcile | `internal/reconcile/` | Converges each online agent's WireGuard/BIRD state with the database (pushes the authoritative peer list via `peers.sync`). Guarded by the distributed locker so it is safe across replicas. |
| Latency checker | `internal/latency/` | Periodic RTT checks and latency alerting. |
| Cleanup | `internal/cleanup/` | Housekeeping / expiry of stale records. |
| Queue scheduler | `internal/queue/` | The asynq periodic scheduler that enqueues the recurring jobs above when Redis-backed queuing is enabled. |

## MCP

The center exposes Model Context Protocol endpoints for AI assistants at
`/api/v1/mcp` (user scope) and `/api/v1/admin/mcp` (admin scope), each guarded by
a dedicated MCP API key with tool approval and audit logging. See
[api/mcp.md](./api/mcp.md) for the tool catalog and key management.

## See also

- [configuration.md](./configuration.md) — every environment variable and what it affects
- [database.md](./database.md) — TimescaleDB schema, hypertables, and migrations
- [websocket-protocol.md](./websocket-protocol.md) — the encrypted agent/bot wire protocol
- [authentication.md](./authentication.md) — JWT model, login methods, and route guards
- [api/README.md](./api/README.md) — the complete per-endpoint HTTP API reference
