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

`vde extract` runs one document through the extractor and the judge and prints the
job-shaped result as JSON on stdout. With a key set:

```
export ANTHROPIC_API_KEY=...
vde extract --config examples/vde.dev.yaml \
  --document examples/samples/booking.pdf --page examples/samples/booking.png \
  --extractor-instructions examples/samples/extractor.md \
  --judge-instructions examples/samples/judge.md \
  --schema examples/samples/schema.json
```

The media type comes from the extension (`.txt`, `.md`, `.html`, `.eml`, `.pdf`, `.png`,
`.jpg`, `.webp`, `.heic`; `--type` overrides it). `--page` adds a page image, in order, used
when the configured models read PDFs only as images (as with `vde.local-ollama.yaml`). Leave
out `--schema` for schema-less extraction; the instructions then describe the JSON.
`--max-attempts`, `--timeout` and `--html raw_html|visible_text` override the configuration
for this run, and `--verbose` prints one line per attempt and per model call (endpoint,
outcome, tokens) to stderr, never document content.

The result has `status` (`accepted`, `rejected` or `failed`), `result`, `feedback`,
`attempts`, `service_version`, `judge_confidence`, `error` (a code such as `timeout` or
`provider_unavailable` when failed), `finished_at` and, for an `.eml`, the `attachments`
it lists but does not read. The exit code is 0 accepted, 2 rejected, 3 failed and 1 for a
usage or configuration error.

### Try it without a key

`--provider-script` replaces every endpoint's provider with one fake that replays a
script, so no key is needed and nothing leaves the machine. The scenarios in
[`testdata/scenarios/`](../testdata/scenarios/) are such scripts:

```
vde extract --config examples/vde.dev.yaml \
  --document examples/samples/booking.eml \
  --extractor-instructions examples/samples/extractor.md \
  --judge-instructions examples/samples/judge.md \
  --provider-script testdata/scenarios/accept-first-try.yaml --verbose

vde extract --config examples/vde.dev.yaml \
  --document examples/samples/booking.txt --schema examples/samples/schema.json \
  --extractor-instructions examples/samples/extractor.md \
  --judge-instructions examples/samples/judge.md \
  --provider-script testdata/scenarios/three-rejections.yaml   # exits 2, rejected
```

The fake's answers are scripted, not read from the document. `make scenarios` runs every
scenario and checks its outcome.

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
