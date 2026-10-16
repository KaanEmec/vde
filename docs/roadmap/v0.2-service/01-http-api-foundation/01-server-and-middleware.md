# Job: Server, middleware chain and `serve` command

Version: v0.2 · Epic: 01 HTTP API foundation · Status: done · Depends on: v0.1

## Goal

`vde serve` starts an `http.Server` with the `/v1` mux, `/healthz` and `/metrics`, a
small middleware chain, TLS or trusted-proxy HTTPS enforcement, timeouts and graceful
shutdown.

## Context

ADR 0001. Contract §2 (HTTPS outside loopback, `X-Request-Id` echoed or generated),
§12 (`serve` subcommand). Architecture overview, security model.

## Tasks

- [x] `internal/api.New(deps) http.Handler` builds the mux with the Go 1.22 pattern
      syntax (`POST /v1/jobs`, `GET /v1/jobs/{id}`, `DELETE /v1/jobs/{id}`,
      `GET /v1/quota`, `GET /healthz`, `GET /metrics`).
- [x] Middleware, in order: panic recovery (500 `internal`, no stack in body);
      request id (echo `X-Request-Id` ≤ 128 chars, else ULID); HTTPS enforcement
      (TLS listener, or `trusted_proxy: true` requiring `X-Forwarded-Proto: https`,
      loopback exempt); access log (method, path pattern, status, duration, app id,
      request id, never query or body); then per-route auth and quota (epic 02, 05).
- [x] `server.tls` with cert and key files → `ListenAndServeTLS` with modern defaults;
      otherwise plain HTTP, refusing to bind non-loopback unless `trusted_proxy: true`.
- [x] Timeouts: `ReadHeaderTimeout` 10 s, per-route body read bound by size (epic 04) and
      a request timeout of 60 s for uploads; `MaxHeaderBytes` 64 KiB.
- [x] Graceful shutdown on SIGTERM and SIGINT: stop accepting, drain in-flight requests
      (30 s), then stop the worker (epic 03).
- [x] `X-Service-Version` header on every `/v1/jobs` response (value from v0.1 job 05-07).
- [x] Tests with `httptest`: request id echo and generation, HTTPS refusal, panic → 500
      JSON, unknown route → 404 JSON error body.

## Acceptance criteria

- `vde serve` with the dev configuration listens on loopback over HTTP; with
  `trusted_proxy: false` and a non-loopback listen it refuses to start without TLS.

## Out of scope

HTTP/3, automatic certificates (use a reverse proxy), CORS (no browser callers in v0.2;
revisit if the backlog item on per-user tokens is promoted).

## Notes and deviations

- `internal/api.New(api.Deps)`; `Deps` holds the config, service version, logger and the
  two role groups (`HealthChecker`) only. The store, token, quota and worker fields come
  with epics 02 to 05. `POST /v1/jobs`, `GET` and `DELETE /v1/jobs/{id}`, `GET /v1/quota`
  and `GET /metrics` answer `503 unavailable` until their epics replace
  `server.notImplemented` in `routes()`.
- Per-route hooks: `server.authenticate` (epic 02) and `server.limit` (epic 05) wrap every
  `/v1` route and are pass-through today. The access log has no `app_id` until epic 02.
- HTTPS refusal answers `400 bad_request` ("HTTPS is required."). With
  `trusted_proxy: true` there is no loopback exemption: a local proxy connects from
  loopback, so only `X-Forwarded-Proto: https` (or TLS) passes.
- Timeouts: `ReadTimeout` 60 s covers the upload, `WriteTimeout` 90 s, `IdleTimeout`
  120 s; TLS minimum 1.2 with Go's default suites.
- `/healthz` checks the role groups only (`role_extractor_down`, `role_judge_down`);
  epic 07 adds database and blob checks. Its 503 body also carries the §9 error object.
- Worker hooks are comments in `cmd/vde/serve.go` (start before serving, stop after the
  drain), for epic 03.
- Acceptance: the dev configuration (`examples/vde.dev.yaml`) has no `server` section, so
  it listens on the default `:8090`, which is not loopback, and `vde serve` refuses it.
  Either the default listen or the dev file needs `127.0.0.1:8090` (outside this job's
  files; follow-up).
