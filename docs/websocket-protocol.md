# WebSocket Protocol

AutoPeer Center talks to three kinds of long-lived WebSocket clients:

- **Agents** — one process per physical node, connected to `GET /api/v1/agent/ws`. Agents install/tear down WireGuard + BIRD config and stream telemetry.
- **Bots** — a chat/diagnostics frontend connected to `GET /api/v1/bot/ws`. Bots issue diagnostics, manage user bindings, and perform user/admin peer operations on behalf of chat users.
- **Flap agents** — a `flapalerted-agent` BGP route-flap detector connected to `GET /api/v1/flap/agent/ws`. The agent holds all flap state in memory; center pulls it on demand and relays it to the public flap endpoints.

The agent and bot endpoints share a single `Hub` (`internal/ws/hub.go`) — agents keyed by `node_id`, bots keyed by a generated connection ID. Flap agents use a separate `FlapHub` (`internal/ws/flap_hub.go`) keyed by `agent_id`.

For the HTTP API that sits in front of these endpoints, see [`./api/README.md`](./api/README.md).

> The frontend that consumes these endpoints is not open-sourced; this document is the authoritative protocol reference.

---

## Wire envelope

Every message in both directions is a single JSON object with the same shape (`internal/ws/protocol.go`):

```jsonc
{
  "type":    "peer.add",   // string, required — message type
  "id":      "f1e2...",    // string, optional — UUID for request/response correlation
  "payload": { /* ... */ },// any, optional — type-specific body
  "success": true,         // bool, optional — set on `response` messages
  "error":   "..."         // string, optional — error detail when something failed
}
```

```go
type Message struct {
    Type    string `json:"type"`
    ID      string `json:"id,omitempty"`
    Payload any    `json:"payload,omitempty"`
    Success *bool  `json:"success,omitempty"`
    Error   string `json:"error,omitempty"`
}
```

### Request/response correlation

When the center sends a command that expects a reply, it generates a UUID and puts it in `id`. The remote side echoes that same `id` back in its reply, and the center matches the reply to the waiting caller by `id` (`Hub.SendCommandContext`). Each in-flight request has its own response channel registered under the `id`.

### 30-second command timeout

`Hub.SendCommand` wraps every command in a 30-second context deadline:

```go
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
```

A command fails fast (without waiting the full 30s) if the agent is not connected, has not completed its handshake, the send buffer is full, or the connection drops mid-command.

---

## Agent endpoint — `/api/v1/agent/ws`

### Transport auth at upgrade

Before the WebSocket is upgraded, `AgentHandler.HandleWebSocket` (`internal/handler/agent.go`) requires one of two transport-level credentials:

1. **Token auth** — header `X-Agent-Token`. The token is looked up to resolve the `node_id`. An unknown token is rejected with `401 invalid token`.
2. **Key-auth mode** — header `X-Node-ID` (or `?node_id=` query parameter). The node must already have a registered agent public key; otherwise the upgrade is rejected with `401 key auth not available for this node`. The supplied `node_id` is truncated to 128 characters.

If neither is present the upgrade is rejected with `401 missing token or node_id`.

**Rate limiting (key-auth mode only)** — `checkKeyAuthRate` enforces a minimum 10-second interval both per source IP and per `IP + node_id` pair, over a rolling 1-minute window. Exceeding it returns `429 too many key-auth attempts`. (Token auth is not rate-limited here.)

**Origin check** — for the agent endpoint, a missing `Origin` header is allowed only when `X-Agent-Token` or `X-Node-ID` is present (native agents do not send an `Origin`); otherwise the origin must be in the configured allow-list (`CORS_ORIGIN`).

Transport auth only establishes *which node is connecting*. The connection is registered in the hub in a `pendingAuth` state and is **not trusted** for commands until the cryptographic handshake below completes.

### X25519 + ChaCha20-Poly1305 handshake

After upgrade, the agent performs an ECDH key exchange against the center's persistent key pair before any commands flow. The center key pair is loaded from `CENTER_KEY_PATH`. Primitives live in `internal/crypto/` (X25519 ECDH, HKDF-derived keys, ChaCha20-Poly1305 AEAD). The handshake is four messages (`internal/ws/handshake.go`):

