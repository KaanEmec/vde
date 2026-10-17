# Epic 02: Grounding check

Version: v0.4 · Status: done· Depends on: v0.3

A deterministic check that every string and number value in a candidate occurs in the
document text. It is the cheap guard against invented values that no tested judge caught
(the event-invoice finding, [ADR 0009](../../../architecture/decisions/0009-judge-policy-and-grounding.md)).
The new package `internal/ground` has no model calls, no I/O and no logging of content.

| # | Job | Status |
| --- | --- | --- |
| 01 | [Normalization, numbers and dates](01-normalization.md) | done |
| 02 | [`Check` and schema annotations](02-check-and-annotations.md) | done |
| 03 | [Document text for grounding](03-document-text.md) | done |
