# Job: Shared JSON transport

Version: v1.1 · Epic: 04 Providers · Status: planned · Depends on: 01-03

## Goal

One function in `internal/provider` posts a JSON request and classifies the outcome;
the adapters build bodies and parse answers only. Structure only: requests, error
classes and the rule that response bytes are never quoted stay.

## Context

Five adapters repeat 25 to 35 lines (`anthropic.go:124-145`, `openai/client.go:101-128`,
`gemini.go:100-137`, `vertex/vertex.go:110-146`, `classifier/client.go:93-121`): marshal,
`NewRequestWithContext`, headers, `Do` with `provider.Classify`, non-2xx to
`provider.StatusError`, `io.ReadAll`, unmarshal without quoting the bytes. `refused` is
defined twice (`gemini.go:211`, `vertex/gemini.go:130`). A
`provider.PostJSON(ctx, client, url, header, body, name) ([]byte, int, error)` and one
`provider.Refused(status, msg)` replace them; the 2xx "no text" check can be shared too.
Bedrock uses the AWS SDK and is not part of this. About −110 lines.

## Tasks

- [ ] Plan and execute.
  - Skill: GOREF.
  - Prompt: "/golang-refactoring Plan mode: add provider.PostJSON and provider.Refused
    and use them in anthropic, openai, gemini, vertex and classifier; error kinds, status
    handling and the never-quote-response rule must not change; each adapter's httptest
    suite must pass unchanged. Guardrails: skills.md." Then "/golang-refactoring Execute
    mode: one adapter per step."
- [ ] Check error paths.
  - Skill: PRTK (`silent-failure-hunter`).
  - Prompt: "Use the silent-failure-hunter agent on the diff of this job: every transport
    and decode error must still become a provider.Error with the same Kind, and no
    response body may reach an error message."
- [ ] Gate.
  - Skill: REVIEW.
  - Prompt: `/code-review high <first>^..<last>`, then `make scenarios`.

## Acceptance criteria

- No adapter calls `http.NewRequestWithContext` itself (except Bedrock's SDK path); all
  adapter tests and `TestNoSentinelInLogs` green.

## Out of scope

- Retries (the router owns failover).
