# Job: CLI boilerplate and one local runner

Version: v1.1 · Epic: 03 Single source · Status: planned · Depends on: 03-03

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

- [ ] Plan and execute.
  - Skill: GOREF.
  - Prompt: "/golang-refactoring Plan mode: in cmd/vde add one flag helper (new flag
    set, usage, parse with ErrHelp and the positional-argument check) used by every
    subcommand; one openOperator(path) for app, audit, migrate and token; one localRunner
    shared by extract and eval; one pair formatter. Help text, flags, output and exit
    codes must not change (the cmd/vde tests compare them). Guardrails: skills.md." Then
    "/golang-refactoring Execute mode: step <n>."
- [ ] Polish.
  - Skill: SIMPLIFIER.
  - Prompt: "Use the code-simplifier agent on cmd/vde (all non-test files) to remove what
    the shared helpers made redundant. Go project; follow AGENTS.md; ignore the JS/React
    standards. Do not split serveAs (05-02). Guardrails: skills.md."
- [ ] Gate.
  - Skill: REVIEW.
  - Prompt: `/code-review high <first>^..<last>`, then `make scenarios` and `make eval`.

## Acceptance criteria

- Each subcommand's flag parsing is under five lines; `TestRunExtract`,
  `TestRunConfigCheck` and the eval tests pass unchanged.

## Out of scope

- Adopting a CLI library (a new dependency).
