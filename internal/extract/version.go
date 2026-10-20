package extract

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"regexp"
	"slices"

	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/document"
	"github.com/kaanemec/vde/internal/ground"
)

// pdfEngineModule is the module of the PDF engine (internal/pdf: pdfium compiled to
// WebAssembly, ADR 0010), whose version is hashed with the PDF policy.
const pdfEngineModule = "github.com/klippa-app/go-pdfium"

// Pinned versions of the libraries whose behaviour is part of service_version. They are
// constants rather than runtime/debug build info because test binaries built with
// -cover on some platforms carry no dependency list, and the hash must not depend on how
// the binary was built. TestPinnedModuleVersionsMatchGoMod keeps them equal to go.mod.
const (
	validatorVersion = "v6.0.3"
	pdfEngineVersion = "v1.21.3"
)

// devVersion is the semver of a build without a release version.
const devVersion = "0.0.0-dev"

var semverRe = regexp.MustCompile(`^v?([0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?)$`)

// ServiceVersion returns the contract §11 service_version, vde-<semver>+<8 hex>, for the
// effective configuration cfg and the build's version (internal/version.Version:
// v0.1.0 gives 0.1.0, anything that is not a semver gives 0.0.0-dev). The hash covers
// what changes results: per role, the group's ordered endpoint-model pairs (provider
// type, base URL host, model, accepted media types) and failover bounds; the attempt
// limit, job timeout, judge re-ask count and feedback message cap; the HTML and image
// policies; the framing texts and separators; the answer parser's leniency and the schema
// library's version. Configured extras are hashed too and leave the hash unchanged when
// unset: per-attempt escalation steps, the second judge's group, a classifier judge's
// feedback fallback (llm, with its group), a classifier model's questions and a model's
// structured_output native. The judge
// policy (ADR 0009) is hashed only when it is not always, and the sample rate only under
// auto. The grounding rules (ground.RulesVersion) are always hashed, since every policy
// grounds candidates (always for its one repair, ADR 0009). The document preparation
// version (document.PreparationFor: context line, page markers, email labels, and under
// visible_text the HTML data tables) is always hashed. The PDF policy (mode, dpi,
// minimum text characters) and the PDF engine's module version are hashed when they can
// change what is read: when a role's
// groups do not all read PDFs natively and the mode is not native, or when the judge
// policy is not always (natively sent PDFs are then opened for grounding). It excludes
// secrets, listen addresses, data paths and application settings.
//
// For an application use ApplicationServiceVersion, so each application's jobs
// carry the hash of the configuration that runs them.
func ServiceVersion(cfg *config.Config, buildVersion string) string {
	return serviceVersion(cfg, config.DefaultSampleRate, buildVersion)
}

func serviceVersion(cfg *config.Config, sampleRate float64, buildVersion string) string {
	semver := devVersion
	if m := semverRe.FindStringSubmatch(buildVersion); m != nil {
		semver = m[1]
	}
	sum := sha256.Sum256(canonicalConfig(cfg, sampleRate))
	return "vde-" + semver + "+" + hex.EncodeToString(sum[:4])
}

// ApplicationServiceVersion is ServiceVersion with the application's attempt limit,
// HTML policy, PDF mode and sample rate in place of the global ones (zero values keep the
// global ones), for a job run under policy: the application's effective one
// (cfg.JudgePolicyOf), or the one a request chose when the application allows override.
func ApplicationServiceVersion(cfg *config.Config, app config.Application, policy config.JudgePolicy, buildVersion string) string {
	c := *cfg
	if app.MaxAttempts > 0 {
		c.Limits.MaxAttempts = app.MaxAttempts
	}
	if app.Documents.HTML != "" {
		c.Documents.HTML = app.Documents.HTML
	}
	if app.Documents.PDF.Mode != "" {
		c.Documents.PDF.Mode = app.Documents.PDF.Mode
	}
	c.Roles.Judge.Policy = policy
	return serviceVersion(&c, app.Judge.Rate(), buildVersion)
}

