package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/kaanemec/vde/internal/extract"
)

// liveProvider is one real provider the smoke test runs against when its key is set.
type liveProvider struct {
	name, keyEnv, modelEnv, model string
	endpoint                      string // the endpoint's YAML without models
	accepts                       string
}

var liveProviders = []liveProvider{
	{
		name: "anthropic", keyEnv: "ANTHROPIC_API_KEY", modelEnv: "VDE_LIVE_ANTHROPIC_MODEL", model: "claude-sonnet-4-5",
		endpoint: "provider: anthropic\n    api_key_env: ANTHROPIC_API_KEY",
		accepts:  "[application/pdf, image/png, image/jpeg, text/plain, text/html]",
	},
	{
		name: "openai", keyEnv: "OPENAI_API_KEY", modelEnv: "VDE_LIVE_OPENAI_MODEL", model: "gpt-4.1-mini",
		// VDE_LIVE_OPENAI_BASE_URL points the OpenAI-compatible adapter at another host
		// (Nous Research, vLLM, Ollama); the default is OpenAI itself.
		endpoint: "provider: openai_compatible\n    base_url: " + liveOpenAIBaseURL() + "\n    api_key_env: OPENAI_API_KEY",
		accepts:  "[application/pdf, image/png, image/jpeg, text/plain, text/html]",
	},
	{
		name: "openrouter", keyEnv: "OPENROUTER_API_KEY", modelEnv: "VDE_LIVE_OPENROUTER_MODEL", model: "anthropic/claude-sonnet-4.5",
		endpoint: "provider: openai_compatible\n    preset: openrouter\n    api_key_env: OPENROUTER_API_KEY",
		accepts:  "[application/pdf, image/png, image/jpeg, text/plain, text/html]",
	},
	{
		name: "gemini", keyEnv: "GEMINI_API_KEY", modelEnv: "VDE_LIVE_GEMINI_MODEL", model: "gemini-2.5-flash",
		endpoint: "provider: gemini\n    api_key_env: GEMINI_API_KEY",
		accepts:  "[application/pdf, image/png, image/jpeg, text/plain, text/html]",
	},
}

func liveOpenAIBaseURL() string {
	if u := os.Getenv("VDE_LIVE_OPENAI_BASE_URL"); u != "" {
		return u
	}
	return "https://api.openai.com/v1"
}

