# Job: `auto` gate, sampling and metrics

Version: v0.4 · Epic: 03 Auto decision in the loop · Status: planned · Depends on: 01

## Goal

Under `auto`, a candidate that passes the grounding gate is accepted without a judge, with
`verification: grounded`. A deterministic sample goes to the judge anyway, and disagreements
are counted. Everything else goes to the judge.

## Context

ADR 0009. The gate passes only when all of these hold: the representation is groundable,
no schema retry occurred in this attempt (the candidate came out valid on the extractor's
first answer of the attempt), and `CriticalMissed == 0`. Sampling must be reproducible: a
job is sampled when `fnv64a(key) / 2^64 < sample_rate`. The key is the job id in the
service and the document bytes in the CLI and the local eval, so reruns sample the same
cases.

## Tasks

- [ ] `internal/extract/extract.go`: the gate as above. Pass: unless sampled, end
      `accepted`, `verification: grounded`, `judge_confidence: null`, feedback empty.
      Sampled: ask the judge (and the second judge when configured). An accept gives
      `verification: judge`. A reject counts a disagreement and is the attempt's feedback, as
      in v0.3 (the judge's authority wins).
- [ ] Gate fails: ungroundable, schema retry this attempt, or critical misses left after
      the one repair (job 01). The judge is asked as under `always`. Grounding entries are
      kept as advisory feedback on the outcome (`auto` is opt-in, so `grounding` sources are
      allowed there).
- [ ] `internal/extract/sample.go`: `Sampled(key []byte, rate float64) bool` (FNV-1a 64,
      `hash/fnv`). Rate 0 never samples and rate 1 always does.
- [ ] `internal/metrics`: `vde_judge_decisions_total{policy, decision}` with `decision` in
      `judged | grounded | skipped | sampled` (`skipped` is `never`), counted once per
      finished job, and `vde_grounding_disagreements_total` (a sampled judge rejected a
      grounded candidate). The loop reports through an observer like `Usage`, so
      `internal/extract` does not import `internal/metrics`.

## Acceptance criteria

Scenario tests (fake provider):

- `auto`, text document, grounded valid candidate, not sampled: 1 extractor call, 0 judge
  calls, `accepted`, `verification: grounded`.
- **Exit criterion:** `auto`, candidate with an invented date, in every variant (repair
  fails, last attempt, schema retry first): the outcome is never `accepted` with
  `verification: grounded`. It is accepted only by a judge call.
- `auto`, page-image document (ungroundable): the judge is always asked.
- `auto`, schema violation then a valid grounded candidate: the judge is asked (a schema
  retry occurred).
- Sampling: with rate 1, the grounded candidate is judged. A judge reject increments the
  disagreement counter and feeds the next attempt. With rate 0, never judged.
  `TestSampled` checks that the same key always gives the same answer and that the share
  is near the rate over 10,000 random keys (±1 %).
- Metrics tests: one decision per job with the right labels.

## Out of scope

Adaptive sample rates, per-field confidence, a judge that sees grounding results.
