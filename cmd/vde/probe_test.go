package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

// probeSecret stands for answer text: it must never reach the output.
const probeSecret = "PROBE-ANSWER-TEXT-NEVER-PRINT"

// probeServer is an OpenAI-compatible endpoint answering per model: good parses in both
// roles (the judge rejects the wrong total), lenient accepts every candidate, broken
// answers prose without JSON, down fails with 503. It counts the requests.
func probeServer(t *testing.T, requests *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		var req struct {
			Model    string `json:"model"`
			Messages []struct {
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Messages) == 0 {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		var system string
		_ = json.Unmarshal(req.Messages[0].Content, &system)
		judge := strings.HasPrefix(system, "You are the judge")
		analysis := "<analysis>" + probeSecret + "</analysis>"
		var answer string
		switch {
		case req.Model == "down":
			http.Error(w, `{"error": "unavailable"}`, http.StatusServiceUnavailable)
			return
		case req.Model == "broken":
			answer = probeSecret + " no JSON here"
		case judge && req.Model == "lenient":
			answer = analysis + `<verdict>{"verdict": "accept", "feedback": {}}</verdict>`
		case judge:
			answer = analysis + `<verdict>{"verdict": "reject", "feedback": {"/total": "` + probeSecret + `"}}</verdict>`
		default:
			answer = analysis + `<json>{"invoice_number": "INV-2041", "issued": "2026-03-14", "total": 50, "currency": "EUR"}</json>`
		}
		content, _ := json.Marshal(answer)
		fmt.Fprintf(w, `{"choices": [{"message": {"content": %s}, "finish_reason": "stop"}], "usage": {"prompt_tokens": 321, "completion_tokens": 45}}`, content)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func probeConfig(baseURL, extractor, judge string) string {
	return `
endpoints:
  local:
    provider: openai_compatible
    base_url: ` + baseURL + `
    timeout: 5s
    models:
      good: {accepts: [text/plain]}
      lenient: {accepts: [text/plain]}
      broken: {accepts: [text/plain]}
      down: {accepts: [text/plain]}
groups:
  main: {endpoints: [` + extractor + `]}
  review: {endpoints: [` + judge + `]}
roles:
  extractor: {group: main}
  judge: {group: review}
`
}

func TestConfigCheckProbe(t *testing.T) {
	t.Parallel()
	pair := func(model string) string { return "{endpoint: local, model: " + model + "}" }
	tests := []struct {
		name         string
		probe        bool
		extractor    string
		judge        string
		wantCode     int
		wantRequests int32
		wantStdout   []string
		wantStderr   []string
	}{
		{
			name:      "without probe no network call",
			extractor: pair("broken"), judge: pair("down"),
			wantCode: exitOK, wantRequests: 0,
		},
		{
			name: "every pair ok", probe: true,
			extractor: pair("good"), judge: pair("good"),
			wantCode: exitOK, wantRequests: 2,
			wantStdout: []string{
				"extractor  local/good",
				"321 in, 45 out  ok\n",
				"judge      local/good",
				"probe: 0 of 2 pairs failed",
			},
		},
		{
			name: "failures and a warning", probe: true,
			extractor: pair("good") + ", " + pair("broken"),
			judge:     pair("lenient") + ", " + pair("down"),
			wantCode:  exitFailure, wantRequests: 4,
			wantStdout: []string{
				"extractor  local/broken",
				"unparseable: no JSON value was found",
				"judge      local/lenient",
				"warning: accepts a wrong candidate",
				"retryable provider failure (status 503)",
				"probe: 2 of 4 pairs failed",
			},
			wantStderr: []string{"probe: 2 pair(s) failed"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var requests atomic.Int32
			srv := probeServer(t, &requests)
			path := writeConfig(t, probeConfig(srv.URL, tt.extractor, tt.judge))
			args := []string{"config", "check", "--config", path}
			if tt.probe {
				args = append(args, "--probe")
			}
			var stdout, stderr bytes.Buffer
			code := run(args, &stdout, &stderr)
			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s", code, tt.wantCode, &stdout, &stderr)
			}
			if got := requests.Load(); got != tt.wantRequests {
				t.Errorf("%d requests, want %d", got, tt.wantRequests)
			}
			for _, w := range tt.wantStdout {
				if !strings.Contains(stdout.String(), w) {
					t.Errorf("stdout does not contain %q:\n%s", w, &stdout)
				}
			}
			for _, w := range tt.wantStderr {
				if !strings.Contains(stderr.String(), w) {
					t.Errorf("stderr does not contain %q:\n%s", w, &stderr)
				}
			}
			if !tt.probe && strings.Contains(stdout.String(), "\nprobe:") {
				t.Errorf("probe output without --probe:\n%s", &stdout)
			}
			if out := stdout.String() + stderr.String(); strings.Contains(out, probeSecret) || strings.Contains(out, "INV-2041") {
				t.Errorf("output carries answer or sample text:\n%s", out)
			}
		})
	}
}

// TestConfigCheckProbeDocs keeps the usage line and the operator docs in step.
func TestConfigCheckProbeDocs(t *testing.T) {
	t.Parallel()
	const synopsis = "vde config check [--config path] [--strict] [--probe]"
	if !strings.Contains(configUsage, synopsis) {
		t.Errorf("usage lacks %q", synopsis)
	}
	for file, want := range map[string]string{
		"../../docs/operator/README.md":     "`" + strings.ReplaceAll(synopsis, "|", `\|`) + "`",
		"../../docs/operator/operations.md": "vde config check --probe",
		"../../docs/providers/README.md":    "vde config check --probe",
	} {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), want) {
			t.Errorf("%s does not contain %q", file, want)
		}
	}
}
