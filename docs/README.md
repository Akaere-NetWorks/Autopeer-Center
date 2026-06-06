# autopeer-center

`autopeer-center` is the central control plane for **AutoPeer**, an automated [DN42](https://dn42.dev) peering service. It is the brain that users, administrators, node agents, and a Telegram bot all talk to.

The compiled binary is `center`; the Go module is `github.com/akaere/autopeer-center`.

> **The AutoPeer frontend is not open-source.** This repository is the AutoPeer *backend*. There is no companion web UI in this release — these documents are the complete and authoritative API reference. Any client (a custom dashboard, a script, the Telegram bot, or an MCP/AI assistant) integrates with the center purely through the HTTP and WebSocket APIs documented here.

## What it does

autopeer-center is responsible for the full lifecycle of a DN42 peering:

- **HTTP & WebSocket APIs for users and admins** — a [chi](https://github.com/go-chi/chi)-based REST API under `/api/v1` for self-service peer management (create, inspect, update, delete) and for administrative review, node management, settings, audit logs, and queue monitoring.
- **BGP / WireGuard peer lifecycle** — turning a user-submitted peering request into a real, running BGP session: WireGuard interface naming and listen-port allocation (`internal/peering/`), admin approval, then `peer.add` / `peer.remove` commands pushed to the node where the peering lives.
- **Encrypted WebSocket links to node agents** — every physical node runs an `autopeer-agent` that dials back to the center over WebSocket (`/api/v1/agent/ws`). The link is authenticated with an X25519 ECDH handshake and every frame is encrypted with ChaCha20-Poly1305 (`internal/crypto/`, `internal/ws/`). The center installs/removes WireGuard + BIRD config, ships agent binary updates/rollbacks, and ingests periodic heartbeats (WireGuard byte counters, BGP state, RTT, route counts).
- **Telegram bot transport** — the bot connects over its own WebSocket (`/api/v1/bot/ws`), letting users manage peerings from chat. Users bind their account to a Telegram identity and configure per-channel notification preferences.
- **TimescaleDB for relational *and* time-series data** — peers, nodes, users, sessions, settings, and audit logs live in regular tables; agent heartbeats land in TimescaleDB hypertables (e.g. `peer_metrics`, `node_metrics`) for time-series queries. Access is via the `pgx` pool plus a Bun ORM adapter (`internal/database/`, `internal/repository/`).
- **Optional Redis / asynq** — when `REDIS_URL` is set, Redis provides a shared cache, distributed locks (for safe singleton jobs across replicas), and an [asynq](https://github.com/hibiken/asynq) job queue with an in-process scheduler. Without Redis the center degrades gracefully to a local bbolt cache, process-local locks, and in-process schedulers.
- **AI assistant integration (MCP)** — Model Context Protocol endpoints (`/api/v1/mcp` and `/api/v1/admin/mcp`) expose peer operations to AI assistants, gated by dedicated MCP API keys with tool-approval and audit logging. See [./mcp.md](./mcp.md).

The Go module targets Go 1.25.0. Database migrations in `migrations/*.up.sql` are applied automatically on startup (under a Postgres advisory lock, so multiple replicas can boot safely).

## Request flow

```
HTTP / WebSocket client
        │
        ▼
chi router  (cmd/center/routes.go)
        │
        ▼
global middleware
  RealIP (if TRUSTED_PROXY_CIDR set) · RequestID · BodyLimit · CORS · RequestLog · Recover
        │
        ▼
route guards  (internal/middleware/)
  RequireUser* · RequireAdmin* · RequireAnyAuth* · RequireMCPKey · RequireAdminMCPKey
  APIVersion (Autopeer-Version header negotiation, mounted on /api/v1)
        │
        ▼
handlers  (internal/handler/)
  auth · peer · node · admin · agent · bot · stats · registry
  telegram-binding · email-prefs · passkey · MCP · queue-monitor · system-status
        │
        ▼
repository / service layers
  internal/repository/  (SQL data access)
  internal/service/     (audit, email, DN42 registry, workflows)
  internal/peering/     (interface/port allocation + validation)
        │
        ├──────────────► pgx pool ──► TimescaleDB (relational tables + hypertables)
        │
        ├──────────────► Redis (cache · distributed locks · asynq queue)   [optional]
        │
        └──────────────► WebSocket hubs (internal/ws/)
                            • agent hub  — keyed by node_id, encrypted frames
                            • bot hub    — Telegram bot transport
```

Long-lived connections (agent/bot WebSockets, SSE streams) bypass the per-request write timeout and the synchronous Sentry wrapper so they can stay open. Request logging is offloaded to the asynq queue when available, falling back to a synchronous DB write otherwise.

## Package layout

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

## Documentation

| Document | Covers |
|---|---|
| [./configuration.md](./configuration.md) | Every environment variable, defaults, and what each one affects |
| [./deployment.md](./deployment.md) | Building and running the center (Docker Compose and from source), volumes, and operations |
| [./database.md](./database.md) | TimescaleDB schema, hypertables, and the auto-migration system |
| [./authentication.md](./authentication.md) | JWT model, email-code / GPG / passkey / admin login, device flow, and route guards |
| [./api-reference.md](./api-reference.md) | The full `/api/v1` HTTP API — public, user, and admin endpoints |
| [./websocket-protocol.md](./websocket-protocol.md) | The encrypted agent/bot WebSocket handshake and message types |
| [./mcp.md](./mcp.md) | Model Context Protocol endpoints, API keys, tool approval, and audit logging |

## Quickstart

To build and run the center (Docker Compose, which also brings up TimescaleDB, or from source), see [./deployment.md](./deployment.md). At minimum you must set `DATABASE_URL` and `JWT_SECRET`; the full list of settings is in [./configuration.md](./configuration.md). The server listens on `:8080` by default and applies database migrations automatically on first start.

## License

MIT. Copyright (c) 2026 Akaere Networks.
