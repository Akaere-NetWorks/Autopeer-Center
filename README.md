# autopeer-center

Central control plane for the AutoPeer automated DN42 peering system.

Licensed under the [MIT License](#license) — Copyright (c) 2026 Akaere Networks.

## Overview

`autopeer-center` (`github.com/akaere/autopeer-center`) is the control plane for
automated DN42 peering. It exposes HTTP and WebSocket APIs for users and admins,
manages the BGP/WireGuard peer lifecycle, and communicates with node agents and
the Telegram bot over encrypted WebSocket connections. State is persisted in
TimescaleDB (PostgreSQL-compatible) for both relational data and time-series
metrics. Redis is an optional layer for shared cache, distributed locks, and the
asynq job queue. An MCP (Model Context Protocol) integration exposes selected
operations to AI assistants. The Go module targets Go 1.25.0; the binary is
`center`.

## Architecture

```
HTTP client → chi router
  → global middleware (RealIP, RequestID, BodyLimit, CORS, RequestLog, Recover)
  → /api/v1 group: APIVersion middleware (Autopeer-Version header negotiation)
  → route-level middleware (RequireUser / RequireAdmin / refresh / session guards)
  → handler (internal/handler/)
      → repository / service layers
      → TimescaleDB (pgx pool)
      → Redis (cache, locks, asynq queues) — optional
      → WebSocket hub (agents, bots)
```

### Key components

| Package | Role |
|---|---|
| `cmd/center/` | Entry point — wires config, DB, migrations, router, workers, schedulers, graceful shutdown |
| `internal/config/` | Environment-based config loading and validation |
| `internal/database/` | pgx pool setup |
| `internal/model/` | Shared data types |
| `internal/handler/` | HTTP handlers: auth, node, peer, admin, agent, bot, registry, MCP, telegram-binding, email-prefs |
| `internal/middleware/` | JWT parsing/validation, CORS, RequestID, BodyLimit, proxy/IP helpers, context helpers |
| `internal/apiversion/` | Stripe-style, header-negotiated API response versioning |
| `internal/repository/` | SQL data-access layer |
| `internal/service/` | Audit logging, email sending, DN42 registry lookup, system workflows |
| `internal/ws/` | WebSocket hub and protocol for agents and bots (encrypted, X25519 + ChaCha20-Poly1305) |
| `internal/crypto/` | X25519 ECDH + ChaCha20-Poly1305 primitives |
| `internal/cache/` | bbolt-backed persistent cache with optional Redis layer |
| `internal/latency/` | RTT checker and alerting worker |
| `internal/cleanup/` | Housekeeping workers |
| `internal/reconcile/` | Periodic peer/state reconciliation worker |
| `internal/lock/` | Distributed locking (Redis-backed or process-local) |
| `internal/queue/` | Asynq job queue (Redis-backed async workers) |
| `internal/redisx/` | Redis client wrapper with health-check semantics |
| `internal/whois/` | WHOIS lookups |
| `migrations/` | Numbered SQL migrations (auto-applied on startup) |

### Peer lifecycle

1. User submits `POST /api/v1/user/peers` → status `pending`
2. Admin approves → `peer.add` sent to the node agent → status `active`
3. User deletes → `peer.remove` sent to the agent → record deleted
4. Admin can reject (with reason), suspend, or hard-delete at any time

WireGuard port: `50000 + (asn % 10000)` · Interface: `dn42_NNNNN`
where `NNNNN` is `asn % 100000`.

### Agent & bot communication

Each node agent connects via WebSocket to `/api/v1/agent/ws`. Authentication uses
an ECDH X25519 key exchange (`key.init` / `key.init_ack` / `key.auth` /
`key.auth_ack`) followed by ChaCha20-Poly1305 frame encryption; only after the
handshake is the agent registered in the `Hub`, keyed by `node_id`. Commands are
request/response with a 30-second timeout and UUID correlation. Heartbeats carry
WireGuard byte counters, BGP state, RTT, and route counts, written to the
`peer_metrics` and `node_metrics` TimescaleDB hypertables.

The Telegram bot connects via `/api/v1/bot/ws` and authenticates with a shared
bot auth token.

### MCP integration

SSE-based AI-assistant endpoints at `/api/v1/mcp` and `/api/v1/admin/mcp`, with
tool approval, tool calls, API-key management, and audit logging.

### Authentication

- JWT (HS256) with role-specific access/refresh/impersonation token lifetimes
- Email verification-code login (`/auth/user/request-code`, `/auth/user/verify-code`)
- GPG signature verification against DN42 registry mntner keys
- Admin email + password login
- Admin impersonation with a dedicated TTL
- WebAuthn / passkey support
- Cloudflare Turnstile CAPTCHA support
- Admin account bootstrapped from `ADMIN_INITIAL_EMAIL` + `ADMIN_INITIAL_PASSWORD` on startup

## Quick start

### Docker Compose (recommended)

```bash
cp .env.example .env
# Edit .env — at minimum set DATABASE_URL, JWT_SECRET, ADMIN_INITIAL_EMAIL/PASSWORD
docker compose up --build
```

The compose file starts TimescaleDB and the center service together with named
volumes for data, releases, cache, and keys.

### Local

```bash
go build ./cmd/center
cp .env.example .env   # fill in values
./center
```

The server listens on `:8080` by default. Migrations run automatically on startup.

## Documentation

> **The AutoPeer frontend is not open-sourced.** These backend docs are the only
> API reference. Full backend and HTTP/WebSocket API documentation lives in
> [`./docs/`](./docs/README.md).

Start here:

- [Documentation index](./docs/README.md)
- [Getting started](./docs/getting-started.md) — from a fresh clone to a running backend
- [Architecture](./docs/architecture.md) — how the control plane fits together
- [Configuration reference](./docs/configuration.md) — every environment variable
- [Deployment guide](./docs/deployment.md) — Docker Compose and production notes

## Development

```bash
go build ./...                    # verify compilation
go test ./...                     # run all tests
go test ./internal/handler/...    # single package
golangci-lint run ./...           # lint (requires golangci-lint)
```

Always run `go build ./...` before pushing.

## Deployment

A `Dockerfile` and `docker-compose.yml` are provided. Build args `COMMIT_HASH`,
`BUILD_DATE`, and `VERSION` are injected via ldflags. See
[`./docs/deployment.md`](./docs/deployment.md) for the full guide.

## License

Released under the MIT License.

Copyright (c) 2026 Akaere Networks.
