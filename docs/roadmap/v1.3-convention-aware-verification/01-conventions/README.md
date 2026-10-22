# Epic 01: Conventions in schema and instructions

Version: v1.3 · Status: planned · Depends on: v1.2 (released)

Write TravelInbox's labelling conventions down where they work: as schema constraints a
validator checks for free (job 02), and as examples the models follow (job 03), measured on
vde's co-owned copy of the TravelInbox corpus and handed to TravelInbox as a proposal for
their roadmap job `v1.0-launch/04-vde/06-extraction-quality`. Job 01 first brings the copy
in line with what TravelInbox runs today, so the baseline is their production prompt.

| # | Job | Status |
| --- | --- | --- |
| 01 | [Baseline on TravelInbox prompt_version 4](01-baseline-prompt-version-4.md) | planned |
| 02 | [Conventions in the schema](02-conventions-in-schema.md) | planned |
| 03 | [Conventions by example, and the hand-over](03-conventions-by-example.md) | planned |

The jobs are sequential: each one's report is the next one's baseline.

Rules for every job:

- Edit only `testdata/travelinbox/corpus/` (co-owned, see its `README.md`) and vde's docs.
  Never the TravelInbox repository, never `FromTravelApp/` (read-only contract input).
- Examples and wording never come from the corpus documents, least of all the five
  measured cases: they are written on synthetic values, so the measurement is not leaked.
- Document content, expected values and candidates never appear in commits, docs or
  reports; reports carry numbers and pointers only.
