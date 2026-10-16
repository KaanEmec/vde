package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kaanemec/vde/internal/eval"
)

var (
	evalCorpus = filepath.Join(repoRoot, "testdata", "eval")
	evalScript = filepath.Join(evalCorpus, "fake-script.yaml")
)

func runEvalArgs(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(append([]string{"eval"}, args...), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func onlyReport(t *testing.T, dir string) eval.Report {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("reports in %s: %v, %v; want one", dir, paths, err)
	}
	r, err := eval.LoadReport(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// TestEval_fakeProvider runs the synthetic receipt cases with the make eval script and
// compares the report with itself.
func TestEval_fakeProvider(t *testing.T) {
	t.Parallel()
	out := t.TempDir()
	code, stdout, stderr := runEvalArgs(t, "--corpus", evalCorpus, "--config", scenarioConfig,
		"--provider-script", evalScript, "--cases", "receipt-*", "--concurrency", "3", "--out", out)
	if code != exitOK {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	r := onlyReport(t, out)
	s := r.Summary
	if s.Cases != 6 || s.Accepted != 6 || s.Correct != 6 || s.FalseAccepts != 0 || s.MeanAttempts != 2 || s.InputTokens == 0 {
		t.Errorf("summary %+v", s)
	}
	if !strings.HasPrefix(r.ServiceVersion, "vde-") || len(r.Extractor) != 2 || r.Cases != "receipt-*" {
		t.Errorf("report header %+v", r)
	}
	for _, content := range []string{"Greenleaf", "Copper Kettle", "Swiftride", "12.35"} {
		if strings.Contains(stdout+stderr, content) {
			t.Errorf("output carries document content %q", content)
		}
	}
	if !strings.Contains(stdout, "false accepts      0 of 6 accepted") || !strings.Contains(stderr, "[6/6]") {
		t.Errorf("stdout:\n%s\nstderr:\n%s", stdout, stderr)
	}

	code, stdout, _ = runEvalArgs(t, "--corpus", evalCorpus, "--config", scenarioConfig,
		"--provider-script", evalScript, "--cases", "receipt-00[12]*", "--out", "",
		"--compare", filepath.Join(out, filepath.Base(mustGlob(t, out))))
	if code != exitOK || !strings.Contains(stdout, "compared with "+r.ServiceVersion) ||
		!strings.Contains(stdout, "receipt-003-hardware: not in this run") {
		t.Errorf("--compare: exit %d\n%s", code, stdout)
	}
}

func mustGlob(t *testing.T, dir string) string {
	t.Helper()
	paths, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	if len(paths) == 0 {
		t.Fatal("no report")
	}
	return paths[0]
}

// writeCase writes a schema-less case expecting {"total": 12.5, "name": "Ada"}.
func writeCase(t *testing.T, corpus, id string) {
	t.Helper()
	dir := filepath.Join(corpus, id)
	files := map[string]string{
		"document.txt":  "Name: Ada. Total 12.50.",
		"extractor.md":  "Extract name and total.",
		"judge.md":      "Check name and total.",
		"expected.json": `{"total": 12.5, "name": "Ada"}`,
		"compare.yaml":  "rules: [{pointer: /total, mode: numeric, tolerance: 0.01}]\n",
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestEval_falseAcceptAndHarnessError(t *testing.T) {
	t.Parallel()
	corpus, out := t.TempDir(), t.TempDir()
	writeCase(t, corpus, "a-wrong")
	writeCase(t, corpus, "b-unreadable")
	if err := os.Rename(filepath.Join(corpus, "b-unreadable", "document.txt"), filepath.Join(corpus, "b-unreadable", "document.xyz")); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(corpus, "script.yaml")
	if err := os.WriteFile(script, []byte(`steps:
  - {role: You are the extractor, repeat: true, answer: '{"total": 12.49, "name": "Eve"}'}
  - {role: You are the judge, repeat: true, answer: '{"verdict": "accept", "feedback": {}}'}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runEvalArgs(t, "--corpus", corpus, "--config", scenarioConfig, "--provider-script", script, "--out", out)
	if code != exitFailure {
		t.Errorf("exit %d, want %d for a harness error\n%s\n%s", code, exitFailure, stdout, stderr)
	}
	r := onlyReport(t, out)
	if s := r.Summary; s.Accepted != 1 || s.FalseAccepts != 1 || s.FalseAcceptRate != 1 || s.Errors != 1 || s.FieldAccuracy != 0.25 {
		t.Errorf("summary %+v", s)
	}
	if got := r.Results[0].Fields.Mismatches; len(got) != 1 || got[0] != "/name" {
		t.Errorf("mismatches %q, want [/name]", got)
	}
	if !strings.Contains(stdout, "FALSE ACCEPT") || strings.Contains(stdout+stderr, "Eve") {
		t.Errorf("stdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
}

// TestEval_url runs a case against a fake deployment: a 429 on the first submit, one
// poll in progress, then the terminal job. Not parallel: it shortens the poll timings.
func TestEval_url(t *testing.T) {
	pollFirst, pollMax, submitRetryWait = time.Millisecond, time.Millisecond, time.Millisecond
	t.Cleanup(func() { pollFirst, pollMax, submitRetryWait = 2*time.Second, 30*time.Second, 5*time.Second })

	var (
		mu      sync.Mutex
		submits int
		polls   int
		parts   []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer secret-token" {
			http.Error(w, `{"error":{"code":"unauthorized"}}`, http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/jobs":
			if submits++; submits == 1 {
				w.WriteHeader(http.StatusTooManyRequests)
				fmt.Fprint(w, `{"error":{"code":"rate_limited","message":"slow down"}}`)
				return
			}
			mr, err := r.MultipartReader()
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			for {
				p, err := mr.NextPart()
				if err != nil {
					break
				}
				body, _ := io.ReadAll(p)
				parts = append(parts, p.FormName()+"="+p.Header.Get("Content-Type")+"="+string(body[:min(len(body), 4)]))
			}
			w.WriteHeader(http.StatusAccepted)
			fmt.Fprint(w, `{"id": "j_1", "status": "queued"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/j_1":
			if polls++; polls == 1 {
				fmt.Fprint(w, `{"id": "j_1", "status": "running"}`)
				return
			}
			w.Header().Set("X-Service-Version", "vde-9.9.9+remote")
			fmt.Fprint(w, `{"id": "j_1", "status": "accepted", "result": {"total": "12.50", "name": "Ada"}, "attempts": 1}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	corpus, out := t.TempDir(), t.TempDir()
	writeCase(t, corpus, "remote")
	t.Setenv("VDE_EVAL_TEST_TOKEN", "secret-token")
	code, stdout, stderr := runEvalArgs(t, "--corpus", corpus, "--url", srv.URL+"/", "--token-env", "VDE_EVAL_TEST_TOKEN", "--out", out)
	if code != exitOK {
		t.Fatalf("exit %d\n%s\n%s", code, stdout, stderr)
	}
	r := onlyReport(t, out)
	if r.ServiceVersion != "vde-9.9.9+remote" || r.Target != srv.URL || r.Summary.Correct != 1 || r.Results[0].Usage != nil {
		t.Errorf("report %+v", r)
	}
	want := []string{
		"document=text/plain=Name", "extractor_instructions=text/markdown=Extr",
		"judge_instructions=text/markdown=Chec", "retention==none",
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(parts, "\n") != strings.Join(want, "\n") || submits != 2 || polls != 2 {
		t.Errorf("parts %q, submits %d, polls %d", parts, submits, polls)
	}
	if strings.Contains(stdout+stderr, "secret-token") {
		t.Error("output carries the token")
	}
}

func TestEval_usageErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"no corpus", nil, "--corpus is required"},
		{"argument", []string{"--corpus", evalCorpus, "extra"}, `unexpected argument "extra"`},
		{"concurrency", []string{"--corpus", evalCorpus, "--concurrency", "0"}, "at least 1"},
		{"config and url", []string{"--corpus", evalCorpus, "--config", "x.yaml", "--url", "http://127.0.0.1:1", "--token-env", "X"}, "exclude each other"},
		{"url without token", []string{"--corpus", evalCorpus, "--url", "http://127.0.0.1:1"}, "--url needs --token-env"},
		{"token without url", []string{"--corpus", evalCorpus, "--token-env", "X"}, "only for --url"},
		{"script and url", []string{"--corpus", evalCorpus, "--url", "http://127.0.0.1:1", "--token-env", "X", "--provider-script", evalScript}, "excludes --url"},
		{"token unset", []string{"--corpus", evalCorpus, "--url", "http://127.0.0.1:1", "--token-env", "VDE_EVAL_UNSET_TOKEN"}, "is not set"},
		{"no match", []string{"--corpus", evalCorpus, "--cases", "nothing-*"}, "no case"},
		{"bad glob", []string{"--corpus", evalCorpus, "--cases", "["}, "syntax error"},
		{"missing compare", []string{"--corpus", evalCorpus, "--compare", "missing.json"}, "--compare"},
		{"missing config", []string{"--corpus", evalCorpus, "--config", "missing.yaml"}, "config:"},
		{"import flags", []string{"import-travelinbox", "--from", "x"}, "--from and --to are required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			code, _, stderr := runEvalArgs(t, tt.args...)
			if code != exitUsage || !strings.Contains(stderr, tt.want) {
				t.Errorf("exit %d, stderr %q; want %d and %q", code, stderr, exitUsage, tt.want)
			}
		})
	}
}

func TestEval_importTravelInbox(t *testing.T) {
	t.Parallel()
	to := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := run([]string{"eval", "import-travelinbox", "--from", filepath.Join(repoRoot, "testdata", "travelinbox"), "--to", to}, &stdout, &stderr)
	if code != exitOK || !strings.Contains(stdout.String(), "50 case(s)") {
		t.Fatalf("exit %d\n%s\n%s", code, stdout.String(), stderr.String())
	}
	cases, err := eval.LoadCorpus(to, "travelinbox-*")
	if err != nil || len(cases) != 50 {
		t.Errorf("LoadCorpus(imported) = %d cases, %v", len(cases), err)
	}
	var expected map[string]any
	if err := json.Unmarshal(cases[0].Expected, &expected); err != nil || expected["schema_version"] == nil {
		t.Errorf("expected.json of %s: %v", cases[0].ID, err)
	}
	code = run([]string{"eval", "import-travelinbox", "--from", t.TempDir(), "--to", to}, &stdout, &stderr)
	if code != exitFailure {
		t.Errorf("import from an empty directory: exit %d, want %d", code, exitFailure)
	}
}
