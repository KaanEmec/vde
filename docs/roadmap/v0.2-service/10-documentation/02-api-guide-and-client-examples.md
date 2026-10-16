# Job: API guide and client examples

Version: v0.2 · Epic: 10 Documentation · Status: in progress · Depends on: 01-03, 04-*

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

- [x] `docs/api/guide.md`: authentication, submit, poll with backoff, interpret
      statuses and feedback, idempotency, quota headers, retention, error handling
      table (which codes to retry).
- [x] `docs/api/examples/`: curl script; a small Node client; a Swift
      `URLSession`-based client with multipart assembly, with a note on where it may run.
- [x] `docs/api/writing-instructions.md`: how the framings are composed, what to put in
      extractor and judge instructions, how to design schemas (nullable fields,
      `additionalProperties: false`, confidence patterns like TravelInbox's), with the
      generic sample from `examples/`.
- [x] Schema-less mode explained with an example.

## Acceptance criteria

- [ ] Each example runs against the compose deployment and produces a terminal job.

## Progress notes

Written: `docs/api/guide.md`, `docs/api/writing-instructions.md`, `docs/api/examples/submit.sh`,
`examples/node/client.mjs`, `examples/swift/VDEClient.swift`, plus a links section in
`docs/api/README.md`.

Checks done: `bash -n` on `submit.sh`, `node --check` on `client.mjs`, `swiftc -parse` and
`swiftc -typecheck` on `VDEClient.swift`. The shell script and the Node client were also
exercised once against a throwaway local fake server (202 then `accepted`), which only
shows that the multipart request is well formed and the poll loop terminates.

**Not done: the examples have not been run against a live deployment**, because the `/v1`
handlers are still being wired. The acceptance criterion below stays unticked until each
example has been run against the compose deployment and reached a terminal job. The
guide's `jobs:read` scope description should be checked against the token job when it lands.

## Out of scope

Published client libraries.
