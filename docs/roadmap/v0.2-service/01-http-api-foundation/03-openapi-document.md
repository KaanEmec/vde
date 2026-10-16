# Job: OpenAPI document

Version: v0.2 · Epic: 01 HTTP API foundation · Status: planned · Depends on: 01, 02

## Goal

`api/openapi.yaml`, the service's own API description, starting as a superset of
`FromTravelApp/extraction-service-api.yaml` and kept in step with the code by a test.

## Context

Contract §14: "the service's own OpenAPI document SHOULD be a superset". ADR 0005. Open
questions 6 and 15 (schema optional, `DELETE`).

## Tasks

- [ ] Copy TravelInbox's document; change `info`, keep every path, schema and code; mark
      `schema` optional with a description of schema-less mode; keep `DELETE`.
- [ ] Add `X-Request-Id` as a response header everywhere and `X-Service-Version` where
      the contract has it; add the optional `judge_confidence` and `attachments` job
      fields and the `message/rfc822` document media type.
- [ ] A Go test parses the YAML (a light-weight parser, no code generation) and asserts:
      every route in the mux appears with the same method; every error code constant
      appears in some description; every `Job` field is produced by the handler's struct
      (json tags).
- [ ] Serve the document at `GET /v1/openapi.yaml` (authenticated, so it leaks nothing
      to the unauthenticated internet) and ship it in the repository.
- [ ] A short `docs/api/README.md` linking the document and summarizing conventions.

## Acceptance criteria

- The superset relation is checked by a test that loads both documents and verifies every
  TravelInbox path, method, response code and required field is present in ours.

## Out of scope

Generating server code or clients from the document.
