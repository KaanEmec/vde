# Epic 04: Provider adapters

Version: v1.1 · Status: done · Depends on: 01-03

The adapters each carry their own copy of the same HTTP-and-JSON transport, Vertex copies
the Anthropic and Gemini wire code (and the copies have drifted), and every adapter test
file re-implements the same helpers. The `Provider` interface stays (ADR 0004). Which
adapters stay at all is epic 09's question; this epic shrinks them whatever the answer.

| # | Job | Status |
| --- | --- | --- |
| 01 | [Shared JSON transport](01-shared-json-transport.md) | done |
| 02 | [Vertex reuses the Anthropic and Gemini wire code](02-vertex-reuses-wire.md) | done |
| 03 | [Shared adapter test helpers](03-provider-test-helpers.md) | done |
