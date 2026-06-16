# Peers API

Peer lifecycle endpoints for both authenticated users (managing their own ASN's peers) and admins (managing all peers, including approve/reject/suspend flows).

Base URL: `https://your-center.example.com`

All routes below live under the `/api/v1` group and therefore pass the `APIVersion` middleware: send an optional `Autopeer-Version` request header (echoed back on the response); an unknown value returns `400 invalid_api_version`. Endpoints whose handler calls `JSONVersioned` are flagged **Versioned** — see [`./versioning.md`](./versioning.md). For auth schemes see [`../authentication.md`](../authentication.md); for the agent command flow that peer mutations trigger see [`../websocket-protocol.md`](../websocket-protocol.md).

See also: [`./nodes.md`](./nodes.md), [`./admin.md`](./admin.md).

---

# User endpoints

These require a user Bearer JWT (`Authorization: Bearer <access_token>`, role=user). A user only ever sees and mutates peers belonging to their own ASN (derived from the JWT).

## `GET /api/v1/user/peers`

List all peers owned by the authenticated user's ASN.

- **Auth:** Bearer JWT (user)
- **Path parameters:** None.
- **Query parameters:** None.
- **Request body:** None.
- **Response `200`:** A bare JSON array of peer objects.

| Field | Type | Description |
|---|---|---|
| `id` | string | Peer ID |
| `node_id` | string | Node this peer is attached to |
| `remote_asn` | integer | The peer's (your) ASN |
| `remote_pubkey` | string | Remote WireGuard public key |
| `remote_endpoint` | string | Remote WireGuard endpoint (`IP:Port` or `[IPv6]:Port`) |
| `remote_lla` | string | Remote link-local address |
| `contact_email` | string | Contact email resolved from the DN42 registry |
| `wg_listen_port` | integer | WireGuard listen port on the node |
| `wg_interface_name` | string | WireGuard interface name on the node |
| `wg_managed` | boolean | Whether the tunnel is managed by the agent |
| `status` | string | `pending`, `active`, `suspended`, or `rejected` |
| `reject_reason` | string (nullable, omitempty) | Reason set when rejected |
| `endpoint_mismatch_since` | string (nullable, omitempty) | RFC3339 timestamp; present only on newer API versions |
| `bgp_suspended_by_endpoint` | boolean | Present only on newer API versions |
| `created_at` | string | RFC3339 timestamp |
| `updated_at` | string | RFC3339 timestamp |
| `node_name` | string | Node display name |
| `node_location` | string | Node location |
| `mtu` | integer (nullable, omitempty) | Tunnel MTU |
| `wg_preshared_key` | string (nullable, omitempty) | WireGuard PSK if one was generated |

```json
[
  {
    "id": "9f1c2e7a-4b6d-4f0a-9c33-1a2b3c4d5e6f",
    "node_id": "node-fra-01",
    "remote_asn": 4242420000,
    "remote_pubkey": "abcdEFGH1234ijklMNOP5678qrstUVWX9012yzAB=",
    "remote_endpoint": "203.0.113.10:51820",
    "remote_lla": "fe80::1",
    "contact_email": "admin@example.com",
    "wg_listen_port": 50000,
    "wg_interface_name": "dn42_20000",
    "wg_managed": true,
    "status": "active",
    "bgp_suspended_by_endpoint": false,
    "created_at": "2026-06-01T12:00:00Z",
    "updated_at": "2026-06-02T08:30:00Z",
    "node_name": "Frankfurt 01",
    "node_location": "Frankfurt, DE",
    "mtu": 1420
  }
]
```

- **Errors:**

| Status | `error` code | Condition |
|---|---|---|
| 500 | `internal_error` | Failed to fetch peers from the database |

- **Versioned:** Yes — see [`./versioning.md`](./versioning.md)
- **Source:** `PeerHandler.List` — `internal/handler/peer.go`

---

## `POST /api/v1/user/peers`

Submit a new peering request for the authenticated user's ASN. Creates a peer in `pending` status (or `active` if the site has auto-approve enabled and the agent accepts the configuration).

- **Auth:** Bearer JWT (user)
- **Path parameters:** None.
- **Query parameters:** None.
- **Request body:** (struct `createPeerReq`)

| Field | Type | Required | Description |
|---|---|---|---|
| `node_id` | string | Yes | Target node ID; the node must exist and be enabled |
| `remote_pubkey` | string | Yes | WireGuard public key; must match `^[A-Za-z0-9+/]+=*$` (Base64) and be at least 40 characters |
| `remote_endpoint` | string | Yes | `IP:Port` or `[IPv6]:Port`; host may be an IP or hostname, port 1–65535 |
| `remote_lla` | string | Yes | Link-local address (must parse as an IP and be link-local-unicast, i.e. `fe80::/10`) |
| `mtu` | integer (nullable) | No | If supplied, must be between 576 and 9000 |
| `enable_psk` | boolean | No | If `true`, the center generates a WireGuard pre-shared key |

```json
{
  "node_id": "node-fra-01",
  "remote_pubkey": "abcdEFGH1234ijklMNOP5678qrstUVWX9012yzAB=",
  "remote_endpoint": "203.0.113.10:51820",
  "remote_lla": "fe80::1",
  "mtu": 1420,
  "enable_psk": true
}
```

- **Response `201`:** Object describing the created peer. `status` is `"pending"` normally, or `"active"` if auto-approve succeeded.

| Field | Type | Description |
|---|---|---|
| `id` | string | New peer ID |
| `status` | string | `pending` or `active` |
| `wg_listen_port` | integer | Computed WireGuard port on the node |
| `wg_interface_name` | string | Computed WireGuard interface name |
| `mtu` | integer (nullable) | Echo of the requested MTU (null if none supplied) |
| `wg_preshared_key` | string (nullable) | Generated PSK if `enable_psk` was `true`, else null |

```json
{
  "id": "9f1c2e7a-4b6d-4f0a-9c33-1a2b3c4d5e6f",
  "status": "pending",
  "wg_listen_port": 50000,
  "wg_interface_name": "dn42_20000",
  "mtu": 1420,
  "wg_preshared_key": "QmFzZTY0UFNLZXhhbXBsZXZhbHVlMTIzNDU2Nzg5MA=="
}
```

- **Errors:**

| Status | `error` code | Condition |
|---|---|---|
| 403 | `peer_creation_disabled` | Site setting `peer_creation_enabled` is not `"true"` |
| 400 | `bad_request` | Invalid JSON body |
| 400 | `bad_request` | `node_id is required` |
| 400 | `bad_request` | `remote_pubkey` not Base64 or shorter than 40 chars |
| 400 | `bad_request` | Invalid `remote_endpoint` format |
| 400 | `bad_request` | Invalid `remote_lla` (must be `fe80::/10`) |
| 400 | `bad_request` | `mtu` outside 576–9000 |
| 400 | `invalid_node` | Node not found or disabled |
| 409 | `operation_in_progress` | Another peer operation for this (node, ASN) is in progress |
| 409 | `peer_exists` | You already have a peer on this node |
| 429 | `too_many_pending` | You have 10 or more pending peer requests |
| 409 | `port_conflict` | Computed WireGuard port already used on this node (pre-insert check, or DB unique-violation `23505` on a `port` constraint) |
| 500 | `internal_error` | Failed to generate pre-shared key |
| 500 | `internal_error` | Failed to create peer (DB insert) |

- **Source:** `PeerHandler.Create` — `internal/handler/peer.go`

---

## `GET /api/v1/user/peers/summary`

Return a lightweight metric summary for each of the user's peers (for dashboards).

- **Auth:** Bearer JWT (user)
- **Path parameters:** None.
- **Query parameters:** None.
- **Request body:** None.
- **Response `200`:** A bare JSON array of summary items.

| Field | Type | Description |
|---|---|---|
| `peer_id` | string | Peer ID |
| `latest_rtt` | number (nullable) | Most recent RTT in ms |
| `latest_bgp_state` | string (nullable) | Most recent BGP session state |
| `latest_handshake` | string (nullable) | RFC3339 timestamp of last WireGuard handshake |
| `latest_bgp_uptime_secs` | integer (nullable) | Seconds the BGP session has been up |

```json
[
  {
    "peer_id": "9f1c2e7a-4b6d-4f0a-9c33-1a2b3c4d5e6f",
    "latest_rtt": 12.4,
    "latest_bgp_state": "Established",
    "latest_handshake": "2026-06-07T10:15:00Z",
    "latest_bgp_uptime_secs": 86400
  }
]
```

- **Errors:**

| Status | `error` code | Condition |
|---|---|---|
| 500 | `internal_error` | Failed to fetch peer summary |

- **Source:** `PeerHandler.Summary` — `internal/handler/peer.go`

---

## `GET /api/v1/user/peers/{id}`

Fetch a single peer (with node connection details) owned by the user's ASN.

- **Auth:** Bearer JWT (user)
- **Path parameters:**

| Name | Type | Description |
|---|---|---|
| `id` | string | Peer ID |

- **Query parameters:** None.
- **Request body:** None.
- **Response `200`:** A single peer object. Same fields as the list, plus the node's connection details:

| Field | Type | Description |
|---|---|---|
| `id` | string | Peer ID |
| `node_id` | string | Node ID |
| `remote_asn` | integer | The peer's ASN |
| `remote_pubkey` | string | Remote WireGuard public key |
| `remote_endpoint` | string | Remote endpoint |
| `remote_lla` | string | Remote link-local address |
| `contact_email` | string | Contact email |
| `wg_listen_port` | integer | WireGuard listen port |
| `wg_interface_name` | string | WireGuard interface name |
| `wg_managed` | boolean | Agent-managed flag |
| `status` | string | Peer status |
| `reject_reason` | string (nullable, omitempty) | Reason if rejected |
| `endpoint_mismatch_since` | string (nullable, omitempty) | RFC3339 timestamp; present only on newer API versions |
| `bgp_suspended_by_endpoint` | boolean | Present only on newer API versions |
| `created_at` | string | RFC3339 timestamp |
| `updated_at` | string | RFC3339 timestamp |
| `node_name` | string | Node display name |
| `node_location` | string | Node location |
| `node_public_ip` | string | Node public IP (your side endpoint) |
| `node_our_lla` | string | Node's link-local address (our side) |
| `node_our_wg_pubkey` | string | Node's WireGuard public key (our side) |
| `mtu` | integer (nullable, omitempty) | Tunnel MTU |
| `wg_preshared_key` | string (nullable, omitempty) | WireGuard PSK if present |

```json
{
  "id": "9f1c2e7a-4b6d-4f0a-9c33-1a2b3c4d5e6f",
  "node_id": "node-fra-01",
  "remote_asn": 4242420000,
  "remote_pubkey": "abcdEFGH1234ijklMNOP5678qrstUVWX9012yzAB=",
  "remote_endpoint": "203.0.113.10:51820",
  "remote_lla": "fe80::1",
  "contact_email": "admin@example.com",
  "wg_listen_port": 50000,
  "wg_interface_name": "dn42_20000",
  "wg_managed": true,
  "status": "active",
  "bgp_suspended_by_endpoint": false,
  "created_at": "2026-06-01T12:00:00Z",
  "updated_at": "2026-06-02T08:30:00Z",
  "node_name": "Frankfurt 01",
  "node_location": "Frankfurt, DE",
  "node_public_ip": "198.51.100.5",
  "node_our_lla": "fe80::1",
  "node_our_wg_pubkey": "ZYXWvuts9876rqpoNMLK5432jihgFEDC1098zyxw=",
  "mtu": 1420
}
```

- **Errors:**

| Status | `error` code | Condition |
|---|---|---|
| 404 | `peer_not_found` | Peer not found (or not owned by this ASN) |

- **Versioned:** Yes — see [`./versioning.md`](./versioning.md)
- **Source:** `PeerHandler.Get` — `internal/handler/peer.go`

---

## `GET /api/v1/user/peers/{id}/metrics`

Return time-series metrics for one of the user's peers.

- **Auth:** Bearer JWT (user)
- **Path parameters:**

| Name | Type | Description |
|---|---|---|
| `id` | string | Peer ID |

- **Query parameters:**

| Name | Type | Required | Description |
|---|---|---|---|
| `hours` | integer | No | Lookback window in hours. Used only when `> 0` and `<= 720`; otherwise defaults to `24`. |

- **Request body:** None.
- **Response `200`:**

| Field | Type | Description |
|---|---|---|
| `peer_id` | string | Peer ID |
| `latest_rtt` | number (nullable) | RTT of the most recent point |
| `latest_bgp_state` | string (nullable) | BGP state of the most recent point |
| `latest_handshake` | string (nullable) | RFC3339 timestamp of the most recent point's handshake |
| `latest_wg_actual_endpoint` | string (nullable) | Most recently observed actual WireGuard endpoint (from the latest metric summary) |
| `points` | array | Time-series points (see below) |

Each `points` element:

| Field | Type | Description |
|---|---|---|
| `time` | string | RFC3339 timestamp |
| `rx_bytes` | integer | Bytes received |
| `tx_bytes` | integer | Bytes transmitted |
| `rtt_ms` | number (nullable) | RTT in ms |
| `bgp_state` | string (nullable) | BGP session state |
| `last_handshake` | string (nullable) | RFC3339 timestamp of last handshake |

```json
{
  "peer_id": "9f1c2e7a-4b6d-4f0a-9c33-1a2b3c4d5e6f",
  "latest_rtt": 12.4,
  "latest_bgp_state": "Established",
  "latest_handshake": "2026-06-07T10:15:00Z",
  "latest_wg_actual_endpoint": "203.0.113.10:51820",
  "points": [
    {
      "time": "2026-06-07T09:00:00Z",
      "rx_bytes": 1048576,
      "tx_bytes": 524288,
      "rtt_ms": 12.4,
      "bgp_state": "Established",
      "last_handshake": "2026-06-07T08:59:30Z"
    }
  ]
}
```

- **Errors:**

| Status | `error` code | Condition |
|---|---|---|
| 404 | `peer_not_found` | Peer not found (or not owned by this ASN) |
| 500 | `internal_error` | Failed to fetch metrics |

- **Source:** `PeerHandler.Metrics` — `internal/handler/peer.go`

---

## `PUT /api/v1/user/peers/{id}`

Update mutable connection fields of one of the user's peers. Only `pending` or `active` peers can be edited; for `active` peers the tunnel is re-applied on the agent (remove + re-add, with rollback on failure).

- **Auth:** Bearer JWT (user)
- **Path parameters:**

| Name | Type | Description |
|---|---|---|
| `id` | string | Peer ID |

- **Query parameters:** None.
- **Request body:** (struct `peerUpdateBody`, decoded by `decodePeerUpdateJSON`) All fields optional, but at least one must be present. `mtu` is tracked specially: sending the `mtu` key (even as `null`) marks it for update; a `null` value clears the MTU, a numeric value sets it.

| Field | Type | Required | Description |
|---|---|---|---|
| `remote_pubkey` | string (nullable) | No | New WireGuard public key; must be Base64 and at least 40 chars |
| `remote_endpoint` | string (nullable) | No | New endpoint (`IP:Port` or `[IPv6]:Port`) |
| `remote_lla` | string (nullable) | No | New link-local address (must be `fe80::/10`) |
| `mtu` | integer (nullable) | No | New MTU; if non-null must be between 576 and 9000 |

```json
{
  "remote_endpoint": "203.0.113.20:51820",
  "mtu": 1400
}
```

- **Response `200`:**

| Field | Type | Description |
|---|---|---|
| `status` | string | Always `"updated"` |

```json
{ "status": "updated" }
```

- **Errors:**

| Status | `error` code | Condition |
|---|---|---|
| 409 | `operation_in_progress` | Another peer operation is in progress |
| 400 | `bad_request` | Invalid JSON body |
| 400 | `bad_request` | No fields to update |
| 400 | `bad_request` | Invalid `remote_pubkey` |
| 400 | `bad_request` | Invalid `remote_endpoint` |
| 400 | `bad_request` | Invalid `remote_lla` |
| 400 | `bad_request` | `mtu` outside 576–9000 |
| 404 | `peer_not_found` | Peer not found (or not owned by this ASN) |
| 400 | `bad_request` | Peer is not `pending` or `active` (`Only pending or active peers can be edited`) |
| 503 | `agent_error` | Failed to remove old peer config (agent unreachable) |
| 500 | `agent_error` | Agent rejected peer removal |
| 500 | `agent_error` | Failed to re-add peer (rollback attempted, agent unreachable) |
| 500 | `agent_error` | Agent rejected peer re-add (rollback attempted) |
| 500 | `internal_error` | DB update failed |

- **Source:** `PeerHandler.Update` — `internal/handler/peer.go`

---

## `DELETE /api/v1/user/peers/{id}`

Delete one of the user's peers. If the peer is `active`, a `peer.remove` command is sent to the node's agent before the DB record is removed.

- **Auth:** Bearer JWT (user)
- **Path parameters:**

| Name | Type | Description |
|---|---|---|
| `id` | string | Peer ID |

- **Query parameters:** None.
- **Request body:** None.
- **Response `200`:**

| Field | Type | Description |
|---|---|---|
| `status` | string | Always `"deleted"` |

```json
{ "status": "deleted" }
```

- **Errors:**

| Status | `error` code | Condition |
|---|---|---|
| 409 | `operation_in_progress` | Another peer operation is in progress |
| 404 | `peer_not_found` | Peer not found (or not owned by this ASN) |
| 503 | `agent_error` | Failed to reach agent (active peer) |
| 500 | `agent_error` | Agent failed to remove peer |
| 500 | `internal_error` | DB delete failed |

- **Source:** `PeerHandler.Delete` — `internal/handler/peer.go`

---

# Admin endpoints

These require an admin Bearer JWT (`Authorization: Bearer <access_token>`, role=admin). Admins operate across all peers and ASNs.

## `GET /api/v1/admin/peers`

List peers across all ASNs with filtering and pagination.

- **Auth:** Bearer JWT (admin)
- **Path parameters:** None.
- **Query parameters:**

| Name | Type | Required | Description |
|---|---|---|---|
| `status` | string | No | Filter by peer status |
| `asn` | integer | No | Filter by remote ASN (ignored if not a valid integer) |
| `node_id` | string | No | Filter by node ID |
| `page` | integer | No | Page number; used only when `> 0`, default `1` |
| `per_page` | integer | No | Page size; used only when `> 0` and `<= 200`, default `20` |

- **Request body:** None.
- **Response `200`:** A paginated wrapper.

| Field | Type | Description |
|---|---|---|
| `peers` | array | Peer objects (see below) |
| `total` | integer | Total matching peers |
| `page` | integer | Current page |
| `per_page` | integer | Page size |

Each `peers` element:

| Field | Type | Description |
|---|---|---|
| `id` | string | Peer ID |
| `node_id` | string | Node ID |
| `remote_asn` | integer | Remote ASN |
| `remote_pubkey` | string | Remote WireGuard public key |
| `remote_endpoint` | string | Remote endpoint |
| `remote_lla` | string | Remote link-local address |
| `contact_email` | string | Contact email |
| `wg_listen_port` | integer | WireGuard listen port |
| `wg_interface_name` | string | WireGuard interface name |
| `wg_managed` | boolean | Agent-managed flag |
| `status` | string | Peer status |
| `reject_reason` | string (nullable, omitempty) | Reason if rejected |
| `created_at` | string | RFC3339 timestamp |
| `updated_at` | string | RFC3339 timestamp |
| `node_name` | string | Node display name |
| `node_location` | string | Node location |
| `mtu` | integer (nullable, omitempty) | Tunnel MTU |
| `latest_rtt` | number (nullable, omitempty) | Most recent RTT in ms |
| `latest_bgp_state` | string (nullable, omitempty) | Most recent BGP state |
| `rx_bytes_24h` | integer | Bytes received in last 24h |
| `tx_bytes_24h` | integer | Bytes transmitted in last 24h |
| `endpoint_mismatch_since` | string (nullable, omitempty) | RFC3339 timestamp; present only on newer API versions |
| `bgp_suspended_by_endpoint` | boolean | Present only on newer API versions |

```json
{
  "peers": [
    {
      "id": "9f1c2e7a-4b6d-4f0a-9c33-1a2b3c4d5e6f",
      "node_id": "node-fra-01",
      "remote_asn": 4242420000,
      "remote_pubkey": "abcdEFGH1234ijklMNOP5678qrstUVWX9012yzAB=",
      "remote_endpoint": "203.0.113.10:51820",
      "remote_lla": "fe80::1",
      "contact_email": "admin@example.com",
      "wg_listen_port": 50000,
      "wg_interface_name": "dn42_20000",
      "wg_managed": true,
      "status": "active",
      "created_at": "2026-06-01T12:00:00Z",
      "updated_at": "2026-06-02T08:30:00Z",
      "node_name": "Frankfurt 01",
      "node_location": "Frankfurt, DE",
      "mtu": 1420,
      "latest_rtt": 12.4,
      "latest_bgp_state": "Established",
      "rx_bytes_24h": 1048576,
      "tx_bytes_24h": 524288,
      "bgp_suspended_by_endpoint": false
    }
  ],
  "total": 1,
  "page": 1,
  "per_page": 20
}
```

- **Errors:**

| Status | `error` code | Condition |
|---|---|---|
| 500 | `internal_error` | Failed to fetch peers |

- **Versioned:** Yes — see [`./versioning.md`](./versioning.md)
- **Source:** `AdminHandler.ListPeers` — `internal/handler/admin_peer.go`

---

## `GET /api/v1/admin/peers/export`

Export **all** peers as a JSON file for backup, bulk editing, or migration. The
output round-trips back through `POST /api/v1/admin/peers/import`.

- **Auth:** Bearer JWT (admin)
- **Path parameters:** None.
- **Query parameters:** None.
- **Request body:** None.
- **Response `200`:** A downloadable JSON document (`Content-Disposition:
  attachment; filename="peers-export.json"`). Not subject to API versioning — the
  dump schema is fixed.

| Field | Type | Description |
|---|---|---|
| `version` | integer | Export schema version (currently `1`) |
| `exported_at` | string | RFC3339 timestamp (UTC) |
| `peers` | array | Peer entries (see below) |

Each `peers` element:

| Field | Type | Description |
|---|---|---|
| `node_id` | string | Node ID (import key) |
| `node_name` | string (omitempty) | Node display name; export-only helper, ignored on import |
| `remote_asn` | integer | Remote ASN (import key) |
| `remote_pubkey` | string | Remote WireGuard public key |
| `remote_endpoint` | string | Remote endpoint |
| `remote_lla` | string | Remote link-local address |
| `contact_email` | string | Contact email |
| `wg_listen_port` | integer | WireGuard listen port |
| `wg_interface_name` | string | WireGuard interface name |
| `bgp_proto_name` | string | BIRD BGP protocol name |
| `bird_config_filename` | string | BIRD config filename |
| `wg_managed` | boolean | Agent-managed flag |
| `mtu` | integer (nullable, omitempty) | Tunnel MTU |
| `wg_preshared_key` | string (nullable, omitempty) | WireGuard pre-shared key |
| `status` | string | Peer status |

```json
{
  "version": 1,
  "exported_at": "2026-06-07T12:00:00Z",
  "peers": [
    {
      "node_id": "node-fra-01",
      "node_name": "Frankfurt 01",
      "remote_asn": 4242420000,
      "remote_pubkey": "abcdEFGH1234ijklMNOP5678qrstUVWX9012yzAB=",
      "remote_endpoint": "203.0.113.10:51820",
      "remote_lla": "fe80::1",
      "contact_email": "admin@example.com",
      "wg_listen_port": 50000,
      "wg_interface_name": "dn42_20000",
      "bgp_proto_name": "dn42_20000",
      "bird_config_filename": "dn42_20000.conf",
      "wg_managed": true,
      "mtu": 1420,
      "status": "active"
    }
  ]
}
```

- **Errors:**

| Status | `error` code | Condition |
|---|---|---|
| 500 | `internal_error` | Failed to load peers or encode the export |

- **Versioned:** No
- **Source:** `AdminHandler.ExportPeers` — `internal/handler/admin_peer.go`

---

## `POST /api/v1/admin/peers/import`

Import peers from a JSON dump produced by `GET /api/v1/admin/peers/export`.
Writes DB rows **only** — it does not push config to agents. Peers are keyed on
`(node_id, remote_asn)`. Invalid entries are reported in the response `errors`
list rather than failing the whole import.

- **Auth:** Bearer JWT (admin)
- **Path parameters:** None.
- **Query parameters:**

| Name | Type | Required | Description |
|---|---|---|---|
| `overwrite` | string | No | `true` updates an existing peer's fields; any other value skips existing peers (default) |

- **Request body:** Either the export wrapper object (`{version, exported_at, peers}`)
  or a bare JSON array of peer entries. `status` defaults to `pending` when empty.
  `id`/timestamps in the input are ignored.
- **Response `200`:**

| Field | Type | Description |
|---|---|---|
| `status` | string | Always `"completed"` |
| `imported` | integer | Newly inserted peers |
| `overwritten` | integer | Existing peers updated (only when `overwrite=true`) |
| `skipped` | integer | Existing peers left untouched (when `overwrite` is off) |
| `total` | integer | Total entries in the input |
| `errors` | array | Entries that could not be imported |

Each `errors` element:

| Field | Type | Description |
|---|---|---|
| `node_id` | string | Node ID from the entry |
| `asn` | integer | Remote ASN from the entry |
| `reason` | string | Why it failed (`missing node_id`, `invalid remote_asn`, `node not found`, `db error: ...`) |

```json
{
  "status": "completed",
  "imported": 3,
  "overwritten": 0,
  "skipped": 1,
  "total": 5,
  "errors": [
    { "node_id": "bogus-node", "asn": 4242420001, "reason": "node not found" }
  ]
}
```

- **Errors:**

| Status | `error` code | Condition |
|---|---|---|
| 400 | `invalid_request` | Failed to read request body |
| 400 | `invalid_json` | Body is neither an export object nor a JSON array of peers |

- **Versioned:** No
- **Source:** `AdminHandler.ImportPeers` — `internal/handler/admin_peer.go`

---

## `GET /api/v1/admin/peers/{id}`

Fetch a single peer with full node details, latest metric summary, agent-sync state, and audit history.

- **Auth:** Bearer JWT (admin)
- **Path parameters:**

| Name | Type | Description |
|---|---|---|
| `id` | string | Peer ID |

- **Query parameters:** None.
- **Request body:** None.
- **Response `200`:** A wrapper with four top-level keys.

| Field | Type | Description |
|---|---|---|
| `peer` | object | Peer + node fields (see below) |
| `metrics` | object | Latest metric summary (see below) |
| `agent_sync` | object | Desired-state / agent sync info (see below) |
| `history` | array | Recent audit-log entries for this peer (up to 50) |

`peer` object fields (all keys always present): `id`, `node_id`, `remote_asn`, `remote_pubkey`, `remote_endpoint`, `remote_lla`, `contact_email`, `wg_listen_port`, `wg_interface_name`, `wg_managed`, `status`, `reject_reason` (nullable), `endpoint_mismatch_since` (nullable; present only on newer API versions), `bgp_suspended_by_endpoint` (boolean; present only on newer API versions), `created_at`, `updated_at`, `node_name`, `node_location`, `node_public_ip`, `node_our_lla`, `node_our_wg_pubkey`, `mtu` (nullable), `bgp_proto_name`, `bird_config_filename`.

`metrics` object:

| Field | Type | Description |
|---|---|---|
| `latest_rtt` | number (nullable) | Most recent RTT in ms |
| `latest_bgp_state` | string (nullable) | Most recent BGP state |
| `latest_handshake` | string (nullable) | RFC3339 timestamp of last handshake |
| `latest_bgp_uptime_secs` | integer (nullable) | BGP session uptime in seconds |
| `latest_wg_actual_endpoint` | string (nullable) | Most recently observed actual WireGuard endpoint |

`agent_sync` object:

| Field | Type | Description |
|---|---|---|
| `desired_state` | string | `manual`, `configured`, `absent`, or `unknown` |
| `configuration_status` | string | One of `manual`, `expected_configured`, `agent_offline`, `removed`, `pending`, `rejected`, `unknown` |
| `agent_online` | boolean | Whether the node's agent is currently connected |
| `node_agent_state` | string (omitempty) | Node-reported agent state |
| `node_agent_version` | string (omitempty) | Node-reported agent version |
| `node_agent_state_changed_at` | string (omitempty) | RFC3339 timestamp |
| `checked_at` | string | RFC3339 timestamp this snapshot was computed |

Each `history` element:

| Field | Type | Description |
|---|---|---|
| `id` | string | Audit-log entry ID |
| `action` | string | e.g. `peer.create`, `peer.approve`, `peer.update` |
| `operator` | string | Who performed the action |
| `detail` | object (omitempty) | Action-specific detail |
| `created_at` | string | RFC3339 timestamp |

```json
{
  "peer": {
    "id": "9f1c2e7a-4b6d-4f0a-9c33-1a2b3c4d5e6f",
    "node_id": "node-fra-01",
    "remote_asn": 4242420000,
    "remote_pubkey": "abcdEFGH1234ijklMNOP5678qrstUVWX9012yzAB=",
    "remote_endpoint": "203.0.113.10:51820",
    "remote_lla": "fe80::1",
    "contact_email": "admin@example.com",
    "wg_listen_port": 50000,
    "wg_interface_name": "dn42_20000",
    "wg_managed": true,
    "status": "active",
    "reject_reason": null,
    "bgp_suspended_by_endpoint": false,
    "created_at": "2026-06-01T12:00:00Z",
    "updated_at": "2026-06-02T08:30:00Z",
    "node_name": "Frankfurt 01",
    "node_location": "Frankfurt, DE",
    "node_public_ip": "198.51.100.5",
    "node_our_lla": "fe80::1",
    "node_our_wg_pubkey": "ZYXWvuts9876rqpoNMLK5432jihgFEDC1098zyxw=",
    "mtu": 1420,
    "bgp_proto_name": "dn42_20000_v6",
    "bird_config_filename": "dn42_20000.conf"
  },
  "metrics": {
    "latest_rtt": 12.4,
    "latest_bgp_state": "Established",
    "latest_handshake": "2026-06-07T10:15:00Z",
    "latest_bgp_uptime_secs": 86400,
    "latest_wg_actual_endpoint": "203.0.113.10:51820"
  },
  "agent_sync": {
    "desired_state": "configured",
    "configuration_status": "expected_configured",
    "agent_online": true,
    "node_agent_state": "running",
    "node_agent_version": "1.2.3",
    "node_agent_state_changed_at": "2026-06-05T00:00:00Z",
    "checked_at": "2026-06-07T10:20:00Z"
  },
  "history": [
    {
      "id": "audit-001",
      "action": "peer.approve",
      "operator": "admin@example.com",
      "detail": { "asn": 4242420000 },
      "created_at": "2026-06-01T12:05:00Z"
    }
  ]
}
```

- **Errors:**

| Status | `error` code | Condition |
|---|---|---|
| 404 | `peer_not_found` | Peer not found |
| 404 | `peer_not_found` | Node not found (message: `Node not found`) |

- **Versioned:** Yes — see [`./versioning.md`](./versioning.md)
- **Source:** `AdminHandler.GetPeer` — `internal/handler/admin_peer.go`

---

## `GET /api/v1/admin/peers/{id}/metrics`

Return time-series metrics for any peer, including BGP route counts.

- **Auth:** Bearer JWT (admin)
- **Path parameters:**

| Name | Type | Description |
|---|---|---|
| `id` | string | Peer ID |

- **Query parameters:**

| Name | Type | Required | Description |
|---|---|---|---|
| `hours` | integer | No | Lookback window in hours. Used only when `> 0` and `<= 720`; otherwise defaults to `24`. |

- **Request body:** None.
- **Response `200`:**

| Field | Type | Description |
|---|---|---|
| `peer_id` | string | Peer ID |
| `latest_rtt` | number (nullable) | RTT of the most recent point |
| `latest_bgp_state` | string (nullable) | BGP state of the most recent point |
| `latest_handshake` | string (nullable) | RFC3339 timestamp of the most recent point's handshake |
| `points` | array | Time-series points (see below) |

Each `points` element:

| Field | Type | Description |
|---|---|---|
| `time` | string | RFC3339 timestamp |
| `rx_bytes` | integer | Bytes received |
| `tx_bytes` | integer | Bytes transmitted |
| `rtt_ms` | number (nullable) | RTT in ms |
| `bgp_state` | string (nullable) | BGP session state |
| `last_handshake` | string (nullable) | RFC3339 timestamp of last handshake |
| `routes_imported` | integer (nullable) | Routes imported |
| `routes_exported` | integer (nullable) | Routes exported |
| `routes_preferred` | integer (nullable) | Routes preferred |

```json
{
  "peer_id": "9f1c2e7a-4b6d-4f0a-9c33-1a2b3c4d5e6f",
  "latest_rtt": 12.4,
  "latest_bgp_state": "Established",
  "latest_handshake": "2026-06-07T10:15:00Z",
  "points": [
    {
      "time": "2026-06-07T09:00:00Z",
      "rx_bytes": 1048576,
      "tx_bytes": 524288,
      "rtt_ms": 12.4,
      "bgp_state": "Established",
      "last_handshake": "2026-06-07T08:59:30Z",
      "routes_imported": 1024,
      "routes_exported": 16,
      "routes_preferred": 512
    }
  ]
}
```

- **Errors:**

| Status | `error` code | Condition |
|---|---|---|
| 500 | `internal_error` | Failed to fetch metrics |

- **Source:** `AdminHandler.PeerMetrics` — `internal/handler/admin_peer.go`

---

## `GET /api/v1/admin/peers/{id}/traffic`

Return per-interface DN42 traffic-sampling analytics for one peer. **Optional
feature:** only available when the center is configured with a ClickHouse backend
(`CLICKHOUSE_URL`); otherwise returns `503 traffic_analytics_disabled`. Use the
`traffic_analytics_enabled` flag from `GET /api/v1/admin/stats` to decide whether
to call this. Only aggregate counts and Top-N talkers are returned — never packet
payload.

- **Auth:** Bearer JWT (admin)
- **Path parameters:**

  | Name | Type | Description |
  |---|---|---|
  | `id` | string | Peer ID. |

- **Query parameters:**

  | Name | Type | Default | Description |
  |---|---|---|---|
  | `hours` | integer | `24` | Look-back window in hours (clamped to `1..168`). |
  | `top` | integer | `10` | Number of Top-N rows per table (clamped to `1..50`). |

- **Request body:** None.
- **Response `200`:**

  | Field | Type | Description |
  |---|---|---|
  | `enabled` | boolean | Always `true` on a `200`. |
  | `sample_ratio` | number | The most recent packet sampling ratio (use `1/sample_ratio` to estimate totals). |
  | `points` | array | Time series. Each point: `time`, `sampled_bytes`, `sampled_packets`, `v4_bytes`, `v6_bytes`, and `proto_bytes` / `proto_pkts` / `size_buckets` objects (string→integer maps). |
  | `top_src` | array | Top source DN42 IPs: `{ label, pkts, bytes }`. |
  | `top_dst` | array | Top destination DN42 IPs: `{ label, pkts, bytes }`. |
  | `top_ports` | array | Top destination ports: `{ label, pkts, bytes }` (`label` is the port number as text). |

  Fixed `proto` keys: `tcp, udp, icmp, icmpv6, ospf, bgp, other`. Fixed
  `size_buckets` keys: `0-63, 64-127, 128-255, 256-511, 512-1023, 1024-1499,
  1500, 1501+`.

  ```json
  {
    "enabled": true,
    "sample_ratio": 0.001,
    "points": [
      {
        "time": "2026-06-10T12:00:00Z",
        "sampled_bytes": 31280,
        "sampled_packets": 412,
        "v4_bytes": 0,
        "v6_bytes": 31280,
        "proto_bytes": { "tcp": 22100, "bgp": 4200, "icmpv6": 980 },
        "proto_pkts": { "tcp": 300, "bgp": 80, "icmpv6": 32 },
        "size_buckets": { "64-127": 210, "128-255": 150, "1500": 52 }
      }
    ],
    "top_src": [{ "label": "172.20.0.53", "pkts": 180, "bytes": 14200 }],
    "top_dst": [{ "label": "fd00:1234::1", "pkts": 90, "bytes": 8800 }],
    "top_ports": [{ "label": "179", "pkts": 80, "bytes": 4200 }]
  }
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 503 | `traffic_analytics_disabled` | ClickHouse is not configured. |
  | 500 | `internal_error` | Failed to fetch traffic analytics. |

- **Source:** `TrafficHandler.PeerTraffic` — `internal/handler/traffic.go`

---

## `POST /api/v1/admin/peers/{id}/approve`

Approve a `pending` peer: sends `peer.add` to the node's agent and, on success, flips status to `active`.

- **Auth:** Bearer JWT (admin)
- **Path parameters:**

| Name | Type | Description |
|---|---|---|
| `id` | string | Peer ID |

- **Query parameters:** None.
- **Request body:** None.
- **Response `200`:**

| Field | Type | Description |
|---|---|---|
| `status` | string | Always `"active"` |

```json
{ "status": "active" }
```

- **Errors:**

| Status | `error` code | Condition |
|---|---|---|
| 409 | `operation_in_progress` | Another operation is in progress for this peer |
| 500 | `approve_failed` | Approval failed (peer not found / not pending, agent unreachable, agent rejected, or DB update failed). The `message` carries the underlying reason. |

- **Source:** `AdminHandler.ApprovePeer` — `internal/handler/admin_peer.go`

---

## `POST /api/v1/admin/peers/{id}/reject`

Reject a `pending` peer with a required reason; sets status to `rejected`.

- **Auth:** Bearer JWT (admin)
- **Path parameters:**

| Name | Type | Description |
|---|---|---|
| `id` | string | Peer ID |

- **Query parameters:** None.
- **Request body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `reason` | string | Yes | Rejection reason (must be non-empty) |

```json
{ "reason": "Endpoint not reachable" }
```

- **Response `200`:**

| Field | Type | Description |
|---|---|---|
| `status` | string | Always `"rejected"` |

```json
{ "status": "rejected" }
```

- **Errors:**

| Status | `error` code | Condition |
|---|---|---|
| 400 | `bad_request` | Invalid JSON body or empty `reason` (`Rejection reason is required`) |
| 404 | `peer_not_found` | Peer not found or not in pending status |
| 500 | `internal_error` | Failed to update peer status |

- **Source:** `AdminHandler.RejectPeer` — `internal/handler/admin_peer.go`

---

## `POST /api/v1/admin/peers/{id}/suspend`

Suspend an `active` peer: sends `peer.remove` to the agent and flips status to `suspended`.

- **Auth:** Bearer JWT (admin)
- **Path parameters:**

| Name | Type | Description |
|---|---|---|
| `id` | string | Peer ID |

- **Query parameters:** None.
- **Request body:** Optional. The body is decoded but a decode error is ignored, so it is not required.

| Field | Type | Required | Description |
|---|---|---|---|
| `reason` | string | No | Optional reason included in the suspension notification email |

```json
{ "reason": "Abuse report under review" }
```

- **Response `200`:**

| Field | Type | Description |
|---|---|---|
| `status` | string | Always `"suspended"` |

```json
{ "status": "suspended" }
```

- **Errors:**

| Status | `error` code | Condition |
|---|---|---|
| 409 | `operation_in_progress` | Another operation is in progress for this peer |
| 404 | `peer_not_found` | Peer not found or not active |
| 503 | `agent_error` | Failed to reach agent |
| 500 | `agent_error` | Agent failed to remove peer |
| 500 | `internal_error` | Failed to update peer status |

- **Source:** `AdminHandler.SuspendPeer` — `internal/handler/admin_peer.go`

---

## `POST /api/v1/admin/peers/{id}/unsuspend`

Unsuspend a `suspended` peer: sends `peer.add` to the agent and flips status back to `active`.

- **Auth:** Bearer JWT (admin)
- **Path parameters:**

| Name | Type | Description |
|---|---|---|
| `id` | string | Peer ID |

- **Query parameters:** None.
- **Request body:** None.
- **Response `200`:**

| Field | Type | Description |
|---|---|---|
| `status` | string | Always `"active"` |

```json
{ "status": "active" }
```

- **Errors:**

| Status | `error` code | Condition |
|---|---|---|
| 409 | `operation_in_progress` | Another operation is in progress for this peer |
| 404 | `peer_not_found` | Peer not found or not suspended |
| 503 | `agent_error` | Failed to reach agent |
| 500 | `agent_error` | Agent failed to add peer |
| 500 | `internal_error` | Failed to update peer status |

- **Source:** `AdminHandler.UnsuspendPeer` — `internal/handler/admin_peer.go`

---

## `DELETE /api/v1/admin/peers/{id}`

Hard-delete any peer. If the peer is `active`, a `peer.remove` command is sent to the agent first.

- **Auth:** Bearer JWT (admin)
- **Path parameters:**

| Name | Type | Description |
|---|---|---|
| `id` | string | Peer ID |

- **Query parameters:** None.
- **Request body:** None.
- **Response `200`:**

| Field | Type | Description |
|---|---|---|
| `status` | string | Always `"deleted"` |

```json
{ "status": "deleted" }
```

- **Errors:**

| Status | `error` code | Condition |
|---|---|---|
| 409 | `operation_in_progress` | Another operation is in progress for this peer |
| 404 | `peer_not_found` | Peer not found |
| 503 | `agent_error` | Failed to reach agent (active peer) |
| 500 | `agent_error` | Agent failed to remove peer |
| 500 | `internal_error` | Failed to delete peer |

- **Source:** `AdminHandler.DeletePeer` — `internal/handler/admin_peer.go`

---

## `PUT /api/v1/admin/peers/{id}`

Update mutable connection fields of any peer. For `active` peers the tunnel is re-applied on the agent (remove + re-add, with rollback on failure).

- **Auth:** Bearer JWT (admin)
- **Path parameters:**

| Name | Type | Description |
|---|---|---|
| `id` | string | Peer ID |

- **Query parameters:** None.
- **Request body:** (struct `peerUpdateBody`, decoded by `decodePeerUpdateJSON`) Same shape as the user update. All fields optional, at least one required. Sending the `mtu` key (even `null`) marks it for update; a `null` value clears the MTU.

| Field | Type | Required | Description |
|---|---|---|---|
| `remote_pubkey` | string (nullable) | No | New WireGuard public key; must be Base64 and at least 40 chars |
| `remote_endpoint` | string (nullable) | No | New endpoint (`IP:Port` or `[IPv6]:Port`) |
| `remote_lla` | string (nullable) | No | New link-local address (must be `fe80::/10`) |
| `mtu` | integer (nullable) | No | New MTU; if non-null must be between 576 and 9000 |

```json
{
  "remote_pubkey": "newKEY1234ijklMNOP5678qrstUVWX9012yzABcd=",
  "remote_lla": "fe80::1"
}
```

- **Response `200`:**

| Field | Type | Description |
|---|---|---|
| `status` | string | Always `"updated"` |

```json
{ "status": "updated" }
```

Note: unlike the user `PUT`, the admin update does not restrict by peer status (it acts on any peer; only `active` peers trigger the agent re-apply).

- **Errors:**

| Status | `error` code | Condition |
|---|---|---|
| 409 | `operation_in_progress` | Another operation is in progress for this peer |
| 400 | `bad_request` | Invalid JSON body |
| 400 | `bad_request` | No fields to update |
| 400 | `bad_request` | Invalid `remote_pubkey` |
| 400 | `bad_request` | Invalid `remote_endpoint` |
| 400 | `bad_request` | Invalid `remote_lla` |
| 400 | `bad_request` | `mtu` outside 576–9000 |
| 404 | `peer_not_found` | Peer not found |
| 503 | `agent_error` | Failed to remove old peer config (agent unreachable) |
| 500 | `agent_error` | Agent rejected peer removal |
| 500 | `agent_error` | Failed to re-add peer (rollback attempted, agent unreachable) |
| 500 | `agent_error` | Agent rejected peer re-add (rollback attempted) |
| 500 | `internal_error` | Agent commands succeeded but DB update failed |

- **Source:** `AdminHandler.UpdatePeer` — `internal/handler/admin_peer.go`

---

## `PUT /api/v1/admin/peers/{id}/contact-email`

Set a peer's contact email — either to a value you provide, or by re-querying the DN42 registry.

- **Auth:** Bearer JWT (admin)
- **Path parameters:**

| Name | Type | Description |
|---|---|---|
| `id` | string | Peer ID |

- **Query parameters:** None.
- **Request body:**

| Field | Type | Required | Description |
|---|---|---|---|
| `email` | string | Conditional | New contact email. Required unless `retry` is `true`. |
| `retry` | boolean | No | If `true`, ignore `email` and re-fetch the address from the DN42 registry for the peer's ASN. |

```json
{ "email": "admin@example.com" }
```

```json
{ "retry": true }
```

- **Response `200`:**

| Field | Type | Description |
|---|---|---|
| `email` | string | The new contact email that was stored |

```json
{ "email": "admin@example.com" }
```

- **Errors:**

| Status | `error` code | Condition |
|---|---|---|
| 400 | `bad_request` | Invalid JSON body |
| 400 | `bad_request` | Neither `email` provided nor `retry=true` (`Provide email or set retry=true`) |
| 404 | `peer_not_found` | Peer not found |
| 400 | `registry_error` | Registry lookup failed (when `retry=true`) |
| 500 | `internal_error` | Failed to update contact email |

- **Source:** `AdminHandler.UpdateContactEmail` — `internal/handler/admin_peer.go`
