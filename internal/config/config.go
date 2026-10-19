package config

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"io"
	"maps"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// Config is the whole configuration file. Defaults are applied by Parse; semantic checks
// are done by Validate.
type Config struct {
	Server       Server               `yaml:"server"`
	Endpoints    map[string]*Endpoint `yaml:"endpoints"`
	Groups       map[string]*Group    `yaml:"groups"`
	Roles        Roles                `yaml:"roles"`
	Limits       Limits               `yaml:"limits"`
	Documents    Documents            `yaml:"documents"`
	Applications []Application        `yaml:"applications"`
	Auth         Auth                 `yaml:"auth"`
	Logging      Logging              `yaml:"logging"`
	Debug        Debug                `yaml:"debug"`
	// Metrics, Admin, Worker, Rate, Retention and Audit configure the serve process (service.go).
	Metrics   Metrics          `yaml:"metrics"`
	Admin     Admin            `yaml:"admin"`
	Worker    Worker           `yaml:"worker"`
	Rate      ServiceRate      `yaml:"rate"`
	Retention ServiceRetention `yaml:"retention"`
	Audit     Audit            `yaml:"audit"`
	// Storage and Blobs choose the job store and blob store drivers (storage.go).
	Storage Storage `yaml:"storage"`
	Blobs   Blobs   `yaml:"blobs"`
	// Sync bounds POST /v1/extract (delivery.go).
	Sync Sync `yaml:"sync"`

	typeIssues Issues // set by Parse, reported by Validate (stringScalars)
}

// Server configures the HTTP listener (used from v0.2).
type Server struct {
	Listen       string `yaml:"listen"`
	TLS          TLS    `yaml:"tls"`
	TrustedProxy bool   `yaml:"trusted_proxy"` // require X-Forwarded-Proto: https
	// InsecureHTTP allows plain HTTP from any peer on any listener, for development and
	// containers on a trusted private network only. Never together with TLS.
	InsecureHTTP bool   `yaml:"insecure_http"`
	DataDir      string `yaml:"data_dir"`
}

// DatabasePath is the SQLite file inside DataDir that serve and the operator commands
// share.
func (s Server) DatabasePath() string { return filepath.Join(s.DataDir, "vde.db") }

// TLS names the certificate and key the binary terminates TLS with.
type TLS struct {
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
}

// Endpoint is one provider endpoint and the models it serves.
type Endpoint struct {
	Provider  string `yaml:"provider"` // anthropic, openai_compatible, gemini, bedrock, vertex or fake
	Preset    string `yaml:"preset,omitempty"`
	BaseURL   string `yaml:"base_url"`
	APIKeyEnv string `yaml:"api_key_env"` // name of the environment variable holding the key
	// Region is the cloud region of a bedrock or vertex endpoint (for example us-east-1,
	// us-east5). Credentials come from the cloud's default chain, not from the file.
	Region string `yaml:"region,omitempty"`
	// Project is the Google Cloud project id of a vertex endpoint.
	Project string           `yaml:"project,omitempty"`
	Timeout time.Duration    `yaml:"timeout"` // per call
	Models  map[string]Model `yaml:"models"`

	apiKey string // resolved from APIKeyEnv at load; never serialized or printed
}

// APIKey returns the key resolved from the environment variable named by APIKeyEnv, or ""
// when none is configured or the variable is unset.
func (e *Endpoint) APIKey() string { return e.apiKey }

// String describes the endpoint with its key redacted.
func (e Endpoint) String() string {
	key := "unset"
	if e.apiKey != "" {
		key = "[redacted]"
	}
	return fmt.Sprintf(
		"provider=%s base_url=%s api_key_env=%s api_key=%s timeout=%s models=%d",
		e.Provider, e.BaseURL, e.APIKeyEnv, key, e.Timeout, len(e.Models),
	)
}

