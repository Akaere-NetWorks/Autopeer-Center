# AutoPeer Frontend — AI Design & Build Prompt

The AutoPeer **frontend is not open-source**. This document is a ready-to-use prompt
that lets you build your own frontend with an AI coding assistant (Claude, etc.), with
zero design work on your part. The backend (`autopeer-center`) already exists and is
fully documented under [`./api/`](./api/README.md); the AI builds a web UI on top of it.

## How to use

1. Make sure the AI can read this repository's `docs/` (so it sees the API reference in
   [`./api/`](./api/README.md), [`./authentication.md`](./authentication.md), and
   [`./websocket-protocol.md`](./websocket-protocol.md)). Either run the assistant inside
   a checkout of this repo, or paste those files alongside this prompt.
2. Copy **everything below the `═══ PROMPT ═══` line** and send it to the assistant.
3. The assistant will **first interview you** (hosting provider, backend URL, branding,
   which features to include), **then propose a stack + architecture for your approval**,
   and **only then implement**. Answer its questions and review its plan — that's it.

> You do not choose the framework or write any spec yourself. The prompt makes the AI ask
> what it needs (starting with where you want to deploy), recommend the best fit, and build it.

---

═══ PROMPT ═══

## Your role

You are a senior full-stack frontend engineer. Build a **production-quality, server-side-rendered (SSR) web frontend** for **AutoPeer** — an automated [DN42](https://dn42.dev) peering control plane. The backend already exists and is **fully documented** (see "The backend you are building against" below). **Do not modify or re-implement the backend.** Your job is a maintainable SSR app that talks to the existing HTTP API, with the API **proxied through the frontend's own server** so the browser only ever calls same-origin (no CORS, no exposed backend URL).

Hard requirements:
- **SSR** (server-rendered first paint + hydration), not a pure SPA.
- **Easy to maintain**: typed API client, one central fetch wrapper, clear folder structure, tests, CI.
- **Easy to proxy the backend**: a server route forwards `/api/**` to the backend; the backend base URL stays server-side only.
- Match the backend's request/response shapes and validation **exactly** (read the API docs — do not guess fields).

## Operating procedure — FOLLOW IN ORDER. Do not skip ahead to code.

### Phase 1 — Interview the user, then STOP and wait for answers

Ask the user these questions (group them, keep it short) and **wait** for the replies before doing anything else:

1. **Hosting / provider** (this drives the whole stack): Cloudflare (Workers/Pages), Vercel, Netlify, Deno Deploy, Fly.io, a self-managed **Node.js VPS / Docker**, or other?
2. **Backend**: the base URL of the running `autopeer-center` (e.g. `https://your-center.example.com`). Will the frontend be served on the **same origin** as the API (reverse-proxied together) or a **separate origin**?
3. **Scope**: user-facing app only, or also the **admin console**? Include the MCP-key management UI? Include Telegram-binding UI?
4. **Auth methods to surface** (the backend supports several — see auth docs): email verification-code, GPG, passkey/WebAuthn, admin email+password, OAuth-style device flow. Which should the login UI offer?
5. **Branding**: app name, logo/wordmark, primary color, light/dark default.
6. **Preferences**: package manager (npm/pnpm/bun), TypeScript (default yes), i18n needed?, error reporting (Sentry?), analytics?, any framework preference that would override your recommendation.

Do **not** continue until the user answers.

### Phase 2 — Propose the stack & architecture, then get approval

Using the chosen provider, recommend an SSR stack **with trade-offs**, then present the architecture and the page map for sign-off.

**Default recommendation (state this, but adapt to the provider/preference): Nuxt 4 (Vue 3) + Nitro.** Rationale: one codebase deploys to *every* major provider via Nitro presets, and Nitro **server routes make the API proxy and SSR data-fetching trivial** — directly satisfying "SSR + easy proxy + maintainable". Note credible alternatives and when to choose them:
- **Next.js (App Router)** — if the team is React-first; use route handlers + `rewrites` for the proxy.
- **SvelteKit** — lightest runtime; `+server.ts` endpoints for the proxy; great on Cloudflare/Vercel.
- **Remix / React Router** — if you want web-standard `loader`/`action` data flow.

Provide a **provider → deployment preset** table for the chosen framework, e.g. for Nuxt/Nitro:

| Provider | Nitro preset | Notes |
|---|---|---|
| Cloudflare Workers | `cloudflare_module` | `wrangler.toml`; secrets via `wrangler secret put`; proxy via a server route |
| Vercel | `vercel` | env in dashboard; `vercel.json` rewrites optional (server route preferred) |
| Netlify | `netlify` | env in UI; server route handles the proxy |
| Node VPS / Docker | `node-server` | run behind nginx/Caddy; Dockerfile + reverse proxy |
| Deno Deploy | `deno-deploy` | — |

Then present, for approval:
- **Folder structure** and rendering strategy (which pages SSR vs client-only).
- The **API proxy** design (one server route catching `/api/**` → `${API_BASE}/api/**`, forwarding method/body/headers and the auth cookie, streaming SSE where needed; `API_BASE` server-only).
- **Auth/session model** (below).
- The **route/page map** (below) trimmed to the user's chosen scope.

Adjust based on feedback. Only proceed to Phase 3 once approved.

### Phase 3 — Implement

Scaffold the project, implement the spec, and finish with provider-specific run/build/deploy instructions and a smoke-test checklist. Build incrementally with runnable milestones (auth → user dashboard → peers → account → admin), not one giant drop.

---

## The backend you are building against

Read the real contract in this repo before coding:
- **API reference (every endpoint, request + response):** [`./api/README.md`](./api/README.md) and the per-resource files: [`auth`](./api/auth.md), [`peers`](./api/peers.md), [`nodes`](./api/nodes.md), [`account`](./api/account.md), [`admin`](./api/admin.md), [`admin-ops`](./api/admin-ops.md), [`mcp`](./api/mcp.md), [`public`](./api/public.md).
- **Auth concepts:** [`./authentication.md`](./authentication.md). **Versioning:** [`./api/versioning.md`](./api/versioning.md). **WebSocket protocol:** [`./websocket-protocol.md`](./websocket-protocol.md) (agents/bots only — **the browser must never connect to these**).

Conventions you must honor:
- **All endpoints under `/api/v1`, JSON**, `Content-Type: application/json`, request bodies ≤ 1 MiB.
- **Auth schemes:** user/admin **Bearer JWT access token** (`Authorization: Bearer <token>`); a **refresh token** the backend sets as a cookie (and accepts on `POST /api/v1/auth/refresh`). MCP keys exist but are **not** for the web UI (only the MCP-key *management* screens, which create/list/delete keys, belong in the UI).
- **API versioning (Stripe-style):** send a pinned `Autopeer-Version: YYYY-MM-DD` header on every request (see versioning doc for the current latest). The resolved version echoes back in the response header; an unknown version returns `400 invalid_api_version`. Pin one version in a constant so server responses are stable.
- **Standard error body:** `{ "error": "<code>", "message": "<human text>", "request_id": "<id>" }`. Surface `message` to users and keep `request_id` visible (copyable) for support. Map common statuses (401 → refresh/login, 403 → access denied, 409 → conflict toast, 422/400 → inline form errors, 429 → back off, 5xx → retry/report).
- **Response headers:** every response carries `X-Request-ID`; `/api/v1` responses carry `Autopeer-Version`.

Endpoint groups (full details in the docs above):
- **Public:** `GET /nodes`, `GET /registry/asn/{asn}`, `GET /stats`, `GET /user/peers/creation-status`, health.
- **Auth:** email code (`/auth/user/request-code` → `/auth/user/verify-code`), GPG, passkey login/registration, admin login, device flow, `/auth/refresh`, `/auth/logout`, Turnstile config, login-status. Admin impersonation (`/admin/auth/login-as`).
- **User peers:** list/create/summary/get/metrics/update/delete under `/user/peers`. Looking glass `POST /user/looking-glass/run`.
- **Account:** email + notification preferences, Telegram binding + bind-token + Telegram notification prefs, passkeys management, sessions/devices, own audit log.
- **Admin:** peers (approve/reject/suspend/unsuspend/delete/update/contact-email), nodes (CRUD, import, bird-refresh, agent update/rollback, regenerate-token, reset-pubkey), releases (list/upload/delete), settings, notifications, bot management, audit, stats, system-status, queue monitor (some routes only when `ASYNQ_READONLY_MONITOR=false`).
- **MCP key management:** user keys + admin keys (create/list/delete).

## Application plan — build all of this (trim only to the user's chosen scope)

### Auth & session (foundation — build first)
- **Login page** offering the enabled methods:
  - **Email code:** ASN input → `request-code` (render the **Turnstile** widget if `GET /auth/turnstile-config` says enabled, send the token) → code entry → `verify-code`.
  - **GPG:** request challenge → user signs → submit signature.
  - **Passkey/WebAuthn:** `passkey/begin` → `navigator.credentials.get` → `passkey/finish`.
  - **Admin password:** email + password → `/auth/admin/login`.
  - **Device flow:** show user code + verification URL; poll `device/token`; plus the in-app approval screen (`device/request` / `device/authorize`).
- **Token handling:** keep the **access token in memory / server session**, rely on the **httpOnly refresh cookie** for persistence; on a `401` from any call, attempt `POST /auth/refresh` once and retry, else redirect to login. `POST /auth/logout` clears it. Because the API is proxied same-origin, forward the cookie through the proxy.
- **Route guards:** `/` and `/app/**` require a user (or admin) JWT; `/admin/**` requires an **admin** JWT. Redirect unauthenticated users to login with a return URL. Show an **impersonation banner** + "stop impersonating" when an admin is logged-in-as a user.
- **Sessions/devices page:** list active sessions (`/user/devices`, admin `/admin/auth/devices`), revoke one or all others.

### User area
- **Dashboard:** peer summary (`/user/peers/summary`), public stats (`/stats`), quick actions, recent activity from the user audit log.
- **Peers:**
  - **List** (`GET /user/peers`) with status badges, search/sort/filter; respects the `Autopeer-Version` shape.
  - **Create** form (`POST /user/peers`): node picker (`GET /nodes`), and client-side validation **mirroring the backend** — `remote_pubkey` Base64 `^[A-Za-z0-9+/]+=*$` (≥40 chars), `remote_endpoint` `IP:Port`/`[IPv6]:Port`, `remote_lla` link-local `fe80::/10`, optional `mtu` 576–9000, optional `enable_psk`. Honor the **creation-status gate** (`/user/peers/creation-status`) — disable the form with a message when closed.
  - **Detail** (`GET /user/peers/{id}`): config, status, BGP/WireGuard state, **metrics chart** (`/user/peers/{id}/metrics` — use Chart.js or similar), edit (`PUT`), delete (`DELETE`).
- **Looking glass:** form → `POST /user/looking-glass/run`, render results; respect rate limiting.
- **Account:** email/notification preferences (get/put), Telegram binding (show status; create bind-token / show QR + deep link; unbind; Telegram notification prefs), passkeys (list/register/delete), audit log (`/user/audit`).

### Admin console (only if in scope; behind admin guard)
- **Peers admin:** queue of pending requests; approve/reject (with reason)/suspend/unsuspend/delete/edit/override-contact-email; per-peer metrics.
- **Nodes:** CRUD; import existing peers; bird-refresh; trigger agent **update/rollback**; regenerate agent token (show once); reset stored pubkey. **Releases:** list, **upload** (multipart) a new agent binary, delete.
- **Settings & notifications:** site settings editor, notification settings, send **test email**.
- **Bot management:** settings, token reset, stats, recent commands, blocked-users list (block/unblock), export.
- **Audit & stats:** global audit log with filters; admin stats dashboard (`/admin/stats`).
- **System status:** health snapshot, DB table sizes, rotate-tables action.
- **Queue monitor:** overview, queues, tasks, scheduler, servers, history; show mutating controls (run/archive/delete/pause/resume/cancel) **only when the backend exposes them** (they 404 when `ASYNQ_READONLY_MONITOR=true`) — detect and hide/disable gracefully.
- **MCP keys:** manage user + admin MCP keys (create → show secret once, list, delete).

### Cross-cutting (do these well — this is the "maintainable" requirement)
- **Typed API client:** a single module of typed functions per endpoint group; central `apiFetch` wrapper that (a) prefixes `/api`, (b) injects `Authorization` + `Autopeer-Version`, (c) does the refresh-on-401 retry, (d) parses the `{error,message,request_id}` shape into a typed error, (e) surfaces toasts. Derive types from the API docs (or generate from an OpenAPI spec if you produce one).
- **The proxy server route:** catch-all that forwards `/api/**` to `${API_BASE}/api/**` preserving method, query, body, relevant headers, and the auth/refresh cookies; stream SSE responses unbuffered (the MCP/stream endpoints). Keep `API_BASE` strictly server-side. Document why this removes CORS and hides the backend URL.
- **SSR specifics:** server-fetch the data needed for first paint (dashboard, lists, detail) using the framework's server data layer; never leak admin-only data into a user page's payload; ensure no hydration mismatches; set sensible cache headers (mostly `no-store` for authed data).
- **UX:** responsive layout, light/dark, WCAG-AA accessibility, consistent loading/empty/error states, inline form validation, pagination/sort/filter on lists, confirmation dialogs for destructive admin actions, copyable `request_id` in error toasts.
- **Config:** all via env — `API_BASE` (server-only), public app config (name/colors), Turnstile site key, Sentry DSN, `COMMIT_HASH`/version for the footer. Provide `.env.example`.
- **Quality:** TypeScript strict; ESLint/Prettier; **Vitest** unit tests for the API client + guards + validators; a few **Playwright** e2e (login, create peer, admin approve); a CI workflow (typecheck + test + build).

### Deployment (Phase 3 output, specific to the chosen provider)
- **Cloudflare:** Nitro `cloudflare_module` (or the framework's CF adapter); `wrangler.toml`; `wrangler secret put API_BASE` etc.; proxy via the server route (Workers `fetch`); set `COMMIT_HASH` at build.
- **Vercel / Netlify:** the matching preset/adapter; env vars in the dashboard; prefer the server-route proxy over platform rewrites so cookies/streaming work.
- **Node VPS / Docker:** `node-server` build; Dockerfile; run behind **nginx/Caddy** (TLS, gzip/br, `/api` and the app on one origin); env via the orchestrator; a process manager (systemd/PM2) or container restart policy.
- Always provide: exact `install` / `dev` / `build` / `start` / `deploy` commands, and a **smoke-test checklist**: load home, log in (each enabled method), create a peer, (admin) approve it, hit an SSE/looking-glass call, log out.

## Working rules
- Phase order is mandatory: interview → propose → (approval) → implement.
- **Never** embed admin tokens or `API_BASE` in client bundles; **never** open the agent/bot WebSocket endpoints from the browser.
- Mirror backend validation to avoid 4xx round-trips; pin one `Autopeer-Version`.
- Write a frontend `README` with setup, env, and deploy steps for the chosen provider.
- Prefer clarity and conventional framework patterns over cleverness — this app will be maintained by others.

═══ END PROMPT ═══
