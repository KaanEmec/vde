# Job: Schema guide for callers

Version: v1.2 · Epic: 01 Prompts and document preparation · Status: planned · Depends on: none (uses what 01 and 05 decide)

## Goal

Give callers one guide to writing schemas and instructions that a model follows well, and
give the owner a version to share with TravelInbox, whose schema quality caps what the
service can reach on their documents.

## Context

`docs/api/writing-instructions.md` already has "Schema design", confidence patterns,
grounding annotations, a worked example and a checklist. What it lacks, from the
research: that descriptions are read by the model and decide what it extracts; formats
and units in every description; closed enums for categories; nullable rather than
optional fields, with the meaning of null stated; flat and small schemas; no
contradictions between instructions and schema; how the answer format of job 05 and the
section labels and context line of jobs 02 and 03 can be referred to.

The TravelInbox schema lives in `FromTravelApp/` (read-only input) and
`testdata/travelinbox/` (co-owned). This job does not change it.

## Tasks

- [ ] Extend `docs/api/writing-instructions.md` ("Schema design" and the checklist) with
      the points above, each with a short before and after example on synthetic data.
- [ ] Review the TravelInbox schema against the checklist and write the findings as
      suggestions (field, problem, proposed description or type) in
      `docs/conformance/`, without document content.
- [ ] The owner sends the suggestions to TravelInbox; the agent does not.
- [ ] If TravelInbox adopts any, run one comparison with the new schema on 3 to 5 named
      TravelInbox cases (`--cases` listing each id; the cases the changed fields affect),
      before and after, and record it. Never the whole corpus.

## Acceptance criteria

- The guide's checklist covers descriptions, formats and units, enums, null meaning,
  nesting depth and instruction-schema consistency.
- The suggestions file exists and names fields by JSON pointer only.
- Docs tests and link checks pass.

## Out of scope

Changing the TravelInbox contract or schema in this repository.
