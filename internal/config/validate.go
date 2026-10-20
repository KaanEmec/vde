package config

import (
	"errors"
	"fmt"
	"maps"
	"net"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Issue is one finding of Validate, naming the key and the rule it breaks.
type Issue struct {
	Key     string // dotted key path, e.g. groups.extraction.endpoints[1].model
	Message string
	Warning bool // a warning blocks start only under strict validation
}

func (i Issue) String() string {
	level := "error"
	if i.Warning {
		level = "warning"
	}
	return level + ": " + i.Key + ": " + i.Message
}

// Issues is the full result of Validate, errors and warnings in key order.
type Issues []Issue

// Err returns all blocking issues joined into one error, or nil. With strict, warnings
// block too.
func (is Issues) Err(strict bool) error {
	var errs []error
	for _, i := range is {
		if !i.Warning || strict {
			errs = append(errs, errors.New(i.Key+": "+i.Message))
		}
	}
	return errors.Join(errs...)
}

// Validate checks the configuration against every rule and returns all findings. It makes
// no network calls.
func (c *Config) Validate() Issues {
	v := validator{issues: slices.Clone(c.typeIssues)}
	v.server(c)
	v.endpoints(c)
	v.groups(c)
	v.roles(c)
	v.limits(c)
	v.other(c)
	v.applications(c)
	v.service(c)
	v.storage(c)
	v.delivery(c)
	return v.issues
}

type validator struct{ issues Issues }

func (v *validator) errorf(key, format string, args ...any) {
	v.issues = append(v.issues, Issue{Key: key, Message: fmt.Sprintf(format, args...)})
}

// oneOf refuses val at key unless it is in list.
func (v *validator) oneOf(key, val string, list []string) {
	if !slices.Contains(list, val) {
		v.errorf(key, "%q is not one of %s", val, strings.Join(list, ", "))
	}
}

// between refuses n at key outside lo to hi, both included.
func (v *validator) between(key string, n, lo, hi int) {
	if n < lo || n > hi {
		v.errorf(key, "%d must be between %d and %d", n, lo, hi)
	}
}

func (v *validator) warnf(key, format string, args ...any) {
	v.issues = append(v.issues, Issue{Key: key, Message: fmt.Sprintf(format, args...), Warning: true})
}

// Hard limits and the minimums contract §4 requires the service to allow.
const (
	maxJobTimeout        = 8 * time.Minute
	contractDocumentSize = 25 << 20
	contractPageSize     = 10 << 20
	contractPages        = 20
	contractRequestSize  = 128 << 20
	maxPages             = 999 // blob names are page-01 to page-999 (internal/blob)
)

var (
	providers = []string{"anthropic", "openai_compatible", "gemini", "bedrock", "vertex", ProviderClassifier, "fake"}
	// knownMediaTypes are the document types of contract §4, MUST and SHOULD.
	knownMediaTypes = []string{
		"application/pdf", "image/jpeg", "image/png", "image/webp", "image/heic",
		"text/plain", "text/html", "text/markdown",
	}
	htmlPolicies = []string{"raw_html", "visible_text"}
	envNameRe    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

func (v *validator) server(c *Config) {
	_, _, err := net.SplitHostPort(c.Server.Listen)
	if err != nil {
		v.errorf("server.listen", "%q is not host:port", c.Server.Listen)
	}
	if (c.Server.TLS.CertFile == "") != (c.Server.TLS.KeyFile == "") {
		v.errorf("server.tls", "cert_file and key_file must be set together")
	}
	if c.Debug.LogContent {
		v.warnf("debug.log_content", "has no effect and is deprecated (removal in 2.0): no content is logged")
	}
	if c.Server.InsecureHTTP {
		if c.Server.TLS.CertFile != "" || c.Server.TLS.KeyFile != "" {
			v.errorf("server.insecure_http", "must not be true together with server.tls")
		} else {
			v.warnf("server.insecure_http", "plain HTTP is accepted from any peer: tokens and documents cross the network "+
				"unencrypted; use it only on a trusted private network (development, containers)")
		}
	}
	if c.Auth.MaxTokenTTL <= 0 {
		v.errorf("auth.max_token_ttl", "must be positive")
	}
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (v *validator) endpoints(c *Config) {
	if len(c.Endpoints) == 0 {
		v.errorf("endpoints", "at least one endpoint is required")
	}
	for _, name := range slices.Sorted(maps.Keys(c.Endpoints)) {
		key := "endpoints." + name
		e := c.Endpoints[name]
		if e == nil {
			v.errorf(key, "is empty")
			continue
		}
		v.oneOf(key+".provider", e.Provider, providers)
		if e.Provider == ProviderClassifier {
			v.warnf(key+".provider", "classifier is deprecated (removal in 2.0); it still works")
		}
		if e.Preset != "" {
			if _, ok := presetBaseURLs[e.Preset]; !ok {
				v.errorf(key+".preset", "unknown preset %q", e.Preset)
			} else if e.Provider != "openai_compatible" {
				v.errorf(key+".preset", "presets apply only to openai_compatible endpoints")
			}
		}
		switch u, err := url.Parse(e.BaseURL); {
		case e.BaseURL == "" && e.Provider != "fake" && !cloudProvider(e.Provider):
			v.errorf(key+".base_url", "is required for provider %q without a preset", e.Provider)
		case e.BaseURL != "" && (err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == ""):
			v.errorf(key+".base_url", "%q is not an absolute http or https URL", e.BaseURL)
		case u.Scheme == "http" && !isLoopback(u.Hostname()):
			v.warnf(key+".base_url", "%q is plain HTTP on a non-loopback host: documents and the API key cross the network unencrypted", u.Redacted())
		}
		v.apiKey(key, e)
		v.cloud(key, e)
		if e.Timeout < 0 {
			v.errorf(key+".timeout", "must be positive")
		}
		if len(e.Models) == 0 {
			v.errorf(key+".models", "at least one model is required")
		}
		for _, model := range slices.Sorted(maps.Keys(e.Models)) {
			mkey := key + ".models." + model + ".accepts"
			if e.Models[model].MaxOutputTokens < 0 {
				v.errorf(key+".models."+model+".max_output_tokens", "must be positive")
			}
			accepts := e.Models[model].Accepts
			if len(accepts) == 0 {
				v.errorf(mkey, "must list the media types the model reads")
			}
			for _, mt := range accepts {
				if !slices.Contains(knownMediaTypes, mt) {
					v.errorf(mkey, "unknown media type %q; known: %s", mt, strings.Join(knownMediaTypes, ", "))
				}
				if e.Provider == ProviderClassifier && !strings.HasPrefix(mt, "text/") {
					v.errorf(mkey, "%q: a classifier reads text only", mt)
				}
			}
			v.questions(key+".models."+model+".questions", e.Provider, e.Models[model].Questions)
		}
	}
}

// questions checks a classifier model's questions; other providers take none.
func (v *validator) questions(key, provider string, qs []Question) {
	if provider != ProviderClassifier {
		if len(qs) > 0 {
			v.errorf(key, "apply only to classifier endpoints")
		}
		return
	}
	if len(qs) == 0 {
		v.errorf(key, "a classifier model needs at least one question")
	}
	seen := map[string]bool{}
	for i, q := range qs {
		qkey := fmt.Sprintf("%s[%d]", key, i)
		switch {
		case q.ID == "":
			v.errorf(qkey+".id", "is required")
		case seen[q.ID]:
			v.errorf(qkey+".id", "duplicate question id %q", q.ID)
		}
		seen[q.ID] = true
		if strings.TrimSpace(q.Text) == "" {
			v.errorf(qkey+".text", "is required")
		}
		if q.Threshold <= 0 || q.Threshold > 1 {
			v.errorf(qkey+".threshold", "%g must be above 0 and at most 1", q.Threshold)
		}
	}
}

// cloudProvider reports whether the provider authenticates with the cloud's own
// credential chain (AWS default chain, Google application default credentials) instead
// of an API key, and takes its location from region (and project) instead of a base_url.
func cloudProvider(p string) bool { return p == "bedrock" || p == "vertex" }

// cloud checks the region and project of bedrock and vertex endpoints, and that no other
// provider sets them (a silent ignore would hide a misplaced key).
func (v *validator) cloud(key string, e *Endpoint) {
	if cloudProvider(e.Provider) {
		if e.Region == "" {
			v.errorf(key+".region", "is required for provider %q", e.Provider)
		}
		if e.Provider == "vertex" && e.Project == "" {
			v.errorf(key+".project", "is required for provider %q", e.Provider)
		}
		if e.APIKeyEnv != "" {
			v.errorf(key+".api_key_env", "is not used by provider %q: credentials come from the cloud's default credential chain", e.Provider)
		}
		return
	}
	if e.Region != "" {
		v.errorf(key+".region", "applies only to bedrock and vertex endpoints")
	}
	if e.Project != "" {
		v.errorf(key+".project", "applies only to vertex endpoints")
	}
}

// apiKey checks api_key_env. Only names are ever printed, never values.
func (v *validator) apiKey(key string, e *Endpoint) {
	if cloudProvider(e.Provider) {
		return // v.cloud reports a key variable that is set but unused
	}
	key += ".api_key_env"
	switch {
	case e.APIKeyEnv == "":
		if e.Provider != "openai_compatible" && e.Provider != "fake" && e.Provider != ProviderClassifier {
			v.errorf(key, "is required for provider %q", e.Provider)
		}
	case !envNameRe.MatchString(e.APIKeyEnv):
		v.errorf(key, "%q is not an environment variable name", e.APIKeyEnv)
	case e.apiKey == "":
		v.errorf(key, "environment variable %s is not set", e.APIKeyEnv)
	}
}

func (v *validator) groups(c *Config) {
	if len(c.Groups) == 0 {
		v.errorf("groups", "at least one group is required")
	}
	for _, name := range slices.Sorted(maps.Keys(c.Groups)) {
		key := "groups." + name
		g := c.Groups[name]
		if g == nil || len(g.Endpoints) == 0 {
			v.errorf(key+".endpoints", "at least one endpoint is required")
			continue
		}
		for i, entry := range g.Endpoints {
			ekey := fmt.Sprintf("%s.endpoints[%d]", key, i)
			e, ok := c.Endpoints[entry.Endpoint]
			if !ok || e == nil {
				v.errorf(ekey+".endpoint", "endpoint %q is not defined", entry.Endpoint)
				continue
			}
			if _, ok := e.Models[entry.Model]; !ok {
				v.errorf(ekey+".model", "model %q is not declared on endpoint %q", entry.Model, entry.Endpoint)
			}
		}
		if g.Failover.MaxTries < 1 {
			v.errorf(key+".failover.max_tries", "must be at least 1")
		}
		switch {
		case g.Failover.MaxTotalTime <= 0:
			v.errorf(key+".failover.max_total_time", "must be positive")
		case g.Failover.MaxTotalTime >= c.Limits.JobTimeout && c.Limits.JobTimeout > 0:
			v.errorf(key+".failover.max_total_time", "%s must be less than limits.job_timeout %s",
				g.Failover.MaxTotalTime, c.Limits.JobTimeout)
		}
	}
}

func (v *validator) roles(c *Config) {
	v.role(c, "roles.extractor", c.Roles.Extractor, false)
	v.role(c, "roles.judge", c.Roles.Judge.Role, true)
	v.judgeFeedback(c)
	v.judgeRole(c)
	if g := c.Roles.SecondJudge.Group; g != "" {
		v.roleGroup(c, "roles.second_judge.group", g, true)
	}
	v.deprecatedRoles(c)
}

// deprecatedRoles warns once for each role option deprecated in 1.1 and removed in 2.0
// (the classifier provider warns from endpoints). They keep working until then.
func (v *validator) deprecatedRoles(c *Config) {
	const msg = "is deprecated (removal in 2.0); it still works"
	if len(c.Roles.Extractor.Escalation) > 0 {
		v.warnf("roles.extractor.escalation", msg)
	}
	if len(c.Roles.Judge.Escalation) > 0 {
		v.warnf("roles.judge.escalation", msg)
	}
	if c.Roles.SecondJudge.Group != "" {
		v.warnf("roles.second_judge", msg)
	}
	if c.Roles.Judge.FeedbackFallback != "" || c.Roles.Judge.FeedbackGroup != "" {
		v.warnf("roles.judge.feedback_fallback", "%s (with roles.judge.feedback_group)", msg)
	}
}

// role checks a role's group and its escalation steps: defined groups, from_attempt from
// 2, strictly increasing and at most limits.max_attempts.
func (v *validator) role(c *Config, base string, r Role, judge bool) {
	v.roleGroup(c, base+".group", r.Group, judge)
	prev := 1
	for i, e := range r.Escalation {
		key := fmt.Sprintf("%s.escalation[%d]", base, i)
		v.roleGroup(c, key+".group", e.Group, judge)
		switch {
		case i == 0 && e.FromAttempt < 2:
			v.errorf(key+".from_attempt", "%d must be at least 2: attempt 1 runs on %s.group", e.FromAttempt, base)
		case e.FromAttempt <= prev:
			v.errorf(key+".from_attempt", "%d must be greater than the previous step's %d", e.FromAttempt, prev)
		case c.Limits.MaxAttempts > 0 && e.FromAttempt > c.Limits.MaxAttempts:
			v.errorf(key+".from_attempt", "%d is above limits.max_attempts %d", e.FromAttempt, c.Limits.MaxAttempts)
		}
		prev = max(prev, e.FromAttempt)
	}
}

// roleGroup checks one group serving a role: named, defined, readable, and, for the
// extractor, free of classifier endpoints (a classifier does not generate).
func (v *validator) roleGroup(c *Config, key, group string, judge bool) {
	if group == "" {
		v.errorf(key, "is required")
		return
	}
	g, ok := c.Groups[group]
	if !ok {
		v.errorf(key, "group %q is not defined", group)
		return
	}
	if g == nil || len(g.Endpoints) == 0 {
		return // reported by groups
	}
	if !judge && c.hasClassifier(group) {
		v.errorf(key, "group %q has a classifier endpoint, which can serve only the judge", group)
	}
	var missing []string
	caps := c.Capabilities(group)
	for _, capability := range caps {
		if capability.MediaType == "application/pdf" && capability.Via != ViaNative {
			if !v.pdfReadable(key, group, c.Documents.PDF.Mode, capability.Via, readsText(caps)) {
				missing = append(missing, capability.MediaType)
			}
			continue
		}
		if capability.Required && capability.Via == "" {
			missing = append(missing, capability.MediaType)
		}
	}
	if len(missing) > 0 {
		v.warnf(key, "group %q cannot read %s, directly or via page images", group, strings.Join(missing, ", "))
	}
}

// pdfReadable reports whether a group without a native PDF reader still reads PDFs under
// documents.pdf.mode (ADR 0010), warning where it depends on callers or on a text layer.
// via is the PDF capability's Via: ViaPages when a pair reads PNG or JPEG, else "".
func (v *validator) pdfReadable(key, group, mode, via string, text bool) bool {
	images := via == ViaPages
	switch mode {
	case PDFModeNative:
		if images {
			v.warnf(key, "no endpoint-model pair in group %q reads application/pdf natively; PDFs work only "+
				"when callers send page images (documents.pdf.mode: native)", group)
		}
		return images
	case PDFModeText:
		return text
	case PDFModePages:
		return images
	}
	if !images && text {
		v.warnf(key, "group %q reads PDFs only through their text layer (documents.pdf.mode: auto); "+
			"PDFs without enough text are refused", group)
	}
	return images || text
}

// readsText reports whether a pair of the group reads text/plain.
func readsText(caps []Capability) bool {
	for _, capability := range caps {
		if capability.MediaType == "text/plain" {
			return capability.Via == ViaText
		}
	}
	return false
}

// judgeFeedback checks roles.judge.feedback_fallback and feedback_group.
func (v *validator) judgeFeedback(c *Config) {
	j := c.Roles.Judge
	switch j.FeedbackFallback {
	case "", FeedbackWholeResult:
		if j.FeedbackGroup != "" {
			v.errorf("roles.judge.feedback_group", "is used only with feedback_fallback: %s", FeedbackLLM)
		}
	case FeedbackLLM:
		if j.FeedbackGroup == "" {
			v.errorf("roles.judge.feedback_group", "is required with feedback_fallback: %s", FeedbackLLM)
			break
		}
		v.roleGroup(c, "roles.judge.feedback_group", j.FeedbackGroup, true)
		if c.hasClassifier(j.FeedbackGroup) {
			v.errorf("roles.judge.feedback_group", "group %q has a classifier endpoint; feedback needs an LLM group", j.FeedbackGroup)
		}
	default:
		v.errorf("roles.judge.feedback_fallback", "%q is not one of %s, %s", j.FeedbackFallback, FeedbackWholeResult, FeedbackLLM)
		return
	}
	if j.FeedbackFallback == "" {
		return
	}
	classifier := c.hasClassifier(j.Group) || c.hasClassifier(c.Roles.SecondJudge.Group)
	for _, e := range j.Escalation {
		classifier = classifier || c.hasClassifier(e.Group)
	}
	if !classifier {
		v.warnf("roles.judge.feedback_fallback", "has no effect: no judge group has a classifier endpoint")
	}
}

// hasClassifier reports whether group has an endpoint of provider classifier.
func (c *Config) hasClassifier(group string) bool {
	g := c.Groups[group]
	if g == nil {
		return false
	}
	for _, entry := range g.Endpoints {
		if e := c.Endpoints[entry.Endpoint]; e != nil && e.Provider == ProviderClassifier {
			return true
		}
	}
	return false
}

func (v *validator) limits(c *Config) {
	l := c.Limits
	if l.MaxAttempts < 1 {
		v.errorf("limits.max_attempts", "must be at least 1")
	}
	if l.JobTimeout <= 0 || l.JobTimeout > maxJobTimeout {
		v.errorf("limits.job_timeout", "%s must be positive and at most %s", l.JobTimeout, maxJobTimeout)
	}
	if l.JudgeReparseTries < 0 {
		v.errorf("limits.judge_reparse_tries", "must not be negative")
	}
	if l.FeedbackMessageChars < 0 {
		v.warnf("limits.feedback_message_chars", "%d is negative: the default is used (refused in 2.0)", l.FeedbackMessageChars)
	}
	if l.MaxQueuedJobs < 0 {
		v.errorf("limits.max_queued_jobs", "must not be negative (0 means no ceiling)")
	}
	if l.MaxSubmitsInFlight < 1 {
		v.errorf("limits.max_submits_in_flight", "must be at least 1")
	}
	if l.Pages > maxPages {
		v.errorf("limits.pages", "%d is over %d (stored page names have at most three digits)", l.Pages, maxPages)
	}
	for _, s := range []struct {
		key        string
		value, min int64
	}{
		{"limits.document_bytes", l.DocumentBytes, contractDocumentSize},
		{"limits.page_bytes", l.PageBytes, contractPageSize},
		{"limits.pages", int64(l.Pages), contractPages},
		{"limits.request_bytes", l.RequestBytes, contractRequestSize},
	} {
		switch {
		case s.value <= 0:
			v.errorf(s.key, "must be positive")
		case s.value < s.min:
			v.warnf(s.key, "%d is below the contract minimum of %d", s.value, s.min)
		}
	}
}

// Bounds of documents.images: below the minimum a page is unreadable; above the maximum
// the decoded image no longer fits a sensible memory budget.
const (
	minImageDimension = 64
	maxImageDimension = 16384
)

var imageTargets = []string{"image/jpeg", "image/png"}

func (v *validator) images(i Images) {
	v.between("documents.images.max_dimension", i.MaxDimension, minImageDimension, maxImageDimension)
	v.oneOf("documents.images.convert_to", i.ConvertTo, imageTargets)
	v.between("documents.images.quality", i.Quality, 1, 100)
}

// PDF modes (documents.pdf.mode, ADR 0010).
const (
	PDFModeAuto   = "auto"   // text when every page has enough text, else rendered pages
	PDFModeNative = "native" // ADR 0006: without a native reader, only caller pages
	PDFModeText   = "text"   // always the text layer
	PDFModePages  = "pages"  // always rendered pages
)

var pdfModes = []string{PDFModeAuto, PDFModeNative, PDFModeText, PDFModePages}

func (v *validator) pdf(p PDF) {
	v.oneOf("documents.pdf.mode", p.Mode, pdfModes)
	v.between("documents.pdf.dpi", p.DPI, 72, 300)
	v.between("documents.pdf.max_pages", p.MaxPages, 1, 200)
	if p.MinTextCharsPerPage < 0 {
		v.errorf("documents.pdf.min_text_chars_per_page", "%d must not be negative", p.MinTextCharsPerPage)
	}
	v.between("documents.pdf.workers", p.Workers, 1, 16)
}

func (v *validator) other(c *Config) {
	v.oneOf("documents.html", c.Documents.HTML, htmlPolicies)
	v.images(c.Documents.Images)
	v.pdf(c.Documents.PDF)
	v.oneOf("logging.level", c.Logging.Level, []string{"debug", "info", "warn", "error"})
	v.oneOf("logging.format", c.Logging.Format, []string{"json", "text"})
}

// CheckApplication applies the applications[] defaults to a (a zero value means the
// default, as in the YAML) and validates it as one entry, for an application created or
// changed over the admin API. Issue keys are relative to the application (quota.window).
func (c *Config) CheckApplication(a *Application) Issues {
	tmp := Config{Limits: c.Limits, Applications: []Application{*a}}
	tmp.applyDefaults()
	*a = tmp.Applications[0]
	var v validator
	v.applications(&tmp)
	for i := range v.issues {
		v.issues[i].Key = strings.TrimPrefix(v.issues[i].Key, "applications[0].")
	}
	return v.issues
}

func (v *validator) applications(c *Config) {
	seen := map[string]bool{}
	for i, a := range c.Applications {
		key := fmt.Sprintf("applications[%d]", i)
		switch {
		case a.ID == "":
			v.errorf(key+".id", "is required")
		case seen[a.ID]:
			v.errorf(key+".id", "duplicate application id %q", a.ID)
		}
		seen[a.ID] = true
		for _, n := range []struct {
			key   string
			value int
		}{
			{".max_attempts", a.MaxAttempts},
			{".quota.limit", a.Quota.Limit},
			{".rate.requests_per_minute", a.Rate.RequestsPerMinute},
			{".rate.submits_per_minute", a.Rate.SubmitsPerMinute},
			{".rate.max_in_progress", a.Rate.MaxInProgress},
		} {
			if n.value < 1 {
				v.errorf(key+n.key, "must be positive")
			}
		}
		if _, err := ParseWindow(a.Quota.Window); err != nil {
			v.errorf(key+".quota.window", "%v", err)
		}
		if a.MaxAttempts > c.Limits.MaxAttempts && c.Limits.MaxAttempts > 0 {
			v.warnf(key+".max_attempts", "%d is above limits.max_attempts %d; each attempt costs model calls",
				a.MaxAttempts, c.Limits.MaxAttempts)
		}
		if a.Retention.Default != "none" {
			if d, err := time.ParseDuration(a.Retention.Default); err != nil || d <= 0 {
				v.errorf(key+".retention.default", "%q is neither none nor a positive duration", a.Retention.Default)
			}
		}
		if a.Documents.HTML != "" {
			v.oneOf(key+".documents.html", a.Documents.HTML, htmlPolicies)
		}
		if a.Documents.PDF.Mode != "" {
			v.oneOf(key+".documents.pdf.mode", a.Documents.PDF.Mode, pdfModes)
		}
		v.appJudge(key, a.Judge)
	}
}
