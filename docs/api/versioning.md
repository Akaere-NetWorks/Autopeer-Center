# API Versioning

`autopeer-center` uses **Stripe-style, header-negotiated response versioning**. You pin
your integration to a fixed, dated version of the response shapes by sending a single
request header; the server then guarantees those endpoints keep returning the shape they
had on that date, even as newer fields are added to the latest version.

This is **orthogonal to the URL path** (`/api/v1`). It does not change which routes exist
or what they accept — it only controls the *shape of the JSON objects* returned by a
handful of versioned endpoints.

- See [`./peers.md`](./peers.md) for the endpoints affected today.
- See [`./README.md`](./README.md) for the API index.

---

## The model

There is one source of truth for the data shape: handlers **only ever build the latest
canonical shape** of a response. When you request an older version, the server walks a
chain of dated, backward transformers that *downgrade* the latest object into the shape it
had at your pinned version (deleting/renaming fields as needed).

```
handler builds LATEST shape
        │
        ▼
JSONVersioned(...)  ──  target == Latest()? ──► write as-is (zero-overhead fast path)
        │
        │ target is older
        ▼
apply each VersionChange whose Version > target, newest-first
        │
        ▼
downgraded JSON written to client
```

Concretely, the helper that handlers call is:

```go
JSONVersioned(w, r, status, resource, listKey, data)
```

- `resource` — which kind of object is being serialized (today only `peer`).
- `listKey` — the field that holds the resource objects when `data` is a wrapper map
  (e.g. `"peers"` for `{"peers":[...], "total":N}`). It is `""` when `data` is a bare
  object or a bare list.
- When the resolved version is the latest, `JSONVersioned` is **identical to a plain
  JSON write** — no marshalling round-trip, no transformation. This is the fast path
  for clients that do not pin a version. Only when an older version is targeted does the
  data get marshalled to JSON, re-decoded into a generic value, and downgraded.

---

## The `Autopeer-Version` header

Send the header on any `/api/v1` request:

```
Autopeer-Version: YYYY-MM-DD
```

Resolution rules (handled by the `APIVersion` middleware mounted on the **entire `/api/v1`
group**):

| Header value                     | Result                                                              |
|----------------------------------|--------------------------------------------------------------------|
| absent or empty/whitespace       | resolves to the **latest** version                                 |
| a known dated version            | used as-is                                                         |
| anything else (unknown)          | request rejected with **HTTP 400** `invalid_api_version`           |

The resolved version is **echoed back in the `Autopeer-Version` response header** on every
`/api/v1` response, so you can always confirm which version the server applied.

### Unknown version: 400 response

When the header value is not a recognized version, the request never reaches a handler.
The `APIVersion` middleware writes this body directly (mirroring the standard error shape,
but written by hand because middleware cannot import the handler package):

```json
{
  "error": "invalid_api_version",
  "message": "Unknown Autopeer-Version: 2020-01-01",
  "request_id": "<request-id>"
}
```

The `message` echoes back the *trimmed* header value you sent, and `request_id` is the
request's `X-Request-ID`.

---

## Supported versions

The known versions live in the `versions` slice (the single source of truth), **oldest
first**. The last element is the **latest**:

| Version       | Notes                                                                                          |
|---------------|------------------------------------------------------------------------------------------------|
| `2025-01-01`  | Baseline: before `endpoint_mismatch_since` / `bgp_suspended_by_endpoint` existed on peers.      |
| `2026-06-06`  | Peer objects include `endpoint_mismatch_since` and `bgp_suspended_by_endpoint`.                 |
| `2026-06-07`  | **Latest.** Peer objects additionally include `wg_preshared_key`.                               |

The format is `YYYY-MM-DD` precisely so that lexicographic comparison equals chronological
order.

---

## Registered version changes

Each change is named by the dated version at which the new shape was **introduced**; its
transformer **undoes** that change to produce the prior shape. When you pin to an older
version, every change whose version is newer than your pin is applied, newest-first.

### `2026-06-07` — `peer`

> Add `wg_preshared_key` to peer objects.

Downgrading **removes** the following field from each peer object:

- `wg_preshared_key`

### `2026-06-06` — `peer`

> Add `endpoint_mismatch_since` and `bgp_suspended_by_endpoint` to peer objects.

