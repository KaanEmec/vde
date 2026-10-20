# Extraction quality

Measured with [`vde eval`](operator/evaluation.md) on the corpus in `testdata/eval`. Each
run is keyed by `service_version` (release plus role configuration), the models behind the
roles and the date. Real runs are manual, before releases; `make ci` runs only the
mechanics (`make eval`, fake provider). Numbers only: no document content.

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

## Model comparison on the TravelInbox corpus (2026-10-16)

Same 50 cases, same instructions and schema, OpenAI-compatible adapter via Nous Research,
concurrency 3. Reports in `docs/quality/`.

| Run | Extractor | Judge | Accepted | Rejected | Failed | False accepts (of accepted) | Field accuracy (accepted) | Correct cases | Median / p95 | Tokens in / out |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 1 ([`+56cf97c8`](quality/vde-0.0.0-dev+56cf97c8-2026-10-16.json)) | gemini-3.8-flash | gemini-3.8-flash | 50 | 0 | 0 | 43 (86.0 %) | 92.4 % | 7 | 21.1 s / 43.5 s | 942k / 143k |
| 2 ([`+2b75349f`](quality/vde-0.0.0-dev+2b75349f-2026-10-16.json)) | gemini-3.8-flash | claude-sonnet-5.5 | 45 | 4 | 1 | 40 (88.9 %) | 92.8 % | 5 | 19.5 s / 98.7 s | 1197k / 144k |
| 3 ([`+1a2f4dfb`](quality/vde-0.0.0-dev+1a2f4dfb-2026-10-16.json)) | claude-sonnet-5.5 | claude-sonnet-5.5 | 41 | 0 | 9 | 33 (80.5 %) | 87.2 % | 9 | 8.9 s / 51.1 s | 1063k / 46k |

Mismatched fields, counted once per case (runs 1 → 3): `/trip_hint/ends_on` 23 → 20,
`/reservations/N/travellers` 16 → 6, `/reservations/N/provider` 12 → 16,
`/reservations/N/end_place/name` 13 → 13, `/reservations/N/start_place/name` 9 → 11.

### Reading

- **The judge mechanism works; the judge's leniency is a model-and-instructions matter.**
  The Sonnet judge rejected 4 candidates the Gemini judge had accepted, but the false-accept
  share barely moved because most mismatches are not document errors.
- **Most "false accepts" are convention disagreements between the ground truth and the
  instructions.** Checked on one rail case: the ground truth leaves `trip_hint/ends_on`
  null while the instructions define it as "the latest start or end date of any
  reservation", which the extractor returned; the ground truth leaves `travellers/N/type`
  null while the extractor wrote `adult`. A document-grounded judge is right not to reject
  either. Ignoring `ends_on` and `travellers` alone removes roughly 40 % of the mismatches.
- **A stronger extractor follows the instructions more literally** (travellers 16 → 6) and
  answers four times faster, but the remaining mismatches (provider and place names) grew,
  which again points at naming conventions (operator vs platform, printed vs canonical
  station names) that the instructions leave open.
- **Run 3's nine failures** finished in one to two seconds with zero tokens, consistent with
  provider-side rate limiting at concurrency 3; they are not extraction failures. The
  harness report does not yet record the job `error.code` per case (follow-up).

### Recommendation

1. Align the TravelInbox instructions and `compare.yaml` on `trip_hint/ends_on`,
   `travellers/N/type`, provider naming and place-name canonicalization; re-run.
2. Keep measuring model choices with this harness rather than assuming; with aligned
   conventions, compare `claude-haiku-5.5` and `claude-sonnet-5.5` as extractor against
   `gemini-3.8-flash` on cost per correct case.
3. Lower eval concurrency to 2 for Anthropic models through this endpoint, or add
   `Retry-After` handling on the eval client side, to avoid the failed cases.

## v1.2 prompts and document preparation (2026-10-20)

One real-model run per job of [v1.2 epic 01](roadmap/v1.2-extraction-quality/01-prompts-and-preparation/README.md),
each built from that job's commit and compared with the run before it. Every run uses the
same four owner documents (`owner-01-flight` and `owner-03-lodging`, emails;
`owner-02-flight` and `owner-10-event`, PDFs), `--judge always`, extractor
`anthropic/claude-haiku-5.5` with `z-ai/glm-5.3-flash` as failover, judge
`z-ai/glm-5.3-flash`, through the owner's OpenAI-compatible endpoint. Rows are in the
order the jobs landed on `main`. Four documents give a signal, not a statistic: a single
field that differs between runs with no visible cause is read as noise.

