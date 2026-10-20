# Native structured outputs

Status: measured in v1.2 (job 07) and not adopted. vde sends the schema as prompt text
only; no adapter sends a provider's structured-output constraint. This page keeps what
was learned, so the question can be reopened with numbers.

## What was tried

On extractor calls with a schema, an opt-in per-model setting sent the caller's schema as
the chat completions constraint (`response_format` with `json_schema`, strict) through the
`openai_compatible` adapter; the answer was still validated against the schema.

## Result (2026-10-20)

On the v1.2 core set (four owner documents, `--judge always`), turning it on for
`anthropic/claude-haiku-5.5` was worse than the prompt-only state it was measured against
(job 06): 3 of 4 cases fully correct instead of 4, one false accept, 1.50 attempts instead
of 1.25, and about twice the tokens on the longest case. A constrained answer is bare JSON,
so it has no room for the extractor's analysis block
([ADR 0012](../architecture/decisions/0012-evidence-first-framing.md)), and the field the
analysis had fixed went wrong again. The code was reverted
([quality report](../quality.md)).
## Spike: the owner's endpoint (2026-10-20)

Four requests to the owner's OpenAI-compatible endpoint (Nous Research), with
`examples/samples/booking.txt` as the document. The system prompt asked for the two-block
answer (`<analysis>`, then `<json>` with `guest` and `nights`); the strict schema
additionally required a `marker` property with a single allowed value that the prompt never
mentioned, so the marker in the answer shows that the constraint, not the prompt, shaped
it. `max_tokens` 1,024.

| Request | Model | `response_format` | HTTP | Finish | Answer | Tokens in / out |
| --- | --- | --- | --- | --- | --- | --- |
| A | `anthropic/claude-haiku-5.5` | none | 200 | stop | blocks as prompted; not bare JSON; no marker | 366 / 131 |
| B | `anthropic/claude-haiku-5.5` | strict schema | 200 | stop | bare JSON with `guest`, `nights`, `marker`; no blocks | 629 / 209 |
| C | `z-ai/glm-5.3-flash` | strict schema | 200 | length | bare JSON with `guest`, `nights`; no marker | 274 / 1,024 |
| D | `anthropic/claude-haiku-5.5` | strict schema with `format: date` and `pattern` | 200 | stop | bare JSON with every required property and the marker | 678 / 340 |

Findings:

- The endpoint honours `response_format` `json_schema` (strict) for the owner's default
  extractor, `anthropic/claude-haiku-5.5`: the required property only the schema named
  appeared, and the answer was bare JSON although the prompt asked for blocks. `format`
  and `pattern` were accepted, not refused.
- The constraint added about 260 input tokens per call on that model (A against B).
- `z-ai/glm-5.3-flash` (the owner's failover extractor and judge) did not honour it: the
  schema-only property was missing. It answered JSON without the blocks and spent the whole
  output budget (reasoning tokens), so it is left at `none`.
- Hence the setting is per model, not per endpoint: both models are served by the same
  endpoint.

No other adapter was tried. Anthropic, Gemini, Bedrock and Vertex have their own
structured-output forms and schema subsets, untested here.
