package extract

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/document"
	"github.com/kaanemec/vde/internal/ground"
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
	if got := ApplicationServiceVersion(cfg, app, cfg.JudgePolicyOf(app), "v0.2.0"); got != global {
		t.Errorf("default application = %q, want the global %q", got, global)
	}
	app.MaxAttempts = 5
	five := ApplicationServiceVersion(cfg, app, cfg.JudgePolicyOf(app), "v0.2.0")
	if five == global {
		t.Error("an application attempt limit of 5 did not change service_version")
	}
	app.MaxAttempts, app.Documents.HTML = 3, "visible_text"
	if got := ApplicationServiceVersion(cfg, app, cfg.JudgePolicyOf(app), "v0.2.0"); got == global || got == five {
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
		{"image policy", func(c *config.Config) { c.Documents.Images.MaxDimension = 1000 }, true},
		{"image conversion off", func(c *config.Config) { c.Documents.Images.Enabled = false }, true},
		{"extractor escalation", func(c *config.Config) {
			c.Roles.Extractor.Escalation = []config.Escalation{{FromAttempt: 3, Group: "unused"}}
		}, true},
		{"escalation attempt", func(c *config.Config) {
			c.Roles.Extractor.Escalation = []config.Escalation{{FromAttempt: 2, Group: "unused"}}
		}, true},
		{"judge escalation", func(c *config.Config) {
			c.Roles.Judge.Escalation = []config.Escalation{{FromAttempt: 3, Group: "unused"}}
		}, true},
		{"empty escalation", func(c *config.Config) { c.Roles.Extractor.Escalation = []config.Escalation{} }, false},
		{"second judge", func(c *config.Config) { c.Roles.SecondJudge.Group = "unused" }, true},
		{"feedback fallback llm", func(c *config.Config) {
			c.Roles.Judge.FeedbackFallback, c.Roles.Judge.FeedbackGroup = config.FeedbackLLM, "unused"
		}, true},
		{"feedback fallback default spelled out", func(c *config.Config) {
			c.Roles.Judge.FeedbackFallback = config.FeedbackWholeResult
		}, false},
		{"classifier questions", func(c *config.Config) {
			c.Endpoints["main"].Models["model-a"] = config.Model{
				Accepts:   []string{"text/plain", "application/pdf"},
				Questions: []config.Question{{ID: "accuracy", Text: "Accurate?", Threshold: 0.7}},
			}
		}, true},
		{"judge policy always spelled out", func(c *config.Config) { c.Roles.Judge.Policy = config.JudgeAlways }, false},
		{"judge policy unset", func(c *config.Config) { c.Roles.Judge.Policy = "" }, false},
		{"judge policy never", func(c *config.Config) { c.Roles.Judge.Policy = config.JudgeNever }, true},
		{"judge policy auto", func(c *config.Config) { c.Roles.Judge.Policy = config.JudgeAuto }, true},
		{"application judge settings", func(c *config.Config) {
			r := 0.5
			c.Applications[0].Judge = config.AppJudge{Policy: config.JudgeNever, AllowOverride: true, SampleRate: &r}
		}, false},
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

// TestServiceVersion_framingReleases: every framing or parser change gives a new
// service_version. previous lists the versions of the scenario configuration before each
// such change, oldest first, from v1.1.0 on; the current one differs from all of them.
func TestServiceVersion_framingReleases(t *testing.T) {
	scenario, err := os.ReadFile("../../testdata/scenarios/config/vde.yaml")
	if err != nil {
		t.Fatal(err)
	}
	previous := []string{
		"vde-1.1.0+a1704233", // v1.1.0
		"vde-1.1.0+a597a5da", // v1.2 job 01: document first, null and normalising rules
	}
	got := ServiceVersion(loadConfig(t, string(scenario)), "v1.1.0")
	for _, p := range previous {
		if got == p {
			t.Errorf("service_version %s is that of an earlier framing or parser", got)
		}
	}
}

func TestServiceVersion_missingGroup(t *testing.T) {
	cfg := parseVersionConfig(t, nil)
	cfg.Roles.Judge.Group = "nope"
	if got := ServiceVersion(cfg, "v0.1.0"); !strings.HasPrefix(got, "vde-0.1.0+") {
		t.Errorf("ServiceVersion = %q", got)
	}
}

func TestPinnedModuleVersionsMatchGoMod(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ module, want string }{
		{validatorModule, validatorVersion},
		{pdfEngineModule, pdfEngineVersion},
	} {
		re := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(tc.module) + `\s+(v\S+)`)
		m := re.FindStringSubmatch(string(data))
		if m == nil {
			t.Fatalf("%s not found in go.mod", tc.module)
		}
		if m[1] != tc.want {
			t.Errorf("%s: go.mod has %s, version.go pins %s; update the constant", tc.module, m[1], tc.want)
		}
	}
}

