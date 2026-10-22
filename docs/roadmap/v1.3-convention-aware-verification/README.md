# v1.3 Convention-aware verification

Status: done, released as `v1.3.0` (2026-10-22; 02-01 dropped after measurement) · Depends on: v1.2 (released)

## Goal

Close the misses that remain after v1.2 on TravelInbox's documents. Most of them are not
reading errors but conventions: the model copies a value as printed (`WIZZ`, `Wg. 7`,
`Terminal 1`, `COURTYARD by Marriott`) where TravelInbox expects a convention form (`Wizz
Air`, `7`, `1`, `Marriott`). Grounding finds those values in the document and accepts
them under `auto`, and a judge has no rule to reject them. v1.3 writes the conventions
down where models and checks can use them (schema, examples), makes grounding stop
vouching for verbatim copies of convention fields, and fixes two operational gaps
TravelInbox ran into: a judge model whose answers cannot be parsed, and failed jobs whose
error code is gone after `retention: none`.

Motivation: TravelInbox's model comparison of 2026-10-20 on ten corpus documents (nine
extractor and judge pairs, vde 1.2.0). Production (Haiku 5.5 extracts, GLM 5.3 Flash
judges, `auto`) got 103 of 120 booked fields right. Of its 17 misses, 14 were in
`details`, `provider` and `travellers`, values printed in the document in another form
than the expected one; 3 were a wrong choice between two printed values (fee against
total). Changing only the judge moved the score by at most 3 fields, within single-run
noise. Mistral Small 4 failed on every job sent to it as a judge, and 12 failed jobs had
an empty error after TravelInbox read them. Options and the assessment behind this
selection: ideas 1, 2, 5, 8 and 9 of the 2026-10-22 judge discussion (owner decision).

Ownership: TravelInbox owns its schema and instructions
(`backend/internal/extract/prompts/`, `shared/extraction-schema.json`; their roadmap job
`v1.0-launch/04-vde/06-extraction-quality`). vde owns the co-owned copies in
`testdata/travelinbox/corpus/`, measures proposals there and hands them over (epic 01).
Agents never edit the TravelInbox repository or `FromTravelApp/`.

## Exit criterion

On the measurement set below, with the owner's default models and `--judge auto`:

1. At least 3 fewer field mismatches in total than the epic 01 baseline (one run each),
   and no case with more mismatches than at the baseline.
2. Mismatches at `provider`, `travellers` and `details` pointers at most half of the
   baseline count.
3. The judge calls per run and their cost are reported for every step; any increase from
   epic 02 job 01 is stated next to the fields it fixed.
4. `vde config check --probe` reports, per endpoint-model pair and role, whether the
   model's answer parses, and the Mistral Small 4 judge failure is explained.
5. A failed job's error code can be read by job id after `retention: none` deleted the
   job (test with the fake provider).
6. `make check`, `make scenarios`, `make conformance` and `make eval` pass; conformance
   unchanged.

## Exit criterion check (2026-10-22)

| # | Criterion | Result |
| --- | --- | --- |
| 1 | At least 3 fewer mismatches, no case worse | 20 → 7 mismatches. Not fully met: `021-hotel` has 2 against 1 at baseline in the final-state run (its `provider` miss fixed, a `travellers` and a `cancellation` miss in its place; 1 in the 02-01 run, so within single-run noise) |
| 2 | Convention mismatches at most half | 11 → 4 (`details` 5 → 0, `provider` 2 → 1, `travellers` 4 → 3) |
| 3 | Judge calls and cost reported | Every step in [docs/quality.md](../../quality.md#v13-convention-aware-verification-2026-10-22); 02-01 sent 5 of 5 jobs to the judge for no changed outcome and was reverted |
| 4 | Probe reports per pair; Mistral explained | Probe met (3 of 3 of the owner's pairs `ok` on real models). Mistral not explained: no key for TravelInbox's endpoint; TravelInbox can run the probe |
| 5 | Failure code by job id after `retention: none` | Met (e2e test with the fake provider) |
| 6 | Checks pass, conformance unchanged | Met: `make check`, `make scenarios`, `make conformance`, `make eval` |

## Measuring

Real-model runs cost money, so every run uses the same five TravelInbox cases, named
explicitly, one run per job, `--concurrency 1`, `--judge auto` (TravelInbox's production
policy):

```
set -a; . testdata/user-docs/.env; set +a
go run ./cmd/vde eval --corpus testdata/eval --config testdata/user-docs/vde.nous.yaml --judge auto --concurrency 1 --cases travelinbox-001-flight,travelinbox-011-flight,travelinbox-021-hotel,travelinbox-041-rail,travelinbox-046-cancellation
```

| Case | Why it is in the set |
| --- | --- |
| `travelinbox-001-flight` | `details`: cabin with tariff, terminal with prefix |
| `travelinbox-011-flight` | `provider`: brand printed in capitals (`WIZZ`) |
| `travelinbox-021-hotel` | `provider`: sub-brand; `travellers`: party count typed onto a named traveller |
| `travelinbox-041-rail` | `provider`: operating company; `details`: coach and seats with abbreviations |
| `travelinbox-046-cancellation` | Two printed amounts (fee and original total): the control for what conventions cannot fix |

Each job compares with the report before it (`--compare`), records the report in
`docs/quality/` with a row in [docs/quality.md](../../quality.md), and keeps or reverts its
change. One run gives a signal: a difference of one or two fields is noise unless a
mismatch at a pointer the change targets appears or disappears. vde's eval sends each
`source.eml` as one email job, while TravelInbox sends the HTML body and each PDF as
separate jobs, so numbers are comparable between v1.3 runs, not with TravelInbox's report.

## Epics

| # | Epic | Depends on | Status |
| --- | --- | --- | --- |
| 01 | [Conventions in schema and instructions](01-conventions/README.md) | v1.2 | done |
| 02 | [Verification and operations](02-verification-and-operations/README.md) | 02-01 on epic 01; 02-02 and 02-03 none | done (01 dropped) |

Epic 02 jobs 02 and 03 touch other files than everything else and can run in parallel with
epic 01. Job 02-01 needs epic 01's schema copy and instructions, since the judge it sends
jobs to needs the written conventions to reject anything.

Not in this version (see the [backlog](../backlog.md)): a reasoning-effort setting per model
(TravelInbox question 1), one job for an email and its attachments (question 5), the
devil's advocate judge, schema invariants (sums, ordering), and asking the user between
two values. Timeout guidance for reasoning models (question 4) goes into the reply of
epic 01 job 03 as advice, without code.
