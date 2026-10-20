# Role framings

Status: v1.2. The framing texts live in
[`internal/extract/framings/extractor.md`](../../internal/extract/framings/extractor.md) and
[`internal/extract/framings/judge.md`](../../internal/extract/framings/judge.md); this page
describes them so callers can write instructions that agree with them (contract §7).

The service gives each role a fixed framing as the system prompt and assembles the user
content document first, then the schema, then the caller's instructions. This order
deviates from the contract's (§6 steps 2 and 5), by the owner's decision of 2026-10-20
([ADR 0012](../architecture/decisions/0012-evidence-first-framing.md)); an amendment is
proposed to TravelInbox. The framings are in English, name no
domain and never change per request. Caller instructions are passed byte for byte: the
service does not shorten, rewrite or translate them, and adds no domain content.

## Extractor request

| # | Part | When |
| --- | --- | --- |
| 1 | `Document:`, then the document parts: one text part, the document file, or the page images in order | always |
| 2 | `Schema:`, a blank line, the caller's schema as sent | when a schema is given |
| 3 | `Caller instructions:`, a blank line, the caller's `extractor_instructions` verbatim | always |
| 4 | `Previous candidate:`, a blank line, the previous answer's JSON | on a retry after an answer that was JSON |
| 5 | `Feedback:`, a blank line, one line per issue: `- "<JSON pointer>" (<schema, judge or grounding>): <message>` | on a retry |

The framing tells the extractor to answer in two blocks and nothing else (ADR 0012):

```
<analysis>
/reference: "Booking reference: HV-48213"
/date: "20 February 2026"
/total/amount: not in the document
</analysis>
<json>
{"reference": "HV-48213", "date": "2026-02-20", "total": {"amount": null}}
</json>
```

The analysis is one line per value: its JSON pointer, then the document's words for it,
copied exactly, or "not in the document". Only the `<json>` block is the answer: one JSON
value, no code fences, no comments. Caller instructions that ask for JSON only, or for no
prose, are read as describing the content of the `<json>` block. The framing also says
that every value comes from the document, where formatting, normalising or converting a
value the document gives, as the instructions ask (a date format, a unit, a code, a value
split into parts), is expected and inventing one is not; to use null for a value the
document does not give where the schema (or, without one, the instructions) allows null,
and to leave a field out only when it is neither required nor allowed to be null; and, on
a retry, to correct every listed issue and keep what was right. With a schema the answer
conforms to it, one JSON object unless the schema's top level requires another type.
Without a schema (schema-less mode) the JSON is shaped as the instructions describe: one
object, or one array when they ask for a list.

The analysis is discarded when the answer is parsed: it is never stored, logged, returned,
put into feedback or sent back as the previous candidate, which is the parsed JSON only.
It costs output tokens; the default `max_output_tokens` (16,384) leaves room for it.

## Judge request

| # | Part | When |
| --- | --- | --- |
| 1 | `Document:`, then the same document parts the extractor saw, from the same bytes | always |
| 2 | `Schema:`, a blank line, the caller's schema as sent | when a schema is given |
| 3 | `Caller instructions:`, a blank line, the caller's `judge_instructions` verbatim | always |
| 4 | `Candidate:`, a blank line, the candidate JSON as the extractor produced it | always |
| 5 | A one-line reminder of the verdict format | only when the previous verdict could not be parsed |

The framing tells the judge to compare the candidate with the document only, checking
accuracy, completeness and invented values. A value formatted, normalised or converted as
the instructions ask is accurate when it means the same as the document, and null (or a
field left out) for a value the document does not give is right. With a schema it adds that the candidate has
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

The same judge request, framing included, goes to the second judge
(`roles.second_judge`) and, after a classifier rejection with
`roles.judge.feedback_fallback: llm`, to the feedback group. A
[classifier judge](../providers/classifier.md) gets the request's text parts but not the
framing: its configured questions replace it, and the adapter renders its decisions as
this verdict object, with one entry at pointer `""` on a reject.

Grounding feedback (ADR 0009) uses the same retry framing, with no extra text: after the
one grounding repair, each value the document does not contain is a line such as
`- "/date" (grounding): value not found in the document`. The message never quotes the
value. Under judge policy `always` these entries reach only the extractor, never the job's
`feedback`; under `never` and `auto` they also appear there as advisory entries.

## Answer parsing

An answer in blocks is read from its `<json>` block only. When the answer has an
`<analysis>` block, it must be closed and a `<json>` block must follow it; the `<json>`
block must be closed, and there must be exactly one after the analysis. Two `<json>`
blocks are refused rather than choosing one, so a value in the analysis, or a draft, is
never taken. Each of these failures is an answer that was not JSON (below), with a message
naming the tag, never the content.

An answer with neither block (a model that ignores the format, the fake provider's
scenarios) is read as before. Within what is read, the service strips surrounding
whitespace and prose and takes the first fenced code block when there is one; it then
takes the first complete JSON value starting at the first `{` (or `[` where arrays are
allowed). It never repairs JSON: truncated or invalid JSON is an answer that was not JSON,
which goes back to the extractor as `schema` feedback on the pointer `""` and counts as an
attempt.

## Role names

The extractor framing calls its role "the extractor" and never mentions the judge; the
judge framing calls its role "the judge" and never mentions the extractor. A fake provider
script can therefore match a step to a role with `role: extractor` or `role: judge`
(a substring of the system prompt).

## Versioning

The four framing variants (extractor and judge, with and without a schema), the
separators, the reminder line and the answer parser's leniency are hashed into
`service_version`, so any change to them changes the version string (contract §11).