// GoString keeps %#v from printing the key.
func (e Endpoint) GoString() string { return "config.Endpoint{" + e.String() + "}" }

// Model is one endpoint-model pair's capability declaration.
type Model struct {
	Accepts []string `yaml:"accepts"` // media types the model reads natively
	// MaxOutputTokens caps the tokens of one answer; 0 means DefaultMaxOutputTokens.
	MaxOutputTokens int `yaml:"max_output_tokens"`
	// Questions are the fixed typed questions a classifier model answers (provider
	// classifier only; required there).
	Questions []Question `yaml:"questions,omitempty"`
}

// Question is one typed question of a classifier model, phrased so that "yes" means the
// candidate passes. The verdict is accept when every question's probability of "yes" is
// at least its threshold.
type Question struct {
	ID        string  `yaml:"id"`
	Text      string  `yaml:"text"`
	Threshold float64 `yaml:"threshold"` // from 0 (exclusive) to 1
}

// DefaultMaxOutputTokens is the output limit requested from a model that sets no
// max_output_tokens. Large documents in schema-less mode produce long JSON.
const DefaultMaxOutputTokens = 16384

// OutputTokens returns the output-token limit to request from the model.
func (m Model) OutputTokens() int {
	if m.MaxOutputTokens > 0 {
		return m.MaxOutputTokens
	}
	return DefaultMaxOutputTokens
}

// Group is an ordered list of endpoint-model pairs serving a role, with failover bounds.
type Group struct {
	Endpoints []GroupEntry `yaml:"endpoints"`
	Failover  Failover     `yaml:"failover"`
}

// GroupEntry references a model declared on an endpoint.
type GroupEntry struct {
	Endpoint string `yaml:"endpoint"`
	Model    string `yaml:"model"`
}

// Failover bounds the tries of one role call across a group's endpoints.
type Failover struct {
	MaxTries     int           `yaml:"max_tries"`
	MaxTotalTime time.Duration `yaml:"max_total_time"` // must be less than limits.job_timeout
	// FailoverOnPermanent moves on to the next pair after a permanent failure (a refusal,
	// a bad request) instead of returning it at once.
	FailoverOnPermanent bool `yaml:"failover_on_permanent"`
}

// Roles assigns a group to each role. One group may serve both.
type Roles struct {
	Extractor Role      `yaml:"extractor"`
	Judge     JudgeRole `yaml:"judge"`
	// SecondJudge, when its group is set, must also accept a candidate the judge accepted
	// before the job is accepted. Unset by default.
	SecondJudge SecondJudge `yaml:"second_judge,omitempty"`
}

// Role names the group serving it.
type Role struct {
	Group string `yaml:"group"`
	// Escalation moves later attempts to other groups, in increasing from_attempt order;
	// failover stays inside each group.
	Escalation []Escalation `yaml:"escalation,omitempty"`
	// Policy is when the judge is asked (judge.go); roles.judge only, JudgeAlways when
	// unset. Validate refuses it on roles.extractor.
	Policy JudgePolicy `yaml:"policy,omitempty"`
}

// Escalation serves a role from Group from attempt FromAttempt on.
type Escalation struct {
	FromAttempt int    `yaml:"from_attempt"`
	Group       string `yaml:"group"`
}

// JudgeRole is the judge's Role plus how a classifier judge's rejections get feedback.
type JudgeRole struct {
	Role `yaml:",inline"`
	// FeedbackFallback is FeedbackWholeResult (the default when empty) or FeedbackLLM.
	FeedbackFallback string `yaml:"feedback_fallback,omitempty"`
	// FeedbackGroup is the LLM group asked for pointer-level feedback when a classifier
	// endpoint rejects, with feedback_fallback: llm.
	FeedbackGroup string `yaml:"feedback_group,omitempty"`
}

