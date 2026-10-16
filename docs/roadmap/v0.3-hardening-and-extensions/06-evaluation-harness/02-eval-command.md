# Job: `vde eval` command and report

Version: v0.3 · Epic: 06 Evaluation harness · Status: in progress · Depends on: 01

## Goal

Run a corpus through the engine (locally) or through a deployment (over HTTP), compare
with ground truth and print a report keyed by `service_version`.

## Tasks

- [ ] `vde eval --corpus dir [--config file | --url … --token-env …] [--concurrency n]`:
      per case: status, attempts, duration, tokens, field accuracy per `compare.yaml`;
      aggregate: acceptance rate, field accuracy, false accepts (judge accepted but
      ground truth differs), false rejects, cost estimate.
- [ ] Output: human table and a JSON report saved under `eval-reports/<service_version>-
      <date>.json`; a `--compare previous.json` diff.
- [ ] False-accept rate is the headline number (the judge's purpose is to avoid them).
- [ ] CI runs the harness with the fake provider for mechanics only; real runs are
      manual before releases and recorded in `docs/quality.md`.

## Acceptance criteria

- A report for the TravelInbox corpus with a real provider is recorded before v0.3 is
  tagged, for the LLM judge and, when available, the classifier judge.

## Out of scope

Automatic prompt optimization.
