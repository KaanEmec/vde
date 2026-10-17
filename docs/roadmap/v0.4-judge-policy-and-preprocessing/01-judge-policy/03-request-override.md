# Job: Per-request override, admin API and CLI flag

Version: v0.4 · Epic: 01 Judge policy · Status: done · Depends on: 01, 02

## Goal

A caller of an application with `judge.allow_override: true` can send `judge`
(`always|never|auto`) with a job. Applications created over the admin API can carry the
same `judge` settings. `vde extract --judge` chooses the policy locally.

## Context

ADR 0009. Override is off by default: a caller cannot weaken an operator's choice unless the
operator allows it. The effective policy is stored on the job (`judge_policy`) because the
worker, not the submit handler, runs the loop. The job reports the `service_version` of that
policy.

## Tasks

- [x] `internal/api/submit.go`: optional multipart part `judge` (`text/plain`, one of the
      three values, at most 16 bytes). When the application does not allow override, the
      request is refused with `400 bad_request` and a message naming the part. An
      unknown value is also a `400`. The part is part of the idempotency body hash.
- [x] `internal/api/submit_json.go`: the same as a JSON field `judge`. `internal/api/sync.go`
      accepts it on `POST /v1/extract`.
- [x] The job row stores the effective policy (request, else application, else role).
      `X-Service-Version` and the job's `service_version` use
      `ApplicationServiceVersions[policy]`.
- [x] `internal/api/admin.go` and the `applications` migration (`0009_application_judge.sql`
      for both dialects): `judge.policy`, `judge.allow_override` and `judge.sample_rate` on
      admin-created applications, validated like configuration; audited and logged as other
      application `PATCH` changes are.
- [x] `cmd/vde/extract.go`: `--judge always|never|auto` (default: the configuration's) sets
      `extract.Input.JudgePolicy`. The JSON output gains `verification`.
- [x] `docs/api/guide.md`: the `judge` part, the `400`, `extracted`, `verification` and
      `source: grounding` (draft; epic 05 finishes the guide).
- [x] `docs/conformance/travelinbox-v1-review.md`: section 2 rows for `judge`,
      `verification`, `extracted` and `grounding` entries, and a section 6 open point:
      "TravelInbox keeps `always`; confirm the client tolerates `verification` and would
      treat `extracted` as not accepted."

## Acceptance criteria

- `internal/api/submit_test.go` and `submit_json_test.go`: the part is accepted only with
  `allow_override`, otherwise `400`. Without the part, the application's policy is stored.
  The `service_version` header matches the stored policy's version. Repeating an
  `Idempotency-Key` with a different `judge` answers `422 idempotency_mismatch`.
- Admin API tests: create, patch and read the `judge` settings; invalid values are `400`.
- `cmd/vde/extract_test.go`: `--judge never` reaches `extract.Input`, and a bad value
  exits 1 with a usage message.

## Out of scope

Per-request sample rates; per-request grounding annotations outside the schema.

## Result (2026-10-17)

Done. The `judge` part (and JSON field) is parsed in `submission.judgePolicy`: refused with
`400 bad_request` naming the part unless the application row allows override, `400` for
an unknown value, a media type other than `text/plain` or more than 16 bytes. The effective
policy is stored in `jobs.judge_policy`; the job's `service_version` and the submit
response's `X-Service-Version` are those of that policy. Applications carry
`judge_policy`, `judge_allow_override`, `judge_sample_rate` (migration
`0009_application_judge.sql`, both dialects), mirrored from the YAML by `vde serve` and
settable over the admin API (validated by `config.CheckApplication`).

Deviations:

- `api.Deps.AppServiceVersion` now takes the policy: `func(job.Application,
  config.JudgePolicy) string`; authenticated requests carry the version of the
  application's own policy.
- `vde extract --judge` is checked before the configuration is loaded (exit 1 with
  `vde extract: --judge: ...`); its `service_version` hashes the chosen policy. Exit code 4
  is `extracted`. `extractOptions.run` lets tests observe the `extract.Input`.
- No new audit kind: judge settings changes go through the existing application
  `PATCH` path (audited only on enable or disable, as before).
- Under `never` and `auto` the loop still runs as under `always` (epic 03 implements the
  gate); `Outcome.Verification` is `judge` on accept and `none` otherwise.