// Feedback fallback modes of a classifier judge (roles.judge.feedback_fallback).
const (
	// FeedbackWholeResult keeps the classifier's single entry at pointer "".
	FeedbackWholeResult = "whole_result"
	// FeedbackLLM asks roles.judge.feedback_group for feedback on each classifier reject.
	FeedbackLLM = "llm"
)

// SecondJudge names the group of the optional second judge. It reads the judge's
// instructions.
type SecondJudge struct {
	Group string `yaml:"group"`
}

// ProviderClassifier is the provider type of classifier endpoints, which can serve only
// the judge role.
const ProviderClassifier = "classifier"

// Limits bounds the extraction loop and the documents accepted.
type Limits struct {
	MaxAttempts       int           `yaml:"max_attempts"` // default per application; a request can only lower it
	JobTimeout        time.Duration `yaml:"job_timeout"`  // hard max 8m
	JudgeReparseTries int           `yaml:"judge_reparse_tries"`
	// FeedbackMessageChars caps each feedback message; 0 means the extraction loop's
	// default of 4000.
	FeedbackMessageChars int   `yaml:"feedback_message_chars"`
	DocumentBytes        int64 `yaml:"document_bytes"`
	PageBytes            int64 `yaml:"page_bytes"`
	Pages                int   `yaml:"pages"`
	RequestBytes         int64 `yaml:"request_bytes"` // whole multipart request body (v0.2)
	// MaxQueuedJobs is the global ceiling on queued jobs, all applications together: at
	// or above it a submission answers 503 unavailable with Retry-After (v1.0). 0 means
	// no ceiling.
	MaxQueuedJobs int `yaml:"max_queued_jobs"`
	// MaxSubmitsInFlight bounds the submissions (POST /v1/jobs and POST /v1/extract) one
	// process reads and prepares at a time, each holding up to request_bytes in memory:
	// over it a submission answers 503 unavailable with Retry-After (v1.0). At least 1.
	MaxSubmitsInFlight int `yaml:"max_submits_in_flight"`
}

// Documents sets the representation policy.
type Documents struct {
	HTML   string `yaml:"html"` // raw_html or visible_text
	Images Images `yaml:"images"`
	PDF    PDF    `yaml:"pdf"`   // text layer and page rendering (ADR 0010)
	Fetch  Fetch  `yaml:"fetch"` // deprecated, no effect (delivery.go)
}

// Images is the image conversion policy (v0.3, ADR 0006): images and page images are
// downscaled, rotated by their EXIF orientation and re-encoded once at submit, and WebP
// is converted for endpoints that do not read it. It is part of service_version.
type Images struct {
	Enabled      bool   `yaml:"enabled"`
	MaxDimension int    `yaml:"max_dimension"` // longest side in pixels; larger images are scaled down
	ConvertTo    string `yaml:"convert_to"`    // image/jpeg or image/png
	Quality      int    `yaml:"quality"`       // JPEG quality 1-100
}

// PDF is how a PDF reaches groups that cannot read application/pdf natively (ADR 0010):
// as its text layer, as pages rendered by the service, or refused (native). Mode, DPI and
// MinTextCharsPerPage are part of service_version; Workers is a memory decision (each
// pdfium instance takes up to about 500 MB).
type PDF struct {
	Mode                string `yaml:"mode"`                    // auto, native, text or pages
	DPI                 int    `yaml:"dpi"`                     // rendering resolution, 72 to 300
	MaxPages            int    `yaml:"max_pages"`               // longer PDFs are refused unless read natively
	MinTextCharsPerPage int    `yaml:"min_text_chars_per_page"` // auto sends text when every page has this many
	Workers             int    `yaml:"workers"`                 // pdfium instances, 1 to 16
}

