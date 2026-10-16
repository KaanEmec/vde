# Job: Bearer authentication middleware and scopes

Version: v0.2 · Epic: 02 Applications and tokens · Status: done · Depends on: 02, 01-01

## Goal

Every `/v1` request is authenticated and scoped before any body is read beyond headers,
anything is stored, quota is charged or a provider is called.

## Context

Contract §3: missing, malformed, expired or revoked token → 401 before any provider call,
storage or quota accounting; valid token outside scope → 403; foreign job → 404. Contract
§14 item 2: provider usage counter does not move.

## Tasks

- [x] Middleware: parse `Authorization: Bearer`, look up by token id, verify hash, check
      `revoked_at`, `expires_at`, application enabled; attach `application` and `scopes`
      to the request context. Failure → 401 `unauthorized` with a generic message.
- [x] Per-route scope check → 403 `forbidden`.
- [x] A tiny in-memory cache of token rows (TTL 5 s) is allowed for poll traffic but
      revocation must be effective at once: revocation invalidates through the store
      (the CLI runs in another process, so cache TTL ≤ 5 s is the bound; document it, or
      skip the cache if SQLite reads prove cheap enough, which they should).
- [x] Order in the handler chain: auth → rate limit → body read. The body is not read
      for unauthenticated requests; the server closes the connection after answering 401
      when a large body is pending.
- [x] Tests: no header, malformed header, unknown id, wrong secret, expired, revoked,
      disabled application, wrong scope; a counter on the fake provider asserts zero calls
      in all failure cases.

## Acceptance criteria

- Contract §14 item 2 passes as an automated test.

## Out of scope

mTLS, OAuth, per-user identity.

## Outcome

Done in `internal/api/auth.go`. `server.authenticate(need, next)` replaces the
pass-through: it reads the single `Authorization: Bearer` header (scheme
case-insensitive), calls `auth.Authenticate` (token id lookup, constant-time hash check,
then revoked, expired, application enabled; a wrong secret is checked first so it reveals
nothing), and answers every refusal with the same 401 `unauthorized` body plus
`WWW-Authenticate: Bearer`, auditing `auth.failed` with only `{"reason": …}` and the
token's application when known. Scopes per route: `POST /v1/jobs` and `DELETE` need
`jobs`; `GET /v1/jobs/{id}` and `/v1/quota` need `jobs:read` (which `jobs` implies);
`/v1/openapi.yaml` takes any valid token; else 403 `forbidden`. The principal is in the
context: `api.ApplicationFrom(ctx)` and `api.ScopesFrom(ctx)`. The access log gains
`app_id`. A store failure answers 500 `internal`; a nil `Deps.Store` refuses `/v1`.
Deviations:

- No token cache: one indexed SQLite read per request, and a CLI revocation is effective
  on the next request with no TTL bound to document.
- A refusal (401, 403, 500) of a request that announced a body sets `Connection: close`,
  whatever the body's size, so the server never drains an unauthenticated upload.
- Every refusal writes an audit row; epic 05's rate limit runs after auth, so a flood of
  bad tokens writes rows (bounded by the server's connection handling only).
- Contract §14 item 2: `TestAuthenticateRefusals` (every failure case, body unread, handler
  counter zero) and `cmd/vde` `TestAuthEndToEnd` (fake provider `Calls()` empty, no job
  stored). The foreign-job 404 needs the job handlers (epic 04).