type versionedConfig struct {
	Extractor            versionedGroup `json:"extractor"`
	Judge                versionedGroup `json:"judge"`
	MaxAttempts          int            `json:"max_attempts"`
	JobTimeout           string         `json:"job_timeout"`
	JudgeReparseTries    int            `json:"judge_reparse_tries"`
	FeedbackMessageChars int            `json:"feedback_message_chars"`
	HTMLPolicy           string         `json:"html_policy"`
	ImagePolicy          string         `json:"image_policy"`
	Framings             []string       `json:"framings"`
	Parser               string         `json:"parser"`
	Validator            string         `json:"validator"`
	// Optional extras, absent from the JSON when unset so existing configurations keep
	// their hash.
	ExtractorEscalation []versionedStep `json:"extractor_escalation,omitempty"`
	JudgeEscalation     []versionedStep `json:"judge_escalation,omitempty"`
	SecondJudge         *versionedGroup `json:"second_judge,omitempty"`
	FeedbackFallback    string          `json:"feedback_fallback,omitempty"` // only llm; whole_result is the default
	FeedbackJudge       *versionedGroup `json:"feedback_judge,omitempty"`
	JudgePolicy         string          `json:"judge_policy,omitempty"` // never or auto; always is the default
	SampleRate          *float64        `json:"sample_rate,omitempty"`  // auto only
	Grounding           string          `json:"grounding,omitempty"`    // ground.RulesVersion, always set
	Preparation         string          `json:"preparation,omitempty"`  // document.PreparationFor(HTML policy), always set
	PDF                 string          `json:"pdf,omitempty"`          // document.PDFPolicy.String(), when it matters
}

type versionedStep struct {
	FromAttempt int            `json:"from_attempt"`
	Group       versionedGroup `json:"group"`
}

type versionedQuestion struct {
	ID        string  `json:"id"`
	Text      string  `json:"text"`
	Threshold float64 `json:"threshold"`
}

type versionedGroup struct {
	Pairs               []versionedPair `json:"pairs"`
	MaxTries            int             `json:"max_tries"`
	MaxTotalTime        string          `json:"max_total_time"`
	FailoverOnPermanent bool            `json:"failover_on_permanent"`
}

type versionedPair struct {
	Provider string   `json:"provider"`
	Host     string   `json:"host"`
	Model    string   `json:"model"`
	Accepts  []string `json:"accepts"`
	// Questions of a classifier model, in order.
	Questions []versionedQuestion `json:"questions,omitempty"`
	// StructuredOutput is "native" when set so, absent otherwise (the default keeps the
	// hash).
	StructuredOutput string `json:"structured_output,omitempty"`
}

