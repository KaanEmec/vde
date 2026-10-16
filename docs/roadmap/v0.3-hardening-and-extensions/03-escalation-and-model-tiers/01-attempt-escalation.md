# Job: Per-attempt group escalation

Version: v0.3 · Epic: 03 Escalation and model tiers · Status: done · Depends on: v0.1 05-05

## Goal

`roles.extractor.escalation: [{from_attempt: 3, group: extraction_strong}]` sends later
attempts to another group, keeping failover inside each group.

## Context

Brief: "optionally escalating to a stronger model is a separate setting." Contract §6:
"Escalation to a stronger model on later attempts, if configured, is invisible to the
caller except through `service_version`."

## Tasks

- [x] Config and validation (groups exist, `from_attempt` increasing, ≤ `max_attempts`);
      hashed into `service_version`.
- [x] Loop: choose the group for the attempt number; the representation must be
      readable by every group that may be used, checked at submit (else
      `unsupported_document`).
- [x] Judge escalation is allowed too (a stronger judge for the final attempt).
- [x] Metrics label for the group used; scenario tests.

## Acceptance criteria

- Scenario: attempts 1 and 2 on the primary group, attempt 3 on the strong group;
  `service_version` changes when escalation is configured.

## Out of scope

Automatic cost-based routing, dynamic model selection.

## Outcome

- `config.Role.Escalation []config.Escalation{FromAttempt, Group}` on both roles;
  validation: each group required and defined (and readable, as a warning), no classifier
  endpoint in the extractor's groups, `from_attempt` at least 2, strictly increasing, at
  most `limits.max_attempts`.
- `extract.Roles` carries `ExtractorEscalation` and `JudgeEscalation`; `NewRoles` builds
  one `*router.Group` per group name, shared by every place that names it. The loop picks
  the group from the absolute attempt number, so a resumed job (`PriorAttempts`) escalates
  on time. `Roles.Groups()` lists every distinct group (for health and failover hooks).
- `extract.RoleGroups(cfg)` returns each role's ordered group names (escalation, second
  judge and feedback group included); `extract.RoleCapabilities(cfg)` intersects their
  capabilities, a media type being readable only when every group of the role reads it,
  for `document.SelectWith`. `vde extract` uses it.
- `service_version` hashes the steps (from_attempt and the group's pairs); unset keeps the
  existing hash.
- Tests: `TestRun_escalation` (attempts 1-2 on primary, 3 on strong, for both roles),
  `TestRun_escalationResume`, `TestNewRoles_tiers`, `TestRoleGroups`,
  `TestRoleCapabilities`, version and validation cases.

Deviations:

- The submit path (`internal/api/submit.go`) and the worker (`internal/worker/pool.go`)
  still pass `cfg.Capabilities` of the two primary groups to `document.Select`; they must
  switch to `extract.RoleCapabilities(cfg)` (owned by other jobs). Until then a
  representation unreadable by an escalation group fails that attempt as unusable, never
  silently. Resolved in 07-01: both use `extract.RoleCapabilities(cfg)`.
- No separate group label in metrics: provider-call metrics already carry endpoint and
  model, which identify the group. `cmd/vde/serve.go` should attach `OnFailover` to
  `roles.Groups()` instead of the two primary groups. Resolved in 07-01: provider-call
  metrics carry a `group` label, the hooks cover `roles.Groups()`, and `/healthz` checks
  every group of a role.
