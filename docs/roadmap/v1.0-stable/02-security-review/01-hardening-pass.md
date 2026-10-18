# Job: Internal threat-model review and hardening pass

Version: v1.0 · Epic: 02 Security review · Status: done · Depends on: v0.2 10-03

## Goal

Walk the threat model against the code and close gaps before anyone external looks.

## Tasks

- [x] Review: multipart parsing edge cases (nested boundaries, header injection), JSON
      Schema denial of service (pathological patterns, deep nesting: add limits), zip-bomb
      style images (decode bounds), SSRF in any URL fetch, timing of token comparison,
      SQL built only from constants, path traversal in blob names, HTTP header limits.
- [x] Dependency audit (`govulncheck`, licence check), minimal image review.
- [x] Fuzz tests for the multipart reader, answer parser, verdict parser and HTML-to-text.
- [x] Rate limit the unauthenticated `/healthz` by IP or document network exposure (a
      global rate on its database check; over it the last result is reused, see
      `docs/security.md`).
- [x] Update `docs/security.md` with findings and fixes ("Hardening review (v1.0)").
- [ ] Build releases with go1.26.9 / go1.27.2 or newer (the review's open "Go toolchain"
      finding: the release workflow uses the `go.mod` version, 1.26.0).

## Acceptance criteria

- Fuzzers run in CI for a bounded time; no open findings of medium or higher severity.