func (p liveProvider) config(t *testing.T) string {
	t.Helper()
	model := p.model
	if m := os.Getenv(p.modelEnv); m != "" {
		model = m
	}
	cfg := fmt.Sprintf(`endpoints:
  live:
    %s
    timeout: 120s
    models:
      %q: {accepts: %s}
groups:
  main:
    endpoints: [{endpoint: live, model: %q}]
roles:
  extractor: {group: main}
  judge: {group: main}
logging: {level: error, format: text}
`, p.endpoint, model, p.accepts, model)
	path := filepath.Join(t.TempDir(), "vde.yaml")
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// usageLine is the --verbose summary of a run's calls and tokens.
var usageLine = regexp.MustCompile(`(?m)^vde: usage: .*$`)

// documentCode is the refusal code in a document error, the only part of it printed for
// operator-supplied documents.
var documentCode = regexp.MustCompile(`document: ([a-z_]+)`)

// TestLiveProviders runs vde extract against every real provider whose key is set, on
// the sample documents with and without the sample schema, and on the operator's own
// documents in testdata/user-docs when present (PDFs with the sample schema, .eml
// schema-less). It is skipped unless VDE_LIVE=1. Token usage per run goes to stderr. For
// testdata/user-docs only the status, attempts, error code and usage are printed: the
// documents hold personal data, so neither their names nor any content is.
func TestLiveProviders(t *testing.T) {
	if os.Getenv("VDE_LIVE") != "1" {
		t.Skip("set VDE_LIVE=1 and at least one of ANTHROPIC_API_KEY, OPENAI_API_KEY, OPENROUTER_API_KEY, GEMINI_API_KEY")
	}
	var active []liveProvider
	for _, p := range liveProviders {
		if os.Getenv(p.keyEnv) != "" {
			active = append(active, p)
		}
	}
	if len(active) == 0 {
		t.Skip("VDE_LIVE=1 but no provider key is set")
	}

	type liveDoc struct {
		name     string
		args     []string // --document and --page
		schema   bool
		private  bool   // operator-supplied: print nothing but status, attempts, codes
		mustHave string // for the text sample: values visibly present in the document
	}
	schema := filepath.Join(samples, "schema.json")
	var docs []liveDoc
	for _, withSchema := range []bool{true, false} {
		suffix := map[bool]string{true: "schema", false: "schemaless"}[withSchema]
		for _, d := range []struct{ name, file, page string }{
			{"text", "booking.txt", ""},
			{"html", "booking.html", ""},
			{"email", "booking.eml", ""},
			{"pdf", "booking.pdf", "booking.png"},
			{"image", "receipt.jpg", ""},
		} {
			args := []string{"--document", filepath.Join(samples, d.file)}
			if d.page != "" {
				args = append(args, "--page", filepath.Join(samples, d.page))
			}
			doc := liveDoc{name: d.name + "-" + suffix, args: args, schema: withSchema}
			if d.name == "text" {
				doc.mustHave = "HV-48213|412.5"
			}
			docs = append(docs, doc)
		}
	}
	userDir := filepath.Join(repoRoot, "testdata", "user-docs")
	if entries, err := os.ReadDir(userDir); err == nil {
		var files []string
		for _, e := range entries {
			if ext := strings.ToLower(filepath.Ext(e.Name())); !e.IsDir() && (ext == ".pdf" || ext == ".eml") {
				files = append(files, e.Name())
			}
		}
		slices.Sort(files)
		for i, f := range files {
			isPDF := strings.EqualFold(filepath.Ext(f), ".pdf")
			docs = append(docs, liveDoc{
				name:    fmt.Sprintf("user-doc-%02d-%s", i+1, strings.TrimPrefix(strings.ToLower(filepath.Ext(f)), ".")),
				args:    []string{"--document", filepath.Join(userDir, f)},
				schema:  isPDF,
				private: true,
			})
		}
	}

	for _, p := range active {
		t.Run(p.name, func(t *testing.T) {
			cfg := p.config(t)
			for _, d := range docs {
				t.Run(d.name, func(t *testing.T) {
					args := append([]string{"extract", "--config", cfg, "--verbose",
						"--extractor-instructions", filepath.Join(samples, "extractor.md"),
						"--judge-instructions", filepath.Join(samples, "judge.md")}, d.args...)
					if d.schema {
						args = append(args, "--schema", schema)
					}
					var stdout, stderr bytes.Buffer
					code := run(args, &stdout, &stderr)
					if code == exitUsage {
						reason := "see stderr"
						if m := documentCode.FindStringSubmatch(stderr.String()); m != nil {
							reason = "document " + m[1]
						}
						if !d.private {
							reason = strings.TrimSpace(stderr.String())
						}
						t.Fatalf("exit 1: %s", reason)
					}
					var out jobOutput
					if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
						t.Fatalf("stdout is not a job JSON object: %v", err)
					}
					errCode := ""
					if out.Error != nil {
						errCode = out.Error.Code
					}
					fmt.Fprintf(os.Stderr, "live %s %s: %s %s after %d attempt(s); %s\n",
						p.name, d.name, out.Status, errCode, out.Attempts, strings.TrimPrefix(usageLine.FindString(stderr.String()), "vde: "))
					if !slices.Contains([]extract.Status{extract.StatusAccepted, extract.StatusRejected, extract.StatusFailed}, out.Status) {
						t.Errorf("status %q is not terminal", out.Status)
					}
					if out.Attempts < 1 {
						t.Errorf("attempts = %d, want at least 1", out.Attempts)
					}
					if d.mustHave != "" {
						found := false
						for _, v := range strings.Split(d.mustHave, "|") {
							found = found || bytes.Contains(out.Result, []byte(v))
						}
						if !found {
							t.Errorf("result does not contain any of %s, which the document states", d.mustHave)
						}
					}
				})
			}
		})
	}
}
