# Deployment

This guide covers running the AutoPeer **center** control plane — the `center`
binary built from the `github.com/akaere/autopeer-center` module. It walks
through the Docker Compose quickstart, a local build-and-run flow, persistent
storage, build metadata, reverse-proxy setup, health checks, migrations, and a
recommended CI pipeline.

For the full list of environment variables, see
[`./configuration.md`](./configuration.md). For database internals (schema,
migrations, TimescaleDB usage), see [`./database.md`](./database.md).

## Prerequisites

- **A PostgreSQL-compatible database with TimescaleDB.** The center stores both
  relational data and time-series metrics (heartbeats, node metrics) in
  TimescaleDB hypertables, so a stock PostgreSQL server without the TimescaleDB
  extension is not sufficient. The Compose file uses the
  `timescale/timescaledb:latest-pg16` image.
- **Go 1.25 or newer**, only if you build the binary yourself (the module
  targets Go 1.25.0). The Docker image builds with `golang:1.25-alpine`, so you
  do not need Go installed locally when using Compose.
- **Docker and the Compose plugin**, if you use the quickstart below.
- **Redis (optional).** Redis provides a shared cache, distributed locks, and a
  background job queue. It is not required: when `REDIS_URL` is empty the center
  falls back to a local on-disk cache. Enable it by setting `REDIS_URL`. See
  [`./configuration.md`](./configuration.md) for the related Redis options.

## Docker Compose quickstart

The Compose stack starts two services: a TimescaleDB database (`db`) and the
center (`center`). They come up together — `center` waits for the database to
report healthy before it starts.

1. **Create your environment file** from the template and edit it:

   ```bash
   cp .env.example .env
   ```

   At minimum, set the required values:

   - `DATABASE_URL` — the connection string the center uses. With the bundled
     `db` service the default points at the Compose network host `db`, e.g.
     `postgres://autopeer:changeme@db:5432/autopeer?sslmode=disable`. Use
     `sslmode=require` in production.
   - `DB_PASSWORD` — the password for the bundled `db` service. Keep it in sync
     with the password embedded in `DATABASE_URL`.
   - `JWT_SECRET` — the HMAC secret for all issued tokens. Use a long random
     string (32+ characters).
   - `ADMIN_INITIAL_EMAIL` / `ADMIN_INITIAL_PASSWORD` — the bootstrap admin
     account. These are upserted on every startup, so changing them and
     restarting resets that account (for example `admin@example.com`).

2. **Build and start the stack:**

   ```bash
   docker compose up --build
   ```

   The `db` service comes up first; its healthcheck runs `pg_isready` until the
   database accepts connections. Only then does `center` start. On startup the
   center connects to the database, runs any pending migrations, and begins
   serving.

3. **Verify it is up.** The database listens on host port **5432** and the
   center on host port **8080**:

   ```bash
   curl http://localhost:8080/healthz
   ```

The published ports are defined in `docker-compose.yml`:

| Service  | Container port | Host port |
|----------|----------------|-----------|
| `db`     | 5432           | 5432      |
| `center` | 8080           | 8080      |

Both services use `restart: unless-stopped`, so they recover automatically after
a host reboot.

> The center listens on `:8080` by default. You can change the in-container
> listen address with `LISTEN_ADDR`, but then update the Compose `ports`
> mapping to match.

## Local build and run

To run the center directly on the host (against your own TimescaleDB instance):

```bash
# Build the binary
go build ./cmd/center

# Configure
cp .env.example .env   # then edit DATABASE_URL, JWT_SECRET, etc.

# Run
./center
```

The center loads `.env` from the working directory if present, then reads
configuration from the environment. Point `DATABASE_URL` at a reachable
TimescaleDB instance. When running outside a container, the persistent paths
default to local directories rather than `/var/lib/autopeer-center` — see
[`./configuration.md`](./configuration.md) for the `AGENT_RELEASE_DIR`,
`CACHE_DIR`, and `CENTER_KEY_PATH` defaults.

You can also run without building a separate binary:

```bash
go run ./cmd/center
```

## Persistent data and volumes

The Docker image declares `VOLUME ["/var/lib/autopeer-center"]` and creates that
directory. All long-lived center state lives under that path:

| Path                                          | Contents |
|-----------------------------------------------|----------|
| `/var/lib/autopeer-center/releases`           | Uploaded agent release binaries (`AGENT_RELEASE_DIR`) served to agents for self-update. |
| `/var/lib/autopeer-center/cache`              | On-disk cache used as a fallback when Redis is not configured (`CACHE_DIR`). |
| `/var/lib/autopeer-center/center_keys.json`   | The center's persistent key pair used for the agent handshake (`CENTER_KEY_PATH`). |

The Compose file maps these to named volumes so they survive container
rebuilds, plus a separate volume for the database:

| Volume     | Mounted at                              | Purpose |
|------------|-----------------------------------------|---------|
| `releases` | `/var/lib/autopeer-center/releases`     | Agent release binaries. |
| `cache`    | `/var/lib/autopeer-center/cache`        | Fallback cache. |
| `keys`     | `/var/lib/autopeer-center`              | Center key file (and anything else under the base dir). |
| `pgdata`   | `/var/lib/postgresql/data`              | TimescaleDB data directory. |

> Keep the persistent path environment variables in your `.env` consistent with
> these mounts. If you change `AGENT_RELEASE_DIR`, `CACHE_DIR`, or
> `CENTER_KEY_PATH`, update the volume targets in `docker-compose.yml` to match,
> otherwise that data will not persist.

