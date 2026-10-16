# Job: Prometheus metrics

Version: v0.2 · Epic: 07 Operations · Status: planned · Depends on: 01-01, 03-04

## Goal

A `/metrics` endpoint in Prometheus text format with the series the contract lists, with
labels that can never carry content or high cardinality.

## Context

Contract §12: SHOULD exist, protected by network or a separate token; useful series: jobs
by terminal status, attempts per job, extractor and judge latency per endpoint, failovers,
quota remaining per application.

## Tasks

- [ ] `github.com/prometheus/client_golang` with a custom registry (no Go runtime
      metrics by default; enable with a flag).
- [ ] Series: `vde_jobs_total{app,status}`, `vde_job_attempts` histogram,
      `vde_job_duration_seconds{status}` histogram, `vde_provider_calls_total{role,
      endpoint,model,outcome}`, `vde_provider_latency_seconds{role,endpoint}` histogram,
      `vde_failovers_total{role,from,to}`, `vde_quota_remaining{app}` gauge,
      `vde_jobs_in_progress{app}` gauge, `vde_http_requests_total{route,status}`,
      `vde_swept_jobs_total`.
- [ ] Labels only from configuration ids (app, endpoint, model, route pattern), never
      from request data.
- [ ] Protection: `metrics.token_env` (a separate bearer token) or `metrics.listen` (a
      second loopback or internal listener); when neither is set, `/metrics` is served on
      the main listener only to loopback.
- [ ] Test: scrape after a scenario run and assert the expected series exist with the
      expected label values.

## Acceptance criteria

- Metrics appear in a local Prometheus with the compose example (epic 08).

## Out of scope

OpenTelemetry, dashboards (an example Grafana JSON may be added later).
