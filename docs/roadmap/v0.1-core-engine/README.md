# v0.1 Core engine

Status: in progress

## Goal

Build and prove the heart of the product without the HTTP service: configuration,
provider adapters, endpoint groups with failover, document representation and the
extractor-judge loop, driven by a `vde extract` CLI command and a fake provider that
makes every scenario testable offline. Everything here is reused unchanged by v0.2.

## Exit criterion

From the command line, with a configuration file and one real provider key (Anthropic, an OpenAI-compatible endpoint such as OpenRouter, or Gemini), `vde extract`
turns a text, an HTML, an email (`.eml`), a PDF and an image document into an `accepted` or `rejected` JSON
result with feedback and attempt count, both with and without a schema. The automated
suite demonstrates, with the fake provider: acceptance on the first attempt, a schema
violation sent back without a judge call, a judge rejection repaired on the second
attempt, exhaustion of the attempt limit, failover to a second extractor endpoint, a judge
group that is fully down ending in `provider_unavailable`, and a job deadline producing
`timeout`.

### Exit criterion check (2026-10-16)

- Holds, automated (`make check`, `make scenarios`, no network): every listed fake-provider
  behaviour is a scenario in `testdata/scenarios/` run through `extract.Run` and the
  built binary: `accept-first-try`, `schema-violation-then-accept` (no judge call on
  attempt 1), `judge-reject-then-accept`, `three-rejections`, `extractor-primary-down`,
  `judge-group-down` (`provider_unavailable`), `slow-extractor-timeout` (`timeout`).
- Holds with the fake in place of the provider: `vde extract --config
  examples/vde.dev.yaml --provider-script ...` turns `booking.txt`, `booking.html`,
  `booking.eml`, `booking.pdf` (with `booking.png`) and `receipt.jpg` into `accepted`
  JSON with feedback and attempts, with and without `schema.json`, and a rejected run
  exits 2.
- Not verified: the same with one real provider key (none was available). `make live`
  (epic 06 job 04) does exactly that; run it with one key, then mark job 04, epic 06 and
  this version done.

## Epics

| # | Epic | Depends on | Status |
| --- | --- | --- | --- |
| 01 | [Project foundation](01-project-foundation/README.md) | none | done |
| 02 | [Provider adapters](02-provider-adapters/README.md) | 01 | done |
| 03 | [Endpoint groups and failover](03-endpoint-groups-and-failover/README.md) | 02 | done |
| 04 | [Document representation](04-document-representation/README.md) | 01, 02 (capabilities) | done |
| 05 | [Extraction loop](05-extraction-loop/README.md) | 03, 04 | done |
| 06 | [CLI and developer experience](06-cli-and-developer-experience/README.md) | 05 | in progress (job 04 awaits a live run) |

Epics 02 and 04 can proceed in parallel once 01 is done. Epic 05 is the integration point.

## Out of scope for v0.1

HTTP API, authentication, job storage, quota, retention, packaging. Those are v0.2 and
build on the packages produced here.
