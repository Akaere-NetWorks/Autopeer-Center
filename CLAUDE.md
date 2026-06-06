# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What This Is

`autopeer-center` (`github.com/akaere/autopeer-center`) is the central control
plane for the AutoPeer automated DN42 peering system. It exposes HTTP/WebSocket
APIs for users and admins, manages the BGP/WireGuard peer lifecycle, and
communicates with node agents and the Telegram bot over encrypted WebSocket
connections. State is persisted in TimescaleDB (PostgreSQL-compatible) for both
relational data and time-series metrics; Redis is an optional layer for shared
cache, distributed locks, and the asynq job queue. The Go module targets Go
1.25.0 and builds the `center` binary.

This is the MIT-licensed open-source release (Copyright (c) 2026 Akaere
Networks). The AutoPeer frontend is not open-sourced; the backend docs in
`docs/` are the only API reference.

## Commands

```bash
# Build
go build ./cmd/center

# Run locally (requires .env)
cp .env.example .env   # fill in values
go run ./cmd/center

# Run with Docker Compose (starts TimescaleDB + center)
docker compose up --build

# Run tests
go test ./...

# Run a single package's tests
go test ./internal/handler/...

# Lint (if golangci-lint is available)
golangci-lint run ./...

# Repo policy before pushing
go build ./...
```

Migrations run automatically on startup from `migrations/*.up.sql` in lexical
order. Startup creates `schema_migrations`, takes a pg advisory lock, and
supports `-- +autopeer:no-transaction` in migration files. Down files are
reference-only.

## Architecture

### Request Flow

```
HTTP client → chi router (cmd/center/routes.go)
  → global middleware (RealIP, RequestID, BodyLimit, CORS, RequestLog, Recover)
  → /api/v1 group: APIVersion middleware (Autopeer-Version header negotiation)
  → route-level middleware (RequireUser / RequireAdmin / refresh/session guards)
  → handler (internal/handler/)
      → repository/service layers
      → database (pgx pool)
      → ws.Hub
```

Startup also wires Sentry, the bbolt cache, the cleanup worker, the latency
checker, the reconcile worker, and request logging.

### Routes

Core routes include `/health`, `/healthz`, and the `/api/v1` group; auth
refresh/logout/sessions/devices; MCP endpoints at `/api/v1/mcp` and
`/api/v1/admin/mcp`; the agent WebSocket endpoint at `/api/v1/agent/ws`; the bot
WebSocket endpoint at `/api/v1/bot/ws`; and admin system status endpoints.

### API Versioning (`internal/apiversion/`)

Stripe-style, header-negotiated response versioning, **orthogonal** to the URL
`/v1` path. Clients pin to a dated version via the `Autopeer-Version` request
header (e.g. `2026-06-06`); handlers build only the **latest** canonical shape
and a chain of backward `VersionChange` transformers downgrades the response to
the requested version. The `APIVersion` middleware is mounted on the whole
`/api/v1` group: it resolves the header (absent/empty ⇒ latest), echoes the
resolved version back in the `Autopeer-Version` response header, and returns
`400 invalid_api_version` for an unknown version. Only handlers that call
`handler.JSONVersioned(w, r, status, resource, listKey, data)` transform their
output; everything else is unaffected. Latest requests take a zero-overhead fast
path.

`versions` in `apiversion.go` is the single source of truth (oldest→newest,
`YYYY-MM-DD` so lexical == chronological order; last = `Latest()`). Currently
scoped to the peer endpoints (user list/get, admin list/get); the `2026-06-06`
change strips `endpoint_mismatch_since` + `bgp_suspended_by_endpoint` for older
versions. **To add the next version**: append the new dated string to
`versions`; make the latest shape uniform across all of that resource's
endpoints; register a `VersionChange` in `changes.go` whose `Apply` reshapes an
object back to the prior shape; add a `Resource` constant if new and call
`JSONVersioned` at its write sites; add Downgrade tests. Error bodies are never
versioned.

### Agent Communication (`internal/ws/`)

Each physical node runs an agent process that connects via WebSocket to
`GET /api/v1/agent/ws`; the Telegram bot has its own WebSocket endpoint
(`/api/v1/bot/ws`). The `Hub` manages live connections keyed by `node_id`.
Commands are request/response with a 30-second timeout and correlation via a
UUID `id` field.

**Agent authentication** uses an ECDH X25519 key exchange before the WebSocket
session is trusted. The center generates a persistent key pair stored at
`CENTER_KEY_PATH` (default `./center_keys.json`). On connect the agent and center
exchange public keys (`key.init` / `key.init_ack`), derive a shared secret, and
prove knowledge via HMAC (`key.auth` / `key.auth_ack`). Only after this handshake
is the agent registered in the hub. See `internal/crypto/` for primitives
(X25519 + ChaCha20-Poly1305).

**Message types** (`protocol.go`):
- `key.init` / `key.init_ack` / `key.auth` / `key.auth_ack` — agent authentication handshake
- `peer.add` / `peer.remove` — center → agent (install/teardown WireGuard + BIRD config)
- `response` — agent → center (ack with `success` bool)
- `heartbeat` — agent → center (periodic metrics: WG bytes, BGP state, RTT, BGP routes)
- `peers.sync` — bidirectional: agent requests full list of active peers for its node; center responds with the full list
- `agent.update` / `agent.rollback` — center → agent (binary update/rollback via `AGENT_RELEASE_DIR`)
- `bird.details` / `peers.import` / `status.request` — admin/diagnostic operations
- `bird.enable` / `bird.disable` — center → agent (toggle a BGP protocol)
- `network.*` and `bot.*` — diagnostic and Telegram-bot operations

