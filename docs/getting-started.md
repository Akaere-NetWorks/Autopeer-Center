# Getting started

A quick local setup for **autopeer-center** — the `center` control plane built
from the `github.com/akaere/autopeer-center` module. This page covers the fast
path: clone the repo, fill in a handful of values, and get the backend serving.

For the complete environment-variable reference see
[`./configuration.md`](./configuration.md); for production, volumes, reverse
proxies, and operations see [`./deployment.md`](./deployment.md); for how the
pieces fit together see [`./architecture.md`](./architecture.md).

> **The AutoPeer frontend is not open-source.** The HTTP API is the only
> interface to the center — see [`./api/README.md`](./api/README.md).

## Prerequisites

- **Git** — to clone the repository.
- For the **Docker Compose path** (recommended): **Docker** and the **Compose
  plugin**. You do not need Go or a database installed locally — Compose builds
  the image and brings up TimescaleDB for you.
- For the **from-source path**: **Go 1.25 or newer** (the module targets Go
  1.25.0) and a reachable **PostgreSQL-compatible database with TimescaleDB**.
  The center stores time-series metrics in TimescaleDB hypertables, so a stock
  PostgreSQL server without the TimescaleDB extension is not sufficient.

## Clone

```bash
git clone https://github.com/akaere/autopeer-center.git
cd autopeer-center
```

## Path A — Docker Compose (recommended)

The Compose stack starts two services together: a TimescaleDB database (`db`)
and the center (`center`). The `center` service waits for the database to report
healthy before it starts.

1. **Create your environment file** from the template:

   ```bash
   cp .env.example .env
   ```

2. **Fill in the minimum values** in `.env`:

   - `DATABASE_URL` — the connection string the center uses. With the bundled
     `db` service it points at the Compose network host `db`, e.g.
     `postgres://autopeer:changeme@db:5432/autopeer?sslmode=disable`.
   - `DB_PASSWORD` — the password for the bundled `db` service. Keep it in sync
     with the password embedded in `DATABASE_URL`.
   - `JWT_SECRET` — the HMAC secret for all issued tokens. **Must be at least 32
     characters**; startup fails otherwise. Use a long random string.
   - `ADMIN_INITIAL_EMAIL` / `ADMIN_INITIAL_PASSWORD` — the bootstrap admin
     account, for example `admin@example.com`.

3. **Build and start the stack:**

   ```bash
   docker compose up --build
   ```

   The `db` service comes up first; its healthcheck runs `pg_isready` until the
   database accepts connections. Only then does `center` start, connect to the
   database, apply any pending migrations, and begin serving on port **8080**.
   The `center` service loads your `.env` via `env_file`.

The Compose file maps the center's persistent state to named volumes so it
survives container rebuilds, plus a volume for the database:

| Volume     | Holds |
|------------|-------|
| `pgdata`   | The TimescaleDB data directory. |
| `releases` | Uploaded agent release binaries served to agents for self-update (`AGENT_RELEASE_DIR`). |
| `cache`    | The on-disk cache used as a fallback when Redis is not configured (`CACHE_DIR`). |
| `keys`     | The center's persistent key file used for the agent handshake (`CENTER_KEY_PATH`). |

The database is published on host port **5432** and the center on host port
**8080**. See [`./deployment.md`](./deployment.md) for volume details, build
metadata, and reverse-proxy setup.

## Path B — from source

To run the center directly on the host against your own TimescaleDB instance:

```bash
# Build the binary
go build ./cmd/center

# Configure
cp .env.example .env   # then edit DATABASE_URL, JWT_SECRET, etc.

# Run
./center
```

Fill in `.env` as in Path A, but point `DATABASE_URL` at your own reachable
PostgreSQL/TimescaleDB instance. `DB_PASSWORD` is only used by the Compose `db`
service, so you can ignore it here.

The center loads `.env` from the working directory if present, then reads
configuration from the environment. You can also run without producing a
separate binary:

```bash
go run ./cmd/center
```

Run the center from the repository root (or wherever the `migrations/` directory
is present) so the migration files can be found.

## First run

On startup the center:

- **Applies migrations automatically.** There is no separate migrate command.
  Migrations in `migrations/*.up.sql` are applied in lexical order under a
  PostgreSQL advisory lock, so concurrent startups are safe.
- **Bootstraps the admin account.** If `ADMIN_INITIAL_EMAIL` and
  `ADMIN_INITIAL_PASSWORD` are set, that admin account is upserted on **every**
  boot. Changing them and restarting resets that account.
- **Listens on `:8080`** by default. Change the in-container listen address with
  `LISTEN_ADDR` (and update the Compose `ports` mapping to match).

## Verify it is up

The center exposes two unauthenticated health endpoints:

```bash
curl http://localhost:8080/health
# {"status":"ok"}

curl http://localhost:8080/healthz
# OK
```

Both return `200 OK` once the HTTP server is serving. `/health` responds with
JSON; `/healthz` responds with plain text.

Once it is healthy, you can authenticate against the API as the bootstrapped
admin (the `ADMIN_INITIAL_EMAIL` / `ADMIN_INITIAL_PASSWORD` account) using the
admin email + password login. See [`./authentication.md`](./authentication.md)
for the login flow and [`./api/README.md`](./api/README.md) for the endpoints.

## Next steps

- [`./configuration.md`](./configuration.md) — the full environment-variable
  reference (Redis, asynq, cache, Sentry, Turnstile, WebAuthn, token TTLs, and
  every other setting).
- [`./deployment.md`](./deployment.md) — production and Docker details: volumes,
  build metadata, reverse proxy, health checks, and migrations.
- [`./architecture.md`](./architecture.md) — how the request flow, services,
  and WebSocket hubs fit together.
- [`./api/README.md`](./api/README.md) — the HTTP API reference. Since the
  frontend is not open-source, this is the only interface to the center.

### Optional: traffic analytics (ClickHouse)

DN42 traffic-sampling analytics are off by default. To enable them, run a
ClickHouse instance (the bundled `docker-compose.yml` ships an optional
`clickhouse` service) and set `CLICKHOUSE_URL` (see
[`./configuration.md`](./configuration.md#clickhouse-traffic-analytics)). The
center creates its analytics tables on startup. The matching node agents must
also have `sampling.enabled` set. Leave `CLICKHOUSE_URL` empty to run without it
— the center degrades gracefully and the feature stays hidden.
