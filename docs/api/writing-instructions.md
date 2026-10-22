# Writing instructions and schemas

What you send with a document is three pieces of text you own: the extractor instructions,
the judge instructions and (usually) a JSON Schema. The service adds a fixed framing around
them and adds no domain knowledge of its own, so everything specific to your documents has
to be in what you write. This page explains how the pieces fit together and how to write
each. The mechanics of the call are in [guide.md](guide.md); the framing texts themselves
are described in [operator/framings.md](../operator/framings.md).

## How the prompts are assembled

Each role gets a fixed framing as its system prompt (English, domain-free, the same for
every request) and a user message built in this order: the document first, then the
schema, then your instructions, then what is specific to the round. Your text goes in
verbatim: the service never shortens, rewrites or translates it.

| Extractor message | Judge message |
| --- | --- |
| 1. `Document:` + the document (text, file or page images) | 1. `Document:` + the same document, from the same bytes |
| 2. `Schema:` + your schema, when you sent one | 2. `Schema:` + your schema, when you sent one |
| 3. `Caller instructions:` + your `extractor_instructions` | 3. `Caller instructions:` + your `judge_instructions` |
| 4. On a retry: `Previous candidate:` + the last JSON answer | 4. `Candidate:` + the extractor's JSON |
| 5. On a retry: `Feedback:` + one line per issue | |

The order is deliberate: models answer better about a long document when the task comes
after it. It means your instructions are read last, right before the model answers, so
they can be short and need not repeat the schema. (This differs from the contract's order
and is described in [ADR 0012](../architecture/decisions/0012-evidence-first-framing.md).)

What the framings already say, so you do not need to repeat it:

- **Extractor**: answer in two blocks (see "Answer formats"); every value comes from the
  document and nothing is guessed; use null for what the document does not give where the
  schema allows it, and leave a field out only when it is neither required nor nullable;
  formatting or normalising a value the document gives, as your instructions ask, is
  expected, inventing one is not; on a retry, fix every listed issue and keep what was
  right. With a schema the answer conforms to it.
- **Judge**: compare the candidate with the document only, checking accuracy,
  completeness and invented values; a value normalised as your instructions ask is
  accurate when it means the same as the document, and null for a value the document does
  not give is right; answer in the two-block format below. When a schema was sent, the
  judge is told the candidate already validated, so it checks what the values say, not
  their form.

What the framings cannot say, because it depends on your documents, is your job: what the
fields mean, how to format and normalise them, which derivations are allowed, what to do
with conflicting or missing values, and what the judge may and may not reject for.

## Answer formats

Both roles think first and answer second, in two blocks. The exact texts and the parsing
rules are in [operator/framings.md](../operator/framings.md); what matters when you write
instructions:

