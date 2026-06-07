# Configuration

AutoPeer Center is configured entirely through environment variables. On startup the
`center` binary loads variables from the process environment (and from a `.env` file in
the working directory, if present), validates them, and fails fast if a required value is
missing or invalid.

This page is the complete, authoritative reference for every variable read by the
application. It is generated from `internal/config/config.go`.

Related docs:

- [Deployment](./deployment.md) — Docker Compose, volumes, and migrations
- [Getting started](./getting-started.md) — quick local setup

## How values are parsed

- **Strings** — used verbatim. An empty environment value falls back to the default.
- **Booleans** — parsed with Go's `strconv.ParseBool`, so `true`/`false`, `1`/`0`,
  `t`/`f` are all accepted. An invalid value logs a warning and falls back to the default.
- **Integers** — parsed with `strconv.Atoi`; an invalid value falls back to the default.
- **Floats** — parsed with `strconv.ParseFloat`; an invalid value logs a warning and
  falls back to the default.
- **Durations** — parsed with Go's `time.ParseDuration` (e.g. `30m`, `1h`, `720h`). If
  that fails, a bare integer is interpreted as **seconds**. A non-positive or unparseable
  value falls back to the default.

Defaults shown below are the **OSS defaults** suitable for local development. For
production you must override the public-facing values (`CORS_ORIGIN`, `EXTERNAL_URL`,
`WEBAUTHN_RPID`, `WEBAUTHN_ORIGIN`, the secrets, etc.).

## Required

These two variables have no defaults. Startup aborts with an error if either is missing,
and `JWT_SECRET` is additionally rejected if it is shorter than 32 characters.

| Variable | Required | Default | Description |
|---|---|---|---|
| `DATABASE_URL` | yes | — | PostgreSQL / TimescaleDB connection string, e.g. `postgres://autopeer:changeme@db:5432/autopeer?sslmode=disable`. Use `sslmode=require` in production. The password is masked in startup logs. |
| `JWT_SECRET` | yes | — | HMAC (HS256) secret used to sign all JWTs. **Must be at least 32 characters**; startup fails otherwise. |

## Networking & CORS

| Variable | Required | Default | Description |
|---|---|---|---|
| `LISTEN_ADDR` | no | `:8080` | Address the HTTP server binds to. |
| `CORS_ORIGIN` | no | `http://localhost:8080` | Allowed CORS origin for browser clients. Set this to your frontend origin in production, e.g. `https://your-center.example.com`. |
| `EXTERNAL_URL` | no | `http://localhost:8080` | Public base URL of this center. Used to build agent download links. A warning is logged if this is left at the `http://localhost:8080` default, since agent download URLs would be incorrect in production. |
| `TRUSTED_PROXY_CIDR` | no | `""` (empty) | Comma-separated CIDR list of trusted reverse proxies. When set, the real-client-IP middleware honors forwarded headers from these ranges. Leave empty if the center is not behind a proxy. |

## Authentication & token TTLs

All of these accept Go duration strings (e.g. `30m`, `1h`, `720h`) or a bare integer
number of seconds. See [Authentication](./authentication.md) for how the tokens are used.

| Variable | Required | Default | Description |
|---|---|---|---|
| `USER_ACCESS_TOKEN_TTL` | no | `1h` | Lifetime of user access tokens. |
| `ADMIN_ACCESS_TOKEN_TTL` | no | `30m` | Lifetime of admin access tokens. |
| `USER_REFRESH_TOKEN_TTL` | no | `720h` (30 days) | Lifetime of user refresh tokens. |
| `ADMIN_REFRESH_TOKEN_TTL` | no | `336h` (14 days) | Lifetime of admin refresh tokens. |
| `IMPERSONATION_TOKEN_TTL` | no | `15m` | Lifetime of admin impersonation tokens. |

## WebAuthn

Passkey / security-key support. Both values must match the deployment's public origin for
WebAuthn ceremonies to succeed.

| Variable | Required | Default | Description |
|---|---|---|---|
| `WEBAUTHN_RPID` | no | `localhost` | WebAuthn Relying Party ID — the registrable domain of your center, e.g. `example.com`. |
| `WEBAUTHN_ORIGIN` | no | `http://localhost:8080` | Expected WebAuthn origin, e.g. `https://your-center.example.com`. Must match the scheme + host the browser sees. |

## Admin bootstrap

The initial admin account is upserted on **every** startup from these values. See
[Authentication](./authentication.md).

| Variable | Required | Default | Description |
|---|---|---|---|
| `ADMIN_INITIAL_EMAIL` | no | `""` (empty) | Email of the bootstrap admin account, e.g. `admin@example.com`. Leave empty to skip bootstrapping. |
| `ADMIN_INITIAL_PASSWORD` | no | `""` (empty) | Password for the bootstrap admin account. |
| `ADMIN_NOTIFY_EMAILS` | no | `""` (empty) | Comma-separated list of admin emails to notify for system alerts, e.g. `admin@example.com`. |

## Email

