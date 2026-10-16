# Job: Bearer authentication middleware and scopes

Version: v0.2 · Epic: 02 Applications and tokens · Status: planned · Depends on: 02, 01-01

## Goal

Every `/v1` request is authenticated and scoped before any body is read beyond headers,
anything is stored, quota is charged or a provider is called.

## Context

Contract §3: missing, malformed, expired or revoked token → 401 before any provider call,
storage or quota accounting; valid token outside scope → 403; foreign job → 404. Contract
§14 item 2: provider usage counter does not move.

## Tasks

- [ ] Middleware: parse `Authorization: Bearer`, look up by token id, verify hash, check
      `revoked_at`, `expires_at`, application enabled; attach `application` and `scopes`
      to the request context. Failure → 401 `unauthorized` with a generic message.
- [ ] Per-route scope check → 403 `forbidden`.
- [ ] A tiny in-memory cache of token rows (TTL 5 s) is allowed for poll traffic but
      revocation must be effective at once: revocation invalidates through the store
      (the CLI runs in another process, so cache TTL ≤ 5 s is the bound; document it, or
      skip the cache if SQLite reads prove cheap enough, which they should).
- [ ] Order in the handler chain: auth → rate limit → body read. The body is not read
      for unauthenticated requests; the server closes the connection after answering 401
      when a large body is pending.
- [ ] Tests: no header, malformed header, unknown id, wrong secret, expired, revoked,
      disabled application, wrong scope; a counter on the fake provider asserts zero calls
      in all failure cases.

## Acceptance criteria

- Contract §14 item 2 passes as an automated test.

## Out of scope

mTLS, OAuth, per-user identity.