To back up center state, snapshot the `pgdata`, `releases`, and `keys` volumes.
Losing `center_keys.json` (the `keys` volume) invalidates the cryptographic
identity that agents trust, so guard it carefully.

## Build metadata (version, commit, build date)

The binary embeds version information through `-ldflags`. The variables
`main.CommitHash`, `main.BuildDate`, and `main.Version` default to `dev`,
`unknown`, and `dev` respectively, and are overridden at build time.

The Dockerfile accepts three build args and injects them:

```dockerfile
ARG COMMIT_HASH=dev
ARG BUILD_DATE=unknown
ARG VERSION=dev

RUN CGO_ENABLED=0 GOOS=linux go build \
  -ldflags="-X 'main.CommitHash=${COMMIT_HASH}' -X 'main.BuildDate=${BUILD_DATE}' -X 'main.Version=${VERSION}'" \
  -o /center ./cmd/center
```

The Compose `center` service forwards these from the environment, falling back
to the defaults if unset:

```yaml
build:
  context: .
  args:
    COMMIT_HASH: ${COMMIT_HASH:-dev}
    BUILD_DATE: ${BUILD_DATE:-unknown}
    VERSION: ${VERSION:-dev}
```

To stamp a build with real values:

```bash
COMMIT_HASH=$(git rev-parse --short HEAD) \
BUILD_DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ) \
VERSION=v1.0.0 \
docker compose build
```

Or build the binary directly with the same ldflags:

```bash
go build \
  -ldflags="-X 'main.CommitHash=$(git rev-parse --short HEAD)' -X 'main.BuildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)' -X 'main.Version=v1.0.0'" \
  -o center ./cmd/center
```

## Running behind a reverse proxy

In production you will typically place the center behind a reverse proxy (nginx,
Caddy, Traefik, etc.) that terminates TLS and forwards to port 8080. So that the
center logs the real client IP instead of the proxy's address, set
**`TRUSTED_PROXY_CIDR`** to the proxy's address range(s):

```env
TRUSTED_PROXY_CIDR=10.0.0.0/8,127.0.0.1
```

`TRUSTED_PROXY_CIDR` accepts a comma-separated list of CIDR blocks; a bare IP
address is treated as a `/32`. When a request arrives from an address inside one
of these ranges, the center trusts the `X-Real-IP` header (or the first entry of
`X-Forwarded-For`) as the client IP. Requests from any other source use the
direct connection address and the forwarding headers are ignored. Leaving
`TRUSTED_PROXY_CIDR` empty disables this entirely, so always set it when behind a
proxy and make sure the proxy actually sends `X-Real-IP` or `X-Forwarded-For`.

Set the public-facing values to match your deployment, for example:

```env
EXTERNAL_URL=https://your-center.example.com
CORS_ORIGIN=https://your-center.example.com
```

`EXTERNAL_URL` is used to build agent download links; `CORS_ORIGIN` controls
which browser origin may call the API. Agents and bots connect over WebSocket,
so the proxy must allow WebSocket upgrades on the API paths (for example
`wss://your-center.example.com/api/v1/agent/ws`). See
[`./configuration.md`](./configuration.md) for the full set of network options.

## Health checks

The center exposes two unauthenticated health endpoints, suitable for load
balancers, container orchestrators, and uptime monitors:

| Endpoint    | Response type        | Body              |
|-------------|----------------------|-------------------|
| `/health`   | `application/json`   | `{"status":"ok"}` |
| `/healthz`  | `text/plain`         | `OK`              |

Both return `200 OK` once the HTTP server is serving. Examples:

```bash
curl https://your-center.example.com/health
# {"status":"ok"}

curl https://your-center.example.com/healthz
# OK
```

## Database migrations

Migrations run **automatically on every startup** — there is no separate migrate
command to invoke. On boot the center:

1. Creates the `schema_migrations` bookkeeping table if it does not exist.
2. Takes a PostgreSQL advisory lock (`20260428`) so that only one instance
   applies migrations at a time, making concurrent startups safe.
3. Reads `migrations/*.up.sql` and applies them in lexical (filename) order,
   skipping any version already recorded in `schema_migrations`.
4. Each migration is applied inside a transaction by default. A migration whose
   first line contains `autopeer:no-transaction` is applied statement by
   statement without a wrapping transaction (used for operations that cannot run
   inside one).

In the Docker image the migration files are copied to `/migrations`, and the
binary resolves them relative to its working directory, so the migrations ship
inside the image automatically. When running locally, run the center from the
repository root (or wherever the `migrations/` directory is present) so the files
can be found; if no migration files are found, startup logs a message and
continues.

See [`./database.md`](./database.md) for the migration file conventions and
schema details.

## Continuous integration

The repository policy is to run a full build and the test suite before merging.
A minimal GitHub Actions workflow that mirrors this policy:

```yaml
# .github/workflows/ci.yml
name: CI

on:
  push:
  pull_request:

jobs:
  build-and-test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.25'
      - name: Build
        run: go build ./...
      - name: Test
        run: go test ./...
```

`go build ./...` compiles every package, and `go test ./...` runs the full test
suite. Run the same two commands locally before pushing:

```bash
go build ./...
go test ./...
```

## See also

- [`./configuration.md`](./configuration.md) — every environment variable and
  its default.
- [`./database.md`](./database.md) — schema, TimescaleDB, and migration details.
