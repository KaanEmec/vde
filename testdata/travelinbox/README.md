# TravelInbox assets

Co-owned test material imported from the TravelInbox repository on 2026-10-16
(TravelInbox commit `aa818b8`). Decision 11 in `docs/roadmap/open-questions.md`: freely
usable, copied here so this repository's tests and evaluation do not need a TravelInbox
checkout.

| Path | Origin in TravelInbox | Use here |
| --- | --- | --- |
| `corpus/` | `backend/testdata/corpus/` | 50 synthetic, redacted documents (`source.eml`, `source.pdf` or `source.jpg`) with `truth.json` and `notes.md` each, plus the corpus `README.md` describing provenance and redaction. `vde eval` reads it in place as the `travelinbox-*` cases of `testdata/eval` (docs/operator/evaluation.md) |
| `corpus/extractor.md`, `corpus/judge.md` | `backend/internal/extract/prompts/` | TravelInbox's extractor and judge instructions, sent as `extractor_instructions` and `judge_instructions`. Amended here on 2026-10-20 to state the labelling conventions of `corpus/README.md` that they contradicted or left out (`ends_on` for one-way documents, place names without codes, provider name form, traveller type), and without the "Return only the JSON object" lines that contradict vde's
answer blocks; to be carried into TravelInbox, see `docs/conformance/travelinbox-schema-suggestions.md` |
| `corpus/schema.json` | a symbolic link to `FromTravelApp/extraction-schema.json` (identical to TravelInbox `shared/extraction-schema.json`) | The JSON Schema TravelInbox sends as `schema`; `truth.json` files validate against it |
| `corpus/compare.yaml` | written for this repository | How `vde eval` compares a result with `truth.json` |

Every corpus document is synthetic (see `corpus/README.md`). The go tool skips
directories named `testdata` when expanding `./...`, so nothing here is built.
