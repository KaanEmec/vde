# TravelInbox assets

Co-owned test material imported from the TravelInbox repository on 2026-10-16
(TravelInbox commit `aa818b8`). Decision 11 in `docs/roadmap/open-questions.md`: freely
usable, copied here so this repository's tests and evaluation do not need a TravelInbox
checkout.

| Path | Origin in TravelInbox | Use here |
| --- | --- | --- |
| `corpus/` | `backend/testdata/corpus/` | 50 synthetic, redacted documents (`source.eml`, `source.pdf` or `source.jpg`) with `truth.json` and `notes.md` each, plus the corpus `README.md` describing provenance and redaction |
| `prompts/extractor.md`, `prompts/judge.md` | `backend/internal/extract/prompts/` | TravelInbox's extractor and judge instructions, sent as `extractor_instructions` and `judge_instructions` |
| `extraction-schema.json` | `FromTravelApp/extraction-schema.json` in this repository (identical to TravelInbox `shared/extraction-schema.json`) | The JSON Schema TravelInbox sends as `schema`; `truth.json` files validate against it |
| `reference-stub/service_test.go.txt` | `backend/internal/extract/service_test.go` | TravelInbox's tests against its stub of this service, kept for study only. Renamed to `.txt` so it is never compiled; it imports TravelInbox packages |

Every corpus document is synthetic (see `corpus/README.md`). The go tool skips
directories named `testdata` when expanding `./...`, so nothing here is built.
