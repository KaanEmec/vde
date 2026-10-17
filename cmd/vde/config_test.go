package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testSecret = "sk-cmd-test-NEVER-PRINT-ME"

const goodConfig = `
endpoints:
  anthropic:
    provider: anthropic
    api_key_env: VDE_TEST_ANTHROPIC_KEY
    models:
      claude: {accepts: [application/pdf, image/png, image/jpeg, text/plain]}
groups:
  main:
    endpoints: [{endpoint: anthropic, model: claude}]
roles:
  extractor: {group: main}
  judge: {group: main}
`

// noPDFConfig is valid but warns: no pair reads PDFs natively.
const noPDFConfig = `
endpoints:
  local:
    provider: openai_compatible
    base_url: http://localhost:11434/v1
    models:
      vision: {accepts: [image/png, image/jpeg, text/plain]}
groups:
  main:
    endpoints: [{endpoint: local, model: vision}]
roles:
  extractor: {group: main}
  judge: {group: main}
`

// noPDFNativeConfig is noPDFConfig under documents.pdf.mode native (ADR 0006): PDFs need
// caller pages.
const noPDFNativeConfig = noPDFConfig + `
documents:
  pdf: {mode: native}
`

// tieredConfig escalates the extractor, adds a second judge and an LLM feedback group.
const tieredConfig = `
endpoints:
  anthropic:
    provider: anthropic
    api_key_env: VDE_TEST_ANTHROPIC_KEY
    models:
      claude: {accepts: [application/pdf, image/png, image/jpeg, text/plain]}
      opus: {accepts: [application/pdf, image/png, image/jpeg, text/plain]}
groups:
  main:
    endpoints: [{endpoint: anthropic, model: claude}]
  strong:
    endpoints: [{endpoint: anthropic, model: opus}]
roles:
  extractor: {group: main, escalation: [{from_attempt: 2, group: strong}]}
  judge: {group: main, feedback_fallback: llm, feedback_group: strong}
  second_judge: {group: strong}
rate: {shared: true}
`

// judgeConfig sets the judge policy on the role and per application (ADR 0009).
const judgeConfig = `
endpoints:
  anthropic:
    provider: anthropic
    api_key_env: VDE_TEST_ANTHROPIC_KEY
    models:
      claude: {accepts: [application/pdf, image/png, image/jpeg, text/plain]}
groups:
  main:
    endpoints: [{endpoint: anthropic, model: claude}]
roles:
  extractor: {group: main}
  judge: {group: main, policy: never}
applications:
  - {id: inbox}
  - {id: lab, judge: {policy: auto, allow_override: true, sample_rate: 0.2}}
`