Heartbeat data is written directly to the `peer_metrics` TimescaleDB hypertable.
Node-level metrics (goroutines, memory) go to the `node_metrics` hypertable.

### Authentication

JWTs use HS256 with `JWT_SECRET` and role-specific access/refresh/impersonation
lifetimes. Login methods include email verification-code, GPG signature
verification against DN42 registry mntner keys, admin email + password, and
WebAuthn / passkeys. Cloudflare Turnstile CAPTCHA is supported.

The initial admin account is bootstrapped/synced from `ADMIN_INITIAL_EMAIL` +
`ADMIN_INITIAL_PASSWORD` env vars on every startup (UPSERT).

### DN42 Registry Data

DN42 registry lookups fetch ASN contact email from the public mirror and cache
results in-process. `internal/whois/` provides WHOIS lookups.

### Peer Lifecycle

1. User submits `POST /api/v1/user/peers` — creates a peer record in `pending` status.
2. Admin approves via `POST /api/v1/admin/peers/{id}/approve` — sends `peer.add` to the node's agent; on success, status → `active`.
3. User deletes an active peer — sends `peer.remove` to the agent, then deletes the DB record.
4. Admin can also reject (status → `rejected` with reason), suspend (status → `suspended`), or hard-delete.

WireGuard port is calculated as `50000 + (asn % 10000)`; interface name as
`dn42_NNNNN` where `NNNNN` is `asn % 100000`.

### Key Configuration (`internal/config/config.go`)

All configuration is via environment variables (loaded from `.env` if present).
The two required variables are below; the full table lives in
[`docs/configuration.md`](docs/configuration.md).

| Variable | Required | Description |
|---|---|---|
| `DATABASE_URL` | yes | PostgreSQL/TimescaleDB connection string |
| `JWT_SECRET` | yes | HMAC secret for all JWTs (>=32 chars) |
| `ADMIN_INITIAL_EMAIL` | no | Bootstrapped admin email |
| `ADMIN_INITIAL_PASSWORD` | no | Bootstrapped admin password |
| `LISTEN_ADDR` | no | Listen address; default `:8080` |
| `CORS_ORIGIN` | no | Allowed CORS origin |
| `EXTERNAL_URL` | no | Public URL used in download links; default `http://localhost:8080` |
| `REDIS_URL` | no | Redis URL for shared cache, locks, and asynq queues |
| `CENTER_KEY_PATH` | no | Persistent ECDH key pair path |
| `AGENT_RELEASE_DIR` | no | Directory for OTA agent binaries |

See [`docs/configuration.md`](docs/configuration.md) for Redis, asynq, cache,
Sentry, Turnstile, WebAuthn, reconcile, token-TTL, and all other variables.

### Package Layout

- `cmd/center/` — wiring: config, DB, migrations, services, handlers, router, schedulers, graceful shutdown
- `internal/config/` — env-based config loading
- `internal/database/` — pgx pool setup
- `internal/repository/` — SQL data access layer
- `internal/model/` — shared data types
- `internal/handler/` — HTTP handlers (auth, node, peer, admin, agent, bot, registry, MCP)
- `internal/middleware/` — JWT parsing/validation, CORS, request ID, proxy/IP helpers, context helpers
- `internal/apiversion/` — header-negotiated API response versioning
- `internal/service/` — audit logging, email sending, DN42 registry lookup, system workflows
- `internal/whois/` — WHOIS lookups
- `internal/ws/` — WebSocket hub/protocol for agents and bots
- `internal/crypto/` — ECDH X25519 + ChaCha20-Poly1305 primitives
- `internal/cache/` — bbolt-backed persistent cache (optional Redis layer)
- `internal/redisx/` — Redis client wrapper
- `internal/lock/` — distributed locking (Redis-backed or process-local)
- `internal/queue/` — asynq job queue (Redis-backed async workers)
- `internal/latency/` — RTT checker and alerting worker
- `internal/cleanup/` — housekeeping workers
- `internal/reconcile/` — periodic peer/state reconciliation worker
- `migrations/` — numbered SQL migration files

## Contributing / Branch Workflow

This repository uses a GitHub fork + feature-branch + Pull Request workflow:

1. **Fork** the repository on GitHub and clone your fork.
2. **Add the upstream remote:** `git remote add upstream https://github.com/akaere/autopeer-center.git`
3. **Sync before starting work:**
   ```bash
   git checkout main
   git fetch upstream
   git merge upstream/main
   ```
4. **Create a feature branch:** `git checkout -b feature/<short-description>`
5. **Make changes, commit, and push to your fork:**
   ```bash
   git add -A && git commit -m "..."
   git push -u origin feature/<short-description>
   ```
6. **Open a Pull Request** against `akaere/autopeer-center:main` (e.g. with `gh pr create`).
7. **After merge:** sync `main` from upstream and delete the merged branch.

**Rules:**
- Never commit directly to `main`. All changes go through a feature branch + Pull Request.
- Always run `go build ./...` before pushing.
- Sync with `upstream/main` before starting new work.
