# Job: Prometheus metrics

Version: v0.2 · Epic: 07 Operations · Status: done · Depends on: 01-01, 03-04

## Goal

A `/metrics` endpoint in Prometheus text format with the series the contract lists, with
labels that can never carry content or high cardinality.

## Context

Contract §12: SHOULD exist, protected by network or a separate token; useful series: jobs
by terminal status, attempts per job, extractor and judge latency per endpoint, failovers,
quota remaining per application.

## Tasks

- [x] `github.com/prometheus/client_golang` with a custom registry (no Go runtime
      metrics by default; enable with a flag).
- [x] Series: `vde_jobs_total{app,status}`, `vde_job_attempts` histogram,
      `vde_job_duration_seconds{status}` histogram, `vde_provider_calls_total{role,
      endpoint,model,outcome}`, `vde_provider_latency_seconds{role,endpoint}` histogram,
      `vde_failovers_total{role,from,to}`, `vde_quota_remaining{app}` gauge,
      `vde_jobs_in_progress{app}` gauge, `vde_http_requests_total{route,status}`,
      `vde_swept_jobs_total`.
- [x] Labels only from configuration ids (app, endpoint, model, route pattern), never
      from request data.
- [x] Protection: `metrics.token_env` (a separate bearer token) or `metrics.listen` (a
      second loopback or internal listener); when neither is set, `/metrics` is served on
      the main listener only to loopback. Building blocks are done in `internal/metrics`
      (`RequireToken(h, token)`, `LoopbackOnly(h)`); reading the config keys and mounting
      the route is open: wired by epic 05/07 agent. Contract below.
- [x] Test: scrape after a scenario run and assert the expected series exist with the
      expected label values (`internal/metrics/metrics_test.go` scrapes the handler after
      recording observations; a scrape after a full scenario run is part of the wiring).

## Wiring contract (for the epic 05/07 agent)

API, package `internal/metrics`: `New(opts ...Option) *Metrics` (`WithGoRuntime()` adds
the Go runtime and process collectors, off by default), `(*Metrics).Handler()`, and
`ObserveHTTP(route, status)`, `ObserveProviderCall(role, endpoint, model, outcome, dur)`,
`ObserveFailover(role, from, to)`, `SetQuotaRemaining(app, n)`, `SetJobsInProgress(app, n)`
and `AddSwept(n)`. `*Metrics` implements `worker.Observer`: pass it as
`worker.Options.Observer`. `JobStarted` records nothing; set `vde_jobs_in_progress` from
the job store's queued plus running count per application (on submit, on finish and on
startup), because abandoned jobs never reach `JobFinished`.

Label vocabularies: `status` is a job status; `outcome` is `ok`, `retryable` or
`permanent`; `role` is `extractor` or `judge`; `route` is the matched mux pattern
(`r.Pattern`, an empty one is exported as `none`); `endpoint`, `model`, `app`, `from` and
`to` are configuration ids. Never pass request data as a label.

Protection, config keys to add by the config owner:

| Key | Behaviour |
| --- | --- |
| `metrics.token_env` | Name of an environment variable holding a bearer token. When set, `/metrics` on the main listener is wrapped in `metrics.RequireToken`. |
| `metrics.listen` | A second listener (`host:port`) serving only `/metrics`; it should be loopback or an internal interface. Config check warns when it is not loopback and no token is set. |
| neither set | `/metrics` is mounted on the main listener wrapped in `metrics.LoopbackOnly`, which checks the peer address and never a forwarded header. |

With both set, the second listener also requires the token. The metrics route is not
listed in the OpenAPI document and its requests are counted under the route `GET /metrics`.

## Acceptance criteria

- Metrics appear in a local Prometheus with the compose example (epic 08).

## Out of scope

OpenTelemetry, dashboards (an example Grafana JSON may be added later).

## Notes and deviations

- Wiring (epic 05/07 agent): config keys `metrics.token_env`, `metrics.listen`,
  `metrics.go_runtime` (`internal/config/service.go`, validated, documented). Main
  listener route in `internal/api/metrics.go`: token → `RequireToken`; `listen` set →
  `404` here and a second `http.Server` in `vde serve` serving only `GET /metrics`
  (with the token when set, its requests counted under `GET /metrics`); neither →
  `LoopbackOnly`, plus a refusal of requests carrying forwarding headers so a reverse
  proxy on the same host does not publish the metrics.
- `ObserveHTTP(r.Pattern, status)` from the access log; provider calls observed by a
  wrapper around each endpoint's provider in `vde serve` (`meteredFactory`); the pool
  gets `*Metrics` as its Observer and the sweeper as its Observer.
- [x] Failovers: `router.Group.OnFailover(req, from, to)` is called each time a call moves
  on to another pair; `vde serve` wires it to `ObserveFailover(role, from endpoint, to
  endpoint)`, the role from the request's framing as for provider calls (epic 11 agent;
  test `internal/router` `TestGroup_OnFailover`).
- `vde_jobs_in_progress` is queued plus running (`job.Store.CountInProgress`): set at
  startup for every enabled application, after each submission and admin force delete
  (`internal/api`), and after each finished job (`progressObserver` around the metrics in
  `vde serve`).
- Tests: `internal/api/metrics_test.go` (protection modes, route labels are patterns
  never paths) and the scrape after a full scenario run in
  `cmd/vde/serve_e2e_test.go`.
