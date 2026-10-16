# Job: `vde eval` command and report

Version: v0.3 · Epic: 06 Evaluation harness · Status: done · Depends on: 01

## Goal

Run a corpus through the engine (locally) or through a deployment (over HTTP), compare
with ground truth and print a report keyed by `service_version`.

## Tasks

- [x] `vde eval --corpus dir [--config file | --url … --token-env …] [--concurrency n]`:
      per case: status, attempts, duration, tokens, field accuracy per `compare.yaml`;
      aggregate: acceptance rate, field accuracy, false accepts (judge accepted but
      ground truth differs), false rejects, cost estimate.
- [x] Output: human table and a JSON report saved under `eval-reports/<service_version>-
      <date>.json`; a `--compare previous.json` diff.
- [x] False-accept rate is the headline number (the judge's purpose is to avoid them).
- [x] CI runs the harness with the fake provider for mechanics only; real runs are
      manual before releases and recorded in `docs/quality.md`.

## Acceptance criteria

- A report for the TravelInbox corpus with a real provider is recorded before v0.3 is
  tagged, for the LLM judge and, when available, the classifier judge.

## Follow-ups

- Record the job `error.code` per case in the report so provider failures (rate limits) are distinguishable from extraction failures.

## Out of scope

Automatic prompt optimization.

## Notes and deviations

- `cmd/vde/eval.go` (flags, the local engine, the HTTP client) and `internal/eval` (corpus,
  compare rules, `Run` with a bounded worker count, `Summarize`, the report, its table and
  `--compare` diff). Flags: `--corpus`, `--config` or `--url` with `--token-env`,
  `--concurrency` (default 1), `--cases` (glob on case ids), `--out` (default
  `eval-reports/`, git-ignored; `""` saves nothing), `--compare`, `--provider-script`.
  Documented in [docs/operator/evaluation.md](../../../operator/evaluation.md).
- Local runs reuse `vde extract`'s pieces (`loadConfigEnv`, `readDocument`,
  `document.Select`, `extract.NewRoles`, `extract.Run`) with one fresh set of role groups
  and one usage observer per case, so tokens are per case; endpoint health is therefore not
  shared between cases, unlike the service.
- `--url` mode submits with `retention: none`, retries a 429/503 submit after
  `Retry-After` (5 tries), polls 2 s doubling to 30 s for at most 10 minutes and gives up
  after three failed polls in a row. The job resource has no token counts, so usage is
  "not reported" there; duration includes queueing.
- Definitions: false accept = accepted and a compared field differs (rate over accepted
  cases); false reject = rejected and every compared field matched (rate over rejected);
  field accuracy over all cases (no result matches nothing) and over accepted cases. Cost
  is a placeholder (`cost_usd: null`) next to the token totals.
- `--provider-script` gives every case a fresh fake and replaces `{{expected}}` in answers
  with the case's ground truth. `make eval` runs the whole corpus with
  `testdata/eval/fake-script.yaml` (schema violation, then the ground truth, accepted):
  61 cases accepted after 2 attempts, all correct.
- Exit codes: 0 when every case ran (whatever the quality), 1 for usage or configuration
  errors and for any case the harness could not run.
- Nothing printed or saved carries content: case ids, statuses, counts and JSON pointers of
  mismatched fields only. Tests check that document text and result values stay out of the
  output.
- Real run recorded in [docs/quality.md](../../../quality.md) (report in
  `docs/quality/`): 2026-10-16, `vde-0.0.0-dev+56cf97c8`, `google/gemini-3.8-flash` as
  extractor and LLM judge, 50 TravelInbox cases: false accepts 43 of 50 (86.0%), acceptance
  100%, field accuracy 92.4%, median 21.1 s, p95 43.5 s, 942k input and 143k output tokens.
  The classifier judge run is still open: it is not available yet (epic 02).
