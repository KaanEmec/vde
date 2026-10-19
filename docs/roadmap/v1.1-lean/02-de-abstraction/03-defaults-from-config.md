# Job: Defaults and required dependencies from the configuration

Version: v1.1 · Epic: 02 De-abstraction · Status: planned · Depends on: 02-02

## Goal

Each default value lives once, in `internal/config`; packages trust the validated
configuration instead of re-defaulting it, and `api.New` requires its core dependencies
instead of tolerating nil. Structure only: a configuration produces the same behaviour.

## Context

- `internal/api`: 23 nil-dependency guards (`admin.go:170,400,548`; `auth.go:47,65`;
  `jobs_get.go:39,100`; `metrics.go:15,19`; `health.go:153,165,177`;
  `middleware.go:32,169`; `quota.go:80`; `ratelimit.go:110`;
  `submit.go:163,615,866,877`; `sync.go:31`) for Config, Store, Blobs and Metrics, which
  production always sets. Keep only the optional ones (Wake, Completions, PDF).
- `internal/api`: fallbacks to `defaultRequestBytes`, `defaultSubmitsInFlight` and
  `document.Default*` (`submit.go:32,50,168-171,427-430,445-458`;
  `submit_json.go:70-76,168-174`; `api.go:121-124`) for limits that `config.defaults()`
  sets and `Validate` refuses below 1.
- `internal/worker/pool.go:25-31,110-144,445-457` and
  `internal/sweeper/sweeper.go:19-28,110-130`: `Default*` constants copying
  `internal/config/service.go:68-83` and `config.go:325-350`, zero-value fallbacks, a
  double fallback in `maxAttempts`; `Batch`, `Grace`, `QuotaWindowsToKeep`,
  `TokenRetention` and `Clock` are never set by `serve` (`serve.go:288-297`): make them
  constants unless a test needs them.
- `internal/api/api.go:37-76`: about 40 comment lines on `Deps`, mostly describing nil
  behaviour.
- Test rigs need a complete configuration: add one `config` helper for tests (for
  example `config.ForTest()` in a `_test.go`-only package or in `internal/config`
  returning `defaults()` applied) rather than nil checks.
- About −110 lines; medium risk because many test rigs change.

## Tasks

- [ ] Map every default to its single source.
  - Skill: EVID.
  - Prompt: "List every constant or literal in internal/api, internal/worker,
    internal/sweeper and internal/document that duplicates a default in
    internal/config (defaults(), service.go, config.go), with both locations and values;
    list every `== nil` guard on api.Deps fields with the tests that rely on it. Report
    only."
- [ ] Plan and execute in three steps: config test helper; api; worker and sweeper.
  - Skill: GOREF.
  - Prompt: "/golang-refactoring Plan mode: (1) add one test helper that returns a
    validated default configuration; (2) make api.New require Config, Store and Blobs,
    remove their nil guards and the limit fallbacks, shorten the Deps comments; (3) make
    worker and sweeper read their values from config only, turning never-set knobs into
    constants. Behaviour for a validated configuration must not change. Guardrails:
    skills.md." Then "/golang-refactoring Execute mode: step <n>."
- [ ] Check for lost error paths.
  - Skill: PRTK (`silent-failure-hunter`).
  - Prompt: "Use the silent-failure-hunter agent on the diff of this job: report any
    error that is now ignored or any path where a missing dependency would panic instead
    of failing at start."
- [ ] Gate.
  - Skill: REVIEW.
  - Prompt: `/code-review high <first>^..<last>`, then `make conformance`.

## Acceptance criteria

- `grep -rn 'Default' internal/api internal/worker internal/sweeper` shows no copy of a
  configuration default; `api.New` fails fast on a missing core dependency; `make check`
  and `make conformance` green.

## Out of scope

- Changing any default value.
