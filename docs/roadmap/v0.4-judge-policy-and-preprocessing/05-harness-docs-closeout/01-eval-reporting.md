# Job: `vde eval`: verification, judge calls, case lists

Version: v0.4 · Epic: 05 Harness, docs and closeout · Status: planned · Depends on: 03-03

## Goal

The eval report shows, per case and in total, how each result was verified and how many
judge calls it took, so `always`, `never` and `auto` can be compared on the same corpus
(ADR 0008, ADR 0009).

## Tasks

- [ ] `internal/eval/run.go` and `report.go`: per case `verification`, `judge_calls` and
      `judge_policy`. Totals: judge calls, cases per verification, grounded share, sampled
      cases and disagreements. False accepts are split by verification (`judge` versus
      `grounded`), so a grounded false accept is visible as such. `extracted` cases are
      counted separately with their field accuracy. They are neither accepted nor rejected,
      so they never count as false accepts or false rejects.
- [ ] `--compare` diff covers the new totals.
- [ ] `cmd/vde/eval.go`: `--judge always|never|auto` overrides the configuration for a
      local run (the sample key is the document bytes, so reruns sample the same cases).
      In `--url` mode the flag sends the `judge` part and fails clearly when the application
      does not allow override.
- [ ] `--cases` accepts a comma-separated list of globs (`--cases 'ti-0*,receipt-*'`). A
      case is selected when any glob matches. An empty element is a usage error.
- [ ] Record the job `error.code` per case (follow-up from v0.3 06-02), if not done yet.
- [ ] `make eval` (fake provider) runs the corpus once per policy. The fake script gains a
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
