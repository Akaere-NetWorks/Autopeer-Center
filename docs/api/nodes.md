# Nodes, Releases & Agent Download API

Admin endpoints for managing physical peering nodes, agent binary releases, and the agent-facing binary download endpoint. Most routes require an admin Bearer JWT; the agent download endpoint authenticates with an agent token instead.

Base URL: `https://your-center.example.com`

See also: [`../authentication.md`](../authentication.md), [`../websocket-protocol.md`](../websocket-protocol.md), [`../configuration.md`](../configuration.md), [`./peers.md`](./peers.md), and [`./versioning.md`](./versioning.md).

---

# Nodes

All node endpoints below live under the admin route group and require an admin Bearer JWT.

## `GET /api/v1/admin/nodes`

List all registered nodes with live status, agent state, and aggregated peer/metric data.

- **Auth:** Bearer JWT (admin)
- **Path parameters:** None.
- **Query parameters:** None.
- **Request body:** None.
- **Response `200`:** A bare JSON array of node objects.

  | Field | Type | Description |
  |---|---|---|
  | `id` | string | Node ID. |
  | `name` | string | Node display name. |
  | `location` | string | Node location label. |
  | `public_ip` | string | Public IP of the node. |
  | `our_asn` | integer | The center's ASN on this node. |
  | `our_lla` | string | The center's WireGuard link-local address on this node. |
  | `our_wg_pubkey` | string | The center's WireGuard public key on this node. |
  | `bird_peer_dir` | string | Directory where BIRD peer configs are written. |
  | `wg_dir` | string | Directory where WireGuard configs are written. |
  | `wg_port_prefix` | integer | WireGuard port prefix. |
  | `online` | boolean | Whether the agent is currently connected. |
  | `enabled` | boolean | Whether the node is enabled. |
  | `agent_version` | string | Reported agent version. |
  | `agent_state` | string | Reported agent state. |
  | `created_at` | string | RFC3339 creation timestamp. |
  | `auth_mode` | string | Agent auth mode. |
  | `active_peers` | integer | Count of active peers on this node. |
  | `avg_rtt_ms` | number (nullable) | Average RTT in ms; omitted if absent. |
  | `last_seen` | string (nullable) | Last-seen timestamp; omitted if absent. |
  | `total_rx_mb` | number (nullable) | Total received MB; omitted if absent. |
  | `total_tx_mb` | number (nullable) | Total transmitted MB; omitted if absent. |
  | `mem_alloc_mb` | integer (nullable) | Allocated memory MB; omitted if absent. |
  | `mem_sys_mb` | integer (nullable) | System memory MB; omitted if absent. |
  | `num_goroutine` | integer (nullable) | Goroutine count; omitted if absent. |
  | `uptime_secs` | integer (nullable) | Agent uptime in seconds; omitted if absent. |

  ```json
  [
    {
      "id": "node_a1b2c3",
      "name": "edge-01",
      "location": "Frankfurt",
      "public_ip": "203.0.113.10",
      "our_asn": 4242420000,
      "our_lla": "fe80::1",
      "our_wg_pubkey": "AbCdEf0123456789ExamplePublicKeyValue=",
      "bird_peer_dir": "/etc/bird/dn42/peer/",
      "wg_dir": "/etc/wireguard/",
      "wg_port_prefix": 5,
      "online": true,
      "enabled": true,
      "agent_version": "1.4.2",
      "agent_state": "running",
      "created_at": "2026-01-15T09:30:00Z",
      "auth_mode": "token",
      "active_peers": 12,
      "avg_rtt_ms": 18.4,
      "last_seen": "2026-06-07T12:00:00Z",
      "total_rx_mb": 10240.5,
      "total_tx_mb": 8192.3,
      "mem_alloc_mb": 64,
      "mem_sys_mb": 128,
      "num_goroutine": 42,
      "uptime_secs": 864000
    }
  ]
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 500 | `internal_error` | Failed to fetch nodes. |

- **Source:** `AdminHandler.ListNodes` — `internal/handler/admin_node.go`

## `POST /api/v1/admin/nodes`

Create a new node and generate its agent token (returned once).

- **Auth:** Bearer JWT (admin)
- **Path parameters:** None.
- **Query parameters:** None.
- **Request body:**

  | Field | Type | Required | Description |
  |---|---|---|---|
  | `name` | string | Yes | Node display name. |
  | `location` | string | Yes | Node location label. |
  | `public_ip` | string | Yes | Public IP of the node. |
  | `our_lla` | string | Yes | The center's WireGuard link-local address on this node. |
  | `our_wg_pubkey` | string | Yes | The center's WireGuard public key on this node. |
  | `our_asn` | integer | No | The center's ASN; defaults to the configured ASN if `0`/omitted. |
  | `bird_peer_dir` | string | No | BIRD peer config dir; defaults to `/etc/bird/dn42/peer/`. |
  | `wg_dir` | string | No | WireGuard config dir; defaults to `/etc/wireguard/`. |
  | `wg_port_prefix` | integer | No | WireGuard port prefix; defaults to `5` if `0`/omitted. |

  ```json
  {
    "name": "edge-01",
    "location": "Frankfurt",
    "public_ip": "203.0.113.10",
    "our_asn": 4242420000,
    "our_lla": "fe80::1",
    "our_wg_pubkey": "AbCdEf0123456789ExamplePublicKeyValue=",
    "bird_peer_dir": "/etc/bird/dn42/peer/",
    "wg_dir": "/etc/wireguard/",
    "wg_port_prefix": 5
  }
  ```

- **Response `201`:**

  | Field | Type | Description |
  |---|---|---|
  | `id` | string | The new node's ID. |
  | `agent_token` | string | The agent token (shown only once). |
  | `message` | string | Reminder to save the token. |

  ```json
  {
    "id": "node_a1b2c3",
    "agent_token": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
    "message": "Save this agent token - it will not be shown again"
  }
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 400 | `bad_request` | Invalid JSON body. |
  | 400 | `bad_request` | Missing one of: `name`, `location`, `public_ip`, `our_lla`, `our_wg_pubkey`. |
  | 500 | `internal_error` | Failed to generate agent token. |
  | 500 | `internal_error` | Failed to create node. |

