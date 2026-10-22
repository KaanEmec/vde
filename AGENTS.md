# AGENTS.md

How to work in this repository, for any coding agent. Where the project stands and what
comes next is in [STATUS.md](STATUS.md). This file points at sources; it does not copy them.

## Purpose

`vde` is a self-hosted Go service (one binary, module `github.com/kaanemec/vde`) that turns
one document (PDF, image, HTML, text, email) into one JSON object: an extractor model
proposes, a separately configured judge model checks against the source, in a bounded loop.
Callers bring instructions and an optional JSON Schema per request; the service holds no
domain knowledge, and its first consumer, TravelInbox, defines the `/v1` contract.

## Non-negotiable principles

A change that breaks one of these needs an ADR first (`docs/architecture/decisions/`).
Full list: [overview, "Design principles"](docs/architecture/overview.md#design-principles).

| Principle | Where it is decided |
| --- | --- |
| Standard library first; third-party deps only from the "Implementation decisions" table | ADR 0001, [overview](docs/architecture/overview.md#implementation-decisions-build-start-2026-10-16) |
| An interface only where a second implementation exists (`Provider`, job `Store`, blob `Store`) | overview principle 2 |
| One process, one binary, `CGO_ENABLED=0`; `serve --role api\|worker` splits without a broker | ADR 0003, ADR 0007, ADR 0010 (pdfium as WebAssembly, still cgo-free) |
| Security on by default: every `/v1` route authenticated, only operator-configured endpoints receive documents | overview principle 5, [docs/security.md](docs/security.md) |
| Bounded loops and waits everywhere; a failed judge call is never acceptance | overview principle 6, ADR 0009 |
| Document content, instructions, schemas, candidates, feedback and secrets never in logs, errors, metrics or test output | `internal/logging`, `internal/audit`, [privacy](docs/operator/privacy.md) |
| Contract-first for `/v1`: the TravelInbox contract's MUSTs win; the service may be a superset, never a deviation | ADR 0005 (amended by ADR 0012: prompt order and answer blocks) |

## Repository map

| Path | Owns |
| --- | --- |
| `cmd/vde/` | `main`: subcommands `serve` (`--role`), `extract`, `eval`, `config check [--probe]\|schema`, `app`, `token`, `audit`, `migrate`, `healthcheck`, `version`; e2e, scenario and live tests |
| `api/` | `openapi.yaml` (public `/v1`) and `admin-openapi.yaml`, embedded (`embed.go`) |
| `internal/api/` | HTTP handlers, middleware (auth, request id, limits, quota headers), errors, admin routes, sync endpoint (`doc.go`) |
| `internal/audit/` | Typed audit events, never content (`doc.go`) |
| `internal/auth/` | Application tokens: generation, SHA-256 hashing, expiry, revocation, scopes (`doc.go`) |
| `internal/blob/` | Blob `Store` interface; filesystem and S3 implementations (`doc.go`) |
| `internal/config/` | YAML model, env-var secrets, defaults, validation, media types per role (`doc.go`) |
| `internal/document/` | Document model, media types, representation selection, HTML to text, email parsing, image conversion, PDF policy, grounding text (`doc.go`) |
| `internal/eval/` | Evaluation harness: corpus, `compare.yaml` rules, reports keyed by `service_version` (`doc.go`) |
| `internal/extract/` | The bounded extractor-judge loop, framings, parsing, schema feedback, verdicts, judge policy, sampling, `service_version`, the model probe of `config check --probe` (`doc.go`, loop in `extract.go`, `probe.go`) |
| `internal/ground/` | Deterministic grounding check, no model, no I/O (`doc.go`, ADR 0009) |
| `internal/job/` | Job rows and state machine, `Store` with SQLite and Postgres drivers, migrations in `migrations/` and `migrations_postgres/` (`doc.go`) |
| `internal/logging/` | slog setup with a fixed attribute vocabulary and redaction (`doc.go`) |
| `internal/metrics/` | Prometheus series from a private registry (`doc.go`) |
| `internal/pdf/` | pdfium on WebAssembly (go-pdfium, wazero): pages, text layer, rendering (`pdf.go`, ADR 0010) |
| `internal/provider/` | `Provider` interface, request/response types, error classes (`doc.go`); adapters `anthropic`, `openai` (OpenAI-compatible, OpenRouter preset), `gemini`, `bedrock`, `vertex`, `classifier`, `fake` (each with `doc.go`) |
| `internal/router/` | Endpoint groups, ordered failover, per-endpoint health; never across roles (`doc.go`) |
| `internal/version/` | Build info injected by `-ldflags` (`version.go`) |
| `internal/webhook/` | Signed completion webhooks (`webhook.go`) |
| `internal/worker/` | Worker pool: leasing, job deadline, retention action (`doc.go`); retention sweeper, advisory-locked across processes (`sweep.go`) |
| `test/conformance/` | Black-box suite for the TravelInbox contract (`doc.go`) |
| `docs/` | Architecture and ADRs, roadmap, operator, API, providers, conformance, security, quality |
| `examples/` | Example configs, compose (single and `scaled`), synthetic samples ([README](examples/README.md)) |
| `testdata/` | `scenarios/` (fake-provider scripts), `eval/` (corpus; `travelinbox` links to the TravelInbox corpus), `travelinbox/` (co-owned assets, [README](testdata/travelinbox/README.md)); `user-docs/` is local only |
| `FromTravelApp/` | TravelInbox contract, its OpenAPI and schema (read-only input) |
| `.githooks/` | `pre-push` runs `make check` (enable with `make hooks`); every check runs locally with `make ci` and releases with `make release`; GitHub only hosts the repository and the release artifacts |

## Where the specs live

- Brief: [verified-document-extraction.md](verified-document-extraction.md).
- Contract: [FromTravelApp/EXTRACTION-SERVICE-CONTRACT.md](FromTravelApp/EXTRACTION-SERVICE-CONTRACT.md)
  (cited as `contract §N`) and `FromTravelApp/extraction-service-api.yaml`; the service's
  superset is [api/openapi.yaml](api/openapi.yaml), see [docs/api/README.md](docs/api/README.md).
  MUST traceability: [docs/conformance/travelinbox-v1.md](docs/conformance/travelinbox-v1.md).
- Decisions: [docs/architecture/decisions/](docs/architecture/decisions/) 0001 to 0012; shape
  of the service: [overview](docs/architecture/overview.md); package layout: the repository
  map above.
- Work: [docs/roadmap/](docs/roadmap/README.md), versions → epics → jobs. A job file
  (`vX.Y-theme/NN-epic/NN-job.md`, header line `Version: · Epic: · Status: · Depends on:`)
  is the unit of work; its sections and status values are in the roadmap README,
  "Conventions". A done version is collapsed into `docs/roadmap/vX.Y-history.md`.
  Decisions and open questions: [open-questions.md](docs/roadmap/open-questions.md); deferred
  ideas: [backlog.md](docs/roadmap/backlog.md).

## Build, test, validate

| Command | What it does |
| --- | --- |
| `make check` | `golangci-lint` (v2 config in `.golangci.yml`, includes govet), `go test -race ./...`, a build that writes no binary (so it never touches `bin/vde`). Must pass before every commit |
| `make ci` | Every check, one summary line per step: tidy, lint, race suite with the `go.mod` toolchain and the local Go, `govulncheck`, `make fuzz`, cross-build with static check, conformance, eval, `make storage` (PostgreSQL and RustFS in Docker). Before merging into `main` |
| `go test -race ./...` | All unit and e2e tests; fake provider, no network |
| `make scenarios` | `testdata/scenarios` through the built CLI (`TestScenarios`) |
| `make conformance` | Builds `vde`, serves it against a fake OpenAI-compatible provider, walks contract §14 items 1 to 9 |
| `make eval` | `vde eval` on `testdata/eval` with the fake provider under `always`, `never`, `auto`; reports to `eval-reports/` |
| `make live` | `VDE_LIVE=1` smoke test (`TestLiveProviders`) against every provider whose key is set: `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `OPENROUTER_API_KEY`, `GEMINI_API_KEY`; model per provider `VDE_LIVE_<PROVIDER>_MODEL`, other host `VDE_LIVE_OPENAI_BASE_URL` |
| `make cross` / `make image` | Static linux/amd64 and arm64 binaries (checked static) / local Docker image |
| `make release VERSION=vX.Y.Z [DRY_RUN=1]` | Runs `make ci`, builds four archives and `checksums.txt` into `dist/` with the `go.mod` toolchain, the multi-arch image with buildx (GHCR), then tag and `gh release create` ([CONTRIBUTING.md](CONTRIBUTING.md#releasing)) |

Opt-in suites (skipped unless set): `VDE_TEST_POSTGRES` (DSN) and `VDE_TEST_S3_ENDPOINT`
(+ `VDE_TEST_S3_SSE`, `VDE_TEST_S3_BUCKET`, `VDE_TEST_S3_REGION`, AWS credentials) for the
storage drivers, as `make storage` runs them; per-adapter live tests under
`internal/provider/*/live_test.go` (`VDE_LIVE_BEDROCK_*`, `VDE_LIVE_VERTEX_*`, ...);
`VDE_USER_DOCS=1 go test ./internal/document -run UserDocs -v` for the owner's documents.
Conformance against a deployment: `VDE_CONFORMANCE_URL=<base, no /v1>
VDE_CONFORMANCE_TOKEN=<jobs token> [VDE_CONFORMANCE_FOREIGN_TOKEN=...] go test ./test/conformance/ -count=1 -v`
(no build, HTTP-only items, real provider cost; see the conformance doc, "Deployment mode").

## Conventions

- One commit per job or epic, message `vX.Y/<NN-epic>/<NN-job>: title` (e.g.
  `v0.2/09-contract-conformance/02: ...`, or `v0.4/01-judge-policy: ...` for a whole epic).
  Non-job commits use `docs(...)`, `fix(...)`, `chore: ...`. Each job lands as one commit
  on `main`; no branches or pull requests unless the owner asks.
- A job is `done` when its acceptance criteria are met, by automated tests where possible.
  Update the job file's `Status:` and the epic README's table in the same commit; a changed
  architectural decision adds or amends an ADR in the same commit.
- No new dependency without an ADR and a row in the overview's "Implementation decisions".
- Tests: standard `testing`, table-driven, named subtests, fake provider; no testify.
- Shared files (overview, CHANGELOG, READMEs, roadmap tables): targeted edits, not rewrites.
- Never build to `bin/vde` (`make build`) when a `vde serve` from it may be running; use
  `go run ./cmd/vde` or `go build -o "$TMPDIR/vde" ./cmd/vde`. `make check` writes no binary.
- Never print document content, instructions, candidates or API keys in output, commits,
  docs or reports. Eval reports carry numbers only.
- Do not commit anything under `testdata/user-docs/`, `.env`, `/vde.yaml`, `eval-reports/`,
  `bin/`, `/vde`, `/vde-data/` (all gitignored). Real-run reports worth keeping go to
  `docs/quality/` and a row in [docs/quality.md](docs/quality.md).

## Running against real models

- The owner's local, untracked material is in `testdata/user-docs/` (gitignored): ten real
  documents with personal data, the owner suite `testdata/user-docs/eval/` (10 cases), and
  configs such as `vde.nous.yaml` (CLI and eval) and `vde.serve.yaml` (local server).
- Keys live in `testdata/user-docs/.env` (gitignored; one variable, `NOUS_API_KEY`, named by
  the configs' `api_key_env`). `vde` does not read `.env`; export it into the shell
  (`set -a; . testdata/user-docs/.env; set +a`) and never echo it.
- Owner's default (2026-10-17): OpenAI-compatible endpoint (Nous Research); extractor
  `anthropic/claude-haiku-5.5` with `z-ai/glm-5.3-flash` as failover, judge
  `z-ai/glm-5.3-flash`, HTML `visible_text`.
- Real-model evals cost money: 3 to 5 cases per run, named with `--cases`, one run per
  change, `--concurrency 1`; never a whole corpus. Before trusting a new model, run
  `vde config check --probe` (one tiny request per role and pair).
- Commands: `go run ./cmd/vde eval --corpus testdata/user-docs/eval --config
  testdata/user-docs/vde.nous.yaml --judge always|auto|never [--compare eval-reports/<x>.json]`;
  the TravelInbox corpus with `--corpus testdata/eval --cases 'travelinbox-*'`. The full
  policy-comparison plan is in [docs/quality.md, "Judge policy"](docs/quality.md#judge-policy).
- `make live` through the same endpoint: `OPENAI_API_KEY=$NOUS_API_KEY
  VDE_LIVE_OPENAI_BASE_URL=<the base_url in vde.nous.yaml> VDE_LIVE_OPENAI_MODEL=<model> make live`.

## Orchestration notes

- Job `Status:` headers and the epic README tables are the source of truth for progress;
  any dashboard is generated from them, never the other way round.
- Parallel agents must own disjoint files; one orchestrator commits. The overview's
  "Build order" shows how waves were split.
- At the end of a session, update [STATUS.md](STATUS.md).

## Before you change X, read Y

| Change | Read first |
| --- | --- |
| Extraction loop, judge policy, grounding | ADR 0009 (with its 2026-10-18 amendment), `internal/extract/extract.go`, `testdata/scenarios/` incl. `judge-policy/`, [judge-policy.md](docs/operator/judge-policy.md) |
| Representation (HTML, images, PDF) | ADR 0006, ADR 0010, `internal/document`, `internal/pdf`, [pdf.md](docs/operator/pdf.md) |
| `/v1` API | Contract, `api/openapi.yaml`, [conformance doc](docs/conformance/travelinbox-v1.md) and its [review](docs/conformance/travelinbox-v1-review.md), `test/conformance/` |
| Storage | ADR 0002, ADR 0007, `internal/job` with a migration in both `migrations/` and `migrations_postgres/`, [storage.md](docs/operator/storage.md) |
| Providers | ADR 0004, [docs/providers/](docs/providers/README.md), `internal/provider` and `internal/router` |
| Configuration keys | [configuration.md](docs/operator/configuration.md) (kept in step by docs tests), `internal/config` |
| Quality claims | ADR 0008, [docs/quality.md](docs/quality.md), [evaluation.md](docs/operator/evaluation.md) |
