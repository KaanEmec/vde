# Conventions in schema and instructions: a proposal for TravelInbox (vde 1.3)

Status: v1.3, for the owner to send to TravelInbox; this repository sends nothing and
changes no TravelInbox file. Target: TravelInbox's job `v1.0-launch/04-vde/06-extraction-quality`.
It answers questions 2, 3, 4 and 6 of `vde-model-comparison-2026-10-20.md`. Nothing here
quotes a document or an expected value; the examples in the instructions are invented.

## 1. Summary

Measured on five corpus cases (`001-flight`, `011-flight`, `021-hotel`, `041-rail`,
`046-cancellation`; 151 scored fields), `--judge auto`, extractor Claude Haiku 5.5, judge
GLM 5.3 Flash, one run per step, starting from TravelInbox's prompt_version 4 and its
production schema:

| Step | Mismatches | Of which conventions (`provider`, `travellers`, `details`) | Cases fully right |
| --- | --- | --- | --- |
| Baseline (prompt_version 4, production schema) | 20 | 11 | 0 of 5 |
| Schema constraints and descriptions (section 2) | 14 | 5 | 0 of 5 |
| Plus convention examples, prompt_version 5 (section 3) | 7 | 4 | 1 of 5 |

Every baseline miss was a printed value in a form the expected answers do not use. The
grounding check accepts such a value and no judge was called, so each was a false accept.
Caveat: five cases and one run each; your own report put the noise of one run at about
±3 fields. The direction is clear, the exact counts are not.

## 2. Proposed schema changes

Each goes on the property, never on `$defs/text`, which is shared. All 50 corpus
`truth.json` files validate against the changed schema, so no constraint contradicts your
own expected answers. A constraint's `description` is what the service quotes back to the
model when a pattern fails.

| JSON pointer | Change | Miss removed |
| --- | --- | --- |
| `/$defs/rail/properties/coach` | `pattern` `^[A-Za-z0-9]{1,4}$`; description "Coach number or letter only, without Coach, Wagen or Wg." | coach printed with its label |
| `/$defs/rail/properties/seat` | `pattern` `^[0-9]+[A-Z]?(, [0-9]+[A-Z]?)*$`; description "Seat designators only, without "Seat" or "Sitz"; several separated by ', '." | seat with its label |
| `/$defs/flight/properties/seat`, `/$defs/flight_leg/properties/seat` | the same pattern; descriptions say the same, the flight one adds "every traveller's seat, in traveller order" | seat with its label |
| `/$defs/flight/properties/terminal`, `/$defs/flight_leg/properties/terminal` | `pattern` `^[A-Za-z0-9]{1,5}$`; description "Departure terminal: the designator only, without the word Terminal." | terminal with its label (two cases) |
| `/$defs/flight/properties/cabin`, `/$defs/flight_leg/properties/cabin` | description only: "Cabin class only, without the fare or tariff name when the document prints both." (the corpus's cabin values are free text, no pattern fits) | cabin with the tariff name |
| `/$defs/traveller/properties/type` | description only (the enum exists): "Only when the document states it for this person; a party count such as 2 adults does not type a named traveller. Otherwise null." | `type` set from a party count (one case) |

Effect: all five `details` misses (cabin, terminal twice, coach, seat) disappeared in the
first attempt; the descriptions steered the extractor and no pattern retry was needed.
`travellers` went from 4 to 3 misses.

## 3. Proposed instruction changes (prompt_version 5)

A "Conventions" section in `extractor.md` (examples as "as printed → as expected"), and the
same conventions in `judge.md` phrased as what to reject, so the judge has a rule to reject
a verbatim copy. The full text is in vde's `testdata/travelinbox/corpus/extractor.md` and
`judge.md` at prompt_version 5; adopt it by copying those two sections. Topics:

- `provider`: the short brand customers know; not the operating or legal company, a
  sub-brand or service line, a legal form, or logo capitals; a booking through an agency
  or platform names the platform. Three examples on invented companies.
- `travellers[].type`: only when stated for that person; a party count does not type a
  named person. Two examples.
- Place `name`: without codes; null when only a city and a code are printed. Two examples.
- `trip_hint.ends_on`: null when the document holds only one-way journeys. One example
  with and one without a return.
- Cancellation `price`: the amount charged (the cancellation fee), not the original total
  or a refund; the others go in `notes`. One example.

The examples use invented values that appear in no corpus document. Effect of this step
(14 → 7): `trip_hint.ends_on`, the cancellation price and three place names were fixed.
Left: `travellers` 3, `provider` 1.

## 4. What did not help

`x-vde-grounding: convention` on `provider` (a vde 1.3 experiment that sends such fields to
the judge even when grounded) was measured and reverted. Under it every job went to the
judge, which accepted all five at once, three of them with wrong values; no outcome
changed, at 1.7 times the input tokens and 2.2 times the output tokens. Keep `provider`
as it is; the instructions above are what moved it.

## 5. Answers to the questions

**Q2, failure codes after retention `none`.** vde 1.3: `vde audit list --job <id>` or
`GET /admin/v1/audit?job_id=<id>` (admin token) list every event of the job whatever its
age; `job.finished` carries `error_code`. The job row stays empty by contract §10. Audit
events hold codes and counts, never content.

**Q3, Mistral Small 4 as judge.** We have not diagnosed your runs. vde 1.3 has
`vde config check --probe`: one synthetic request per endpoint-model pair of each role,
through the real framing and parser, reporting `ok`, `unparseable` (with the parser's
error, no answer text), the provider's failure class, or a warning when a judge accepts a
deliberately wrong candidate. Run it against the Mistral setup; it says whether its
answers parse before you spend a corpus run on it.

**Q4, timeouts for reasoning models.** Advice only, nothing changed in the service: set
the endpoint `timeout` above the model's p95 for one call, measured on your corpus; keep
`job_timeout` at 8 minutes or less (contract §6 default 5, maximum 8, queue time included;
a timed-out job fails with `timeout` and keeps any candidate produced); give
`max_output_tokens` room for the model's reasoning plus the analysis block plus the JSON.
Measure first with the probe's latency and token counts, then set limits.

**Q6, adopting vde's amended instructions.** Yes: the prompt_version 5 Conventions
sections and the schema constraints of section 2, measured here against prompt_version 4
(section 1). Not measured: the other conventions of your prompt, and other models.

## 6. What remains

On the five cases, 7 mismatches are left:

- Travellers (2 to 3 cases): the extractor still lists or types people differently from
  the expected answers. Candidate: TravelInbox's own wording of the traveller rule, which
  vde does not hold; you know which expected answers it must produce.
- A cancellation or price choice between two printed amounts: the example fixed the
  cancellation price but not every such choice. Candidates on vde's side, both in the
  [backlog](../roadmap/backlog.md): a devil's-advocate judge (names an alternative printed
  value per field before deciding) and schema invariants (`x-vde-invariants`).

Measurements: [quality.md](../quality.md#v13-convention-aware-verification-2026-10-22).