- **Source:** `AdminHandler.CreateNode` — `internal/handler/admin_node.go`

## `PUT /api/v1/admin/nodes/{id}`

Update mutable fields of an existing node. Only non-empty string fields are applied; `enabled` is applied only when present.

- **Auth:** Bearer JWT (admin)
- **Path parameters:**

  | Name | Type | Description |
  |---|---|---|
  | `id` | string | Node ID. |

- **Query parameters:** None.
- **Request body:**

  | Field | Type | Required | Description |
  |---|---|---|---|
  | `name` | string | No | New display name (applied only if non-empty). |
  | `location` | string | No | New location label (applied only if non-empty). |
  | `public_ip` | string | No | New public IP (applied only if non-empty). |
  | `our_lla` | string | No | New link-local address (applied only if non-empty). |
  | `our_wg_pubkey` | string | No | New WireGuard public key (applied only if non-empty). |
  | `enabled` | boolean (nullable) | No | Enable/disable the node (applied only if present). |

  ```json
  {
    "name": "edge-01-renamed",
    "location": "Amsterdam",
    "public_ip": "203.0.113.11",
    "our_lla": "fe80::1",
    "our_wg_pubkey": "AbCdEf0123456789ExamplePublicKeyValue=",
    "enabled": false
  }
  ```

- **Response `200`:**

  | Field | Type | Description |
  |---|---|---|
  | `status` | string | Always `"updated"`. |

  ```json
  { "status": "updated" }
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 400 | `bad_request` | Invalid JSON body. |
  | 404 | `not_found` | Node not found. |
  | 500 | `internal_error` | Failed to update node. |

- **Source:** `AdminHandler.UpdateNode` — `internal/handler/admin_node.go`

## `DELETE /api/v1/admin/nodes/{id}`

Delete a node (and its peer records). Fails if the node still has peers in any non-`rejected` state.

- **Auth:** Bearer JWT (admin)
- **Path parameters:**

  | Name | Type | Description |
  |---|---|---|
  | `id` | string | Node ID. |

- **Query parameters:** None.
- **Request body:** None.
- **Response `200`:**

  | Field | Type | Description |
  |---|---|---|
  | `status` | string | Always `"deleted"`. |

  ```json
  { "status": "deleted" }
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 409 | `node_has_peers` | Node has peers in pending/active/suspended state; clear them first. |
  | 500 | `internal_error` | Failed to verify peers. |
  | 500 | `internal_error` | Failed to delete node. |