// Application is one caller with its own limits (used from v0.2).
type Application struct {
	ID          string       `yaml:"id"`
	Name        string       `yaml:"name"`
	MaxAttempts int          `yaml:"max_attempts"`
	Quota       Quota        `yaml:"quota"`
	Rate        Rate         `yaml:"rate"`
	Retention   Retention    `yaml:"retention"`
	Documents   AppDocuments `yaml:"documents"`
	Webhook     Webhook      `yaml:"webhook,omitempty"` // completion webhook (delivery.go); empty url means none
	Judge       AppJudge     `yaml:"judge,omitempty"`   // judge policy (judge.go)
}

// Quota is the number of submissions allowed per window.
type Quota struct {
	Limit  int    `yaml:"limit"`
	Window string `yaml:"window"` // ISO 8601 duration, e.g. P1D
}

// Rate holds an application's rate limits.
type Rate struct {
	RequestsPerMinute int `yaml:"requests_per_minute"`
	SubmitsPerMinute  int `yaml:"submits_per_minute"`
	MaxInProgress     int `yaml:"max_in_progress"`
}

// Retention is an application's retention policy.
type Retention struct {
	Default   string `yaml:"default"`    // a duration such as 24h, or none
	ForceNone bool   `yaml:"force_none"` // treat every job as retention: none
	// DeleteBlobsAtTerminal deletes the document and pages of a retention: default job
	// at its terminal state instead of at expiry.
	DeleteBlobsAtTerminal bool `yaml:"delete_blobs_at_terminal"`
}

// AppDocuments overrides the representation policy for one application.
type AppDocuments struct {
	HTML string `yaml:"html,omitempty"` // empty means documents.html
	PDF  AppPDF `yaml:"pdf,omitempty"`
}

// AppPDF overrides documents.pdf.mode for one application; the other PDF settings are
// process-wide.
type AppPDF struct {
	Mode string `yaml:"mode,omitempty"` // empty means documents.pdf.mode
}

// Auth bounds the access tokens the operator issues.
type Auth struct {
	MaxTokenTTL time.Duration `yaml:"max_token_ttl"` // longest allowed token lifetime
}

// Logging configures log output.
type Logging struct {
	Level  string `yaml:"level"`  // debug, info, warn or error
	Format string `yaml:"format"` // json or text
}

// Debug holds switches for local development only.
type Debug struct {
	LogContent bool `yaml:"log_content"`
}

// Defaults. Contract §4 sets the document limits; contract §6 and ADR 0004 the loop bounds.
const (
	defaultListen            = "127.0.0.1:8090"
	defaultDataDir           = "/var/lib/vde"
	defaultEndpointTimeout   = 90 * time.Second
	defaultFailoverTries     = 3
	defaultFailoverTotalTime = 3 * time.Minute
	defaultMaxAttempts       = 3
	defaultJobTimeout        = 5 * time.Minute
	defaultJudgeReparseTries = 1
	defaultDocumentBytes     = 25 << 20
	defaultPageBytes         = 10 << 20
	defaultPages             = 20
	defaultImageMaxDimension = 2048
	defaultImageQuality      = 85
	defaultPDFDPI            = 150
	defaultPDFMaxPages       = 20
	defaultPDFMinTextChars   = 200
	defaultPDFWorkers        = 1
	defaultRequestBytes      = 128 << 20
	defaultSubmitsInFlight   = 16
	defaultQuotaLimit        = 1000
	defaultQuotaWindow       = "P1D"
	defaultRequestsPerMinute = 300
	defaultSubmitsPerMinute  = 30
	defaultMaxInProgress     = 20
	defaultRetention         = "24h"
	defaultMaxTokenTTL       = 730 * 24 * time.Hour // two years
)

// defaultBaseURLs are used when an endpoint leaves base_url empty.
var defaultBaseURLs = map[string]string{
	"anthropic": "https://api.anthropic.com",
	"gemini":    "https://generativelanguage.googleapis.com",
}

// presetBaseURLs are the base URLs set by an openai_compatible preset.
var presetBaseURLs = map[string]string{
	"openrouter": "https://openrouter.ai/api/v1",
}

