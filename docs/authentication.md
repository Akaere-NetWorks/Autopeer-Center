# Authentication

The AutoPeer `center` exposes two parallel credential systems:

1. **JWT bearer tokens** — short-lived access tokens backed by server-side
   sessions, used by the web frontend and the CLI. This is the primary
   authentication mechanism for the `/api/v1` HTTP API.
2. **MCP API keys** — long-lived, hashed bearer keys (`ap_mcp_…`) used by Model
   Context Protocol clients. Keys are scoped by capabilities and are completely
   separate from the JWT/session machinery.

This document describes how both work, the token model, the session model, and
every supported login flow. For the request/response shape of individual
endpoints, see [`./api/README.md`](./api/README.md).

---

## JWT tokens

Access tokens are JSON Web Tokens signed with **HS256** using the `JWT_SECRET`
environment variable as the HMAC key. `JWT_SECRET` is **required** and must be
**at least 32 characters** — the center refuses to start otherwise.

Tokens are presented on the HTTP `Authorization` header:

```
Authorization: Bearer <access-token>
```

Token parsing enforces two invariants:

- The signing algorithm **must** be `HS256`; tokens signed with any other
  algorithm are rejected.
- The token type (`typ`) must be empty or `access`. This prevents a token minted
  for some other purpose from being replayed as an access token.

### Claims

The token payload (`middleware.Claims`) carries the following fields:

| Claim | JSON key | Description |
|---|---|---|
| Role | `role` | `user` or `admin`. Determines which routes the token may reach. |
| ASN | `asn` | DN42 ASN for `user` tokens (e.g. `4242420000`). Omitted for admin tokens. |
| Email | `email` | Contact email associated with the subject. |
| Session ID | `sid` | ID of the backing row in `auth_sessions`. Empty for legacy/sessionless tokens. |
| Token ID | `jti` | Unique per-token UUID. |
| Type | `typ` | Always `access` for tokens issued by the center. |
| Login method | `login_method` | How the session was created: `email`, `gpg`, `password`, `passkey`, `device_cli`, or `impersonation`. |
| Impersonating admin | `impersonated_by_admin_id` | Set only on impersonation tokens — the admin who started the session. |
| Parent session | `parent_sid` | Set only on impersonation tokens — the admin's own session that authorized the impersonation. |

In addition the standard registered claims are present: `sub` (the ASN for
users, the admin ID for admins), `iat`, `nbf`, and `exp`.

### Roles

There are exactly two roles:

- **`user`** — a DN42 operator, identified by their ASN. User tokens reach the
  `/api/v1/user/*` routes.
- **`admin`** — an operator of the AutoPeer deployment. Admin tokens reach the
  `/api/v1/admin/*` routes.

