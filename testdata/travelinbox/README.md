# TravelInbox assets

Co-owned test material imported from the TravelInbox repository on 2026-10-16
(TravelInbox commit `aa818b8`). Decision 11 in `docs/roadmap/open-questions.md`: freely
usable, copied here so this repository's tests and evaluation do not need a TravelInbox
checkout.

| Path | Origin in TravelInbox | Use here |
| --- | --- | --- |
| `corpus/` | `backend/testdata/corpus/` | 50 synthetic, redacted documents (`source.eml`, `source.pdf` or `source.jpg`) with `truth.json` and `notes.md` each, plus the corpus `README.md` describing provenance and redaction. `vde eval` reads it in place as the `travelinbox-*` cases of `testdata/eval` (docs/operator/evaluation.md) |
| `corpus/extractor.md`, `corpus/judge.md` | `backend/internal/extract/prompts/` | TravelInbox's extractor and judge instructions, sent as `extractor_instructions` and `judge_instructions`. `prompt_version: 5` since 2026-10-22: TravelInbox's `prompt_version: 4` (commit `96e1631`) plus a vde proposal, a "Conventions" section of invented "as printed → as expected" examples (provider brand, traveller type, place name, `trip_hint.ends_on`, cancellation price; v1.3 job 03), same in `judge.md` as reasons to reject; the rest is byte for byte version 4 |
| `corpus/schema.json` | `shared/extraction-schema.json` (TravelInbox commit `d5c36ae`) | The JSON Schema TravelInbox sends as `schema` in production, with its `x-vde-grounding` annotations; `truth.json` files validate against it. Copied 2026-10-22 in place of a link to `FromTravelApp/extraction-schema.json`, which is older and stays the contract's input |
| `corpus/compare.yaml` | written for this repository | How `vde eval` compares a result with `truth.json` |

The corpus `schema.json` carries a vde proposal for TravelInbox on top of the production copy (v1.3 job 01/02): `pattern` and `description` on rail coach and seat, flight and leg seat and terminal, `description` on cabin and `travellers[].type`; `schema_version` stays `1`, the data version of the contract.

Every corpus document is synthetic (see `corpus/README.md`). The go tool skips
directories named `testdata` when expanding `./...`, so nothing here is built.