```
agent                                   center
  │ ── key.init {pubkey} ───────────────▶ │  ECDH against persistent keypair
  │                                        │  TOFU-store agent pubkey
  │ ◀──────── key.init_ack {pubkey,nonce} │  encryption enabled (init key)
  │                                        │
  │ ── key.auth {node_id,pubkey,          │  verify HMAC proof; mint ephemeral key
  │              nonce,proof} ───────────▶ │  derive session key
  │ ◀── key.auth_ack {pubkey(ephemeral),  │  session key enabled — agent trusted
  │                   auth_nonce,          │
  │                   center_nonce}        │
```

1. **`key.init`** (agent → center) — `{ "pubkey": "<hex>" }`. The center derives a shared secret via ECDH between its persistent private key and the agent public key, then derives an encryption key from a fresh nonce.

   **TOFU (trust-on-first-use) pubkey storage** — on first connect, the agent's public key is stored on the node record. On later connects:
   - same pubkey → idempotent, accepted;
   - **different** pubkey already registered → rejected. The center replies `key.init_ack` with `error: "reset_required"`, calls `notifyPubkeyReset(node_id)`, and closes the connection. An operator must clear the stored pubkey to re-pair.

2. **`key.init_ack`** (center → center → agent) — `{ "pubkey": "<center hex>", "nonce": <bytes> }`. The center enables encryption with the init-derived key and activates the connection.

3. **`key.auth`** (agent → center) — `{ "node_id", "pubkey", "nonce": <bytes>, "proof": <bytes> }`. The center re-validates that the node exists with a stored pubkey, that the claimed `node_id` matches the connection's established `node_id`, and that the stored pubkey matches the presented one. It then verifies the HMAC `proof` over the nonce using the shared secret. On success it generates an **ephemeral** key pair and derives a fresh session key from `ephemeral · agentPubkey` combined with `auth_nonce ‖ center_nonce`.

4. **`key.auth_ack`** (center → agent) — `{ "pubkey": "<ephemeral hex>", "auth_nonce": <bytes>, "center_nonce": <bytes> }`. The center switches the connection to the ephemeral **session key** and marks the agent fully active. Only at this point will `peers.sync` and commands be served.

After the handshake, application frames are **ChaCha20-Poly1305-encrypted binary** WebSocket messages; plaintext JSON frames are only used during the handshake itself. Liveness uses WebSocket ping/pong on a 30-second interval with a 90-second read deadline.

### Agent message types

All constants are in `internal/ws/protocol.go`.

#### Handshake

| Type | Direction | Payload |
|---|---|---|
| `key.init` | agent → center | `{ pubkey }` |
| `key.init_ack` | center → agent | `{ pubkey, nonce }` |
| `key.auth` | agent → center | `{ node_id, pubkey, nonce, proof }` |
| `key.auth_ack` | center → agent | `{ pubkey, auth_nonce, center_nonce }` |

#### Center → agent (commands)

| Type | Payload | Summary |
|---|---|---|
| `peer.add` | `PeerAddPayload` | Install a WireGuard + BIRD peer. Fields: `peer_id`, `asn`, `remote_endpoint`, `remote_wg_pubkey`, `remote_lla`, `listen_port`, `wg_interface`, optional `mtu`, optional `wg_preshared_key`. |
| `peer.remove` | `PeerRemovePayload` | Tear down a peer. Fields: `peer_id`, `asn`. |
| `bird.disable` | `BirdControlPayload` | Disable a BGP protocol for a peer. Fields: `peer_id`, `proto_name`. |
| `bird.enable` | `BirdControlPayload` | Re-enable a BGP protocol for a peer. |
| `bird.details` | — | Request BIRD protocol detail for diagnostics. |
| `peers.import` | — | Import/bulk-apply peer state. |
| `status.request` | — | Request a `status.response` snapshot from the agent. |
| `agent.update` | — | Instruct the agent to self-update its binary (served from `AGENT_RELEASE_DIR`). |
| `agent.rollback` | — | Roll back to the previous agent binary. |
| `agent.resume` | — | Resume normal operation after an update window. |

#### Agent → center

