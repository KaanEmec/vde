# Job: `vde eval`: verification, judge calls, case lists

Version: v0.4 · Epic: 05 Harness, docs and closeout · Status: done · Depends on: 03-03

## Goal

The eval report shows, per case and in total, how each result was verified and how many
judge calls it took, so `always`, `never` and `auto` can be compared on the same corpus
(ADR 0008, ADR 0009).

## Tasks

- [x] `internal/eval/run.go` and `report.go`: per case `verification`, `judge_calls` and
      `judge_policy`. Totals: judge calls, cases per verification, grounded share, sampled
      cases and disagreements. False accepts are split by verification (`judge` versus
      `grounded`), so a grounded false accept is visible as such. `extracted` cases are
      counted separately with their field accuracy. They are neither accepted nor rejected,
      so they never count as false accepts or false rejects.
- [x] `--compare` diff covers the new totals.
- [x] `cmd/vde/eval.go`: `--judge always|never|auto` overrides the configuration for a
      local run (the sample key is the document bytes, so reruns sample the same cases).
      In `--url` mode the flag sends the `judge` part and fails clearly when the application
      does not allow override.
- [x] `--cases` accepts a comma-separated list of globs (`--cases 'ti-0*,receipt-*'`). A
      case is selected when any glob matches. An empty element is a usage error.
- [x] Record the job `error.code` per case (follow-up from v0.3 06-02), if not done yet.
- [x] `make eval` (fake provider) runs the corpus once per policy. The fake script gains a
      grounded answer so `auto` takes the grounded path in CI.

## Acceptance criteria

- `internal/eval/report_test.go`: totals for a mixed set (judge-accepted, grounded,
  sampled with a disagreement, extracted, rejected) match hand-computed numbers. A grounded
  false accept is counted under `grounded`.
- `cmd/vde/eval_test.go`: `--cases` with two globs selects their union, and `--judge never`
  yields only `extracted` or `rejected` cases with zero judge calls.
- No report or table output carries content (the existing content assertions cover the new
  fields).

## Out of scope

Running the owner's comparison (the owner runs it and records it in `docs/quality.md`);
cost in currency.

## Result (2026-10-18)

Done. `internal/eval`: `Outcome` and `CaseResult` gain `judge_policy`, `verification`,
`judge_calls` (`null` over HTTP), `decision` and `disagreements` (the per-case `error_code`
existed since v0.3); `StatusExtracted`; `Summary` gains `extracted`,
`extracted_field_accuracy`, `false_accepts_judge`, `false_accepts_grounded`,
`verified_judge`, `verified_grounded`, `grounded_share`, `judge_calls`,
`judge_call_cases`, `sampled` and `disagreements`; `Report.judge_policy`. The table has
`VERIFIED`, `DECISION` and `JUDGE CALLS` columns and marks `FALSE ACCEPT (grounded)`;
`--compare` diffs every new total and lists a case whose verification changed
(`accepted/judge -> accepted/grounded`). `eval.ParseCases` and
`LoadCorpus(dir, patterns...)` select the union of comma-separated globs.

`cmd/vde/eval.go`: `--judge` (checked before the configuration is loaded) runs every
local case under that policy with its `service_version`; with `--url` it sends the `judge`
part, and a `400` naming the part fails the case with a message naming
`judge.allow_override`. `make eval` runs the corpus three times: `always` and `never` with
`testdata/eval/fake-script.yaml`, `auto` with the new `testdata/eval/fake-script-grounded.yaml`
(the ground truth on the first attempt). On the 61 cases: `always` 61 judged; `never` 61
extracted, 0 judge calls; `auto` 14 grounded (all 11 synthetic cases, three TravelInbox
hotels), 47 judged.

Tests: `TestSummarize_judgePolicy` (a mixed set with hand-computed totals, a grounded false
accept counted under `grounded`), `TestParseCasesAndLoadCorpus`,
`TestEval_judgePolicies` (two globs select their union; `--judge never` yields only
`extracted` or `rejected` with zero judge calls; `--judge auto` yields grounded cases with
zero judge calls; no content in the output), `TestEval_urlJudge` (`judge` part sent,
`extracted` is terminal, the refusal names `allow_override`) and new usage errors.

Deviations:

- Found in review: the `--url` poll loop did not treat `extracted` as terminal and would
  have polled until the 10-minute limit; it now uses the job store's terminal set
  (`job.Status.Terminal`).
- The decision totals are `sampled` and `disagreements` only: `grounded` decisions equal
  `verified_grounded`, and `skipped` equals the cases that reached a decision under
  `never`. Decisions and judge calls are known for local runs only.
- `make eval` adds `testdata/eval/fake-script-grounded.yaml` instead of changing
  `fake-script.yaml`, whose schema retry first keeps the `always` story (two attempts).