- **Extractor**: a short `<analysis>…</analysis>` (one line per value: its pointer and the
  document's words for it, or "not in the document"), then the answer as
  `<json>…</json>`. Only the `<json>` block is read.
- **Judge**: a short `<analysis>…</analysis>` checking each candidate value against the
  document, then the verdict object as `<verdict>…</verdict>`. Only the `<verdict>` block
  is read.
- The analysis is discarded when the answer is parsed. It is never stored, logged,
  returned or used as feedback, so there is nothing for you to do with it and nothing to
  ask the model to put in it.
- An answer that is bare JSON, with no blocks, still parses: a model that ignores the
  format costs nothing but the analysis. An answer with two `<json>` blocks (or two
  `<verdict>` blocks) is refused rather than guessed at, and an unclosed block is an
  answer that was not JSON, which goes back as `schema` feedback; for the judge it is an
  unparseable verdict and never acceptance.
- **Drop "JSON only" lines.** Instructions such as "Return only the JSON object: no prose,
  no code fences" contradict the blocks. The framings tell the model to read such a line as
  describing the content of the block, so it still works, but a contradiction costs
  attention; delete it. "No code fences inside the block" is fine and already in the
  framing.

## What the models see of your document

The service lays a document out before a model reads it, in ways your instructions can
rely on and refer to. Nothing is removed from the document by these steps; they only add
lines or structure.

- **A first line of context**: `type: email · sent: 2026-10-10T09:14:00+02:00 · pages: 3`.
  `type` is `email`, `html`, `text`, `markdown`, `pdf` or `image`. `sent` is an email's
  `Date` header as RFC 3339 with the sender's own offset, and is left out when the header is
  missing or unparseable; it is never the time you submitted the job, and other types never
  carry it. `pages` is left out when the service has not counted the pages (a PDF sent
  natively). This is where a model finds the year of a date printed without one: your
  instructions can say "a date without a year takes the year of `sent`".
- **Page markers**: `--- page N ---` before each page, both in a PDF's text layer and
  before each page image (rendered or sent by you). Instructions can say "the fare is on
  the page that lists the passengers", and judge feedback names pages in the same terms.
- **Email section labels** (forwarded and quoted material): a line `[forwarded message]`
  before, and `[end of forwarded message]` after, a forwarded message, and
  `[quoted earlier reply]` … `[end of quoted earlier reply]` around a quoted reply. Nested
  sections are labelled in turn. Detection is a heuristic for the common mail clients;
  a section it does not recognise is plain text, as before, and no text is ever removed.
  Instructions can use the labels: "the booking is usually in the forwarded message; the
  lines of the forwarder around it are not booking data".
- **Data tables** (only with `documents.html: visible_text`, for HTML documents and HTML
  emails): a table that has a header or is regular (at least two rows and two columns of
  the same width, no nested table) is written as a Markdown table with a header row, so
  each value stays under its column. Layout tables, which most mail templates are built
  from, stay flattened as before. PDFs and `raw_html` are unchanged.

None of these lines is evidence: the context line, page markers and labels are not part of
the text the grounding check searches, so a value that appears only there still counts as
not found in the document.

## Extractor instructions

Describe the task as you would to a careful clerk who has never seen your documents.

- **Say what a record is.** Which things in the document become items, and how they are
  split or merged. ("One line item per line of the table; no line for subtotals or totals.")
- **Fix every format.** Dates, times and zones, amounts and currencies, name order, casing,
  identifiers. State what to copy as printed and what may be normalised.
- **Say what is allowed besides copying.** If a derivation is permitted (an ISO currency code
  from a symbol, a city's country), list it; anything not listed is invention. The judge's
  instructions should list the same derivations. Normalising what the document does give
  is expected, so say how: "dates as YYYY-MM-DD", "amounts with a dot and no thousands
  separator". Inventing what it does not give is the one thing the framing forbids.
- **Say what to do when the document is silent or contradictory.** "Null, not a guess" is the
  default the framing already applies, wherever the schema allows null (see "Schema
  design"); add where conflicts go (for example a `notes` field).
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
- **Restate the verdict format**, as below, if you like; the framing already defines it, and a
  restatement that agrees with it is harmless. Do not add "JSON only, no prose": the judge
  is asked to analyse first.

### Verdict format

The judge answers with a short `<analysis>` block and then the verdict object inside
`<verdict>…</verdict>` (see "Answer formats"). The verdict object is:

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
- A bare verdict object with no blocks still parses. An answer with an analysis but no
  closed `<verdict>` block, or with two, is an unparseable verdict, never an accept.

## Schema design

A schema pins the shape so that your code can rely on it, and every job's result is
validated against it before the judge sees it. A candidate that fails validation goes back
to the extractor as `source: "schema"` feedback without a judge call and counts as an
attempt, so a schema that is stricter than the documents allow burns attempts.

- **Close objects**: `"additionalProperties": false` on every object. Without it the
  extractor may add fields that nothing checks. Open one only where the shape is genuinely
  free.
- **Required means the document always has it.** Require only what every document of the
  kind states. Make the rest nullable (preferably) or optional, so "not in the document"
  has a valid answer other than inventing a value.
- **Prefer nullable to optional.** A field that is present but may be null,
  `"type": ["string", "null"]` and listed in `required`, forces the model to decide about
  it and lets "the document does not give it" be told apart from "the model forgot it". A
  field that may be absent is not listed in `required`; the model then tends to skip it
  whenever it is unsure. State what null means in the field's description.
- **Nullable fields**: `{"type": ["string", "null"]}`. In a reusable definition, write the
  null once (TravelInbox's `$defs/text`, `date`, `iata` are all nullable) and `$ref` it.
- **Constrain what you will rely on**: `pattern` for identifiers and codes, `enum` for closed
  vocabularies (include `null` in the enum if null is valid), `format: date` or a date
  `pattern`, `minimum` and `maximum`, `minLength: 1` to rule out empty strings. Amounts as a
  decimal string with a pattern avoid floating-point drift; plain numbers are fine for a
  simple case like the sample.
- **Use closed enums for categories.** A `kind`, `status` or `role` with a fixed set of
  values (`enum`) is answered consistently; a free string is answered in as many spellings
  as there are runs. Say in the description when to pick each value, and give the catch-all
  (`other`) its own rule so it is not used as a first resort.
- **Describe every field.** `description` is read by the extractor and the judge as part of
  the schema, and it decides what is extracted: a field named `date` with no description
  gets whichever date the model finds first. Say what the field is, where it comes from in
  the document, and what it is not. Put the format and unit there ("YYYY-MM-DD",
  "24-hour HH:MM", "decimal string, no thousands separator", "ISO 4217 code") and the
  "or null when" rule, as well as in the instructions. The names of properties help too, but
  they are not enough: a description is the only place the model is told the format.
- **Keep it flat and small.** Every level of nesting and every `oneOf` or `if`/`then` is
  another place a model gets the shape wrong, and every failed validation burns an attempt.
  Prefer a handful of top-level fields over deep objects; avoid `oneOf` and conditional
  rules where a nullable field does the same; put a repeated shape in `$defs`; split a
  document with several very different kinds of content into more than one request if
  the schema grows past what one answer can carry. Models are still unreliable on large
  schemas and long outputs.
- **No contradictions between schema and instructions.** The model reads both, and the
  judge reads both. If the instructions say "null when not printed" and a description says
  "required", or the instructions format dates one way and the `pattern` another, the
  extractor cannot satisfy both and the retry feedback repeats. When you change one, change
  the other; the checklist below ends with this check.
- **Mind the model's limits.** Prefer plain keywords (`type`, `properties`, `required`,
  `enum`, `pattern`, `items`, `$ref` to local `$defs`) over exotic ones. The schema is limited to 256 KiB and
  must be a valid 2020-12 schema, or the submit is refused with `422 invalid_schema`.
- **Version it**: `"schema_version": {"const": 1}` in the result, plus a version of your
  own for the instructions. You see them as opaque; the service only sees text.

### Before and after

Synthetic examples of the points above; the documents are invented.

**Description with format, unit and null** (the model finds a date and an amount, but not
the format or which of two dates is meant):

```json
"valid_until": {"type": "string"},
"deposit": {"type": "number"}
```

```json
"valid_until": {
  "description": "Last day the quote can be accepted, YYYY-MM-DD. Null when the document gives no expiry.",
  "type": ["string", "null"], "pattern": "^[0-9]{4}-[0-9]{2}-[0-9]{2}$"
},
"deposit": {
  "description": "Deposit due at booking, in the quote's currency, decimal string with a dot and no thousands separator (\"1250.00\"). Null when no deposit is mentioned; \"0.00\" only when the document says no deposit is due.",
  "type": ["string", "null"], "pattern": "^[0-9]+\\.[0-9]{2}$"
}
```

**Closed enum instead of a free string, with the meaning of each value:**

```json
"payment_status": {"type": "string"}
```

```json
"payment_status": {
  "description": "paid: the document says payment was received; due: it asks for payment; unknown: it says nothing about payment.",
  "enum": ["paid", "due", "unknown"]
}
```

**Nullable instead of optional**, with the meaning of null (before, a model that is unsure
leaves `reference` out and nobody can tell whether the document had none):

```json
"required": ["title"],
"properties": {"title": {"type": "string"}, "reference": {"type": "string"}}
```

```json
"required": ["title", "reference"],
"properties": {
  "title": {"description": "Heading of the document, as printed.", "type": "string"},
  "reference": {"description": "Booking or order number as printed. Null when the document has none.", "type": ["string", "null"]}
}
```

**Flat instead of nested.** A `price` object holding an `amount` object holding `value` and
`currency` asks for three levels to answer two questions. Two sibling fields, or one object
of two, say the same:

```json
"total": {"type": "object", "properties": {"amount": {"type": "object", "properties": {"value": {"type": "string"}, "currency": {"type": "string"}}}}}
```

```json
"total_amount": {"description": "Total due, decimal string.", "type": ["string", "null"]},
"total_currency": {"description": "ISO 4217 code of the total.", "type": ["string", "null"], "pattern": "^[A-Z]{3}$"}
```

**Instruction and schema in agreement.** Before, the instructions say "Dates as DD.MM.YYYY
as printed" while the schema's `pattern` demands `YYYY-MM-DD`; the extractor cannot satisfy
both and the schema feedback comes back every attempt. After, both say `YYYY-MM-DD`, and
the instructions say "the document may print dates as 31.12.2026 or 31 December 2026;
write them as 2026-12-31".

### Confidence patterns

TravelInbox's schema ([`FromTravelApp/extraction-schema.json`](../../FromTravelApp/extraction-schema.json))
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

Instructions:

- Every format and every allowed derivation is stated, in the same words in the extractor and
  the judge instructions; normalising is asked for explicitly, inventing is not allowed.
- There is no "JSON only, no prose" line; the answer blocks are the service's business.
- Anything that depends on where text sits (the year of an email, a forwarded booking, a
  page) refers to the context line, the section labels or the page markers by name.
- The judge's reject reasons and non-reasons are listed, and it is asked for pointers.

Schema:

- Every field has a description that says what it is, its format and unit, and when it is
  null. A reader who has never seen your instructions could fill the schema from it.
- The schema marks as required only what every document states; everything else is nullable
  (preferably, with the meaning of null stated) or optional.
- Objects are closed, identifiers have patterns, categories are closed enums with a rule for
  each value (and for the catch-all).
- It is flat and small: shallow nesting, no `oneOf` or `if`/`then` where a nullable field
  would do, shared shapes in `$defs`.
- Nothing in the schema contradicts the instructions (required against "null when not
  printed", a format in a `pattern` against another in the text, a field the instructions
  never mention).

Try it:

- You tried it on a few documents with `retention: default` and read the `feedback` of
  rejected jobs: repeated, unfixable feedback points at an instruction or schema problem,
  not at the model.
