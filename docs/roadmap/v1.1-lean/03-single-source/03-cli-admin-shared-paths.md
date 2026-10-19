# Job: CLI and admin API share one code path

Version: v1.1 · Epic: 03 Single source · Status: planned · Depends on: 03-02

## Goal

`vde token`, `vde app` and the admin API use the same functions for token status,
revocation, creation checks, application rows, time parsing and job deletion, as v0.2
epic 11 intended ("the CLI and the admin API share one code path"). One behaviour fix
rides along in its own commit: deleting a queued job through `/v1` refreshes the
in-progress gauge.

## Context

- Application conversion twice: `cmd/vde/app.go:63-93` (`storeApplication`,
  `storedApplication`) and `internal/api/admin.go:144-195` (`settingsOf`, `checkApp`).
- Token status and revoke twice: `cmd/vde/token.go:253-261` vs `admin.go:340-347`;
  `token.go:196-251` vs `admin.go:430-451`. Use `job.Token.Status(now)` and one
  `auth.Revoke`.
- `admin.go:393-414` pre-checks expiry, max TTL and label, then `auth.Create` checks the
  same; `maxLabelChars` is defined in both packages. Let `auth.Create` return a typed
  error naming the field.
- `audit.ParseSince` (`audit.go:406-425`) and `auth.ParseExpires` (`token.go:120-139`)
  parse the same grammar in opposite directions: one parser with a sign.
- `DELETE /v1/jobs/{id}` (`jobs_get.go:33-55`) and `DELETE /admin/v1/jobs/{id}`
  (`admin.go:542-565`) are nearly identical, but only the admin path calls
  `refreshInProgress`: a `/v1` delete of a queued job leaves `vde_jobs_in_progress`
  stale. One `s.removeJob(w, r, j, reason)` fixes both.
- `admin.go:294-305` `loadApp` and `:567-578` `loadAnyJob` are the same shape (dupl).
- About −100 lines.

## Tasks

- [ ] Fix the gauge first, test first.
  - Skill: GOREF.
  - Prompt: "/golang-refactoring Execute mode: add a failing test that deletes a queued
    job through DELETE /v1/jobs/{id} and expects vde_jobs_in_progress to drop; then make
    it pass by having both delete routes call one removeJob helper. Commit as
    `fix(api): refresh the in-progress gauge on /v1 delete`. Guardrails: skills.md."
- [ ] Plan and execute the shared paths.
  - Skill: GOREF.
  - Prompt: "/golang-refactoring Plan mode: one application row conversion (in
    internal/config or internal/job), job.Token.Status, auth.Revoke, a typed field error
    from auth.Create replacing the admin pre-checks, one signed duration/date parser for
    ParseSince and ParseExpires, one loader for loadApp and loadAnyJob. CLI output and
    admin responses must stay identical. Guardrails: skills.md." Then
    "/golang-refactoring Execute mode: step <n>."
- [ ] Gate.
  - Skill: REVIEW, SEC.
  - Prompt: `/code-review high <first>^..<last>`, then `/security-review` (token
    creation and revocation changed).

## Acceptance criteria

- The admin e2e and CLI token/app tests pass unchanged; the new gauge test passes;
  `maxLabelChars` exists once.

## Out of scope

- Removing the admin API (a deliberate owner decision, open question 10).
