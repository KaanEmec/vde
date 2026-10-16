# Job: Role framings

Version: v0.1 · Epic: 05 Extraction loop · Status: done · Depends on: 02-01

## Goal

The service's fixed system prompts for the extractor and the judge, and the exact order in
which caller instructions, schema, document and prior feedback are assembled into the
request. Versioned as part of `service_version`.

## Context

Contract §6 steps 2 and 5: extractor prompt = fixed framing (task, exactly one JSON object
conforming to the schema, no prose, no code fences) + caller instructions verbatim and
complete + schema + document representation + on retry the previous candidate and the
feedback; judge prompt = fixed framing defining the verdict format of §7 + caller judge
instructions verbatim + schema + same representation + candidate. "The service MUST NOT
shorten, rewrite or translate the caller's instructions, and MUST NOT add domain content."

## Tasks

- [x] `internal/extract/framing.go`: two string constants (or embedded `.md` files) for
      the extractor and judge system framings; an `assembleExtractor(...)` and
      `assembleJudge(...)` that return `[]provider.Part` in the contract's order, with
      clear text separators ("Caller instructions:", "Schema:", "Document:", "Previous
      candidate:", "Feedback:").
- [x] Extractor framing content: output exactly one JSON object; conform to the schema
      when given; unknown values null or omitted as the schema allows, never guessed; no
      prose, no code fences; on retry, correct the listed issues and keep everything else
      that was right.
- [x] Judge framing content: compare the candidate to the source only; check accuracy,
      completeness and invented values; answer exactly
      `{"verdict": "accept"|"reject", "feedback": {pointer: message}, "confidence": 0.0-1.0}`;
      `confidence` is optional and expresses how sure the judge is of its verdict
      (decision 14); `reject` needs at least one entry; messages are for humans and may
      quote the source.
- [x] Framings are in English and contain no domain words. A test greps them for a small
      deny-list (travel, flight, hotel, invoice, patient) to keep them domain-neutral.
- [x] Framing text and separators are hashed into `service_version` (job 07).
- [x] Document the framings in `docs/operator/framings.md` so callers can write
      instructions that agree with them (contract §7: "the service's framing MUST agree
      with" the caller's judge instructions when they restate the format).

## Acceptance criteria

- Unit tests assert the part order and that caller instructions appear byte-for-byte.
- Framings documented and hashed.

## Out of scope

Prompt tuning on real corpora (v0.3 evaluation harness).

## Implementation notes

Done in `internal/extract/framing.go` with `framings/extractor.md` and `framings/judge.md`
(embedded), documented in `docs/operator/framings.md`. Deviations:

- Each framing file has one placeholder line filled per mode (schema or schema-less, job
  06), so four framing variants exist; all four are hashed.
- A separator and its content share one text part (`"Schema:\n\n" + schema`), so no part
  is ever empty; `Document:` is a part of its own followed by the representation's parts
  unchanged. The judge's candidate uses a sixth separator, `Candidate:`.
- The extractor framing never says "judge" and the judge framing never says "extractor", so
  fake provider steps can match roles by `role:` (tested).
