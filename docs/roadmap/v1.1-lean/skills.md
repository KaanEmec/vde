# Skills, guardrails and prompts for v1.1 Lean

Every task in this version names a **skill** and a **prompt**. This page says which skills
were vetted (research of 2026-10-19), how to install them, the guardrails every prompt
includes by reference, and the gate that closes every job. A job file's prompt assumes
the agent reads this page first; prompts say so with "Guardrails: skills.md".

## Vetted skills

Trust is judged on publisher, licence, what the skill runs, and whether its advice pushes
toward fewer layers (our goal) or more. "Installed" was checked on the owner's machine on
2026-10-19.

| Key | Skill | Source and licence | Trust | Install | Use it for |
| --- | --- | --- | --- | --- | --- |
| **EVID** | Go evidence tools: `deadcode`, `gopls` (references, `unusedfunc`, remove-unused-parameter), a one-off `golangci-lint` run, `go mod why` | Go project (`golang.org/x/tools`), golangci-lint | Trusted | Installed (`deadcode`, `gopls`, `golangci-lint` 2.14) | The input to every deletion: no symbol is removed without tool output that shows it unused |
| **SIMPLIFY** | `/simplify` (built in to Claude Code) | Anthropic | Trusted | None | Applying de-abstraction and cleanup to one package or path; four reviewers (reuse, simplification, efficiency, abstraction level). Built for diffs: on a whole package say what to remove |
| **GOREF** | `/golang-refactoring` v1.1.1 | [samber/cc-skills-golang](https://github.com/samber/cc-skills-golang/tree/main/skills/golang-refactoring), MIT | Trusted | Installed (`~/.claude/skills/golang-refactoring`) | Safe mechanics of a multi-step Go change: Plan mode (gopls blast radius, sign-off), Execute mode (one tool-driven step, `go build`/`vet`/`test -race`, one commit). Override its branch/worktree/PR stacking with our commit convention |
| **REVIEW** | `/code-review` (built in) | Anthropic | Trusted | None | The gate after each job: behaviour changes and bugs in the job's commit range |
| **SIMPLIFIER** | `code-simplifier` agent | [anthropics/claude-plugins-official](https://github.com/anthropics/claude-plugins-official/tree/main/plugins/code-simplifier), Apache-2.0 | Trusted | `/plugin install code-simplifier@claude-plugins-official` | A second polish pass on a named package. Its built-in standards are JS/React: the prompt must say "Go, follow AGENTS.md, ignore the JS/React standards" and widen its scope beyond recent edits |
| **PRTK** | `/pr-review-toolkit:review-pr` with agents `comment-analyzer`, `silent-failure-hunter`, `pr-test-analyzer` | [anthropics/claude-plugins-official](https://github.com/anthropics/claude-plugins-official/tree/main/plugins/pr-review-toolkit), Apache-2.0 | Trusted | `/plugin install pr-review-toolkit@claude-plugins-official` | Stale comments and `doc.go` text after deletions; no error path lost when a layer goes (a failed judge call is never acceptance); test coverage kept when tests merge. Skip its `type-design-analyzer` (it adds types) |
| **ARCH** | `/mattpocock-skills:improve-codebase-architecture` | [mattpocock/skills](https://github.com/mattpocock/skills), MIT; listed in the official marketplace pinned to a commit | Usable with caution | `/plugin install mattpocock-skills@claude-plugins-official` | Deciding which seams to collapse ("one adapter means a hypothetical seam"). It writes a report to `$TMPDIR` and may offer `GLOSSARY.md` and ADRs: tell it not to write repository files and that ADRs live in `docs/architecture/decisions/` |
| **CRITIC** | `architecture-critic` agent of `code-modernization` | [anthropics/claude-plugins-official](https://github.com/anthropics/claude-plugins-official/tree/main/plugins/code-modernization), Apache-2.0 | Agent trusted, plugin heavy (13 commands, hooks, scripts, writes `analysis/`) | Optional: `/plugin install code-modernization@claude-plugins-official`. Without it, paste the lens into the prompt (below) | Adversarial review of a plan or of a finished epic: "abstractions with exactly one implementation", over-engineering, simpler alternatives |
| **GODEPS** | `/golang-dependency-management` | samber/cc-skills-golang, MIT | Trusted | Installed | `go mod why`, `go mod tidy`, binary weight per module, `govulncheck` after a dependency leaves |
| **GOLINT** | `/golang-lint` | samber/cc-skills-golang, MIT | Trusted | Installed | The one-off audit run with `unparam`, `dupl`, `iface`, `gocognit`; never adds linters to `.golangci.yml` in this version |
| **GOIFACE** | `/golang-structs-interfaces` | samber/cc-skills-golang, MIT | Trusted | Installed | The rule set for interface removal: define where consumed, return concrete types, no premature interfaces |
| **DOCKER** | `/multi-stage-dockerfile` | github/awesome-copilot (generic, short) | Usable | Installed | Reviewing the Dockerfile and `.dockerignore` only |
| **SEC** | `/security-review` (built in) | Anthropic | Trusted | None | Jobs that touch auth, fetch, webhooks or limits, and the closeout |
| **LSP** | `gopls-lsp` plugin | anthropics/claude-plugins-official, Apache-2.0 | Trusted (an LSP config only) | `/plugin install gopls-lsp@claude-plugins-official` at project scope | Go-to-definition and references for every agent in this version |

**Researched and not used**, with the reason:

| Skill | Why not |
| --- | --- |
| wshobson/agents `code-refactoring`, `codebase-cleanup` (`/refactor-clean`) | Recommends adding interfaces, DI, factory, strategy and repository layers and more CI scanners: the opposite of this version |
| tirth8205/code-review-graph `refactor-safely`, `dead-code` | pip install that writes MCP entries, hooks and skills into every AI tool it finds; weak Go support. `deadcode` and gopls are sounder |
| cjber/skills `deadcode` and re-hosted "dead code" skills on aggregator sites | No track record, Python/TypeScript tools, unclear provenance |
| `feature-dev` (`code-architect`) | Blueprints for new features, not removal |
| `security-guidance` plugin | Hooks on every edit and commit that send diffs for review; costly during a large refactor. `/security-review` at the end is enough |
| samber `golang-continuous-integration` ("Improve" mode), `golang-documentation`, `golang-design-patterns` | They add jobs, docs or layers ("accept interfaces, inject dependencies"). Read-only reference at most |
| samber `golang-testing` | Recommends testify and interface mocks; AGENTS.md forbids testify |
| `claude-md-management`, `doc-coauthoring` | Only find `CLAUDE.md` / write new docs; no fit for consolidating `docs/` |
| Trail of Bits `code-improver` | Well guarded but an autonomous fix loop with its own ledger directory; optional for a single branch, not assigned to any task |
| `/batch` | Splits work across worktree agents that each commit; conflicts with "one orchestrator commits". Only for a pure mechanical sweep, and none is planned |

## Guardrails

Every prompt in this version says "Guardrails: skills.md". It means all of these:

1. **Behaviour stays.** `/v1` stays a superset of `api/openapi.v1.0.0.yaml`
   (`TestOpenAPISupersetOfSnapshot`); every 1.0 configuration key keeps being accepted
   (`internal/config/schema.json`, `docs/compatibility.md`); `service_version` is unchanged
   for a configuration that does not use what a job removes; log attribute names, metric
   names and audit kinds stay unless the job file says otherwise.
2. **Structure and behaviour never share a commit.** A job that changes behaviour says
   so in its Goal: 01-02, 03-06, 06-03, 09-01, 09-02, and the separate `fix(...)`
   commits in 03-03 (in-progress gauge) and 04-02 (Gemini and Vertex finish reasons).
3. **Evidence before deletion.** Run EVID first; delete only what the output and a
   `gopls references` check show unused. "Dead" is not "safe": check reflection, YAML and
   JSON tags, `text/template`, docs tests (`internal/config/docs_test.go`,
   `internal/ground/docs_test.go`, `internal/provider/docs_test.go`,
   `test/conformance/checklist_test.go`) and links from `docs/`.
4. **Keep the decided interfaces.** `provider.Provider`, `job.Store`, `blob.Store` stay
   (overview principle 2). Every other interface must justify a second production
   implementation or go.
5. **Nothing new.** No new dependency, package, interface, linter in `.golangci.yml`, `make ci`
   step or config key (one exception: the test-support package of 04-03, which replaces
   five copies). A job's net production line count goes down; record
   `git diff --stat` in the job file when done.
6. **Checks.** `make check` after every step; `make ci` (00-02) before a job is marked
   done. Also `make conformance` when `internal/api`
   or `cmd/vde/serve.go` changes; `make scenarios` and `make eval` when `internal/extract`,
   `internal/ground`, `internal/document`, `internal/eval` or a provider changes, compared
   with the baseline report (`--compare`, numbers identical).
7. **Privacy.** Never print document content, instructions, candidates or keys; never
   read `testdata/user-docs/`.
8. **Process.** Commit message `v1.1/<NN-epic>/<NN-job>: title`; update the job's `Status:`
   and the epic table in the same commit; one orchestrator commits; no worktrees or PR
   stacks unless the owner asks (this overrides GOREF's defaults). Parallel agents own
   disjoint files (the wave table in the [README](README.md#order-and-parallel-waves)).
9. **Comments.** Match the surrounding density; remove roadmap references in code
   comments ("epic 11", "job 01", "v0.3 selection") that the change touches.
10. **Stop and ask** before removing an exported identifier that `docs/` names, before any
    change with compatibility impact, and before anything in epic 09.

## Gate

The last task of every job runs the gate on the job's commit range:

- `/code-review high <first>^..<last>` (REVIEW): fix every confirmed finding.
- `/pr-review-toolkit:review-pr comments errors` (PRTK) when the job deleted code or
  layers: stale comments and lost error paths.
- `/security-review` (SEC) when the job file says so.

## Prompt conventions

- **SIMPLIFY:** `/simplify <path>` followed by one paragraph: what to remove, what to keep,
  "Guardrails: skills.md".
- **GOREF:** `/golang-refactoring Plan mode: <goal>. <inventory input>. Guardrails:
  skills.md.` then, after sign-off, `/golang-refactoring Execute mode: step <n> of the
  plan.`
- **Agents (SIMPLIFIER, CRITIC, PRTK agents):** natural language naming the agent:
  "Use the <agent> agent on <scope> to <goal>. Go project; follow AGENTS.md; ignore
  non-Go standards. Guardrails: skills.md."
- **CRITIC lens without the plugin:** "Review skeptically as a principal engineer: for
  each abstraction, is there a second implementation in production? Is there a simpler
  alternative? Rank findings Blocker, High, Medium, Nit."
