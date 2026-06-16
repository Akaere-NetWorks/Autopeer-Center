# Flap (BGP route-flap monitor) API

Public, no-authentication endpoints exposing BGP route-flap data for a public
dashboard, plus the dedicated WebSocket endpoint that a `flapalerted-agent`
connects to.

Center holds **no flap state**. A flap agent maintains the live detection state
in memory and connects to center as a WebSocket client. On a public request,
center sends a query over the WebSocket to the connected agent and relays the
reply, with a short (~3s) in-memory cache so many concurrent public clients
collapse to at most one agent query per window. See
[../websocket-protocol.md](../websocket-protocol.md) for the wire protocol and
the admin-managed agent allowlist (the `flap_agents` table; see
[Admin](./admin.md) and [../configuration.md](../configuration.md)).

Base URL: `https://your-center.example.com`

When a `?agent=` parameter is omitted, snapshot/metrics responses are a **merged**
view across all connected agents (summed metrics, unioned prefixes/peers, summed
time series). When no agent is connected, endpoints return an **empty** payload
with `200` (never `500` / `503`, except the stream — see below).

---

## `GET /api/v1/flap/agents`
List the connected flap agents and their capabilities.

- **Auth:** public
- **Query parameters:** None.
- **Response `200`:**

  | Field | Type | Description |
  |---|---|---|
  | `agents` | array | Connected agents. |
  | `agents[].agent_id` | string | Agent identity / hub key. |
  | `agents[].capabilities` | object | Detector configuration (see below). |
  | `agents[].online` | bool | Always `true` for listed agents. |

  `capabilities`: `{ version, route_change_counter, over_threshold_target, under_threshold_target, add_path }`.

- **Source:** `internal/handler/flap.go` → `FlapHandler.ListAgents`

## `GET /api/v1/flap/snapshot`
Return the current snapshot for one agent, or a merged view of all.

- **Auth:** public
- **Query parameters:** `agent` (optional) — restrict to one agent id.
- **Response `200`:** a snapshot object:

  | Field | Type | Description |
  |---|---|---|
  | `capabilities` | object | Detector configuration. |
  | `metric` | object | `active_flap_count`, `active_flap_total_path_change_count`, `total_path_change_count`, `average_route_changes_90`, `sessions`. |
  | `stats` | array | Time series; each point `{ time, changes, listed_changes, active, route_count }` (≤51 points, 5s apart). |
  | `active_flaps` | array | Each `{ prefix, first_seen, rate_sec, total_count }` (≤100). |
  | `peers` | array | Each `{ asn, rate_sec, rate_sec_avg }` (≤30). |
  | `sessions` | array | Each `{ remote, router_id, hostname, establish_time, import_count }`. |
  | `session_count` | int | Number of established BGP sessions. |
  | `generated_at` | int | Unix seconds when the snapshot was built. |

- **Source:** `internal/handler/flap.go` → `FlapHandler.Snapshot`

## `GET /api/v1/flap/prefix`
Return the AS-path change history for one prefix.

- **Auth:** public
- **Query parameters:** `prefix` (required, CIDR), `agent` (optional; defaults to
  the first connected agent).
- **Response `200`:**

  | Field | Type | Description |
  |---|---|---|
  | `found` | bool | Whether the prefix is currently an active flap. |
  | `prefix` | string | The queried prefix. |
  | `first_seen` | int | Unix seconds. |
  | `rate_sec` | int | Recent change rate per second (`-1` until first interval). |
  | `total_count` | int | Total path changes recorded. |
  | `path_history` | array | Each `{ path: [asn...], ac, wc }` (announcement/withdrawal counts). |

- **Errors:** `400 bad_request` when `prefix` is missing.
- **Source:** `internal/handler/flap.go` → `FlapHandler.Prefix`

## `GET /api/v1/flap/metrics`
Return the headline gauge metric for one agent or summed across all.

- **Auth:** public
- **Query parameters:** `agent` (optional).
- **Response `200`:** the `metric` object described under `/flap/snapshot`.
- **Source:** `internal/handler/flap.go` → `FlapHandler.Metrics`

## `GET /api/v1/flap/stream`
Server-Sent Events stream of snapshots for one agent (the dashboard live feed).

- **Auth:** public
- **Query parameters:** `agent` (optional; defaults to the first connected agent).
- **Response:** `Content-Type: text/event-stream`. On connect, center replays the
  current stat series as `event: c` (one per point), then `event: ready` with the
  full snapshot, then emits `event: u` with a fresh snapshot every 5 seconds.
- **Errors:** `503 no_agent` when no flap agent is connected.
- **Source:** `internal/handler/flap.go` → `FlapHandler.Stream`

---

## `GET /api/v1/flap/agent/ws` — agent WebSocket
Dedicated WebSocket endpoint for a `flapalerted-agent` (not a browser client).

- **Auth:** `X-Flap-Token` header matching an enabled row in the `flap_agents`
  table (admin-managed). The token resolves to the agent id; if the client also
  supplies `X-Flap-Agent-ID` (or `?agent_id=`) it must equal the token-bound id.
  After the upgrade the agent must complete the X25519 `key.init` key exchange
  (TOFU pubkey pinning) before it is registered and queryable — see
  [../websocket-protocol.md](../websocket-protocol.md).
- **Errors:** `401` (missing/invalid token), `403` (agent id mismatch).
- **Protocol:** see [../websocket-protocol.md](../websocket-protocol.md).
- **Source:** `internal/handler/flap.go` → `FlapHandler.HandleWebSocket`