| Step | service_version | Correct cases | False accepts | Field accuracy | Attempts (mean) | Tokens in / out | Decision | Report |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| Baseline (`v1.1.0`) | `vde-0.0.0-dev+d9f9cfb4` | 3 of 4 | 1 | 98.9% | 1.5 | 86778 / 18978 | - | [JSON](quality/vde-0.0.0-dev+d9f9cfb4-2026-10-20.json) |
| 02 context line, page markers | `vde-0.0.0-dev+1a5e36d9` | 4 of 4 | 0 | 100% | 1.5 | 87085 / 17724 | kept | [JSON](quality/vde-0.0.0-dev+1a5e36d9-2026-10-20.json) |
| 03 email section labels | `vde-0.0.0-dev+7bea13af` | 3 of 4 | 1 | 98.9% | 1.5 | 82577 / 21733 | kept (miss on one derived field; did not reproduce in the next run) | [JSON](quality/vde-0.0.0-dev+7bea13af-2026-10-20.json) |
| 04 HTML data tables | `vde-0.0.0-dev+4741893c` | 4 of 4 | 0 | 100% | 1.25 | 68885 / 13968 | kept | [JSON](quality/vde-0.0.0-dev+4741893c-2026-10-20.json) |
| 01 prompt order and wording | `vde-0.0.0-dev+5779adaf` | 3 of 4 | 1 | 98.9% | 1.25 | 69630 / 14863 | kept (the `travellers` miss flips between runs) | [JSON](quality/vde-0.0.0-dev+5779adaf-2026-10-20.json) |
| 05 extractor analysis block | `vde-0.0.0-dev+267a1b59` | 4 of 4 | 0 | 100% | 1.5 | 81912 / 26125 | kept | [JSON](quality/vde-0.0.0-dev+267a1b59-2026-10-20.json) |
| 06 judge evidence quotes | `vde-0.0.0-dev+f4214f03` | 4 of 4 | 0 | 100% | 1.25 | 70696 / 18367 | kept: final state | [JSON](quality/vde-0.0.0-dev+f4214f03-2026-10-20.json) |
| 07 structured output on (Haiku) | `vde-0.0.0-dev+7963ce7e` | 3 of 4 | 1 | 98.9% | 1.5 | 89296 / 31446 | reverted | [JSON](quality/vde-0.0.0-dev+7963ce7e-2026-10-20.json) |

Reading: the one recurring miss is `/reservations/0/travellers` on the event invoice,
accepted by the judge at baseline. It came and went across the runs without an analysis
block, and stayed right once the extractor quoted its evidence first (05, 06). With native
structured outputs the answer has no room for that analysis and the miss came back, with
more attempts and tokens, so job 07 was reverted
([structured-outputs.md](providers/structured-outputs.md)). Final state against baseline:
4 of 4 instead of 3 of 4, no false accept, 1.25 attempts instead of 1.5, 19% fewer input
and 3% fewer output tokens. No large-schema or multi-item case is left failing, so job 09
(multi-pass extraction) was dropped.

### Wider check: ten more documents (2026-10-20)

On the owner's request, ten documents outside the core set, each run once on `v1.1.0` and
once on v1.2 (`main`, same models and settings as above): the other six owner cases and
four TravelInbox cases chosen for coverage (a hotel PDF, a rail email with tables, a
three-booking email, a ticket photo).

| Documents | Build | Correct cases | False accepts | Failed | Field accuracy | Attempts (mean) | Tokens in / out | Report |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 6 owner (04 to 09) | `v1.1.0` | 2 of 6 | 3 | 1 | 95.1% | 1.33 | 129551 / 17118 | [JSON](quality/vde-0.0.0-dev+d9f9cfb4-2026-10-20-wider-owner.json) |
| 6 owner (04 to 09) | v1.2 (`main`) | 2 of 6 | 4 | 0 | 95.1% | 1.33 | 165356 / 22172 | [JSON](quality/vde-0.0.0-dev+f4214f03-2026-10-20-wider-owner.json) |
| 4 TravelInbox (030, 041, 048, 049) | `v1.1.0` | 0 of 4 | 4 | 0 | 69.5% | 1.00 | 69146 / 20302 | [JSON](quality/vde-0.0.0-dev+d9f9cfb4-2026-10-20-wider-ti.json) |
| 4 TravelInbox (030, 041, 048, 049) | v1.2 (`main`) | 0 of 4 | 4 | 0 | 86.6% | 1.00 | 72114 / 24048 | [JSON](quality/vde-0.0.0-dev+f4214f03-2026-10-20-wider-ti.json) |