| Type | Payload | Summary |
|---|---|---|
| `response` | echoed `id` + `success` (+ `error`/`payload`) | Reply to a center command; matched by `id`. |
| `status.response` | — | Reply to `status.request`; also routed by `id`. |
| `heartbeat` | `HeartbeatPayload` | Periodic telemetry (see below). |
| `traffic.report` | per-window sampling analytics | Optional DN42 packet-sampling analytics, one message per window per chunk of interfaces. Persisted to ClickHouse when configured; **dropped immediately when ClickHouse is not configured** (`h.traffic == nil`). Carries `node_id`, `window_start/end`, `sample_ratio`, and an `interfaces[]` array (per-ASN sampled byte/packet counts, v4/v6 split, fixed `proto` map, 8-bucket size histogram, and Top-N source/dest/port tables). Headers only — never payload. |
| `agent.updating` | — | Agent announces it is updating; the node is marked `updating` and offline-notify is suppressed for the window. |

#### Bidirectional

| Type | Payload | Summary |
|---|---|---|
| `peers.sync` | `PeersSyncPayload` | The agent may request the full active-peer list for its node; the center pushes the authoritative list and the agent reconciles its WireGuard/BIRD config to match (creating missing peers, removing stale ones). Also pushed proactively by the reconcile worker via `TriggerPeersSync`. Rejected while the connection is still `pendingAuth`. Each `PeerSyncEntry` carries the full peer wiring plus `bgp_proto_name` and `bird_config_filename`. |

#### Network diagnostics (center → agent, request/response)

Triggered on behalf of bot diagnostics; each returns a `response` correlated by `id`.

| Type | Payload | Summary |
|---|---|---|
| `network.ping` | `NetworkPingPayload` `{ target, count? }` | ICMP ping from the node. |
| `network.trace` | `NetworkTracePayload` `{ target, max_hops? }` | Traceroute from the node. |
| `network.mtr` | `NetworkMTRPayload` `{ target, cycles? }` | MTR from the node. |
| `network.bgp_route` | `NetworkBGPRoutePayload` `{ target, detailed? }` | BGP route lookup on the node. |

### Heartbeat → metrics hypertables

`handleHeartbeat` (`internal/ws/heartbeat.go`) processes each `heartbeat`. The `HeartbeatPayload` contains:

- `node_id`, `timestamp`, `version`
- optional `node_metrics` (`mem_alloc_mb`, `mem_sys_mb`, `num_goroutine`, `uptime_secs`)
- `peers[]`, each a `PeerHeartbeat`: `peer_id`, `asn`, `wg_rx_bytes`, `wg_tx_bytes`, `wg_last_handshake`, `bgp_state`, `rtt_ms`, optional `routes_imported` / `routes_exported` / `routes_preferred` / `bgp_uptime_secs`, optional `wg_actual_endpoint`.

The center writes telemetry to two TimescaleDB hypertables:

- **`peer_metrics`** — one row per peer per heartbeat (`InsertPeerMetricFromHeartbeat`): byte counters, BGP state, RTT, route counts, last handshake, actual endpoint.
- **`node_metrics`** — node-level runtime stats (`InsertNodeMetric`): memory, goroutine count, uptime.

A heartbeat also: updates the node's reported `agent_version` (and sends an admin notification on version change), marks the node back-online if it had been flagged offline, and runs per-peer alerting (BGP-down, BGP-recovered, stale-handshake) gated by operator settings and notification preferences.

### Traffic report → ClickHouse

`handleTrafficReport` (`internal/ws/traffic.go`) processes each `traffic.report`,
but only when a ClickHouse store is configured — otherwise it returns
immediately and the report is discarded. For each interface it resolves the
authenticated `(node_id, asn)` to a `peers.id` (cached briefly to avoid hitting
Postgres per row) and batch-inserts into two ClickHouse MergeTree tables:
`traffic_samples` (one row per interface per window: scalar counters plus
protocol/size `Map` columns) and `traffic_top` (one row per Top-N entry). The
authenticated connection's `node_id` is authoritative; the payload's own
`node_id` is ignored. See [database.md](database.md) for the table schemas.

---

## Bot endpoint — `/api/v1/bot/ws`

### Origin check

`BotHandler` (`internal/handler/bot.go`) requires a known `Origin` header on the upgrade — it must match the configured allow-list (`CORS_ORIGIN`). Unlike the agent endpoint, there is **no** transport-level token; an empty `Origin` is rejected outright.

### In-band `bot.auth` handshake

The bot authenticates as its **first** WebSocket message (`internal/ws/bot_conn.go`, `internal/ws/bot_handler.go`). The read deadline starts at 5 seconds; after successful auth it relaxes to 120 seconds, with ping/pong every 30 seconds.

