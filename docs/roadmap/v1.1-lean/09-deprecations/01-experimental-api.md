# Job: Remove the experimental `document.url`

Version: v1.1 · Epic: 09 Deprecations · Status: planned · Depends on: owner decision L7, decided 2026-10-19 ([open questions](../../open-questions.md#decided), item 17); 03-02

## Goal

Remove the experimental `document.url` submission (owner decision L7), keeping the
`documents.fetch` configuration keys accepted with a "no effect, deprecated" warning. The
completion webhook stays.

## Context

- Completion webhook (kept, L7): `internal/webhook/webhook.go` (283 lines), its tests (260), part
  of `cmd/vde/webhook_e2e_test.go` (109), wiring in `serve.go` (about 15), two audit
  kinds (about 40), the OpenAPI `jobFinished` callback, docs. About 700 lines. Marked
  experimental: "No consumer uses it yet"; deliveries live in memory.
- `document.url` (`documents.fetch`): `fetch.go` 132, `fetch_test.go` 160, about 40 in
  `submit_json.go`; about 330 lines. Experimental; removing it also removes an SSRF
  surface the v1.0 review had to cover.
- Keys that stay accepted with a warning: `documents.fetch.*`.
- Check before removing: whether `api/openapi.v1.0.0.yaml` contains the callback and the
  `url` field. `TestOpenAPISupersetOfSnapshot` already skips subtrees marked
  `x-stability: experimental` (`internal/api/openapi_snapshot_test.go:63, 102`); confirm
  that covers the callback and the `url` field before removing them.

## Tasks

- [x] Owner decision L7 (2026-10-19): remove `document.url`, keep the webhook.
- [ ] Confirm the snapshot test exempts exactly the experimental parts.
  - Skill: EVID.
  - Prompt: "On a scratch branch, delete the document `url` field from
    api/openapi.yaml and run TestOpenAPISupersetOfSnapshot; then delete one
    stable field and run it again. Report both results; discard the branch."
- [ ] Remove what was decided.
  - Skill: GOREF.
  - Prompt: "/golang-refactoring Plan mode: remove the document.url fetch end to end (code,
    tests, OpenAPI, docs, security notes), keep the configuration
    keys parsed and validated with a one-line 'no effect, deprecated' warning from config
    check and serve; CHANGELOG 'Removed' (experimental) and 'Deprecated' (keys).
    Guardrails: skills.md." Then "/golang-refactoring Execute mode: step <n>."
- [ ] Gate.
  - Skill: REVIEW, SEC.
  - Prompt: `/code-review high <first>^..<last>`, `/security-review`, `make conformance`.

## Acceptance criteria

- Decision recorded; a 1.0 configuration that sets the keys still loads and warns;
  conformance green.

## Out of scope

- Removing the keys (2.0).
- The completion webhook (kept by L7; its `Verify` comment is fixed in 01-01).
