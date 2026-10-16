# Evaluation

`vde eval` measures extraction quality on a corpus of documents with ground truth, so a
change of model, framing, instructions or representation is decided on numbers rather than
on judge confidence. The brief's "99% correct" is an aspiration to measure on
representative documents; this is how it is measured. Results are keyed by
`service_version`, and real runs are recorded in [docs/quality.md](../quality.md).

## The corpus layout

A corpus is a directory with one subdirectory per case. The directory name is the case id.

```
<corpus>/<case>/
  document.*        the document: .txt .md .html .eml .pdf .png .jpg .jpeg .webp .heic
  page-*.png|jpg    optional page images, in name order (as the `page` parts of a job)
  extractor.md      extractor instructions, sent verbatim
  judge.md          judge instructions, sent verbatim
  schema.json       optional JSON Schema; without it the case runs schema-less
  expected.json     the ground truth: exactly what a perfect extractor returns
  compare.yaml      how each value of expected.json is compared
  notes.md          optional, for people: where the document comes from and why it is here
```

A subdirectory without `expected.json` is not a case and is skipped; a case with
`expected.json` and a missing or malformed file stops the run before anything is sent.

### compare.yaml

Every value of `expected.json` is compared, leaf by leaf (objects by key, arrays by index),
with `default` unless a rule covers its pointer. A rule's pointer is an RFC 6901 JSON
pointer into `expected.json` in which a segment `*` matches any array index or object key;
the most specific matching rule wins (most literal segments, the later rule on a tie). The
value at a rule's pointer counts as one field, whatever its shape.

```yaml
default: exact                 # exact (the default) or normalized
rules:
  - {pointer: /notes, mode: ignore}                          # free text: not compared, not counted
  - {pointer: /items/*/description, mode: normalized}
  - {pointer: /total, mode: numeric, tolerance: 0.005}
  - {pointer: /reservations/*/travellers, mode: set, elements: normalized}
```

| Mode | Equal when |
| --- | --- |
| `exact` | The JSON values are equal (numbers by value: `1` equals `1.0`). |
| `normalized` | Both are strings and equal after normalization: lower case, whitespace runs collapsed, surrounding space and one trailing full stop removed, typographic quotes, dashes and non-breaking spaces as ASCII. Non-strings: exact. |
| `numeric` | Both are numbers or numeric strings (`"12.50"`) within `tolerance`. Otherwise exact. |
| `set` | Both are arrays with the same elements in any order, counting duplicates; `elements: normalized` normalizes every string inside the elements first. Otherwise exact. |
| `ignore` | Never compared and not counted, with everything below the pointer. |

A key absent from the result counts as `null`. Keys the result has and `expected.json`
lacks are not compared (the schema governs shape); an array element too many counts as a
mismatched field. A case without any result (a failed job, or an answer that was never
JSON) matches no field.

## What is in testdata/eval

| Cases | Domain | Source |
| --- | --- | --- |
| `travelinbox-001-flight` ... `travelinbox-050-boarding-pass` (50) | Travel bookings: flights, hotels, dining, cars, rail, transfers, cancellations; 45 emails, 4 PDFs, 1 photo; ten languages | The co-owned TravelInbox corpus (`testdata/travelinbox`, decision 11), converted by `vde eval import-travelinbox`. Shared TravelInbox prompts and schema; `compare.yaml` follows the corpus labelling conventions (title, notes and confidence ignored, printed texts normalized, travellers a set, price amount numeric). |
| `receipt-001` ... `receipt-006` (6) | Receipts: grocery, café, hardware, restaurant with tip, German pharmacy, ride-hailing email | Synthetic, written for this repository: fictional merchants, no real people. |
| `lab-001` ... `lab-005` (5) | Laboratory reports: blood count, lipids, metabolic panel, urinalysis, German thyroid panel | Synthetic, written for this repository: invented patients and laboratories. |

The TravelInbox cases are regenerated whenever that corpus grows:

```
vde eval import-travelinbox --from testdata/travelinbox --to testdata/eval
vde eval import-travelinbox --from ../TravelInbox --to testdata/eval   # from a checkout
```

`go test ./internal/eval` fails when the committed cases differ from a fresh conversion, and
checks every `expected.json` against its schema.

## Running it

```
vde eval --corpus testdata/eval --config vde.yaml [--cases 'travelinbox-*'] [--concurrency 2]
vde eval --corpus my-corpus --url https://vde.example.com --token-env VDE_TOKEN
```

