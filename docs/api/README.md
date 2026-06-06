# HTTP API Reference

This is the reference for the public HTTP API of `autopeer-center`. It documents
the conventions shared by every endpoint (base URL, content type, authentication,
errors, headers, limits) and links to the per-resource reference pages.

For background concepts, see [Authentication](../authentication.md),
the [WebSocket Protocol](../websocket-protocol.md),
[Configuration](../configuration.md), and [Deployment](../deployment.md).

> **Building a frontend against this API?** The AutoPeer frontend is not open-source.
> Hand the [**frontend design prompt**](../frontend-design-prompt.md) to an AI coding
> assistant — it interviews you about your hosting provider, recommends an SSR stack,
> and builds a complete, maintainable web UI against the endpoints documented here.

## Base URL

All endpoints are served under a single origin. Throughout these docs the base
URL is written as:

```
https://your-center.example.com
```

Replace it with the public origin of your deployment. WebSocket endpoints use the
`wss://` scheme on the same origin (for example `wss://your-center.example.com/api/v1/agent/ws`).

Application endpoints live under the `/api/v1` path prefix. Two utility endpoints
(`/health` and `/healthz`) sit at the root.

## Content type

All request and response bodies are JSON. Send `Content-Type: application/json`
on any request that has a body. Successful and error responses are written with
`Content-Type: application/json` (the sole exception is `/healthz`, which returns
`text/plain`).

Responses are produced by the shared helpers in
`internal/handler/helpers.go`:

- `JSON(w, status, data)` — encodes `data` as JSON with the given status.
- `JSONVersioned(w, r, status, resource, listKey, data)` — encodes the latest
  canonical shape, then downgrades it to the request's resolved
  `Autopeer-Version` (see [Versioning](./versioning.md)).
