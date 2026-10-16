# Job: Answer parsing

Version: v0.1 · Epic: 05 Extraction loop · Status: done · Depends on: none

## Goal

Turn a model's text answer into a JSON value with exactly the leniency the contract allows
and no more: strip code fences and leading or trailing prose, never repair JSON.

## Context

Contract §6 step 3: "Parse the answer as JSON (the service MAY strip code fences and
leading or trailing prose; it MUST NOT repair the JSON beyond that)." Step 4: a candidate
that is not JSON goes back to the extractor with `source: "schema"` feedback and counts as
an attempt.

## Tasks

- [x] `ParseJSONObject(text) (json.RawMessage, error)`: trim; if fenced, take the first
      fenced block; otherwise find the first `{` and the matching last `}` by a simple
      balanced scan that respects strings; `json.Valid` on the slice; require a top-level
      object (not array or scalar) unless the schema allows another top-level type
      (schema-less mode and non-object schemas: accept arrays too).
- [x] Preserve the raw text of the candidate for `result` as parsed JSON (`result` is the
      parsed candidate, kept "exactly as the extractor produced it, invalid or not" for
      rejected and failed jobs; the unparseable text is not exposed, `result` is `null`).
- [x] Failure returns a single feedback entry: pointer `""`, source `schema`, message
      "The answer was not a single JSON object" plus the parser's position.
- [x] Table tests: clean JSON, fenced JSON, prose before and after, two objects (take
      the first complete one), truncated JSON (fail), empty answer (fail), a refusal
      sentence (fail).

## Acceptance criteria

- No test input is ever "repaired"; truncated or invalid JSON fails.

## Out of scope

Streaming parsing, JSON5 or comments.

## Implementation notes

Done in `internal/extract/parse.go` as the unexported `parseJSON(text, allowArray)`; the
package exports no parser because only the loop uses it. The whole trimmed answer is tried
first (so a clean object containing a fence inside a string is kept), then the first
fenced block, then the first value from the first `{` (or `[` when arrays are allowed).
Only the first opening bracket is tried: trying later ones could turn a truncated value into
one of its fragments. A top-level array where an object is required fails rather than
yielding an object inside it. The loop's feedback message is "The answer was not a single
JSON object: <reason with offset>." A fuzz test checks that every accepted value is a
substring of the answer.
