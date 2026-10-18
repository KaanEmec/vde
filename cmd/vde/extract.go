package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/document"
	"github.com/kaanemec/vde/internal/extract"
	"github.com/kaanemec/vde/internal/logging"
	"github.com/kaanemec/vde/internal/pdf"
	"github.com/kaanemec/vde/internal/provider"
	"github.com/kaanemec/vde/internal/provider/fake"
	"github.com/kaanemec/vde/internal/version"
)

const extractUsage = `Usage: vde extract --document file --extractor-instructions file --judge-instructions file
                   [--config path] [--type media/type] [--page image]... [--schema file]
                   [--max-attempts n] [--html raw_html|visible_text] [--timeout d]
                   [--judge always|never|auto] [--provider-script file] [--verbose]

Runs one extraction with the configured extractor and judge groups and prints the
job-shaped result as JSON on stdout. The media type defaults from the file extension
(.txt .md .html .eml .pdf .png .jpg .webp .heic); --page adds a page image, in order, for
models that read PDFs as images. Without --schema the extraction is schema-less.
--provider-script replaces every endpoint's provider with one fake provider replaying the
script (a fake script or a testdata/scenarios file), so no key is needed or used.
--verbose writes one line per attempt and per model call to stderr, never content. The
result's usage counts the provider calls of the run (failed ones included) and the input
and output tokens the providers reported.

--judge sets the judge policy of the run (default: roles.judge.policy); the result's
verification says what accepted it: judge, grounded or none. Under auto the sample of
grounded runs judged anyway (5 %) is decided on the document's bytes, so a rerun of the
same document is sampled the same way.

Exit codes: 0 accepted, 2 rejected, 3 failed, 4 extracted (no judge asked, judge policy
never), 1 usage or configuration error.

Flags:
`

// mediaTypesByExtension are the document types inferred when --type is absent.
var mediaTypesByExtension = map[string]string{
	".txt":      "text/plain",
	".text":     "text/plain",
	".md":       "text/markdown",
	".markdown": "text/markdown",
	".html":     "text/html",
	".htm":      "text/html",
	".eml":      "message/rfc822",
	".pdf":      "application/pdf",
	".png":      "image/png",
	".jpg":      "image/jpeg",
	".jpeg":     "image/jpeg",
	".webp":     "image/webp",
	".heic":     "image/heic",
}

// pageFlags collects repeated --page values.
type pageFlags []string

func (p *pageFlags) String() string     { return strings.Join(*p, ",") }
func (p *pageFlags) Set(v string) error { *p = append(*p, v); return nil }

type extractOptions struct {
	config, document, mediaType string
	pages                       pageFlags
	extractorInstructions       string
	judgeInstructions           string
	schema                      string
	maxAttempts                 int
	html                        string
	judge                       string
	timeout                     time.Duration
	providerScript              string
	verbose                     bool
	// run is the loop; nil means extract.Run (tests observe the input through it).
	run func(context.Context, extract.Input, extract.Roles) extract.Outcome
}

// attachment is an email attachment listed in the result (metadata only).
type attachment struct {
	Name      string `json:"name"`
	MediaType string `json:"media_type"`
	Bytes     int64  `json:"bytes"`
}

// jobOutput is the job resource's fields that apply to one CLI run (contract §5).
type jobOutput struct {
	Status          extract.Status     `json:"status"`
	Result          json.RawMessage    `json:"result"`
	Feedback        []extract.Feedback `json:"feedback"`
	Attempts        int                `json:"attempts"`
	Usage           extract.Usage      `json:"usage"` // every provider call of the run, failed ones included
	ServiceVersion  string             `json:"service_version"`
	JudgeConfidence *float64           `json:"judge_confidence"`
	Error           *extract.JobError  `json:"error"`
	Verification    string             `json:"verification"`
	Attachments     []attachment       `json:"attachments,omitempty"`
	FinishedAt      time.Time          `json:"finished_at"`
}

