# Epic 05: Quota and rate limits

Version: v0.2 · Status: planned · Depends on: 02, 04

Two separate mechanisms per application: a quota of submissions per window, and
short-term rate limits including a ceiling on jobs in progress. Headers on every
authenticated response; `Retry-After` on every 429.

| # | Job | Status |
| --- | --- | --- |
| 01 | [Quota windows and headers](01-quota-windows-and-headers.md) | planned |
| 02 | [Rate limits and in-progress ceiling](02-rate-limits.md) | planned |
| 03 | [`GET /v1/quota`](03-quota-endpoint.md) | planned |