1. **`bot.auth`** (bot → center) — `{ "token": "<secret>" }`. `handleBotAuth`:
   - rejects if the `bot_enabled` setting is not `"true"`;
   - rejects an empty token;
   - compares the token against the `bot_auth_token` setting. A bcrypt hash is preferred; a legacy plaintext value is accepted via constant-time compare and then transparently migrated to a bcrypt hash.
2. **`bot.auth_ack`** (center → bot) — `{ "success": bool, "error"?: string }`. On success the connection is registered and the center immediately pushes a `bot.settings_update` snapshot of all bot-related settings.

Any non-`bot.auth` message received before authentication closes the connection.

### Concurrency and feature gating

After auth, each command runs through a shared concurrency **semaphore** (`botSem`). If the semaphore is saturated, the command returns its `*_result` immediately with `error: "too many concurrent commands"` instead of blocking. Diagnostics commands are additionally gated by per-feature settings (e.g. `bot_ping_enabled`); a disabled feature returns a result with a "command is disabled" error.

### Bot message types (overview)

Bot operations follow a request → `*_result` pattern correlated by `id`. Every payload has a typed struct in `internal/ws/protocol.go`. Categories:

**Diagnostics** — fan out to online nodes via the network-diagnostic commands above and aggregate per-node results.

| Request | Result | Summary |
|---|---|---|
| `bot.ping` | `bot.ping_result` | Ping `target` from one or all online nodes (`BotPingResult` / `PingResultEntry`). |
| `bot.trace` | `bot.trace_result` | Traceroute `target` (`BotTraceResult` / `TraceResultEntry`). |
| `bot.status` | `bot.status_result` | Fleet summary: node/peer counts (`BotStatusResult`). |
| `bot.nodes` | `bot.nodes_result` | List enabled nodes (`BotNodesResult` / `BotNodeInfo`). |

Diagnostic targets must be public IP addresses; loopback, link-local, private (RFC 1918 / `169.254`), multicast, broadcast, and unspecified addresses are rejected (`validateBotTarget`), and hostnames are not allowed.

**Telemetry / push** (no result expected):

| Type | Summary |
|---|---|
| `bot.command_stats` | bot → center: record a command-usage stat. |
| `bot.settings_update` | center → bot: push current bot settings (sent right after auth). |
| `bot.notify` | center → bot: push an event to a Telegram user or the admin chat. |

**Binding** — link a chat user to a DN42 ASN:

| Request | Result | Summary |
|---|---|---|
| `bot.bind_request` | `bot.bind_pending` / `bot.bind_result` | Start binding; emails a verification code (returns masked email + expiry). |
| `bot.bind_verify` | `bot.bind_result` | Complete binding with the emailed code. |
| `bot.web_bind` | `bot.bind_result` | Bind via a web-issued token. |
| `bot.unbind` | `bot.unbind_result` | Remove a binding. |
| `bot.whoami` | `bot.whoami_result` | Report current binding (ASN, admin flag, bound-via/at). |

**User peer operations** — scoped to the bound user's ASN:

| Request | Result | Summary |
|---|---|---|
| `bot.user_peers` | `bot.user_peers_result` | List the user's peers. |
| `bot.user_peer_detail` | `bot.user_peer_detail_result` | Full detail for one peer. |
| `bot.user_peer_create` | `bot.user_peer_create_result` | Request a new peer. |
| `bot.user_peer_delete` | `bot.user_peer_delete_result` | Delete a peer. |
| `bot.user_peer_metrics` | `bot.user_peer_metrics_result` | Recent metric points for a peer. |
| `bot.user_peer_update` | `bot.user_peer_update_result` | Edit a user's own peer (pubkey, endpoint, LLA, MTU). Active peers are reconfigured on the agent with rollback on failure. |

**Admin operations** — privileged fleet management:

| Request | Result | Summary |
|---|---|---|
| `bot.admin_peers` | `bot.admin_peers_result` | Paginated peer list (optionally by status). |
| `bot.admin_peer_detail` | `bot.admin_peer_detail_result` | Fetch full detail for a single peer by ID. |
| `bot.admin_peer_action` | `bot.admin_peer_action_result` | Approve / reject / suspend / etc. a peer. |
| `bot.admin_nodes` | `bot.admin_nodes_result` | List all nodes with agent state/version. |
| `bot.admin_node_detail` | `bot.admin_node_detail_result` | Detail for one node. |
| `bot.admin_node_update` | `bot.admin_node_update_result` | Trigger an agent update on a node. |
| `bot.admin_audit` | `bot.admin_audit_result` | Paginated audit log. |

