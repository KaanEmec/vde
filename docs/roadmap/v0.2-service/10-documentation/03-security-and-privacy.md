# Job: Security and privacy document

Version: v0.2 · Epic: 10 Documentation · Status: planned · Depends on: epics 02, 06, 07

## Goal

One document, `docs/security.md`, that states the threat model, the controls and their
limits, and a `SECURITY.md` with the vulnerability reporting process, so operators and
consumers can approve the service.

## Context

Brief, "Security and access tokens" in full. Contract §3 and §10. Architecture security
model.

## Tasks

- [ ] Threat model: who can reach the API, what a leaked token can do and for how long,
      what a leaked database file reveals (hashes, metadata, content for `default`
      retention), what providers receive.
- [ ] Controls table: authentication, scopes, ownership, HTTPS, hashing, limits, quota,
      rate limits, retention, redaction, audit, endpoint allow-listing (no request can
      name an endpoint or model), and their configuration keys.
- [ ] Explicit non-controls: no encryption of blobs by the service (host volume), no
      tamper-evident audit, one-process trust boundary.
- [ ] `SECURITY.md`: supported versions, private reporting channel, disclosure timeline.
- [ ] A checklist for operators before exposing the service to the internet.

## Acceptance criteria

- Reviewed by a second person; linked from the README.

## Out of scope

External audit (v1.0 security review).
