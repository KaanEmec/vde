# Job: Tokens: issue, hash, expire, revoke, `vde token`

Version: v0.2 · Epic: 02 Applications and tokens · Status: planned · Depends on: 01

## Goal

Random, opaque tokens printed once, stored as hashes, with expiry, scopes and immediate
revocation, several active per application for rotation.

## Context

Contract §3: opaque strings; the service stores only a hash; issued and revoked by the
operator, not through the application API; expiry on each token; several active tokens
per application; revocation effective at once; a CLI like
`vde token create --app travelinbox-prod --scope jobs --expires 2027-10-16` is enough.
Brief: "scoped, expiring, and revocable access tokens ... with credential rotation."

## Tasks

- [ ] Format: `vde_<8-char token id>_<32 bytes base64url random>`; the id is stored in
      clear for lookup and display, the secret part is hashed with SHA-256 (random
      256-bit secret needs no slow hash) and compared in constant time.
- [ ] Store: `tokens(id, application_id, hash, scopes, expires_at, revoked_at,
      created_at, last_used_at, label)`; `last_used_at` updated at most once a minute to
      avoid write amplification on polls.
- [ ] CLI: `vde token create --app id --scope jobs[,…] --expires date|duration
      [--label text]` prints the token once to stdout and nothing else; `vde token list
      --app id`; `vde token revoke <token id>`. All run against the same database file
      as `serve` (SQLite WAL allows a concurrent CLI writer); the audit log records each.
- [ ] Expiry is required (no non-expiring tokens); a maximum of, say, 2 years enforced
      with a configurable cap.
- [ ] Scopes: `jobs` (submit, poll, delete, quota) in v0.2; `jobs:read` as a finer
      optional scope for poll-only clients. Unknown scopes refused at creation.

## Acceptance criteria

- A created token authenticates; after `revoke` the next request is 401; after expiry
  401; the database contains no token secret (test greps the file).

## Out of scope

Admin API for tokens (epic 11), per-user tokens (backlog).