- **Source:** `AdminHandler.DeleteNode` — `internal/handler/admin_node.go`

## `POST /api/v1/admin/nodes/{id}/import`

Import the node's existing peers by asking the node's agent to scan its live config and inserting any discovered peers into the center. The contact email for each imported peer is looked up from the DN42 registry by ASN; peers whose email lookup failed are reported in `missing_email`.

- **Auth:** Bearer JWT (admin)
- **Path parameters:**

  | Name | Type | Description |
  |---|---|---|
  | `id` | string | Node ID. |

- **Query parameters:** None.
- **Request body:** None.
- **Response `200`:**

  | Field | Type | Description |
  |---|---|---|
  | `status` | string | Always `"completed"`. |
  | `inserted` | integer | Number of peers newly inserted. |
  | `skipped` | integer | Number of peers skipped (already present). |
  | `db_errors` | integer | Number of peers that failed to insert due to a DB error. |
  | `total` | integer | Total number of peers reported by the agent. |
  | `missing_email` | array | Peers whose registry email lookup failed (see below). May be `null`/absent if none. |

  Each `missing_email` entry:

  | Field | Type | Description |
  |---|---|---|
  | `peer_id` | string | The imported peer's ID. |
  | `asn` | integer | The peer's remote ASN. |
  | `reason` | string | Why the email lookup failed. |

  ```json
  {
    "status": "completed",
    "inserted": 8,
    "skipped": 2,
    "db_errors": 0,
    "total": 10,
    "missing_email": [
      {
        "peer_id": "peer_d4e5f6",
        "asn": 4242420001,
        "reason": "registry lookup failed"
      }
    ]
  }
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 503 | `agent_error` | Failed to reach agent. |
  | 500 | `agent_error` | Agent scan failed. |

- **Source:** `AdminHandler.ImportNodePeers` — `internal/handler/admin_peer.go`

## `POST /api/v1/admin/nodes/{id}/bird-refresh`

Ask the node's agent for current BIRD protocol details and persist them as BIRD metrics.

- **Auth:** Bearer JWT (admin)
- **Path parameters:**

  | Name | Type | Description |
  |---|---|---|
  | `id` | string | Node ID. |

- **Query parameters:** None.
- **Request body:** None.
- **Response `200`:**

  | Field | Type | Description |
  |---|---|---|
  | `protocols` | integer | Number of BIRD protocols reported by the agent. |
  | `updated` | integer | Number of protocol metrics successfully stored. |

  ```json
  {
    "protocols": 12,
    "updated": 12
  }
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 503 | `agent_error` | Failed to reach agent. |
  | 500 | `agent_error` | BIRD refresh failed on the agent. |

- **Source:** `AdminHandler.RefreshBirdDetails` — `internal/handler/admin_node.go`

## `POST /api/v1/admin/nodes/{id}/update`

Trigger an agent binary update on the node to a specific release version. The center looks up the release, builds a download URL, and sends an `agent.update` command over the agent WebSocket (see [`../websocket-protocol.md`](../websocket-protocol.md)).

- **Auth:** Bearer JWT (admin)
- **Path parameters:**

  | Name | Type | Description |
  |---|---|---|
  | `id` | string | Node ID. |

- **Query parameters:** None.
- **Request body:**

  | Field | Type | Required | Description |
  |---|---|---|---|
  | `version` | string | Yes | Target release version. |
  | `os` | string | No | Target OS; defaults to `linux`. |
  | `arch` | string | No | Target architecture; defaults to `amd64`. |

  ```json
  {
    "version": "1.4.3",
    "os": "linux",
    "arch": "amd64"
  }
  ```

