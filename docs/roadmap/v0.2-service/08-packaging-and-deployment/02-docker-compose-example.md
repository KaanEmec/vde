# Job: Docker compose example

Version: v0.2 · Epic: 08 Packaging and deployment · Status: done · Depends on: 01

## Goal

A copy-and-run compose file that starts the service with the minimal configuration, a
volume for data, and an optional reverse proxy with TLS, as TravelInbox's development
setup expects.

## Context

Contract §12 and §13 (docker compose in development, same VM in production; minimal
development configuration with one provider key).

## Tasks

- [x] `examples/compose/docker-compose.yaml`: `vde` service with `./vde.yaml` mounted,
      `ANTHROPIC_API_KEY` (or the chosen provider) from `.env`, named volume, port 8090
      on loopback.
- [x] `examples/compose/vde.yaml`: the dev configuration with one application
      `dev-app`, `trusted_proxy: false`, loopback listen.
- [x] A commented optional Caddy service terminating TLS with `trusted_proxy: true` for
      production-like runs.
- [x] `examples/compose/README.md`: start, create a token (`docker compose exec vde vde
      token create …`), submit the sample document with curl, poll, read quota.
- [x] Optional `prometheus` profile scraping `/metrics`.

## Acceptance criteria

- A newcomer gets a `202` and an `accepted` or `rejected` job within ten minutes of
  cloning, following only the README.

## Implementation notes

- Files in `examples/compose/`: `docker-compose.yaml`, `vde.yaml`, `.env.example`,
  `README.md`, `Caddyfile`, `prometheus.yml`. The configuration listens on `:8090` inside the
  container (compose publishes it on `127.0.0.1` only), which is what "loopback listen" means
  for a container; `trusted_proxy: false`.
- Verified: `docker compose config` succeeds (also with `--profile prometheus`), and
  `vde config check` on `examples/compose/vde.yaml` in the built image exits 0.
- Not verified: starting the stack, token creation and the curl walkthrough, because `vde
  serve`, `vde token` and the `/v1` API were not complete when this job was written; the
  README documents them as specified (02-02 tokens, contract section 15). The commented
  Caddy service and the Prometheus profile were not run; the profile also needs
  `metrics.token_env` (job 07-02), which is therefore commented out in `vde.yaml`.
  The ten-minute acceptance criterion needs a manual run once `serve` lands.

## Out of scope

Kubernetes manifests.