func defaults() *Config {
	return &Config{
		Server: Server{Listen: defaultListen, DataDir: defaultDataDir},
		Limits: Limits{
			MaxAttempts:        defaultMaxAttempts,
			JobTimeout:         defaultJobTimeout,
			JudgeReparseTries:  defaultJudgeReparseTries,
			DocumentBytes:      defaultDocumentBytes,
			PageBytes:          defaultPageBytes,
			Pages:              defaultPages,
			RequestBytes:       defaultRequestBytes,
			MaxSubmitsInFlight: defaultSubmitsInFlight,
		},
		Documents: Documents{
			HTML:   "raw_html",
			Images: Images{Enabled: true, MaxDimension: defaultImageMaxDimension, ConvertTo: "image/jpeg", Quality: defaultImageQuality},
			PDF: PDF{Mode: "auto", DPI: defaultPDFDPI, MaxPages: defaultPDFMaxPages,
				MinTextCharsPerPage: defaultPDFMinTextChars, Workers: defaultPDFWorkers},
		},
		Auth:      Auth{MaxTokenTTL: defaultMaxTokenTTL},
		Logging:   Logging{Level: "info", Format: "json"},
		Admin:     Admin{Listen: defaultAdminListen},
		Worker:    Worker{Size: defaultWorkerSize, PollInterval: defaultPollInterval, Lease: defaultLease},
		Retention: ServiceRetention{SweepInterval: defaultSweepInterval},
		Audit:     Audit{Retention: defaultAuditRetention},
		Storage:   defaultStorage(),
		Blobs:     defaultBlobs(),
	}
}

// Parse decodes a configuration, rejecting unknown keys, applies defaults and resolves
// each endpoint's key through getenv. Every decoding problem is reported, each prefixed
// with its line and key path. Parse does not validate semantics; call Validate, which
// also reports the warnings found while decoding.
func Parse(data []byte, getenv func(string) string) (*Config, error) {
	cfg := defaults()
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("config: file is empty")
		}
		return nil, decodeError(data, err)
	}
	var root yaml.Node
	_ = yaml.Unmarshal(data, &root) // cannot fail: Decode got past the syntax
	cfg.typeIssues = stringScalars(&root, reflect.TypeFor[Config](), "")
	cfg.applyDefaults()
	for _, e := range cfg.Endpoints {
		if e != nil && e.APIKeyEnv != "" {
			e.apiKey = getenv(e.APIKeyEnv)
		}
	}
	if cfg.Metrics.TokenEnv != "" {
		cfg.Metrics.token = getenv(cfg.Metrics.TokenEnv)
	}
	cfg.resolveDelivery(getenv)
	cfg.Storage.resolve(getenv)
	return cfg, nil
}

// applyDefaults fills values inside maps and lists, which cannot be pre-populated before
// decoding. A zero value there means "use the default".
func (c *Config) applyDefaults() {
	setDefault(&c.Roles.Judge.Policy, JudgeAlways)
	for _, e := range c.Endpoints {
		if e == nil {
			continue
		}
		if e.Timeout == 0 {
			e.Timeout = defaultEndpointTimeout
		}
		if e.BaseURL == "" {
			if u, ok := presetBaseURLs[e.Preset]; ok && e.Provider == "openai_compatible" {
				e.BaseURL = u
			} else {
				e.BaseURL = defaultBaseURLs[e.Provider]
			}
		}
	}
	for _, g := range c.Groups {
		if g == nil {
			continue
		}
		if g.Failover.MaxTries == 0 {
			g.Failover.MaxTries = defaultFailoverTries
		}
		if g.Failover.MaxTotalTime == 0 {
			g.Failover.MaxTotalTime = defaultFailoverTotalTime
		}
	}
	for i := range c.Applications {
		a := &c.Applications[i]
		setDefault(&a.MaxAttempts, c.Limits.MaxAttempts)
		setDefault(&a.Quota.Limit, defaultQuotaLimit)
		setDefault(&a.Quota.Window, defaultQuotaWindow)
		setDefault(&a.Rate.RequestsPerMinute, defaultRequestsPerMinute)
		setDefault(&a.Rate.SubmitsPerMinute, defaultSubmitsPerMinute)
		setDefault(&a.Rate.MaxInProgress, defaultMaxInProgress)
		setDefault(&a.Retention.Default, defaultRetention)
	}
}