Reading:

- The gain is the multi-item case: on the three-booking email v1.1.0 got the first booking
  only (40 of 82 fields), v1.2 all three (71 of 82). The other TravelInbox cases moved by
  one field each, either way.
- The owner job that failed on `v1.1.0` (`owner-09-activity`) completes on v1.2, with one
  field wrong; that is the extra false accept.
- No case got more correct or less correct otherwise. What is left wrong is almost all
  the same few pointers: `/reservations/*/provider`, `/reservations/*/travellers`,
  `start_place/name` and `end_place/name`, and `/trip_hint/ends_on`. These are the top
  misses of the TravelInbox run of 2026-10-16 too: canonical names and derived values,
  which the judge (`z-ai/glm-5.3-flash`) accepts. They are a judge and convention problem
  that v1.2's changes did not target.
- Cost: about 20% more tokens on these ten (analysis blocks and the run that no longer
  fails).

## Judge policy

Not run yet: the owner runs this comparison.

v0.4 lets a deployment choose when the judge is asked (`always`, `never`, `auto`) and adds
the deterministic grounding check ([judge policy](operator/judge-policy.md),
[ADR 0009](architecture/decisions/0009-judge-policy-and-grounding.md)). ADR 0008 asks that a
judge arrangement be adopted on measured runs; this is the measurement plan.

**Corpora.** The owner suite (`testdata/user-docs`, ten real documents, untracked) and the
TravelInbox corpus (`travelinbox-*` in `testdata/eval`). **Models.** The same extractor and
judge groups under all three policies, so only the policy changes. **Commands** (the owner
suite; the reports land in `eval-reports/`, one `service_version` per policy):

```
go build -o bin/vde ./cmd/vde
go run ./cmd/vde eval --corpus testdata/user-docs/eval --config testdata/user-docs/vde.nous.yaml --judge always
go run ./cmd/vde eval --corpus testdata/user-docs/eval --config testdata/user-docs/vde.nous.yaml --judge auto \
  --compare eval-reports/<the always report>.json
go run ./cmd/vde eval --corpus testdata/user-docs/eval --config testdata/user-docs/vde.nous.yaml --judge never \
  --compare eval-reports/<the always report>.json
VDE_USER_DOCS=1 go test ./internal/document -run UserDocs -v   # PDFs: representation per file, no caller pages
```

and the same three runs with `--corpus testdata/eval --cases 'travelinbox-*'`.

**Columns to record** (one row per corpus and policy):

| Corpus | Policy | service_version | Extractor / judge | False accepts: judge / grounded | Verified judge / grounded (grounded share) | Extracted | Judge calls | Sampled / disagreements | Field accuracy | Tokens in / out | Cost per correct case | Report |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| owner suite | `always` | pending | pending | pending | pending | pending | pending | pending | pending | pending | pending | pending |
| owner suite | `auto` | pending | pending | pending | pending | pending | pending | pending | pending | pending | pending | pending |
| owner suite | `never` | pending | pending | pending | pending | pending | pending | pending | pending | pending | pending | pending |
| TravelInbox | `always` | pending | pending | pending | pending | pending | pending | pending | pending | pending | pending | pending |
| TravelInbox | `auto` | pending | pending | pending | pending | pending | pending | pending | pending | pending | pending | pending |
| TravelInbox | `never` | pending | pending | pending | pending | pending | pending | pending | pending | pending | pending | pending |

**What decides it.** v0.4's exit criterion: under `auto`, no case with
`verification: grounded` is a false accept caused by an invented value (the event invoice
must never be `accepted`/`grounded`), the email cases end grounded without a judge call
except the sampled ones, and every PDF runs from its bytes alone. Then false accepts by
verification, judge calls saved and cost per correct case. A grounded false accept caused
by a printed value in the wrong field is the risk `auto` accepts; it is weighed against the
judge calls saved. Cost per correct case is computed by hand from the token counts and the
providers' prices until the report carries prices.

The per-case `error_code` follow-up of the 2026-10-16 run is done: the report records the
job error code per case.
