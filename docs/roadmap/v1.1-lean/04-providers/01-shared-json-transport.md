# Job: Shared JSON transport

Version: v1.1 · Epic: 04 Providers · Status: done (error-path check and Gate tasks left to the orchestrator) · Depends on: 01-03

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

- [x] Plan and execute.
  - Skill: GOREF.
  - Prompt: "/golang-refactoring Plan mode: add provider.PostJSON and provider.Refused
    and use them in anthropic, openai, gemini, vertex and classifier; error kinds, status
    handling and the never-quote-response rule must not change; each adapter's httptest
    suite must pass unchanged. Guardrails: skills.md." Then "/golang-refactoring Execute
    mode: one adapter per step."
- [ ] Check error paths (orchestrator).
  - Skill: PRTK (`silent-failure-hunter`).
  - Prompt: "Use the silent-failure-hunter agent on the diff of this job: every transport
    and decode error must still become a provider.Error with the same Kind, and no
    response body may reach an error message."
- [ ] Gate (orchestrator).
  - Skill: REVIEW.
  - Prompt: `/code-review high <first>^..<last>`, then `make scenarios`.

## Acceptance criteria

- No adapter calls `http.NewRequestWithContext` itself (except Bedrock's SDK path); all
  adapter tests and `TestNoSentinelInLogs` green.

## Out of scope

- Retries (the router owns failover).

## Result

`provider.PostJSON(ctx, client, endpoint, header, body, name) ([]byte, int, error)` in
`httpclient.go` builds the request, sets `Content-Type: application/json` beside the
adapter's headers, calls `Do`, turns a non-2xx into `StatusError` and reads the body;
`provider.Refused(status, msg)` replaces the two `refused` copies. Anthropic, OpenAI,
Gemini, Vertex and the classifier call it, one adapter per step; no adapter calls
`http.NewRequestWithContext` any more (Bedrock uses the AWS SDK). The 2xx "no text"
check stays per adapter: the five differ (trimmed or not, which blocks count), and
04-02 settles the Gemini and Vertex pair.

Kept: every request body and header on the wire (Accept, OpenRouter and auth headers
stay with the adapter; Vertex sets `Authorization` as `oauth2.Token.SetAuthHeader` does),
every error `Kind`, `Status` and `RetryAfter` the adapter suites assert, and the rule
that response bytes never reach an error. Anthropic still names a non-2xx by its
documented error type: `PostJSON` returns at most 64 KiB of a non-2xx body for that
purpose only, and Anthropic still decodes with `json.Decoder`, so an empty or cut-off 2xx
body stays Retryable there.

Changed text only (no kind changes): transport messages now read `<adapter>: request
failed`, `: invalid endpoint URL`, `: request rejected`, `: reading response failed`,
`: response body too large` for all five (OpenAI and the classifier had no prefix; the
invalid-URL error no longer wraps the `url.Error`); a body-read failure after a 2xx
carries status 0 for Gemini and Vertex too, as it did for the other three.

Checks: `make check` and `make scenarios` green (run in a local clone of `HEAD` plus this
job's files, because another agent's in-progress `internal/config` edit did not build in
the shared tree). `go test -cover ./internal/provider/...` before → after: provider
98.2 → 98.8 % (new `TestPostJSON`, `TestRefused`), anthropic 97.3 → 98.5, bedrock 98.9 →
98.9, classifier 91.9 → 96.2, fake 100 → 100, gemini 93.7 → 95.7, openai 95.1 → 97.1,
vertex 94.5 → 95.4.

`git diff --stat`: production 8 files, +98 −165 (net −67); tests 1 file, +81 −0.
