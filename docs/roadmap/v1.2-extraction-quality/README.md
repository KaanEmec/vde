# v1.2 Extraction quality

Status: done (2026-10-20, not released; jobs 07 and 09 dropped) · Depends on: v1.1

## Goal

Make the extractor follow the caller's structure and instructions more reliably on
complicated, unstructured documents, and make the judge's feedback easier to act on. The
work changes two things: the framings (what the models are asked and how they answer)
and the representation (how an email, an HTML page or a PDF is laid out as text before a
model reads it). Neither is API under the [compatibility policy](../../compatibility.md):
both change `service_version`, not `/v1` or the configuration schema. A new configuration
key arrives with a default that keeps the previous behaviour.

Motivation: real runs with `anthropic/claude-haiku-5.5` (the owner's default extractor)
still gave results that were confusing to read. The framings ask for "exactly one JSON
value and nothing else", put the document after the instructions, and leave
"null or leave the field out" and "take every value from the document" against
"normalise as instructed" to the model. Email bodies, HTML tables and PDF text reach the
model with less structure than the source had. The research behind each job, with
sources, is summarized in the epic README.

Contract (owner decision 2026-10-20): the document-first order (job 01) and the answer
blocks before the JSON (jobs 05 and 06) deviate from TravelInbox contract §6 and §7. They
become the default anyway, recorded in [ADR 0012](../../architecture/decisions/0012-evidence-first-framing.md) (0011 was
taken), and the owner proposes the matching
amendment to TravelInbox. Everything else in v1.2 stays within the contract.

## Exit criterion

On the epic's core set of four owner documents (two emails, two PDFs; see the epic
README, "Measuring"), with the owner's default configuration and `--judge always`. Every
real-model eval run in this version uses 3 to 5 documents, never more, to keep provider
cost small:

1. At least as many cases fully correct as the v1.1.0 baseline, and no case that was fully
   correct at the baseline is worse.
2. Fewer field mismatches in total than the baseline (the report's per-pointer counts).
3. `make conformance` and `make scenarios` pass (conformance changed only where ADR 0012
   says); `make eval` (fake provider) passes, with its expected reports updated only where a framing or representation change
   is the cause.

The baseline and every comparison report are recorded in `docs/quality/` with a row in
[docs/quality.md](../../quality.md). Reports carry numbers only.

Exit criterion check (2026-10-20): met on the core set. Baseline 3 of 4 fully correct with
one false accept and one field mismatch; final state (job 06) 4 of 4, no false accept, no
mismatch, 1.25 attempts instead of 1.5. `make check` (with conformance), `make scenarios`
and `make eval` pass. Runs and decisions: [docs/quality.md](../../quality.md#v12-prompts-and-document-preparation-2026-10-20).

## Epics

| # | Epic | Depends on | Status |
| --- | --- | --- | --- |
| 01 | [Prompts and document preparation](01-prompts-and-preparation/README.md) | v1.1 (released) | done |

v1.2 starts after `v1.1.0`: v1.1's exit criterion is identical eval results, and every job
here changes them on purpose. Several v1.1 jobs edit the same files
(`internal/extract`, `internal/document`, the provider adapters, `testdata/scenarios`,
`docs/api/writing-instructions.md`).

Not in this version: OCR, layout models (Docling, MinerU, Marker), hybrid PDF input (text
layer and page images together) and per-page representation decisions. The last two
amend ADR 0010 and are in the [backlog](../backlog.md).
