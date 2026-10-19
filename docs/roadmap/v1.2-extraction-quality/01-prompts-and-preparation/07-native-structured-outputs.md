# Job: Native structured outputs

Version: v1.2 · Epic: 01 Prompts and document preparation · Status: planned · Depends on: 06

## Goal

Where an endpoint supports it, send the caller's schema as the provider's native
structured-output constraint, so the answer's shape is guaranteed by decoding instead of
asked for in the prompt. Opt-in per endpoint; the default keeps today's behaviour.

## Context

Every adapter sends the schema as prompt text only. Native support differs:
OpenAI-compatible `response_format: {type: "json_schema", strict: true}` (support varies
by host and model; OpenRouter and Nous route to backends that may ignore it), Anthropic
structured outputs or a forced tool call, Gemini `responseSchema`, Bedrock and Vertex
through their Anthropic or Gemini paths. Each provider accepts a different subset of JSON
Schema, so the caller's schema needs a per-provider reduction, and a schema the provider
rejects must fall back to the prompt path, not fail the job.

A decoding constraint fixes the shape, not the content, and a constrained answer has no
room for job 05's analysis block. Options to compare: the constraint wraps the answer as
`{"analysis": "...", "result": <caller schema>}` and the service unwraps it; or the
analysis comes from the provider's reasoning mode and the answer is constrained. The
job's first task decides by measurement.

## Tasks

- [ ] Spike on the owner's endpoint and models: does `response_format` json_schema reach
      the model, and with which schema subset; same for Anthropic directly if a key is
      available. Use synthetic samples from `examples/` and at most five requests per
      endpoint. Write the findings in `docs/providers/`.
- [ ] `internal/config`: an optional per-endpoint (or per model pair) key, for example
      `structured_output: none|native`, default `none`; schema, docs
      (`docs/operator/configuration.md`) and `vde config check` output in step.
- [ ] `internal/provider`: a field on the request carrying the schema; each adapter that
      supports it maps it, the others ignore it. Unsupported keywords are reduced or the
      request falls back to prompt-only, deterministically.
- [ ] Extractor only; the judge's verdict format may follow in the same way if the
      extractor result is kept.
- [ ] Schema validation (`schema.go`) still runs on every answer: the constraint is not
      trusted.
- [ ] Hash the setting into `service_version`.
- [ ] Run the comparison with the setting on for the owner's endpoint; record it, keep or
      revert (a revert keeps the code only if another endpoint benefits; otherwise remove
      it).

## Acceptance criteria

- Adapter tests (`internal/provider/*/client_test.go`): the request carries the
  provider's structured-output field when enabled and not otherwise; a schema with an
  unsupported keyword falls back as documented.
- Config tests: the key validates, defaults to `none`, and `none` gives byte-identical
  requests to today.
- `version_test.go`: `service_version` changes only when enabled.
- The comparison is recorded with the keep or revert decision.

## Out of scope

Constraining the classifier judge, provider-side JSON mode without a schema.
