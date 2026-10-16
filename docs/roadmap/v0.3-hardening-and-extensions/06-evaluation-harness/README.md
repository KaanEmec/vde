# Epic 06: Evaluation harness

Version: v0.3 · Status: done · Depends on: v0.2

Measure extraction quality per `service_version` on a corpus with ground truth, so model,
framing and representation changes are decided on numbers. The "99% correct" aspiration
of the brief becomes a measured figure.

| # | Job | Status |
| --- | --- | --- |
| 01 | [Corpus format and generic sample corpus](01-corpus-format.md) | done |
| 02 | [`vde eval` command and report](02-eval-command.md) | done |

First real run (2026-10-16, TravelInbox corpus, LLM judge): see [docs/quality.md](../../../quality.md).
A classifier-judge run follows when epic 02 delivers it.
