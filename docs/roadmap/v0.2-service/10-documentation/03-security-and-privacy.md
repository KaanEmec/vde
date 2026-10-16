# Job: Security and privacy document

Version: v0.2 · Epic: 10 Documentation · Status: in progress (written; second-person review open) · Depends on: epics 02, 06, 07

## Goal

One document, `docs/security.md`, that states the threat model, the controls and their
limits, and a `SECURITY.md` with the vulnerability reporting process, so operators and
consumers can approve the service.

## Context

Brief, "Security and access tokens" in full. Contract §3 and §10. Architecture security
model.

## Tasks

- [x] Threat model: who can reach the API, what a leaked token can do and for how long,
      what a leaked database file reveals (hashes, metadata, content for `default`
      retention), what providers receive.
- [x] Controls table: authentication, scopes, ownership, HTTPS, hashing, limits, quota,
      rate limits, retention, redaction, audit, endpoint allow-listing (no request can
      name an endpoint or model), and their configuration keys.
- [x] Explicit non-controls: no encryption of blobs by the service (host volume), no
      tamper-evident audit, one-process trust boundary.
- [x] `SECURITY.md`: supported versions, private reporting channel, disclosure timeline.
- [x] A checklist for operators before exposing the service to the internet.

## Acceptance criteria

- [ ] Reviewed by a second person (open: the document was written by its developers;
      reviewer not yet named).
- [x] Linked from the README.

## Out of scope

External audit (v1.0 security review).

## Outcome

Written as `docs/security.md` and `SECURITY.md` (repository root), linked from the README.
Every control is marked implemented or planned against the code of 2026-10-16; the planned
marks (job ownership, quota, rate limits, upload limits, retention deletion, metrics, per-job
audit events, content-log wiring) must be re-checked and removed when epics 04 to 07 land.
Found in the code and stated in the document: `trusted_proxy` trusts `X-Forwarded-Proto` from
any peer, and the database does not set `secure_delete`. The `SECURITY.md` acknowledgment
(7 days) is the project's own choice, not in the brief.
