# v0.4 Judge policy and preprocessing

Status: done (2026-10-18, owner suite run pending; see the exit criterion check below) · Depends on: v0.3

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
| 01 | [Judge policy](01-judge-policy/README.md) | v0.3 | done |
| 02 | [Grounding check](02-grounding-check/README.md) | v0.3 | done |
| 03 | [Auto decision in the loop](03-auto-decision/README.md) | 01, 02 | done |
| 04 | [PDF preprocessing (text layer and rendering)](04-pdf-preprocessing/README.md) | v0.3 (independent) | in progress |
| 05 | [Harness, docs and closeout](05-harness-docs-closeout/README.md) | 01 to 04 | done |

Epics 01, 02 and 04 can proceed in parallel. Epic 02's PDF grounding text comes from epic 04
(job 04-03). Until that lands, PDFs are ungroundable, which is safe: `auto` sends them to the
judge.

Not in this version: OCR, HEIC conversion, a per-application role set, the owner's policy
comparison run (epic 05 leaves a placeholder for it in `docs/quality.md`).

## Exit criterion check (2026-10-18)

Automated tests prove each criterion with the fake provider. The runs on the owner suite
(`testdata/user-docs`, real models) are the owner's and are pending: the commands are in
[`docs/quality.md`](../../quality.md#judge-policy).

| # | Criterion | Result |
| --- | --- | --- |
| 1 | No invented value accepted without a judge under `auto` (owner suite and the synthetic event-invoice fixture) | Verified by tests: `TestRun_autoNeverGroundsInvented` (`internal/extract`, 48 variants over attempt limits, sample rates, a schema violation first and four judge behaviours: a candidate with an invented critical value never ends `accepted`/`grounded`), the planted-invented-date scenarios `auto-invented-after-schema-retry`, `auto-invented-last-attempt` and `auto-invented-repair-fails` in `testdata/scenarios/judge-policy` (`TestScenarios` in `cmd/vde` since v1.1; the candidate's date 2026-04-18 and organiser are not in `booking.txt`), and `TestCheck_eventInvoice` and `TestCheck_planted` (`internal/ground`, the event-invoice pattern: invented dates, venue, organiser and travellers are critical misses). Owner suite: **pending owner run**. |
| 2 | Emails skip the judge when grounded (judge calls per email case, sampled cases named) | Verified by tests: `TestServeJudgePolicies` (`cmd/vde`, end to end over HTTP with a judge that always fails: under `auto` the text document ends `accepted`/`grounded`, so no judge call was needed; `always` fails `provider_unavailable`), the `auto-grounded` and `auto-sampled-*` scenarios, `TestExtractJudgeFlag` (one provider call) and `make eval` under `auto` (the email case `receipt-006-ride-email` ends `grounded` with 0 judge calls; the 45 TravelInbox emails go to the judge because their schema derives values without `x-vde-grounding` annotations, see open items). Owner suite: **pending owner run** (`vde eval --judge auto` reports judge calls and the sampled cases per case). |
| 3 | PDFs processed without caller-supplied pages (representation per PDF case: text or pages; models used) | Verified by tests: `TestExtractPDFWithoutPages` (`booking.pdf` as text layer, rendered pages and native, under `always` and `auto`, text-only and image-only groups), `TestServeJudgePolicies` (a PDF over HTTP, `auto`: `accepted`/`grounded`), `TestPDFPreparedByService` (worker) and `TestSubmitPDFPreparedByService` (API). Owner suite, selection only (`VDE_USER_DOCS=1`, 2026-10-17, job 04-03): all 5 PDFs selected without caller pages, `text+text_layer` for a text-only group and `pages+rendered` for an image-only group, all groundable. With real models that do not read `application/pdf`: **pending owner run**. |
| 4 | Conformance suite unchanged under `always` | Verified: `make conformance` passes (contract §14 items 1 to 9). Its instance without a PDF reader runs `documents.pdf.mode: native`, which keeps contract 4.11's refusal exactly (see the [review](../../conformance/travelinbox-v1-review.md#6-open-items-for-the-review-meeting)). |
| 5 | Binary and image size after embedding pdfium (number recorded against v0.3) | `CGO_ENABLED=0`, `-trimpath -s -w`: linux/amd64 35.9 MB (v0.3.0: 26.8 MB, +9.2 MB), linux/arm64 34.2 MB (v0.3.0: 25.2 MB, +9.0 MB), darwin/arm64 34.9 MB; under the budget of v0.3 plus 20 MB. Both linux builds are static (`make cross`, also in CI). Image: not built for this release (no buildx on the build machine); it is the binary plus about 2 MB of distroless/static. |

Open after v0.4, none of which changes `/v1` under `always`:

- The owner's policy comparison (`always`, `auto`, `never` on the owner suite and the
  TravelInbox corpus) and criteria 1 to 3 on real documents and models: pending owner run,
  recorded in `docs/quality.md`.
- The TravelInbox schema has no `x-vde-grounding` annotations: its derived values (codes,
  kinds, canonical names) are critical misses, so under `auto` its emails go to the judge.
  Under `always` it takes no grounding repair (ADR 0009 amendment 2026-10-18: the repair
  needs an annotated schema; `make eval` 42 of 50 TravelInbox cases repaired before, 0
  after). Annotating
  it is TravelInbox's choice; it does not affect contract fidelity.
- The image size (`make image`) was not measured.
- pdfium has no system fonts (fallback glyphs for non-embedded fonts); about 500 MB of
  memory per `documents.pdf.workers`; interpreted (about 60 times slower) outside amd64
  and arm64.
- No OCR; HEIC not converted; no per-application role set.
- The conformance review's open points: TravelInbox to confirm it tolerates the superset
  statuses and fields, and which `documents.pdf.mode` it wants.
