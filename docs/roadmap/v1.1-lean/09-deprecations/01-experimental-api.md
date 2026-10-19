# Job: Remove the experimental `document.url`

Version: v1.1 · Epic: 09 Deprecations · Status: done · Depends on: owner decision L7, decided 2026-10-19 ([open questions](../../open-questions.md#decided), item 17); 03-02

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
- [x] Confirm the snapshot test exempts exactly the experimental parts.
  - Skill: EVID.
  - Prompt: "On a scratch branch, delete the document `url` field from
    api/openapi.yaml and run TestOpenAPISupersetOfSnapshot; then delete one
    stable field and run it again. Report both results; discard the branch."
- [x] Remove what was decided.
  - Skill: GOREF.
  - Prompt: "/golang-refactoring Plan mode: remove the document.url fetch end to end (code,
    tests, OpenAPI, docs, security notes), keep the configuration
    keys parsed and validated with a one-line 'no effect, deprecated' warning from config
    check and serve; CHANGELOG 'Removed' (experimental) and 'Deprecated' (keys).
    Guardrails: skills.md." Then "/golang-refactoring Execute mode: step <n>."
- [ ] Gate (orchestrator).
  - Skill: REVIEW, SEC.
  - Prompt: `/code-review high <first>^..<last>`, `/security-review`, `make conformance`.

## Acceptance criteria

- Decision recorded; a 1.0 configuration that sets the keys still loads and warns;
  conformance green.

## Result

- **Evidence.** On a scratch copy of the tree: deleting the `url` property of
  `SubmitJobJSON.document` from `api/openapi.yaml` leaves `TestOpenAPISupersetOfSnapshot`
  green (`onlyExperimental`); deleting the stable `filename` next to it fails it
  (`.../document/properties/filename missing`). The snapshot carries the `url` field and the
  `jobFinished` callback, both marked experimental. Copy discarded.
- **Removed.** `internal/api/fetch.go` and `fetch_test.go`; the `url` field, the four fetch
  messages and `fetchPending` in `submit_json.go`; `submission.documentURL` and the two
  fetch steps (idempotent replay, `prepare`) in `submit.go`; the fetcher in `api.go`.
  `readJSONSubmission` and the document decoding no longer need the server
  (`jsonSubmission.documentParts`, like `textParts`).
- **HTTP answer.** A JSON body with `document.url` is `400 bad_request`, "The JSON body is
  malformed or has a field this service does not define." (`DisallowUnknownFields`), with or
  without `data_base64`: the compatibility policy's "Unknown JSON fields stay `400`" and
  `SubmitJobJSON`'s "Unknown fields are refused with `400`". A document without
  `data_base64` is `400`, "The document needs data_base64."
  (`TestJSONSubmissionDocumentURL`, `TestJSONSubmissionRefusals`). The OpenAPI `required`
  list of `document` stays `[media_type]` (a request schema may not gain a required field);
  its description says `data_base64` is required. `info.version` stays `1.0.0` (nothing
  added).
- **Keys.** `documents.fetch.*` are parsed and validated as in 1.0 (`allowed_hosts` with
  `enabled`, `max_bytes` not negative, `timeout` at most 1m); any set key warns once:
  `documents.fetch: has no effect and is deprecated (removal in 2.0): ...`, from
  `vde config check` and `vde serve` (`config.Validate`). The 10s timeout default is gone, so
  an unset block does not warn; an explicit `timeout: 0s` was and is accepted.
- **Docs.** OpenAPI descriptions, API guide and README, compatibility (removed under the
  experimental rule), configuration reference and `schema.json` (deprecated), security
  controls row (removed, keys deprecated) and review notes, overview, README, CHANGELOG
  "Removed" and "Deprecated".
- Checks: `make check`, `make conformance` green. Not run: the CRITIC step and the Gate
  (orchestrator).

`git diff --stat`: 21 files, +109 −431; production Go +33 −216, tests +34 −169, docs and
OpenAPI +42 −46.

## Out of scope

- Removing the keys (2.0).
- The completion webhook (kept by L7; its `Verify` comment is fixed in 01-01).
