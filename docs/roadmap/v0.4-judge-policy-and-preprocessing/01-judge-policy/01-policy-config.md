# Job: Policy configuration and `service_version`

Version: v0.4 · Epic: 01 Judge policy · Status: in progress · Depends on: v0.3

## Goal

`roles.judge.policy` and `applications[].judge` are parsed, validated, hashed into
`service_version` and printed by `vde config check`. The loop does not change behaviour yet.

## Context

ADR 0009. The default `always` must leave every existing `service_version` unchanged, so
TravelInbox deployments see no version bump from this job alone. An application that allows
override needs one `service_version` per policy it can run under.

## Tasks

- [ ] `internal/config/config.go`: `Role.Policy string` (judge role only; `always`,
      `never`, `auto`; default `always`); `Application.Judge` with `Policy` (default: the
      role's), `AllowOverride bool` (default `false`) and `SampleRate *float64` (default
      0.05). A `JudgePolicy` type with `Parse`/`String` lives in `internal/config`.
- [ ] `internal/config/validate.go`: unknown policy is an error. `sample_rate` must be in
      [0, 1]. `policy` on `roles.extractor` is an error. `never` on the role together with
      a `second_judge` is a warning (the second judge is never asked). `auto` with a judge
      group that is a classifier is allowed.
- [ ] `internal/extract/version.go`: `ApplicationServiceVersion` hashes the effective policy
      and, for `auto`, the sample rate. A new `ApplicationServiceVersions(cfg, app, build)
      map[config.JudgePolicy]string` returns one version per policy the application can run
      under (only the configured one unless `allow_override`). `always` with defaults adds
      nothing to the hash, so existing versions stay as they are. The grounding rule version
      constant (`ground.RulesVersion`, epic 02) is hashed when the policy is `auto` or
      `never`, and also under `always` once job 03-01 adds grounding repair (that job
      changes the hash for `always` on purpose).
- [ ] `cmd/vde/config.go` (`vde config check`): print the judge policy of the role and of
      every application, override allowed or not, and the sample rate for `auto`.
- [ ] `docs/operator/configuration.md`: rows for `roles.judge.policy` and
      `applications[].judge.*` (full wording in epic 05).

## Acceptance criteria

- Validation table tests in `internal/config/validate_test.go`, one per rule above.
- `internal/extract/version_test.go`: an unset policy and an explicit `always` with defaults
  produce the v0.3 hash. `never`, `auto` and a different `sample_rate` each produce a
  different one. `ApplicationServiceVersions` returns three entries with `allow_override`
  and one without.
- `TestRun_scenarioSuiteUnchanged` still passes.
- `cmd/vde/config_test.go`: the summary lines appear and carry no secrets.

## Out of scope

Behaviour in the loop (epic 03), the request part (job 03), grounding (epic 02).
