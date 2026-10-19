# Roadmap

Status: v0.1, v0.2 and v0.3 done (2026-10-17); v0.4 done (2026-10-18, owner suite run pending); v1.0 done (2026-10-18; performance baseline and release engineering deferred by the owner); v1.1 Lean planned (2026-10-19); v1.2 Extraction quality planned (2026-10-19). Each job file carries its own `Status`.

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
| [v0.1 Core engine](v0.1-history.md) (done) | Configuration, provider adapters (Anthropic, OpenAI-compatible, Gemini), failover, document representation including email, the extraction loop, a CLI to run it | `vde extract` turns a PDF, an image, an HTML and a text document into accepted or rejected JSON against the fake and one real provider, with failover demonstrated per role |
| [v0.2 Service](v0.2-history.md) (done) | The authenticated HTTP service: applications, tokens, jobs, quota, retention, operations, admin API, packaging | TravelInbox contract v1 acceptance checklist passes against a container deployment |
| [v0.3 Hardening and extensions](v0.3-history.md) (done; [open items](v0.3-history.md#exit-criterion-check-2026-10-17)) | Postgres and S3, Bedrock and Vertex, classifier judge, escalation, image conversion, webhooks and sync endpoint, evaluation harness, multi-replica | A second consumer can use the service without contract changes; the service runs with more than one replica |
| [v0.4 Judge policy and preprocessing](v0.4-history.md) (done; [owner suite run pending](v0.4-history.md#exit-criterion-check-2026-10-18)) | Judge policy per application (`always`, `never`, `auto`) with a `verification` field and an `extracted` status, a deterministic grounding check against invented values, PDF text layer and page rendering in the service (pure-Go pdfium, ADR 0010) | On the owner suite under `auto`, no invented value is accepted without a judge, emails skip the judge when grounded, and PDFs are processed without caller-supplied pages |
| [v1.0 Stable](v1.0-history.md) (done; [scope decision](v1.0-history.md#exit-criterion)) | API and configuration freeze, security review, backpressure; performance baseline and release engineering deferred | `/v1` and the configuration schema are frozen with a 12-month compatibility promise |
| [v1.1 Lean](v1.1-lean/README.md) (planned) | Remove dead code, single-implementation abstractions, duplicated logic, generated testdata, unused files and duplicated docs without breaking the 1.x promise; each task names a vetted skill and prompt ([skills.md](v1.1-lean/skills.md)); breaking cuts only deprecated, after owner decisions | `v1.1.0` with identical conformance and eval results, at least 1,800 fewer production lines, 1,000 fewer test lines and 350 fewer tracked files |
| [v1.2 Extraction quality](v1.2-extraction-quality/README.md) (planned) | Framings (document first, consistent wording, an analysis block before the answer for both roles, native structured outputs where supported), document preparation (context line, email section labels, HTML data tables as Markdown), a schema guide for callers, multi-pass extraction only after an ADR; each change measured on real models and kept only if not worse | On a fixed core set of four owner documents (every real-model eval run 3 to 5 documents), at least as many cases fully correct and fewer field mismatches than `v1.1.0`, no case worse, conformance unchanged |

Versions are sequential: v0.4 comes before v1.0, so the v1.0 freeze covers what v0.4
adds. Epics inside a version are listed in a suggested order but many can proceed in
parallel (each epic README says what it depends on).

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
  order; they are not strict dependencies. A done version is collapsed into one
  `vX.Y-history.md` (epic tables; per job its header line, goal, acceptance criteria and
  commit); the full job files stay in git (decision 17, L6).
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
