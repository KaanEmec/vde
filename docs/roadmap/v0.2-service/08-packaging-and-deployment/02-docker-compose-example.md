# Job: Docker compose example

Version: v0.2 · Epic: 08 Packaging and deployment · Status: planned · Depends on: 01

## Goal

A copy-and-run compose file that starts the service with the minimal configuration, a
volume for data, and an optional reverse proxy with TLS, as TravelInbox's development
setup expects.

## Context

Contract §12 and §13 (docker compose in development, same VM in production; minimal
development configuration with one provider key).

## Tasks

- [ ] `examples/compose/docker-compose.yaml`: `vde` service with `./vde.yaml` mounted,
      `ANTHROPIC_API_KEY` (or the chosen provider) from `.env`, named volume, port 8090
      on loopback.
- [ ] `examples/compose/vde.yaml`: the dev configuration with one application
      `dev-app`, `trusted_proxy: false`, loopback listen.
- [ ] A commented optional Caddy service terminating TLS with `trusted_proxy: true` for
      production-like runs.
- [ ] `examples/compose/README.md`: start, create a token (`docker compose exec vde vde
      token create …`), submit the sample document with curl, poll, read quota.
- [ ] Optional `prometheus` profile scraping `/metrics`.

## Acceptance criteria

- A newcomer gets a `202` and an `accepted` or `rejected` job within ten minutes of
  cloning, following only the README.

## Out of scope

Kubernetes manifests.
