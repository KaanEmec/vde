package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

const testSecret = "sk-test-0123456789-DO-NOT-LEAK"

// env returns a getenv func backed by a map, so tests stay parallel.
func env(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

var exampleEnv = env(map[string]string{
	"GEMINI_API_KEY":     testSecret,
	"OPENROUTER_API_KEY": testSecret,
	"ANTHROPIC_API_KEY":  testSecret,
})

func loadExample(t *testing.T) *Config {
	t.Helper()
	data, err := os.ReadFile("testdata/example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := Parse(data, exampleEnv)
	if err != nil {
		t.Fatalf("Parse(example) error: %v", err)
	}
	return cfg
}

func TestLoad(t *testing.T) {
	t.Parallel()
	if _, err := Load("testdata/example.yaml"); err != nil {
		t.Errorf("Load(example) error: %v", err)
	}
	_, err := Load("testdata/does-not-exist.yaml")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Load(missing) error = %v, want fs.ErrNotExist", err)
	}
}

func TestParse_example(t *testing.T) {
	t.Parallel()
	cfg := loadExample(t)

	if got := len(cfg.Endpoints); got != 4 {
		t.Errorf("endpoints = %d, want 4", got)
	}
	anthropic := cfg.Endpoints["anthropic"]
	if anthropic.Timeout != 90*time.Second {
		t.Errorf("anthropic timeout = %s, want 90s", anthropic.Timeout)
	}
	if anthropic.APIKey() != testSecret {
		t.Errorf("anthropic key not resolved from ANTHROPIC_API_KEY")
	}
	if got := cfg.Endpoints["local"].APIKey(); got != "" {
		t.Errorf("local key = %q, want empty", got)
	}
	if got := cfg.Endpoints["openrouter"].BaseURL; got != "https://openrouter.ai/api/v1" {
		t.Errorf("openrouter base_url = %q, want the preset URL", got)
	}
	if got := cfg.Endpoints["local"].Timeout; got != defaultEndpointTimeout {
		t.Errorf("local timeout = %s, want default %s", got, defaultEndpointTimeout)
	}
	accepts := anthropic.Models["claude-fable-5-1"].Accepts
	if len(accepts) != 5 || accepts[0] != "application/pdf" {
		t.Errorf("anthropic model accepts = %v", accepts)
	}
	extraction := cfg.Groups["extraction"]
	wantEntries := []GroupEntry{
		{Endpoint: "anthropic", Model: "claude-fable-5-1"},
		{Endpoint: "local", Model: "some-vision-model"},
	}
	if !reflect.DeepEqual(extraction.Endpoints, wantEntries) {
		t.Errorf("extraction endpoints = %v, want %v", extraction.Endpoints, wantEntries)
	}
	if got := cfg.Groups["review"].Failover; got != (Failover{MaxTries: 3, MaxTotalTime: 3 * time.Minute}) {
		t.Errorf("review failover = %+v, want defaults", got)
	}
	if cfg.Roles.Extractor.Group != "extraction" || cfg.Roles.Judge.Group != "review" {
		t.Errorf("roles = %+v", cfg.Roles)
	}
	wantLimits := Limits{
		MaxAttempts:       3,
		JobTimeout:        5 * time.Minute,
		JudgeReparseTries: 1,
		DocumentBytes:     25 << 20,
		PageBytes:         10 << 20,
		Pages:             20,
		RequestBytes:      128 << 20,
	}
	if cfg.Limits != wantLimits {
		t.Errorf("limits = %+v, want %+v", cfg.Limits, wantLimits)
	}
	if want := (Images{Enabled: true, MaxDimension: 2048, ConvertTo: "image/jpeg", Quality: 85}); cfg.Documents.Images != want {
		t.Errorf("documents.images = %+v, want %+v", cfg.Documents.Images, want)
	}
	if cfg.Server.Listen != ":8090" || cfg.Documents.HTML != "raw_html" || cfg.Logging.Format != "json" {
		t.Errorf("server/documents/logging = %+v %+v %+v", cfg.Server, cfg.Documents, cfg.Logging)
	}
}

func TestParse_roundTrip(t *testing.T) {
	t.Parallel()
	cfg := loadExample(t)
	out, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	again, err := Parse(out, exampleEnv)
	if err != nil {
		t.Fatalf("Parse(Marshal(example)) error: %v\n%s", err, out)
	}
	if !reflect.DeepEqual(cfg, again) {
		t.Errorf("round trip changed the configuration:\nfirst:  %s\nsecond: %s", cfg, again)
	}
}

func TestParse_defaults(t *testing.T) {
	t.Parallel()
	cfg, err := Parse([]byte(`
endpoints:
  a: {provider: anthropic, api_key_env: K, models: {m: {accepts: [text/plain]}}}
groups:
  g: {endpoints: [{endpoint: a, model: m}]}
roles: {extractor: {group: g}, judge: {group: g}}
applications:
  - {id: app1}
  - {id: app2, max_attempts: 2, quota: {limit: 5}}
`), env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Endpoints["a"].BaseURL; got != "https://api.anthropic.com" {
		t.Errorf("anthropic default base_url = %q", got)
	}
	if got := cfg.Groups["g"].Failover; got.MaxTries != 3 || got.MaxTotalTime != 3*time.Minute {
		t.Errorf("failover defaults = %+v", got)
	}
	want := Application{
		ID:          "app1",
		MaxAttempts: 3,
		Quota:       Quota{Limit: 1000, Window: "P1D"},
		Rate:        Rate{RequestsPerMinute: 300, SubmitsPerMinute: 30, MaxInProgress: 20},
		Retention:   Retention{Default: "24h"},
	}
	if !reflect.DeepEqual(cfg.Applications[0], want) {
		t.Errorf("application defaults = %+v, want %+v", cfg.Applications[0], want)
	}
	if a := cfg.Applications[1]; a.MaxAttempts != 2 || a.Quota.Limit != 5 {
		t.Errorf("explicit application values overwritten: %+v", a)
	}
	if cfg.Limits.JobTimeout != 5*time.Minute || cfg.Limits.Pages != 20 {
		t.Errorf("limit defaults = %+v", cfg.Limits)
	}
	if cfg.Server.Listen != "127.0.0.1:8090" || cfg.Server.InsecureHTTP {
		t.Errorf("server defaults = %+v, want loopback 127.0.0.1:8090 and no insecure_http", cfg.Server)
	}
	if cfg.Auth.MaxTokenTTL != 730*24*time.Hour {
		t.Errorf("auth.max_token_ttl default = %s, want 730 days", cfg.Auth.MaxTokenTTL)
	}
	if got := cfg.Server.DatabasePath(); got != "/var/lib/vde/vde.db" {
		t.Errorf("DatabasePath() = %q", got)
	}
}

func TestParseWindow(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want time.Duration
	}{
		{"P1D", 24 * time.Hour},
		{"PT1H", time.Hour},
		{"P7D", 7 * 24 * time.Hour},
		{"P1W", 7 * 24 * time.Hour},
		{"P1DT12H", 36 * time.Hour},
		{"PT30M", 30 * time.Minute},
		{"P1M", 0}, {"P1Y", 0}, {"P", 0}, {"PT", 0}, {"P0D", 0}, {"1D", 0}, {"24h", 0}, {"P1DT", 0}, {"p1d", 0}, {"", 0},
	}
	for _, tt := range tests {
		got, err := ParseWindow(tt.in)
		if tt.want == 0 {
			if err == nil {
				t.Errorf("ParseWindow(%q) = %s, want an error", tt.in, got)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("ParseWindow(%q) = %s, %v, want %s", tt.in, got, err, tt.want)
		}
	}
}

func TestParse_errors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		yaml string
		want []string // substrings that must all appear in the error
	}{
		{
			name: "empty file",
			yaml: "",
			want: []string{"file is empty"},
		},
		{
			name: "syntax error",
			yaml: "endpoints: [unclosed\n",
			want: []string{"config: yaml:", "line"},
		},
		{
			name: "unknown top-level key",
			yaml: "server: {listen: ':8090'}\nendpoint: {}\n",
			want: []string{"line 2: endpoint: unknown key"},
		},
		{
			name: "unknown nested key",
			yaml: "endpoints:\n  anthropic:\n    provider: anthropic\n    modls: {}\n",
			want: []string{"line 4: endpoints.anthropic.modls: unknown key"},
		},
		{
			name: "unknown key in a list item",
			yaml: "groups:\n  g:\n    endpoints:\n      - {endpoint: a, model: m, weight: 2}\n",
			want: []string{"line 4: groups.g.endpoints[0].weight: unknown key"},
		},
		{
			name: "bad duration",
			yaml: "limits:\n  max_attempts: 3\n  job_timeout: five minutes\n",
			want: []string{"line 3: limits.job_timeout: cannot unmarshal", "time.Duration"},
		},
		{
			name: "integer duration is rejected",
			yaml: "endpoints:\n  a: {timeout: 90}\n",
			want: []string{"line 2: endpoints.a.timeout: cannot unmarshal"},
		},
		{
			name: "wrong type",
			yaml: "limits:\n  pages: many\n",
			want: []string{"line 2: limits.pages: cannot unmarshal"},
		},
		{
			name: "all problems reported",
			yaml: "nope: 1\nlimits:\n  pages: many\n  job_timeout: soon\ndebug: {log_contents: true}\n",
			want: []string{
				"line 1: nope: unknown key",
				"line 3: limits.pages:",
				"line 4: limits.job_timeout:",
				"line 5: debug.log_contents: unknown key",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := Parse([]byte(tt.yaml), env(nil))
			if err == nil {
				t.Fatal("Parse() error = nil, want an error")
			}
			for _, w := range tt.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("Parse() error = %q, want it to contain %q", err, w)
				}
			}
		})
	}
}

