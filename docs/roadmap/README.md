# Roadmap

Status: in implementation since 2026-10-16. Each job file carries its own `Status`.

The roadmap is organized as **versions → epics → jobs**. A version is a releasable
milestone with an exit criterion. An epic is a coherent body of work inside a version with
its own folder and `README.md`. A job is one markdown file with a goal, a task checklist
and acceptance criteria, sized so one person can finish it in roughly one to three days.

Architecture, package layout and the decisions behind them are in
[`docs/architecture/overview.md`](../architecture/overview.md). Decisions taken so far, and any question still waiting for an
answer from the project owner or from TravelInbox are in [`open-questions.md`](open-questions.md).
Ideas deliberately kept out of the first releases are in [`backlog.md`](backlog.md).

## Versions

| Version | Theme | Exit criterion |
| --- | --- | --- |
| [v0.1 Core engine](v0.1-core-engine/README.md) (done) | Configuration, provider adapters (Anthropic, OpenAI-compatible, Gemini), failover, document representation including email, the extraction loop, a CLI to run it | `vde extract` turns a PDF, an image, an HTML and a text document into accepted or rejected JSON against the fake and one real provider, with failover demonstrated per role |
| [v0.2 Service](v0.2-service/README.md) (in progress, conformance done) | The authenticated HTTP service: applications, tokens, jobs, quota, retention, operations, admin API, packaging | TravelInbox contract v1 acceptance checklist passes against a container deployment |
| [v0.3 Hardening and extensions](v0.3-hardening-and-extensions/README.md) | Postgres and S3, Bedrock and Vertex, classifier judge, escalation, image conversion, webhooks and sync endpoint, evaluation harness, multi-replica | A second consumer can use the service without contract changes; the service runs with more than one replica |
| [v1.0 Stable](v1.0-stable/README.md) | API and configuration freeze, security review, performance baseline, release engineering | `/v1` and the configuration schema are frozen with a 12-month compatibility promise |

Versions are sequential; epics inside a version are listed in a suggested order but many
can proceed in parallel (each epic README says what it depends on).

## Guiding constraints

Taken from the project brief, the TravelInbox contract and the owner's direction:

- Lean, not over-engineered: standard library first, interfaces only where a second
  implementation is planned, one process and one binary.
- Security on by default: every `/v1` request authenticated, content never logged, only
  operator-approved endpoints receive documents.
- Bounded loops and waits everywhere; a judge failure is never acceptance.
- The TravelInbox contract's MUSTs decide the first deployable release; the service may be
  a superset, never a deviation.

## Conventions

- Folder names: `vX.Y-theme/NN-epic-name/NN-job-name.md`. Numbers give the suggested
  order; they are not strict dependencies.
- Every job file has the same sections: header line (version, epic, status, depends on),
  Goal, Context, Tasks (checkboxes), Acceptance criteria, Out of scope.
- Status values: `planned`, `in progress`, `done`, `dropped`. Update the job file's header
  and the epic README's table together.
- Contract references are written as `contract §N` and point to
  `FromTravelApp/EXTRACTION-SERVICE-CONTRACT.md`. Brief references point to
  `verified-document-extraction.md`.
- A job that changes an architectural decision adds or amends an ADR in
  `docs/architecture/decisions/` in the same pull request.

## Working agreement for implementation

- Each job becomes one branch and one pull request, named after the job file.
- A job is `done` when its acceptance criteria are met by automated tests where tests are
  possible, and the epic README table is updated.
- Keep the fake provider the backbone of tests; real provider calls run only in an
  opt-in integration suite gated by environment variables.
