# vde with docker compose

A development setup: the `vde` service with one provider (Anthropic), one model for both
roles and one application, `dev-app`. About ten minutes from clone to a first job.

| File | Purpose |
| --- | --- |
| [`docker-compose.yaml`](docker-compose.yaml) | The `vde` service, a named data volume, port 8090 on loopback; a commented Caddy service; an optional `prometheus` profile. |
| [`vde.yaml`](vde.yaml) | The configuration: [`../vde.dev.yaml`](../vde.dev.yaml) plus `server` and the `dev-app` application. |
| [`.env.example`](.env.example) | The variables compose reads: the provider key, the image tag. |
| [`Caddyfile`](Caddyfile) | TLS termination for the commented `caddy` service. |
| [`prometheus.yml`](prometheus.yml) | Scrape configuration for the `prometheus` profile. |

> **Status.** `vde serve`, `vde token` and the `/v1` API are the v0.2 work in progress. The
> steps below describe the interface as specified in
> [`docs/roadmap/v0.2-service/`](../../docs/roadmap/v0.2-service/README.md) and the
> [service contract](../../FromTravelApp/EXTRACTION-SERVICE-CONTRACT.md); they work once
> those jobs have landed. The image itself, the configuration check and `vde extract`
> work today.

## 1. Start

```
cd examples/compose
cp .env.example .env        # set ANTHROPIC_API_KEY in it; .env is ignored by git
docker compose up -d
docker compose ps           # vde should become healthy
```

This pulls `ghcr.io/kaanemec/vde:latest`. To run the code in this checkout instead:
`docker compose up -d --build`. To pin a release, set `VDE_IMAGE_TAG=0.2.0` in `.env`.

Check the configuration without starting anything and without a network call:

```
docker compose run --rm --no-deps vde config check --config /etc/vde/vde.yaml
```

The service answers on `http://127.0.0.1:8090` only; the port is not published on other
interfaces.

```
curl -sS http://127.0.0.1:8090/healthz     # {"status": "ok"}
```

## 2. Create a token

Tokens are issued by the operator on the command line, against the same database the
service uses (the `vde-data` volume). The token is printed once and only its hash is stored:

```
export VDE_TOKEN=$(docker compose exec -T vde vde token create \
  --app dev-app --scope jobs --expires 2027-12-31 --label "compose dev")
```

`--expires` takes a date or a duration and is required. Rotate by creating a second token
before revoking the first (`vde token list --app dev-app`, `vde token revoke <token id>`).

## 3. Submit a document

The samples in [`../samples/`](../samples/) are synthetic. From the repository root:

```
cd ../..
curl -sS http://127.0.0.1:8090/v1/jobs \
  -H "Authorization: Bearer $VDE_TOKEN" \
  -F "document=@examples/samples/booking.txt;type=text/plain; charset=utf-8" \
  -F "extractor_instructions=<examples/samples/extractor.md;type=text/markdown" \
  -F "judge_instructions=<examples/samples/judge.md;type=text/markdown" \
  -F "schema=<examples/samples/schema.json;type=application/schema+json" \
  -F "retention=none"
```

The answer is `202` with a job in status `queued` and an `id`.

## 4. Poll the job

```
curl -sS http://127.0.0.1:8090/v1/jobs/<id> -H "Authorization: Bearer $VDE_TOKEN"
```

Repeat until `status` is `accepted`, `rejected` or `failed` (usually seconds). `result` holds
the extraction, `feedback` the judge's objections when it was rejected, `attempts` how many
rounds it took. With `retention: none` the job disappears once you have read its terminal
state.

## 5. Read the quota

```
curl -si http://127.0.0.1:8090/v1/quota -H "Authorization: Bearer $VDE_TOKEN"
```

The body and the `X-Quota-Remaining` and `X-Quota-Reset` headers show what is left of
`dev-app`'s 1000 submissions per day (see `quota` in [`vde.yaml`](vde.yaml)).

## Reverse proxy with TLS (optional)

For a production-like run, uncomment the `caddy` service and the `caddy-*` volumes in
[`docker-compose.yaml`](docker-compose.yaml), set `server.trusted_proxy: true` in
[`vde.yaml`](vde.yaml) (requests must then carry `X-Forwarded-Proto: https`, which Caddy
sets), set `VDE_PUBLIC_HOST` in `.env`, and remove the `127.0.0.1:8090:8090` mapping of `vde`
so that only Caddy is reachable. With `VDE_PUBLIC_HOST=localhost` Caddy uses its own local
certificate authority, which is enough to try it (`curl -k https://localhost/healthz`).

## Prometheus (optional)

```
docker compose --profile prometheus up -d      # then open http://localhost:9090
```

`/metrics` is protected by a separate bearer token. Uncomment `metrics.token_env` in
[`vde.yaml`](vde.yaml) and the `VDE_METRICS_TOKEN` line of the `vde` service, and set
`VDE_METRICS_TOKEN` in `.env`. That key is planned in the metrics job (v0.2 epic 07); until
it lands the configuration check may reject it, so leave it commented.

## Data and cleanup

The database and any retained documents live in the `vde-data` volume and survive
`docker compose down`. `docker compose down -v` deletes them.

## Without compose

```
docker run --rm -p 127.0.0.1:8090:8090 -e ANTHROPIC_API_KEY \
  -v "$PWD/vde.yaml:/etc/vde/vde.yaml:ro" -v vde-data:/var/lib/vde \
  ghcr.io/kaanemec/vde:latest
```
