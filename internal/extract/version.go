package extract

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"regexp"
	"runtime/debug"
	"slices"

	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/document"
)

// devVersion is the semver of a build without a release version.
const devVersion = "0.0.0-dev"

var semverRe = regexp.MustCompile(`^v?([0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?)$`)

// ServiceVersion returns the contract §11 service_version, vde-<semver>+<8 hex>, for the
// effective configuration cfg and the build's version (internal/version.Version:
// v0.1.0 gives 0.1.0, anything that is not a semver gives 0.0.0-dev). The hash covers
// what changes results: per role, the group's ordered endpoint-model pairs (provider
// type, base URL host, model, accepted media types) and failover bounds; the attempt
// limit, job timeout, judge re-ask count and feedback message cap; the HTML policy; the
// framing texts and separators; the answer parser's leniency and the schema library's
// version. It excludes secrets, listen addresses, data paths and application settings.
//
// For an application (v0.2) pass a copy of cfg whose Limits.MaxAttempts and
// Documents.HTML are the application's effective values, so each application's jobs
// carry the hash of the configuration that runs them.
func ServiceVersion(cfg *config.Config, buildVersion string) string {
	semver := devVersion
	if m := semverRe.FindStringSubmatch(buildVersion); m != nil {
		semver = m[1]
	}
	sum := sha256.Sum256(canonicalConfig(cfg))
	return "vde-" + semver + "+" + hex.EncodeToString(sum[:4])
}

type versionedConfig struct {
	Extractor            versionedGroup `json:"extractor"`
	Judge                versionedGroup `json:"judge"`
	MaxAttempts          int            `json:"max_attempts"`
	JobTimeout           string         `json:"job_timeout"`
	JudgeReparseTries    int            `json:"judge_reparse_tries"`
	FeedbackMessageChars int            `json:"feedback_message_chars"`
	HTMLPolicy           string         `json:"html_policy"`
	Framings             []string       `json:"framings"`
	Parser               string         `json:"parser"`
	Validator            string         `json:"validator"`
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
}

// canonicalConfig is the JSON hashed by ServiceVersion. Struct fields marshal in a fixed
// order and accepted media types are sorted, so equal configurations give equal bytes.
func canonicalConfig(cfg *config.Config) []byte {
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
		Framings: []string{
			extractorSchema, judgeSchema, extractorSchemaless, judgeSchemaless,
			sepInstructions, sepSchema, sepDocument, sepPrevious, sepFeedback, sepCandidate,
			reaskReminder,
		},
		Parser:    parserLeniency,
		Validator: validatorModule + "@" + moduleVersion(validatorModule),
	}
	data, err := json.Marshal(v)
	if err != nil {
		panic("extract: service version: " + err.Error()) // strings, ints and bools always marshal
	}
	return data
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
			p.Accepts = slices.Sorted(slices.Values(e.Models[entry.Model].Accepts))
		}
		out.Pairs = append(out.Pairs, p)
	}
	return out
}

// moduleVersion returns the version of a module linked into the binary, or "unknown".
func moduleVersion(path string) string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	for _, dep := range info.Deps {
		if dep.Path == path {
			if dep.Replace != nil {
				return dep.Replace.Version
			}
			return dep.Version
		}
	}
	return "unknown"
}
