# Fake provider (tests only)

Adapter: `internal/provider/fake`. Configure with `provider: fake`.

The fake is a scripted provider for tests and offline experiments. It makes no network
calls, needs no API key and never sees a real document: it replays the answers and errors
of a script in order, optionally after a delay, and records every request so a test can
assert what each role was sent.

## What is sent, and where

Nothing leaves the process. There is no retention or training to approve, because no
third party is involved. For the same reason it must not be used as a stand-in for a real
endpoint in production: it never reads the document, it returns what the script says.

## Scripts

A script is a YAML file (`steps:` with an `answer` or an `error` per step); the format and
the matching rules are in the package documentation of `internal/provider/fake`, and
ready-made scenarios (failover, retries, judge feedback) are in `testdata/scenarios/`.

```yaml
endpoints:
  fake:
    provider: fake
    models:
      scripted:
        accepts: [application/pdf, image/png, image/jpeg, text/plain, text/html]
```

`base_url` and `api_key_env` are optional for this provider. The `accepts` list still
decides which representation of a document the engine builds, so tests set it to exercise
the representation they care about.
