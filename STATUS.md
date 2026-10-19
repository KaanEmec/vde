# STATUS

Where the project stands, as of **2026-10-18** (last commit `c0b03a4`). How to work here is in
[AGENTS.md](AGENTS.md). Job files under [docs/roadmap/](docs/roadmap/README.md) stay the
source of truth; this page summarizes them and must be updated at the end of every session.

## Summary

v0.1 to v1.0 are implemented and committed on `main` (remote `origin`, public; checks
run locally with `make ci`): the extractor-judge engine and CLI, the authenticated HTTP service of the
TravelInbox contract, Postgres/S3 storage, more providers, escalation, delivery options, an
evaluation harness, multi-replica operation, (v0.4) judge policies `always`/`never`/`auto`
with a deterministic grounding check and PDF preparation in the service, and (v1.0) the
`/v1` and configuration freeze under a [compatibility policy](docs/compatibility.md), a
closed security review ([docs/security.md](docs/security.md)) and `503` backpressure.
The owner took the performance baseline and release engineering (signed images, SBOMs,
docs site) out of v1.0 on 2026-10-18; drafts are on the unpushed branch
`wip/release-engineering`. Everything is proven with the fake provider and the
conformance suite; real-model evidence is one smoke run, the TravelInbox live test and a
few evaluation runs through one OpenAI-compatible endpoint. The owner's comparison of the
judge policies on real documents has not been run. `v1.0.0` was tagged and released on
2026-10-18 after `make live` passed (21 of 21 sample runs accepted through the owner's
endpoint); the release workflow built the image and four archives.

## Versions

| Version | Status | Tag | Scope |
| --- | --- | --- | --- |
| [v0.1 Core engine](docs/roadmap/v0.1-history.md) | done 2026-10-16 | none (CHANGELOG 0.1.0) | Config, Anthropic/OpenAI-compatible/Gemini adapters, failover, representation incl. email, the loop, `vde extract` |
| [v0.2 Service](docs/roadmap/v0.2-history.md) | done 2026-10-16 | none (CHANGELOG 0.2.0) | HTTP API, applications and tokens, jobs, quota, retention, operations, admin API, packaging, contract conformance |
| [v0.3 Hardening and extensions](docs/roadmap/v0.3-history.md) | done 2026-10-17 | `v0.3.0` | Postgres and S3, Bedrock and Vertex, classifier judge, escalation, image conversion, webhooks and sync endpoint, `vde eval`, `serve --role` |
| [v0.4 Judge policy and preprocessing](docs/roadmap/v0.4-history.md) | done 2026-10-18, owner suite run pending | `v0.4.0` | Judge policy per application, `extracted` and `verification`, `internal/ground`, PDF text layer and rendering (`internal/pdf`) |
| [v1.0 Stable](docs/roadmap/v1.0-history.md) | done 2026-10-18 (03-01 and epic 04 deferred by the owner) | `v1.0.0` | Compatibility policy and OpenAPI snapshot test, config JSON Schema (`vde config schema`), hardening pass with fuzzers, independent review with fixes, `limits.max_queued_jobs` and `limits.max_submits_in_flight` |
| [v1.1 Lean](docs/roadmap/v1.1-lean/README.md) | in progress: epic 00 jobs 02 and 03 (local CI and release) merged into `main` 2026-10-19, not pushed | none | Leaner code, tests, testdata and docs within the 1.x promise; vetted skills and prompts per task in [skills.md](docs/roadmap/v1.1-lean/skills.md); CI and releases move to the owner's machine (`make ci`, `make release`; GitHub only as backup and artifact host); owner decisions L1 to L9 and 18 taken 2026-10-19 ([open questions](docs/roadmap/open-questions.md#decided), item 17) |
| [v1.2 Extraction quality](docs/roadmap/v1.2-extraction-quality/README.md) | planned 2026-10-19 (roadmap only) | none | One epic, nine jobs: prompt order and wording, context line and page markers, email section labels, HTML data tables, analysis blocks for extractor and judge, native structured outputs, caller schema guide, multi-pass extraction after ADR 0011; each measured against the previous report |

## What is verified, and how

