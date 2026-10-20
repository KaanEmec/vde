# Proposed amendment to the TravelInbox contract (vde v1.2)

Status: draft for the owner to send to TravelInbox (2026-10-20). Not sent by the service's
developers. Decision and scope: [ADR 0012](../architecture/decisions/0012-evidence-first-framing.md).

vde v1.2 changes how the prompts are ordered and how the two models answer. The `/v1` API,
the job resource, the verdict keys and every other MUST stay as they are. Three passages
of `EXTRACTION-SERVICE-CONTRACT.md` describe the old prompts; this is the wording proposed
in their place.

## Rationale

- **Document first, task last.** On long inputs, models answer better with the document
  above the instructions and the question at the end; Anthropic reports up to 30% better
  answers in its tests. Booking emails and PDFs are long inputs.
- **Reason, then answer.** Asking for "exactly one JSON object, no prose" costs accuracy;
  letting the model quote the document's evidence first and give the fixed-format answer
  afterwards recovers most of it ("The Format Tax", "Thinking Before Constraining"). The
  judge's feedback gets better too: it quotes what the document says and where.
- **Nothing changes for the caller's data.** The analysis text is discarded: never stored,
  logged, returned or shown. The service still never repairs JSON, a judge failure is
  still never acceptance, and `service_version` changes with each step, so results stay
  attributable.

## §6, step 2 (replaces the current text)

> 2. **Build the extractor prompt** from: the service's own fixed framing for the
> extractor role (what the task is, and that the answer is a short analysis followed by
> exactly one JSON object conforming to the schema, delimited so the service can find it);
> then, in this order, the document representation; the schema; the caller's
> `extractor_instructions`, verbatim and complete; and, on a retry, the previous candidate
> and the feedback on it. The service MUST NOT shorten, rewrite or translate the caller's
> instructions, and MUST NOT add domain content. The analysis is discarded after parsing:
> the service MUST NOT store, log or return it.

Step 3 needs no change: the delimiters around the JSON are surrounding prose, which the
service MAY strip; it still MUST NOT repair the JSON.

## §6, step 5 (replaces the current text)

> 5. **Build the judge prompt** from: the service's fixed framing for the judge role, which
> defines the verdict format of section 7; then, in this order, the same document
> representation the extractor saw; the schema; the caller's `judge_instructions`,
> verbatim; and the candidate.

## §7, first paragraph (replaces "The service's judge framing MUST ask for, and the service MUST parse, exactly this JSON object as the judge's whole answer:")

> The service's judge framing MUST ask for, and the service MUST parse, exactly this JSON
> object as the judge's verdict. The framing MAY ask the judge to check the candidate
> first in a short analysis that comes before the verdict and is delimited from it; the
> service parses only the delimited verdict, discards the analysis (it MUST NOT store, log
> or return it), and treats a missing, unclosed or invalid verdict as unparseable
> (section 6, step 6):

The two examples and the bullet points of §7 stay as they are.

## TravelInbox's instructions

`extractor.md` and `judge.md` in TravelInbox's corpus say "Return only the JSON object: no
prose, no code fences" (and the same for the verdict). The service reads such a line as
describing the delimited JSON or verdict, and a model that follows it still parses, but the
two texts disagree. We suggest dropping those lines; the rest of TravelInbox's
instructions, including the restated verdict format, agree with the new framing.
