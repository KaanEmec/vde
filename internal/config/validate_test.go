package config

import (
	"strings"
	"testing"
	"time"
)

// validYAML passes Validate with no issues at all; each test case breaks or varies it.
const validYAML = `
server: {listen: ":8090"}
endpoints:
  anthropic:
    provider: anthropic
    api_key_env: ANTHROPIC_API_KEY
    models:
      claude: {accepts: [application/pdf, image/png, image/jpeg, text/plain]}
  local:
    provider: openai_compatible
    base_url: "http://ollama:11434/v1"
    models:
      vision: {accepts: [image/png, image/jpeg, text/plain]}
groups:
  extraction:
    endpoints:
      - {endpoint: anthropic, model: claude}
      - {endpoint: local, model: vision}
  review:
    endpoints:
      - {endpoint: anthropic, model: claude}
roles:
  extractor: {group: extraction}
  judge: {group: review}
applications:
  - {id: travelinbox}
`

func validConfig(t *testing.T) *Config {
	t.Helper()
	cfg, err := Parse([]byte(validYAML), env(map[string]string{"ANTHROPIC_API_KEY": testSecret}))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestValidate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		edit        func(c *Config)
		wantKey     string // "" means no issue at all
		wantMessage string
		wantWarning bool
	}{
		{name: "valid configuration", edit: func(c *Config) {}},

		// Every group entry references a defined endpoint and a model declared on it.
		{
			name:        "group references undefined endpoint",
			edit:        func(c *Config) { c.Groups["review"].Endpoints[0].Endpoint = "nope" },
			wantKey:     "groups.review.endpoints[0].endpoint",
			wantMessage: `endpoint "nope" is not defined`,
		},
		{
			name:        "group references undeclared model",
			edit:        func(c *Config) { c.Groups["extraction"].Endpoints[1].Model = "claude" },
			wantKey:     "groups.extraction.endpoints[1].model",
			wantMessage: `model "claude" is not declared on endpoint "local"`,
		},
		{
			name:        "group without endpoints",
			edit:        func(c *Config) { c.Groups["review"].Endpoints = nil },
			wantKey:     "groups.review.endpoints",
			wantMessage: "at least one endpoint",
		},

		// Every endpoint-model pair has a non-empty accepts list of known media types.
		{
			name:        "empty accepts",
			edit:        func(c *Config) { c.Endpoints["local"].Models["vision"] = Model{} },
			wantKey:     "endpoints.local.models.vision.accepts",
			wantMessage: "must list the media types",
		},
		{
			name: "unknown media type",
			edit: func(c *Config) {
				c.Endpoints["local"].Models["vision"] = Model{Accepts: []string{"image/png", "image/gif"}}
			},
			wantKey:     "endpoints.local.models.vision.accepts",
			wantMessage: `unknown media type "image/gif"`,
		},
		{
			name: "endpoint without models",
			edit: func(c *Config) {
				c.Endpoints["local"].Models = nil
				c.Groups["extraction"].Endpoints = c.Groups["extraction"].Endpoints[:1]
			},
			wantKey:     "endpoints.local.models",
			wantMessage: "at least one model",
		},
		{
			name: "SHOULD media types are known",
			edit: func(c *Config) {
				c.Endpoints["local"].Models["vision"] = Model{Accepts: []string{"image/webp", "image/heic", "text/markdown", "text/html"}}
			},
		},

		// Both roles reference a defined group; a group may serve both roles.
		{
			name:        "extractor group undefined",
			edit:        func(c *Config) { c.Roles.Extractor.Group = "missing" },
			wantKey:     "roles.extractor.group",
			wantMessage: `group "missing" is not defined`,
		},
		{
			name:        "judge group missing",
			edit:        func(c *Config) { c.Roles.Judge.Group = "" },
			wantKey:     "roles.judge.group",
			wantMessage: "is required",
		},
		{
			name: "one group serves both roles",
			edit: func(c *Config) { c.Roles.Judge.Group = "extraction" },
		},

		// The extractor group reads every MUST media type, directly or via page images.
		{
			name: "no native PDF reader",
			edit: func(c *Config) {
				c.Endpoints["anthropic"].Models["claude"] = Model{Accepts: []string{"image/png", "image/jpeg", "text/plain"}}
			},
			wantKey:     "roles.extractor.group",
			wantMessage: "reads application/pdf natively",
			wantWarning: true,
		},
		{
			name: "MUST types not readable",
			edit: func(c *Config) {
				c.Endpoints["anthropic"].Models["claude"] = Model{Accepts: []string{"text/plain"}}
				c.Groups["extraction"].Endpoints = c.Groups["extraction"].Endpoints[:1]
				c.Endpoints["local"].Models["vision"] = Model{Accepts: []string{"text/plain"}}
			},
			wantKey:     "roles.extractor.group",
			wantMessage: "cannot read application/pdf, image/jpeg, image/png",
			wantWarning: true,
		},
		{
			name: "PDF through a fallback pair counts",
			edit: func(c *Config) {
				c.Groups["extraction"].Endpoints[0], c.Groups["extraction"].Endpoints[1] =
					c.Groups["extraction"].Endpoints[1], c.Groups["extraction"].Endpoints[0]
			},
		},

		// limits.job_timeout ≤ 8m, max_attempts ≥ 1, failover bounds positive.
		{
			name:        "job timeout over 8m",
			edit:        func(c *Config) { c.Limits.JobTimeout = 9 * time.Minute },
			wantKey:     "limits.job_timeout",
			wantMessage: "at most 8m0s",
		},
		{
			name: "job timeout of exactly 8m",
			edit: func(c *Config) { c.Limits.JobTimeout = 8 * time.Minute },
		},
		{
			name:        "job timeout zero",
			edit:        func(c *Config) { c.Limits.JobTimeout = 0 },
			wantKey:     "limits.job_timeout",
			wantMessage: "must be positive",
		},
		{
			name:        "max attempts zero",
			edit:        func(c *Config) { c.Limits.MaxAttempts = 0 },
			wantKey:     "limits.max_attempts",
			wantMessage: "at least 1",
		},
		{
			name: "max attempts one",
			edit: func(c *Config) { c.Limits.MaxAttempts = 1 },
		},
		{
			name:        "failover tries negative",
			edit:        func(c *Config) { c.Groups["review"].Failover.MaxTries = -1 },
			wantKey:     "groups.review.failover.max_tries",
			wantMessage: "at least 1",
		},
		{
			name:        "failover total time negative",
			edit:        func(c *Config) { c.Groups["review"].Failover.MaxTotalTime = -time.Second },
			wantKey:     "groups.review.failover.max_total_time",
			wantMessage: "must be positive",
		},
		{
			name:        "judge reparse tries negative",
			edit:        func(c *Config) { c.Limits.JudgeReparseTries = -1 },
			wantKey:     "limits.judge_reparse_tries",
			wantMessage: "must not be negative",
		},
		{
			name:        "document limit below contract",
			edit:        func(c *Config) { c.Limits.DocumentBytes = 1 << 20 },
			wantKey:     "limits.document_bytes",
			wantMessage: "below the contract minimum",
			wantWarning: true,
		},
		{
			name:        "page limit not positive",
			edit:        func(c *Config) { c.Limits.Pages = 0 },
			wantKey:     "limits.pages",
			wantMessage: "must be positive",
		},
		{
			name: "limits raised above contract",
			edit: func(c *Config) { c.Limits.DocumentBytes, c.Limits.PageBytes, c.Limits.Pages = 64<<20, 20<<20, 50 },
		},

		// api_key_env is set when the provider needs a key; empty only for openai_compatible.
		{
			name:        "key variable not set",
			edit:        func(c *Config) { c.Endpoints["anthropic"].apiKey = "" },
			wantKey:     "endpoints.anthropic.api_key_env",
			wantMessage: "environment variable ANTHROPIC_API_KEY is not set",
		},
		{
			name:        "anthropic without api_key_env",
			edit:        func(c *Config) { c.Endpoints["anthropic"].APIKeyEnv = "" },
			wantKey:     "endpoints.anthropic.api_key_env",
			wantMessage: `is required for provider "anthropic"`,
		},
		{
			name:        "openai_compatible names a variable that is not set",
			edit:        func(c *Config) { c.Endpoints["local"].APIKeyEnv = "LOCAL_KEY" },
			wantKey:     "endpoints.local.api_key_env",
			wantMessage: "environment variable LOCAL_KEY is not set",
		},
		{
			name:        "interpolation syntax is not a variable name",
			edit:        func(c *Config) { c.Endpoints["anthropic"].APIKeyEnv = "${ANTHROPIC_API_KEY}" },
			wantKey:     "endpoints.anthropic.api_key_env",
			wantMessage: "is not an environment variable name",
		},
		{
			name: "openai_compatible without a key",
			edit: func(c *Config) { c.Endpoints["local"].APIKeyEnv = "" },
		},

		// Endpoint shape.
		{
			name:        "unknown provider",
			edit:        func(c *Config) { c.Endpoints["local"].Provider = "bedrock" },
			wantKey:     "endpoints.local.provider",
			wantMessage: `"bedrock" is not one of`,
		},
		{
			name:        "preset on the wrong provider",
			edit:        func(c *Config) { c.Endpoints["anthropic"].Preset = "openrouter" },
			wantKey:     "endpoints.anthropic.preset",
			wantMessage: "only to openai_compatible",
		},
		{
			name:        "unknown preset",
			edit:        func(c *Config) { c.Endpoints["local"].Preset = "azure" },
			wantKey:     "endpoints.local.preset",
			wantMessage: `unknown preset "azure"`,
		},
		{
			name:        "openai_compatible without base_url",
			edit:        func(c *Config) { c.Endpoints["local"].BaseURL = "" },
			wantKey:     "endpoints.local.base_url",
			wantMessage: "is required",
		},
		{
			name:        "relative base_url",
			edit:        func(c *Config) { c.Endpoints["local"].BaseURL = "ollama:11434/v1" },
			wantKey:     "endpoints.local.base_url",
			wantMessage: "not an absolute http or https URL",
		},
		{
			name:        "negative timeout",
			edit:        func(c *Config) { c.Endpoints["local"].Timeout = -time.Second },
			wantKey:     "endpoints.local.timeout",
			wantMessage: "must be positive",
		},

		// debug.log_content with a non-loopback listener is an error.
		{
			name:        "log content on all interfaces",
			edit:        func(c *Config) { c.Debug.LogContent = true },
			wantKey:     "debug.log_content",
			wantMessage: "not a loopback address",
		},
		{
			name:        "log content on a public address",
			edit:        func(c *Config) { c.Debug.LogContent = true; c.Server.Listen = "10.0.0.5:8090" },
			wantKey:     "debug.log_content",
			wantMessage: "not a loopback address",
		},
		{
			name: "log content on loopback",
			edit: func(c *Config) { c.Debug.LogContent = true; c.Server.Listen = "127.0.0.1:8090" },
		},
		{
			name: "log content on localhost",
			edit: func(c *Config) { c.Debug.LogContent = true; c.Server.Listen = "localhost:8090" },
		},

		// Server, documents and logging values.
		{
			name:        "listen without port",
			edit:        func(c *Config) { c.Server.Listen = "8090" },
			wantKey:     "server.listen",
			wantMessage: "is not host:port",
		},
		{
			name:        "tls key without certificate",
			edit:        func(c *Config) { c.Server.TLS.KeyFile = "/etc/vde/key.pem" },
			wantKey:     "server.tls",
			wantMessage: "set together",
		},
		{
			name: "tls with both files",
			edit: func(c *Config) { c.Server.TLS = TLS{CertFile: "/etc/vde/cert.pem", KeyFile: "/etc/vde/key.pem"} },
		},
		{
			name:        "unknown html policy",
			edit:        func(c *Config) { c.Documents.HTML = "markdown" },
			wantKey:     "documents.html",
			wantMessage: `"markdown" is not one of raw_html, visible_text`,
		},
		{
			name: "visible text policy",
			edit: func(c *Config) { c.Documents.HTML = "visible_text" },
		},
		{
			name:        "unknown log level",
			edit:        func(c *Config) { c.Logging.Level = "trace" },
			wantKey:     "logging.level",
			wantMessage: `"trace" is not one of`,
		},
		{
			name:        "unknown log format",
			edit:        func(c *Config) { c.Logging.Format = "xml" },
			wantKey:     "logging.format",
			wantMessage: `"xml" is not one of json, text`,
		},

		// Application settings: unique ids, positive quotas.
		{
			name:        "duplicate application id",
			edit:        func(c *Config) { c.Applications = append(c.Applications, c.Applications[0]) },
			wantKey:     "applications[1].id",
			wantMessage: `duplicate application id "travelinbox"`,
		},
		{
			name:        "application without id",
			edit:        func(c *Config) { c.Applications[0].ID = "" },
			wantKey:     "applications[0].id",
			wantMessage: "is required",
		},
		{
			name:        "negative quota",
			edit:        func(c *Config) { c.Applications[0].Quota.Limit = -5 },
			wantKey:     "applications[0].quota.limit",
			wantMessage: "must be positive",
		},
		{
			name:        "zero rate limit",
			edit:        func(c *Config) { c.Applications[0].Rate.SubmitsPerMinute = 0 },
			wantKey:     "applications[0].rate.submits_per_minute",
			wantMessage: "must be positive",
		},
		{
			name:        "bad retention",
			edit:        func(c *Config) { c.Applications[0].Retention.Default = "forever" },
			wantKey:     "applications[0].retention.default",
			wantMessage: "neither none nor a positive duration",
		},
		{
			name:        "bad application html policy",
			edit:        func(c *Config) { c.Applications[0].Documents.HTML = "pdf" },
			wantKey:     "applications[0].documents.html",
			wantMessage: `"pdf" is not one of`,
		},
		{
			name: "two applications with retention none",
			edit: func(c *Config) {
				c.Applications = append(c.Applications, Application{
					ID:          "other",
					MaxAttempts: 1,
					Quota:       Quota{Limit: 10, Window: "PT1H"},
					Rate:        Rate{RequestsPerMinute: 1, SubmitsPerMinute: 1, MaxInProgress: 1},
					Retention:   Retention{Default: "none", ForceNone: true},
				})
			},
		},
		{
			name: "no applications",
			edit: func(c *Config) { c.Applications = nil },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := validConfig(t)
			tt.edit(cfg)
			issues := cfg.Validate()
			if tt.wantKey == "" {
				if len(issues) != 0 {
					t.Fatalf("Validate() = %v, want no issues", issues)
				}
				return
			}
			for _, i := range issues {
				if i.Key == tt.wantKey && strings.Contains(i.Message, tt.wantMessage) {
					if i.Warning != tt.wantWarning {
						t.Errorf("issue %v: Warning = %t, want %t", i, i.Warning, tt.wantWarning)
					}
					return
				}
			}
			t.Errorf("Validate() = %v, want an issue at %s containing %q", issues, tt.wantKey, tt.wantMessage)
		})
	}
}

