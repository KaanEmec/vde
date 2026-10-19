# Job: Dead feature surfaces: content log sink and `--probe`

Version: v1.1 · Epic: 01 Dead code · Status: planned · Depends on: 01-01; owner decision L1, decided 2026-10-19 ([open questions](../../open-questions.md#decided), item 17)

## Goal

Remove two features that exist only as stubs: the `debug.log_content` sink that is opened
but never written, and the `vde config check --probe` flag whose probe table is empty.
This changes behaviour (an empty `content.log` is no longer created; `--probe` no longer
prints "probe not available"), so it is its own job.

## Context

- `internal/logging/content.go` (91 lines) and `cmd/vde/serve.go:217-225` open
  `content.log` in `server.data_dir` and warn at start; no provider adapter writes to it
  (`ContentLogger.Log` has no production caller). v0.2 job 06-03 ("log redaction") is
  still `in progress` because of it. About 145 lines with tests.
- `cmd/vde/config.go:40-45, 112-114, 321-340`: `probes` is an empty map, so `--probe`
  always warns. About 30 lines and one test case. `docs/operator/README.md` still calls it
  "planned for v0.3".
- `debug.log_content` is a frozen configuration key: it stays accepted. The lean option
  keeps it parsed and validated, and `vde serve` and `vde config check` warn that it has
  no effect (deprecated, removal in 2.0). The alternative is to finish v0.2 06-03, which
  adds code.
- The CLI is an operator surface, not frozen; removing `--probe` is a CHANGELOG entry.

## Tasks

- [x] Owner decision L1 (2026-10-19): drop the sink and `--probe`.
- [ ] Remove the content sink; keep the key with a "no effect, deprecated" warning.
  - Skill: GOREF.
  - Prompt: "/golang-refactoring Plan mode, then Execute mode: delete
    internal/logging/content.go, its tests and the wiring in cmd/vde/serve.go; keep
    `debug.log_content` in internal/config (struct, schema.json, validation) and make
    `vde serve` and `vde config check` print one warning when it is true: the key has no
    effect and is deprecated. Update docs/operator/configuration.md (the `debug.log_content`
    and `server.data_dir` rows) and docs/operator/privacy.md. Mark
    docs/roadmap/v0.2-service/06-retention-and-privacy/03-log-redaction.md `dropped` with
    the reason. Guardrails: skills.md."
- [ ] Remove `--probe` and the `probes` table.
  - Skill: SIMPLIFY.
  - Prompt: "/simplify cmd/vde/config.go — remove the `--probe` flag, the empty `probes`
    map and their test case; the flag is a stub that always warns. Fix
    docs/operator/README.md 'What is planned'. Add both removals to CHANGELOG
    'Unreleased'. Guardrails: skills.md."
- [ ] Gate.
  - Skill: REVIEW, SEC.
  - Prompt: `/code-review high <first>^..<last>`, then `/security-review` (the job touches
    the privacy path).

## Acceptance criteria

- No file named `content.log` is created by any configuration; a configuration with
  `debug.log_content: true` still loads, and both commands warn once.
- `vde config check --probe` fails as an unknown flag; CHANGELOG says so.
- `make check` green; `TestNoSentinelInLogs` unchanged and green.

## Out of scope

- Removing the `debug.log_content` key (2.0).
