# v1.1 Lean

Status: done 2026-10-20, released as `v1.1.0` (00-01 is the owner's; 07-02 and 09-03 dropped) · Depends on: v1.0

## Goal

Make the project smaller without changing what it promises: remove dead code, unneeded
abstractions and layers, duplicated logic, unused files and duplicated documentation, so
that every remaining line has a production reason. CI and releases move from GitHub
Actions to the owner's machine (`make ci`, `make release`); GitHub stays the online
backup and hosts the release artifacts. The `/v1` API and the configuration
schema stay as frozen in v1.0 ([compatibility policy](../../compatibility.md)); anything
that would break them is only deprecated here (epic 09) and removed in a later major
version, after an owner decision.

## Scope: backend, web and mobile

The repository is the Go service; it has no web or mobile application. "Web" here means
the HTTP surface (`internal/api`, `api/openapi.yaml`, the admin API) and the Node example
client `docs/api/examples/node/client.mjs`; "mobile" is the Swift example client
`docs/api/examples/swift/VDEClient.swift`. Both are deleted (owner decision L5,
[08-03](08-docs-and-clients/03-example-clients.md)).

## How a job is worked

Each task names a **skill** and a **prompt**. The vetted skills, the guardrails every
prompt includes ("Guardrails: skills.md") and the review gate that closes each job are in
[skills.md](skills.md). Read it before the first job. The inventory behind every task is
the audit of 2026-10-19 (four read-only passes over all packages, files and docs), cited
in each job's Context with file and line numbers as of commit `78b6486`; re-check the line
numbers with the tools before editing.

## Baseline (2026-10-19, commit `78b6486`)

| Measure | Value |
| --- | --- |
| Go production lines (no `_test.go`) | 23,235 |
| Go test lines | 32,085 |
| Tracked files | 1,144 (testdata 609, internal 257, docs 189); 1,189 after this roadmap and epic 00 (docs 237) |
| Roadmap | 139 files, 6,818 lines |
| Direct dependencies (`go.mod`) | 17 |
| Binary, linux/amd64, stripped | 36.1 MB |
| `deadcode ./cmd/...` | 24 functions reachable only from tests |
| `golangci-lint` `unparam` | 9 findings (2 in production code) |
| Longest functions | `serveAs` 254, `createJob` 163, `extractFile` 147, `loop.run` 138 (cognitive complexity 54), `runEval` 138 |

Job 00-04 re-measured these on 2026-10-19 after the local CI merge (`07f6552`): Go lines,
dependencies, binary size (go1.26.9) and `deadcode` unchanged; `go test -race` 135 s per
toolchain, `make ci` about 8.5 min. Reference reports: `eval-reports/lean-baseline/`
(local, gitignored).

## Exit criterion

Release `v1.1.0` with `make release` when:

- `make ci` is green (check, lint, both toolchains, govulncheck, fuzz, cross,
  conformance, eval, storage) and `make eval` numbers equal the 00-04 reference under
  `always`, `never` and `auto`.
- Every 1.0 configuration file still loads; `TestOpenAPISupersetOfSnapshot` passes.
- No workflow remains under `.github/`, Actions is disabled for the repository, and the
  release (archives, checksums, GHCR image) was built and uploaded from the owner's
  machine.
- Production Go lines down by at least 1,800 (about 8 %), test lines by at least 1,000,
  tracked files by at least 350, with no new dependency, production package or
  interface (04-03 adds one test-support package that replaces five copies).
- No function in `deadcode ./cmd/...` output without a recorded reason; no production
  interface with one implementation outside the three that overview principle 2 names.
- Every deprecation decided on 2026-10-19 ([open-questions.md](../open-questions.md#decided),
  item 17) listed in the CHANGELOG and warned about by `vde config check` and
  `vde serve`.

## Exit criterion check (2026-10-20)

Measured on `8f0a0a9` (after the final review leftovers) with the commands of
[00-04](00-preparation/04-baseline.md); binary with `GOTOOLCHAIN=go1.26.9`.

| Measure | Baseline (00-04) | Closeout | Change |
| --- | --- | --- | --- |
| Go production lines (no `_test.go`) | 23,235 | 22,070 | −1,165 (−5.0 %) |
| Go test lines | 32,085 | 32,196 | +111 |
| Tracked files | 1,189 | 679 | −510 |
| of which testdata / docs | 609 / 237 | 224 / 116 | −385 / −121 |
| Roadmap | 139 files, 6,818 lines | 67 files, 6,241 lines (incl. v1.2) | −72 files |
| Direct dependencies (`go.mod`) | 17 | 16 | −1 (`oklog/ulid`) |
| Binary, linux/amd64, stripped | 36.1 MB | 36.0 MB (35,958,946 bytes) | −0.1 MB |
| `deadcode ./cmd/...` | 24 | 7 | −17 |
| Production interfaces (top-level `type … interface`) | 17 | 11 | −6 |
| Packages with production code | 30 | 30 | `internal/sweeper` merged into `internal/worker`; test-support `internal/provider/providertest` added (04-03) |
| Longest function | `serveAs` 254 | `printSummary` 89 (`createJob` 81) | −165 |
| `make ci` | about 8.5 min | 8.2 min (492 s; race suite 129 s per toolchain) | −0.3 min |

Verdict per criterion:

- **`make ci` green, `make eval` equal to the 00-04 reference: met.** `make ci` passed on
  the final tree (every step); the `always`, `never` and `auto` reports equal the
  `eval-reports/lean-baseline/` ones in every summary number and per-case result (timings
  aside). `make scenarios` and `make conformance` green.
- **Every 1.0 configuration file loads; `TestOpenAPISupersetOfSnapshot` passes: met.**
  The seven `vde` configuration files at `v1.0.0` (`examples/*.yaml`,
  `examples/compose/vde.yaml`, `examples/compose/scaled/vde.yaml`, the scenario config)
  pass `vde config check` with their environment variables set.
- **No workflow under `.github/`: met** (the directory is gone). **Actions disabled and
  the release built and uploaded from the owner's machine: not met yet.** Both are owner
  actions: `main` is not pushed, and the release waits until the owner asks for it.
- **Production lines −1,800: not met** (−1,165). Splitting long functions (epic 05, about
  +220), the deprecation warnings (epic 09) and the shared helpers add lines, and many
  jobs consolidated copies rather than deleted features. The removals ADR 0011 measured
  (about 590 production and 1,100 test lines) need a major version and are deferred to
  2.0. **Test lines −1,000: not met** (+111): behaviour-pinning tests were added (OpenAPI
  superset proof cases, `/metrics` series, the local ULID, drift tests between the merged
  copies, deprecation warnings) where shared harnesses removed others. **Tracked files
  −350: met** (−510). **No new dependency, production package or interface: met.**
- **`deadcode` and single-implementation interfaces: met, with recorded reasons.** The
  seven remaining `deadcode` entries (`audit.Decode`, `webhook.Verify`, four `fake`
  helpers, `document.MediaTypes`) are test oracles used across packages
  ([01-01](01-dead-code/01-test-only-symbols.md)). Besides `Provider`, job `Store` and
  blob `Store`, the interfaces with one production implementation are test seams kept
  with their reason in [02-02](02-de-abstraction/02-single-implementation-types.md):
  `audit.Appender`, `bedrock.ConverseAPI`, `worker.Locker`, `document.PDFEngine` and
  `PDFDoc` (measured).
- **Every 2026-10-19 deprecation in the CHANGELOG and warned about by `vde config check`
  and `vde serve`: met** (CHANGELOG `[Unreleased]`, "Deprecated").

Overall: done except the release and the owner's GitHub actions; the size targets for Go
lines were not reached and are not pursued further within 1.x.

## Epics

| # | Epic | Depends on | Status |
| --- | --- | --- | --- |
| 00 | [Preparation: skills, local CI and release, baseline](00-preparation/README.md) | none; runs first, merged into `main` before the rest | done (01 is the owner's) |
| 01 | [Dead code and dead features](01-dead-code/README.md) | 00 | done |
| 02 | [Remove unneeded abstractions](02-de-abstraction/README.md) | 01 | done |
| 03 | [One source for each rule](03-single-source/README.md) | 01 | done |
| 04 | [Provider adapters](04-providers/README.md) | 01-03 | done |
| 05 | [Long functions](05-long-functions/README.md) | 02, 03 | done |
| 06 | [Tests and testdata](06-tests-and-testdata/README.md) | 00 | done |
| 07 | [Dependencies](07-dependencies-and-build/README.md) | 00 | done (02 dropped) |
| 08 | [Documentation and example clients](08-docs-and-clients/README.md) | 00; 08-01 last of all docs edits | done |
| 09 | [Deprecations (owner decisions)](09-deprecations/README.md) | 02, 04 | done (03 dropped) |
| 10 | [Closeout](10-closeout/README.md) | all | done except the release (owner) |

## Order and parallel waves

Epic 00 comes first and ends on `main`: the local CI and release (00-02, 00-03) are
merged and pushed, and Actions is off, before the baseline (00-04) and before any
refactoring job, so every later job is checked by `make ci`. After that, parallel agents
must own disjoint files (AGENTS.md) and one orchestrator commits. Suggested waves:

| Wave | Jobs (in parallel) | Files owned |
| --- | --- | --- |
| 0a | 00-01, then 00-02, then 00-03 on branch `v1.1/local-ci`; merged into `main` and pushed | `.github/`, `.githooks/`, `Makefile`, `Dockerfile`, `.dockerignore`, `.goreleaser.yaml`, CONTRIBUTING, the docs that mention GitHub CI |
| 0b | 00-04 | none (measurements) |
| 1 | 01-01; 06-03; 07-01; 08-03 | 01-01: Go code across packages (alone in Go); 06-03: `internal/eval`, `testdata/eval`, `testdata/travelinbox`, `cmd/vde/eval.go`; 07-01: `internal/job/job.go`, `internal/api/middleware.go`; 08-03: `docs/api/examples/` |
| 2 | 01-02, 01-03 (sequential); 03-06 | 01-02/03: `internal/logging`, `cmd/vde/config.go`, `internal/provider`, `internal/ground`; 03-06: `internal/config/schema*.go` |
| 3 | 02-01, 02-02, 02-03, 02-04 (sequential: all touch `internal/api` and `cmd/vde/serve.go`) | |
| 4 | 03-01 … 03-05 (sequential); 04-01, 04-02 (sequential) in parallel with them | 03: `internal/api`, `internal/worker`, `internal/sweeper`, `cmd/vde`; 04: `internal/provider/**` |
| 5 | 05-01, 05-02; 08-02 | 05-01: `internal/extract/extract.go`, `verdict.go`; 05-02: `cmd/vde/serve.go`, `cmd/vde/extract.go`, `internal/api/submit*.go`; 08-02: `internal/config`, `docs/operator/configuration.md` |
| 6 | 06-01, 06-02 | 06-01: scenario tests in `cmd/vde/extract_test.go`, `internal/extract/*_test.go`, `testdata/scenarios`; 06-02: the other `_test.go` files |
| 7 | 09-01, then 09-02 | |
| 8 | 08-04, then 08-01 (docs last, after every job that edits docs) | |
| 9 | 10-01 | |