Outbound transactional email is delivered over **SMTP** as **plain text**. The center
renders each notification template to plain text in-process and sends it via the configured
SMTP server.

Set `SMTP_HOST` and `SMTP_FROM` to enable email. Leave `SMTP_HOST` empty to disable email
delivery (a warning is logged). When `SMTP_HOST` is set, `SMTP_FROM` is required and
`SMTP_TLS` must be one of `starttls`, `tls`, or `none`.

| Variable | Required | Default | Description |
|---|---|---|---|
| `SMTP_HOST` | no | `""` (empty) | SMTP server host. Set it to enable email; empty disables email delivery. |
| `SMTP_PORT` | no | `587` | SMTP server port. |
| `SMTP_USERNAME` | no | `""` (empty) | SMTP auth username. Authentication is skipped when empty. |
| `SMTP_PASSWORD` | no | `""` (empty) | SMTP auth password. |
| `SMTP_FROM` | no | `""` (empty) | From address. Required when `SMTP_HOST` is set. |
| `SMTP_FROM_NAME` | no | `AutoPeer` | From display name. |
| `SMTP_TLS` | no | `starttls` | TLS mode: `starttls`, `tls` (implicit TLS, e.g. port 465), or `none` (dev only). |

## DN42 registry

| Variable | Required | Default | Description |
|---|---|---|---|
| `DN42_REGISTRY_TOKEN` | no | `""` (empty) | Bearer token used for DN42 registry API lookups. Leave empty to use only unauthenticated access. |

## Telegram

| Variable | Required | Default | Description |
|---|---|---|---|
| `TELEGRAM_BOT_USERNAME` | no | `""` (empty) | Public @username of the Telegram bot. A leading `@` is stripped automatically, and surrounding whitespace is trimmed. Used to build bot deep-links. |

## Cloudflare Turnstile

Bot-protection challenge for relevant endpoints. Leave both empty to disable Turnstile
verification.

| Variable | Required | Default | Description |
|---|---|---|---|
| `TURNSTILE_SITE_KEY` | no | `""` (empty) | Cloudflare Turnstile site key (public). |
| `TURNSTILE_SECRET_KEY` | no | `""` (empty) | Cloudflare Turnstile secret key (server-side verification). |

## Redis & asynq

Redis is optional. Redis is enabled only when `REDIS_URL` is non-empty; it backs the
shared cache, distributed locks, and the asynq job queues. When Redis is unavailable the
center can fall back to a local on-disk cache (see `CACHE_LOCAL_FALLBACK` and `CACHE_DIR`).

| Variable | Required | Default | Description |
|---|---|---|---|
| `REDIS_URL` | no | `""` (empty) | Redis connection URL, e.g. `redis://default:password@redis.example.com:6379/0`. Empty disables Redis. Surrounding whitespace is trimmed. |
| `REDIS_REQUIRED` | no | `false` | If `true`, startup fails when `REDIS_URL` is set but Redis cannot be reached. If `false`, the center falls back to local cache. |
| `REDIS_KEY_PREFIX` | no | `autopeer:center:` | Prefix applied to all Redis keys, so one Redis instance can be shared across deployments. |
| `ASYNQ_ENABLED` | no | `true` | Enable Redis-backed asynq workers for supported background jobs. Effective only when Redis is configured. |
| `ASYNQ_CONCURRENCY` | no | `10` | Number of concurrent asynq workers. |
| `ASYNQ_QUEUES` | no | `critical:6,default:3,low:1` | Weighted asynq queue priorities (`name:weight`, comma-separated). |
| `ASYNQ_READONLY_MONITOR` | no | `false` | If `true`, disables the mutating queue-monitor routes (delete/archive/retry), leaving the asynq monitor read-only. |
| `ASYNQ_MONITOR_PAGE_SIZE` | no | `20` | Page size for the asynq queue-monitor listings. |

## Reconcile worker

Periodically reconciles the center's view of active peers with each agent via the
`peers.sync` protocol message.

| Variable | Required | Default | Description |
|---|---|---|---|
| `RECONCILE_ENABLED` | no | `true` | Enable the periodic peer reconcile worker. |
| `RECONCILE_INTERVAL` | no | `10m` | Interval between reconcile passes (duration string or seconds). |

## Persistent paths

These paths should map to persistent storage. The defaults are platform-dependent: the
Linux defaults are shown below; on Windows the center uses the relative `./...` form
instead. Keep them in sync with your container volume mounts — see
[Deployment](./deployment.md).

| Variable | Required | Default (Linux) | Description |
|---|---|---|---|
| `CENTER_KEY_PATH` | no | `/var/lib/autopeer-center/center_keys.json` | Path to the center's persistent ECDH key file (used for the agent handshake). Windows default: `./center_keys.json`. |
| `CACHE_DIR` | no | `/var/lib/autopeer-center/cache` | Local on-disk cache location used when Redis is unavailable. Windows default: `./cache`. **See the note below about the file-vs-directory inconsistency.** |
| `CACHE_LOCAL_FALLBACK` | no | `true` | Enable the local on-disk cache fallback when Redis is unavailable. |
| `AGENT_RELEASE_DIR` | no | `/var/lib/autopeer-center/releases` | Directory holding agent release binaries served for agent update/rollback. Windows default: `./releases`. |