Downgrading **removes** the following fields from each peer object:

- `endpoint_mismatch_since`
- `bgp_suspended_by_endpoint`

### What each pinned version sees

| Pinned version  | `wg_preshared_key` | `endpoint_mismatch_since` / `bgp_suspended_by_endpoint` |
|-----------------|:------------------:|:------------------------------------------------------:|
| `2026-06-07` / latest / absent | present | present                                 |
| `2026-06-06`    | removed            | present                                                |
| `2025-01-01`    | removed            | removed                                                |

There are no rename or type-conversion changes registered today — both current changes
only **add** fields to the latest shape (and so only **delete** them when downgrading).
A `delete` of a field that is absent from a given object is a harmless no-op, so a change
applies cleanly even to peer shapes that never carried the field in the first place.

---

## Which endpoints are versioned

Only handlers that call `JSONVersioned` are transformed. Today those are the **peer**
endpoints (see [`./peers.md`](./peers.md)):

| Method & path                          | Auth   | `listKey` | Payload shape                                                  |
|----------------------------------------|--------|-----------|---------------------------------------------------------------|
| `GET /api/v1/user/peers`               | user   | `""`      | bare JSON array of peer objects                               |
| `GET /api/v1/user/peers/{id}`          | user   | `""`      | bare peer object                                              |
| `GET /api/v1/admin/peers`              | admin  | `"peers"` | `{"peers":[...], "total", "page", "per_page"}`                |
| `GET /api/v1/admin/peers/{id}`         | admin  | `"peer"`  | `{"peer": {...}, "metrics", "agent_sync", "history"}`         |

The downgrader walks the value generically: it handles a bare object, a bare list, and a
wrapper map (descending into `listKey` and transforming each object inside). Non-object
list elements are skipped, so a malformed payload cannot break the transformation. Only the
objects reached through `listKey` are reshaped — sibling fields of the wrapper map (e.g.
`total`, `metrics`, `history`) are left untouched.

> Note: `wg_preshared_key` is only present in the **user** peer endpoints' latest shape.
> The admin list/get peer objects never carry it, so the `2026-06-07` change is a no-op on
> the admin payloads (it still strips `endpoint_mismatch_since` /
> `bgp_suspended_by_endpoint` from them for `2025-01-01`).

Every other `/api/v1` endpoint resolves and echoes the version header but returns the same
body regardless of the pinned version.

### Error bodies are never versioned

Version transformation only applies to the success payloads written through
`JSONVersioned`. Error responses (including the `400 invalid_api_version` above) are never
reshaped by the version chain.

---

## Client examples

### Without the header (latest)

```bash
curl https://your-center.example.com/api/v1/user/peers \
  -H "Authorization: Bearer <access_token>"
```

Response includes the echoed version header:

```
HTTP/1.1 200 OK
Autopeer-Version: 2026-06-07
Content-Type: application/json
```

```json
[
  {
    "id": "…",
    "remote_asn": 4242420000,
    "wg_preshared_key": "…",
    "endpoint_mismatch_since": null,
    "bgp_suspended_by_endpoint": false
  }
]
```

(Other peer fields are omitted here for brevity — see [`./peers.md`](./peers.md) for the
full peer object.)

### Pinned to an older version

```bash
curl https://your-center.example.com/api/v1/user/peers \
  -H "Authorization: Bearer <access_token>" \
  -H "Autopeer-Version: 2025-01-01"
```

```
HTTP/1.1 200 OK
Autopeer-Version: 2025-01-01
Content-Type: application/json
```

```json
[
  {
    "id": "…",
    "remote_asn": 4242420000
  }
]
```

The three newer fields (`wg_preshared_key`, `endpoint_mismatch_since`,
`bgp_suspended_by_endpoint`) are stripped, because both the `2026-06-07` and `2026-06-06`
changes are newer than the pinned `2025-01-01`.

### Unknown version (400)

```bash
curl -i https://your-center.example.com/api/v1/user/peers \
  -H "Authorization: Bearer <access_token>" \
  -H "Autopeer-Version: 2020-01-01"
```

```
HTTP/1.1 400 Bad Request
Content-Type: application/json
```

```json
{
  "error": "invalid_api_version",
  "message": "Unknown Autopeer-Version: 2020-01-01",
  "request_id": "<request-id>"
}
```