func runExtract(args []string, stdout, stderr io.Writer) int {
	var o extractOptions
	fs := flag.NewFlagSet("vde extract", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprint(stderr, extractUsage)
		fs.PrintDefaults()
	}
	fs.StringVar(&o.config, "config", defaultConfigPath, "configuration file")
	fs.StringVar(&o.document, "document", "", "the document file (required)")
	fs.StringVar(&o.mediaType, "type", "", "the document's media type (default: from the file extension)")
	fs.Var(&o.pages, "page", "a page image (PNG or JPEG); repeat in page order")
	fs.StringVar(&o.extractorInstructions, "extractor-instructions", "", "extractor instructions file (required)")
	fs.StringVar(&o.judgeInstructions, "judge-instructions", "", "judge instructions file (required)")
	fs.StringVar(&o.schema, "schema", "", "JSON Schema file (default: schema-less)")
	fs.IntVar(&o.maxAttempts, "max-attempts", 0, "attempt limit, at most limits.max_attempts (default: limits.max_attempts)")
	fs.StringVar(&o.html, "html", "", "HTML policy, raw_html or visible_text (default: documents.html)")
	fs.DurationVar(&o.timeout, "timeout", 0, "job time limit (default: limits.job_timeout)")
	fs.StringVar(&o.judge, "judge", "", "judge policy, always, never or auto (default: roles.judge.policy)")
	fs.StringVar(&o.providerScript, "provider-script", "", "replace every provider with the fake replaying this script")
	fs.BoolVar(&o.verbose, "verbose", false, "progress lines on stderr (never content)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "vde extract: unexpected argument %q\n", fs.Arg(0))
		return exitUsage
	}
	for _, req := range []struct{ name, value string }{
		{"--document", o.document},
		{"--extractor-instructions", o.extractorInstructions},
		{"--judge-instructions", o.judgeInstructions},
	} {
		if req.value == "" {
			fmt.Fprintf(stderr, "vde extract: %s is required\n", req.name)
			return exitUsage
		}
	}

	if o.judge != "" {
		if _, err := config.ParseJudgePolicy(o.judge); err != nil {
			fmt.Fprintf(stderr, "vde extract: --judge: %v\n", err)
			return exitUsage
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	out, err := extractFile(ctx, o, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "vde extract: %v\n", err)
		return exitUsage
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		fmt.Fprintf(stderr, "vde extract: write result: %v\n", err)
		return exitFailed
	}
	switch out.Status {
	case extract.StatusAccepted:
		return exitOK
	case extract.StatusRejected:
		return exitRejected
	case extract.StatusExtracted:
		return exitExtracted
	}
	return exitFailed
}

// extractFile prepares and runs one job. Its errors are usage or configuration errors:
// once the loop runs, every outcome, failures included, is a jobOutput.
func extractFile(ctx context.Context, o extractOptions, stderr io.Writer) (jobOutput, error) {
	getenv := os.Getenv
	var script provider.Provider
	if o.providerScript != "" {
		// A scenario file holds the script as its first YAML document; fake.Load reads
		// only that one.
		f, err := fake.Load(o.providerScript)
		if err != nil {
			return jobOutput{}, err
		}
		script = f
		// No endpoint is called, so a key the configuration names need not be set.
		getenv = func(k string) string {
			if v := os.Getenv(k); v != "" {
				return v
			}
			return "unused-with-provider-script"
		}
	}
	cfg, issues, err := loadConfigEnv(o.config, false, getenv)
	for _, i := range issues {
		fmt.Fprintln(stderr, i)
	}
	if err != nil {
		return jobOutput{}, err
	}

	judgePolicy := cfg.RolePolicy()
	if o.judge != "" {
		if judgePolicy, err = config.ParseJudgePolicy(o.judge); err != nil {
			return jobOutput{}, fmt.Errorf("--judge: %w", err)
		}
	}
	logger := newLogger(stderr, cfg.Logging)
	vcfg := *cfg
	vcfg.Roles.Judge.Policy = judgePolicy
	sv := extract.ServiceVersion(&vcfg, version.Version)
	logger.Info("vde extract", "service_version", sv, "version", version.Version)

	maxAttempts := cfg.Limits.MaxAttempts
	if o.maxAttempts != 0 {
		if o.maxAttempts < 1 || o.maxAttempts > cfg.Limits.MaxAttempts {
			return jobOutput{}, fmt.Errorf("--max-attempts %d: must be from 1 to limits.max_attempts (%d)", o.maxAttempts, cfg.Limits.MaxAttempts)
		}
		maxAttempts = o.maxAttempts
	}
	timeout := cfg.Limits.JobTimeout
	if o.timeout != 0 {
		if o.timeout < 0 {
			return jobOutput{}, fmt.Errorf("--timeout %s: must be positive", o.timeout)
		}
		timeout = o.timeout
	}
	policy := document.HTMLPolicy(cfg.Documents.HTML)
	if o.html != "" {
		if policy, err = document.ResolveHTMLPolicy(cfg.Documents.HTML, o.html); err != nil {
			return jobOutput{}, fmt.Errorf("--html: %w", err)
		}
	}

	doc, err := readDocument(o, cfg.Limits)
	if err != nil {
		return jobOutput{}, err
	}
	extractorInstructions, err := os.ReadFile(o.extractorInstructions)
	if err != nil {
		return jobOutput{}, err
	}
	judgeInstructions, err := os.ReadFile(o.judgeInstructions)
	if err != nil {
		return jobOutput{}, err
	}
	var schema *extract.Schema
	if o.schema != "" {
		b, err := os.ReadFile(o.schema)
		if err != nil {
			return jobOutput{}, err
		}
		if schema, err = extract.CompileSchema(b); err != nil {
			return jobOutput{}, fmt.Errorf("%s: %w", o.schema, err)
		}
	}
	pdfEngine, err := newPDFEngine(cfg, cliPDFCacheDir())
	if err != nil {
		return jobOutput{}, err
	}
	defer func() { _ = pdfEngine.Close() }()
	rep, err := selectRepresentation(ctx, cfg, doc, policy, pdfEngine, judgePolicy)
	if err != nil {
		return jobOutput{}, err
	}

	var progress io.Writer
	if o.verbose {
		progress = stderr
	}
	obs := &observer{w: progress}
	roles, err := extract.NewRoles(cfg, providerFactory(cfg, script, obs))
	if err != nil {
		return jobOutput{}, err
	}
	obs.logf("document %s, %s representation, %d part(s), schema %t, max %d attempt(s), timeout %s",
		doc.BaseType(), rep.Kind, len(rep.Parts), schema != nil, maxAttempts, timeout)

	run := o.run
	if run == nil {
		run = extract.Run
	}
	res := run(ctx, extract.Input{
		Representation:        rep,
		ExtractorInstructions: string(extractorInstructions),
		JudgeInstructions:     string(judgeInstructions),
		Schema:                schema,
		MaxAttempts:           maxAttempts,
		JudgePolicy:           judgePolicy,
		SampleRate:            config.DefaultSampleRate,
		SampleKey:             doc.Data,
		Deadline:              time.Now().Add(timeout),
		OnAttempt:             func(p extract.Progress) { obs.logf("attempt %d: %s", p.Attempts, describeProgress(p)) },
		Logger:                logger,
	}, roles)

	calls, in, outTokens := obs.usage()
	summary := string(res.Status) + ", verification " + res.Verification
	if res.Error != nil {
		summary += " (" + res.Error.Code + ")"
	}
	obs.logf("attempt %d: %s", res.Attempts, summary)
	obs.logf("usage: %d call(s), %d input tokens, %d output tokens", calls, in, outTokens)

	out := jobOutput{
		Status:          res.Status,
		Result:          res.Result,
		Feedback:        res.Feedback,
		Attempts:        res.Attempts,
		Usage:           extract.Usage{InputTokens: in, OutputTokens: outTokens, Calls: calls},
		ServiceVersion:  sv,
		JudgeConfidence: res.JudgeConfidence,
		Error:           res.Error,
		Verification:    res.Verification,
		FinishedAt:      time.Now().UTC(),
	}
	for _, a := range doc.Attachments {
		out.Attachments = append(out.Attachments, attachment{Name: a.Filename, MediaType: a.MediaType, Bytes: a.Size})
	}
	return out, nil
}

// selectRepresentation chooses what both roles read as the service does: the image and
// PDF policies, the PDF prepared with engine when no group reads it natively. Under
// judge policy never and auto it also reads the grounding text of a PDF sent natively or
// as pages; under always it does not, so pdfium never starts for PDF-capable models.
func selectRepresentation(ctx context.Context, cfg *config.Config, doc *document.Document, html document.HTMLPolicy,
	engine *pdf.Engine, judgePolicy config.JudgePolicy) (document.Representation, error) {
	// Every group that may serve a role (escalation, second judge, feedback group) must
	// read the representation.
	extractorCaps, judgeCaps := extract.RoleCapabilities(cfg)
	pdfPolicy := document.NewPDFPolicy(cfg.Documents.PDF, "", document.PDFEngineOf(engine))
	rep, err := document.SelectContext(ctx, doc, extractorCaps, judgeCaps, html, document.NewImagePolicy(cfg.Documents.Images), pdfPolicy)
	if err != nil {
		return document.Representation{}, err
	}
	if judgePolicy != config.JudgeAlways {
		if rep.GroundText, rep.Groundable, err = document.GroundingText(ctx, doc, rep, pdfPolicy); err != nil {
			return document.Representation{}, err
		}
	}
	return rep, nil
}

// readDocument reads the document and its pages and validates them like the service.
func readDocument(o extractOptions, limits config.Limits) (*document.Document, error) {
	mediaType := o.mediaType
	if mediaType == "" {
		ext := strings.ToLower(filepath.Ext(o.document))
		if mediaType = mediaTypesByExtension[ext]; mediaType == "" {
			return nil, fmt.Errorf("--type is required: no media type known for extension %q", ext)
		}
	}
	data, err := os.ReadFile(o.document)
	if err != nil {
		return nil, err
	}
	in := document.Input{MediaType: mediaType, Data: data, Filename: filepath.Base(o.document)}
	for _, p := range o.pages {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		in.Pages = append(in.Pages, document.Page{MediaType: mediaTypesByExtension[strings.ToLower(filepath.Ext(p))], Data: b})
	}
	return document.New(in, limits)
}

// describeProgress says why an attempt did not end the job, from counts only.
func describeProgress(p extract.Progress) string {
	judge, schema, grounding := 0, 0, 0
	for _, f := range p.Feedback {
		switch f.Source {
		case extract.SourceJudge:
			judge++
		case extract.SourceGrounding:
			grounding++
		default:
			schema++
		}
	}
	switch {
	case judge > 0:
		return fmt.Sprintf("judge rejected the candidate, %d feedback entries", judge)
	case grounding > 0 && schema == 0:
		return fmt.Sprintf("values not found in the document, %d feedback entries, judge not called (grounding repair)", grounding)
	case schema > 0:
		return fmt.Sprintf("answer not JSON or not valid against the schema, %d feedback entries, judge not called", schema)
	}
	return "no usable answer"
}

// newLogger returns the redacting logger (internal/logging: a fixed key allow-list and
// value truncation) on w at the configured level and format.
func newLogger(w io.Writer, l config.Logging) *slog.Logger {
	var level slog.Level
	if err := level.UnmarshalText([]byte(l.Level)); err != nil {
		level = slog.LevelInfo
	}
	return logging.New(w, level, l.Format)
}