func setDefault[T comparable](v *T, def T) {
	var zero T
	if *v == zero {
		*v = def
	}
}

// String renders the effective configuration as YAML. Secrets are never included: they
// live only in unexported fields.
func (c Config) String() string {
	out, err := yaml.Marshal(c)
	if err != nil {
		return "config: " + err.Error()
	}
	return string(out)
}

// GoString keeps %#v from printing secrets.
func (c Config) GoString() string { return c.String() }

var (
	lineRe         = regexp.MustCompile(`^line (\d+): (.*)$`)
	unknownFieldRe = regexp.MustCompile(`^field (\S+) not found in type \S+$`)
	quotedValueRe  = regexp.MustCompile("`([^`]*)`")
)

// decodeError rewrites yaml.v3 errors so each names the key path of the offending key.
func decodeError(data []byte, err error) error {
	var te *yaml.TypeError
	if !errors.As(err, &te) {
		return fmt.Errorf("config: %w", err) // syntax error, already carries the line
	}
	var root yaml.Node
	_ = yaml.Unmarshal(data, &root) // cannot fail: Decode got past the syntax
	errs := make([]error, 0, len(te.Errors))
	for _, msg := range te.Errors {
		m := lineRe.FindStringSubmatch(msg)
		if m == nil {
			errs = append(errs, errors.New("config: "+msg))
			continue
		}
		line, _ := strconv.Atoi(m[1])
		detail := m[2]
		match := func(k, v *yaml.Node) bool { return v.Kind == yaml.ScalarNode && v.Line == line }
		if f := unknownFieldRe.FindStringSubmatch(detail); f != nil {
			detail = "unknown key"
			match = func(k, _ *yaml.Node) bool { return k.Line == line && k.Value == f[1] }
		} else if q := quotedValueRe.FindStringSubmatch(detail); q != nil {
			// yaml.v3 shortens long values to their first characters plus "...".
			prefix, cut := strings.CutSuffix(q[1], "...")
			match = func(k, v *yaml.Node) bool {
				same := v.Value == q[1] || cut && strings.HasPrefix(v.Value, prefix)
				return v.Kind == yaml.ScalarNode && v.Line == line && same
			}
		}
		if path := keyPath(&root, match); path != "" {
			errs = append(errs, fmt.Errorf("config: line %d: %s: %s", line, path, detail))
			continue
		}
		errs = append(errs, fmt.Errorf("config: line %d: %s", line, detail))
	}
	return errors.Join(errs...)
}

// stringScalars warns about a scalar of another YAML type where the configuration holds
// a string: yaml.v3 reads name: 42 as "42" and date: 2026-10-16 as "2026-10-16", the
// scalar as written, and 1.0 ran such files, so they decode as before and Validate
// reports a warning until 2.0 refuses them. t is the type n decodes into, path its key.
//
// The walk follows what the decoder reads: an alias is checked as the node it names
// (Decode has already refused recursive and excessive aliases), and a << merge key's
// mapping, or each mapping of its list, is checked as part of the mapping it merges into,
// except the keys that mapping sets itself. A key that an earlier mapping of the same
// merge list shadows is still checked; the warning is then about a value not used.
func stringScalars(n *yaml.Node, t reflect.Type, path string) Issues {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	var issues Issues
	switch n.Kind {
	case yaml.DocumentNode:
		for _, c := range n.Content {
			issues = append(issues, stringScalars(c, t, path)...)
		}
	case yaml.AliasNode:
		if n.Alias != nil {
			issues = stringScalars(n.Alias, t, path)
		}
	case yaml.ScalarNode:
		if tag := n.ShortTag(); t.Kind() == reflect.String && tag != "!!str" && tag != "!!null" {
			kind := map[string]string{"!!int": "number", "!!float": "number", "!!bool": "boolean"}[tag]
			issues = Issues{{Key: path, Warning: true, Message: cmp.Or(kind, strings.TrimPrefix(tag, "!!")) +
				" where a string is expected; quote it (refused in 2.0)"}}
		}
	case yaml.SequenceNode:
		if t.Kind() != reflect.Slice {
			return nil
		}
		for i, c := range n.Content {
			issues = append(issues, stringScalars(c, t.Elem(), path+"["+strconv.Itoa(i)+"]")...)
		}
	case yaml.MappingNode:
		issues = mappingStringScalars(n, t, path, nil)
	}
	return issues
}