- **Response `200`:**

  | Field | Type | Description |
  |---|---|---|
  | `status` | string | Always `"initiated"`. |
  | `version` | string | The target version. |
  | `update_id` | string | Generated UUID correlating this update. |

  ```json
  {
    "status": "initiated",
    "version": "1.4.3",
    "update_id": "550e8400-e29b-41d4-a716-446655440000"
  }
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 400 | `bad_request` | Invalid JSON body or missing `version`. |
  | 404 | `not_found` | Release not found for the given version/os/arch. |
  | 503 | `agent_error` | Agent not reachable. |
  | 500 | `agent_error` | Agent reported the update failed. |

- **Source:** `AdminHandler.UpdateAgent` — `internal/handler/admin_release.go`

## `POST /api/v1/admin/nodes/{id}/rollback`

Tell the node's agent to roll back to its previous binary by sending an `agent.rollback` command over the agent WebSocket.

- **Auth:** Bearer JWT (admin)
- **Path parameters:**

  | Name | Type | Description |
  |---|---|---|
  | `id` | string | Node ID. |

- **Query parameters:** None.
- **Request body:** None.
- **Response `200`:**

  | Field | Type | Description |
  |---|---|---|
  | `status` | string | Always `"rollback initiated"`. |

  ```json
  { "status": "rollback initiated" }
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 503 | `agent_error` | Agent not reachable. |
  | 500 | `agent_error` | Agent reported the rollback failed. |

- **Source:** `AdminHandler.RollbackAgent` — `internal/handler/admin_release.go`

## `POST /api/v1/admin/nodes/{id}/regenerate-token`

Generate and store a new agent token for the node (returned once); invalidates the old token.

- **Auth:** Bearer JWT (admin)
- **Path parameters:**

  | Name | Type | Description |
  |---|---|---|
  | `id` | string | Node ID. |

- **Query parameters:** None.
- **Request body:** None.
- **Response `200`:**

  | Field | Type | Description |
  |---|---|---|
  | `agent_token` | string | The new agent token (shown only once). |
  | `node_id` | string | The node's ID. |
  | `message` | string | Reminder to save the token. |

  ```json
  {
    "agent_token": "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210",
    "node_id": "node_a1b2c3",
    "message": "Save this agent token - it will not be shown again"
  }
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 404 | `not_found` | Node not found. |
  | 500 | `internal_error` | Failed to generate agent token. |
  | 500 | `internal_error` | Failed to update agent token. |

- **Source:** `AdminHandler.RegenerateToken` — `internal/handler/admin_node.go`

## `POST /api/v1/admin/nodes/{id}/reset-pubkey`

Clear the node's stored agent public key, forcing a fresh key-exchange handshake on the agent's next connect (see [`../websocket-protocol.md`](../websocket-protocol.md)).

- **Auth:** Bearer JWT (admin)
- **Path parameters:**

  | Name | Type | Description |
  |---|---|---|
  | `id` | string | Node ID. |

- **Query parameters:** None.
- **Request body:** None.
- **Response `200`:**

  | Field | Type | Description |
  |---|---|---|
  | `status` | string | Always `"pubkey_cleared"`. |

  ```json
  { "status": "pubkey_cleared" }
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 404 | `not_found` | Node not found. |
  | 500 | `internal_error` | Failed to reset agent pubkey. |

- **Source:** `AdminHandler.ResetPubkey` — `internal/handler/admin_node.go`

---

## `GET /api/v1/admin/nodes/{id}/traffic`