// TestSecretsNeverPrinted renders the configuration every way a caller might and asserts
// the resolved key is absent from all of them.
func TestSecretsNeverPrinted(t *testing.T) {
	t.Parallel()
	cfg := loadExample(t)
	ep := cfg.Endpoints["anthropic"]
	if ep.APIKey() != testSecret {
		t.Fatal("precondition: key must be resolved")
	}

	var logs bytes.Buffer
	text := slog.New(slog.NewTextHandler(&logs, nil))
	text.Info("cfg", "config", cfg, "endpoint", ep, "endpoints", cfg.Endpoints)
	jsonLog := slog.New(slog.NewJSONHandler(&logs, nil))
	jsonLog.Info("cfg", "config", cfg, "endpoint", ep)

	yamlOut, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	jsonOut, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// A configuration with problems, so error and issue output is exercised too.
	broken := loadExample(t)
	broken.Limits.JobTimeout = time.Hour
	broken.Endpoints["anthropic"].Models = nil
	broken.Endpoints["gemini"].APIKeyEnv = "${GEMINI_API_KEY}"

	outputs := map[string]string{
		"issues":          fmt.Sprintf("%v", broken.Validate()),
		"issues error":    fmt.Sprint(broken.Validate().Err(true)),
		"String":          cfg.String(),
		"%v config":       fmt.Sprintf("%v", cfg),
		"%+v config":      fmt.Sprintf("%+v", cfg),
		"%#v config":      fmt.Sprintf("%#v", cfg),
		"%+v *config":     fmt.Sprintf("%+v", *cfg),
		"%v endpoint":     fmt.Sprintf("%v", ep),
		"%+v endpoint":    fmt.Sprintf("%+v", *ep),
		"%#v endpoint":    fmt.Sprintf("%#v", *ep),
		"%+v endpoints":   fmt.Sprintf("%+v", cfg.Endpoints),
		"Sprint endpoint": fmt.Sprint(ep),
		"yaml":            string(yamlOut),
		"json":            string(jsonOut),
		"slog":            logs.String(),
	}
	for name, out := range outputs {
		if strings.Contains(out, testSecret) {
			t.Errorf("%s output contains the secret:\n%s", name, out)
		}
	}
	if !strings.Contains(outputs["%+v endpoint"], "api_key=[redacted]") {
		t.Errorf("endpoint String() = %q, want it to show the key as redacted", outputs["%+v endpoint"])
	}
}