// TestServiceVersion_judgePolicyDefault: the default policy adds nothing to the hashed
// configuration, so a v0.3 configuration keeps its service_version.
func TestServiceVersion_judgePolicyDefault(t *testing.T) {
	cfg := parseVersionConfig(t, nil)
	unset := *cfg
	unset.Roles.Judge.Policy = ""
	for _, c := range []*config.Config{cfg, &unset} {
		data := string(canonicalConfig(c, config.DefaultSampleRate))
		if strings.Contains(data, "judge_policy") || strings.Contains(data, "sample_rate") {
			t.Errorf("canonical configuration under always carries the policy: %s", data)
		}
	}
	if string(canonicalConfig(cfg, 0.9)) != string(canonicalConfig(&unset, config.DefaultSampleRate)) {
		t.Error("the sample rate changes the hash under always")
	}
}

func TestApplicationServiceVersion_judge(t *testing.T) {
	cfg := parseVersionConfig(t, nil)
	global := ServiceVersion(cfg, "v0.4.0")
	app := cfg.Applications[0]
	if got := ApplicationServiceVersion(cfg, app, cfg.JudgePolicyOf(app), "v0.4.0"); got != global {
		t.Errorf("default application = %q, want the global %q", got, global)
	}
	explicit := app
	explicit.Judge.Policy = config.JudgeAlways
	if got := ApplicationServiceVersion(cfg, explicit, cfg.JudgePolicyOf(explicit), "v0.4.0"); got != global {
		t.Errorf("explicit always = %q, want the global %q", got, global)
	}
	rate := 0.2
	explicit.Judge.SampleRate = &rate
	if got := ApplicationServiceVersion(cfg, explicit, cfg.JudgePolicyOf(explicit), "v0.4.0"); got != global {
		t.Errorf("a sample rate under always = %q, want the global %q", got, global)
	}

	seen := map[string]string{global: "always"}
	for _, tt := range []struct {
		name string
		app  func(a *config.Application)
	}{
		{"never", func(a *config.Application) { a.Judge.Policy = config.JudgeNever }},
		{"auto", func(a *config.Application) { a.Judge.Policy = config.JudgeAuto }},
		{"auto, sample rate 0.2", func(a *config.Application) {
			r := 0.2
			a.Judge.Policy, a.Judge.SampleRate = config.JudgeAuto, &r
		}},
	} {
		a := app
		tt.app(&a)
		got := ApplicationServiceVersion(cfg, a, cfg.JudgePolicyOf(a), "v0.4.0")
		if prev, dup := seen[got]; dup {
			t.Errorf("%s has the service_version of %s: %q", tt.name, prev, got)
		}
		seen[got] = tt.name
	}

	// The role's policy is the application's default.
	never := *cfg
	never.Roles.Judge.Policy = config.JudgeNever
	viaRole := ApplicationServiceVersion(&never, app, (&never).JudgePolicyOf(app), "v0.4.0")
	own := app
	own.Judge.Policy = config.JudgeNever
	if viaRole != ApplicationServiceVersion(cfg, own, cfg.JudgePolicyOf(own), "v0.4.0") || viaRole == global {
		t.Errorf("roles.judge.policy never = %q, want the application never version", viaRole)
	}
	if cfg.Roles.Judge.Policy != config.JudgeAlways {
		t.Error("ApplicationServiceVersion changed the configuration")
	}
}

// TestServiceVersion_v04Fields: v0.4 hashes the grounding rules into every
// service_version and the PDF policy where it can change what is read (ADR 0009, ADR
// 0010). Without those two fields, the framings and the parser, the hash is the v0.3 one
// of the same fields, pinned here from the v0.3 code, so the loop's grounding repair
// under always changed service_version by these fields only. (Until v1.2 the framings and
// the parser were part of the pinned hash; v1.2 changes them, ADR 0012.)
func TestServiceVersion_v04Fields(t *testing.T) {
	scenario, err := os.ReadFile("../../testdata/scenarios/config/vde.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name    string
		cfg     *config.Config
		wantPDF bool
		v03     string
	}{
		// Both configurations read PDFs natively (a group reads a type when one of its
		// pairs does): under always the PDF policy changes nothing.
		{"version configuration", parseVersionConfig(t, nil), false, "vde-0.3.0+1efec537"},
		{"scenario configuration", loadConfig(t, string(scenario)), false, "vde-0.3.0+a2e02af3"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var v versionedConfig
			if err := json.Unmarshal(canonicalConfig(tt.cfg, config.DefaultSampleRate), &v); err != nil {
				t.Fatal(err)
			}
			if v.Grounding != ground.RulesVersion || (v.PDF != "") != tt.wantPDF {
				t.Errorf("grounding %q, pdf %q; want %q, pdf hashed %v", v.Grounding, v.PDF, ground.RulesVersion, tt.wantPDF)
			}
			// Since v1.2 the document preparation version is hashed too (document.PreparationVersion).
			if want := document.PreparationFor(document.HTMLPolicy(tt.cfg.Documents.HTML)); v.Preparation != want {
				t.Errorf("preparation %q, want %q", v.Preparation, want)
			}
			v.Grounding, v.PDF, v.Preparation, v.Framings, v.Parser = "", "", "", nil, ""
			stripped, err := json.Marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(stripped)
			if got := "vde-0.3.0+" + hex.EncodeToString(sum[:4]); got != tt.v03 {
				t.Errorf("without the v0.4 fields = %s, want the v0.3 %s: something else entered the hash "+
					"(or the JSON Schema library's version changed)", got, tt.v03)
			}
			if ServiceVersion(tt.cfg, "v0.3.0") == tt.v03 {
				t.Error("service_version did not change in v0.4")
			}
		})
	}
}

