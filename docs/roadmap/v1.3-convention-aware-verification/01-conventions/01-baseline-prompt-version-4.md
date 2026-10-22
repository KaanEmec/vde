# Job: Baseline on TravelInbox prompt_version 4

Version: v1.3 · Epic: 01 Conventions in schema and instructions · Status: done · Depends on: v1.2 (released)

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

- [x] Compare TravelInbox's prompt_version 4 files with vde's copy and list, in the job's
      notes below, which of vde's 2026-10-20 amendments prompt_version 4 already covers and
      which it does not (by topic, no document content).
- [x] Replace `testdata/travelinbox/corpus/extractor.md` and `judge.md` with the
      prompt_version 4 files, byte for byte; check that `shared/extraction-schema.json` in
      TravelInbox equals `FromTravelApp/extraction-schema.json` and note it.
- [x] Update `testdata/travelinbox/README.md` (origin commit, date, that the copy is now
      prompt_version 4 unamended).
- [x] `make eval` (fake provider) still passes; update its expected reports only where the
      instruction text is the cause.
- [x] Record the baseline: one run of the "Measuring" command of the version README, saved
      to `docs/quality/` with a v1.3 section in [docs/quality.md](../../../quality.md)
      listing per case the mismatched pointers, judge calls and tokens.

## Notes

Comparison of prompt_version 4 (TravelInbox `96e1631`) with vde's 2026-10-20 amendments to the
prompt_version 1 copy (topics only):

- Already covered by prompt_version 4: removal of the "Return only the JSON object" lines
  (version 4 has none). Nothing else of the amendments is stated in version 4.
- Lacking in prompt_version 4, both extractor and judge: `trip_hint.ends_on` null for a
  document holding only one-way journeys (version 4 says only "`trip_hint` comes from this
  document alone"); place `name` without codes and null when only a city and a code are
  printed; `provider` as short brand name without legal form, loyalty programme or web
  address, and the operator-over-platform `booking_ref` pairing; traveller `type` only when
  the document states it for that person, not from a party count.
- Also lost from version 1 (not 2026-10-20 amendments): the per-kind `title` forms, the
  `details` rules for connections, and the `price` rules (null when per-night, per-person or
  shared); version 4 leaves some of these to the schema's `description` fields.

Schema: TravelInbox `shared/extraction-schema.json` does NOT equal
`FromTravelApp/extraction-schema.json` (both `schema_version` 1; cmp differs, 194 diff lines).
The TravelInbox file carries `description` texts (trip_hint, notes, traveller `name`/`type`,
...), `x-vde-grounding` annotations and per-traveller `booking_ref`/`ticket_number`; the
FromTravelApp copy has none of them. Deviation (orchestrator, 2026-10-22): the baseline must
measure what TravelInbox runs, so the corpus `schema.json` link is replaced by a copy of
TravelInbox's `shared/extraction-schema.json` (commit `d5c36ae`) in this job instead of job
02; every `truth.json` still validates (`make eval`).

## Acceptance criteria

- vde's copy equals TravelInbox's prompt_version 4 byte for byte.
- The baseline report is recorded with per-case mismatched pointers and judge calls.
- `make check` and `make eval` pass.

## Out of scope

Changing the instructions or the schema (jobs 02 and 03).
