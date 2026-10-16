# Job: Judge verdict

Version: v0.1 · Epic: 05 Extraction loop · Status: done · Depends on: 02

## Goal

Parse the judge's answer into `accept` or `reject` with pointer-keyed feedback and an
optional confidence, exactly as the contract specifies plus the one optional key, and
convert it to the feedback array without editing messages beyond a length cap.

## Context

Contract §7: `{"verdict": "accept"|"reject", "feedback": {pointer: message}}`; anything
else is unparseable; a `reject` MUST have at least one entry; `accept` MAY carry advisory
entries which are forwarded; order preserved; messages capped at ≥ 2,000 characters;
"If the service needs the judge to return more (a confidence, a category), it adds
optional keys and ignores their absence." Decision 14: optional `confidence` (0 to 1),
forwarded on the job as `judge_confidence`, and a future classifier-style judge (v0.3)
that produces verdict and confidence natively.

## Tasks

- [x] `Verdict{Accept bool; Feedback []Feedback; Confidence *float64}`.
      `ParseVerdict(text) (Verdict, error)` built on the answer parser (job 02); verdict
      must be exactly `accept` or `reject`; `feedback` an object of string values;
      `confidence` optional, a number in [0, 1] (out of range or wrong type → ignored,
      logged as a warning count, never fails the verdict); unknown keys ignored.
- [x] Order: parse `feedback` with a `json.Decoder` token walk to keep the judge's order.
- [x] Rules: `reject` with empty feedback → unparseable (the loop re-asks once); `accept`
      with feedback → accepted with advisory entries (`source: "judge"`); each message
      truncated at `limits.feedback_message_chars` (default 4,000) with an ellipsis.
- [x] Pointers are syntactically checked as RFC 6901 and otherwise kept as given.
- [x] `Outcome.JudgeConfidence *float64` carries the last verdict's confidence (nil when
      absent), persisted and exposed by v0.2 as `judge_confidence`.
- [x] Tests: accept, reject with two entries in order, accept with advisory, reject
      without entries, wrong verdict word, fenced verdict, extra keys, confidence
      present, confidence out of range.

## Acceptance criteria

- Order of entries matches the judge's output in tests; confidence is optional and never
  changes acceptance.
- No message is edited except truncation.

## Out of scope

Using confidence to decide acceptance (the verdict decides; a threshold policy could be a
later per-application option), multiple judges, the classifier judge adapter (v0.3).

## Implementation notes

Done in `internal/extract/verdict.go` as the unexported `parseVerdict(text, maxChars)`.
Details beyond the job text: a missing `feedback` key on an accept is an empty feedback
(on a reject it is unparseable); a feedback key that is not an RFC 6901 pointer, a
non-string or null message, and a duplicate `verdict` or `feedback` key make the verdict
unparseable. An ignored confidence is logged as a warning (no content) instead of being
counted; metrics arrive in v0.2. `limits.feedback_message_chars` was added to the
configuration (0 means 4,000) and the cap also applies to schema feedback messages.
