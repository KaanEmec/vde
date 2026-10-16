package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
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
	Provider  string           `yaml:"provider"` // anthropic, openai_compatible, gemini or fake
	Preset    string           `yaml:"preset,omitempty"`
	BaseURL   string           `yaml:"base_url"`
	APIKeyEnv string           `yaml:"api_key_env"` // name of the environment variable holding the key
	Timeout   time.Duration    `yaml:"timeout"`     // per call
	Models    map[string]Model `yaml:"models"`

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
	Extractor Role `yaml:"extractor"`
	Judge     Role `yaml:"judge"`
}

// Role names the group serving it.
type Role struct {
	Group string `yaml:"group"`
}

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
}

// Documents sets the representation policy.
type Documents struct {
	HTML string `yaml:"html"` // raw_html or visible_text
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
}

// AppDocuments overrides the representation policy for one application.
type AppDocuments struct {
	HTML string `yaml:"html,omitempty"` // empty means documents.html
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
			MaxAttempts:       defaultMaxAttempts,
			JobTimeout:        defaultJobTimeout,
			JudgeReparseTries: defaultJudgeReparseTries,
			DocumentBytes:     defaultDocumentBytes,
			PageBytes:         defaultPageBytes,
			Pages:             defaultPages,
		},
		Documents: Documents{HTML: "raw_html"},
		Auth:      Auth{MaxTokenTTL: defaultMaxTokenTTL},
		Logging:   Logging{Level: "info", Format: "json"},
	}
}

// Load reads the file at path and parses it with secrets taken from the process
// environment. See Parse.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return Parse(data, os.Getenv)
}

// Parse decodes a configuration, rejecting unknown keys, applies defaults and resolves
// each endpoint's key through getenv. Every decoding problem is reported, each prefixed
// with its line and key path. Parse does not validate semantics; call Validate.
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
	cfg.applyDefaults()
	for _, e := range cfg.Endpoints {
		if e != nil && e.APIKeyEnv != "" {
			e.apiKey = getenv(e.APIKeyEnv)
		}
	}
	return cfg, nil
}

// applyDefaults fills values inside maps and lists, which cannot be pre-populated before
// decoding. A zero value there means "use the default".
func (c *Config) applyDefaults() {
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
