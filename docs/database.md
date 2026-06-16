# Database

The AutoPeer `center` stores all of its state in a single **TimescaleDB** database.
TimescaleDB is a PostgreSQL extension, so the database is fully PostgreSQL-compatible:
`center` connects with the standard `pgx` driver and the connection string in
[`DATABASE_URL`](./configuration.md). Any tooling that speaks PostgreSQL (`psql`,
`pg_dump`, migrations, ORMs) works unchanged.

## Why TimescaleDB (not plain PostgreSQL)

`center` mixes two very different access patterns in one store:

- **Relational data** — admins, nodes, peers, sessions, settings, audit logs, and
  similar records. These are ordinary PostgreSQL tables with primary keys, foreign
  keys, and unique constraints.
- **Time-series data** — high-volume, append-only metrics keyed by time. These are
  stored in TimescaleDB **hypertables**, which transparently partition rows into
  time-based chunks and support compression and automatic retention policies.

The three hypertables are:

| Hypertable | Time column | Written by | Purpose |
|---|---|---|---|
| `peer_metrics` | `time` | agent heartbeats | per-peer WireGuard byte counters, BGP state, RTT, uptime, etc. |
| `node_metrics` | `time` | agent heartbeats | per-node process metrics (memory, goroutines, uptime) |
| `request_logs` | `created_at` | request-logging middleware | HTTP access log (method, path, status, IP, duration) |

Because metric ingestion is continuous and unbounded, plain PostgreSQL would grow
without limit and slow down range scans. TimescaleDB solves this with native chunk
partitioning, columnar **compression policies**, and **retention policies** that drop
old chunks automatically. For example, `peer_metrics` is segment-compressed by
`peer_id` and ordered by `time DESC`, with a compression policy after one day and a
retention policy that prunes old data. `node_metrics` carries its own retention
policy. These features require the TimescaleDB extension; a vanilla PostgreSQL server
will fail when a migration calls `create_hypertable(...)` or `add_retention_policy(...)`.

> The official Docker Compose setup ships a TimescaleDB image, so a local stack works
> out of the box. See the [deployment guide](./deployment.md) for details.

## How migrations work

Migrations run **automatically on every startup** of `center`, before the HTTP server
begins serving traffic. The logic lives in
[`cmd/center/migrate.go`](../cmd/center/migrate.go).

### The mechanism

1. **Bookkeeping table.** `center` ensures a `schema_migrations` table exists:

   ```sql
   CREATE TABLE IF NOT EXISTS schema_migrations (
       version    TEXT PRIMARY KEY,
       applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
   );
   ```

   Each row records one applied migration by its `version` string.

2. **Advisory lock.** Before doing anything, `center` takes a PostgreSQL session-level
   advisory lock (`pg_advisory_lock(20260428)`) and releases it when done. This
   serializes migrations so that if several `center` instances start at once, only one
   runs migrations at a time — the others wait, then find everything already applied.

3. **Discovery & ordering.** Files matching `migrations/*.up.sql` are globbed and sorted
   **lexically**. Migration filenames are **zero-padded** (e.g. `001_…`, `010_…`,
   `067_…`), so lexical order equals numeric order. The `version` is the filename with
   the `.up.sql` suffix stripped (e.g. `004_create_peers`).

4. **Idempotent application.** For each discovered migration, `center` checks whether its
   `version` already exists in `schema_migrations`. If it does, the file is skipped.
   Otherwise the SQL is executed and the `version` is recorded — both inside a single
   database transaction, so a migration either fully applies and is recorded, or rolls
   back entirely. A failure is fatal: `center` logs the error and exits rather than
   serving with a half-applied schema.

5. **Down files are reference-only.** Every migration normally ships a sibling
   `*.down.sql` describing how to reverse it. **`center` never executes `.down.sql`
   files.** They exist purely as human-readable documentation of the inverse change. (A
   handful of forward-only migrations have no `.down.sql` at all.) There is no automatic
   rollback path; to undo a migration you run its down SQL manually.

### The `no-transaction` directive

Some statements cannot run inside a transaction block (for example certain DDL that
PostgreSQL or TimescaleDB requires to run standalone). To support these, a migration may
opt out of the transaction wrapper by placing a directive on its **first line**:

```sql
-- autopeer:no-transaction
```

When the first line contains `autopeer:no-transaction`, `center` does not wrap the file
in a transaction. Instead it splits the file on `;`, strips blank lines and `--`
comments, and executes each statement individually (auto-committed), then records the
`version` afterwards. Use this only when a statement genuinely cannot run transactionally
— ordinary migrations should stay transactional so they apply atomically.

## Adding a migration

To introduce a schema change:

1. **Pick the next number.** Find the highest existing prefix in `migrations/` and add
   one, keeping the same zero-padded width (e.g. after `067_…` the next is `068_…`).
   Sequential, zero-padded numbers guarantee correct lexical ordering.

2. **Create the up file.** Name it `NNN_short_description.up.sql` and put the forward
   schema change in it. This file's name (minus `.up.sql`) becomes the `version`
   recorded in `schema_migrations`, so it must be unique and must not be renamed once
   it has shipped.

3. **Create the matching down file.** Name it `NNN_short_description.down.sql` with the
   inverse SQL. It is documentation only and is never executed, but keeping it accurate
   helps reviewers and operators reason about rollbacks.

