# Job: Worker, CLI and audit wiring

Version: v0.4 · Epic: 03 Auto decision in the loop · Status: done · Depends on: 02

## Goal

The service and the CLI run jobs under their stored or chosen policy, persist
`verification`, and audit it. The end-to-end paths are tested.

## Tasks

- [x] `internal/worker/pool.go`: pass `JudgePolicy` (from the job row), `SampleRate` (from
      the application) and `SampleKey` (the job id) into `extract.Input`. Write
      `Outcome.Verification` and `StatusExtracted` through the store. A resumed job keeps
      its policy and its sampling decision (same key).
- [x] `internal/api/sync.go`: the synchronous endpoint returns `extracted` and
      `verification` like the job resource.
- [x] `internal/audit`: `job.finished` details gain `verification` and `judge_policy`
      (metadata, never content). `docs/operator/operations.md` lists the new details.
- [x] `cmd/vde/extract.go`: the sample key is the document bytes. The output and the
      progress line on stderr show `verification`. `extracted` exits with a new code 4
      (`exitExtracted` in `cmd/vde/main.go`). It is not 0, because scripts read 0 as
      accepted. Codes 0, 2 and 3 keep their meanings. Document it in the CLI help and in
      `docs/operator/evaluation.md`.
- [x] `internal/webhook`: the notice carries `status: extracted` unchanged. Add a test.
- [x] `cmd/vde/serve_e2e_test.go`: one end-to-end case per policy over HTTP with the fake
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

## Result (2026-10-17)

Done. The worker runs each job under its stored policy, with the application row's
sample rate and the job id as the sample key (a resumed job keeps both); it selects with
the image and PDF policies and the process's pdfium engine, as submit does, compares
`Representation.Label()` with the stored representation, and under `never` and `auto`
reads the grounding text of a PDF sent natively or as pages. `vde serve` builds one engine
per process (`server.data_dir/pdfium-cache`, `documents.pdf.workers`, lazy start, closed
on shutdown, a warning when the cache is unusable) and gives it to submit and the workers.
`job.finished` audit details carry `verification` and `judge_policy`.

Tests: `TestJudgePolicies`, `TestResumeAfterGroundingRepair`,
`TestTimeoutAfterGroundingRepairDropsEntries`, `TestPDFPreparedByService` and
`TestJobFinishedIsAudited` (worker); `TestSubmitPDFPreparedByService` and
`TestExtractSyncExtracted` (API); `TestExtractedStatusPassesThrough` (webhook);
`TestExtractJudgeFlag` and `TestExtractPDFWithoutPages` (CLI, `--judge auto` on
`booking.txt` and `booking.pdf`: `grounded`, one provider call); `TestServeJudgePolicies`
(end to end over HTTP with a judge that always fails: `auto` text and PDF end
`accepted`/`grounded`, `never` ends `extracted`, `always` fails `provider_unavailable`,
decision metrics checked); the redaction test runs the grounding paths. `make check` and
`make conformance` pass.

Deviations:

- `vde extract` already had exit code 4 and `--judge` (job 01-03). Its sample rate is
  `config.DefaultSampleRate` (there is no role-level rate); the final progress line shows
  the verification, and a repair attempt reads "values not found in the document ...
  (grounding repair)". `vde eval` runs cases the same way under `roles.judge.policy`.
- The end-to-end test is one service with an application per policy
  (`TestServeJudgePolicies`) rather than one test per policy.
- `internal/api/sync.go` needed no change (the job JSON carries both fields); a test pins it.
- The worker now applies the image policy when it re-selects (it called `Select` without
  it), so its parts match submit's for converted images too.
- Submit refuses a PDF over `documents.pdf.max_pages` with a message naming the limit
  (`msgPDFTooManyPages`); the document package's refusal also wraps
  `pdf.ErrTooManyPages` for that.
- An application's `documents.pdf.mode` exists only in the YAML
  (`Config.PDFModeOf(id)`); applications created over the admin API use
  `documents.pdf.mode`.
- Conformance: with service-side PDF preparation a PDF without pages for models without
  a PDF reader is no longer refused under the default `documents.pdf.mode: auto`. The
  suite's instance without a PDF reader now runs `mode: native`, which keeps contract 4.11
  exactly; `docs/conformance/travelinbox-v1-review.md` has the open point.
- `vde config check` prints the grounding rules under the judge role and an
  application's `documents.pdf.mode` when it sets one (policies and PDF lines were there
  from epics 01 and 04). The log allow-list gains `verification`.
- Leftovers from epic 04 done here: `LICENSE` and `NOTICE` copied into the image
  (`/usr/share/doc/vde/`, `.dockerignore` lets them in), the `internal/ground` and
  `internal/pdf` rows and the pdfium dependency row in `docs/architecture/overview.md`,
  and the owner-suite note in `docs/operator/evaluation.md`.
