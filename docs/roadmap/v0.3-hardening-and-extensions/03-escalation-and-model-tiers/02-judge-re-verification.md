# Job: Judge-only re-verification option

Version: v0.3 · Epic: 03 Escalation and model tiers · Status: planned · Depends on: 01

## Goal

Optional per-application setting to run a second, independent judge (another group) on
an accepted result before reporting `accepted`, for callers who want two opinions on
high-stakes documents.

## Tasks

- [ ] `roles.second_judge: {group, instructions: inherit}` optional; when set, an
      `accept` from the first judge is confirmed by the second; a `reject` from the
      second feeds the next attempt; both failures follow the same never-accept rules.
- [ ] Only when configured; hashed into `service_version`; metrics.
- [ ] Decide after consumer demand; keep as a small addition to the loop, not a
      generalized "judge pipeline".

## Acceptance criteria

- Scenario tests; off by default with no behaviour change.

## Out of scope

Majority voting across many judges.
