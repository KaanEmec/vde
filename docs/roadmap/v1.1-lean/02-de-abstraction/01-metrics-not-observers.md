# Job: Metrics instead of observer interfaces

Version: v1.1 · Epic: 02 De-abstraction · Status: done (Polish and Gate left to the orchestrator) · Depends on: 01-01

## Goal

The worker and the sweeper take a nil-safe `*metrics.Metrics` directly; the observer
interfaces, their no-op types and the metrics wrappers in `cmd/vde/serve.go` are gone.
Structure only; every metric series keeps its name, labels and values.

## Context

- `internal/worker/pool.go:34-57` `Observer`/`NopObserver` and
  `internal/sweeper/sweeper.go:43-53` `Observer`/`nopObserver`: the only real
  implementation is `*metrics.Metrics` (plus a wrapper). `JobStarted`
  (`metrics.go:116-119`) is a no-op that exists only to satisfy `worker.Observer`.
- `internal/metrics/metrics.go:39-48`: functional options (`Option`, `WithGoRuntime`) with
  one option and one caller (`serve.go:176-180`): use `New(goRuntime bool)`.
- `cmd/vde/serve.go:460-473` `progressObserver` refreshes the in-progress gauge; the
  `OnFinish` hook at `serve.go:282` can do it.
- `cmd/vde/serve.go:446-458, 475-483` `metricsHandler` with its own `statusRecorder`
  duplicates `api.statusWriter` and the access-log metric: move into `internal/api`.
- `cmd/vde/serve.go:522-557` `extractorGroups`, `judgeGroups`, `allHealthy`: re-split
  `extract.Roles`; belongs on `extract.Roles`. `api.HealthChecker` (`api.go:20-24`) is an
  interface for one function: a `func() bool` field.
- `internal/audit` `Recorder` is already nil-safe: copy that pattern.
- About −110 lines.

## Tasks

- [x] Confirm the inventory and that no second production implementation exists.
  - Skill: GOIFACE, EVID.
  - Prompt: "/golang-structs-interfaces Review worker.Observer, sweeper.Observer,
    api.HealthChecker and metrics.Option against their implementations (use `gopls
    implementation`). For each: production implementations, test fakes, and whether a
    nil-safe concrete type or a func field replaces it. Report only. Guardrails:
    skills.md."
- [x] Plan and execute.
  - Skill: GOREF.
  - Prompt: "/golang-refactoring Plan mode: make *metrics.Metrics methods nil-safe; pass
    it to worker and sweeper instead of their Observer interfaces; delete NopObserver,
    nopObserver, JobStarted, progressObserver (refresh the gauge in OnFinish), the
    functional options (New(goRuntime bool)); move metricsHandler into internal/api
    reusing statusWriter; replace api.HealthChecker by a func() bool and move the group
    split onto extract.Roles. Metric names, labels and values must not change; compare
    `curl /metrics` series names before and after in a test. Guardrails: skills.md." Then
    "/golang-refactoring Execute mode: step <n>."
- [ ] Polish (orchestrator).
  - Skill: SIMPLIFIER.
  - Prompt: "Use the code-simplifier agent on internal/metrics, internal/worker,
    internal/sweeper and the metrics wiring in cmd/vde/serve.go (whole files, not only
    recent edits) to remove leftovers of the observer layer. Go project; follow AGENTS.md;
    ignore the JS/React standards. Guardrails: skills.md."
- [ ] Gate (orchestrator).
  - Skill: REVIEW, PRTK.
  - Prompt: `/code-review high <first>^..<last>`, then
    `/pr-review-toolkit:review-pr errors`.

## Acceptance criteria

- No `Observer` interface in `internal/worker` or `internal/sweeper`; the set of series
  served on `/metrics` is unchanged (a test lists them); `make check` and
  `make conformance` green.

## Out of scope

- Replacing the Prometheus client library (07-02).

## Result

Inventory: `worker.Observer` and `sweeper.Observer` had one production implementation
(`*metrics.Metrics`, plus `progressObserver`) and one test recorder each;
`api.HealthChecker` had `allHealthy` and two test fakes; `metrics.Option` one option and
one caller. All four are gone: `*metrics.Metrics` is nil-safe and passed as
`Options.Metrics`; the in-progress gauge refresh is in `OnFinish`; `metrics.New(goRuntime
bool)`; `api.NewMetrics` serves `metrics.listen` with `statusWriter`;
`extract.Roles.ExtractorHealthy`/`JudgeHealthy` feed `Deps.Extractor`/`Judge`
(`func() bool`). `TestServeEndToEnd` now pins the series names and label names of
`/metrics` (added before the change, unchanged after). `make check`, `make conformance`
green.

`git diff --stat` (Go): production 9 files, +133 −182 (net −49); tests 10 files, +227
−162 (net +65: the series-name check, a `NewMetrics` test, a nil-safety test). The
estimate of −110 was missed because the role split moved to `extract` instead of being
deleted, and each recording method gained a nil guard.
