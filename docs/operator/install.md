# Install

Three ways to run `vde`: a release binary, the container image, or the compose example.
All three read one YAML file and keep their state in one data directory.

## Before you start

You need a provider key (see [providers](providers.md)) and a configuration file. Start from
one of the [example files](configuration.md#the-example-files); [`examples/vde.dev.yaml`](../../examples/vde.dev.yaml)
is the smallest.

## Release binary

Each release on GitHub lists four archives and `checksums.txt` (SHA-256):

| Archive | Platform |
| --- | --- |
| `vde_<version>_linux_amd64.tar.gz` | Linux x86-64 |
| `vde_<version>_linux_arm64.tar.gz` | Linux arm64 |
| `vde_<version>_darwin_amd64.tar.gz` | macOS Intel |
| `vde_<version>_darwin_arm64.tar.gz` | macOS Apple silicon |

An archive holds the `vde` binary, `LICENSE`, `README.md`, `CHANGELOG.md` and `examples/`.
The binary is static (no cgo) and has no runtime dependencies.

```
sha256sum --check --ignore-missing checksums.txt
tar -xzf vde_0.2.0_linux_amd64.tar.gz
sudo install vde /usr/local/bin/vde
vde version
```

To build from source instead, with Go installed: `make build` writes `bin/vde`.

Run it:

```
export ANTHROPIC_API_KEY=...                     # the variable your endpoint names
vde config check --config /etc/vde/vde.yaml      # no network call
vde serve --config /etc/vde/vde.yaml
```

`vde serve` runs in the foreground, logs to stderr and stops on `SIGINT` or `SIGTERM`: it
stops accepting connections, drains in-flight requests and running jobs for up to 30
seconds, and a job still running then resumes after the next start. Use a service manager
(systemd, launchd) to run it as a daemon and to pass the key variables; a minimal systemd
unit sets `User=`, `EnvironmentFile=` with the keys (mode `0600`), `ExecStart=` and
`Restart=on-failure`.

Configuration is read once at start. After editing the file or changing a key variable,
restart the service.

## Container image

The image is `ghcr.io/kaanemec/vde`, for `linux/amd64` and `linux/arm64`, built on
`gcr.io/distroless/static:nonroot`. Tags: `X.Y.Z` for every release, `X.Y` and `latest` only
for stable versions. Pin a version for anything that matters.

| Item | Value |
| --- | --- |
| User | `nonroot` (uid and gid 65532) |
| Entrypoint, default command | `vde`, `serve --config /etc/vde/vde.yaml` |
| Configuration | Mount your file read-only at `/etc/vde/vde.yaml` |
| Data directory | `/var/lib/vde` (declared as a volume, owned by 65532) |
| Port | `8090` |
| Health check | `vde healthcheck` every 30 s (`/healthz` on the configured listener); the image has no `curl` |

```
docker run -d --name vde -p 127.0.0.1:8090:8090 \
  -e ANTHROPIC_API_KEY \
  -v "$PWD/vde.yaml:/etc/vde/vde.yaml:ro" \
  -v vde-data:/var/lib/vde \
  ghcr.io/kaanemec/vde:0.2.0
```

Inside a container the service must listen on every interface (`server.listen: ":8090"`),
and requests then arrive from the Docker network rather than loopback. Plain HTTP on such a
listener is refused unless you declare how TLS is handled: `server.tls`, `server.trusted_proxy`
or, for development only, `server.insecure_http` (see [TLS](#tls-or-a-reverse-proxy)). The
[compose configuration](../../examples/compose/vde.yaml) does the latter and publishes the
port on the host's loopback only.

Run operator commands in the running container; they use the same database:

```
docker exec vde vde token create --app dev-app --scope jobs --expires 90d
docker exec vde vde app list
```

To build the image from a checkout: `make image` (`docker build` with version arguments).
With a bind mount instead of a named volume for the data directory, make it writable by
uid 65532 (`chown 65532:65532 dir`), or the service cannot create its database.

## Compose

[`examples/compose/`](../../examples/compose/README.md) holds a working setup (one provider,
one application) with its own walk-through: start, create a token, submit a document, poll,
read the quota. It also contains an optional Caddy service for TLS and a Prometheus profile.

## The data directory

`server.data_dir` (default `/var/lib/vde`) holds everything the service stores:

| Path | Content | Mode |
| --- | --- | --- |
| `vde.db`, `vde.db-wal`, `vde.db-shm` | SQLite database in WAL mode: applications, tokens, jobs, quota counters, audit events. Shared by `vde serve`, `vde token` and `vde app`. | `0600` |
| `blobs/` | Documents and page images of jobs, one directory per job. | `0700` / `0600` |

The service creates the directory (mode `0700`) and the database on first start and applies
database migrations itself. Requirements:

- Local, writable by the service user, on a disk that survives restarts. SQLite over a
  network file system (NFS, SMB) is not supported.
- One `vde serve` process per data directory. The `vde token` and `vde app` commands may run
  alongside it.
- Encrypted at rest if the documents are sensitive: the service does not encrypt anything.
  See [security](../security.md).
- Back it up as described in [operations](operations.md#backups).

With `storage.driver: postgres` and `blobs.driver: s3` the database and the blobs live
outside the data directory, which then holds only the PDF engine's cache, and several
replicas may run; see [storage](storage.md).

The default `/var/lib/vde` usually needs root to create. Outside a container, set `data_dir`
to a directory the service user owns.

## TLS or a reverse proxy

The API carries bearer tokens and documents, so HTTPS is required outside loopback. Exactly
one of these must hold for a listener that is not loopback, or `vde serve` refuses to start:

| Mode | Keys | What happens |
| --- | --- | --- |
| The binary terminates TLS | `server.tls.cert_file` and `key_file` | `vde` serves HTTPS itself (TLS 1.2 or newer). Both keys must be set together. Restart to load a renewed certificate. |
| A reverse proxy terminates TLS | `server.trusted_proxy: true` | `vde` serves plain HTTP behind the proxy and accepts a request only when it carries `X-Forwarded-Proto: https`; any other answers `400 bad_request`. |
| Loopback only | none of the above, `listen: 127.0.0.1:8090` (the default) | Plain HTTP from loopback peers. For local development. |
| Insecure | `server.insecure_http: true` | See below. |

Loopback means `127.0.0.1`, `[::1]` or `localhost`.

### Behind a reverse proxy

Set `server.trusted_proxy: true`, and:

- Make `vde` reachable only through the proxy: bind it to loopback or a private network and
  firewall the port. `vde` believes `X-Forwarded-Proto` from any peer; it does not check who
  sent it.
- Have the proxy set the header itself, replacing any value the client sent (Caddy and
  nginx's `proxy_set_header` do; Caddy does it by default).
- Do not log `Authorization` headers, bodies or query strings in the proxy.
- Allow request bodies up to `limits.request_bytes` (default 128 MiB) and a read time of 60
  seconds: nginx needs `client_max_body_size 128m;` and a matching `proxy_read_timeout`.
- Do not forward `/metrics`. The service answers `404` to a metrics request that carries a
  forwarding header; see [operations](operations.md#metrics).
- `GET /healthz` is exempt from the HTTPS rule, so a health probe sent straight to the
  service needs no `X-Forwarded-Proto` header.

The compose example has a commented Caddy service and a
[`Caddyfile`](../../examples/compose/Caddyfile) that does all of this.

### `insecure_http`

`server.insecure_http: true` accepts plain HTTP from any peer on any listener. Tokens and
documents then cross the network unencrypted, readable and alterable by anyone on the path.
`vde serve` logs a loud warning at start and `vde config check` warns. It exists for two
cases: local development, and containers on a trusted private network where something else
(a service mesh, an overlay network) already encrypts traffic. Never set it on a listener
that is reachable from untrusted networks. It cannot be combined with `server.tls`. The
compose example sets it only because the container must listen on all interfaces while the
host port is published on loopback.

## Check the installation

```
vde config check --config vde.yaml     # exit 0, summary of endpoints, groups, roles
curl -sS http://127.0.0.1:8090/healthz # {"status":"ok"}
```

`/healthz` answers `503` with reason codes when something is wrong; see
[troubleshooting](troubleshooting.md#healthz-503). Then
[create an application and a token](applications-and-tokens.md).
