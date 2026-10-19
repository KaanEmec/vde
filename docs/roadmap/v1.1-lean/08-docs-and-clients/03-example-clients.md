# Job: Example clients: Node (web) and Swift (mobile)

Version: v1.1 · Epic: 08 Docs and clients · Status: planned · Depends on: owner decision L5 ([open questions](../../open-questions.md#open))

## Goal

Every example client in the repository is either run by a test or gone.

## Context

- `docs/api/examples/node/client.mjs` (149 lines, the web example),
  `docs/api/examples/swift/VDEClient.swift` (255 lines, the mobile example),
  `docs/api/examples/submit.sh` (81 lines). No test or CI step runs any of them; only
  `docs/api/README.md` and a v0.2 job file link them.
- The contract says only server workers hold tokens (open question on per-user tokens in
  the backlog): a mobile app must not call the service directly, so the Swift client
  shows a pattern the security model discourages. TravelInbox's backend is Go.
- Options: (a) delete Node and Swift, keep `submit.sh` (default); (b) keep them and run
  each against the fake-provider service in the conformance job (adds CI cost and
  toolchains); (c) keep only `submit.sh` and a short request/response walkthrough in
  `docs/api/guide.md`.

## Tasks

- [ ] Owner decision L5.
  - Skill: none.
  - Prompt: none.
- [ ] If (a) or (c): delete and repoint links.
  - Skill: none (Edit).
  - Prompt: "Delete docs/api/examples/node and docs/api/examples/swift; update
    docs/api/README.md and any link (grep for client.mjs and VDEClient); add a CHANGELOG
    line. Keep submit.sh. Guardrails: skills.md."
- [ ] If (b): simplify and test them.
  - Skill: SIMPLIFY.
  - Prompt: "/simplify docs/api/examples — reduce each client to submit, poll and
    delete with an Idempotency-Key, no retries beyond Retry-After; then add one step to
    the existing conformance job that runs submit.sh and the Node client against the
    built binary (no Swift toolchain in CI: compile-check only if a macOS runner already
    exists). Guardrails: skills.md."
- [ ] Gate.
  - Skill: REVIEW.
  - Prompt: `/code-review low <first>^..<last>`.

## Acceptance criteria

- No example client without a test; no dead link to a removed client.

## Out of scope

- An SDK package.
