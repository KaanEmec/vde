package config

import (
	"slices"
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
    base_url: "http://localhost:11434/v1"
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
			name: "negative max_output_tokens",
			edit: func(c *Config) {
				c.Endpoints["local"].Models["vision"] = Model{Accepts: []string{"image/png"}, MaxOutputTokens: -1}
			},
			wantKey:     "endpoints.local.models.vision.max_output_tokens",
			wantMessage: "must be positive",
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
			name: "no native PDF reader under native",
			edit: func(c *Config) {
				c.Endpoints["anthropic"].Models["claude"] = Model{Accepts: []string{"image/png", "image/jpeg", "text/plain"}}
				c.Documents.PDF.Mode = PDFModeNative
			},
			wantKey:     "roles.extractor.group",
			wantMessage: "reads application/pdf natively; PDFs work only when callers send page images",
			wantWarning: true,
		},
		{
			// Under auto the service renders pages for a vision group: nothing to warn.
			name: "no native PDF reader under auto",
			edit: func(c *Config) {
				c.Endpoints["anthropic"].Models["claude"] = Model{Accepts: []string{"image/png", "image/jpeg", "text/plain"}}
			},
		},
		{
			name: "MUST types not readable",
			edit: func(c *Config) {
				c.Endpoints["anthropic"].Models["claude"] = Model{Accepts: []string{"text/plain"}}
				c.Groups["extraction"].Endpoints = c.Groups["extraction"].Endpoints[:1]
				c.Endpoints["local"].Models["vision"] = Model{Accepts: []string{"text/plain"}}
			},
			wantKey:     "roles.extractor.group",
			wantMessage: "cannot read image/jpeg, image/png",
			wantWarning: true,
		},
		{
			name: "text-only group reads PDFs through the text layer under auto",
			edit: func(c *Config) {
				c.Endpoints["anthropic"].Models["claude"] = Model{Accepts: []string{"text/plain"}}
				c.Groups["extraction"].Endpoints = c.Groups["extraction"].Endpoints[:1]
			},
			wantKey:     "roles.extractor.group",
			wantMessage: "reads PDFs only through their text layer",
			wantWarning: true,
		},
		{
			name: "text-only group cannot read PDFs under pages",
			edit: func(c *Config) {
				c.Endpoints["anthropic"].Models["claude"] = Model{Accepts: []string{"text/plain"}}
				c.Groups["extraction"].Endpoints = c.Groups["extraction"].Endpoints[:1]
				c.Documents.PDF.Mode = PDFModePages
			},
			wantKey:     "roles.extractor.group",
			wantMessage: "cannot read application/pdf",
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
			edit: func(c *Config) { c.Limits.MaxAttempts = 1; c.Applications[0].MaxAttempts = 1 },
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
			name:        "failover total time not below the job timeout",
			edit:        func(c *Config) { c.Groups["review"].Failover.MaxTotalTime = c.Limits.JobTimeout },
			wantKey:     "groups.review.failover.max_total_time",
			wantMessage: "less than limits.job_timeout",
		},
		{
			name: "failover total time just below the job timeout",
			edit: func(c *Config) { c.Groups["review"].Failover.MaxTotalTime = c.Limits.JobTimeout - time.Second },
		},
		{
			name:        "judge reparse tries negative",
			edit:        func(c *Config) { c.Limits.JudgeReparseTries = -1 },
			wantKey:     "limits.judge_reparse_tries",
			wantMessage: "must not be negative",
		},
		{
			name:        "feedback message chars negative",
			edit:        func(c *Config) { c.Limits.FeedbackMessageChars = -1 },
			wantKey:     "limits.feedback_message_chars",
			wantMessage: "the default is used (refused in 2.0)",
			wantWarning: true,
		},
		{
			name:        "max queued jobs negative",
			edit:        func(c *Config) { c.Limits.MaxQueuedJobs = -1 },
			wantKey:     "limits.max_queued_jobs",
			wantMessage: "must not be negative",
		},
		{
			name: "max queued jobs set",
			edit: func(c *Config) { c.Limits.MaxQueuedJobs = 1000 },
		},
		{
			name:        "max submits in flight zero",
			edit:        func(c *Config) { c.Limits.MaxSubmitsInFlight = 0 },
			wantKey:     "limits.max_submits_in_flight",
			wantMessage: "at least 1",
		},
		{
			name: "max submits in flight one",
			edit: func(c *Config) { c.Limits.MaxSubmitsInFlight = 1 },
		},
		{
			name:        "pages over the blob name limit",
			edit:        func(c *Config) { c.Limits.Pages = 1000 },
			wantKey:     "limits.pages",
			wantMessage: "over 999",
		},
		{
			name: "pages at the blob name limit",
			edit: func(c *Config) { c.Limits.Pages = 999 },
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
			edit:        func(c *Config) { c.Endpoints["local"].Provider = "cohere" },
			wantKey:     "endpoints.local.provider",
			wantMessage: `"cohere" is not one of`,
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
			name:        "plain HTTP base_url on a non-loopback host",
			edit:        func(c *Config) { c.Endpoints["local"].BaseURL = "http://ollama:11434/v1" },
			wantKey:     "endpoints.local.base_url",
			wantMessage: "plain HTTP on a non-loopback host",
			wantWarning: true,
		},
		{
			name:        "plain HTTP base_url names it without credentials",
			edit:        func(c *Config) { c.Endpoints["local"].BaseURL = "http://u:url-password@ollama:11434/v1" },
			wantKey:     "endpoints.local.base_url",
			wantMessage: `"http://u:xxxxx@ollama:11434/v1" is plain HTTP`,
			wantWarning: true,
		},
		{
			name: "https base_url on another host",
			edit: func(c *Config) { c.Endpoints["local"].BaseURL = "https://llm.internal.example/v1" },
		},
		{
			name: "plain HTTP base_url on loopback",
			edit: func(c *Config) { c.Endpoints["local"].BaseURL = "http://127.0.0.1:11434/v1" },
		},
		{
			name:        "negative timeout",
			edit:        func(c *Config) { c.Endpoints["local"].Timeout = -time.Second },
			wantKey:     "endpoints.local.timeout",
			wantMessage: "must be positive",
		},

		// debug.log_content only warns that it has no effect (deprecated), on any listener.
		{
			name:        "log content on a public address",
			edit:        func(c *Config) { c.Debug.LogContent = true; c.Server.Listen = "10.0.0.5:8090" },
			wantKey:     "debug.log_content",
			wantMessage: "has no effect and is deprecated",
			wantWarning: true,
		},
		{
			name:        "log content on loopback",
			edit:        func(c *Config) { c.Debug.LogContent = true; c.Server.Listen = "127.0.0.1:8090" },
			wantKey:     "debug.log_content",
			wantMessage: "has no effect and is deprecated",
			wantWarning: true,
		},
		{
			name:        "log content on localhost",
			edit:        func(c *Config) { c.Debug.LogContent = true; c.Server.Listen = "localhost:8090" },
			wantKey:     "debug.log_content",
			wantMessage: "has no effect and is deprecated",
			wantWarning: true,
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
			name:        "unknown pdf mode",
			edit:        func(c *Config) { c.Documents.PDF.Mode = "ocr" },
			wantKey:     "documents.pdf.mode",
			wantMessage: `"ocr" is not one of auto, native, text, pages`,
		},
		{
			name:        "pdf dpi too low",
			edit:        func(c *Config) { c.Documents.PDF.DPI = 50 },
			wantKey:     "documents.pdf.dpi",
			wantMessage: "must be between 72 and 300",
		},
		{
			name:        "pdf max pages too high",
			edit:        func(c *Config) { c.Documents.PDF.MaxPages = 500 },
			wantKey:     "documents.pdf.max_pages",
			wantMessage: "must be between 1 and 200",
		},
		{
			name:        "pdf negative min text",
			edit:        func(c *Config) { c.Documents.PDF.MinTextCharsPerPage = -1 },
			wantKey:     "documents.pdf.min_text_chars_per_page",
			wantMessage: "must not be negative",
		},
		{
			name:        "pdf workers zero",
			edit:        func(c *Config) { c.Documents.PDF.Workers = 0 },
			wantKey:     "documents.pdf.workers",
			wantMessage: "must be between 1 and 16",
		},
		{
			name: "pdf min text zero",
			edit: func(c *Config) { c.Documents.PDF.MinTextCharsPerPage = 0 },
		},
		{
			name:        "image max dimension too small",
			edit:        func(c *Config) { c.Documents.Images.MaxDimension = 10 },
			wantKey:     "documents.images.max_dimension",
			wantMessage: "must be between 64 and 16384",
		},
		{
			name:        "image max dimension too large",
			edit:        func(c *Config) { c.Documents.Images.MaxDimension = 100000 },
			wantKey:     "documents.images.max_dimension",
			wantMessage: "must be between 64 and 16384",
		},
		{
			name:        "image target not supported",
			edit:        func(c *Config) { c.Documents.Images.ConvertTo = "image/webp" },
			wantKey:     "documents.images.convert_to",
			wantMessage: `"image/webp" is not one of image/jpeg, image/png`,
		},
		{
			name:        "image quality out of range",
			edit:        func(c *Config) { c.Documents.Images.Quality = 101 },
			wantKey:     "documents.images.quality",
			wantMessage: "must be between 1 and 100",
		},
		{
			name: "image conversion to png",
			edit: func(c *Config) {
				c.Documents.Images = Images{Enabled: false, MaxDimension: 1024, ConvertTo: "image/png", Quality: 90}
			},
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
			name:        "bad application pdf mode",
			edit:        func(c *Config) { c.Applications[0].Documents.PDF.Mode = "always" },
			wantKey:     "applications[0].documents.pdf.mode",
			wantMessage: `"always" is not one of auto, native, text, pages`,
		},
		{
			name: "application pdf mode",
			edit: func(c *Config) { c.Applications[0].Documents.PDF.Mode = PDFModeNative },
		},
		{
			name:        "quota window in months",
			edit:        func(c *Config) { c.Applications[0].Quota.Window = "P1M" },
			wantKey:     "applications[0].quota.window",
			wantMessage: "not an ISO 8601 duration",
		},
		{
			name:        "quota window as a Go duration",
			edit:        func(c *Config) { c.Applications[0].Quota.Window = "24h" },
			wantKey:     "applications[0].quota.window",
			wantMessage: "not an ISO 8601 duration",
		},
		{
			name: "weekly quota window",
			edit: func(c *Config) { c.Applications[0].Quota.Window = "P7D" },
		},
		{
			name:        "application attempts above the global limit",
			edit:        func(c *Config) { c.Applications[0].MaxAttempts = 5 },
			wantKey:     "applications[0].max_attempts",
			wantMessage: "above limits.max_attempts 3",
			wantWarning: true,
		},

		// Plain HTTP and token lifetime.
		{
			name:        "insecure http warns",
			edit:        func(c *Config) { c.Server.InsecureHTTP = true },
			wantKey:     "server.insecure_http",
			wantMessage: "plain HTTP is accepted",
			wantWarning: true,
		},
		{
			name: "insecure http with tls",
			edit: func(c *Config) {
				c.Server.InsecureHTTP = true
				c.Server.TLS = TLS{CertFile: "/etc/vde/cert.pem", KeyFile: "/etc/vde/key.pem"}
			},
			wantKey:     "server.insecure_http",
			wantMessage: "together with server.tls",
		},
		{
			name:        "zero token lifetime",
			edit:        func(c *Config) { c.Auth.MaxTokenTTL = 0 },
			wantKey:     "auth.max_token_ttl",
			wantMessage: "must be positive",
		},

		// Escalation: defined groups, from_attempt from 2, increasing, within max_attempts.
		// A valid one only warns (TestValidate_deprecated).
		{
			name:        "escalation group undefined",
			edit:        func(c *Config) { c.Roles.Judge.Escalation = []Escalation{{FromAttempt: 3, Group: "strong"}} },
			wantKey:     "roles.judge.escalation[0].group",
			wantMessage: `group "strong" is not defined`,
		},
		{
			name:        "escalation group missing",
			edit:        func(c *Config) { c.Roles.Extractor.Escalation = []Escalation{{FromAttempt: 3}} },
			wantKey:     "roles.extractor.escalation[0].group",
			wantMessage: "is required",
		},
		{
			name:        "escalation from attempt 1",
			edit:        func(c *Config) { c.Roles.Extractor.Escalation = []Escalation{{FromAttempt: 1, Group: "review"}} },
			wantKey:     "roles.extractor.escalation[0].from_attempt",
			wantMessage: "must be at least 2",
		},
		{
			name: "escalation not increasing",
			edit: func(c *Config) {
				c.Roles.Extractor.Escalation = []Escalation{{FromAttempt: 3, Group: "review"}, {FromAttempt: 3, Group: "extraction"}}
			},
			wantKey:     "roles.extractor.escalation[1].from_attempt",
			wantMessage: "must be greater than the previous step's 3",
		},
		{
			name:        "escalation beyond max_attempts",
			edit:        func(c *Config) { c.Roles.Extractor.Escalation = []Escalation{{FromAttempt: 4, Group: "review"}} },
			wantKey:     "roles.extractor.escalation[0].from_attempt",
			wantMessage: "above limits.max_attempts 3",
		},
		{
			name: "escalation group readability is checked",
			edit: func(c *Config) {
				c.Groups["text"] = &Group{Endpoints: []GroupEntry{{Endpoint: "local", Model: "vision"}}, Failover: c.Groups["review"].Failover}
				c.Endpoints["local"].Models["vision"] = Model{Accepts: []string{"text/plain"}}
				c.Groups["extraction"].Endpoints = c.Groups["extraction"].Endpoints[:1]
				c.Roles.Extractor.Escalation = []Escalation{{FromAttempt: 2, Group: "text"}}
			},
			wantKey:     "roles.extractor.escalation[0].group",
			wantMessage: `group "text" cannot read`,
			wantWarning: true,
		},

		// Second judge; a valid one only warns (TestValidate_deprecated).
		{
			name:        "second judge group undefined",
			edit:        func(c *Config) { c.Roles.SecondJudge.Group = "other" },
			wantKey:     "roles.second_judge.group",
			wantMessage: `group "other" is not defined`,
		},

		// Classifier judge (v0.3 02-02).
		{
			// A classifier reads text only, so only text documents are accepted.
			name:        "classifier judge",
			edit:        func(c *Config) { addClassifier(c); c.Roles.Judge.Group = "classify" },
			wantKey:     "roles.judge.group",
			wantMessage: `group "classify" cannot read image/jpeg, image/png`,
			wantWarning: true,
		},
		{
			name: "classifier judge with LLM feedback",
			edit: func(c *Config) {
				addClassifier(c)
				c.Roles.Judge.Group = "classify"
				c.Roles.Judge.FeedbackFallback, c.Roles.Judge.FeedbackGroup = FeedbackLLM, "review"
			},
			wantKey:     "roles.judge.group",
			wantMessage: "cannot read",
			wantWarning: true,
		},
		{
			name:        "classifier extractor",
			edit:        func(c *Config) { addClassifier(c); c.Roles.Extractor.Group = "classify" },
			wantKey:     "roles.extractor.group",
			wantMessage: "can serve only the judge",
		},
		{
			name: "classifier extractor escalation",
			edit: func(c *Config) {
				addClassifier(c)
				c.Roles.Extractor.Escalation = []Escalation{{FromAttempt: 2, Group: "classify"}}
			},
			wantKey:     "roles.extractor.escalation[0].group",
			wantMessage: "can serve only the judge",
		},
		{
			name: "classifier without questions",
			edit: func(c *Config) {
				addClassifier(c)
				c.Endpoints["jev"].Models["jev-1"] = Model{Accepts: []string{"text/plain"}}
			},
			wantKey:     "endpoints.jev.models.jev-1.questions",
			wantMessage: "at least one question",
		},
		{
			name: "classifier question threshold",
			edit: func(c *Config) {
				addClassifier(c)
				m := c.Endpoints["jev"].Models["jev-1"]
				m.Questions = append(m.Questions, Question{ID: "complete", Text: "Complete?", Threshold: 1.5})
				c.Endpoints["jev"].Models["jev-1"] = m
			},
			wantKey:     "endpoints.jev.models.jev-1.questions[1].threshold",
			wantMessage: "must be above 0 and at most 1",
		},
		{
			name: "classifier duplicate question",
			edit: func(c *Config) {
				addClassifier(c)
				m := c.Endpoints["jev"].Models["jev-1"]
				m.Questions = append(m.Questions, m.Questions[0])
				c.Endpoints["jev"].Models["jev-1"] = m
			},
			wantKey:     "endpoints.jev.models.jev-1.questions[1].id",
			wantMessage: "duplicate question id",
		},
		{
			name: "classifier reads text only",
			edit: func(c *Config) {
				addClassifier(c)
				m := c.Endpoints["jev"].Models["jev-1"]
				m.Accepts = []string{"text/plain", "image/png"}
				c.Endpoints["jev"].Models["jev-1"] = m
			},
			wantKey:     "endpoints.jev.models.jev-1.accepts",
			wantMessage: "a classifier reads text only",
		},
		{
			name: "questions on an LLM",
			edit: func(c *Config) {
				c.Endpoints["local"].Models["vision"] = Model{Accepts: []string{"image/png", "image/jpeg", "text/plain"},
					Questions: []Question{{ID: "a", Text: "A?", Threshold: 0.5}}}
			},
			wantKey:     "endpoints.local.models.vision.questions",
			wantMessage: "apply only to classifier endpoints",
		},
		{
			name:        "unknown feedback fallback",
			edit:        func(c *Config) { c.Roles.Judge.FeedbackFallback = "none" },
			wantKey:     "roles.judge.feedback_fallback",
			wantMessage: `"none" is not one of whole_result, llm`,
		},
		{
			name: "llm feedback without group",
			edit: func(c *Config) {
				addClassifier(c)
				c.Roles.Judge.Group = "classify"
				c.Roles.Judge.FeedbackFallback = FeedbackLLM
			},
			wantKey:     "roles.judge.feedback_group",
			wantMessage: "is required with feedback_fallback: llm",
		},
		{
			name:        "feedback group without llm",
			edit:        func(c *Config) { c.Roles.Judge.FeedbackGroup = "review" },
			wantKey:     "roles.judge.feedback_group",
			wantMessage: "only with feedback_fallback: llm",
		},
		{
			name: "classifier feedback group",
			edit: func(c *Config) {
				addClassifier(c)
				c.Roles.Judge.Group = "classify"
				c.Roles.Judge.FeedbackFallback, c.Roles.Judge.FeedbackGroup = FeedbackLLM, "classify"
			},
			wantKey:     "roles.judge.feedback_group",
			wantMessage: "feedback needs an LLM group",
		},
		{
			name:        "feedback fallback without a classifier",
			edit:        func(c *Config) { c.Roles.Judge.FeedbackFallback = FeedbackWholeResult },
			wantKey:     "roles.judge.feedback_fallback",
			wantMessage: "has no effect",
			wantWarning: true,
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

		// Judge policy (v0.4, ADR 0009).
		{
			name: "judge policy never and auto",
			edit: func(c *Config) {
				c.Roles.Judge.Policy = JudgeNever
				rate := 0.5
				c.Applications[0].Judge = AppJudge{Policy: JudgeAuto, AllowOverride: true, SampleRate: &rate}
			},
		},
		{
			name:        "unknown role judge policy",
			edit:        func(c *Config) { c.Roles.Judge.Policy = "sometimes" },
			wantKey:     "roles.judge.policy",
			wantMessage: `"sometimes" is not one of always, never, auto`,
		},
		{
			name:        "policy on the extractor",
			edit:        func(c *Config) { c.Roles.Extractor.Policy = JudgeAlways },
			wantKey:     "roles.extractor.policy",
			wantMessage: "applies only to roles.judge",
		},
		{
			name:        "unknown application judge policy",
			edit:        func(c *Config) { c.Applications[0].Judge.Policy = "Auto" },
			wantKey:     "applications[0].judge.policy",
			wantMessage: `"Auto" is not one of always, never, auto`,
		},
		{
			name:        "sample rate above 1",
			edit:        func(c *Config) { r := 1.5; c.Applications[0].Judge.SampleRate = &r },
			wantKey:     "applications[0].judge.sample_rate",
			wantMessage: "1.5 must be from 0 to 1",
		},
		{
			name:        "negative sample rate",
			edit:        func(c *Config) { r := -0.1; c.Applications[0].Judge.SampleRate = &r },
			wantKey:     "applications[0].judge.sample_rate",
			wantMessage: "must be from 0 to 1",
		},
		{
			name: "sample rate bounds",
			edit: func(c *Config) {
				zero, one := 0.0, 1.0
				c.Applications[0].Judge.SampleRate = &zero
				c.Applications = append(c.Applications, Application{
					ID: "other", MaxAttempts: 3, Quota: Quota{Limit: 1, Window: "P1D"},
					Rate:      Rate{RequestsPerMinute: 1, SubmitsPerMinute: 1, MaxInProgress: 1},
					Retention: Retention{Default: "24h"}, Judge: AppJudge{SampleRate: &one},
				})
			},
		},
		{
			name: "never with a second judge",
			edit: func(c *Config) {
				c.Roles.Judge.Policy = JudgeNever
				c.Roles.SecondJudge.Group = "review"
			},
			wantKey:     "roles.second_judge",
			wantMessage: "is never asked",
			wantWarning: true,
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

// TestValidate_autoWithClassifierJudge: auto is allowed when the judge group is a
// classifier (a sampled or ungroundable job then goes to the classifier).
func TestValidate_autoWithClassifierJudge(t *testing.T) {
	t.Parallel()
	cfg := validConfig(t)
	addClassifier(cfg)
	cfg.Roles.Judge.Group = "classify"
	cfg.Roles.Judge.Policy = JudgeAuto
	for _, i := range cfg.Validate() {
		if strings.Contains(i.Key, "policy") || !i.Warning {
			t.Errorf("issue %v, want only the classifier's media type warning", i)
		}
	}
}

// addClassifier adds the classifier endpoint jev and the group classify serving it.
// TestValidate_deprecated: escalation, the second judge, the classifier provider and the
// feedback fallback keep working in 1.x and warn once each, naming the 2.0 removal.
func TestValidate_deprecated(t *testing.T) {
	t.Parallel()
	const msg = "is deprecated (removal in 2.0); it still works"
	for _, tt := range []struct {
		name string
		edit func(c *Config)
		want []string // every issue, all warnings
	}{
		{
			name: "escalation steps",
			edit: func(c *Config) {
				c.Limits.MaxAttempts = 4
				c.Roles.Extractor.Escalation = []Escalation{{FromAttempt: 2, Group: "review"}, {FromAttempt: 4, Group: "extraction"}}
				c.Roles.Judge.Escalation = []Escalation{{FromAttempt: 3, Group: "extraction"}}
			},
			want: []string{"roles.extractor.escalation: " + msg, "roles.judge.escalation: " + msg},
		},
		{
			name: "second judge",
			edit: func(c *Config) { c.Roles.SecondJudge.Group = "extraction" },
			want: []string{"roles.second_judge: " + msg},
		},
		{
			name: "classifier endpoint",
			edit: addClassifier,
			want: []string{"endpoints.jev.provider: classifier " + msg},
		},
		{
			name: "classifier second judge with LLM feedback",
			edit: func(c *Config) {
				addClassifier(c)
				c.Groups["classify"].Endpoints = append(c.Groups["classify"].Endpoints, c.Groups["review"].Endpoints...)
				c.Roles.SecondJudge.Group = "classify"
				c.Roles.Judge.FeedbackFallback, c.Roles.Judge.FeedbackGroup = FeedbackLLM, "review"
			},
			want: []string{
				"endpoints.jev.provider: classifier " + msg,
				"roles.second_judge: " + msg,
				"roles.judge.feedback_fallback: " + msg + " (with roles.judge.feedback_group)",
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := validConfig(t)
			tt.edit(cfg)
			var got []string
			for _, i := range cfg.Validate() {
				if !i.Warning {
					t.Errorf("error %v, want warnings only", i)
				}
				got = append(got, i.Key+": "+i.Message)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("Validate() = %q, want %q", got, tt.want)
			}
		})
	}
}

func addClassifier(c *Config) {
	c.Endpoints["jev"] = &Endpoint{
		Provider: ProviderClassifier,
		BaseURL:  "https://classifier.example",
		Timeout:  time.Minute,
		Models: map[string]Model{"jev-1": {
			Accepts:   []string{"text/plain", "text/html"},
			Questions: []Question{{ID: "accuracy", Text: "Is every value what the document says?", Threshold: 0.8}},
		}},
	}
	c.Groups["classify"] = &Group{Endpoints: []GroupEntry{{Endpoint: "jev", Model: "jev-1"}}, Failover: c.Groups["review"].Failover}
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

// TestLimitsMaxQueuedJobs: limits.max_queued_jobs is read from YAML and defaults to 0,
// no ceiling.
func TestLimitsMaxQueuedJobs(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		yaml string
		want int
	}{
		{name: "default", yaml: validYAML, want: 0},
		{name: "set", yaml: validYAML + "limits: {max_queued_jobs: 500}\n", want: 500},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg, err := Parse([]byte(tt.yaml), env(map[string]string{"ANTHROPIC_API_KEY": testSecret}))
			if err != nil {
				t.Fatal(err)
			}
			if got := cfg.Limits.MaxQueuedJobs; got != tt.want {
				t.Errorf("limits.max_queued_jobs = %d, want %d", got, tt.want)
			}
			if issues := cfg.Validate(); len(issues) != 0 {
				t.Errorf("Validate() = %v, want no issues", issues)
			}
		})
	}
}
