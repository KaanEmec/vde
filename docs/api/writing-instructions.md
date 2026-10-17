# Writing instructions and schemas

What you send with a document is three pieces of text you own: the extractor instructions,
the judge instructions and (usually) a JSON Schema. The service adds a fixed framing around
them and adds no domain knowledge of its own, so everything specific to your documents has
to be in what you write. This page explains how the pieces fit together and how to write
each. The mechanics of the call are in [guide.md](guide.md); the framing texts themselves
are described in [operator/framings.md](../operator/framings.md).

## How the prompts are assembled

Each role gets a fixed framing as its system prompt (English, domain-free, the same for
every request) and a user message built in this order. Your text goes in verbatim: the
service never shortens, rewrites or translates it.

| Extractor message | Judge message |
| --- | --- |
| 1. `Caller instructions:` + your `extractor_instructions` | 1. `Caller instructions:` + your `judge_instructions` |
| 2. `Schema:` + your schema, when you sent one | 2. `Schema:` + your schema, when you sent one |
| 3. `Document:` + the document (text, file or page images) | 3. `Document:` + the same document, from the same bytes |
| 4. On a retry: `Previous candidate:` + the last JSON answer | 4. `Candidate:` + the extractor's JSON |
| 5. On a retry: `Feedback:` + one line per issue | |

What the framings already say, so you do not need to repeat it:

- **Extractor**: answer with exactly one JSON value and nothing else (no prose, no code
  fences); take every value from the document and never guess; use null or omit what the
  document does not give, as the schema allows; on a retry, fix every listed issue and keep
  what was right. With a schema the answer conforms to it.
- **Judge**: compare the candidate with the document only, checking accuracy,
  completeness and invented values; answer in the verdict format below. When a schema was
  sent, the judge is told the candidate already validated, so it checks what the values
  say, not their form.

What the framings cannot say, because it depends on your documents, is your job: what the
fields mean, how to format and normalise them, which derivations are allowed, what to do
with conflicting or missing values, and what the judge may and may not reject for.

## Extractor instructions

Describe the task as you would to a careful clerk who has never seen your documents.

- **Say what a record is.** Which things in the document become items, and how they are
  split or merged. ("One line item per line of the table; no line for subtotals or totals.")
- **Fix every format.** Dates, times and zones, amounts and currencies, name order, casing,
  identifiers. State what to copy as printed and what may be normalised.
