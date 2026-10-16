# Job: Capability declarations and data-handling notes

Version: v0.1 · Epic: 02 Provider adapters · Status: planned · Depends on: 01-02, 03, 04, 05

## Goal

The configuration knows, per endpoint-model pair, which media types the model reads, and
the operator documentation makes clear what each adapter sends and which provider settings
affect retention, so endpoints are approved knowingly.

## Context

Contract §4 ("which representation the models read") and §10 ("The service MUST document,
per provider adapter, what the adapter sends and which provider retention or training
settings it sets"). Brief: "Only operator-approved endpoints are eligible to receive
documents."

## Tasks

- [ ] `accepts` on each model entry is the single source of truth; the router and the
      representation selection read it through a small `Capabilities` lookup in
      `internal/config` (no separate registry).
- [ ] Ship commented example entries for common models in `examples/` with conservative
      `accepts` lists; make clear in docs that the operator is responsible for the list.
- [ ] A `docs/providers/README.md` index with a comparison table: adapter, document input,
      image input, text, local or hosted, retention notes link.
- [ ] A test that every provider package has a corresponding `docs/providers/<name>.md`.

## Acceptance criteria

- `vde config check` summary prints, per role, the media types it can read and through
  which endpoint-model pair.
- Documentation index exists and is linked from the README.

## Out of scope

Automatic capability probing (not planned; providers change too often and probing would
send a document).