const brokenConfig = `
endpoints:
  anthropic:
    provider: anthropic
    api_key_env: VDE_TEST_ANTHROPIC_KEY
    models:
      claude: {accepts: [application/pdf, image/gif]}
groups:
  main:
    endpoints: [{endpoint: anthropic, model: claud}]
roles:
  extractor: {group: main}
  judge: {group: review}
limits:
  job_timeout: 10m
server: {listen: ":8090"}
debug:
  log_content: true
`

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "vde.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// Not parallel: t.Setenv provides the provider key through the process environment.
func TestRunConfigCheck(t *testing.T) {
	t.Setenv("VDE_TEST_ANTHROPIC_KEY", testSecret)
	good := writeConfig(t, goodConfig)
	noPDF := writeConfig(t, noPDFConfig)
	noPDFNative := writeConfig(t, noPDFNativeConfig)
	broken := writeConfig(t, brokenConfig)

	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout []string
		wantStderr []string
	}{
		{
			name:     "valid configuration",
			args:     []string{"config", "check", "--config", good},
			wantCode: exitOK,
			wantStdout: []string{
				"configuration " + good + ": ok",
				"key VDE_TEST_ANTHROPIC_KEY (set)",
				"extractor: group main",
				"application/pdf  native  anthropic/claude",
				"image/webp       -       cannot read",
				"service_version",
			},
		},
		{
			name:     "escalation, second judge and feedback fallback",
			args:     []string{"config", "check", "--config", writeConfig(t, tieredConfig)},
			wantCode: exitOK,
			wantStdout: []string{
				"escalation: from attempt 2, group strong",
				"second judge: group strong",
				"classifier feedback fallback: llm, group strong",
				"worker size 4, poll_interval 500ms, lease 2m0s; rate limits shared (job store)",
			},
		},
		{
			name:     "judge policy of the role and of every application",
			args:     []string{"config", "check", "--config", writeConfig(t, judgeConfig)},
			wantCode: exitOK,
			wantStdout: []string{
				"    policy: never\n",
				"  inbox: judge policy never (roles.judge.policy), override not allowed\n",
				"  lab: judge policy auto, override allowed, sample rate 0.2\n",
			},
		},
		{
			name:       "default judge policy",
			args:       []string{"config", "check", "--config", good},
			wantCode:   exitOK,
			wantStdout: []string{"    policy: always\n"},
		},
		{
			name:     "PDFs rendered or read as text without a native reader",
			args:     []string{"config", "check", "--config", noPDF},
			wantCode: exitOK,
			wantStdout: []string{
				"application/pdf  pages   local/vision",
				"pdf: text layer when every page has 200 characters, else pages rendered at 150 dpi (documents.pdf.mode: auto)",
				"documents.pdf: mode auto, dpi 150, max_pages 20, min_text_chars_per_page 200, workers 1 (about 500 MB",
			},
		},
		{
			name:       "PDF readers sent as given",
			args:       []string{"config", "check", "--config", good},
			wantCode:   exitOK,
			wantStdout: []string{"pdf: sent as given (a pair reads application/pdf)"},
		},
		{
			name:       "warnings do not fail",
			args:       []string{"config", "check", "-config", noPDFNative},
			wantCode:   exitOK,
			wantStdout: []string{"application/pdf  pages   local/vision", "pdf: only caller pages"},
			wantStderr: []string{"warning: roles.extractor.group", "natively", "documents.pdf.mode: native"},
		},
		{
			name:       "strict turns warnings into errors",
			args:       []string{"config", "check", "--strict", "--config", noPDFNative},
			wantCode:   exitFailure,
			wantStderr: []string{"warning: roles.extractor.group", "2 blocking problem(s)"},
		},
		{
			name:     "all errors listed at once",
			args:     []string{"config", "check", "--config", broken},
			wantCode: exitFailure,
			wantStderr: []string{
				`error: endpoints.anthropic.models.claude.accepts: unknown media type "image/gif"`,
				`error: groups.main.endpoints[0].model: model "claud" is not declared`,
				`error: roles.judge.group: group "review" is not defined`,
				"error: limits.job_timeout:",
				"error: debug.log_content:",
				"5 blocking problem(s)",
			},
		},
		{
			name:       "probe hook without adapters",
			args:       []string{"config", "check", "--probe", "--config", good},
			wantCode:   exitOK,
			wantStderr: []string{`warning: endpoints.anthropic: probe not available for provider "anthropic" yet`},
		},
		{
			name:       "missing file",
			args:       []string{"config", "check", "--config", filepath.Join(t.TempDir(), "nope.yaml")},
			wantCode:   exitFailure,
			wantStderr: []string{"no such file"},
		},
		{
			name:       "unknown key",
			args:       []string{"config", "check", "--config", writeConfig(t, "endpoint: {}\n")},
			wantCode:   exitFailure,
			wantStderr: []string{"line 1: endpoint: unknown key"},
		},
		{
			name:       "no subcommand",
			args:       []string{"config"},
			wantCode:   exitUsage,
			wantStderr: []string{"Usage: vde config check"},
		},
		{
			name:       "unexpected argument",
			args:       []string{"config", "check", "extra"},
			wantCode:   exitUsage,
			wantStderr: []string{`unexpected argument "extra"`},
		},
		{
			name:       "unknown flag",
			args:       []string{"config", "check", "--frob"},
			wantCode:   exitUsage,
			wantStderr: []string{"flag provided but not defined"},
		},
		{
			name:       "help",
			args:       []string{"config", "check", "-h"},
			wantCode:   exitOK,
			wantStderr: []string{"-strict"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(tt.args, &stdout, &stderr)
			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s", code, tt.wantCode, &stdout, &stderr)
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
			if strings.Contains(stdout.String()+stderr.String(), testSecret) {
				t.Errorf("output contains the secret")
			}
		})
	}
}