- **Say what is allowed besides copying.** If a derivation is permitted (an ISO currency code
  from a symbol, a city's country), list it; anything not listed is invention. The judge's
  instructions should list the same derivations.
- **Say what to do when the document is silent or contradictory.** "Null, not a guess" is the
  default the framing already applies; add where conflicts go (for example a `notes` field).
- **Keep documents' own instructions out.** A document is data. Say once that text inside a
  document that addresses the reader is content, not an order.
- **Mention retries lightly.** The framing already handles feedback; a line such as "fix
  exactly what the feedback names and keep the rest" is enough.

## Judge instructions

The judge decides acceptance, so its instructions are the quality bar. Write them as rules
for what to reject and what not to reject.

- **State what the extractor was told**, in brief, so the judge does not apply its own
  preferences ("allowed derivations: ...", "dates may be reformatted"). Disagreement between
  the two instruction sets shows up as rejections the extractor cannot fix.
- **List reject reasons**: a value that is wrong or invented, a stated value that is missing,
  an item that the document does not contain, swapped or merged items, sensitive data that
  must not appear.
- **List non-reasons**: null where the document is silent, a correct allowed derivation, a
  field omitted instead of null. Without them the judge tends to reject on style, and each
  rejection costs another extraction attempt.
- **Ask for pointers.** Feedback is useful only if the key is as specific as possible and the
  message says what the document shows and where.
- **Restate the verdict format**, as below. The framing already defines it, and a restatement
  that agrees with it is harmless.

### Verdict format

```json
{"verdict": "accept", "feedback": {}, "confidence": 0.9}
{"verdict": "reject", "feedback": {"/total/amount": "The document says 412.50, not 421.50."}}
```

- `verdict` is exactly `accept` or `reject`; anything else is an unparseable verdict, which
  is asked for once more and then fails the job with `judge_failed`.
- `feedback` maps an RFC 6901 pointer into the candidate to one message. `""` is the whole
  candidate; `/line_items/-` means something is missing from that array. A `reject` has at
  least one entry; an `accept` may carry advisory entries.
- `confidence` is optional (0 to 1) and appears on the job as `judge_confidence`. It is
  advisory and never changes the status. Ask for it only if you will use it.
- Other keys are ignored.

## Schema design

A schema pins the shape so that your code can rely on it, and every job's result is
validated against it before the judge sees it. A candidate that fails validation goes back
to the extractor as `source: "schema"` feedback without a judge call and counts as an
attempt, so a schema that is stricter than the documents allow burns attempts.

- **Close objects**: `"additionalProperties": false` on every object. Without it the
  extractor may add fields that nothing checks. Open one only where the shape is genuinely
  free.
- **Required means the document always has it.** Require only what every document of the
  kind states. Make the rest optional or nullable, so "not in the document" has a valid
  answer other than inventing a value. A field that must be present but may be empty is
  `"type": ["string", "null"]` and listed in `required`; a field that may be absent is not
  listed.
- **Nullable fields**: `{"type": ["string", "null"]}`. In a reusable definition, write the
  null once (TravelInbox's `$defs/text`, `date`, `iata` are all nullable) and `$ref` it.
- **Constrain what you will rely on**: `pattern` for identifiers and codes, `enum` for closed
  vocabularies (include `null` in the enum if null is valid), `format: date` or a date
  `pattern`, `minimum` and `maximum`, `minLength: 1` to rule out empty strings. Amounts as a
  decimal string with a pattern avoid floating-point drift; plain numbers are fine for a
  simple case like the sample.
- **Describe every field.** `description` is read by the extractor and the judge as part of
  the schema; put the format and the "or null when" rule there as well as in the
  instructions.
- **Mind the model's limits.** Prefer plain keywords (`type`, `properties`, `required`,
  `enum`, `pattern`, `items`, `$ref` to local `$defs`) over exotic ones. The schema is limited to 256 KiB and
  must be a valid 2020-12 schema, or the submit is refused with `422 invalid_schema`.
- **Version it**: `"schema_version": {"const": 1}` in the result, plus a version of your
  own for the instructions. You see them as opaque; the service only sees text.

### Confidence patterns

TravelInbox's schema ([`testdata/travelinbox/extraction-schema.json`](../../testdata/travelinbox/extraction-schema.json))
asks the extractor for its own confidence per field, next to the data:

- Each reservation has a required `confidence` object. Its keys are limited to the
  reservation's field names with `propertyNames: {"enum": [...]}`, its values are numbers
  between 0 and 1 (`additionalProperties`), and only `kind` and `title` are `required`.
- The instructions give the scale: 0.95 to 1 printed plainly and copied; 0.8 to 0.95
  reformatted or derived; 0.5 to 0.8 inferred or hard to read; below 0.5 do not output the
  value, use null and explain in `notes`. A field that is null gets no confidence.
- The consumer uses these to decide what a person should look at; the judge's verdict still
  decides acceptance.

Two kinds of confidence exist and mean different things: the extractor's per-field numbers
live in `result` and are yours to define; `judge_confidence` is the judge's single number
about its verdict, optional and advisory. Neither replaces the verdict.

A `notes` field (a nullable string at the top level) is a good outlet for what the extractor
cannot fit in the schema or found ambiguous; it saves the model from forcing a value.

### Annotating the schema for grounding

Since v0.4 the service checks deterministically that the values of a candidate occur in
the document text (the grounding check of [ADR 0009](../architecture/decisions/0009-judge-policy-and-grounding.md)).
Under the default judge policy `always` it only sends a candidate with invented values back
to the extractor once before the judge; under `auto` it decides whether the judge is
needed at all. A value the model legitimately derives (an ISO currency code from `€`, a
city from an airport code, a computed total) is not printed and would count as invented.
Tell the check with the keyword `x-vde-grounding` on a property:

- `critical`: must be found in the document (references, dates, amounts, names). Once any
  property is `critical`, only those gate acceptance without a judge.
- `derived`: an allowed derivation, not checked.
- `skip`: never checked (your `notes`, titles you ask the model to write, confidences).
- `check`: checked but not critical; overrides an inherited `derived` or `skip`.

An annotation applies to everything below the property. Enum and `const` values,
booleans and nulls are never checked. Without any annotation every checked leaf is
critical, and properties named `confidence` and `notes` are skipped. The annotation is
ignored by schema validation; an unknown value is refused with `422 invalid_schema`.

```json
"reference": {"type": ["string", "null"], "x-vde-grounding": "critical"},
"issue_date": {"type": "string", "format": "date", "x-vde-grounding": "critical"},
"currency": {"type": "string", "pattern": "^[A-Z]{3}$", "x-vde-grounding": "derived"},
"notes": {"type": ["string", "null"], "x-vde-grounding": "skip"}
```

Dates in ISO form are matched against the printed forms the service knows (`21.03.2027`,
`21 March 2027`, `March 21, 2027`, ... in six languages); the full table and the matching
rules are in [judge policy](../operator/judge-policy.md#normalization-and-matching).

## Worked example: the generic sample

[`examples/samples/`](../../examples/samples/) has a complete, domain-neutral set:
[`extractor.md`](../../examples/samples/extractor.md),
[`judge.md`](../../examples/samples/judge.md),
[`schema.json`](../../examples/samples/schema.json) and sample documents, among them
[`booking.txt`](../../examples/samples/booking.txt), a hotel booking confirmation.

The schema describes a document summary: `title`, `reference` (nullable, so a document
without a number is valid), `date` (`format: date`), `parties` (name and role), `total`
(`amount` and a currency with `pattern: "^[A-Z]{3}$"`) and `line_items`. Every object is
closed with `additionalProperties: false`, and only `title`, `date`, `parties`, `total` and
`line_items` are required.

The extractor instructions do the things listed above in five bullets: use only what the
document says (null when the schema allows it, omit an optional field otherwise); copy
names, references and amounts as written; fix the date and amount formats; read every line
item and add none for subtotals or totals; on feedback, fix what is named and keep the rest.
They name no domain, so the same instructions work for invoices, receipts and bookings.

The judge instructions say what to reject (unsupported, missing, differing or extra values),
what to accept, and give the verdict format with a pointer example. They tell the judge to
judge only fidelity to the document, not the document.

For `booking.txt` an accepted result looks like:

```json
{
  "title": "HARBOUR VIEW HOTEL - BOOKING CONFIRMATION",
  "reference": "HV-48213",
  "date": "2026-02-20",
  "parties": [
    {"name": "Harbour View Hotel", "role": "issuer"},
    {"name": "Jane Example", "role": "guest"},
    {"name": "Example Travel Desk", "role": "booker"}
  ],
  "total": {"amount": 412.5, "currency": "EUR"},
  "line_items": [
    {"description": "Room, 3 nights", "quantity": 3, "unit_amount": 120, "amount": 360},
    {"description": "Breakfast", "quantity": 3, "unit_amount": 15, "amount": 45},
    {"description": "City tax, 3 nights", "quantity": 3, "unit_amount": 2.5, "amount": 7.5}
  ]
}
```

(Which parties the model lists and what it calls their roles can vary between runs; the
instructions leave that to the document, and the judge only objects to unsupported values.)

If the extractor had read the total as 421.50, the job would go back with feedback shaped
like this, and the second attempt would correct exactly that:

```json
{"verdict": "reject", "feedback": {"/total/amount": "The document says 412.50, not 421.50."}}
```

To adapt the sample, change the schema first, then make the extractor instructions say how
to fill each new field, then add the matching reject reasons to the judge instructions.

## Pointers and feedback semantics

Pointers are RFC 6901 JSON pointers into the candidate: `/` separates levels, array indices
are decimal (`/line_items/1/amount`), `~0` and `~1` escape `~` and `/` in keys, `""` is the
whole document and a trailing `/-` is a position past the end of an array, used for
something missing.

- **`source: "schema"`** entries come from the service's validator: one per violation,
  pointing at the offending location, naming the keyword and expected form. They are sent
  to the extractor with the previous candidate and cost an attempt; the judge is not called.
  A candidate that is not JSON at all gets a single entry at `""`.
- **`source: "judge"`** entries are the judge's, in the order it gave them. The service
  does not edit the messages except to cap their length.
- On a retry the extractor sees each entry as `- "<pointer>" (<source>): <message>`, so
  messages should read as instructions to fix something, and say what the document shows.
- In the final job, `feedback` is the last round's. `rejected` jobs carry the last candidate
  and the feedback that sank it; `accepted` jobs carry advisory entries only.
- Messages may quote the document. Judge instructions should tell the judge never to
  repeat sensitive data in feedback (name its kind instead) if your documents contain it,
  since you will display or store these messages.

## Checklist

- Every format and every allowed derivation is stated, in the same words in the extractor and
  the judge instructions.
- The schema marks as required only what every document states, and everything else is
  nullable or optional.
- Objects are closed, identifiers have patterns, closed vocabularies are enums.
- The judge's reject reasons and non-reasons are listed, and it is asked for pointers.
- You tried it on a few documents with `retention: default` and read the `feedback` of
  rejected jobs: repeated, unfixable feedback points at an instruction or schema problem,
  not at the model.
