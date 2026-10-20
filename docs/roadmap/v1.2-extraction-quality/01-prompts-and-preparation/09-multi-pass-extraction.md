# Job: Multi-pass extraction (ADR 0013)

Version: v1.2 · Epic: 01 Prompts and document preparation · Status: dropped · Depends on: 07, and the owner's go-ahead on ADR 0013

Dropped 2026-10-20: after jobs 01 to 06 every core case is fully correct and none is a large-schema or multi-item failure (see [quality](../../../quality.md#v12-prompts-and-document-preparation-2026-10-20)). No ADR 0013 was written.

## Goal

Decide, by an ADR and a measurement, whether large schemas or documents with many items
(several bookings, a multi-leg itinerary) should be extracted in several passes, each
with a part of the schema or one item, and merged into one candidate the judge checks as
a whole.

## Context

ExtractBench finds frontier models unreliable on schemas with hundreds of fields: long,
precisely structured outputs break first. Multi-pass changes the loop's shape (more
extractor calls per attempt, a merge step, the attempt and cost bounds of ADR 0009), so it
is an architectural decision and needs ADR 0013 before code.

Start only if jobs 01 to 07 leave a measurable gap on large-schema or multi-item cases;
otherwise mark this job `dropped` with the numbers.

## Tasks

- [x] From the reports of jobs 01 to 07, list the cases that still fail and whether they
      are large-schema or multi-item cases. Stop here (job `dropped`) if they are not.
- [ ] ADR 0013: when to split (schema size, top-level arrays), how (by top-level property
      groups, by array item after a first pass that lists the items), the merge rules,
      how the judge sees the result, how attempts, deadlines and cost stay bounded, and
      what `service_version` hashes.
- [ ] Owner decision on the ADR.
- [ ] If accepted: implement behind an application-level setting that defaults off, with
      fake-provider scenarios for split, merge, a failed pass and the bounds.
- [ ] Run one comparison on 3 to 5 cases: the failing large-schema or multi-item cases,
      filled up with core cases; record it, keep or revert. Never the full suites.

## Acceptance criteria

- Either the job is `dropped` with the numbers that show no gap, or ADR 0013 is accepted
  and the implementation passes its scenarios with the comparison recorded.
- Under the default setting, requests and results are unchanged.

## Out of scope

Retrieval or chunking of documents that fit the context window.