- **Automated, fake provider, no network** (`make check`, `make scenarios`, `make eval`):
  every loop scenario in `testdata/scenarios/` (incl. `judge-policy/`), the HTTP e2e tests in
  `cmd/vde` (`serve_e2e_test.go`, `admin_e2e_test.go`, `webhook_e2e_test.go`,
  `TestServeJudgePolicies`), and v0.4's criteria (`TestRun_autoNeverGroundsInvented`,
  `TestCheck_eventInvoice`, `TestExtractPDFWithoutPages`); details in the
  [v0.4 exit criterion check](docs/roadmap/v0.4-history.md#exit-criterion-check-2026-10-18).
- **Contract conformance** (`make conformance`): contract §14 items 1 to 9 against the built
  binary, unchanged under `always` after v0.4 and after the v1.0 limits; MUST traceability in
  [travelinbox-v1.md](docs/conformance/travelinbox-v1.md).
- **v1.0 freeze and security** (`make check`): `TestOpenAPISupersetOfSnapshot` against
  `api/openapi.v1.0.0.yaml` (proven to catch a lost enum value, property, header, error
  code or required field), `internal/config/schema_test.go` (every example validates, the
  struct tree and the schema agree both ways), seven fuzzers clean for 20 to 30 s each and
  run by `make fuzz` (part of `make ci`), regression tests for every fixed finding in
  [docs/security.md](docs/security.md), `govulncheck` clean with `toolchain go1.26.9`.
- **Local CI**: `make ci` replaced GitHub Actions in v1.1/00-02; the flaky timing tests
  found on the first GitHub runs (grounding performance bound, worker heartbeat, pdfium cold start) were fixed
  on 2026-10-18.
- **Storage and scaling**: Postgres and S3 suites (opt-in env vars); two worker pools run
  100 jobs exactly once and resume a killed pool's job; `examples/compose/scaled` ran
  locally with PostgreSQL 17 and RustFS ([v0.3 check](docs/roadmap/v0.3-history.md#exit-criterion-check-2026-10-17)).
- **Smoke test, real model** (2026-10-16, `make live` via Nous Research,
  `google/gemini-3.8-flash`): samples (text, HTML, email, PDF, JPEG, with and without schema)
  and the owner's ten documents all `accepted`
  ([job record](docs/roadmap/v0.1-history.md#06-04-real-provider-smoke-test)).
- **TravelInbox live test** (2026-10-16, local `vde serve`, same model): `TestServiceLive`
  PASS (`one_document`; `corpus_end_to_end` skipped). TravelInbox's evaluation: 50 documents,
  0 failed, no-correction share flights 33.3 %, lodging 5.9 % (below TravelInbox's 90 %
  targets) ([record](docs/conformance/travelinbox-v1.md)).
- **`vde eval` on the TravelInbox corpus** (2026-10-16, [quality.md](docs/quality.md)): false
  accepts 86.0 % (gemini-3.8-flash both roles), 88.9 % (Sonnet 5.5 judge), 80.5 % (Sonnet 5.5
  both); mostly ground-truth convention disagreements, not document errors.
- **Owner suite runs** (2026-10-17, recorded in [ADR 0009](docs/architecture/decisions/0009-judge-policy-and-grounding.md)
  and [ADR 0010](docs/architecture/decisions/0010-pdf-preprocessing-in-service.md), not as
  rows in quality.md): Haiku 5.5 extractor with GLM 5.3 Flash judge, 8 of 10 fully correct,
  field accuracy 96.8 %, about 0.6 cents per document; every judge tried (Gemini Flash,
  Sonnet, Haiku, GLM, DeepSeek) accepted an event invoice with invented values. PDF selection
  without caller pages on the owner suite (`VDE_USER_DOCS=1`, 2026-10-17): all 5 PDFs.
- **Binary size** (v0.4): linux/amd64 35.9 MB, linux/arm64 34.2 MB, static, `CGO_ENABLED=0`.

## What is NOT verified

- Bedrock and Vertex adapters against live endpoints (no credentials).
- The classifier judge against a vendor API (unavailable); only the generic contract ships.
- Anthropic, OpenAI and Gemini direct keys: live runs went through the OpenAI-compatible
  adapter only; the others are covered by httptest (Anthropic and Gemini also have opt-in
  `live_test.go` files, with no recorded run).
- The multi-arch image is built by `make release` (buildx, not yet installed on the owner's
  machine, so the v1.1 dry run skipped it); the v0.3.0 and v0.4.0 GitHub release runs failed on the flaky tests above (a separate session is rebuilding v0.4.0).
  Releases are unsigned: signing, SBOMs and provenance are drafted on
  `wip/release-engineering`, never run.
- A load test: never run on the target VM; no capacity document (deferred).
- An external security audit: the two v1.0 reviews were internal (a checklist pass and an
  independent read-only pass by a separate agent); a paid audit is the owner's option.
- The owner's judge-policy comparison (`always`/`auto`/`never` on the owner suite and the
  TravelInbox corpus): **pending**, table of placeholders in
  [quality.md, "Judge policy"](docs/quality.md#judge-policy).
- Conformance in container mode (only local binary and fake provider so far).
- A real second consumer (v0.3 criterion a).

## Known gaps and follow-ups

Sources: [CHANGELOG "Known gaps"](CHANGELOG.md), [backlog](docs/roadmap/backlog.md),
[open questions](docs/roadmap/open-questions.md), the
[contract review](docs/conformance/travelinbox-v1-review.md#6-open-items-for-the-review-meeting).

- Idempotency replay of a content-deleted job answers the same id with `result: null` and
  `content_deleted_at`; TravelInbox has to confirm this reading of contract §4/§5 (review
  item 10). The review meeting has not happened.
- TravelInbox to confirm it tolerates `verification`, `extracted`, `source: "grounding"` and
  which `documents.pdf.mode` it wants.
- `vde config check --probe` is accepted but no provider implements a probe.
- HEIC is not converted (no pure-Go decoder); accepted only where an endpoint declares it.
- Postgres `LISTEN/NOTIFY` skipped (workers poll); pending webhook deliveries live in memory.
- Grounding of partially printed dates (no year, next-day arrival) is ungrounded today and
  goes to the judge ([backlog](docs/roadmap/backlog.md)).
- Unannotated schemas (TravelInbox's, the owner suite's) mark derived values as critical
  misses, so under `auto` most such jobs still go to the judge.
- pdfium: no system fonts, about 500 MB per `documents.pdf.workers`, interpreted outside
  amd64/arm64. No OCR: images and scanned PDFs are ungroundable.
- `debug.log_content` sink opened but not written by provider adapters
  ([v0.2 06-03](docs/roadmap/v0.2-history.md#06-03-log-redaction-and-the-debug-content-switch), in progress).
- Idea, not documented or implemented elsewhere: `auto` thresholds per application
  (`max_document_chars`, `max_items`, `min_grounded_share`) that send large or weakly
  grounded results to the judge.
- Accepted info findings of the independent review (F9 to F13 in
  [docs/security.md](docs/security.md)): 403 vs 401 for a token on the wrong listener,
  `trusted_proxy` trusting any peer, `queue_age_seconds` on an unauthenticated
  `/healthz`, `debug.log_content` with `trusted_proxy`, workers reading `max_in_progress`
  from YAML applications only.
- Git history note: the v1.0 wave-1 code (API freeze, config schema, hardening,
  backpressure) landed inside commits `31dfa36` and `b315948`, whose messages name CI
  fixes, because a parallel session committed with `git add -A`; the job files and
  `docs/security.md` say what each job delivered.

## Recommended next steps (in order)

1. Owner runs the policy comparison ([quality.md](docs/quality.md#judge-policy) commands) and
   records the rows; then decide v0.4 criteria 1 to 3 on real models.
2. Annotate schemas with `x-vde-grounding` (owner suite first; TravelInbox's is their choice)
   and re-run `auto` to measure judge calls saved.
3. Make the GHCR package public if `docker pull ghcr.io/kaanemec/vde:1.0.0` needs a
   login (CONTRIBUTING, "First release only").
4. If wanted later: the deferred v1.0 items from `wip/release-engineering` (signing and
   SBOMs first, then the docs site, then the load test), each as its own job.
5. If the comparison justifies it, the `auto` thresholds above (needs an ADR 0009 amendment).
6. v1.1 Lean ([roadmap](docs/roadmap/v1.1-lean/README.md)): epic 00 first. Local CI and release (00-02, 00-03)
   are merged into `main`; owner: push `main`, turn GitHub Actions off, install buildx and
   give `gh` `write:packages` (00-03). Then 00-01 skills and 00-04 baseline, then the waves.
7. v1.2 Extraction quality ([roadmap](docs/roadmap/v1.2-extraction-quality/README.md), planned
   2026-10-19): starts after `v1.1.0`. Record the baseline report first (epic 01 README,
   "Measuring"); every job then runs one real-model comparison on 3 to 5 documents (a
   fixed core set of four) with the owner's key.

## How to resume

1. `git log --oneline | head -20` and `git status`; compare with the last commit above.
2. `make check` (and `make ci` before merging into `main`).
3. Read this file, then [AGENTS.md](AGENTS.md), then the next `planned` job file.
4. Work the job; update its `Status:`, the epic table and, at the end of the session, this
   file (date, last commit, what changed in the sections above).
