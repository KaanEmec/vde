# Job: Internal threat-model review and hardening pass

Version: v1.0 · Epic: 02 Security review · Status: planned · Depends on: v0.2 10-03

## Goal

Walk the threat model against the code and close gaps before anyone external looks.

## Tasks

- [ ] Review: multipart parsing edge cases (nested boundaries, header injection), JSON
      Schema denial of service (pathological patterns, deep nesting: add limits), zip-bomb
      style images (decode bounds), SSRF in any URL fetch, timing of token comparison,
      SQL built only from constants, path traversal in blob names, HTTP header limits.
- [ ] Dependency audit (`govulncheck`, licence check), minimal image review.
- [ ] Fuzz tests for the multipart reader, answer parser, verdict parser and HTML-to-text.
- [ ] Rate limit the unauthenticated `/healthz` by IP or document network exposure.
- [ ] Update `docs/security.md` with findings and fixes.

## Acceptance criteria

- Fuzzers run in CI for a bounded time; no open findings of medium or higher severity.