// canonicalConfig is the JSON hashed by ServiceVersion. Struct fields marshal in a fixed
// order and accepted media types are sorted, so equal configurations give equal bytes.
func canonicalConfig(cfg *config.Config, sampleRate float64) []byte {
	maxChars := cfg.Limits.FeedbackMessageChars
	if maxChars <= 0 {
		maxChars = DefaultFeedbackMessageChars
	}
	extractorSchema, judgeSchema := framings(false)
	extractorSchemaless, judgeSchemaless := framings(true)
	v := versionedConfig{
		Extractor:            versionGroup(cfg, cfg.Roles.Extractor.Group),
		Judge:                versionGroup(cfg, cfg.Roles.Judge.Group),
		MaxAttempts:          cfg.Limits.MaxAttempts,
		JobTimeout:           cfg.Limits.JobTimeout.String(),
		JudgeReparseTries:    cfg.Limits.JudgeReparseTries,
		FeedbackMessageChars: maxChars,
		HTMLPolicy:           document.HTMLPolicy(cfg.Documents.HTML).String(),
		ImagePolicy:          document.NewImagePolicy(cfg.Documents.Images).String(),
		Framings: []string{
			extractorSchema, judgeSchema, extractorSchemaless, judgeSchemaless,
			sepInstructions, sepSchema, sepDocument, sepPrevious, sepFeedback, sepCandidate,
			reaskReminder,
		},
		Parser:      parserLeniency,
		Validator:   validatorModule + "@" + validatorVersion,
		Grounding:   ground.RulesVersion,
		Preparation: document.PreparationFor(document.HTMLPolicy(cfg.Documents.HTML)),
	}
	v.ExtractorEscalation = versionSteps(cfg, cfg.Roles.Extractor.Escalation)
	v.JudgeEscalation = versionSteps(cfg, cfg.Roles.Judge.Escalation)
	if name := cfg.Roles.SecondJudge.Group; name != "" {
		g := versionGroup(cfg, name)
		v.SecondJudge = &g
	}
	if cfg.Roles.Judge.FeedbackFallback == config.FeedbackLLM {
		g := versionGroup(cfg, cfg.Roles.Judge.FeedbackGroup)
		v.FeedbackFallback, v.FeedbackJudge = config.FeedbackLLM, &g
	}
	if p := cfg.RolePolicy(); p != config.JudgeAlways {
		v.JudgePolicy = string(p)
		if p == config.JudgeAuto {
			v.SampleRate = &sampleRate
		}
	}
	if cfg.RolePolicy() != config.JudgeAlways || (cfg.Documents.PDF.Mode != config.PDFModeNative && !readsPDFNatively(cfg)) {
		// The engine's version too: another pdfium build may read other text or render
		// other pixels from the same PDF.
		v.PDF = document.NewPDFPolicy(cfg.Documents.PDF, "", nil).String() + "/engine=" + pdfEngineModule + "@" + pdfEngineVersion
	}
	data, err := json.Marshal(v)
	if err != nil {
		panic("extract: service version: " + err.Error()) // strings, ints, bools and finite floats always marshal
	}
	return data
}

// readsPDFNatively reports whether every group of both roles reads application/pdf
// natively, so the service never prepares a PDF itself.
func readsPDFNatively(cfg *config.Config) bool {
	extractor, judge := RoleCapabilities(cfg)
	for _, caps := range [][]config.Capability{extractor, judge} {
		i := slices.IndexFunc(caps, func(c config.Capability) bool { return c.MediaType == "application/pdf" })
		if i < 0 || caps[i].Via != config.ViaNative {
			return false
		}
	}
	return true
}

func versionSteps(cfg *config.Config, steps []config.Escalation) []versionedStep {
	var out []versionedStep
	for _, s := range steps {
		out = append(out, versionedStep{FromAttempt: s.FromAttempt, Group: versionGroup(cfg, s.Group)})
	}
	return out
}

func versionGroup(cfg *config.Config, name string) versionedGroup {
	g := cfg.Groups[name]
	if g == nil {
		return versionedGroup{}
	}
	out := versionedGroup{
		Pairs:               make([]versionedPair, 0, len(g.Endpoints)),
		MaxTries:            g.Failover.MaxTries,
		MaxTotalTime:        g.Failover.MaxTotalTime.String(),
		FailoverOnPermanent: g.Failover.FailoverOnPermanent,
	}
	if out.MaxTries <= 0 {
		out.MaxTries = len(g.Endpoints) // the router's default
	}
	for _, entry := range g.Endpoints {
		p := versionedPair{Model: entry.Model}
		if e := cfg.Endpoints[entry.Endpoint]; e != nil {
			p.Provider = e.Provider
			if u, err := url.Parse(e.BaseURL); err == nil {
				p.Host = u.Host
			}
			m := e.Models[entry.Model]
			p.Accepts = slices.Sorted(slices.Values(m.Accepts))
			for _, q := range m.Questions {
				p.Questions = append(p.Questions, versionedQuestion{ID: q.ID, Text: q.Text, Threshold: q.Threshold})
			}
			if m.NativeSchema() {
				p.StructuredOutput = config.StructuredNative
			}
		}
		out.Pairs = append(out.Pairs, p)
	}
	return out
}
