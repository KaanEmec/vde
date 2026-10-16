# Job: Provider interface and request types

Version: v0.1 · Epic: 02 Provider adapters · Status: done · Depends on: 01-02

## Goal

The one interface every model call goes through, with request and response types that
are provider-neutral and an error type the router can classify.

## Context

ADR 0004. Brief: "Keep provider integration behind small Go interfaces."

## Proposed interface (`internal/provider`)

```go
type Part struct {            // exactly one field set
    Text      string
    Image     *Blob           // MediaType image/*
    Document  *Blob           // MediaType application/pdf (or others an adapter declares)
}
type Blob struct { MediaType string; Data []byte }

type Request struct {
    Model       string
    System      string        // the role framing
    Parts       []Part        // instructions, schema, document representation, candidate, feedback
    MaxTokens   int
    Temperature float64
}
type Response struct { Text string; InputTokens, OutputTokens int; Model string }

type Provider interface {
    Complete(ctx context.Context, req Request) (Response, error)
}

type Error struct { Kind Kind; Status int; Message string; RetryAfter time.Duration }
// Kind: Retryable (network, 5xx, 429, timeout) | Permanent (4xx other than 429, refusal, unsupported input)
```

## Tasks

- [x] Define the types above; keep them in one file with doc comments. No options
      pattern, no builder.
- [x] `Error` implements `error` and `Unwrap`; helper `Classify(err) Kind` treats context
      deadline and network errors as retryable.
- [x] A shared `httpclient` helper: timeout from endpoint config, no redirects, response
      size cap, user agent `vde/<version>`.
- [x] Table test for `Classify`.

## Acceptance criteria

- The fake, Anthropic and OpenAI-compatible packages compile against this interface with
  no other coupling to the rest of the code.

## Out of scope

Streaming, tool calling, embeddings. Structured-output modes may be used internally by an
adapter later but are not part of the interface.
