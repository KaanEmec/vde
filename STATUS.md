# STATUS

Where the project stands, as of **2026-10-22** (v1.3 done on `main`, not released).
How to work here is in [AGENTS.md](AGENTS.md). Job files under [docs/roadmap/](docs/roadmap/README.md) stay the
source of truth; this page summarizes them and must be updated at the end of every session.

## Summary

v0.1 to v1.0 are implemented (scope per version in the table below). `v1.0.0` was tagged
and released on 2026-10-18, after `make live` passed (21 of 21 sample runs accepted through
the owner's endpoint), by the GitHub release workflow v1.1 has since removed. The owner took
the performance baseline and release engineering (signed images, SBOMs, docs site) out of
v1.0; drafts are on the unpushed branch `wip/release-engineering`. Everything is proven
with the fake provider and the conformance suite; real-model evidence is one smoke run, the
TravelInbox live test and a few evaluation runs through one OpenAI-compatible endpoint. The
owner's comparison of the judge policies on real documents has not been run.

v1.1 Lean is done and released as `v1.1.0` (2026-10-20, built and published locally with
`make release`: GitHub release with four archives and `checksums.txt`, image
`ghcr.io/kaanemec/vde:1.1.0`, also `1.1` and `latest`, linux/amd64 and arm64):
the code, tests, testdata and docs are smaller with the `/v1` API and the 1.x
configuration unchanged (every 1.0 configuration file loads, `TestOpenAPISupersetOfSnapshot`
passes, conformance and the `make eval` numbers equal the 00-04 reference). Checks and
releases run on the owner's machine (`make ci`, `make release`; `.github/` is gone).
Removed: the never-written `debug.log_content` sink and `vde config check --probe` (L1),
`vde eval import-travelinbox` (L3), the experimental JSON `document.url` (L7;
`documents.fetch.*` still load, with a warning), the Node and Swift example clients (L5),
and the per-job files of v0.1 to v1.0, collapsed into one `vX.Y-history.md` per version
(L6). Deprecated for removal in 2.0, still working with a warning: escalation,
`roles.second_judge`, the classifier judge and `feedback_fallback` (L8,
[ADR 0011](docs/architecture/decisions/0011-remove-tiers-and-classifier-judge.md),
proposed), plus the former schema violations. Against the 00-04 baseline
([exit criterion check](docs/roadmap/v1.1-lean/README.md#exit-criterion-check-2026-10-20)):
Go production lines 23,235 → 22,070 (−1,165; the criterion asked −1,800, not met), test
lines 32,085 → 32,196 (+111; asked −1,000, not met), tracked files 1,189 → 679 (−510),
direct dependencies 17 → 16, binary 36.1 → 36.0 MB, `deadcode` 24 → 7 (each with a
recorded reason), longest function 254 → 89 lines, `make ci` 8.5 → 8.2 min. `main`
and the tag `v1.1.0` are pushed; `make live` passed before the release.

## Versions

| Version | Status | Tag | Scope |
| --- | --- | --- | --- |
| [v0.1 Core engine](docs/roadmap/v0.1-history.md) | done 2026-10-16 | none (CHANGELOG 0.1.0) | Config, Anthropic/OpenAI-compatible/Gemini adapters, failover, representation incl. email, the loop, `vde extract` |
| [v0.2 Service](docs/roadmap/v0.2-history.md) | done 2026-10-16 | none (CHANGELOG 0.2.0) | HTTP API, applications and tokens, jobs, quota, retention, operations, admin API, packaging, contract conformance |
| [v0.3 Hardening and extensions](docs/roadmap/v0.3-history.md) | done 2026-10-17 | `v0.3.0` | Postgres and S3, Bedrock and Vertex, classifier judge, escalation, image conversion, webhooks and sync endpoint, `vde eval`, `serve --role` |
| [v0.4 Judge policy and preprocessing](docs/roadmap/v0.4-history.md) | done 2026-10-18, owner suite run pending | `v0.4.0` | Judge policy per application, `extracted` and `verification`, `internal/ground`, PDF text layer and rendering (`internal/pdf`) |
| [v1.0 Stable](docs/roadmap/v1.0-history.md) | done 2026-10-18 (03-01 and epic 04 deferred by the owner) | `v1.0.0` | Compatibility policy and OpenAPI snapshot test, config JSON Schema (`vde config schema`), hardening pass with fuzzers, independent review with fixes, `limits.max_queued_jobs` and `limits.max_submits_in_flight` |
| [v1.1 Lean](docs/roadmap/v1.1-lean/README.md) | done 2026-10-20; 00-01 is the owner's, 07-02 and 09-03 dropped | `v1.1.0` | Leaner code, tests, testdata and docs within the 1.x promise; vetted skills and prompts per task in [skills.md](docs/roadmap/v1.1-lean/skills.md); CI and releases move to the owner's machine (`make ci`, `make release`; GitHub only as backup and artifact host); owner decisions L1 to L9 and 18 taken 2026-10-19 ([open questions](docs/roadmap/open-questions.md#decided), item 17) |
| [v1.2 Extraction quality](docs/roadmap/v1.2-extraction-quality/README.md) | released 2026-10-20 | `v1.2.0` | Evidence-first framings (document first, `<analysis>` before the `<json>` answer and the `<verdict>`), context line and page markers, email section labels, HTML data tables as Markdown, caller guide; deviates from contract §6/§7 by owner decision ([ADR 0012](docs/architecture/decisions/0012-evidence-first-framing.md), amendment proposed); structured outputs measured and dropped, multi-pass dropped. Core set 3 → 4 of 4 correct ([quality](docs/quality.md#v12-prompts-and-document-preparation-2026-10-20)) |
| [v1.3 Convention-aware verification](docs/roadmap/v1.3-convention-aware-verification/README.md) | done 2026-10-22, not released | none | TravelInbox conventions as schema constraints and instruction examples in the co-owned corpus copy (5 TravelInbox cases: 20 → 7 field mismatches, convention misses 11 → 4), hand-over in [travelinbox-conventions-v1.3.md](docs/conformance/travelinbox-conventions-v1.3.md); `vde config check --probe`; failure codes by job id after `retention: none`; `x-vde-grounding: convention` measured and reverted (judge changed no outcome at 2× tokens) |

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
- **Local CI** (v1.1 00-02): `make ci` replaced GitHub Actions; the flaky timing tests
  found on the first GitHub runs (grounding performance bound, worker heartbeat, pdfium
  cold start) were fixed on 2026-10-18. `make release VERSION=v1.1.0-rc.0 DRY_RUN=1` passed
  without the image step (00-03).
- **v1.1 refactoring**: each code job recorded `make check` and `make ci` green and, where
  the loop, documents or providers changed, `make scenarios` and `make eval` equal to the
  00-04 baseline under `always`, `never` and `auto` (job files, "Result"); conformance and
  `TestOpenAPISupersetOfSnapshot` unchanged. The closeout (2026-10-20) re-ran it on the
  final tree: `make ci` green, `make eval` equal to the 00-04 reports, every 1.0
  configuration file loads.
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

## What is NOT verified

- Bedrock and Vertex adapters against live endpoints (no credentials).
- The classifier judge against a vendor API (unavailable); only the generic contract ships.
- Anthropic, OpenAI and Gemini direct keys: live runs went through the OpenAI-compatible
  adapter only; the others are covered by httptest (Anthropic and Gemini also have opt-in
  `live_test.go` files, with no recorded run).
- Releases are unsigned: signing, SBOMs and provenance are drafted on `wip/release-engineering` for the
  GitHub workflow v1.1 removed, never run.
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
- HEIC is not converted (no pure-Go decoder); accepted only where an endpoint declares it.
- Postgres `LISTEN/NOTIFY` skipped (workers poll); pending webhook deliveries live in memory.
- Grounding of partially printed dates and `auto` thresholds per application: see the
  [backlog](docs/roadmap/backlog.md).
- Unannotated schemas (TravelInbox's, the owner suite's) mark derived values as critical
  misses, so under `auto` most such jobs still go to the judge.
- pdfium: no system fonts, about 500 MB per `documents.pdf.workers`, interpreted outside
  amd64/arm64. No OCR: images and scanned PDFs are ungroundable.
- Accepted info findings of the independent review (F9 to F13 in
  [docs/security.md](docs/security.md)): 403 vs 401 for a token on the wrong listener,
  `trusted_proxy` trusting any peer, `queue_age_seconds` on an unauthenticated
  `/healthz`, workers reading `max_in_progress` from YAML applications only (F12 is moot
  since the content sink was removed in v1.1).
- Git history note: the v1.0 wave-1 code (API freeze, config schema, hardening,
  backpressure) landed inside commits `31dfa36` and `b315948`, whose messages name CI
  fixes, because a parallel session committed with `git add -A`; the
  [v1.0 history](docs/roadmap/v1.0-history.md) and `docs/security.md` say what each job
  delivered.

## Recommended next steps (in order)

1. v1.1 owner actions left: turn GitHub Actions off for the repository (no workflow file
   remains, so nothing runs meanwhile) and run `make hooks` once in this clone.
2. Owner runs the policy comparison ([quality.md](docs/quality.md#judge-policy) commands) and
   records the rows; then decide v0.4 criteria 1 to 3 on real models.
3. Annotate schemas with `x-vde-grounding` (owner suite first; TravelInbox's is their choice)
   and re-run `auto` to measure judge calls saved.
4. Make the GHCR package public if `docker pull ghcr.io/kaanemec/vde:1.0.0` needs a
   login (CONTRIBUTING, "First release only").
5. v1.2 Extraction quality is released as `v1.2.0` (2026-10-20; GitHub release with four
   archives and `checksums.txt`, image `ghcr.io/kaanemec/vde:1.2.0`, `1.2` and `latest`). Owner: send
   [contract-amendment-v1.2.md](docs/conformance/contract-amendment-v1.2.md) and
   [travelinbox-schema-suggestions.md](docs/conformance/travelinbox-schema-suggestions.md)
   to TravelInbox; until they amend §6/§7, vde deviates from the contract on prompt order
   and answer format (ADR 0012).
6. v1.3 is done on `main`, not released. Owner: send
   [travelinbox-conventions-v1.3.md](docs/conformance/travelinbox-conventions-v1.3.md) to
   TravelInbox (schema constraints and prompt_version 5 examples, measured). Still missed on
   the five cases: `travellers` and a choice between two printed amounts; next candidates are
   the backlog's devil's advocate judge and schema invariants. Release `v1.3.0` on request.
7. The 2.0 removals measured in [ADR 0011](docs/architecture/decisions/0011-remove-tiers-and-classifier-judge.md)
   (about 590 production and 1,100 test lines, plus the deprecated keys), once the owner
   accepts the ADR and wants a major version.
8. If wanted later: the deferred v1.0 items from `wip/release-engineering` (signing and
   SBOMs first, then the docs site, then the load test), each as its own job. The branch
   was deleted on 2026-10-20; its tip `7321040` stays recoverable for a while
   (`git branch wip/release-engineering 7321040`).
9. If the comparison justifies it, the `auto` thresholds of the
   [backlog](docs/roadmap/backlog.md) (needs an ADR 0009 amendment).

## How to resume

1. `git log --oneline | head -20` and `git status`; compare with the last commit above.
2. `make check` (and `make ci` before merging into `main`).
3. Read this file, then [AGENTS.md](AGENTS.md), then the next `planned` job file.
4. Work the job; update its `Status:`, the epic table and, at the end of the session, this
   file (date, last commit, what changed in the sections above).