| Flag | Meaning |
| --- | --- |
| `--corpus dir` | The corpus (required). |
| `--config file` | Run locally through the engine with this configuration (default `vde.yaml`): the same representation choice, loop and limits as the service, one fresh set of role groups per case. Tokens are counted per case. |
| `--url URL --token-env NAME` | Run against a deployment instead: each case is submitted to `POST /v1/jobs` with `retention: none` and polled as the [API guide](../api/guide.md) describes (2 s doubling to 30 s, 10 minutes at most). The token is read from the named variable and never printed. The service does not report tokens, so the token columns stay empty. |
| `--concurrency n` | Cases run at the same time (default 1). Mind the provider's rate limits, or the application's quota with `--url`. |
| `--cases glob` | Only the cases whose id matches (shell glob, default `*`). |
| `--out dir` | Where the JSON report goes (default `eval-reports/`, ignored by git); `""` saves none. |
| `--compare file` | An earlier report: prints the change of every summary number and each case whose status or verdict changed. |
| `--provider-script file` | Replace every provider with a fake replaying the script, a fresh fake per case; `{{expected}}` in an answer becomes the case's `expected.json`. For testing the harness: `make eval` runs the whole corpus this way in CI. |

Exit codes: 0 when every case ran, whatever the quality; 1 for a usage or configuration
error or when a case could not be run at all (unreadable file, unsupported document,
transport failure).

### The numbers

Progress lines on stderr and the table on stdout carry case ids, statuses, counts and JSON
pointers only, never document content or result values; so does the report.

| Number | Meaning |
| --- | --- |
| **False accepts** | Accepted cases where a compared field differs from the ground truth, and their share of the accepted cases. The headline: the judge exists to keep it at zero, because a consumer uses an accepted result without review. |
| Acceptance rate | Accepted cases / cases. |
| Field accuracy | Matched / compared fields over every case (a case without a result matches none), and over accepted cases only. |
| False rejects | Rejected cases whose result matched on every compared field: work sent to a person that did not need one. |
| Correct cases | Cases with a result that matched on every compared field, whatever the status. |
| Attempts, duration | Mean extractor attempts; median and p95 wall time per case (with `--url`, queueing included). |
| Tokens | Input and output tokens and model calls of local runs. The cost estimate is a placeholder (`cost_usd: null`) until per-model prices are configured. |

The report is saved as `<out>/<service_version>-<date>.json` (`-2`, `-3` ... when the file
exists). Keep the report of every release candidate and record its summary in
[docs/quality.md](../quality.md).

## Building a corpus from production rejections

The documents worth measuring are the ones the service got wrong or nearly wrong. A
consumer builds its own corpus from them; the service keeps no documents after their
retention, so the consumer collects them.

1. **Collect.** For every job that ends `rejected` (and a sample of `accepted` ones, which
   is where false accepts hide), keep the document, the instructions and schema version it
   was sent with, the `service_version` and the `feedback`, in a store that is allowed to
   hold documents. The person who reviews the rejection writes down the correct result.
2. **Redact or synthesize.** A corpus is shared with whoever runs the evaluation and often
   committed. Replace names, references, card numbers, addresses and contact details with
   stable fakes in the document and in the ground truth alike, or re-create the document
   from redacted text (the TravelInbox corpus README describes its redaction tool and its
   limits: a PDF or photo cannot be rewritten and must be checked by eye or re-rendered).
   Never commit a real person's document.
3. **Write the case.** `document.*` (and `page-*.png` if the consumer sends pages),
   the instructions and schema the consumer sends today, `expected.json` written by hand
   from the reviewed result (every field written out, `null` where the document says
   nothing), and `compare.yaml`: ignore free-text fields the instructions leave open,
   normalize texts copied "as printed", give amounts a tolerance.
4. **Check it.** `vde eval --corpus my-corpus --provider-script testdata/eval/fake-script.yaml
   --config testdata/scenarios/config/vde.yaml` must report every case correct: the fake
   answers each case's ground truth, so a mismatch means a broken `expected.json` or rule,
   and a harness error means a document the configuration cannot read.
5. **Measure.** Run against the real configuration before and after every change of model,
   instructions or framing, with `--compare` against the last report, and decide on false
   accepts first, field accuracy second, acceptance rate third.

Add a case for every false accept found in production: it is the one number that has to
stay at zero.
