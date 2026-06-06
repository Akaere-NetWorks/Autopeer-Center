# Authentication API

Login, session/device management, and passkey (WebAuthn) endpoints. For the conceptual overview of the JWT + refresh-cookie model, roles, and login methods, see [../authentication.md](../authentication.md).

Base URL: `https://your-center.example.com`

All routes below are under `/api/v1` and pass the `APIVersion` middleware (it echoes the `Autopeer-Version` header and returns `400 invalid_api_version` for an unknown version; see [./versioning.md](./versioning.md)). None of these handlers call `JSONVersioned`, so their bodies are not transformed by the requested version.

Error bodies always have the form `{"error":"<code>","message":"<message>","request_id":"<id>"}`.

## Session response shape

Successful login, refresh, device-exchange, and passkey-login endpoints all return the same `authResponse` shape. Tokens are delivered as JSON (access token) plus an HTTP-only refresh cookie:

- The access token is returned in BOTH `token` and `access_token` (identical value), an HS256 JWT.
- The refresh token is set as a cookie named `refresh_token` (`HttpOnly`, `SameSite=Lax`, `Path=/api/v1/auth`, `Secure` when the request is HTTPS — `r.TLS != nil` or `X-Forwarded-Proto: https`). It is never returned in the JSON body.

`authResponse` fields:

| Field | Type | Description |
|---|---|---|
| `token` | string | Access JWT (same value as `access_token`). |
| `access_token` | string | Access JWT. |
| `expires_in` | integer | Access-token lifetime in seconds. |
| `refresh_expires_in` | integer | Refresh-token lifetime in seconds (omitted when zero). |
| `asn` | integer | The authenticated ASN (omitted when zero, e.g. admin sessions). |
| `session` | object | The `AuthSession` record (see below; omitted when absent). |
| `user` | object | `{ "role": string, "asn": integer (omitted when null), "email": string (omitted when empty) }`. |

The embedded `session` object (`model.AuthSession`) serializes as:

| Field | Type | Description |
|---|---|---|
| `id` | string | Session UUID. |
| `subject_type` | string | `user`, `admin`, or `impersonation`. |
| `asn` | integer | ASN, when applicable (omitted when null). |
| `admin_id` | string | Admin UUID, when applicable (omitted when null). |
| `email` | string | Account email (omitted when empty). |
| `parent_session_id` | string | Parent session for impersonation sessions (omitted when null). |
| `user_agent` | string | Client User-Agent at login (omitted when empty). |
| `ip_address` | string | Client IP at login (omitted when empty). |
| `device_name` | string | Friendly device label (omitted when empty). |
| `login_method` | string | `email`, `gpg`, `passkey`, `password`, `device_cli`, or `impersonation` (omitted when empty). |
| `created_at` | string | RFC 3339 timestamp. |
| `last_used_at` | string | RFC 3339 timestamp (omitted when null). |
| `expires_at` | string | RFC 3339 timestamp. |
| `revoked_at` | string | RFC 3339 timestamp (omitted when null). |
| `revoked_reason` | string | Revocation reason (omitted when empty). |

The `refresh_token_hash` and previous-token-hash fields are tagged `json:"-"` and never serialized.

---

# User login (email code)

## `POST /api/v1/auth/user/request-code`
Sends a 6-digit verification code to the email on the ASN's DN42 registry `mntner` object.

- **Auth:** public
- **Path parameters:** None.
- **Query parameters:** None.
- **Request body** (`requestCodeReq`):

| Field | Type | Required | Description |
|---|---|---|---|
| `asn` | integer (int64) | Yes | DN42 ASN to log in as (must be > 0). |
| `cf_turnstile_response` | string | Conditional | Cloudflare Turnstile token; required only when Turnstile is configured (`TURNSTILE_SECRET_KEY` set). |

```json
{
  "asn": 4242420000,
  "cf_turnstile_response": "0.AbCdEf..."
}
```

- **Response `200`:**

| Field | Type | Description |
|---|---|---|
| `masked_email` | string | Partially-masked registry email the code was sent to. |
| `expires_in` | integer | Code lifetime in seconds (600). |

```json
{
  "masked_email": "ad***@example.com",
  "expires_in": 600
}
```

- **Errors:**

| Status | `error` code | Condition |
|---|---|---|
| 403 | `login_disabled` | User login is disabled (`user_login_enabled` site setting != `true`). |
| 400 | `bad_request` | Invalid JSON body. |
| 400 | `bad_request` | `asn` <= 0. |
| 403 | `turnstile_failed` | Turnstile verification failed. |
| 429 | `rate_limited` | Per-ASN 60-second cooldown hit. |
| 429 | `rate_limited` | Too many requests from this IP (>5/min). |
| 404 | `asn_not_found` | ASN not found in the DN42 registry. |
| 500 | `internal_error` | Failed to generate/process/store the verification code. |
| 500 | `email_error` | Failed to send the verification email. |

- **Source:** `AuthHandler.RequestCode` — `internal/handler/auth.go`

## `POST /api/v1/auth/user/verify-code`
Verifies the emailed code and issues a user session.

- **Auth:** public
- **Path parameters:** None.
- **Query parameters:** None.
- **Request body** (`verifyCodeReq`):

