# Job: Server, middleware chain and `serve` command

Version: v0.2 · Epic: 01 HTTP API foundation · Status: planned · Depends on: v0.1

## Goal

`vde serve` starts an `http.Server` with the `/v1` mux, `/healthz` and `/metrics`, a
small middleware chain, TLS or trusted-proxy HTTPS enforcement, timeouts and graceful
shutdown.

## Context

ADR 0001. Contract §2 (HTTPS outside loopback, `X-Request-Id` echoed or generated),
§12 (`serve` subcommand). Architecture overview, security model.

## Tasks

- [ ] `internal/api.New(deps) http.Handler` builds the mux with the Go 1.22 pattern
      syntax (`POST /v1/jobs`, `GET /v1/jobs/{id}`, `DELETE /v1/jobs/{id}`,
      `GET /v1/quota`, `GET /healthz`, `GET /metrics`).
- [ ] Middleware, in order: panic recovery (500 `internal`, no stack in body);
      request id (echo `X-Request-Id` ≤ 128 chars, else ULID); HTTPS enforcement
      (TLS listener, or `trusted_proxy: true` requiring `X-Forwarded-Proto: https`,
      loopback exempt); access log (method, path pattern, status, duration, app id,
      request id, never query or body); then per-route auth and quota (epic 02, 05).
- [ ] `server.tls` with cert and key files → `ListenAndServeTLS` with modern defaults;
      otherwise plain HTTP, refusing to bind non-loopback unless `trusted_proxy: true`.
- [ ] Timeouts: `ReadHeaderTimeout` 10 s, per-route body read bound by size (epic 04) and
      a request timeout of 60 s for uploads; `MaxHeaderBytes` 64 KiB.
- [ ] Graceful shutdown on SIGTERM and SIGINT: stop accepting, drain in-flight requests
      (30 s), then stop the worker (epic 03).
- [ ] `X-Service-Version` header on every `/v1/jobs` response (value from v0.1 job 05-07).
- [ ] Tests with `httptest`: request id echo and generation, HTTPS refusal, panic → 500
      JSON, unknown route → 404 JSON error body.

## Acceptance criteria

- `vde serve` with the dev configuration listens on loopback over HTTP; with
  `trusted_proxy: false` and a non-loopback listen it refuses to start without TLS.

## Out of scope

HTTP/3, automatic certificates (use a reverse proxy), CORS (no browser callers in v0.2;
revisit if the backlog item on per-user tokens is promoted).
