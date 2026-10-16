# ADR 0005: Contract-first, the TravelInbox contract is the acceptance test

Status: accepted (planning), 2026-10-16

## Context

TravelInbox wrote `EXTRACTION-SERVICE-CONTRACT.md` and an OpenAPI 3.1 document so that
the service can be built in parallel without either side reading the other's code. It
includes an acceptance checklist, a live test and an evaluation corpus. The project brief
is broader (schema optional, emails, Swift and web clients) but less precise.

## Decision

- The service's own API document (`api/openapi.yaml`) starts as a copy of
  `FromTravelApp/extraction-service-api.yaml` and only grows: optional parts, extra
  endpoints, extra fields and error codes. Every MUST in the contract maps to at least one
  job in the roadmap and one automated test.
- Where the brief is broader, the service is a superset: `schema` is optional at the
  service level (TravelInbox always sends it), `message/rfc822` is a v0.3 media type,
  a synchronous endpoint and webhooks are v0.3 options.
- Where the brief and contract differ in detail (for example error shapes), the contract
  wins for `/v1`.
- The contract's open points are answered in `docs/roadmap/open-questions.md` and, once
  decided, fed back to TravelInbox as a new contract version.

## Consequences

- The first deployable release (v0.2) is "contract v1 conformant", measured by the
  checklist in section 14 of the contract and the live test.
- Generality for other consumers is added without breaking `/v1`.
