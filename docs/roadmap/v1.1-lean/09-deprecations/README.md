# Epic 09: Deprecations (owner decisions)

Version: v1.1 · Status: done · Depends on: owner decisions L7 to L9 (taken 2026-10-19); 02 and 04 done

The largest possible cuts are features nobody uses yet: they are speculative, but they
sit behind the 1.x compatibility promise. Under [docs/compatibility.md](../../../compatibility.md):

- **Experimental `/v1` parts** (completion webhook, `document.url`) may be removed in a
  minor release with a CHANGELOG entry.
- **Configuration keys and values** of 1.0 stay accepted in every 1.x release. Removing
  them means: deprecate in 1.1 (accepted, a warning from `vde config check` and `vde
  serve`, CHANGELOG "Deprecated"), remove in 2.0 together with an ADR. The 2.0 removal
  gets its own version folder only after the owner decides.
- **CLI surfaces** are operator surfaces, not frozen: removal is a CHANGELOG entry.

The decisions (2026-10-19) are recorded in
[open-questions.md](../../open-questions.md#decided).

| # | Job | Status |
| --- | --- | --- |
| 01 | [Remove the experimental `document.url`](01-experimental-api.md) | done |
| 02 | [Unverified providers and model tiers](02-unverified-providers-and-tiers.md) | done |
| 03 | [`vde eval --url`](03-eval-url.md) | dropped (L9) |
