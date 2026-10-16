# Job: Corpus format, TravelInbox corpus import and generic samples

Version: v0.3 · Epic: 06 Evaluation harness · Status: planned · Depends on: v0.1 06-02

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

- [ ] `testdata/eval/<case>/{document.*, page-*.png, extractor.md, judge.md,
      schema.json, expected.json, compare.yaml}`; `compare.yaml` lists pointers to
      compare and how (exact, normalized string, numeric tolerance, set equality for
      arrays, ignore).
- [ ] A converter from TravelInbox's corpus layout (`backend/testdata/corpus/`) into
      this layout, run once and re-run when the corpus grows.
- [ ] Document how consumers build their own corpus from production rejections.

## Acceptance criteria

- The 51 TravelInbox cases plus at least 10 synthetic cases in 2 other domains, each
  with ground truth, runnable offline against the fake provider for harness tests.

## Out of scope

Hosting a shared public corpus.
