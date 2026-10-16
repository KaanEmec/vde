# Epic 02: Applications and tokens

Version: v0.2 · Status: done · Depends on: 01, 03-01 (store)

The service knows applications; each has tokens that are opaque, hashed at rest, scoped,
expiring and revocable, managed by the operator with the CLI. The auth middleware refuses
bad credentials before anything is stored, charged or called.

| # | Job | Status |
| --- | --- | --- |
| 01 | [Applications model and `vde app`](01-applications-and-cli.md) | done |
| 02 | [Tokens: issue, hash, expire, revoke, `vde token`](02-tokens.md) | done |
| 03 | [Bearer authentication middleware and scopes](03-auth-middleware.md) | done |