// TestServiceVersion_pdfPolicy: the PDF policy is hashed when a role's groups do not all
// read PDFs natively and the mode is not native, or when the judge policy is not always
// (natively sent PDFs are then opened for grounding); dpi, minimum characters and an
// application's mode then change the hash.
func TestServiceVersion_pdfPolicy(t *testing.T) {
	scenario, err := os.ReadFile("../../testdata/scenarios/config/vde.yaml")
	if err != nil {
		t.Fatal(err)
	}
	capable := loadConfig(t, string(scenario))
	base := ServiceVersion(capable, "v0.4.0")
	dpi := *capable
	dpi.Documents.PDF.DPI = 200
	if ServiceVersion(&dpi, "v0.4.0") != base {
		t.Error("dpi changed the hash of a PDF-capable configuration under always")
	}
	auto := dpi
	auto.Roles.Judge.Policy = config.JudgeAuto
	autoDefault := *capable
	autoDefault.Roles.Judge.Policy = config.JudgeAuto
	if ServiceVersion(&auto, "v0.4.0") == ServiceVersion(&autoDefault, "v0.4.0") {
		t.Error("dpi did not change the hash under auto")
	}

	cfg := parseVersionConfig(t, nil)
	cfg.Endpoints["main"].Models["model-a"] = config.Model{Accepts: []string{"text/plain"}} // no group reads PDFs now
	global := ServiceVersion(cfg, "v0.4.0")
	var v versionedConfig
	wantPDF := "auto/dpi=150/min_chars=200/engine=github.com/klippa-app/go-pdfium@" + pdfEngineVersion
	if err := json.Unmarshal(canonicalConfig(cfg, config.DefaultSampleRate), &v); err != nil || v.PDF != wantPDF {
		t.Errorf("pdf = %q (%v), want the default policy and the engine version %q hashed", v.PDF, err, wantPDF)
	}
	for name, edit := range map[string]func(*config.Config){
		"dpi":       func(c *config.Config) { c.Documents.PDF.DPI = 200 },
		"min chars": func(c *config.Config) { c.Documents.PDF.MinTextCharsPerPage = 50 },
		"mode":      func(c *config.Config) { c.Documents.PDF.Mode = config.PDFModeText },
	} {
		c := *cfg
		edit(&c)
		if ServiceVersion(&c, "v0.4.0") == global {
			t.Errorf("%s did not change the hash", name)
		}
	}
	workers := *cfg
	workers.Documents.PDF.Workers = 4
	if ServiceVersion(&workers, "v0.4.0") != global {
		t.Error("workers changed the hash")
	}
	native := *cfg
	native.Documents.PDF.Mode, native.Documents.PDF.DPI = config.PDFModeNative, 300
	native2 := native
	native2.Documents.PDF.DPI = 72
	if ServiceVersion(&native, "v0.4.0") != ServiceVersion(&native2, "v0.4.0") {
		t.Error("under native mode and always, dpi changed the hash")
	}
	app := cfg.Applications[0]
	app.Documents.PDF.Mode = config.PDFModePages
	if ApplicationServiceVersion(cfg, app, cfg.JudgePolicyOf(app), "v0.4.0") == global {
		t.Error("an application PDF mode did not change its service_version")
	}
}

// TestServiceVersion_preparation: the document preparation version is hashed always; the
// table output is part of it only under visible_text, so a raw_html deployment's hash
// does not move with it.
func TestServiceVersion_preparation(t *testing.T) {
	prep := func(html string) string {
		cfg := parseVersionConfig(t, nil)
		cfg.Documents.HTML = html
		var v versionedConfig
		if err := json.Unmarshal(canonicalConfig(cfg, config.DefaultSampleRate), &v); err != nil {
			t.Fatal(err)
		}
		return v.Preparation
	}
	for _, tt := range []struct{ html, want string }{
		{"", document.PreparationVersion},
		{"raw_html", document.PreparationVersion},
		{"visible_text", document.PreparationVersion + "+tables/1"},
	} {
		if got := prep(tt.html); got != tt.want {
			t.Errorf("html %q: preparation %q, want %q", tt.html, got, tt.want)
		}
	}
}
