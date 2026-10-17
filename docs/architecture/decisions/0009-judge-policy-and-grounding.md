# ADR 0009: Judge policy and a deterministic grounding check

Status: accepted, 2026-10-17

## Context

Until v0.3 every schema-valid candidate went to the judge, and `accepted` meant the judge
accepted it (contract §5). Runs on the owner's ten real documents on 2026-10-17 changed the
picture:

- With compact instructions, visible-text emails, page images for PDFs and cheap models
  (Haiku 5.5 extractor, GLM 5.3 Flash judge), 8 of 10 documents were fully correct, field
  accuracy 96.8 %, about 0.6 cents per document.
- The judge is the most expensive step, and emails were reliably right on the first
  attempt: for them the judge call mostly confirms.
- Every judge tested (Gemini Flash, Sonnet, Haiku, GLM, DeepSeek) accepted an event invoice
  whose candidate carried dates, a venue, an organiser and travellers the document never
  prints. Catching invented values is the judge's core job (contract §1), and no judge did.
  A plain check that every extracted value occurs in the document text would have.

ADR 0008 asks that judge arrangements be adopted on measured runs. This ADR adds two
mechanisms so those runs can compare arrangements, and so a deployment can stop paying for
a judge where a deterministic check is stronger.

## Decision

- **Judge policy.** `roles.judge.policy` is `always` (default), `never` or `auto`. Each
  application may override it with `applications[].judge.policy`, and may let callers choose
  per request (`judge.allow_override`, default `false`; request part `judge`, refused with
  `400` when not allowed). `judge.sample_rate` (0 to 1, default 0.05) is used by `auto`.
- **`always` keeps the TravelInbox contract exactly**: `accepted` means the judge accepted a
  candidate that validates. It is the default, and TravelInbox's applications keep it.
- **`never`** ends a schema-valid final candidate in a new terminal status, `extracted`,
  never `accepted`. The contract tells clients to treat any status they do not know as not
  accepted, so this is additive. Grounding entries, when the document has text, are
  attached as advisory feedback; they do not block.
- **`auto`** accepts a candidate without a judge only when the representation is
  groundable, the attempt needed no schema retry, and every critical leaf is grounded. Such
  a job is `accepted` with `verification: "grounded"`. A deterministic sample (FNV-1a of the
  job id, of the document bytes in the CLI, below `sample_rate`) goes to the judge anyway,
  and disagreements are counted. Everything else goes to the judge as under `always`. This
  extends what `accepted` can mean: the judge accepted, or the grounding check passed. Only
  applications that opt in ever see the second meaning, and the job field `verification`
  says which one applies.
- **A new job field `verification`** (`judge`, `grounded` or `none`; `null` while queued
  or running) is always present.
- **Grounding check** (`internal/ground`). It is deterministic and has no model. Every
  string and number leaf of the candidate must occur in the normalized document text.
  Normalization casefolds, strips diacritics, collapses whitespace and strips punctuation
  around tokens. Numbers are compared as canonical decimals, and ISO dates and times are
  matched against a small, documented table of printed forms. Booleans, nulls and the
  schema's enum values are skipped. Callers steer it with the schema annotation
  `x-vde-grounding: derived | skip | critical`. When no property is marked `critical`,
  every checked leaf is critical. A miss is a feedback entry with `source: "grounding"`.
- **Grounding repair.** Under `always` (narrowed by the amendment below) and `auto`, a
  candidate with ungrounded critical leaves goes back to the extractor once, with the grounding feedback, before the judge is
  asked. This happens at most once per job and only while an attempt remains; it costs one
  extractor call and no judge call.
- **Where grounding entries appear.** Under `always` they reach only the extractor, the
  metrics and the evaluation report, never the job's `feedback`, so a contract-v1 client
  sees only `judge` and `schema` sources. Under `never` and `auto` they appear in
  `feedback` with `source: "grounding"`.
- **Versioning.** The effective policy, the sample rate and the grounding rules (the
  normalization version and the date table) are hashed into `service_version`. An
  application that allows override has one `service_version` per policy, and a job carries
  the one for the policy it ran under.

## What grounding can and cannot catch

It catches values that are not in the document at all: invented dates, names, venues,
amounts and references. It cannot catch a printed value in the wrong field (a departure
date stored as the arrival date), a wrong choice among printed values, an omission or a
semantic misreading. Those remain the judge's job, which is why `auto` samples and why
`always` stays the default. It cannot run on documents without text (images, and PDFs
without a text layer): those are "ungroundable" and always go to the judge under `auto`.
It flags legitimate derivations (a city from an airport code, a currency from a symbol)
unless the schema marks them `derived` or `skip`.

## Consequences

- TravelInbox needs no change: its applications run `always`, statuses and feedback
  sources stay within contract v1, and the extra grounding repair is visible only through
  `service_version`. The [contract review](../../conformance/travelinbox-v1-review.md) gets
  an open point on `extracted`, `verification` and `source: "grounding"` for other policies.
- `auto` makes cheap deployments cheaper (emails skip the judge when grounded) and catches
  the invented-value class that no tested judge caught. It accepts a false-accept risk on
  wrong-field and misreading errors, which the sampled judge measures
  (`vde_grounding_disagreements_total`).
- The eval harness reports `verification` and judge calls per case, so the three policies
  can be compared on the same corpus (ADR 0008). The comparison run is the owner's to make.
- The job store gains `judge_policy` and `verification` columns. The loop's never-accept
  rules still hold: a failed judge call is never acceptance, and an ungroundable or
  ungrounded candidate is never accepted without a judge.

## Amendment 2026-10-18

Under `always` the grounding repair runs only when the job's schema carries at least one
`x-vde-grounding` annotation anywhere (`ground.RulesFromSchema` reports it); schema-less
jobs and unannotated schemas go to the judge without it. An unannotated schema has not
said which values are derivations, and legitimately derived values (a city in English, a
country or IATA code, a currency) are not printed in the document, so the repair fired on
most such jobs: the TravelInbox contract path (unannotated schema, `always`) paid one extra
extractor call per job, which v0.4 promised not to do (`make eval`: 42 of the 50
TravelInbox cases repaired before, 0 after). The judge still decides every candidate, so
nothing is accepted that was not before. `never` and `auto` are unchanged: under `auto` an
unannotated derived value is a critical miss that is repaired once and sends the job to
the judge. The rule is part of the grounding rules, so `ground.RulesVersion` is `ground-2`
and every `service_version` changes once; nothing new is hashed.