func TestValidate_reportsAllErrors(t *testing.T) {
	t.Parallel()
	cfg := validConfig(t)
	cfg.Limits.JobTimeout = time.Hour
	cfg.Roles.Judge.Group = "missing"
	cfg.Groups["extraction"].Endpoints[1].Model = "nope"
	cfg.Endpoints["anthropic"].apiKey = ""

	issues := cfg.Validate()
	want := []string{
		"limits.job_timeout",
		"roles.judge.group",
		"groups.extraction.endpoints[1].model",
		"endpoints.anthropic.api_key_env",
	}
	err := issues.Err(false)
	if err == nil {
		t.Fatal("Err(false) = nil, want errors")
	}
	for _, key := range want {
		if !strings.Contains(err.Error(), key+": ") {
			t.Errorf("Err() = %q, want it to report %s", err, key)
		}
	}
}

func TestIssues_Err(t *testing.T) {
	t.Parallel()
	warning := Issues{{Key: "limits.pages", Message: "low", Warning: true}}
	if err := warning.Err(false); err != nil {
		t.Errorf("warning without strict: Err() = %v, want nil", err)
	}
	if err := warning.Err(true); err == nil || err.Error() != "limits.pages: low" {
		t.Errorf("warning with strict: Err() = %v, want limits.pages: low", err)
	}
	if err := Issues(nil).Err(true); err != nil {
		t.Errorf("no issues: Err() = %v, want nil", err)
	}
	if got := warning[0].String(); got != "warning: limits.pages: low" {
		t.Errorf("String() = %q", got)
	}
}

