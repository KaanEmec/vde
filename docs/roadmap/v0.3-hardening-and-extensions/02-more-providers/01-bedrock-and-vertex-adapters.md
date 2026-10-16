# Job: AWS Bedrock and Vertex AI adapters

Version: v0.3 · Epic: 02 More providers · Status: done except the live smoke test · Depends on: v0.1 02-01, 02-05

## Goal

Reach Anthropic and other models through cloud accounts operators already have, with
cloud IAM instead of API keys.

## Tasks

- [x] Bedrock: Converse API with document and image blocks; SigV4 auth through the AWS
      SDK default credential chain; region from config. Anthropic models on Bedrock
      reuse the Anthropic request mapping where the API shape is the Messages API.
- [x] Vertex AI: Gemini and Anthropic models with Google application default
      credentials.
- [x] Both: data-handling documents stressing that content stays within the operator's
      cloud account and region.
- [x] Decide per adapter whether to implement now or when a user asks; this job may be
      split or deferred without affecting anything else.

## Acceptance criteria

- [ ] At least one of the two passes a live smoke test before being announced. Not done:
      no AWS or Google Cloud credentials were available where this was built. The opt-in
      tests exist (`TestLiveSmoke` in `internal/provider/bedrock` and `internal/provider/vertex`,
      skipped unless `VDE_LIVE_BEDROCK_*` / `VDE_LIVE_VERTEX_*` are set); until one has run,
      do not announce either adapter as supported.

## Implementation notes and deviations

- **Both adapters implemented now.** `provider: bedrock` and `provider: vertex`, in
  `internal/provider/bedrock` and `internal/provider/vertex`, wired in `cmd/vde/providers.go`
  and the provider enum of `internal/config/validate.go`. Pages: `docs/providers/bedrock.md`
  and `vertex.md`; configuration snippets in `docs/operator/providers.md`.
- **Config.** New `Endpoint` fields `region` (required for both) and `project` (required for
  `vertex`). Both providers reject `api_key_env` (credentials come from the AWS default chain
  and Google application default credentials) and do not require `base_url`; it is an
  optional endpoint override (a private endpoint, or a test server). `region` and `project`
  are rejected on other providers. The validation test that used `bedrock` as its example
  of an unknown provider now uses `cohere`.
- **Bedrock uses Converse only.** The task text suggested reusing the Anthropic mapping for
  Anthropic models on Bedrock; Converse already carries image and document blocks for every
  model, so one mapping serves all models. The SDK's retries are off (`RetryMaxAttempts` 1)
  so the router owns retry and failover. `ConverseAPI` is the one-method interface tests
  inject. Documents are named `document N`, never by file name.
- **Vertex duplicates the small wire mappings** of the Gemini and Anthropic adapters
  (`vertex/gemini.go`, `vertex/claude.go`) instead of importing them: both keep their types
  unexported, and exporting them would change those packages' surface. The model id picks
  the publisher (`gemini*` or `claude*`; anything else is refused before a call). Vertex
  also treats the finish reasons `BLOCKLIST` and `SPII` as refusals.
- **`go.mod`.** `cloud.google.com/go/compute/metadata v0.9.0` was added (go.mod and go.sum)
  because `golang.org/x/oauth2/google` imports it and go.sum had no entry. No `go mod tidy`
  was run; the AWS and oauth2 requirements are still marked `// indirect` and `go mod tidy`
  will move them.
- **Not verified live.** Request shapes are checked against local servers and the real AWS
  SDK client (path and SigV4), not against the services. Model ids, quotas, request size
  limits and the retention statements in the provider pages need checking in the consoles.

## Out of scope

Azure OpenAI (covered by the OpenAI-compatible adapter).
