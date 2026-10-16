# Examples

Ready-to-edit configurations and synthetic sample documents. Everything here is invented;
no real person, booking or receipt appears in it.

## Configurations

Each file is a complete configuration. Pick one, set the environment variable it names, and
check it. Only the key is a secret, and it never goes in the file: an endpoint names the
variable (`api_key_env`).

| File | Providers | Set this variable | Notes |
| --- | --- | --- | --- |
| [`vde.dev.yaml`](vde.dev.yaml) | Anthropic, one model for both roles | `ANTHROPIC_API_KEY` | The minimal development setup: one provider, one model, one key. |
| [`vde.openrouter.yaml`](vde.openrouter.yaml) | OpenRouter (preset) | `OPENROUTER_API_KEY` | Any model OpenRouter serves; the preset asks for no-retention routing. |
| [`vde.gemini.yaml`](vde.gemini.yaml) | Google Gemini | `GEMINI_API_KEY` | Use a paid-tier key for confidential documents. |
| [`vde.local-ollama.yaml`](vde.local-ollama.yaml) | Ollama on this machine | none | Fully local. No PDF reader, so PDFs need caller-supplied page images (a warning says so). |
| [`vde.failover.yaml`](vde.failover.yaml) | Anthropic, Gemini, OpenRouter | all three | Primary and fallback per role, across providers. |

Model names change. The ones here were current when the files were written; replace them
with models your account can use. **You** declare what each model reads in its `accepts`
list: `vde` does not probe the model, and a type listed for a model that cannot read it
makes jobs fail. Take the list from the provider's documentation and keep it conservative;
the files carry commented alternatives. See [`docs/providers/`](../docs/providers/README.md)
and the [configuration reference](../docs/operator/configuration.md).

### Check a configuration

```
export ANTHROPIC_API_KEY=...          # the variable named in the file
vde config check --config examples/vde.dev.yaml
```

The check makes no network call. It prints the endpoints (the key as its variable name and
"set", never the value), the groups, and for each role every media type with how it is
read and through which endpoint-model pair:

```
roles
  extractor: group main
    application/pdf   native  anthropic/claude-sonnet-4-5
    image/png         native  anthropic/claude-sonnet-4-5
    image/heic        -       cannot read
```

It exits 0 when the file is valid (warnings are printed to stderr), and 1 with every
problem listed when it is not. `--strict` turns warnings into errors; `--probe` also checks
that each endpoint answers.

### Extract a document

`vde extract` lands with v0.1 epic 06, job 01. Once it does, one document runs like this
(the samples below are what the command takes):

```
vde extract --config examples/vde.dev.yaml \
  --document examples/samples/booking.pdf \
  --extractor-instructions examples/samples/extractor.md \
  --judge-instructions examples/samples/judge.md \
  --schema examples/samples/schema.json
```

The result is one JSON object on stdout with `status` (`accepted`, `rejected` or `failed`),
`result`, `feedback` and `attempts`; the exit code is 0 for accepted. Until then,
`vde config check` is the part of the workflow you can run.

## Samples

[`samples/`](samples/) holds synthetic inputs, all in a generic business-document shape:

| File | What it is |
| --- | --- |
| `booking.txt` | A booking confirmation as plain text. |
| `booking.html` | The same confirmation as an HTML email body. |
| `booking.eml` | The same email as an `.eml` file: plain and HTML bodies plus `booking.pdf` as an attachment. |
| `booking.pdf` | A one-page PDF of the confirmation, with a real text layer. |
| `booking.png` | The same page rendered as an image, for models that read pages as images. |
| `receipt.jpg` | A JPEG "photo" of a small café receipt, a different document with its own amounts. |
| `extractor.md`, `judge.md` | Generic, domain-neutral instructions for the two roles. The judge file restates the verdict format `{"verdict": "accept"\|"reject", "feedback": {pointer: message}}`. |
| `schema.json` | A small JSON Schema (2020-12): title, date, parties, total and line items. |

The booking files all state the same facts (confirmation `HV-48213`, three line items, total
EUR 412.50), so the results from the text, HTML, email, PDF and image forms can be compared.
The images and the PDF were generated with a throwaway program; they are not meant to look
like a real document, only to carry the same text.

Your own documents belong in `testdata/user-docs/`, which git ignores.
