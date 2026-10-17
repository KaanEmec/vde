# v0.4 Judge policy and preprocessing

Status: in progress (planned 2026-10-17) · Depends on: v0.3

## Goal

Let a deployment decide per application whether a judge is needed. Add a deterministic
grounding check that catches invented values, which no tested judge caught. Let the
service prepare PDFs itself (a text layer, rendered pages) so cheap models that do not read
PDFs natively can be used without the caller sending pages. The TravelInbox contract stays
exact under the default policy (`always`). The other policies are opt-in supersets with a
new status `extracted` and a `verification` field.

Motivation: the owner's runs on ten real documents (2026-10-17). Cheap models reached 8 of
10 fully correct at about 0.6 cents per document. Every judge accepted an event invoice with
invented dates, venue, organiser and travellers. The judge is the most expensive step,
emails were right on the first attempt, and PDFs had to be rasterized by hand for models
without native PDF input. Decisions: [ADR 0009](../../architecture/decisions/0009-judge-policy-and-grounding.md)
(judge policy and grounding) and [ADR 0010](../../architecture/decisions/0010-pdf-preprocessing-in-service.md)
(PDF preprocessing in the service, amends ADR 0006).

## Exit criterion

On the owner suite (`testdata/user-docs`, ten documents) under `judge.policy: auto`:

1. No invented value is accepted without a judge: every job with `verification: grounded`
   has all its critical leaves in the document text, and the event-invoice pattern is never
   `accepted` with `verification: grounded`.
2. Emails skip the judge when grounded: the email cases end `accepted` with
   `verification: grounded` and no judge call, except the deterministically sampled ones.
3. PDFs are processed without caller-supplied pages: with an extractor and judge that do not
   read `application/pdf`, every PDF of the suite runs from the bytes alone (text or
   rendered pages), and no hand-rasterized copy is used.

The TravelInbox conformance suite (`test/conformance`) passes unchanged under the default
`always`.

## Epics

| # | Epic | Depends on | Status |
| --- | --- | --- | --- |
| 01 | [Judge policy](01-judge-policy/README.md) | v0.3 | planned |
| 02 | [Grounding check](02-grounding-check/README.md) | v0.3 | planned |
| 03 | [Auto decision in the loop](03-auto-decision/README.md) | 01, 02 | planned |
| 04 | [PDF preprocessing (text layer and rendering)](04-pdf-preprocessing/README.md) | v0.3 (independent) | planned |
| 05 | [Harness, docs and closeout](05-harness-docs-closeout/README.md) | 01 to 04 | planned |

Epics 01, 02 and 04 can proceed in parallel. Epic 02's PDF grounding text comes from epic 04
(job 04-03). Until that lands, PDFs are ungroundable, which is safe: `auto` sends them to the
judge.

Not in this version: OCR, HEIC conversion, a per-application role set, the owner's policy
comparison run (epic 05 leaves a placeholder for it in `docs/quality.md`).

## Exit criterion check (template, filled at closeout)

| # | Criterion | Result |
| --- | --- | --- |
| 1 | No invented value accepted without a judge under `auto` (owner suite and the synthetic event-invoice fixture) | |
| 2 | Emails skip the judge when grounded (judge calls per email case, sampled cases named) | |
| 3 | PDFs processed without caller-supplied pages (representation per PDF case: text or pages; models used) | |
| 4 | Conformance suite unchanged under `always` | |
| 5 | Binary and image size after embedding pdfium (number recorded against v0.3) | |

Open after v0.4: (filled at closeout)
