# Judge policy and grounding

Part of the [operator guide](README.md). Since v0.4 a deployment decides per application
whether the judge is asked, and a deterministic grounding check catches values the document
does not contain ([ADR 0009](../architecture/decisions/0009-judge-policy-and-grounding.md)).
Callers read the result through the job's `status` and `verification` fields
([API guide](../api/guide.md#judge-policy)).

## The three policies

| Policy | What happens to a schema-valid candidate | Terminal statuses | `verification` |
| --- | --- | --- | --- |
| `always` (default) | The judge decides, as in contract v1. When the schema carries an `x-vde-grounding` annotation, a candidate with critical values missing from the document goes back to the extractor once (the grounding repair) before the judge is asked. | `accepted`, `rejected`, `failed` | `judge` on `accepted`, `none` otherwise |
| `never` | No judge. The final candidate ends `extracted`, never `accepted`; grounding misses are attached as advisory `feedback`. Schema violations still retry up to the attempt limit and can end `rejected`. | `extracted`, `rejected`, `failed` | `none` |
| `auto` | Accepted without a judge when the grounding gate passes (below); otherwise, and for a deterministic sample, the judge decides as under `always`. | `accepted`, `rejected`, `failed` | `grounded` when the gate accepted, `judge` when the judge did, `none` otherwise |

Under `auto` the gate passes, and the job ends `accepted` with `verification: grounded`,
`judge_confidence: null` and no feedback, only when all of these hold:

- the representation is groundable: the document has text (plain text, markdown, HTML,
  email, or a PDF whose text layer averages `documents.pdf.min_text_chars_per_page`
  characters per page). Images and scanned PDFs are ungroundable and always go to the judge;
- at least one critical leaf was checked, and every critical leaf is found in the text;
- no `convention` leaf is a verbatim copy of the document (see the table below);
- the candidate does not answer schema or judge feedback. A candidate written after a
  judge's rejection goes back to the judge, so grounding never overrules a judge. A
  candidate that answers only the grounding repair may pass;
- the job is not in the sample (`judge.sample_rate`, default 0.05). The sample is chosen
  from the job id (FNV-1a), so it is deterministic: a sampled job that the judge rejects
  counts as a disagreement and feeds the next attempt like any rejection.

The grounding repair happens at most once per job, only while an attempt remains, and
costs one extractor call and no judge call. Under `auto` it runs for every critical miss;
under `always` only when the schema carries at least one `x-vde-grounding` annotation
(any value, anywhere in the schema), because an unannotated schema has not said which
values are derivations: a schema-less job or an unannotated schema such as TravelInbox's
goes straight to the judge, with no extra extractor call. Whether it was made is
stored with the job, so a resumed job does not repeat it. Under `always` grounding entries
reach only the extractor, the metrics and the evaluation report, never the job's
`feedback`: a contract-v1 client sees only `judge` and `schema` sources, while a job is
running too. The repair is visible only through `service_version`, which hashes the
grounding rules.

## Choosing a policy

| Situation | Policy |
| --- | --- |
| Contract fidelity (TravelInbox), high stakes, documents a person will not review | `always` |
| Cheap bulk extraction with review downstream: someone checks every result anyway | `never` |
| Text-heavy documents (emails, text PDFs) and a cost target, with a schema annotated for grounding | `auto` |

`auto` trades a judge call for a deterministic check on grounded candidates. Measure it on
your own corpus before switching (`vde eval --judge always|auto|never`, see
[evaluation](evaluation.md#comparing-judge-policies)); the owner's comparison is recorded in
[docs/quality.md](../quality.md#judge-policy).

## Configuring it

```yaml
roles:
  judge:
    group: review
    policy: always          # always | never | auto: the default for every application

applications:
  - id: inbox
    judge:
      policy: auto          # this application's policy
      sample_rate: 0.05     # auto: share of grounded jobs judged anyway, 0 to 1
      allow_override: false # true lets a request choose with the judge part
```

The keys are in the [configuration reference](configuration.md#roles). With
`allow_override: true` a request may send the `judge` part (multipart, `text/plain`) or
JSON field (`always`, `never` or `auto`); without it the part is refused with
`400 bad_request`, whatever its value. Such an application has one `service_version` per
policy, and each job carries the one it ran under. `never` together with
`roles.second_judge` is a `config check` warning (the second judge is never asked).
`vde config check` prints the policy and the grounding rules under the judge role.

## What grounding catches and what it misses

It catches values that are not in the document at all: invented dates, names, venues,
organisers, amounts and references. Every judge tested on the owner's documents accepted an
event invoice whose candidate carried dates, a venue, an organiser and travellers the
document never prints; the grounding check rejects that candidate.

It misses:

- a printed value in the wrong field (a departure date stored as the arrival date, a
  swapped origin and destination);
- a wrong choice among printed values (the subtotal instead of the total);
- an omission (a traveller left out) and a semantic misreading.

Those remain the judge's job, which is why `auto` samples and why `always` stays the
default. Grounding also flags legitimate derivations (a city from an airport code, a
currency from a symbol, an ISO country code) unless the schema marks them `derived` or
`skip`, and it cannot run on documents without text.

## Annotating a schema

The schema keyword `x-vde-grounding` says how a property, and everything below it, is
treated. A deeper annotation overrides an inherited one.

| Value | Meaning |
| --- | --- |
| `critical` | Checked, and must be found for the `auto` gate. When no property is `critical`, every checked leaf is critical. |
| `check` | Checked (a miss is feedback), but not critical once some property is `critical`. Overrides an inherited `derived` or `skip`. |
| `derived` | An allowed derivation (a city, an IATA code, a currency code, a computed value): not checked. |
| `skip` | Never checked (notes, titles, confidence). |
| `convention` | Expected in a form other than the printed one (a brand name for a printed legal name, a number without its printed label). Never a miss and never feedback. Found verbatim it is a copy: under `auto` the gate fails and the judge decides. Found means the usual phrase match, so a correct value that is part of the printed form (a brand inside a legal name, a number after its label) is a copy too: use it where the judge's call is worth it. Not found it is treated as `derived`. `always` and `never` are unaffected. Never critical, also inside a `critical` object. |

Booleans, nulls, empty strings, one-character strings and values equal to an `enum` or
`const` of their location are never checked. A schema without any annotation skips
properties named `confidence` and `notes`; a schema with any annotation gets no such
defaults. An unknown value is `422 invalid_schema`. Annotations are read through
`properties`, `additionalProperties`, `prefixItems`, `items`, local `$ref` and every
`allOf`, `anyOf` and `oneOf` branch.

A worked example for invoices:

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "properties": {
    "invoice_number": {"type": "string", "x-vde-grounding": "critical"},
    "issue_date": {"type": "string", "format": "date", "x-vde-grounding": "critical"},
    "total": {"type": "number", "x-vde-grounding": "critical"},
    "currency": {"type": "string", "pattern": "^[A-Z]{3}$", "x-vde-grounding": "derived"},
    "supplier": {
      "type": "object",
      "x-vde-grounding": "critical",
      "properties": {
        "name": {"type": "string"},
        "country": {"type": "string", "x-vde-grounding": "derived"}
      }
    },
    "event": {
      "type": ["object", "null"],
      "x-vde-grounding": "critical",
      "properties": {
        "start_date": {"type": "string"},
        "venue": {"type": "string"}
      }
    },
    "line_items": {
      "type": "array",
      "x-vde-grounding": "check",
      "items": {"type": "object", "properties": {
        "description": {"type": "string"},
        "amount": {"type": "number"}
      }}
    },
    "document_type": {"enum": ["invoice", "booking", "ticket"]},
    "notes": {"type": "string", "x-vde-grounding": "skip"}
  }
}
```

Here an invented event date or venue, or a supplier name the document does not print, is a
critical miss: under `auto` the candidate is repaired once and then judged, never accepted
by grounding. A line-item description the model paraphrased is a non-critical miss: it is
feedback, and the gate can still pass. `currency` (`EUR` from `€`) and the supplier's
country are derivations, `document_type` is an enum and `notes` is the model's own text.

A miss is a feedback entry with the pointer and a fixed message, never the value:

```json
{"pointer": "/event/start_date", "message": "value not found in the document", "source": "grounding"}
```

Under `never` and `auto` such entries appear in the job's `feedback` (advisory on
`extracted`, after the judge's entries on judged outcomes); under `always` they never do.

## Normalization and matching

The document text and every value are normalized the same way: Unicode NFKD with
combining marks dropped, casefolded (Turkish dotted and dotless i become `i`, `ß` becomes
`ss`), soft hyphens and zero-width characters dropped, whitespace and control characters
collapsed to one space, and punctuation and symbols stripped at token edges but kept inside
tokens (`LX42QZ`, `A-12`, `10:45`, `21.03.2027`).

- **Strings** match on word boundaries. A string of two to six words also matches when all
  its words occur close together in any order, so `SMITH/JOHN MR` grounds `John Smith`.
  This is not fuzzy matching: a misspelt name does not match.
- **Numbers**, and strings that are a number with a currency symbol or code (`€1.234,50`,
  `EUR 412.50`), match when the text prints a number within 0.005. Printed numbers accept
  `.`, `,` and spaces as thousands separators and both decimal conventions; an ambiguous
  `1.234` counts as both 1234 and 1.234. Signs are ignored. A digit run right after a
  letter is not a number (`LX42` does not print 42).
- **Dates** in ISO form (`YYYY-MM-DD`) match when one of the printed forms below occurs.
  An ISO datetime needs its date and its time; a time-only string (`HH:MM[:SS]`) needs its
  time. Seconds and zones are ignored. An impossible date (`2027-02-30`) is matched as a
  plain string.

### The date table

Date forms, tried in this order (`{yyyy}` year, `{yy}` two-digit year, `{mm}` and `{dd}` two
digits, `{m}` and `{d}` without a leading zero, `{month}` any full or short month name
below). The US month-first numeric order (`03/21/2027`) is deliberately absent.

| Form | Example |
| --- | --- |
| `{yyyy}-{mm}-{dd}` | 2027-03-21 |
| `{dd}.{mm}.{yyyy}` | 21.03.2027 |
| `{d}.{m}.{yyyy}` | 1.3.2027 |
| `{dd}/{mm}/{yyyy}` | 21/03/2027 |
| `{d}/{m}/{yyyy}` | 1/3/2027 |
| `{dd}-{mm}-{yyyy}` | 21-03-2027 |
| `{yyyy}/{mm}/{dd}` | 2027/03/21 |
| `{dd}.{mm}.{yy}` | 21.03.27 |
| `{d}. {month} {yyyy}` | 21 Mar 2027, 21. März 2027, 1 mars 2027 |
| `{dd} {month} {yyyy}` | 01 March 2027 |
| `{month} {d}, {yyyy}` | March 21, 2027 |
| `{month} {dd}, {yyyy}` | March 01, 2027 |
| `{d}-{month}-{yyyy}` | 21-Mar-2027 |
| `{dd}-{month}-{yyyy}` | 01-Mar-2027 |
| `{d} de {month} de {yyyy}` | 21 de marzo de 2027 |

Time forms (`{HH}` and `{H}` 24-hour, `{hh}` and `{h}` 12-hour, `{MM}` minutes, `{ampm}`
`am`, `a.m.`, `pm` or `p.m.`):

| Form | Example |
| --- | --- |
| `{HH}:{MM}` | 09:05, 14:45 |
| `{H}:{MM}` | 9:05 |
| `{h}:{MM} {ampm}` | 2:45 pm, 2:45 p.m. |
| `{h}:{MM}{ampm}` | 2:45pm |
| `{hh}:{MM} {ampm}` | 02:45 PM |

Month names: English, Turkish, German, French, Spanish and Dutch, full and short. The
table is `ground.DateForms`, `ground.TimeForms` and `ground.Months`, versioned by
`ground.RulesVersion` (`ground-3`), which is hashed into `service_version`; a test keeps
this page and the code in step.

## Metrics to watch

| Series | Read it as |
| --- | --- |
| `vde_judge_decisions_total{policy, decision}` | One per finished job that reached a decision: `judged`, `grounded` (auto, no judge), `sampled` (auto, judged although grounded) or `skipped` (never). The grounded share is `grounded / (grounded + sampled + judged)` under `auto`. |
| `vde_grounding_disagreements_total` | Sampled judge calls that rejected a candidate grounding alone would have accepted. Divide by the `sampled` decisions: a rising rate means grounded acceptances include wrong-field or misreading errors, so raise `sample_rate` or go back to `always`. |
| `vde_jobs_total{status="extracted"}` | Jobs under `never`. |

A low grounded share usually means derived fields without annotations: see
[troubleshooting](troubleshooting.md#grounded-share-is-low).

## The `extracted` status

`extracted` is terminal and is never acceptance: no judge looked at the result. The
contract tells clients to treat any status they do not know as not accepted, so a
contract-v1 client treats it correctly without a change. Its `result` validates against
the schema (when one was sent), `verification` is `none`, `judge_confidence` is `null`, and
`feedback` holds the advisory grounding entries. Webhooks and `POST /v1/extract` carry it
like any terminal status; `vde extract` exits 4 for it.
