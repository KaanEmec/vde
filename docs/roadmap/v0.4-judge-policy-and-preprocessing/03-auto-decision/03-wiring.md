# Job: Worker, CLI and audit wiring

Version: v0.4 · Epic: 03 Auto decision in the loop · Status: in progress · Depends on: 02

## Goal

The service and the CLI run jobs under their stored or chosen policy, persist
`verification`, and audit it. The end-to-end paths are tested.

## Tasks

- [ ] `internal/worker/pool.go`: pass `JudgePolicy` (from the job row), `SampleRate` (from
      the application) and `SampleKey` (the job id) into `extract.Input`. Write
      `Outcome.Verification` and `StatusExtracted` through the store. A resumed job keeps
      its policy and its sampling decision (same key).
- [ ] `internal/api/sync.go`: the synchronous endpoint returns `extracted` and
      `verification` like the job resource.
- [ ] `internal/audit`: `job.finished` details gain `verification` and `judge_policy`
      (metadata, never content). `docs/operator/operations.md` lists the new details.
- [ ] `cmd/vde/extract.go`: the sample key is the document bytes. The output and the
      progress line on stderr show `verification`. `extracted` exits with a new code 4
      (`exitExtracted` in `cmd/vde/main.go`). It is not 0, because scripts read 0 as
      accepted. Codes 0, 2 and 3 keep their meanings. Document it in the CLI help and in
      `docs/operator/evaluation.md`.
- [ ] `internal/webhook`: the notice carries `status: extracted` unchanged. Add a test.
- [ ] `cmd/vde/serve_e2e_test.go`: one end-to-end case per policy over HTTP with the fake
      provider (submit, poll, check `status` and `verification`).

## Acceptance criteria

- Worker tests: `always`, `never` and `auto` jobs end with the expected status and
  `verification` in SQLite. A crash after a grounding repair resumes without a second
  repair.
- `TestJobFinishedIsAudited` covers `verification`. Audit output carries no candidate
  values.
- `cmd/vde/extract_test.go`: `--judge auto` on `examples/samples/booking.txt` with a fake
  grounded candidate prints `verification: grounded` and makes no judge call.
- The e2e test passes for all three policies, and the conformance suite passes unchanged.

## Out of scope

Eval reporting (epic 05).