- `ErrorJSON(w, r, status, errCode, message)` — writes the standard error body
  (see [Errors](#errors)).

## Authentication

Authentication is configured per route group. A given endpoint accepts exactly
one of the schemes below (or none, for public endpoints). The reference page for
each endpoint states which scheme applies. The middleware that enforces these
lives in `internal/middleware/middleware.go`.

| Scheme | Header | Who | Used by |
|---|---|---|---|
| Public | _(none)_ | anyone | Public listings, registry lookup, stats, and the auth bootstrap flows |
| Bearer JWT (user) | `Authorization: Bearer <access_token>` | role `user` | User account, peers, MCP key management |
| Bearer JWT (admin) | `Authorization: Bearer <access_token>` | role `admin` | Admin management endpoints |
| Bearer JWT (any) | `Authorization: Bearer <access_token>` | role `user` or `admin` | Logout, device authorize/request |
| Refresh token | refresh cookie **or** JSON body field | any session | `POST /api/v1/auth/refresh`, `POST /api/v1/auth/logout` |
| MCP key | `Authorization: Bearer ap_mcp_<...>` | MCP client (acts as a user/ASN) | `/api/v1/mcp` |
| Admin MCP key | `Authorization: Bearer ap_mcp_admin_<...>` | admin MCP client | `/api/v1/admin/mcp` |
| Agent token | `X-Agent-Token: <token>` | node agent | `GET /api/v1/agent/download` |

Notes on the Bearer schemes:

- User/admin/any JWT routes use the session-validating middleware
  (`RequireUserWithSessions`, `RequireAdminWithSessions`,
  `RequireAnyAuthWithSessions`). Besides verifying the HS256 JWT, these check the
  associated server-side session is neither expired nor revoked.
- A missing or malformed token returns `401` with body
  `{"error":"unauthorized","message":"Invalid or missing token"}`.
- A valid token with the wrong role returns `403` with body
  `{"error":"forbidden","message":"Access denied"}` on the role-specific routes,
  or `{"error":"forbidden","message":"Authentication required"}` on the
  any-authenticated routes.
- A revoked or expired session returns `401` with body
  `{"error":"unauthorized","message":"Session expired or revoked"}`.

MCP keys are matched strictly by prefix: the user MCP middleware accepts only
`ap_mcp_` keys and explicitly rejects `ap_mcp_admin_` keys; the admin MCP
middleware accepts only `ap_mcp_admin_` keys. Invalid format, unknown, expired,
or revoked keys return `401`.

See [Authentication concepts](../authentication.md) for the full login, refresh,
device, GPG, passkey, and MCP-key flows.

## Errors

Errors emitted via the `ErrorJSON` helper have a fixed shape:

```json
{
  "error": "not_found",
  "message": "The requested resource does not exist",
  "request_id": "1f2e3d4c-5b6a-7890-abcd-ef0123456789"
}
```

- `error` — a short machine-readable code.
- `message` — a human-readable description.
- `request_id` — the per-request UUID (also returned in the `X-Request-ID`
  response header). Include it when reporting problems.

Individual handlers raise descriptive, endpoint-specific codes via
`ErrorJSON(w, r, status, code, message)` — the per-endpoint pages list the exact
status, code, and message for each failure path. The codes below are common
across the router and middleware:

| Status | Code | Where it comes from |
|---|---|---|
| `400` | `invalid_api_version` | The `Autopeer-Version` header is unknown (APIVersion middleware) |
| `401` | `unauthorized` | Missing/invalid token, key, or session |
| `403` | `forbidden` | Authenticated but wrong role |
| `404` | `not_found` | No route matches the requested path |
| `405` | `method_not_allowed` | The route exists but not for that HTTP method |
| `500` | `internal_error` | Unhandled panic (recovered) or response-encoding failure |

The router-level `404` and `405` bodies (`not_found`, `method_not_allowed`) and
the recovered-panic `500` (`internal_error`) are written directly and therefore
omit the `request_id` field; the `400 invalid_api_version` body includes it.
Error bodies are never transformed by API versioning.

## Response headers

| Header | On | Meaning |
|---|---|---|
| `X-Request-ID` | every response | A UUID generated per request (the `request_id` in error bodies) |
| `Autopeer-Version` | every `/api/v1` response | The resolved API version echoed back to the client |

CORS responses additionally expose `X-Request-ID` and `Autopeer-Version` via
`Access-Control-Expose-Headers`.

## API versioning

Every `/api/v1` route passes the `APIVersion` middleware. It reads the optional
`Autopeer-Version` request header:

- absent or empty ⇒ resolves to the latest version;
- a known dated version ⇒ used as-is;
- an unknown version ⇒ `400 invalid_api_version`.

The resolved version is echoed in the `Autopeer-Version` response header. Only
handlers that call `JSONVersioned` actually reshape their output for older
versions. See [Versioning](./versioning.md) for the version list and the per-resource
changes.

## Request body limit

Request bodies are capped at **1 MiB** (`middleware.BodyLimit(1 << 20)`) globally.
A body exceeding the limit causes the read to fail; the handler then returns its
own decode/validation error.

## CORS

CORS is driven by the `CORS_ORIGIN` configuration value (a comma-separated allow
list, or `*` to allow any origin). For an allowed origin the server reflects it
in `Access-Control-Allow-Origin` and sets `Access-Control-Allow-Credentials: true`.
Allowed methods are `GET, POST, PUT, DELETE, OPTIONS`; allowed request headers are
`Content-Type, Authorization, Autopeer-Version`. Preflight `OPTIONS` requests are
answered with `204 No Content`. See [Configuration](../configuration.md) for
`CORS_ORIGIN`.

## Health checks

Two unauthenticated endpoints are mounted at the root for liveness/readiness
probes:

| Method | Path | Response |
|---|---|---|
| `GET` | `/health` | `200`, JSON `{"status":"ok"}` |
| `GET` | `/healthz` | `200`, text/plain `OK` |

## Reference

Per-resource endpoint pages:

- [Versioning](./versioning.md) — the `Autopeer-Version` header, version list, and downgrade rules.
- [Public endpoints](./public.md) — unauthenticated listings, registry lookup, and stats.
- [Authentication endpoints](./auth.md) — login, refresh, logout, device, GPG, passkey, and Turnstile flows.
- [Peers](./peers.md) — user peer lifecycle (list, create, get, update, delete, metrics).
- [Nodes](./nodes.md) — public node listing.
- [Account](./account.md) — devices, email/notification/Telegram preferences, and audit.
- [MCP](./mcp.md) — MCP key management and the MCP transport endpoints.
- [Admin](./admin.md) — admin management of peers, nodes, releases, settings, and users.
- [Admin operations](./admin-ops.md) — system status, queue monitor, and operational tooling.

Related concept docs:

- [Authentication concepts](../authentication.md)
- [WebSocket protocol](../websocket-protocol.md)
- [Configuration](../configuration.md)
- [Deployment](../deployment.md)

### Per-endpoint entry format

Each endpoint entry in the reference pages records:

1. The HTTP **method and path** (with `{param}` path parameters).
2. The **auth scheme** required.
3. **Path / query parameters** and their meaning.
4. The **request body** (field names, JSON tags, and Go types) where applicable.
5. The **success response** (status and JSON shape).
6. The **error responses** (status, `error` code, and message) the handler can return.
</content>
</invoke>
