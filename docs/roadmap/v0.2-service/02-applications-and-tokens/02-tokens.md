# Job: Tokens: issue, hash, expire, revoke, `vde token`

Version: v0.2 · Epic: 02 Applications and tokens · Status: done · Depends on: 01

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

- [x] Format: `vde_<8-char token id>_<32 bytes base64url random>`; the id is stored in
      clear for lookup and display, the secret part is hashed with SHA-256 (random
      256-bit secret needs no slow hash) and compared in constant time.
- [x] Store: `tokens(id, application_id, hash, scopes, expires_at, revoked_at,
      created_at, last_used_at, label)`; `last_used_at` updated at most once a minute to
      avoid write amplification on polls.
- [x] CLI: `vde token create --app id --scope jobs[,…] --expires date|duration
      [--label text]` prints the token once to stdout and nothing else; `vde token list
      --app id`; `vde token revoke <token id>`. All run against the same database file
      as `serve` (SQLite WAL allows a concurrent CLI writer); the audit log records each.
- [x] Expiry is required (no non-expiring tokens); a maximum of, say, 2 years enforced
      with a configurable cap.
- [x] Scopes: `jobs` (submit, poll, delete, quota) in v0.2; `jobs:read` as a finer
      optional scope for poll-only clients. Unknown scopes refused at creation.

## Acceptance criteria

- A created token authenticates; after `revoke` the next request is 401; after expiry
  401; the database contains no token secret (test greps the file).

## Out of scope

Admin API for tokens (epic 11), per-user tokens (backlog).

## Outcome

Done in `internal/auth` (`token.go`: `Generate`, `Parse`, `Hash`, `Verify`,
`ParseScopes`, `Permits`; `auth.go`: `Authenticate`, `Create`) and `cmd/vde/token.go`.
The id is 8 characters of lowercase base32 (`a-z2-7`), so the `_` that base64url may put in
the secret never confuses `Parse`, which splits by position and decodes the secret
strictly. `auth.max_token_ttl` (default `17520h`, 730 days) caps `--expires`, which takes a
date (midnight UTC), an RFC 3339 time, `Nd` or a Go duration. `create` prints only the
token on stdout (id, scopes and expiry on stderr) and audits `token.created`
(`token_id`, `scopes`, `expires_at`); `revoke` audits `token.revoked`; both actor `cli`.
`TestAuthEndToEnd` greps every file in the data directory for the secrets. Deviations:

- `last_used_at` is written by the middleware only when the stored value is a minute old
  (the store's `TouchTokenLastUsed` also guards it), so a poll costs one read.
- `vde token revoke` also accepts a whole token and uses its id; revoking a revoked token
  exits 0 and writes no second audit event.
- Labels are capped at 200 characters; `token list` shows status active, revoked or
  expired, never the hash.
