# Job: CLI boilerplate and one local runner

Version: v1.1 · Epic: 03 Single source · Status: done (Polish and Gate tasks left to the orchestrator) · Depends on: 03-03

## Goal

The subcommands of `cmd/vde` share one flag helper and one operator-store opener, and
`vde extract` and `vde eval` share one local-run setup. Structure only: every command's
flags, output and exit codes stay.

## Context

- Every subcommand repeats NewFlagSet, SetOutput, Usage, Parse/ErrHelp and the NArg check
  (10 to 12 lines each: `serve.go:72-89`, `extract.go:116-144`,
  `eval.go:83-104, 626-639`, `config.go:63-81`, `app.go:142-155`, `audit.go:35-52`,
  `migrate.go:21-34`, `healthcheck.go:25-38`, token); `parseFlags` exists but only five
  commands use it.
- "Load the operator config, open the store, report errors" four times: `app.go:156-166`,
  `audit.go:67-77`, `migrate.go:35-46`, `token.go:68-82`.
- Same local-run setup in `extract.go:190-311` and `eval.go:271-379` (provider-script
  override, `loadConfigEnv` and issue printing, judge policy and `service_version` copy,
  instructions and schema reading, `extract.Input`): one `localRunner`.
- `eval.go:247-257` `groupPairs` vs `config.go:175-178`: same formatting.
- About −150 lines.

## Tasks

- [x] Plan and execute.
  - Skill: GOREF.
  - Prompt: "/golang-refactoring Plan mode: in cmd/vde add one flag helper (new flag
    set, usage, parse with ErrHelp and the positional-argument check) used by every
    subcommand; one openOperator(path) for app, audit, migrate and token; one localRunner
    shared by extract and eval; one pair formatter. Help text, flags, output and exit
    codes must not change (the cmd/vde tests compare them). Guardrails: skills.md." Then
    "/golang-refactoring Execute mode: step <n>."
- [ ] Polish (orchestrator).
  - Skill: SIMPLIFIER.
  - Prompt: "Use the code-simplifier agent on cmd/vde (all non-test files) to remove what
    the shared helpers made redundant. Go project; follow AGENTS.md; ignore the JS/React
    standards. Do not split serveAs (05-02). Guardrails: skills.md."
- [ ] Gate (orchestrator).
  - Skill: REVIEW.
  - Prompt: `/code-review high <first>^..<last>`, then `make scenarios` and `make eval`.

## Acceptance criteria

- Each subcommand's flag parsing is under five lines; `TestRunExtract`,
  `TestRunConfigCheck` and the eval tests pass unchanged.

## Out of scope

- Adopting a CLI library (a new dependency).

## Result

- Flags: `newFlagSet(name, usage, stderr)` (main.go) builds every subcommand's set;
  `parseFlags` moved to main.go and `parseArgs` adds the "<name>: unexpected argument"
  check. serve, extract, config check, app list, audit list, migrate, healthcheck and
  token create use `parseArgs` (one call); eval, eval import-travelinbox, token list and
  token revoke keep `parseFlags` because their positional-argument message differs.
  `tokenFlagSet` is a one-line wrapper. Every message was identical before.
- Operator store: `openOperator(name, path, stderr)` for app, audit and migrate (config
  errors printed as they are, database errors after the command name, as before).
  Token keeps `openOperatorStore`: its config errors carry the command prefix and it
  checks `auth.max_token_ttl`, so sharing would change its output.
- Local run: `localRun` (configuration with the provider-script key stub, issues on
  stderr, judge policy, `service_version`, logger), `readJobFiles` and `localRun.input`
  are shared by `vde extract` and the eval `localEngine` (which embeds `*localRun`).
  Differences kept at the callers: extract loads the scenario script with `fake.Load`,
  eval parses it with `fake.Parse` and fills the expected answer per case; extract has
  `--html`, `--max-attempts`, `--timeout`, `OnAttempt` and one PDF engine per run; the
  schema compile error names the file (extract) or `eval.FileSchema` (eval). The logger
  is created before `--judge` is parsed in both now (it writes nothing on creation).
- `groupPairs` serves `vde config check` too.

Checks: `make check`, `make scenarios`, `make conformance` green; `make eval` identical to
`eval-reports/lean-baseline` (timings and the removed `summary.cost_usd` aside); no test
changed.

`git diff --stat` (Go): production 10 files, +200 −290 (net −90); tests unchanged.