Impersonation tokens carry `role: user` (so they behave like a user) but also
carry `impersonated_by_admin_id` and `parent_sid` — see
[Admin impersonation](#6-admin-impersonation).

### Token lifetimes

Five TTLs control how long tokens live. All are configurable via environment
variables (see [`./configuration.md`](./configuration.md)) and accept Go
duration strings (e.g. `1h`, `30m`, `720h`).

| Token | Env var | Default |
|---|---|---|
| User access | `USER_ACCESS_TOKEN_TTL` | `1h` |
| Admin access | `ADMIN_ACCESS_TOKEN_TTL` | `30m` |
| User refresh | `USER_REFRESH_TOKEN_TTL` | `720h` (30 days) |
| Admin refresh | `ADMIN_REFRESH_TOKEN_TTL` | `336h` (14 days) |
| Impersonation | `IMPERSONATION_TOKEN_TTL` | `15m` |

Access tokens are short; clients keep their session alive by exchanging the
refresh token (see [Sessions](#sessions)). Impersonation sessions use a single
short TTL for both the access token and the (optional) refresh.

---

## Sessions

Every interactive login creates a row in the `auth_sessions` table and embeds
its ID in the token's `sid` claim. The session is the durable, revocable record
of a login; the access token is just a short-lived bearer of that session.

The session-aware middleware guards are:

- `RequireUserWithSessions` — requires `role: user`.
- `RequireAdminWithSessions` — requires `role: admin`.
- `RequireAnyAuthWithSessions` — requires either role.

> The deployed routes always use the `*WithSessions` variants. Non-session
> guards (`RequireUser`, `RequireAdmin`, `RequireAnyAuth`) exist in the codebase
> but are not mounted; this document describes the session-backed behavior.

On every authenticated request, after the JWT is validated the guard looks up
the session by `sid` and enforces:

- **Liveness** — the session must not be revoked (`revoked_at` is null) and must
  not be past its `expires_at`.
- **Role ↔ subject_type match** — an `admin` token must back an `admin` session;
  a `user` token must back a `user` *or* `impersonation` session. A mismatch is
  rejected.
- **Parent-session liveness (impersonation)** — for an `impersonation` session,
  the parent admin session is also looked up and must itself be live. When the
  admin's own session is revoked or expires, all impersonation sessions spawned
  from it stop working.

When validation passes, the session is **"touched"** asynchronously — its
last-seen timestamp is updated in a background goroutine so the check never adds
latency to the request.

### Refresh tokens and cookies

Refresh tokens are random secrets generated server-side, stored **only as a
SHA-256 hash** in the session row, and never embedded in a JWT. The raw refresh
token is delivered to the client as an `HttpOnly` cookie named `refresh_token`,
scoped to the `/api/v1/auth` path. The cookie is marked `Secure` whenever the
request arrives over TLS (or via a proxy that sets `X-Forwarded-Proto: https`),
and uses `SameSite=Lax`.

The refresh endpoint (`POST /api/v1/auth/refresh`) exchanges the cookie for a
fresh access token and **rotates** the refresh token (single-use). It also
detects **refresh-token reuse**: if a previously-rotated token is replayed, the
session and its children are revoked and the event is audit-logged — a defense
against stolen-token replay. Logout revokes the current session (and, for
non-impersonation sessions, its children) and clears the cookie.

### Device / session management

Authenticated users and admins can list and revoke their own sessions
("devices") — including a "log out everywhere else" action that revokes all
sessions except the current one. Impersonation sessions cannot manage devices.
Revoking a session takes effect immediately because every request re-checks
session liveness. See [`./api/README.md`](./api/README.md) for the device
endpoints.

---

## Login flows

All login endpoints live under `/api/v1/auth/`. The user-facing flows
(email, GPG, passkey login) are gated by a global "user login enabled" site
setting; when disabled they return `403 login_disabled`.

### 1. Email verification code

A two-step flow tied to the DN42 registry contact email of an ASN.

1. `POST /api/v1/auth/user/request-code` with the ASN. The center:
   - verifies a Cloudflare Turnstile token (when Turnstile is configured),
   - applies a per-ASN cooldown and a per-IP sliding-window rate limit,
   - looks up the ASN's contact email in the DN42 registry,
   - emails a 6-digit code (stored hashed with bcrypt, valid ~10 minutes), and
   - responds with a masked form of the destination email.
2. `POST /api/v1/auth/user/verify-code` with the ASN and code. On success the
   center mints a `user` session (`login_method: email`), returns the access
   token, and sets the refresh cookie.

Because the code is only ever sent to the registry contact email, control of
that mailbox proves control of the ASN.

### 2. GPG clearsign challenge

For ASNs whose DN42 `mntner` object lists one or more PGP keys, login can be
proven by signing a challenge.

1. `POST /api/v1/auth/user/check-gpg-availability` reports whether the ASN has
   any usable GPG fingerprints in the registry.
2. `POST /api/v1/auth/user/request-gpg-challenge` (Turnstile-gated,
   rate-limited) returns a random hex challenge string and a challenge ID, valid
   ~10 minutes.
3. The operator clearsigns the challenge, e.g.:

   ```bash
   echo "<challenge>" | gpg --clearsign
   ```

4. `POST /api/v1/auth/user/verify-gpg` with the ASN, challenge ID, and signed
   message. The center verifies the signature against the fingerprints from the
   `mntner` object — fetching the public key from a keyserver, or accepting a
   public key pasted in the request body — and checks that the signed text
   matches the challenge. On success it mints a `user` session
   (`login_method: gpg`).

### 3. Admin email + password

`POST /api/v1/auth/admin/login` with `email` and `password` (Turnstile-gated,
rate-limited). Credentials are checked against the `admins` table (passwords are
bcrypt-hashed). On success the center mints an `admin` session
(`login_method: password`).

The initial admin account is bootstrapped from `ADMIN_INITIAL_EMAIL` and
`ADMIN_INITIAL_PASSWORD` on startup (see [`./configuration.md`](./configuration.md)).

### 4. Passkey / WebAuthn

The center implements WebAuthn for both passwordless login and authenticated
enrollment. The relying party is configured via `WEBAUTHN_RPID` and
`WEBAUTHN_ORIGIN`.

**Passwordless login** (public endpoints, identified by ASN):

1. `POST /api/v1/auth/user/passkey/begin` with the ASN (Turnstile-gated, with
   per-IP and per-ASN rate limits) returns the WebAuthn assertion options. The
   response is intentionally generic — it does not reveal whether the ASN has
   any passkeys registered — to prevent enumeration.
2. `POST /api/v1/auth/user/passkey/finish?asn=<asn>` completes the assertion.
   On success the center mints a `user` session (`login_method: passkey`).

**Enrollment** (requires an existing authenticated user session):

- `GET /api/v1/passkey/status` — whether the current ASN has a passkey.
- `POST /api/v1/passkey/register/begin` / `POST /api/v1/passkey/register/finish`
  — register a new credential.
- `GET /api/v1/user/passkeys` and `DELETE /api/v1/user/passkeys/{id}` — list and
  remove credentials.

### 5. Device flow (OAuth-style)

For CLI clients that cannot easily run a browser, the center implements an
OAuth-2.0-style device authorization grant.

1. The CLI calls `POST /api/v1/auth/device/code` with a client/device name and a
   single scope (`user` or `admin`). It receives a `device_code`, a short
   human-typeable `user_code` (formatted `XXXX-XXXX`), a verification URI, an
   expiry, and a poll interval.
2. The user opens the verification URI in an already-authenticated browser
   session and reviews the request via
   `GET /api/v1/auth/device/request?user_code=…`, then approves or denies it via
   `POST /api/v1/auth/device/authorize`. The approving session's role must match
   the requested scope (only an admin session can approve an admin-scope device,
   only a user session can approve a user-scope device). Impersonation sessions
   cannot authorize devices.
3. The CLI polls `POST /api/v1/auth/device/token` with
   `grant_type: urn:ietf:params:oauth:grant-type:device_code` and the
   `device_code`. While pending it receives `authorization_pending`; polling too
   fast yields `slow_down`. Once approved, the exchange returns a full session
   (`login_method: device_cli`) — a `user` session for user scope, or an `admin`
   session for admin scope.

Device codes are stored only as keyed HMAC digests and expire after ~10 minutes.

### 6. Admin impersonation

An admin can act as a user for support/debugging via
`POST /api/v1/admin/auth/login-as` with a target ASN (and an optional `persist`
flag controlling whether a refresh cookie is set).

This mints a short-lived token with `role: user` but with `login_method:
impersonation`, `impersonated_by_admin_id` set to the admin, and `parent_sid`
set to the admin's own session. The backing session has `subject_type:
impersonation`. As noted under [Sessions](#sessions), the impersonation session
is only valid while the **parent admin session is still live**, so it dies with
the admin's logout or session revocation. Impersonation sessions are
deliberately restricted: they cannot authorize devices or manage device lists.
The action is audit-logged.

### Turnstile

When `TURNSTILE_SITE_KEY` and `TURNSTILE_SECRET_KEY` are configured, the
human-initiated login endpoints (request-code, request-gpg-challenge, admin
login, passkey begin) require a valid Cloudflare Turnstile token, validated
server-side against Cloudflare with the client IP. When the secret is unset,
Turnstile checks are skipped. `GET /api/v1/auth/turnstile-config` exposes the
site key and whether Turnstile is enabled.

---

## MCP API key authentication

Model Context Protocol clients authenticate with API keys rather than JWTs. Keys
are presented as bearer tokens on the `Authorization` header and validated by
dedicated middleware that **rejects JWTs outright** — only the `ap_mcp_…` prefix
is accepted.

There are two key types:

| Key type | Prefix | Middleware | Role injected |
|---|---|---|---|
| User MCP key | `ap_mcp_` | `RequireMCPKey` | `user` (+ the key's ASN) |
| Admin MCP key | `ap_mcp_admin_` | `RequireAdminMCPKey` | `admin` (+ the admin ID) |

`RequireMCPKey` accepts `ap_mcp_` keys but explicitly rejects any key that
begins with `ap_mcp_admin_`, so the two namespaces never overlap.

**Storage and validation.** Keys are random secrets. Only their **SHA-256 hash**
is stored — the raw key is shown exactly once, in the response to the create
call, and cannot be recovered afterward. On each request the middleware hashes
the presented key, looks it up, and rejects it if it is unknown, expired
(`expires_at` in the past), or revoked. Like sessions, keys are "touched"
(last-used timestamp) asynchronously so validation adds no latency.

**Capabilities.** Each key carries a capability list that scopes what it may do;
the granted capabilities are injected into the request context for handlers to
enforce. User keys draw from a read/write capability set such as `read:peers`,
`read:nodes`, `read:metrics`, `write:peer:create`, and `write:preferences`.
Admin keys draw from an admin-scoped read set such as `admin:read:peers`,
`admin:read:topology`, and `admin:read:settings`. Requested capabilities are
validated against the allowed set at creation time.

Keys are managed through the JWT-authenticated API (you cannot use an MCP key to
mint another MCP key): users manage their own keys under `/api/v1/user/mcp-keys`,
and admins manage admin keys (and may force-revoke user keys) under the admin
routes. See [`./api/README.md`](./api/README.md) for the management and MCP
transport endpoints.

---

## API versioning header

The HTTP API uses **Stripe-style, dated API versioning** on the `/api/v1` route
group. This is orthogonal to the `/v1` URL prefix: it controls the *shape* of
responses, not which routes exist.

Clients pin a version with the `Autopeer-Version` request header, using a dated
identifier in `YYYY-MM-DD` form, e.g.:

```
Autopeer-Version: 2026-06-06
```

Resolution rules (handled by middleware mounted on the whole `/api/v1` group):

- **Absent or empty header** → resolves to the latest known version.
- **A known dated version** → used as-is; the server downgrades versioned
  responses to that older shape via a chain of transformers.
- **An unknown version** → rejected with `400 invalid_api_version`.

The resolved version is always echoed back in the `Autopeer-Version` **response**
header. Requests on the latest version take a zero-overhead fast path, and error
bodies are never versioned. Only handlers that opt into versioned output are
affected; everything else returns the same shape regardless of the header.

See [`./api/README.md`](./api/README.md) for which resources are versioned
and what changed between dated versions.
