# Public API

Public, no-authentication endpoints. These are safe to call from any client without credentials. For authenticated flows, see [../authentication.md](../authentication.md).

Base URL: `https://your-center.example.com`

All `/api/v1` routes pass the API version middleware: an optional `Autopeer-Version` request header is accepted and echoed back, and an unknown value returns `400 invalid_api_version`. See [./versioning.md](./versioning.md).

---

# Utility

## `GET /health`
Liveness probe returning a small JSON body.

- **Auth:** public
- **Path parameters:** None.
- **Query parameters:** None.
- **Request body:** None.
- **Response `200`:** A fixed JSON object with `Content-Type: application/json`.

  | Field | Type | Description |
  |---|---|---|
  | `status` | string | Always `"ok"`. |

  ```json
  {"status":"ok"}
  ```

- **Errors:** None.
- **Source:** inline handler — `cmd/center/routes.go`

## `GET /healthz`
Plain-text liveness probe.

- **Auth:** public
- **Path parameters:** None.
- **Query parameters:** None.
- **Request body:** None.
- **Response `200`:** The literal text `OK` with `Content-Type: text/plain` (not JSON).

  ```
  OK
  ```

- **Errors:** None.
- **Source:** inline handler — `cmd/center/routes.go`

---

# Nodes

## `GET /api/v1/nodes`
Lists all enabled peering nodes with their public connection details.

- **Auth:** public
- **Path parameters:** None.
- **Query parameters:** None.
- **Request body:** None.
- **Response `200`:** A bare JSON array of node objects (empty array `[]` when no nodes are enabled).

  | Field | Type | Description |
  |---|---|---|
  | `id` | string | Node identifier. |
  | `name` | string | Human-readable node name. |
  | `location` | string | Node location label. |
  | `public_ip` | string | Public IP/host used to reach the node. |
  | `our_asn` | integer | The node's own DN42 ASN. |
  | `our_lla` | string | The node's own IPv6 link-local address. |
  | `our_wg_pubkey` | string | The node's WireGuard public key. |
  | `online` | boolean | Whether the node's agent is currently connected. |
  | `enabled` | boolean | Whether the node is enabled (always `true` in this list). |
  | `wg_port_prefix` | integer | WireGuard port prefix used by the node. |

  ```json
  [
    {
      "id": "node-fra1",
      "name": "Frankfurt 1",
      "location": "Frankfurt, DE",
      "public_ip": "203.0.113.10",
      "our_asn": 4242420000,
      "our_lla": "fe80::1",
      "our_wg_pubkey": "AbCdEf0123456789AbCdEf0123456789AbCdEf01234=",
      "online": true,
      "enabled": true,
      "wg_port_prefix": 50000
    }
  ]
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 500 | `internal_error` | Failed to fetch nodes from the database. |

- **Source:** `NodeHandler.ListPublic` — `internal/handler/node.go`

---

# Registry

## `GET /api/v1/registry/asn/{asn}`
Looks up the DN42 registry contact email for an ASN, returning a masked email to anonymous callers.

- **Auth:** public (admin callers — i.e. a valid admin Bearer JWT — bypass IP rate limiting and additionally receive the unmasked `email`).
- **Path parameters:**

  | Name | Type | Description |
  |---|---|---|
  | `asn` | integer | DN42 ASN to look up. Must parse as a positive integer (`> 0`). |

- **Query parameters:** None.
- **Request body:** None.
- **Rate limiting:** Non-admin requests are rate-limited per client IP to 10 lookups per rolling minute (client IP resolved via the trusted-proxy / RealIP logic, `middleware.RealIP`). Exceeding the limit returns `429 rate_limited`. Admin callers are not rate-limited.
- **Response `200`:** A JSON object. The `email` field is present only for admin callers.

  | Field | Type | Description |
  |---|---|---|
  | `asn` | integer | The looked-up ASN. |
  | `masked_email` | string | Registry contact email with the local part masked to its first character followed by `***` (e.g. `a***@example.com`). |
  | `email` | string | Unmasked registry contact email. Present only for admin callers. |

  ```json
  {
    "asn": 4242420000,
    "masked_email": "a***@example.com"
  }
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 429 | `rate_limited` | Non-admin caller exceeded the per-IP lookup rate limit (10/minute). |
  | 400 | `bad_request` | `asn` is not a valid positive integer. |
  | 404 | `asn_not_found` | ASN was not found in the DN42 registry. |

- **Source:** `RegistryHandler.LookupASN` — `internal/handler/registry.go`

---

# Stats

## `GET /api/v1/stats`
Returns aggregate public network statistics plus per-node summaries.

- **Auth:** public
- **Path parameters:** None.
- **Query parameters:** None.
- **Request body:** None.
- **Response `200`:** A JSON object with overall totals and a `nodes` array.

  | Field | Type | Description |
  |---|---|---|
  | `nodes_online` | integer | Number of online nodes. |
  | `peers_active` | integer | Number of active peers across all nodes. |
  | `total_rx_bytes` | integer | Total received bytes in the last 24h. |
  | `total_tx_bytes` | integer | Total transmitted bytes in the last 24h. |
  | `avg_rtt_ms` | number | Average RTT in milliseconds across nodes. |
  | `nodes` | array | Per-node statistics (objects, see below; empty array when none). |

  Each object in `nodes`:

  | Field | Type | Description |
  |---|---|---|
  | `node_id` | string | Node identifier. |
  | `name` | string | Node name. |
  | `peers_active` | integer | Active peers on this node. |
  | `avg_rtt_ms` | number | Average RTT in milliseconds for this node (`0` when unavailable). |
  | `rx_bytes_24h` | integer | Received bytes on this node in the last 24h. |
  | `tx_bytes_24h` | integer | Transmitted bytes on this node in the last 24h. |

  ```json
  {
    "nodes_online": 3,
    "peers_active": 42,
    "total_rx_bytes": 10485760,
    "total_tx_bytes": 8388608,
    "avg_rtt_ms": 23.5,
    "nodes": [
      {
        "node_id": "node-fra1",
        "name": "Frankfurt 1",
        "peers_active": 17,
        "avg_rtt_ms": 18.2,
        "rx_bytes_24h": 5242880,
        "tx_bytes_24h": 4194304
      }
    ]
  }
  ```

- **Errors:**

  | Status | `error` code | Condition |
  |---|---|---|
  | 500 | `internal_error` | Failed to fetch overall stats. |
  | 500 | `internal_error` | Failed to fetch per-node stats. |

- **Source:** `StatsHandler.Public` — `internal/handler/stats.go`

---

# Peers

## `GET /api/v1/user/peers/creation-status`
Reports whether new peer creation is currently enabled site-wide.

- **Auth:** public
- **Path parameters:** None.
- **Query parameters:** None.
- **Request body:** None.
- **Response `200`:** A JSON object with a single boolean. `enabled` is `true` only when the `peer_creation_enabled` site setting is exactly the string `"true"`; an unset or unavailable setting reads as an empty string and yields `false`.

  | Field | Type | Description |
  |---|---|---|
  | `enabled` | boolean | `true` if users can currently create peers. |

  ```json
  {"enabled":true}
  ```

- **Errors:** None.
- **Source:** `PeerHandler.CreationStatus` — `internal/handler/peer.go`

---

See also: [./nodes.md](./nodes.md), [./peers.md](./peers.md), [../authentication.md](../authentication.md), [../configuration.md](../configuration.md).