// mappingStringScalars is stringScalars for the mapping n, skipping the keys in skip
// (those set by the mapping a << merge key merges n into).
func mappingStringScalars(n *yaml.Node, t reflect.Type, path string, skip map[string]bool) Issues {
	own := map[string]bool{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if k := n.Content[i]; k.ShortTag() != "!!merge" {
			own[k.Value] = true
		}
	}
	maps.Copy(own, skip)
	var issues Issues
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if k.ShortTag() == "!!merge" {
			if v.Kind == yaml.AliasNode && v.Alias != nil {
				v = v.Alias
			}
			sources := []*yaml.Node{v}
			if v.Kind == yaml.SequenceNode {
				sources = v.Content
			}
			for _, s := range sources {
				if s.Kind == yaml.AliasNode && s.Alias != nil {
					s = s.Alias
				}
				if s.Kind == yaml.MappingNode {
					issues = append(issues, mappingStringScalars(s, t, path, own)...)
				}
			}
			continue
		}
		if skip[k.Value] {
			continue
		}
		var ft reflect.Type
		switch t.Kind() {
		case reflect.Map:
			ft = t.Elem()
		case reflect.Struct:
			ft = yamlField(t, k.Value)
		}
		if ft != nil {
			issues = append(issues, stringScalars(v, ft, strings.TrimPrefix(path+"."+k.Value, "."))...)
		}
	}
	return issues
}

// yamlField returns the type of the field of struct t that decodes the key, inlined
// structs included, or nil.
func yamlField(t reflect.Type, key string) reflect.Type {
	for f := range t.Fields() {
		name, opts, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		switch {
		case opts == "inline":
			if ft := yamlField(f.Type, key); ft != nil {
				return ft
			}
		case name == key:
			return f.Type
		}
	}
	return nil
}

// keyPath returns the path (endpoints.local.models, applications[0].id) of the first
// mapping entry for which match reports true, or "".
func keyPath(n *yaml.Node, match func(k, v *yaml.Node) bool) string {
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) > 0 {
			return keyPath(n.Content[0], match)
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			if match(k, v) {
				return k.Value
			}
			if p := keyPath(v, match); p != "" {
				return k.Value + joinPath(p)
			}
		}
	case yaml.SequenceNode:
		for i, c := range n.Content {
			if p := keyPath(c, match); p != "" {
				return "[" + strconv.Itoa(i) + "]" + joinPath(p)
			}
		}
	}
	return ""
}

func joinPath(p string) string {
	if strings.HasPrefix(p, "[") {
		return p
	}
	return "." + p
}

// PDFModeOf is applications[].documents.pdf.mode of the configured application id, ""
// (documents.pdf.mode) when it sets none or is not configured (created over the admin
// API, which has no PDF setting).
func (c *Config) PDFModeOf(appID string) string {
	for _, a := range c.Applications {
		if a.ID == appID {
			return a.Documents.PDF.Mode
		}
	}
	return ""
}