Return node-aggregated DN42 traffic-sampling analytics (summed across all of the
node's peer interfaces, bucketed over time). **Optional feature:** only available
when the center is configured with a ClickHouse backend (`CLICKHOUSE_URL`);
otherwise returns `503 traffic_analytics_disabled`. Use the
`traffic_analytics_enabled` flag from `GET /api/v1/admin/stats` to decide whether
to call this. Only aggregate counts and Top-N talkers are returned — never packet
payload.

- **Auth:** Bearer JWT (admin)
- **Path parameters:**

  | Name | Type | Description |
  |---|---|---|
  | `id` | string | Node ID. |

- **Query parameters:**

  | Name | Type | Default | Description |
  |---|---|---|---|
  | `hours` | integer | `24` | Look-back window in hours (clamped to `1..168`). |
  | `top` | integer | `10` | Number of Top-N rows per table (clamped to `1..50`). |
  | `bucket` | integer | auto (~window/200, min 1) | Aggregation bucket size in minutes (clamped to `1..1440`). |

- **Request body:** None.
- **Response `200`:** Identical shape to `GET /api/v1/admin/peers/{id}/traffic`
  (see [peers.md](peers.md)) — `enabled`, `sample_ratio`, `points[]`, and
  `top_src` / `top_dst` / `top_ports`. Here the series is summed across the
  node's interfaces and bucketed by `bucket` minutes, and the Top-N tables are
  node-level.

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 503 | `traffic_analytics_disabled` | ClickHouse is not configured. |
  | 500 | `internal_error` | Failed to fetch traffic analytics. |

- **Source:** `TrafficHandler.NodeTraffic` — `internal/handler/traffic.go`

---

# Agent Releases

Admin endpoints for managing uploaded agent binaries. All require an admin Bearer JWT.

## `GET /api/v1/admin/releases`

List all uploaded agent releases.

- **Auth:** Bearer JWT (admin)
- **Path parameters:** None.
- **Query parameters:** None.
- **Request body:** None.
- **Response `200`:** An object wrapping the release list.

  | Field | Type | Description |
  |---|---|---|
  | `releases` | array | List of release objects (see below). |

  Each release object:

  | Field | Type | Description |
  |---|---|---|
  | `version` | string | Release version. |
  | `os` | string | Target OS. |
  | `arch` | string | Target architecture. |
  | `sha256` | string | SHA-256 hex digest of the binary. |
  | `size` | integer | File size in bytes. |
  | `uploaded_by` | string | Email of the uploader (empty string if unknown). |
  | `uploaded_at` | string | RFC3339 upload timestamp. |

  ```json
  {
    "releases": [
      {
        "version": "1.4.3",
        "os": "linux",
        "arch": "amd64",
        "sha256": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
        "size": 15728640,
        "uploaded_by": "admin@example.com",
        "uploaded_at": "2026-06-01T08:00:00Z"
      }
    ]
  }
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 500 | `internal_error` | Failed to list releases. |

- **Source:** `AdminHandler.ListReleases` — `internal/handler/admin_release.go`

## `POST /api/v1/admin/releases`

Upload a new agent binary release. The request must be `multipart/form-data`. The binary part is limited to 100 MB and its SHA-256 is computed server-side.

- **Auth:** Bearer JWT (admin)
- **Path parameters:** None.
- **Query parameters:** None.
- **Request body:** `multipart/form-data` with the following parts:

  | Field | Type | Required | Description |
  |---|---|---|---|
  | `version` | form field (string) | Yes | Release version. Must match `^[a-zA-Z0-9._-]+$`. |
  | `os` | form field (string) | Yes | Target OS. Must match `^[a-zA-Z0-9._-]+$`. |
  | `arch` | form field (string) | Yes | Target architecture. Must match `^[a-zA-Z0-9._-]+$`. |
  | `binary` | file part | Yes | The agent binary file (max 100 MB). |

  Example (curl):

  ```bash
  curl -X POST https://your-center.example.com/api/v1/admin/releases \
    -H "Authorization: Bearer <admin_access_token>" \
    -F version=1.4.3 \
    -F os=linux \
    -F arch=amd64 \
    -F binary=@autopeer-agent
  ```

- **Response `201`:**

  | Field | Type | Description |
  |---|---|---|
  | `version` | string | Release version. |
  | `os` | string | Target OS. |
  | `arch` | string | Target architecture. |
  | `sha256` | string | Computed SHA-256 hex digest of the uploaded binary. |
  | `size` | integer | Bytes written to disk. |

  ```json
  {
    "version": "1.4.3",
    "os": "linux",
    "arch": "amd64",
    "sha256": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
    "size": 15728640
  }
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 400 | `bad_request` | `version`, `os`, or `arch` missing. |
  | 400 | `bad_request` | Invalid path component (a field fails the `^[a-zA-Z0-9._-]+$` check). |
  | 400 | `bad_request` | Resolved destination path escapes the release directory. |
  | 400 | `bad_request` | `binary` file part is missing/unreadable. |
  | 413 | `too_large` | Binary file exceeds the 100 MB limit. |
  | 409 | `version_exists` | A release with this version/os/arch already exists. |
  | 500 | `internal_error` | Failed to write release file. |
  | 500 | `internal_error` | Failed to save release record. |
  | 500 | `internal_error` | Failed to finalize release file (rename). |

- **Source:** `AdminHandler.UploadRelease` — `internal/handler/admin_release.go`

## `DELETE /api/v1/admin/releases/{version}`

Delete a release record and remove its binary from disk.

- **Auth:** Bearer JWT (admin)
- **Path parameters:**

  | Name | Type | Description |
  |---|---|---|
  | `version` | string | Release version. Must match `^[a-zA-Z0-9._-]+$`. |

- **Query parameters:**

  | Name | Type | Required | Description |
  |---|---|---|---|
  | `os` | string | No | Target OS; defaults to `linux`. Must match `^[a-zA-Z0-9._-]+$`. |
  | `arch` | string | No | Target architecture; defaults to `amd64`. Must match `^[a-zA-Z0-9._-]+$`. |

- **Request body:** None.
- **Response `200`:**

  | Field | Type | Description |
  |---|---|---|
  | `status` | string | Always `"deleted"`. |

  ```json
  { "status": "deleted" }
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 400 | `bad_request` | Invalid `version`, `os`, or `arch` (fails the `^[a-zA-Z0-9._-]+$` check). |
  | 404 | `not_found` | Release not found. |

- **Source:** `AdminHandler.DeleteRelease` — `internal/handler/admin_release.go`

---

# Agent Download

## `GET /api/v1/agent/download`

Stream an agent binary to a node's agent. Authentication is performed inside the handler using either an agent token (`X-Agent-Token`) or a node ID (`X-Node-ID`) that has a stored agent public key; there is no route-level auth middleware.

> Note: Unlike the JSON admin endpoints, this handler writes plain-text error bodies via `http.Error` (not the standard `{"error":...}` JSON envelope).

- **Auth:** agent token — supply `X-Agent-Token: <agent_token>`, or alternatively `X-Node-ID: <node_id>` for a node that is enabled and has a stored agent public key (key-auth mode).
- **Path parameters:** None.
- **Query parameters:**

  | Name | Type | Required | Description |
  |---|---|---|---|
  | `version` | string | Yes | Release version to download. |
  | `os` | string | No | Target OS; defaults to `linux`. |
  | `arch` | string | No | Target architecture; defaults to `amd64`. |

- **Request body:** None.
- **Response `200`:** The raw binary file, served via `http.ServeContent`. Response headers include:

  | Header | Description |
  |---|---|
  | `X-Checksum-SHA256` | SHA-256 hex digest of the release binary. |
  | `Content-Disposition` | `attachment; filename=autopeer-agent` |

  Example request:

  ```bash
  curl -OJ "https://your-center.example.com/api/v1/agent/download?version=1.4.3&os=linux&arch=amd64" \
    -H "X-Agent-Token: <agent_token>"
  ```

- **Errors:** (plain-text bodies, not JSON)

  | Status | Body | Condition |
  |---|---|---|
  | 400 | `missing version` | `version` query parameter is absent. |
  | 400 | `missing token or node_id` | Neither `X-Agent-Token` nor `X-Node-ID` was provided. |
  | 401 | `invalid token` | `X-Agent-Token` does not match any node. |
  | 401 | `invalid node_id or key-auth not configured` | `X-Node-ID` is unknown/disabled, or the node has no stored agent public key. |
  | 404 | `release not found` | No release record for the version/os/arch. |
  | 404 | `release file not found on disk` | Release record exists but the file is missing on disk. |
  | 500 | `internal error` | Unexpected error looking up the release. |

- **Source:** `AgentHandler.HandleDownload` — `internal/handler/agent.go`