| Field | Type | Required | Description |
|---|---|---|---|
| `asn` | integer (int64) | Yes | DN42 ASN (must be > 0). |
| `code` | string | Yes | The 6-digit verification code. |

```json
{
  "asn": 4242420000,
  "code": "123456"
}
```

- **Response `200`:** `authResponse` (see [Session response shape](#session-response-shape); `login_method` is `email`). Sets the `refresh_token` cookie.

```json
{
  "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
  "access_token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
  "expires_in": 3600,
  "refresh_expires_in": 2592000,
  "asn": 4242420000,
  "session": {
    "id": "0c2c9a1e-3c1b-4f2a-9a8e-1b2c3d4e5f60",
    "subject_type": "user",
    "asn": 4242420000,
    "email": "admin@example.com",
    "user_agent": "Mozilla/5.0",
    "ip_address": "fe80::1",
    "device_name": "Mozilla/5.0",
    "login_method": "email",
    "created_at": "2026-06-07T12:00:00Z",
    "expires_at": "2026-07-07T12:00:00Z"
  },
  "user": { "role": "user", "asn": 4242420000, "email": "admin@example.com" }
}
```

- **Errors:**

| Status | `error` code | Condition |
|---|---|---|
| 403 | `login_disabled` | User login is disabled. |
| 400 | `bad_request` | Invalid JSON body. |
| 400 | `bad_request` | `asn` <= 0 or `code` empty. |
| 429 | `rate_limited` | Too many attempts from this IP (>10/min). |
| 429 | `rate_limited` | Per-ASN 3-second cooldown after a recent failed attempt. |
| 401 | `invalid_code` | No valid code, wrong code, or expired. |
| 401 | `invalid_code` | Code already used (mark-used race lost). |
| 500 | `internal_error` | Failed to generate the session token. |

- **Source:** `AuthHandler.VerifyCode` — `internal/handler/auth.go`

---

# User login (GPG)

## `POST /api/v1/auth/user/request-gpg-challenge`
Issues a random 32-byte hex challenge to be clearsigned with a GPG key matching a fingerprint in the ASN's `mntner` object.

- **Auth:** public
- **Path parameters:** None.
- **Query parameters:** None.
- **Request body** (`requestGPGChallengeReq`):

| Field | Type | Required | Description |
|---|---|---|---|
| `asn` | integer (int64) | Yes | DN42 ASN (must be > 0). |
| `cf_turnstile_response` | string | Conditional | Turnstile token; required only when Turnstile is configured. |

```json
{
  "asn": 4242420000,
  "cf_turnstile_response": "0.AbCdEf..."
}
```

- **Response `200`:**

| Field | Type | Description |
|---|---|---|
| `challenge_id` | string | Challenge UUID, echoed back when verifying. |
| `challenge_text` | string | Hex string to clearsign. |
| `expires_in` | integer | Challenge lifetime in seconds (600). |

```json
{
  "challenge_id": "8f1d2c3b-4a5e-6f70-8192-a3b4c5d6e7f8",
  "challenge_text": "a1b2c3d4e5f6...",
  "expires_in": 600
}
```

- **Errors:**

| Status | `error` code | Condition |
|---|---|---|
| 403 | `login_disabled` | User login is disabled. |
| 400 | `bad_request` | Invalid JSON body. |
| 400 | `bad_request` | `asn` <= 0. |
| 403 | `turnstile_failed` | Turnstile verification failed. |
| 429 | `rate_limited` | Per-ASN 60-second cooldown hit. |
| 429 | `rate_limited` | Too many requests from this IP (>5/min). |
| 404 | `asn_not_found` | Fingerprint lookup failed / ASN not found in the DN42 registry. |
| 404 | `gpg_not_supported` | No GPG fingerprint in the `mntner` object. |
| 500 | `internal_error` | Failed to generate or store the challenge. |

- **Source:** `AuthHandler.RequestGPGChallenge` — `internal/handler/auth.go`

## `POST /api/v1/auth/user/verify-gpg`
Verifies a clearsigned (or detached) GPG signature over the challenge and issues a user session.

- **Auth:** public
- **Path parameters:** None.
- **Query parameters:** None.
- **Request body** (`verifyGPGReq`):

| Field | Type | Required | Description |
|---|---|---|---|
| `asn` | integer (int64) | Yes | DN42 ASN (must be > 0). |
| `challenge_id` | string | Yes | Challenge UUID from `request-gpg-challenge`. |
| `signed_message` | string | Yes | The GPG clearsigned message (e.g. `gpg --clearsign`). |
| `public_key` | string | No | Optional armored public key; if given it is used directly instead of fetching from a keyserver. |

```json
{
  "asn": 4242420000,
  "challenge_id": "8f1d2c3b-4a5e-6f70-8192-a3b4c5d6e7f8",
  "signed_message": "-----BEGIN PGP SIGNED MESSAGE-----\nHash: SHA256\n\na1b2c3d4...\n-----BEGIN PGP SIGNATURE-----\n...\n-----END PGP SIGNATURE-----\n",
  "public_key": "-----BEGIN PGP PUBLIC KEY BLOCK-----\n...\n-----END PGP PUBLIC KEY BLOCK-----\n"
}
```

- **Response `200`:** `authResponse` (see [Session response shape](#session-response-shape); `login_method` is `gpg`). Sets the `refresh_token` cookie. (Email may be empty if the registry lookup fails; the login still succeeds.)

- **Errors:**

| Status | `error` code | Condition |
|---|---|---|
| 403 | `login_disabled` | User login is disabled. |
| 400 | `bad_request` | Invalid JSON body. |
| 400 | `bad_request` | `asn` <= 0, or `challenge_id`/`signed_message` empty. |
| 429 | `rate_limited` | Too many attempts from this IP (>10/min). |
| 401 | `invalid_challenge` | Challenge not found or expired. |
| 400 | `invalid_signature` | Signed message could not be parsed. |
| 401 | `invalid_signature` | Signed text does not match the challenge, or cryptographic verification / fingerprint match failed (message carries detail). |
| 401 | `invalid_challenge` | Challenge already used. |
| 500 | `internal_error` | Failed to generate the session token. |

- **Source:** `AuthHandler.VerifyGPGSignature` — `internal/handler/auth.go`

## `POST /api/v1/auth/user/check-gpg-availability`
Reports whether the ASN's `mntner` object has any GPG fingerprint (i.e. whether GPG login is possible).

- **Auth:** public
- **Path parameters:** None.
- **Query parameters:** None.
- **Request body** (`checkGPGAvailabilityReq`):

| Field | Type | Required | Description |
|---|---|---|---|
| `asn` | integer (int64) | Yes | DN42 ASN (must be > 0). |

```json
{ "asn": 4242420000 }
```

- **Response `200`:**

| Field | Type | Description |
|---|---|---|
| `available` | boolean | `true` if at least one GPG fingerprint is found, else `false`. |

```json
{ "available": true }
```

- **Errors:**

| Status | `error` code | Condition |
|---|---|---|
| 400 | `bad_request` | Invalid JSON body. |
| 400 | `bad_request` | `asn` <= 0. |

> A registry lookup failure or no-fingerprint result is NOT an error: it returns `200` with `{"available": false}`.

- **Source:** `AuthHandler.CheckGPGAvailability` — `internal/handler/auth.go`

---

# User login (passkey / WebAuthn)

These two public endpoints perform a passwordless WebAuthn assertion against an ASN that has previously registered a passkey (see [Passkey management](#passkey-management-user)).

## `POST /api/v1/auth/user/passkey/begin`
Begins a passkey login: returns WebAuthn assertion options for the ASN. Sets `Cache-Control: no-store`.

- **Auth:** public
- **Path parameters:** None.
- **Query parameters:** None.
- **Request body** (anonymous struct):

| Field | Type | Required | Description |
|---|---|---|---|
| `asn` | integer (int64) | Yes | DN42 ASN (must be non-zero). |
| `cf_turnstile_response` | string | Conditional | Turnstile token; required only when Turnstile is configured. |

```json
{
  "asn": 4242420000,
  "cf_turnstile_response": "0.AbCdEf..."
}
```

- **Response `200`:** The WebAuthn `CredentialAssertion` options object produced by the WebAuthn library (passed straight to `navigator.credentials.get`). Shape is determined by the library, e.g.:

```json
{
  "publicKey": {
    "challenge": "rZ8h...",
    "timeout": 60000,
    "rpId": "your-center.example.com",
    "allowCredentials": [
      { "type": "public-key", "id": "Aa-Bb-Cc..." }
    ],
    "userVerification": "preferred"
  }
}
```

- **Errors:**

| Status | `error` code | Condition |
|---|---|---|
| 403 | `login_disabled` | User login is disabled. |
| 400 | `bad_request` | Invalid JSON body or `asn` is 0. |
| 429 | `rate_limited` | Too many requests from this IP (>10/min on the `pk:begin` bucket). |
| 403 | `turnstile_failed` | Turnstile verification failed. |
| 429 | `rate_limited` | Per-ASN 60-second cooldown hit. |
| 400 | `passkey_not_available` | The ASN has no registered passkeys (generic, to avoid enumeration). |
| 500 | `internal_error` | Failed to begin login, serialize, or save the session. |

- **Source:** `PasskeyHandler.LoginBegin` — `internal/handler/auth_passkey.go`

## `POST /api/v1/auth/user/passkey/finish`
Completes the passkey assertion and issues a user session. Sets `Cache-Control: no-store`.

- **Auth:** public
- **Path parameters:** None.
- **Query parameters:**

| Name | Type | Required | Description |
|---|---|---|---|
| `asn` | integer | Yes | The ASN that began the login (parsed with `%d`; must be non-zero). |

- **Request body:** The WebAuthn `AuthenticatorAssertionResponse` (the credential returned by `navigator.credentials.get`), parsed directly from the request body by the WebAuthn library.

```json
{
  "id": "Aa-Bb-Cc...",
  "rawId": "Aa-Bb-Cc...",
  "type": "public-key",
  "response": {
    "authenticatorData": "...",
    "clientDataJSON": "...",
    "signature": "...",
    "userHandle": "..."
  }
}
```

- **Response `200`:** `authResponse` (see [Session response shape](#session-response-shape); `login_method` is `passkey`, email best-effort from the registry). Sets the `refresh_token` cookie.

- **Errors:**

| Status | `error` code | Condition |
|---|---|---|
| 403 | `login_disabled` | User login is disabled. |
| 429 | `rate_limited` | Too many requests from this IP (>20/min on the `pk:finish` bucket). |
| 400 | `bad_request` | `asn` query parameter missing or 0. |
| 429 | `rate_limited` | Per-ASN 3-second cooldown after a recent failed attempt. |
| 400 | `session_not_found` | Login session not found or expired. |
| 500 | `internal_error` | Failed to deserialize/load credentials, generate/create the session, or sign the token. |
| 401 | `login_failed` | Passkey assertion verification failed. |

- **Source:** `PasskeyHandler.LoginFinish` — `internal/handler/auth_passkey.go`

---

# Admin login

## `POST /api/v1/auth/admin/login`
Logs in an admin with email + password and issues an admin session.

- **Auth:** public
- **Path parameters:** None.
- **Query parameters:** None.
- **Request body** (`adminLoginReq`):

| Field | Type | Required | Description |
|---|---|---|---|
| `email` | string | Yes | Admin email. |
| `password` | string | Yes | Admin password. |
| `cf_turnstile_response` | string | Conditional | Turnstile token; required only when Turnstile is configured. |

```json
{
  "email": "admin@example.com",
  "password": "s3cr3t",
  "cf_turnstile_response": "0.AbCdEf..."
}
```

- **Response `200`:** `authResponse` (see [Session response shape](#session-response-shape); `user.role` is `admin`, `login_method` is `password`, no top-level `asn`). Sets the `refresh_token` cookie.

```json
{
  "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
  "access_token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
  "expires_in": 1800,
  "refresh_expires_in": 1209600,
  "session": { "id": "…", "subject_type": "admin", "admin_id": "11111111-2222-3333-4444-555555555555", "email": "admin@example.com", "login_method": "password", "created_at": "2026-06-07T12:00:00Z", "expires_at": "2026-06-21T12:00:00Z" },
  "user": { "role": "admin", "email": "admin@example.com" }
}
```

- **Errors:**

| Status | `error` code | Condition |
|---|---|---|
| 400 | `bad_request` | Invalid JSON body. |
| 400 | `bad_request` | `email` or `password` empty. |
| 403 | `turnstile_failed` | Turnstile verification failed. |
| 429 | `rate_limited` | Too many login attempts from this IP (>5/min). |
| 401 | `invalid_credentials` | Unknown email or wrong password. |
| 500 | `internal_error` | Failed to generate the session token. |

- **Source:** `AuthHandler.AdminLogin` — `internal/handler/auth.go`

## `POST /api/v1/admin/auth/login-as`
Issues a short-lived impersonation session so an admin can act as a given ASN's user.

- **Auth:** Bearer JWT (admin), session-backed
- **Path parameters:** None.
- **Query parameters:** None.
- **Request body** (anonymous struct):

| Field | Type | Required | Description |
|---|---|---|---|
| `asn` | integer (int64) | Yes | ASN to impersonate (must be > 0). |
| `persist` | boolean | No | If `true`, also sets the impersonation refresh cookie. |

```json
{ "asn": 4242420000, "persist": false }
```

- **Response `200`:** `authResponse` (see [Session response shape](#session-response-shape); `session.subject_type` is `impersonation`, `login_method` is `impersonation`, `user.role` is `user`). `refresh_expires_in` is omitted (the impersonation `authResponse` carries no refresh TTL). The refresh cookie is set only when `persist` is `true`.

- **Errors:**

| Status | `error` code | Condition |
|---|---|---|
| 401 | `session_required` | The admin token is not session-backed (no admin ID / parent session). |
| 400 | `bad_request` | Invalid JSON body. |
| 400 | `bad_request` | `asn` <= 0. |
| 404 | `asn_not_found` | ASN not found in the DN42 registry. |
| 500 | `internal_error` | Failed to generate the impersonation token. |

- **Source:** `AuthHandler.AdminLoginAs` — `internal/handler/auth.go`

---

# Tokens and session lifecycle

## `POST /api/v1/auth/refresh`
Rotates the refresh token (read from the `refresh_token` cookie) and returns a fresh access token. Detects refresh-token reuse and revokes the affected session family.

- **Auth:** public (authenticates via the `refresh_token` cookie)
- **Path parameters:** None.
- **Query parameters:** None.
- **Request body:** None. The refresh token is read from the `refresh_token` cookie.
- **Response `200`:** `authResponse` (see [Session response shape](#session-response-shape)). `refresh_expires_in` reflects time remaining until `session.expires_at`. Sets a rotated `refresh_token` cookie.

```json
{
  "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
  "access_token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
  "expires_in": 3600,
  "refresh_expires_in": 2587000,
  "asn": 4242420000,
  "session": { "id": "…", "subject_type": "user", "asn": 4242420000, "email": "admin@example.com", "login_method": "email", "created_at": "2026-06-07T12:00:00Z", "expires_at": "2026-07-07T12:00:00Z" },
  "user": { "role": "user", "asn": 4242420000, "email": "admin@example.com" }
}
```

- **Errors:**

| Status | `error` code | Condition |
|---|---|---|
| 401 | `unauthorized` | Missing/empty refresh cookie. |
| 401 | `unauthorized` | Invalid refresh token (reuse triggers revocation of the session family; clears the cookie). |
| 401 | `unauthorized` | Session has no admin ID / ASN / parent-session as required by its `subject_type`, or its `subject_type` cannot be refreshed. |
| 401 | `unauthorized` | Refresh token was already used (rotation lost the race; clears the cookie). |
| 500 | `internal_error` | Failed to rotate or generate the token. |

- **Source:** `AuthHandler.Refresh` — `internal/handler/auth.go`

## `POST /api/v1/auth/logout`
Revokes the current session and clears the refresh cookie. For non-impersonation sessions, child sessions are also revoked. For impersonation sessions only the impersonation session itself is revoked and the cookie is left intact.

- **Auth:** Bearer JWT (any: user or admin)
- **Path parameters:** None.
- **Query parameters:** None.
- **Request body:** None.
- **Response `200`:**

```json
{ "ok": true }
```

- **Errors:** None from the handler — it always returns `200` (best-effort revoke). The route group's `RequireAnyAuthWithSessions` middleware rejects unauthenticated requests before the handler runs.
- **Source:** `AuthHandler.Logout` — `internal/handler/auth.go`

## `GET /api/v1/auth/user/login-status`
Reports whether user login is currently enabled in site settings.

- **Auth:** public
- **Path parameters:** None.
- **Query parameters:** None.
- **Request body:** None.
- **Response `200`:**

| Field | Type | Description |
|---|---|---|
| `enabled` | boolean | `true` if user login is enabled (defaults to `true` if the admin handler is unavailable). |

```json
{ "enabled": true }
```

- **Errors:** None.
- **Source:** `AuthHandler.LoginStatus` — `internal/handler/auth.go`

## `GET /api/v1/auth/turnstile-config`
Returns whether Cloudflare Turnstile is enabled and its public site key, so the frontend can render the widget.

- **Auth:** public
- **Path parameters:** None.
- **Query parameters:** None.
- **Request body:** None.
- **Response `200`:**

| Field | Type | Description |
|---|---|---|
| `enabled` | boolean | `true` when both the Turnstile site key and secret key are configured. |
| `site_key` | string | The public Turnstile site key (may be empty). |

```json
{ "enabled": true, "site_key": "0x4AAAAAAA..." }
```

- **Errors:** None.
- **Source:** `AuthHandler.GetTurnstileConfig` — `internal/handler/auth.go`

---

# Device authorization (TUI / CLI, OAuth-style device flow)

A device (e.g. the TUI) requests a `device_code` + `user_code`, the user approves the `user_code` in an authenticated browser session, and the device polls `token` to exchange the `device_code` for a session. All four handlers set `Cache-Control: no-store`.

## `POST /api/v1/auth/device/code`
Starts the device flow: returns a device code, a user code, and verification URIs.

- **Auth:** public
- **Path parameters:** None.
- **Query parameters:** None.
- **Request body** (`deviceCodeReq`):

| Field | Type | Required | Description |
|---|---|---|---|
| `client_name` | string | No | Client identifier (default `autopeer-tui`, truncated to 80 chars). |
| `device_name` | string | No | Friendly device label (default derived from User-Agent, truncated to 120 chars). |
| `version` | string | No | Client version string (truncated to 40 chars). |
| `scopes` | array of string | No | Exactly one of `user` or `admin` (default `["user"]`). |

```json
{
  "client_name": "autopeer-tui",
  "device_name": "workstation",
  "version": "1.0.0",
  "scopes": ["user"]
}
```

- **Response `200`** (`deviceCodeResp`):

| Field | Type | Description |
|---|---|---|
| `device_code` | string | Secret code the device polls with (prefixed `ap_dev_`). |
| `user_code` | string | Human-typed code in `XXXX-XXXX` form. |
| `verification_uri` | string | URL where the user approves the device (`<frontend>/cli/activate`). |
| `verification_uri_complete` | string | `verification_uri` with the `user_code` pre-filled (omitted when empty). |
| `expires_in` | integer | Grant lifetime in seconds (600). |
| `interval` | integer | Recommended poll interval in seconds (5). |

```json
{
  "device_code": "ap_dev_3f9a2b...",
  "user_code": "ABCD-2345",
  "verification_uri": "https://your-center.example.com/cli/activate",
  "verification_uri_complete": "https://your-center.example.com/cli/activate?user_code=ABCD-2345",
  "expires_in": 600,
  "interval": 5
}
```

- **Errors:**

| Status | `error` code | Condition |
|---|---|---|
| 429 | `rate_limited` | Too many device requests from this IP (>20/min). |
| 400 | `bad_request` | Invalid JSON body. |
| 400 | `unsupported_scope` | More than one scope, or a scope other than `user`/`admin`. |
| 500 | `internal_error` | Failed to generate the device/user code or store the grant. |

- **Source:** `AuthHandler.DeviceCode` — `internal/handler/auth.go`

## `GET /api/v1/auth/device/request`
Looks up a pending device grant by its `user_code` so the approving UI can show the device details. Called from the authenticated approval page.

- **Auth:** Bearer JWT (any: user or admin), re-checked in-handler via `ExtractClaims`. Impersonation sessions are rejected.
- **Path parameters:** None.
- **Query parameters:**

| Name | Type | Required | Description |
|---|---|---|---|
| `user_code` | string | Yes | The user code (with or without dash; case-insensitive). |

- **Request body:** None.
- **Response `200`** (`deviceRequestResp`):

| Field | Type | Description |
|---|---|---|
| `user_code` | string | Normalized code in `XXXX-XXXX` form. |
| `client_name` | string | Client identifier. |
| `device_name` | string | Device label (omitted when empty). |
| `version` | string | Client version (omitted when empty). |
| `scopes` | array of string | Requested scopes. |
| `status` | string | Grant status (`pending`, `approved`, `denied`, `exchanged`). |
| `created_at` | string | RFC 3339 timestamp. |
| `expires_at` | string | RFC 3339 timestamp. |

```json
{
  "user_code": "ABCD-2345",
  "client_name": "autopeer-tui",
  "device_name": "workstation",
  "version": "1.0.0",
  "scopes": ["user"],
  "status": "pending",
  "created_at": "2026-06-07T12:00:00Z",
  "expires_at": "2026-06-07T12:10:00Z"
}
```

- **Errors:**

| Status | `error` code | Condition |
|---|---|---|
| 401 | `unauthorized` | Invalid or missing token. |
| 403 | `forbidden` | Token role is neither user nor admin. |
| 403 | `forbidden` | Impersonation session. |
| 400 | `bad_request` | Invalid device (user) code format. |
| 404 | `device_request_not_found` | Grant not found or expired. |
| 403 | `forbidden` | Only admin sessions may view admin-scope grants; only user sessions may view user-scope grants. |

- **Source:** `AuthHandler.DeviceRequest` — `internal/handler/auth.go`

## `POST /api/v1/auth/device/authorize`
Approves or denies a device grant on behalf of the current session's user/admin.

- **Auth:** Bearer JWT (any: user or admin), re-checked in-handler via `ExtractClaims`. Impersonation sessions are rejected.
- **Path parameters:** None.
- **Query parameters:** None.
- **Request body** (`deviceAuthorizeReq`):

| Field | Type | Required | Description |
|---|---|---|---|
| `user_code` | string | Yes | The user code (with or without dash; case-insensitive). |
| `decision` | string | Yes | `approve`, or `deny`/`denied` (case-insensitive). |

```json
{ "user_code": "ABCD-2345", "decision": "approve" }
```

- **Response `200`:**

| Field | Type | Description |
|---|---|---|
| `ok` | boolean | Always `true` on success. |
| `status` | string | New grant status (`approved` or `denied`). |

```json
{ "ok": true, "status": "approved" }
```

- **Errors:**

| Status | `error` code | Condition |
|---|---|---|
| 401 | `unauthorized` | Invalid or missing token. |
| 403 | `forbidden` | Token role is neither user nor admin. |
| 403 | `forbidden` | Impersonation session. |
| 400 | `bad_request` | Invalid JSON body. |
| 400 | `bad_request` | Invalid device (user) code format. |
| 400 | `bad_request` | `decision` is not approve/deny/denied. |
| 404 | `device_request_not_found` | Grant not found or expired. |
| 403 | `forbidden` | Scope/role mismatch (admin grants need admin, user grants need user). |
| 409 | `device_request_inactive` | Grant is not pending or has expired. |

- **Source:** `AuthHandler.DeviceAuthorize` — `internal/handler/auth.go`

## `POST /api/v1/auth/device/token`
Polled by the device to exchange an approved `device_code` for a session.

- **Auth:** public
- **Path parameters:** None.
- **Query parameters:** None.
- **Request body** (`deviceTokenReq`):

| Field | Type | Required | Description |
|---|---|---|---|
| `grant_type` | string | Yes | Must be `urn:ietf:params:oauth:grant-type:device_code`. |
| `device_code` | string | Yes | The `device_code` from `/auth/device/code`. |

```json
{
  "grant_type": "urn:ietf:params:oauth:grant-type:device_code",
  "device_code": "ap_dev_3f9a2b..."
}
```

- **Response `200`:** `authResponse` (see [Session response shape](#session-response-shape); `login_method` is `device_cli`, role per the grant scope). Sets the `refresh_token` cookie.

- **Errors:**

| Status | `error` code | Condition |
|---|---|---|
| 429 | `slow_down` | Polling too fast (>60/min); `Retry-After` header is set. |
| 400 | `bad_request` | Invalid JSON body. |
| 400 | `unsupported_grant_type` | Wrong `grant_type` or empty `device_code`. |
| 400 | `expired_token` | Invalid or expired device code (poll lookup failed). |
| 400 | `expired_token` | Device code expired (grant past its `expires_at`). |
| 400 | `authorization_pending` | Grant not yet approved. |
| 400 | `access_denied` | Grant was denied. |
| 400 | `expired_token` | Grant already exchanged, or in an inactive state. |
| 401 | `expired_token` | Approved grant could not be turned into a session. |

- **Source:** `AuthHandler.DeviceToken` — `internal/handler/auth.go`

---

# Session / device management (user)

These endpoints let a user list and revoke their own active sessions ("devices"). Impersonation sessions cannot manage devices.

## `GET /api/v1/user/devices`
Lists the calling user's active sessions.

- **Auth:** Bearer JWT (user), session-backed
- **Path parameters:** None.
- **Query parameters:** None.
- **Request body:** None.
- **Response `200`:** A bare JSON array of `AuthSession` objects (see [Session response shape](#session-response-shape) for the object fields).

```json
[
  {
    "id": "0c2c9a1e-3c1b-4f2a-9a8e-1b2c3d4e5f60",
    "subject_type": "user",
    "asn": 4242420000,
    "email": "admin@example.com",
    "user_agent": "Mozilla/5.0",
    "ip_address": "fe80::1",
    "device_name": "Mozilla/5.0",
    "login_method": "email",
    "created_at": "2026-06-07T12:00:00Z",
    "last_used_at": "2026-06-07T12:30:00Z",
    "expires_at": "2026-07-07T12:00:00Z"
  }
]
```

- **Errors:**

| Status | `error` code | Condition |
|---|---|---|
| 401 | `session_required` | Token is not session-backed. |
| 403 | `forbidden` | Impersonation session. |
| 500 | `internal_error` | Failed to list devices. |

- **Source:** `AuthHandler.ListUserDevices` — `internal/handler/auth.go`

## `DELETE /api/v1/user/devices`
Revokes all of the user's sessions except the current one.

- **Auth:** Bearer JWT (user), session-backed
- **Path parameters:** None.
- **Query parameters:** None.
- **Request body:** None.
- **Response `200`:**

```json
{ "ok": true }
```

- **Errors:**

| Status | `error` code | Condition |
|---|---|---|
| 401 | `session_required` | Token is not session-backed. |
| 403 | `forbidden` | Impersonation session. |
| 500 | `internal_error` | Failed to list devices. |

- **Source:** `AuthHandler.RevokeOtherUserDevices` — `internal/handler/auth.go`

## `DELETE /api/v1/user/devices/{id}`
Revokes a single one of the user's sessions by ID. Revoking the current session clears the refresh cookie.

- **Auth:** Bearer JWT (user), session-backed
- **Path parameters:**

| Name | Type | Description |
|---|---|---|
| `id` | string | Session UUID to revoke (must be a `user` session belonging to the calling ASN). |

- **Query parameters:** None.
- **Request body:** None.
- **Response `200`:**

```json
{ "ok": true }
```

- **Errors:**

| Status | `error` code | Condition |
|---|---|---|
| 401 | `session_required` | Token is not session-backed. |
| 403 | `forbidden` | Impersonation session. |
| 404 | `device_not_found` | Session not found, not a `user` session, or not owned by the caller's ASN. |
| 500 | `internal_error` | Failed to revoke device. |

- **Source:** `AuthHandler.RevokeUserDevice` — `internal/handler/auth.go`

---

# Session / device management (admin)

## `GET /api/v1/admin/auth/devices`
Lists the calling admin's active sessions.

- **Auth:** Bearer JWT (admin), session-backed
- **Path parameters:** None.
- **Query parameters:** None.
- **Request body:** None.
- **Response `200`:** A bare JSON array of `AuthSession` objects (see [Session response shape](#session-response-shape)).

```json
[
  {
    "id": "b7e2…",
    "subject_type": "admin",
    "admin_id": "11111111-2222-3333-4444-555555555555",
    "email": "admin@example.com",
    "device_name": "Mozilla/5.0",
    "login_method": "password",
    "created_at": "2026-06-07T12:00:00Z",
    "expires_at": "2026-06-21T12:00:00Z"
  }
]
```

- **Errors:**

| Status | `error` code | Condition |
|---|---|---|
| 401 | `session_required` | Token is not session-backed. |
| 500 | `internal_error` | Failed to list devices. |

- **Source:** `AuthHandler.ListAdminDevices` — `internal/handler/auth.go`

## `DELETE /api/v1/admin/auth/devices/{id}`
Revokes one of the calling admin's sessions (and its child sessions). Revoking the current session clears the refresh cookie.

- **Auth:** Bearer JWT (admin), session-backed
- **Path parameters:**

| Name | Type | Description |
|---|---|---|
| `id` | string | Session UUID to revoke (must be an `admin` session belonging to the calling admin). |

- **Query parameters:** None.
- **Request body:** None.
- **Response `200`:**

```json
{ "ok": true }
```

- **Errors:**

| Status | `error` code | Condition |
|---|---|---|
| 401 | `session_required` | Token is not session-backed. |
| 404 | `device_not_found` | Session not found, not an `admin` session, or not owned by the caller. |
| 500 | `internal_error` | Failed to revoke device. |

- **Source:** `AuthHandler.RevokeAdminDevice` — `internal/handler/auth.go`

---

# Passkey management (user)

Authenticated endpoints to register and manage WebAuthn credentials tied to the caller's ASN. The passwordless login flow lives above under [User login (passkey / WebAuthn)](#user-login-passkey--webauthn).

## `GET /api/v1/passkey/status`
Reports whether the caller's ASN has any registered passkeys.

- **Auth:** Bearer JWT (user)
- **Path parameters:** None.
- **Query parameters:** None.
- **Request body:** None.
- **Response `200`:**

| Field | Type | Description |
|---|---|---|
| `has_passkey` | boolean | `true` if the ASN has at least one passkey. |
| `count` | integer | Number of registered passkeys. |

```json
{ "has_passkey": true, "count": 2 }
```

- **Errors:**

| Status | `error` code | Condition |
|---|---|---|
| 401 | `unauthorized` | Not authenticated (ASN is 0). |
| 500 | `internal_error` | Failed to query passkey status. |

- **Source:** `PasskeyHandler.Status` — `internal/handler/auth_passkey.go`

## `POST /api/v1/passkey/register/begin`
Begins passkey registration: returns WebAuthn creation options. Sets `Cache-Control: no-store`.

- **Auth:** Bearer JWT (user)
- **Path parameters:** None.
- **Query parameters:** None.
- **Request body:** None.
- **Response `200`:** The WebAuthn `CredentialCreation` options object (passed to `navigator.credentials.create`). Shape is determined by the library; `rp.name` is `Auto Peer` and `rp.id` is the configured WebAuthn RP ID, e.g.:

```json
{
  "publicKey": {
    "challenge": "rZ8h...",
    "rp": { "name": "Auto Peer", "id": "your-center.example.com" },
    "user": { "id": "YXNuOjQyNDI0MjAwMDA", "name": "AS4242420000", "displayName": "AS4242420000" },
    "pubKeyCredParams": [ { "type": "public-key", "alg": -7 } ],
    "timeout": 60000
  }
}
```

- **Errors:**

| Status | `error` code | Condition |
|---|---|---|
| 401 | `unauthorized` | Not authenticated (ASN is 0). |
| 500 | `internal_error` | Failed to load credentials, begin registration, serialize, or save the session. |

- **Source:** `PasskeyHandler.RegisterBegin` — `internal/handler/auth_passkey.go`

## `POST /api/v1/passkey/register/finish`
Completes passkey registration and stores the new credential.

- **Auth:** Bearer JWT (user)
- **Path parameters:** None.
- **Query parameters:** None.
- **Request body:** The WebAuthn `AuthenticatorAttestationResponse` (the credential returned by `navigator.credentials.create`), parsed directly from the request body by the WebAuthn library.

```json
{
  "id": "Aa-Bb-Cc...",
  "rawId": "Aa-Bb-Cc...",
  "type": "public-key",
  "response": {
    "attestationObject": "...",
    "clientDataJSON": "..."
  }
}
```

- **Response `200`:**

| Field | Type | Description |
|---|---|---|
| `success` | boolean | Always `true` on success. |
| `id` | string | New credential's DB ID. |
| `name` | string | Credential name (always `Passkey` on creation). |

```json
{ "success": true, "id": "cred_01H…", "name": "Passkey" }
```

- **Errors:**

| Status | `error` code | Condition |
|---|---|---|
| 401 | `unauthorized` | Not authenticated (ASN is 0). |
| 400 | `session_not_found` | Registration session not found or expired. |
| 400 | `registration_failed` | WebAuthn registration verification failed (detail in message). |
| 500 | `internal_error` | Failed to deserialize the session, load credentials, or save the credential. |

- **Source:** `PasskeyHandler.RegisterFinish` — `internal/handler/auth_passkey.go`

## `GET /api/v1/user/passkeys`
Lists the caller's registered passkeys.

- **Auth:** Bearer JWT (user)
- **Path parameters:** None.
- **Query parameters:** None.
- **Request body:** None.
- **Response `200`:** An object with a `passkeys` array.

| Field | Type | Description |
|---|---|---|
| `passkeys` | array of object | Registered passkeys (empty array when none). |
| `passkeys[].id` | string | Credential DB ID. |
| `passkeys[].name` | string | Credential name. |
| `passkeys[].aaguid` | string | Authenticator AAGUID (hex). |
| `passkeys[].created_at` | string | `2006-01-02T15:04:05Z` timestamp. |
| `passkeys[].last_used_at` | string | Last-used timestamp (omitted when never used). |

```json
{
  "passkeys": [
    {
      "id": "cred_01H…",
      "name": "Passkey",
      "aaguid": "ea9b8d664d011d213ce4b6b48cb575d4",
      "created_at": "2026-06-07T12:00:00Z",
      "last_used_at": "2026-06-07T12:30:00Z"
    }
  ]
}
```

- **Errors:**

| Status | `error` code | Condition |
|---|---|---|
| 401 | `unauthorized` | Not authenticated (ASN is 0). |
| 500 | `internal_error` | Failed to list passkeys. |

- **Source:** `PasskeyHandler.List` — `internal/handler/auth_passkey.go`

## `DELETE /api/v1/user/passkeys/{id}`
Deletes one of the caller's passkeys.

- **Auth:** Bearer JWT (user)
- **Path parameters:**

| Name | Type | Description |
|---|---|---|
| `id` | string | Credential DB ID to delete (scoped to the caller's ASN). |

- **Query parameters:** None.
- **Request body:** None.
- **Response `200`:**

```json
{ "success": true }
```

- **Errors:**

| Status | `error` code | Condition |
|---|---|---|
| 401 | `unauthorized` | Not authenticated (ASN is 0). |
| 400 | `bad_request` | Missing passkey ID. |
| 500 | `internal_error` | Failed to delete passkey. |

- **Source:** `PasskeyHandler.Delete` — `internal/handler/auth_passkey.go`
</content>
</invoke>
