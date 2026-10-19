# Job: Example clients: Node (web) and Swift (mobile)

Version: v1.1 · Epic: 08 Docs and clients · Status: planned · Depends on: owner decision L5, decided 2026-10-19 ([open questions](../../open-questions.md#decided), item 17)

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
  `docs/api/guide.md`. **Decided (L5): (a).**

## Tasks

- [x] Owner decision L5 (2026-10-19): delete Node and Swift, keep `submit.sh`.
- [ ] Delete and repoint links.
  - Skill: none (Edit).
  - Prompt: "Delete docs/api/examples/node and docs/api/examples/swift; update
    docs/api/README.md and any link (grep for client.mjs and VDEClient); add a CHANGELOG
    line. Keep submit.sh. Guardrails: skills.md."
- [ ] Gate.
  - Skill: REVIEW.
  - Prompt: `/code-review low <first>^..<last>`.

## Acceptance criteria

- No example client without a test; no dead link to a removed client.

## Out of scope

- An SDK package.
