# Job: Per-request override, admin API and CLI flag

Version: v0.4 · Epic: 01 Judge policy · Status: planned · Depends on: 01, 02

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

- [ ] `internal/api/submit.go`: optional multipart part `judge` (`text/plain`, one of the
      three values, at most 16 bytes). When the application does not allow override, the
      request is refused with `400 bad_request` and a message naming the part. An
      unknown value is also a `400`. The part is part of the idempotency body hash.
- [ ] `internal/api/submit_json.go`: the same as a JSON field `judge`. `internal/api/sync.go`
      accepts it on `POST /v1/extract`.
- [ ] The job row stores the effective policy (request, else application, else role).
      `X-Service-Version` and the job's `service_version` use
      `ApplicationServiceVersions[policy]`.
- [ ] `internal/api/admin.go` and the `applications` migration (`0009_application_judge.sql`
      for both dialects): `judge.policy`, `judge.allow_override` and `judge.sample_rate` on
      admin-created applications, validated like configuration; audited and logged as other
      application `PATCH` changes are.
- [ ] `cmd/vde/extract.go`: `--judge always|never|auto` (default: the configuration's) sets
      `extract.Input.JudgePolicy`. The JSON output gains `verification`.
- [ ] `docs/api/guide.md`: the `judge` part, the `400`, `extracted`, `verification` and
      `source: grounding` (draft; epic 05 finishes the guide).
- [ ] `docs/conformance/travelinbox-v1-review.md`: section 2 rows for `judge`,
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
