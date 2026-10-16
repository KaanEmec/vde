# Job: API guide and client examples

Version: v0.2 · Epic: 10 Documentation · Status: planned · Depends on: 01-03, 04-*

## Goal

`docs/api/`: how to call the service, with the OpenAPI document as reference and
copy-paste examples in curl, Swift (server-side or app via a backend) and JavaScript
(Node), plus guidance on writing instructions and schemas that work with the framings.

## Context

Brief: "Provide a minimal authenticated HTTP API and request examples usable from Swift
and web clients." Contract §15 examples; §3 (browser and mobile clients never hold a
service token, so the Swift and web examples go through the caller's own backend or use
a server-side Swift runtime).

## Tasks

- [ ] `docs/api/guide.md`: authentication, submit, poll with backoff, interpret
      statuses and feedback, idempotency, quota headers, retention, error handling
      table (which codes to retry).
- [ ] `docs/api/examples/`: curl script; a small Node client; a Swift
      `URLSession`-based client with multipart assembly, with a note on where it may run.
- [ ] `docs/api/writing-instructions.md`: how the framings are composed, what to put in
      extractor and judge instructions, how to design schemas (nullable fields,
      `additionalProperties: false`, confidence patterns like TravelInbox's), with the
      generic sample from `examples/`.
- [ ] Schema-less mode explained with an example.

## Acceptance criteria

- Each example runs against the compose deployment and produces a terminal job.

## Out of scope

Published client libraries.
