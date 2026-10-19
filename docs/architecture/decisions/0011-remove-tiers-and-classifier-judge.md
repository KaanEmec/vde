# ADR 0011: Remove escalation, the second judge and the classifier judge in 2.0

Status: proposed, 2026-10-19 · Amends: [ADR 0004](0004-provider-adapters.md),
[ADR 0009](0009-judge-policy-and-grounding.md) · Revises: [open questions](../../roadmap/open-questions.md#decided),
decision 14

## Context

v0.3 added three ways to spend more on a job than one extractor group and one judge group:

- **Escalation** (`roles.extractor.escalation`, `roles.judge.escalation`): later attempts
  move to other groups.
- **Second judge** (`roles.second_judge`): a second group must also accept.
- **Classifier judge** (`provider: classifier`, with `models.<m>.questions`) and its
  **feedback fallback** (`roles.judge.feedback_fallback`, `feedback_group`): a judge that
  answers typed questions with probabilities, the answer the owner wanted for decision 14
  (TypeSafe AI's Jev). ADR 0004 planned it as a judge adapter.

None of them is used: no example configuration, scenario, eval case or owner configuration
sets them, and no vendor classifier API was available to test the adapter against (STATUS.md,
"What is NOT verified"). Each still costs code in the loop, the configuration, the
representation selection and `service_version`, and tests that keep it working.

Under the [compatibility policy](../../compatibility.md#configuration) configuration keys
of 1.0 stay accepted in every 1.x release. The owner decided (2026-10-19, L8 in open
questions decision 17) to deprecate them in 1.1 and remove them in 2.0; Bedrock and Vertex
stay. 1.1 deprecates them without a behaviour change: they keep working, and
`vde config check` and `vde serve` warn once for each one set (`endpoints.<name>.provider`
for a classifier, `roles.<role>.escalation`, `roles.second_judge`,
`roles.judge.feedback_fallback`).

## Decision (proposed, for 2.0)

- **One group per role.** `roles.extractor.group` and `roles.judge.group` are the only
  groups that serve a role; failover stays inside them (ADR 0004). `escalation`,
  `second_judge`, `feedback_fallback` and `feedback_group` are removed and become unknown
  keys (a load error, as every unknown key is).
- **No classifier provider.** `provider: classifier`, `models.<m>.questions` and the
  `internal/provider/classifier` adapter are removed. A judge is a model that writes the
  verdict JSON of contract §7. A classifier can still serve as judge behind an
  `openai_compatible` proxy that turns its answer into that JSON, pointer-level feedback
  included; the service no longer defines a classify contract.
- **Amends ADR 0004**: the bullet "the judge role may later be served by a
  classifier-style provider" no longer holds; the verdict keeps its optional `confidence`
  (`judge_confidence`, a stable `/v1` field).
- **Amends ADR 0009**: the loop asks one judge group per verdict. The judge policy,
  grounding and sampling are unchanged.
- **Revises decision 14**: the classifier judge is withdrawn; judge confidence stays.

### The 2.0 shape of `extract.Roles`

```go
type Roles struct {
	Extractor, Judge     *router.Group
	JudgeReparseTries    int
	FeedbackMessageChars int
}
```

`Escalation`, `at`, `SecondJudge`, `FeedbackJudge` and `Classifiers` go. `RoleGroups`
returns one group per role, so `RoleCapabilities` is `cfg.Capabilities` of each group and
`commonCapabilities` and `product` (the intersection of several groups' capabilities,
with "+"-joined pair combinations) go. `Groups`, `ExtractorHealthy` and `JudgeHealthy`
look at two groups. In the loop, `judged` becomes one `judge` call and `verdict`'s
feedback-group branch goes. `service_version` loses the `extractor_escalation`,
`judge_escalation`, `second_judge`, `feedback_fallback`, `feedback_judge` and `questions`
fields; all are `omitempty`, so the hash of every configuration that loads in 2.0 is the
one it had in 1.x.

## Size (measured 2026-10-19, at the 1.1 deprecation)

| Area | Production lines | Test lines |
| --- | --- | --- |
| `internal/provider/classifier` (whole package) and its wiring in `cmd/vde/providers.go` | 171 + 3 | 187 |
| `internal/extract/roles.go` (287 lines): escalation steps and `at` (about 45), second judge and feedback group in `NewRoles`, `Groups` and the health split (about 40), `RoleGroups` extras (about 12), `commonCapabilities` and `product` (53) | about 150 | `roles_test.go` (226): about 150 |
| `internal/extract/extract.go`: second judge in `judged` (22 lines, about 12 go), feedback group in `verdict` (20, about 16 go), comments | about 33 | `extract_tiers_test.go`: 402 (the whole file) |
| `internal/extract/version.go`: versioned steps, groups and questions | about 30 | `version_test.go`: about 25 |
| `internal/config`: the types (`Escalation`, `SecondJudge`, the feedback fields, `Question`, `ProviderClassifier`, about 50), `questions` (29), escalation in `role` (about 14), `judgeFeedback` (32), `hasClassifier` (13), classifier checks (about 10), `deprecatedRoles` (17) | about 165 | `validate_test.go`: about 280 (cases, `addClassifier`, `TestValidate_deprecated`) |
| `cmd/vde/config.go`: the summary lines and `judgeHasClassifier` (15) | about 35 | `config_test.go` (`tieredConfig` and its case): about 30 |
| `internal/worker` | 0 | `TestEscalationGroupCapabilities`: 26 |
| **Total** | **about 590** | **about 1,100** |

Documentation: `docs/providers/classifier.md` (117 lines) goes (the provider docs test
wants one page per adapter, so page and adapter go together); six rows of
`docs/operator/configuration.md`, the second-judge and feedback-group notes in
`docs/operator/framings.md`, `judge-policy.md`, `operations.md` and `evaluation.md`
shrink. `internal/config/schema.json` loses the `escalation` definition, `second_judge`,
the feedback keys, `questions` and the `classifier` enum value.

Dependencies: none. The classifier adapter uses the standard library only, and nothing
else above brings a module, so `go.mod` does not change. The binary gain was not measured
(small: no dependency leaves).

## Consequences

- An operator who used one of these must change the configuration before 2.0, guided by
  the 1.1 warnings: instead of escalation, a stronger extractor group (or more
  `max_attempts`); instead of a second judge, one stronger judge group (`vde eval` measures
  the difference, ADR 0008); instead of a classifier judge, an `openai_compatible` proxy
  that returns the contract's verdict JSON.
- Representation selection becomes per group again: no intersection of capabilities
  across groups, no "+"-joined pair combinations in `vde config check` output.
- Metrics keep their names; `group` on `vde_provider_calls_total` then names one group per
  role. `/healthz` reason codes are unchanged.
- No `/v1` change: none of the keys is visible to callers except through
  `service_version`, which does not change for configurations that load in 2.0.
- The removal waits for the owner to open a 2.0 version folder in the roadmap; until
  then this ADR stays proposed and ADR 0004, ADR 0009 and decision 14 stand as written.
