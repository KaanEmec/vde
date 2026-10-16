package extract

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/kaanemec/vde/internal/config"
)

const versionConfig = `
server: {listen: ":8090", data_dir: /var/lib/vde}
endpoints:
  main:
    provider: anthropic
    api_key_env: KEY_MAIN
    models:
      model-a: {accepts: [text/plain, application/pdf]}
      model-b: {accepts: [text/plain]}
  spare:
    provider: openai_compatible
    base_url: "https://spare.example/v1"
    models:
      model-c: {accepts: [image/png, text/plain]}
groups:
  extraction:
    endpoints: [{endpoint: main, model: model-a}, {endpoint: spare, model: model-c}]
    failover: {max_tries: 2, max_total_time: 1m}
  review:
    endpoints: [{endpoint: main, model: model-a}]
  unused:
    endpoints: [{endpoint: main, model: model-b}]
roles:
  extractor: {group: extraction}
  judge: {group: review}
limits: {max_attempts: 3, job_timeout: 5m}
applications:
  - {id: app, name: App, quota: {limit: 100}}
`

func parseVersionConfig(t *testing.T, env map[string]string) *config.Config {
	t.Helper()
	cfg, err := config.Parse([]byte(versionConfig), func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestServiceVersion_form(t *testing.T) {
	cfg := parseVersionConfig(t, nil)
	tests := []struct{ build, prefix string }{
		{"v0.1.0", "vde-0.1.0+"},
		{"1.4.0", "vde-1.4.0+"},
		{"v0.2.0-rc.1", "vde-0.2.0-rc.1+"},
		{"v0.1.0-3-gabc1234-dirty", "vde-0.1.0-3-gabc1234-dirty+"},
		{"dev", "vde-0.0.0-dev+"},
		{"", "vde-0.0.0-dev+"},
		{"abc1234", "vde-0.0.0-dev+"},
		{"v1.2", "vde-0.0.0-dev+"},
	}
	form := regexp.MustCompile(`^vde-[0-9A-Za-z.-]+\+[0-9a-f]{8}$`)
	for _, tt := range tests {
		t.Run(tt.build, func(t *testing.T) {
			got := ServiceVersion(cfg, tt.build)
			if !strings.HasPrefix(got, tt.prefix) || !form.MatchString(got) {
				t.Errorf("ServiceVersion(%q) = %q, want %s<8 hex>", tt.build, got, tt.prefix)
			}
		})
	}
}

func TestApplicationServiceVersion(t *testing.T) {
	cfg := parseVersionConfig(t, nil)
	global := ServiceVersion(cfg, "v0.2.0")
	app := cfg.Applications[0] // max_attempts defaults to limits.max_attempts
	if got := ApplicationServiceVersion(cfg, app, "v0.2.0"); got != global {
		t.Errorf("default application = %q, want the global %q", got, global)
	}
	app.MaxAttempts = 5
	five := ApplicationServiceVersion(cfg, app, "v0.2.0")
	if five == global {
		t.Error("an application attempt limit of 5 did not change service_version")
	}
	app.MaxAttempts, app.Documents.HTML = 3, "visible_text"
	if got := ApplicationServiceVersion(cfg, app, "v0.2.0"); got == global || got == five {
		t.Errorf("an application HTML policy did not change service_version: %q", got)
	}
	if cfg.Limits.MaxAttempts != 3 || cfg.Documents.HTML != "raw_html" {
		t.Error("ApplicationServiceVersion changed the configuration")
	}
}

func TestServiceVersion_stability(t *testing.T) {
	base := ServiceVersion(parseVersionConfig(t, nil), "v0.1.0")
	if again := ServiceVersion(parseVersionConfig(t, nil), "v0.1.0"); again != base {
		t.Fatalf("same configuration: %q then %q", base, again)
	}
	tests := []struct {
		name    string
		edit    func(*config.Config)
		changes bool
	}{
		{"model", func(c *config.Config) { c.Groups["review"].Endpoints[0].Model = "model-b" }, true},
		{"endpoint order", func(c *config.Config) {
			e := c.Groups["extraction"].Endpoints
			e[0], e[1] = e[1], e[0]
		}, true},
		{"accepts", func(c *config.Config) {
			c.Endpoints["spare"].Models["model-c"] = config.Model{Accepts: []string{"text/plain"}}
		}, true},
		{"base URL host", func(c *config.Config) { c.Endpoints["spare"].BaseURL = "https://other.example/v1" }, true},
		{"provider type", func(c *config.Config) { c.Endpoints["spare"].Provider = "gemini" }, true},
		{"failover tries", func(c *config.Config) { c.Groups["extraction"].Failover.MaxTries = 3 }, true},
		{"failover time", func(c *config.Config) { c.Groups["extraction"].Failover.MaxTotalTime = 2 * time.Minute }, true},
		{"failover on permanent", func(c *config.Config) { c.Groups["review"].Failover.FailoverOnPermanent = true }, true},
		{"role group", func(c *config.Config) { c.Roles.Judge.Group = "extraction" }, true},
		{"HTML policy", func(c *config.Config) { c.Documents.HTML = "visible_text" }, true},
		{"attempt limit", func(c *config.Config) { c.Limits.MaxAttempts = 2 }, true},
		{"job timeout", func(c *config.Config) { c.Limits.JobTimeout = 4 * time.Minute }, true},
		{"judge reparse tries", func(c *config.Config) { c.Limits.JudgeReparseTries = 2 }, true},
		{"feedback cap", func(c *config.Config) { c.Limits.FeedbackMessageChars = 2000 }, true},
		{"feedback cap default spelled out", func(c *config.Config) { c.Limits.FeedbackMessageChars = DefaultFeedbackMessageChars }, false},
		{"accepts order", func(c *config.Config) {
			c.Endpoints["main"].Models["model-a"] = config.Model{Accepts: []string{"application/pdf", "text/plain"}}
		}, false},
		{"base URL path", func(c *config.Config) { c.Endpoints["spare"].BaseURL = "https://spare.example/v2" }, false},
		{"secret", func(c *config.Config) { *c = *parseVersionConfig(t, map[string]string{"KEY_MAIN": "sk-other"}) }, false},
		{"key variable", func(c *config.Config) { c.Endpoints["main"].APIKeyEnv = "OTHER" }, false},
		{"endpoint timeout", func(c *config.Config) { c.Endpoints["main"].Timeout = time.Minute }, false},
		{"listen address", func(c *config.Config) { c.Server.Listen = ":9000" }, false},
		{"data dir", func(c *config.Config) { c.Server.DataDir = "/tmp/vde" }, false},
		{"quota", func(c *config.Config) { c.Applications[0].Quota.Limit = 5 }, false},
		{"unused group", func(c *config.Config) { c.Groups["unused"].Endpoints[0].Model = "model-a" }, false},
		{"logging", func(c *config.Config) { c.Logging.Level = "debug" }, false},
		{"document limits", func(c *config.Config) { c.Limits.Pages = 10 }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := parseVersionConfig(t, nil)
			tt.edit(cfg)
			got := ServiceVersion(cfg, "v0.1.0")
			if changed := got != base; changed != tt.changes {
				t.Errorf("ServiceVersion = %q (base %q): changed = %v, want %v", got, base, changed, tt.changes)
			}
		})
	}
}

func TestServiceVersion_framing(t *testing.T) {
	cfg := parseVersionConfig(t, nil)
	base := ServiceVersion(cfg, "v0.1.0")
	saved := judgeFraming
	t.Cleanup(func() { judgeFraming = saved })
	judgeFraming += "\nOne more rule."
	if got := ServiceVersion(cfg, "v0.1.0"); got == base {
		t.Errorf("changing a framing kept %q", got)
	}
}

func TestServiceVersion_missingGroup(t *testing.T) {
	cfg := parseVersionConfig(t, nil)
	cfg.Roles.Judge.Group = "nope"
	if got := ServiceVersion(cfg, "v0.1.0"); !strings.HasPrefix(got, "vde-0.1.0+") {
		t.Errorf("ServiceVersion = %q", got)
	}
}

func TestModuleVersion(t *testing.T) {
	if got := moduleVersion("example.com/not/linked"); got != "unknown" {
		t.Errorf("moduleVersion(unlinked) = %q, want unknown", got)
	}
	if got := moduleVersion(validatorModule); !strings.HasPrefix(got, "v6.") {
		t.Errorf("moduleVersion(validator) = %q, want v6.x", got)
	}
}