---

## Flap agent endpoint — `/api/v1/flap/agent/ws`

A `flapalerted-agent` connects as a WebSocket **client**; center is the server.
The agent passively peers BGP, detects route flaps, and holds all state in
memory. Center never persists flap data — it issues a request/response RPC to the
connected agent whenever a public flap endpoint is hit, caches the reply for ~3s,
and polls every 5s while there are SSE subscribers. Handled by `FlapConn` /
`FlapHub` (`internal/ws/flap_conn.go`, `flap_hub.go`) and `FlapHandler`
(`internal/handler/flap.go`).

### Transport auth at upgrade

The agent sends an `X-Flap-Token` header matching an enabled row in the
`flap_agents` table (admin-managed; see [configuration.md](./configuration.md)
and [database.md](./database.md)). The token resolves to the agent id; a
client-supplied `X-Flap-Agent-ID` / `?agent_id=` must equal it.

### App-layer key exchange (required)

The token only bootstraps the connection; like the node agent, the flap agent
must then complete an X25519 key exchange before it is trusted and becomes
queryable. On connect the agent sends `key.init` with its public key; center
pins it on first connect (TOFU) in `flap_agents.agent_pubkey`, rejects a changed
key with `key.init_ack { error: "reset_required" }` (cleared by an admin via
`POST /api/v1/admin/flap/agents/{id}/reset-pubkey`), and otherwise replies with
`key.init_ack { pubkey, nonce }`. Both sides derive a ChaCha20-Poly1305 session
key; all subsequent frames are binary-encrypted. Until the exchange completes the
connection is *not* registered in the `FlapHub`, so the public `/api/v1/flap/*`
endpoints do not see it. A new connection for an existing id replaces the old one.

### Flap message types

Request/response are correlated by the `id` field, like the agent endpoint. The
agent answers each `*.query` with the matching reply type.

| center → agent | agent → center | Purpose |
|---|---|---|
| — | `key.init` | Sent by the agent first: `{ pubkey }`. Center pins it (TOFU) and replies with `key.init_ack`. |
| `key.init_ack` | — | `{ pubkey, nonce }` (or `{ error: "reset_required" }`). Enables encryption. |
| — | `flap.register` | Sent by the agent on connect (after key exchange): `{ agent_id, capabilities }`. Updates the hub's view of the agent's detector config and last-seen time. |
| `flap.query` | `flap.snapshot` | Full snapshot (metric, stat series, active flaps, peers, sessions). |
| `flap.prefix.query` | `flap.prefix` | Per-prefix AS-path change history for `{ prefix }`. |
| `flap.metrics.query` | `flap.metrics` | Headline gauge metric only. |

Payload shapes are documented in [api/flap.md](./api/flap.md) (the public
endpoints return them verbatim).

---

## Connection lifecycle summary

| | Agent | Bot | Flap agent |
|---|---|---|---|
| Endpoint | `GET /api/v1/agent/ws` | `GET /api/v1/bot/ws` | `GET /api/v1/flap/agent/ws` |
| Transport auth | `X-Agent-Token`, or key-auth (`X-Node-ID` / `?node_id=`, rate-limited) | none (Origin check only) | `X-Flap-Token` (DB allowlist) |
| App-layer auth | X25519 + ChaCha20-Poly1305 handshake (`key.init`…`key.auth_ack`) | in-band `bot.auth` → `bot.auth_ack` | X25519 + ChaCha20-Poly1305 (`key.init` → `key.init_ack`, TOFU) |
| Encrypted frames | yes (binary, after handshake) | no (plaintext JSON) | yes (binary, after key exchange) |
| Read deadline | 90s (ping/pong every 30s) | 5s pre-auth, then 120s (ping/pong every 30s) | 90s (ping/pong every 30s) |
| Keyed by | `node_id` | generated connection ID | `agent_id` |

For HTTP endpoints, peer lifecycle, and configuration, see [`./api/README.md`](./api/README.md).
