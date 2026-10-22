# Job: Baseline on TravelInbox prompt_version 4

Version: v1.3 · Epic: 01 Conventions in schema and instructions · Status: planned · Depends on: v1.2 (released)

## Goal

Make vde's copy of TravelInbox's instructions the ones TravelInbox runs in production
(prompt_version 4, written for vde 1.2's framing), and record one baseline run on the five
measured cases under `auto`.

## Context

`testdata/travelinbox/corpus/extractor.md` and `judge.md` carry `prompt_version: 1`,
imported 2026-10-16 and amended in vde on 2026-10-20 (conventions stated, "Return only the
JSON object" lines removed). TravelInbox has since moved to prompt_version 4 (TravelInbox
commit `96e1631`, `backend/internal/extract/prompts/`). A baseline on vde's older copy
would measure a prompt nobody runs. The schema copy is a symbolic link to
`FromTravelApp/extraction-schema.json` (schema_version 1), which is what TravelInbox sends
today; it stays a link in this job.

The vde amendments of 2026-10-20 are not lost: job 03 re-applies whatever of them
prompt_version 4 still lacks, as examples or rules, measured.

## Tasks

- [ ] Compare TravelInbox's prompt_version 4 files with vde's copy and list, in the job's
      notes below, which of vde's 2026-10-20 amendments prompt_version 4 already covers and
      which it does not (by topic, no document content).
- [ ] Replace `testdata/travelinbox/corpus/extractor.md` and `judge.md` with the
      prompt_version 4 files, byte for byte; check that `shared/extraction-schema.json` in
      TravelInbox equals `FromTravelApp/extraction-schema.json` and note it.
- [ ] Update `testdata/travelinbox/README.md` (origin commit, date, that the copy is now
      prompt_version 4 unamended).
- [ ] `make eval` (fake provider) still passes; update its expected reports only where the
      instruction text is the cause.
- [ ] Record the baseline: one run of the "Measuring" command of the version README, saved
      to `docs/quality/` with a v1.3 section in [docs/quality.md](../../../quality.md)
      listing per case the mismatched pointers, judge calls and tokens.

## Acceptance criteria

- vde's copy equals TravelInbox's prompt_version 4 byte for byte.
- The baseline report is recorded with per-case mismatched pointers and judge calls.
- `make check` and `make eval` pass.

## Out of scope

Changing the instructions or the schema (jobs 02 and 03).
