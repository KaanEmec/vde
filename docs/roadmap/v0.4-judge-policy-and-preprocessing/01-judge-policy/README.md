# Epic 01: Judge policy

Version: v0.4 · Status: in progress · Depends on: v0.3

Configuration, data model and API surface for `always | never | auto`, per application and
optionally per request. The job gains the `verification` field and the `extracted` status.
The loop behaviour behind the policies is epic 03. See
[ADR 0009](../../../architecture/decisions/0009-judge-policy-and-grounding.md).

| # | Job | Status |
| --- | --- | --- |
| 01 | [Policy configuration and `service_version`](01-policy-config.md) | in progress |
| 02 | [`extracted` status and `verification` field](02-status-and-verification.md) | in progress |
| 03 | [Per-request override, admin API and CLI flag](03-request-override.md) | in progress |