> **Inconsistency to be aware of — `CACHE_DIR`.** The application's compiled default for
> `CACHE_DIR` is the **directory** `/var/lib/autopeer-center/cache`, and the Docker
> Compose volume mounts that same directory. However, the shipped `.env.example` sets
> `CACHE_DIR=/var/lib/autopeer-center/cache/cache.db` — a **file path** one level deeper.
> If you copy `.env.example` verbatim, your `CACHE_DIR` points at a file inside the mounted
> directory rather than at the directory itself. Pick one convention and use it
> consistently; for the Compose setup, prefer the directory form
> `/var/lib/autopeer-center/cache` so it matches the mounted volume.

## Sentry

Error and performance monitoring. Sentry is enabled only when `SENTRY_DSN` is non-empty.
When the DSN is set, `SENTRY_TRACES_SAMPLE_RATE` is validated to be between `0` and `1`
(startup fails otherwise), and an empty `SENTRY_ENVIRONMENT` is normalized to `production`.

| Variable | Required | Default | Description |
|---|---|---|---|
| `SENTRY_DSN` | no | `""` (empty) | Sentry DSN. Empty disables Sentry. Surrounding whitespace is trimmed. |
| `SENTRY_ENVIRONMENT` | no | `production` | Environment tag reported to Sentry. |
| `SENTRY_RELEASE` | no | `""` (empty) | Release identifier reported to Sentry. |
| `SENTRY_TRACES_SAMPLE_RATE` | no | `0` | Fraction of transactions traced. Must be between `0` and `1` when a DSN is set. |
| `SENTRY_DEBUG` | no | `false` | Enable Sentry SDK debug logging. |
| `SENTRY_ENABLE_METRICS` | no | `false` | Enable Sentry metrics. |

## Docker Compose-only variables

Some variables in `.env.example` are consumed by Docker Compose, **not** by the `center`
application itself.

| Variable | Read by app? | Description |
|---|---|---|
| `DB_PASSWORD` | no | Used only by the Compose `db` service to set the PostgreSQL password. It is **not** read by `center`; the application gets its credentials from `DATABASE_URL`. Keep `DB_PASSWORD` and the password inside `DATABASE_URL` in sync. |

## Example `.env`

```dotenv
# ── Required ────────────────────────────────────────────────────────────────
DATABASE_URL=postgres://autopeer:changeme@db:5432/autopeer?sslmode=disable
JWT_SECRET=change-this-to-a-random-secret-at-least-32-chars

# ── Database (docker-compose db service only) ───────────────────────────────
DB_PASSWORD=changeme

# ── Admin bootstrap (applied on every startup) ──────────────────────────────
ADMIN_INITIAL_EMAIL=admin@example.com
ADMIN_INITIAL_PASSWORD=change-this-password

# ── Networking & CORS ───────────────────────────────────────────────────────
LISTEN_ADDR=:8080
CORS_ORIGIN=https://your-center.example.com
EXTERNAL_URL=https://your-center.example.com

# ── WebAuthn ────────────────────────────────────────────────────────────────
WEBAUTHN_RPID=example.com
WEBAUTHN_ORIGIN=https://your-center.example.com

# ── DN42 registry ───────────────────────────────────────────────────────────
DN42_REGISTRY_TOKEN=

# ── Email (SMTP, plain text only) ────────────────────────────────────────────
# Set SMTP_HOST and SMTP_FROM to enable email; leave SMTP_HOST empty to disable.
SMTP_HOST=
SMTP_PORT=587
SMTP_USERNAME=
SMTP_PASSWORD=
SMTP_FROM=noreply@example.com
SMTP_FROM_NAME=AutoPeer
SMTP_TLS=starttls

# ── Notifications (comma-separated admin emails) ────────────────────────────
ADMIN_NOTIFY_EMAILS=admin@example.com

# ── Redis (optional) ────────────────────────────────────────────────────────
REDIS_URL=
REDIS_REQUIRED=false
REDIS_KEY_PREFIX=autopeer:center:
CACHE_LOCAL_FALLBACK=true

# ── Persistent paths (keep in sync with docker-compose volumes) ─────────────
AGENT_RELEASE_DIR=/var/lib/autopeer-center/releases
CACHE_DIR=/var/lib/autopeer-center/cache
CENTER_KEY_PATH=/var/lib/autopeer-center/center_keys.json

# ── Cloudflare Turnstile (leave empty to disable) ───────────────────────────
TURNSTILE_SITE_KEY=
TURNSTILE_SECRET_KEY=

# ── Sentry (leave DSN empty to disable) ─────────────────────────────────────
SENTRY_DSN=
SENTRY_ENVIRONMENT=production
SENTRY_RELEASE=
SENTRY_TRACES_SAMPLE_RATE=0
SENTRY_ENABLE_METRICS=false
SENTRY_DEBUG=false
```
