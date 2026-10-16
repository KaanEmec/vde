# Job: Classifier-style judge adapter

Version: v0.3 · Epic: 02 More providers · Status: done (evaluation pending) · Depends on: v0.1 02-01, 05-04

## Goal

Let the judge role be served by a classifier model that answers fixed typed questions
with calibrated probabilities (the owner named TypeSafe AI's Jev as an example), instead
of a chat model that writes a verdict and feedback, while still meeting the contract's
requirement that a `reject` carries pointer-level feedback.

## Context

Decision 14. ADR 0004 (classifier-style provider). Contract §7: a `reject` MUST have at
least one feedback entry; messages are shown to end users. Jev-style models accept text
or structured JSON and return typed decisions with probabilities, with no free-form
output; at the time of planning the API is early-access and its documentation must be
read before implementing.

## Design to settle in this job

- The provider interface returns text; a classifier returns a decision and a
  probability. Keep the interface: the adapter renders the decision as the verdict JSON
  (`{"verdict": "...", "confidence": p, "feedback": {...}}`) so the loop needs no change.
- Feedback on `reject`: a classifier cannot point at fields. Options, to be chosen with
  evidence from the evaluation harness:
  1. `judge.feedback_fallback: llm` runs the configured LLM judge group only when the
     classifier rejects, to produce feedback (cost only on rejections);
  2. `judge.feedback_fallback: whole_result` emits one entry at pointer `""` with a fixed
     message and the confidence, which satisfies the contract minimally but gives the
     extractor little to repair.
- Classifier questions: a fixed set the operator configures (accuracy, completeness,
  invented values), each yielding a probability; the verdict is `accept` when all pass a
  configured threshold; `confidence` is the minimum of the probabilities.
- Input to the classifier: the same document representation (text only; a classifier
  that cannot read images or PDFs restricts the judge group's `accepts`, which the
  representation selection already handles) plus the candidate JSON.

## Tasks

- [ ] Read the provider's documentation; write `docs/providers/typesafe.md` with what is
      sent and the data-handling terms. Not possible yet (early access); the generic
      contract is documented in `docs/providers/classifier.md` instead.
- [x] Implement the adapter and the two fallback modes behind configuration; hash the
      mode into `service_version`.
- [ ] Evaluate on the TravelInbox corpus with the evaluation harness (epic 06): false
      accept rate, false reject rate, latency and cost versus the LLM judge. Record in
      `docs/quality.md`. Needs an epic 06 harness run against a real classifier.
- [ ] Decide the default fallback mode from the numbers; document the trade-offs.

## Acceptance criteria

- Scenario tests with a fake classifier; evaluation report recorded; the contract's
  `reject` rule holds in both fallback modes.

## Out of scope

Using the classifier as the extractor (it does not generate), threshold tuning per field.

## Outcome

- `internal/provider/classifier`: `provider: classifier` endpoints; models carry
  `questions: [{id, text, threshold}]`. The adapter posts `{model, questions, text}` to
  `{base_url}/classify` and reads `{decisions: [{question, label, probability}]}`, then
  renders the verdict JSON: accept when every probability reaches its threshold,
  confidence the minimum probability, on reject one entry at `""` (fixed message plus the
  confidence). Malformed answers are permanent failures, so they end as `judge_failed`,
  never as acceptance. Documented in `docs/providers/classifier.md`.
- `roles.judge.feedback_fallback: whole_result | llm` (default `whole_result`) and
  `roles.judge.feedback_group`: with `llm`, a reject answered by a classifier endpoint
  gets the feedback group's pointer-level entries (judge framing, one call per rejection);
  the verdict stays reject, and the `""` entry is kept when the group accepts or fails.
  `llm` and its group are hashed into `service_version`; questions are hashed per pair.
- Validation: classifier models need questions and text-only `accepts`; no classifier in
  extractor groups or the feedback group; `feedback_group` only with `llm`.
- Tests: adapter tests against `httptest`, and loop tests with a fake classifier server
  (`TestRun_classifierWholeResult` in both spellings of the default,
  `TestRun_classifierLLMFeedback`, `TestRun_classifierAccept`, `..Deadline`, `..Down`),
  each asserting the contract §7 reject rule.

Deviations:

- The Jev API is early access and was not available, so there is no `typesafe.md`: the
  adapter implements a generic classify contract (`docs/providers/classifier.md`) that a
  vendor adapter or a thin proxy must satisfy. A Jev-specific adapter, with its
  data-handling terms, follows when its documentation is available.
- The adapter always renders the whole-result entry; the `llm` mode replaces it in the
  loop, since only the loop can call another group.