func TestCapabilities(t *testing.T) {
	t.Parallel()
	cfg := validConfig(t)
	cfg.Endpoints["anthropic"].Models["claude"] = Model{Accepts: []string{"image/png", "text/html"}}

	got := map[string]Capability{}
	for _, c := range cfg.Capabilities("extraction") {
		got[c.MediaType] = c
	}
	tests := []struct {
		mediaType string
		wantVia   string
		wantPairs string
	}{
		{"application/pdf", ViaPages, "anthropic/claude local/vision"},
		{"image/png", ViaNative, "anthropic/claude local/vision"},
		{"image/jpeg", ViaNative, "local/vision"},
		{"image/webp", "", ""},
		{"text/plain", ViaText, "local/vision"},
		{"text/html", ViaText, "anthropic/claude local/vision"},
		{"text/markdown", ViaText, "local/vision"},
	}
	for _, tt := range tests {
		c := got[tt.mediaType]
		if c.Via != tt.wantVia || strings.Join(c.Pairs, " ") != tt.wantPairs {
			t.Errorf("%s: Via %q pairs %v, want %q %q", tt.mediaType, c.Via, c.Pairs, tt.wantVia, tt.wantPairs)
		}
	}
	if !got["application/pdf"].Required || got["image/webp"].Required {
		t.Errorf("Required flags wrong: pdf %t, webp %t", got["application/pdf"].Required, got["image/webp"].Required)
	}
	if cfg.Capabilities("missing") != nil {
		t.Error("Capabilities(undefined group) != nil")
	}
}

func TestValidate_example(t *testing.T) {
	t.Parallel()
	if issues := loadExample(t).Validate(); len(issues) != 0 {
		t.Errorf("example configuration: Validate() = %v, want no issues", issues)
	}
}
