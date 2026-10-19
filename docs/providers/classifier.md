# Classifier judge (generic HTTP contract)

Adapter: `internal/provider/classifier`. Configure with `provider: classifier`.

> **Deprecated in 1.1, removal planned for 2.0** (owner decision L8,
> [ADR 0011, proposed](../architecture/decisions/0011-remove-tiers-and-classifier-judge.md)).
> It still works in 1.x; each classifier endpoint makes `vde config check` and `vde serve`
> warn, and so do `roles.judge.feedback_fallback` and `feedback_group`. No vendor
> classifier API was available to test it against.

A classifier model answers a fixed set of typed questions with calibrated probabilities
instead of writing text (ADR 0004, decision 14). TypeSafe AI's Jev is the example the owner
named; its API was early access when this adapter was written and is not called directly.
Instead the adapter speaks the small HTTP contract below, which a vendor adapter or a thin
proxy in front of a vendor API must satisfy. A classifier can serve **only the judge
role** (and the second judge); `config check` rejects it in an extractor group or as
`roles.judge.feedback_group`.

## Configuration

```yaml
endpoints:
  jev:
    provider: classifier
    base_url: https://classifier-proxy.internal   # required; the adapter posts to {base_url}/classify
    api_key_env: CLASSIFIER_KEY                    # optional; sent as Authorization: Bearer
    models:
      jev-1:
        accepts: [text/plain, text/html]           # text types only
        questions:                                 # required, at least one
          - {id: accuracy, text: "Is every value of the candidate what the document says?", threshold: 0.8}
          - {id: completeness, text: "Is nothing the instructions ask for missing?", threshold: 0.7}
          - {id: invented, text: "Is every value supported by the document?", threshold: 0.8}
groups:
  classify:
    endpoints: [{endpoint: jev, model: jev-1}]
roles:
  judge:
    group: classify
    feedback_fallback: llm          # or whole_result (the default)
    feedback_group: review          # an LLM group, only with llm
```

Each question is phrased so that "yes" means the candidate passes. `threshold` is from 0
(exclusive) to 1. The questions, in order, are part of `service_version`.

## The contract

`POST {base_url}/classify`, `Content-Type: application/json`:

```json
{
  "model": "jev-1",
  "questions": [{"id": "accuracy", "text": "Is every value of the candidate what the document says?"}],
  "text": "Caller instructions:\n\n...\n\nSchema:\n\n...\n\nDocument:\n\n<document text>\n\nCandidate:\n\n{\"name\": \"Ada\"}"
}
```

- `model` is the model name from the group entry.
- `questions` are the configured questions, in order, without thresholds.
- `text` is the text parts of the judge request joined by blank lines: the caller's judge
  instructions, the schema (when given), `Document:` and the document text, and the
  candidate JSON, each section opening with its label line as in the
  [judge request](../operator/framings.md#judge-request). The judge framing (system
  prompt) is not sent: the questions replace it. Image and PDF parts are never sent; a
  request that has one fails permanently, which the `accepts` list prevents.

A `2xx` answer is:

```json
{"decisions": [{"question": "accuracy", "label": "yes", "probability": 0.93}]}
```

- One decision per configured question, matched by `question` (the id); extra decisions
  are ignored. `probability` is the calibrated probability of "yes", from 0 to 1. `label`
  is informational.
- A missing or duplicate decision, or a probability out of range, is a malformed answer:
  a permanent failure the loop treats as an unparseable verdict (asked once more, then
  `judge_failed`). It never counts as acceptance.
- `408`, `429` and `5xx` are retryable (failover within the group), other statuses
  permanent; `Retry-After` is honoured as for every adapter.

## How decisions become a verdict

The adapter renders the verdict JSON the loop parses (contract §7), so the loop has no
classifier-specific parsing:

- `verdict` is `accept` when every question's probability is at least its threshold,
  else `reject`;
- `confidence` is the minimum of the probabilities, forwarded as `judge_confidence` (on a
  reject a low value reads "sure it fails");
- on `accept`, `feedback` is `{}`; on `reject` it is one entry at pointer `""` (the whole
  result): a fixed message telling the extractor to check every value against the
  document, followed by the confidence. This satisfies the contract's rule that a reject
  carries at least one entry, but gives the extractor little to repair.

`roles.judge.feedback_fallback` decides what a reject carries:

| Mode | On a classifier reject | Cost |
| --- | --- | --- |
| `whole_result` (default) | The single `""` entry above. | None beyond the classifier call. |
| `llm` | The loop asks `roles.judge.feedback_group`, an LLM group, with the normal judge request for feedback, and uses its entries when it rejects. The verdict stays `reject` whatever it answers; when it accepts, fails or cannot be parsed, the `""` entry is kept (the job's deadline still fails the job with `timeout`). | One LLM judge call per rejection, none on acceptance. |

Only rejections answered by a classifier endpoint use the fallback, so a group that fails
over from a classifier to an LLM judge keeps the LLM's own feedback. The mode (and, with
`llm`, the feedback group) is part of `service_version`. Which mode should be the default
is to be decided from the evaluation harness numbers (false accept and false reject rates,
latency, cost versus the LLM judge).

## What is sent, and where

The document text, the caller's judge instructions, the schema and the candidate go to
`base_url` and nowhere else. The adapter sets no retention or training options because
the contract has none: they are properties of the classifier service or the proxy behind
it, so read the vendor's data-handling terms and approve them as for any endpoint
(contract §10) before configuring one. The vendor's own documentation must be read when
writing the proxy; this page describes only what `vde` sends.
