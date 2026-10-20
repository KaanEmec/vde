# Native structured outputs

Status: v1.2 (job 07), opt-in per model with
[`models.<model>.structured_output: native`](../operator/configuration.md#endpointsname),
`openai_compatible` endpoints only. Default `none`: the schema reaches the model as prompt
text only, and requests are byte for byte those of v1.1.

## What it does

On an extractor call with a schema, a model set to `native` gets the caller's schema as
the chat completions constraint:

```json
"response_format": {"type": "json_schema", "json_schema": {"name": "answer", "strict": true, "schema": <the caller's schema>}}
```

- The judge never gets it, and neither does a schema-less job.
- Only a schema whose root has `"type": "object"` is sent (strict `json_schema` needs an
  object root); any other root stays prompt-only. The schema is sent as the caller wrote it,
  without reduction.
- When the endpoint answers HTTP 400 to a request carrying the constraint, the adapter sends
  the same request once more without it, inside the same provider call. A 400 on the
  prompt-only request is permanent, as before.
- The answer is still parsed and validated against the schema on every attempt: the
  constraint fixes the shape, not the content, and is not trusted.
- A constrained answer is bare JSON, so it has no room for the extractor's
  [analysis block](../operator/framings.md#extractor-request) (ADR 0012); the answer parser's
  fallback reads it. Whether the guaranteed shape is worth more than the analysis is what
  the job's comparison measures.
- `native` is hashed into `service_version`; `none` leaves the hash unchanged.

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

No other adapter maps the setting. Anthropic, Gemini, Bedrock and Vertex have their own
structured-output forms and schema subsets, untested here; configuration validation refuses
`native` on them.
