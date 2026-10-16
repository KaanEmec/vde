# Extraction quality

Measured with [`vde eval`](operator/evaluation.md) on the corpus in `testdata/eval`. Each
run is keyed by `service_version` (release plus role configuration), the models behind the
roles and the date. Real runs are manual, before releases; CI runs only the mechanics
(`make eval`, fake provider). Numbers only: no document content.

**False accepts** (accepted, but a compared field differs from the ground truth) are the
headline: the judge exists to keep them at zero. Rates are fractions of the accepted
cases (false accepts) and of all cases (acceptance). Field accuracy is matched / compared
fields under each case's `compare.yaml`.

## Runs

| Date | service_version | Extractor / judge model | Cases | False accepts | Acceptance | Field accuracy | Correct cases | Attempts (mean) | Duration median / p95 | Tokens in / out | Report |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 2026-10-16 | `vde-0.0.0-dev+56cf97c8` | `google/gemini-3.8-flash` / same (LLM judge), OpenAI-compatible endpoint | `travelinbox-*`, 50 | **43 of 50 (86.0%)** | 100.0% | 92.4% | 7 of 50 | 1.00 | 21.1 s / 43.5 s | 942,298 / 143,442 (100 calls) | [JSON](quality/vde-0.0.0-dev+56cf97c8-2026-10-16.json) |

### 2026-10-16, `vde-0.0.0-dev+56cf97c8`

Local engine (`--config`, development build), concurrency 2, 50 TravelInbox cases, no
harness errors, 0 rejected, 0 failed, so no false rejects could occur. Every case was
accepted on the first attempt; 43 of them differ from the ground truth on at least one
compared field.

Mismatched fields by pointer (all cases, array indexes folded), the top ones:

| Pointer | Cases |
| --- | --- |
| `/trip_hint/ends_on` | 23 |
| `/reservations/*/travellers` | 16 |
| `/reservations/*/end_place/name` | 14 |
| `/reservations/*/provider` | 14 |
| `/reservations/*/start_place/name` | 10 |
| `/reservations/*/details/number` | 9 |
| `/reservations/*/details/terminal` | 5 |
| others (city, iata, address, cancellation, room, cabin, ...) | 1 to 3 each |

Likely reading (from the pointers alone; values were not inspected): the top mismatches
sit where the corpus labelling conventions are stricter than what the model returns and
the judge checks: `ends_on` null for one-way documents, the
place `name` null when only a city and code are printed, traveller `type` only when stated,
the operator rather than the OTA as `provider`, the flight number format. The judge checks
the candidate against the document, apparently not against these conventions, so it
accepts them.
The comparison rules are not loosened to hide this. The next steps are the TravelInbox
instructions (state the conventions explicitly to both roles) and a run with the
classifier judge once it is available (epic 02).
