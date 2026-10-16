# Role framings

Status: v0.1. The framing texts live in
[`internal/extract/framings/extractor.md`](../../internal/extract/framings/extractor.md) and
[`internal/extract/framings/judge.md`](../../internal/extract/framings/judge.md); this page
describes them so callers can write instructions that agree with them (contract §7).

The service gives each role a fixed framing as the system prompt and assembles the user
content in the contract's order (§6 steps 2 and 5). The framings are in English, name no
domain and never change per request. Caller instructions are passed byte for byte: the
service does not shorten, rewrite or translate them, and adds no domain content.

## Extractor request

| # | Part | When |
| --- | --- | --- |
| 1 | `Caller instructions:`, a blank line, the caller's `extractor_instructions` verbatim | always |
| 2 | `Schema:`, a blank line, the caller's schema as sent | when a schema is given |
| 3 | `Document:`, then the document parts: one text part, the document file, or the page images in order | always |
| 4 | `Previous candidate:`, a blank line, the previous answer's JSON | on a retry after an answer that was JSON |
| 5 | `Feedback:`, a blank line, one line per issue: `- "<JSON pointer>" (<schema or judge>): <message>` | on a retry |

The framing tells the extractor to answer with exactly one JSON value and nothing else (no
prose, no code fences, no comments); to take every value from the document and use null or
omit a field the document does not give, as the schema and instructions allow, never
guessing; and, on a retry, to correct every listed issue and keep what was right. With a
schema the answer conforms to it, one JSON object unless the schema's top level requires
another type. Without a schema (schema-less mode) the JSON is shaped as the instructions
describe: one object, or one array when they ask for a list.

## Judge request

| # | Part | When |
| --- | --- | --- |
| 1 | `Caller instructions:`, a blank line, the caller's `judge_instructions` verbatim | always |
| 2 | `Schema:`, a blank line, the caller's schema as sent | when a schema is given |
| 3 | `Document:`, then the same document parts the extractor saw, from the same bytes | always |
| 4 | `Candidate:`, a blank line, the candidate JSON as the extractor produced it | always |
| 5 | A one-line reminder of the verdict format | only when the previous verdict could not be parsed |

The framing tells the judge to compare the candidate with the document only, checking
accuracy, completeness and invented values. With a schema it adds that the candidate has
already been validated, so the judge checks what the values say rather than their form.
Without a schema it adds that the instructions describe the expected JSON and that fields
they ask for which the candidate lacks are completeness issues.

The judge answers with exactly this object and nothing else:

```json
{"verdict": "accept", "feedback": {}, "confidence": 0.9}
```

- `verdict` is `accept` or `reject`, exactly. Anything else is an unparseable verdict.
- `feedback` maps RFC 6901 JSON pointers into the candidate (`""` for the whole candidate)
  to one message each. A `reject` has at least one entry; an `accept` may carry advisory
  entries. Messages are for people and may quote the document. The service keeps their
  order and does not edit them except to cap their length (`limits.feedback_message_chars`,
  default 4,000 characters, the last replaced by `…`).
- `confidence` is optional, a number from 0 to 1 saying how sure the judge is of its
  verdict. It is forwarded as the job's `judge_confidence` and never changes acceptance.
  A value that is not a number in range is ignored.
- Other keys are ignored.

Caller judge instructions may restate this format, as TravelInbox's do; a restatement
without `confidence` agrees with it, since the key is optional.

An unparseable verdict, or a judge refusal, is asked for once more in the same attempt
(`limits.judge_reparse_tries`) with the reminder; if that fails too, the job is `failed`
with `judge_failed`. A judge failure never counts as acceptance.

## Answer parsing

The service strips surrounding whitespace and prose and takes the first fenced code block
when the answer has one; it then takes the first complete JSON value starting at the first
`{` (or `[` where arrays are allowed). It never repairs JSON: truncated or invalid JSON is
an answer that was not JSON, which goes back to the extractor as `schema` feedback on the
pointer `""` and counts as an attempt.

## Role names

The extractor framing calls its role "the extractor" and never mentions the judge; the
judge framing calls its role "the judge" and never mentions the extractor. A fake provider
script can therefore match a step to a role with `role: extractor` or `role: judge`
(a substring of the system prompt).

## Versioning

The four framing variants (extractor and judge, with and without a schema), the
separators, the reminder line and the answer parser's leniency are hashed into
`service_version`, so any change to them changes the version string (contract §11).
