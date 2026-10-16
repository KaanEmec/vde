# Job: Per-attempt group escalation

Version: v0.3 · Epic: 03 Escalation and model tiers · Status: planned · Depends on: v0.1 05-05

## Goal

`roles.extractor.escalation: [{from_attempt: 3, group: extraction_strong}]` sends later
attempts to another group, keeping failover inside each group.

## Context

Brief: "optionally escalating to a stronger model is a separate setting." Contract §6:
"Escalation to a stronger model on later attempts, if configured, is invisible to the
caller except through `service_version`."

## Tasks

- [ ] Config and validation (groups exist, `from_attempt` increasing, ≤ `max_attempts`);
      hashed into `service_version`.
- [ ] Loop: choose the group for the attempt number; the representation must be
      readable by every group that may be used, checked at submit (else
      `unsupported_document`).
- [ ] Judge escalation is allowed too (a stronger judge for the final attempt).
- [ ] Metrics label for the group used; scenario tests.

## Acceptance criteria

- Scenario: attempts 1 and 2 on the primary group, attempt 3 on the strong group;
  `service_version` changes when escalation is configured.

## Out of scope

Automatic cost-based routing, dynamic model selection.