4. **Add the directive only if required.** If your migration contains a statement that
   cannot run in a transaction, make the very first line `-- autopeer:no-transaction`.

5. **Apply it.** Restart `center` (or run the binary) against the target database; the
   new migration is detected, applied once, and recorded. Re-running is a no-op.

Migrations are forward-only in practice and ship as part of the binary's working
directory (the `migrations/` folder is read relative to the process), so deploy the
updated `migrations/` alongside the new `center` build.

## Key tables and hypertables

A non-exhaustive overview of the core schema. The authoritative definitions are the SQL
files in `migrations/`.

### Relational tables

| Table | What it holds |
|---|---|
| `admins` | Admin accounts (email, password hash). The initial admin is upserted from env on startup. |
| `nodes` | Physical peering nodes: name, location, public IP, the node's own ASN and link-local address, WireGuard public key, BIRD/WireGuard config directories, online/enabled flags, and agent metadata. |
| `peers` | Peering sessions: owning `node_id`, remote ASN, remote WireGuard pubkey/endpoint/link-local, contact email, computed WireGuard listen port and interface name, status (`pending` / `active` / `rejected` / `suspended`), reject reason, plus inactivity tracking columns (`last_active_at` updated on each heartbeat with a recent handshake, `inactivity_warning_stage` tracking which warning was last sent). An active-only partial index on `last_active_at` supports the inactivity sweep query. |
| `audit_logs` | Append-only audit trail of admin/user actions (action, operator, target, JSONB detail, timestamp). |
| `agent_releases` | Uploaded `autopeer-agent` binaries available for agent update/rollback. |
| `flap_agents` | Allowlist of `flapalerted-agent` route-flap detectors (admin-managed): `agent_id`, name/description, bearer `token`, TOFU-pinned `agent_pubkey`, `enabled` flag, advertised `version`, and `last_seen_at`. Replaces the former `FLAP_AGENT_TOKENS` / `FLAP_AGENT_PUBKEYS` env allowlist. |
| `auth_sessions` / device & passkey tables | Authentication state: refresh sessions, device grants, WebAuthn/passkey credentials and challenges. |
| `site_settings` / `bot_settings` / notification settings | Operator-configurable runtime settings and notification preferences. |

> The `nodes` and `peers` schemas were extended by later migrations (e.g. ASN columns
> widened from `INTEGER` to `BIGINT` to fit the full DN42 ASN range, plus MTU, preshared
> key, BGP protocol name, and BIRD config filename fields). Check the latest
> `migrations/*.up.sql` for the current column set.

### Hypertables

| Hypertable | Notable details |
|---|---|
| `peer_metrics` | Created via `create_hypertable('peer_metrics', 'time')`. Stores per-peer counters from agent heartbeats (rx/tx bytes, uptime, BGP state, RTT, BGP route counts, last handshake). Compressed (segment by `peer_id`, order by `time DESC`) and pruned by a retention policy. |
| `node_metrics` | Created via `create_hypertable('node_metrics', 'time')`. Stores per-node process stats (`mem_alloc_mb`, `mem_sys_mb`, `num_goroutine`, `uptime_secs`) referencing `nodes(id)`, with its own retention policy. |
| `request_logs` | HTTP access log (`method`, `path`, `status_code`, `ip`, `duration_ms`, `created_at`), indexed on `created_at` for time-range queries. |

## ClickHouse (optional: traffic analytics)

The DN42 traffic-sampling analytics feature stores its high-write, columnar
time-series in a **separate, optional ClickHouse database** rather than the
TimescaleDB primary — keeping the analytical write load off the relational store.
It is enabled only when `CLICKHOUSE_URL` is set (see
[Configuration](./configuration.md)); when unset the feature is disabled
end-to-end and ClickHouse is never contacted.

ClickHouse does **not** use the `migrations/*.up.sql` framework. The schema is
applied idempotently on startup via `CREATE TABLE IF NOT EXISTS`
(`internal/clickhouse/schema.go`). Two `MergeTree` tables are created:

| Table | Notable details |
|---|---|
| `traffic_samples` | One row per interface per sampling window: `time`, `node_id`, `peer_id`, `asn`, `sample_ratio`, scalar sampled/​v4/​v6 packet & byte counters, and `Map(String, UInt64)` columns `proto_pkts` / `proto_bytes` / `size_buckets` (merged at query time with `sumMap`). `PARTITION BY toYYYYMMDD(time)`, `ORDER BY (node_id, peer_id, time)`, `TTL time + INTERVAL 15 DAY`. |
| `traffic_top` | One row per Top-N entry per interface per window: `time`, `node_id`, `peer_id`, `asn`, `kind` (0=src IP, 1=dst IP, 2=dst port), `label`, `pkts`, `bytes`. `ORDER BY (node_id, kind, time, label)`, `TTL time + INTERVAL 7 DAY`. |

The agent only ever captures packet **headers** and reports aggregates and Top-N
talkers — no payload is ever sent or stored. Top-N addresses are restricted to
DN42 ranges.

## See also

- [Configuration](./configuration.md) — `DATABASE_URL` and related settings.
- [Deployment](./deployment.md) — running TimescaleDB and `center` together.
- [Architecture](./architecture.md) — how heartbeats flow into the metric hypertables.
