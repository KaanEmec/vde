# Job: Corpus format, TravelInbox corpus import and generic samples

Version: v0.3 · Epic: 06 Evaluation harness · Status: done · Depends on: v0.1 06-02

## Goal

A simple on-disk layout for evaluation cases (document, optional pages, instructions,
schema, expected result, field-level comparison rules), the TravelInbox corpus (51
documents with ground truth, co-owned, decision 11) converted into it, and a small
synthetic set in two other domains (a receipt, a lab report) so the harness is not
travel-only.

## Context

Brief: "'99% correct' is an aspiration to measure on representative documents." Contract
§14 (TravelInbox's own corpus and `make eval`; decision 11: co-owned, freely reused).

## Tasks

- [x] `testdata/eval/<case>/{document.*, page-*.png, extractor.md, judge.md,
      schema.json, expected.json, compare.yaml}`; `compare.yaml` lists pointers to
      compare and how (exact, normalized string, numeric tolerance, set equality for
      arrays, ignore).
- [x] A converter from TravelInbox's corpus layout (`backend/testdata/corpus/`) into
      this layout, run once and re-run when the corpus grows.
- [x] Document how consumers build their own corpus from production rejections.

## Acceptance criteria

- The 51 TravelInbox cases plus at least 10 synthetic cases in 2 other domains, each
  with ground truth, runnable offline against the fake provider for harness tests.

## Out of scope

Hosting a shared public corpus.

## Notes and deviations

- Layout and rules: `internal/eval` (`LoadCorpus`, `ParseRules`, `Rules.Compare`), documented
  in [docs/operator/evaluation.md](../../../operator/evaluation.md). Pointers may use `*` for
  any index or key; the most specific rule wins. A key absent from the result counts as
  null; keys only the result has are not compared (the schema governs shape); a case
  without a result matches no field.
- The TravelInbox corpus has **50** cases, not 51: `testdata/travelinbox/corpus` (imported
  at TravelInbox commit `aa818b8`) holds 50 and its README counts 50.
- Converter: `vde eval import-travelinbox --from <dir> --to testdata/eval`, accepting both
  `testdata/travelinbox` and a TravelInbox checkout (`backend/testdata/corpus`,
  `backend/internal/extract/prompts`, `shared/extraction-schema.json`). Cases are
  `travelinbox-<nnn>-<kind>`, with `notes.md` kept. The shared prompts and schema are copied
  into every case (the layout is one self-contained directory per case), about 1.4 MB of
  duplication; `TestImportTravelInbox` fails when the committed cases differ from a fresh
  conversion, so they cannot drift. None of the cases has page images.
- TravelInbox `compare.yaml`: title, notes and confidence ignored (free text and the
  extractor's own estimate; the corpus README compares title and notes "loosely", which
  has no mechanical equivalent), cancellation, place names and addresses, room, seat and
  destination normalized, travellers a set with normalized elements, price amount numeric
  (0.005). Everything else exact.
- Synthetic cases: 6 receipts (`receipt-*`: text, Markdown, HTML, email; USD, EUR, CAD,
  AUD; German formats) and 5 laboratory reports (`lab-*`: text, Markdown, HTML;
  qualitative results; German formats), with their own schemas and instructions. Merchants,
  laboratories and patients are invented. `TestCorpus` validates every `expected.json`
  against its schema.
- The corpus runs offline with the fake provider: `make eval` (job 02).
