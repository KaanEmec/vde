package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"runtime"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/extract"
	"github.com/kaanemec/vde/internal/ground"
	"github.com/kaanemec/vde/internal/pdf"
	"github.com/kaanemec/vde/internal/version"
)

const defaultConfigPath = "vde.yaml"

const configUsage = `Usage: vde config check [--config path] [--strict] [--probe]
       vde config schema

check validates the configuration without network calls, then against the JSON Schema,
and prints a summary of endpoints, groups, roles and the media types each role can read.
Exits 1 and lists every error and schema violation when the configuration is invalid.
Secrets are never printed.

schema prints the JSON Schema of the configuration file (draft 2020-12) for editors.

Flags of check:
`

// probes holds the reachability check of each provider adapter, keyed by provider name.
// Adapters add theirs here as they land; a probe makes one cheap request (a models list or
// a one-token completion) and never sends a document.
var probes = map[string]func(ctx context.Context, e *config.Endpoint) error{}

const probeTimeout = 15 * time.Second

func runConfig(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "schema" {
		if len(args) > 1 {
			fmt.Fprintf(stderr, "vde config schema: unexpected argument %q\n", args[1])
			return exitUsage
		}
		if _, err := stdout.Write(config.Schema()); err != nil {
			fmt.Fprintln(stderr, err)
			return exitFailure
		}
		return exitOK
	}
	if len(args) == 0 || args[0] != "check" {
		fmt.Fprint(stderr, configUsage)
		return exitUsage
	}
	fs := flag.NewFlagSet("vde config check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprint(stderr, configUsage)
		fs.PrintDefaults()
	}
	path := fs.String("config", defaultConfigPath, "configuration file")
	strict := fs.Bool("strict", false, "treat warnings as errors")
	probe := fs.Bool("probe", false, "also check that each endpoint is reachable (warnings only)")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "vde config check: unexpected argument %q\n", fs.Arg(0))
		return exitUsage
	}

	cfg, issues, err := loadConfigEnv(*path, *strict, os.Getenv)
	for _, i := range issues {
		fmt.Fprintln(stderr, i)
	}
	decodeFailed := err != nil && len(issues) == 0 // unreadable, not YAML, unknown key, wrong type
	if decodeFailed {
		fmt.Fprintln(stderr, err)
	}
	// The schema's violations come after Validate's, whether or not Validate passed. A
	// file that cannot be read or is not YAML fails loadConfigEnv already.
	violations, serr := schemaViolations(*path)
	for _, v := range violations {
		fmt.Fprintf(stderr, "error: schema: %s: %s\n", v.Key, v.Message)
	}
	if err != nil {
		if !decodeFailed {
			fmt.Fprintln(stderr, err)
		}
		return exitFailure
	}
	if serr != nil {
		fmt.Fprintln(stderr, serr)
		return exitFailure
	}
	if len(violations) > 0 {
		fmt.Fprintf(stderr, "config: %s has %d schema violation(s)\n", *path, len(violations))
		return exitFailure
	}
	printSummary(stdout, *path, cfg)
	if *probe {
		probeEndpoints(context.Background(), stdout, stderr, cfg)
	}
	return exitOK
}

// loadConfigEnv loads and validates the configuration, resolving the endpoint keys
// through getenv. Every command that needs a configuration (config check, serve, extract,
// eval) goes through it and refuses to run when it returns an error.
func loadConfigEnv(path string, strict bool, getenv func(string) string) (*config.Config, config.Issues, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("config: %w", err)
	}
	cfg, err := config.Parse(data, getenv)
	if err != nil {
		return nil, nil, err
	}
	issues := cfg.Validate()
	if issues.Err(strict) != nil {
		n := 0
		for _, i := range issues {
			if !i.Warning || strict {
				n++
			}
		}
		return nil, issues, fmt.Errorf("config: %s has %d blocking problem(s)", path, n)
	}
	return cfg, issues, nil
}

// schemaViolations validates the file at path against the embedded JSON Schema. Only
// config check calls it: serve and extract rely on Validate alone.
func schemaViolations(path string) (config.Issues, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return config.CheckSchema(data)
}

func printSummary(w io.Writer, path string, cfg *config.Config) {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintf(tw, "configuration %s: ok\n\nendpoints\n", path)
	for _, name := range slices.Sorted(maps.Keys(cfg.Endpoints)) {
		e := cfg.Endpoints[name]
		key := "none"
		if e.APIKeyEnv != "" {
			key = e.APIKeyEnv + " (set)" // Validate guarantees a named variable is set
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\tkey %s\ttimeout %s\n", name, e.Provider, e.BaseURL, key, e.Timeout)
		for _, model := range slices.Sorted(maps.Keys(e.Models)) {
			fmt.Fprintf(tw, "    %s\t%s\n", model, strings.Join(e.Models[model].Accepts, ", "))
		}
	}
	fmt.Fprintln(tw, "\ngroups")
	for _, name := range slices.Sorted(maps.Keys(cfg.Groups)) {
		g := cfg.Groups[name]
		pairs := make([]string, 0, len(g.Endpoints))
		for _, entry := range g.Endpoints {
			pairs = append(pairs, entry.Endpoint+"/"+entry.Model)
		}
		fmt.Fprintf(tw, "  %s\t%s\tfailover %d tries, %s\n",
			name, strings.Join(pairs, " -> "), g.Failover.MaxTries, g.Failover.MaxTotalTime)
	}
	fmt.Fprintln(tw, "\nroles")
	extractorCaps, judgeCaps := extract.RoleCapabilities(cfg)
	for _, role := range []struct {
		name, group string
		escalation  []config.Escalation
		caps        []config.Capability
	}{
		{"extractor", cfg.Roles.Extractor.Group, cfg.Roles.Extractor.Escalation, extractorCaps},
		{"judge", cfg.Roles.Judge.Group, cfg.Roles.Judge.Escalation, judgeCaps},
	} {
		fmt.Fprintf(tw, "  %s: group %s\n", role.name, role.group)
		for _, e := range role.escalation {
			fmt.Fprintf(tw, "    escalation: from attempt %d, group %s\n", e.FromAttempt, e.Group)
		}
		if role.name == "judge" {
			fmt.Fprintf(tw, "    policy: %s\n", cfg.RolePolicy())
			fmt.Fprintf(tw, "    grounding: rules %s, one repair before the judge (auto; always with an annotated schema)\n", ground.RulesVersion)
			if g := cfg.Roles.SecondJudge.Group; g != "" {
				fmt.Fprintf(tw, "    second judge: group %s\n", g)
			}
			if cfg.Roles.Judge.FeedbackFallback != "" || judgeHasClassifier(cfg) {
				fallback := cmp.Or(cfg.Roles.Judge.FeedbackFallback, config.FeedbackWholeResult)
				if fallback == config.FeedbackLLM {
					fallback += ", group " + cfg.Roles.Judge.FeedbackGroup
				}
				fmt.Fprintf(tw, "    classifier feedback fallback: %s\n", fallback)
			}
		}
		// What every group of the role reads (escalation, second judge and feedback
		// groups included): the representation chosen at submit.
		for _, c := range role.caps {
			via, pairs := c.Via, strings.Join(c.Pairs, ", ")
			if via == "" {
				via, pairs = "-", "cannot read"
			}
			fmt.Fprintf(tw, "    %s\t%s\t%s\n", c.MediaType, via, pairs)
		}
		fmt.Fprintf(tw, "    pdf: %s\n", pdfSummary(cfg.Documents.PDF, role.caps))
	}
	l := cfg.Limits
	fmt.Fprintf(tw, "\nlimits\n  max_attempts %d, job_timeout %s, judge_reparse_tries %d\n",
		l.MaxAttempts, l.JobTimeout, l.JudgeReparseTries)
	fmt.Fprintf(tw, "  document_bytes %d, page_bytes %d, pages %d\n", l.DocumentBytes, l.PageBytes, l.Pages)
	fmt.Fprintf(tw, "\napplications %d, documents.html %s\n", len(cfg.Applications), cfg.Documents.HTML)
	if p := cfg.Documents.PDF; p.Mode != config.PDFModeNative {
		fmt.Fprintf(tw, "documents.pdf: mode %s, dpi %d, max_pages %d, min_text_chars_per_page %d, workers %d "+
			"(about %d MB of memory for pdfium when it runs)\n", p.Mode, p.DPI, p.MaxPages, p.MinTextCharsPerPage, p.Workers, p.Workers*500)
		if !pdf.Compiled() {
			fmt.Fprintf(tw, "warning: documents.pdf: no WebAssembly compiler on %s/%s; pdfium is interpreted, about 60 times slower\n",
				runtime.GOOS, runtime.GOARCH)
		}
	}
	for _, a := range cfg.Applications {
		line := judgeSummary(cfg, a)
		if m := a.Documents.PDF.Mode; m != "" {
			line += ", documents.pdf.mode " + m
		}
		fmt.Fprintf(tw, "  %s: %s\n", a.ID, line)
	}
	fmt.Fprintf(tw, "%s\n", storageSummary(cfg))
	rate := "per process"
	if cfg.Rate.Shared {
		rate = "shared (job store)"
	}
	fmt.Fprintf(tw, "worker size %d, poll_interval %s, lease %s; rate limits %s\n",
		cfg.Worker.Size, cfg.Worker.PollInterval, cfg.Worker.Lease, rate)
	fmt.Fprintf(tw, "service_version: %s\n", extract.ServiceVersion(cfg, version.Version))
	tw.Flush()
}

// pdfSummary says how a role's groups get a PDF: natively, or else under
// documents.pdf.mode (ADR 0010).
func pdfSummary(p config.PDF, caps []config.Capability) string {
	text, images := false, false
	for _, c := range caps {
		switch {
		case c.MediaType == "application/pdf" && c.Via == config.ViaNative:
			return "sent as given (a pair reads application/pdf)"
		case c.MediaType == "application/pdf":
			images = c.Via == config.ViaPages
		case c.MediaType == "text/plain":
			text = c.Via == config.ViaText
		}
	}
	rendered := fmt.Sprintf("pages rendered at %d dpi", p.DPI)
	var out string
	switch p.Mode {
	case config.PDFModeNative:
		out = "only caller pages; PDFs without them are refused"
	case config.PDFModeText:
		out = "text layer"
	case config.PDFModePages:
		out = rendered
	default:
		out = fmt.Sprintf("text layer when every page has %d characters, else %s", p.MinTextCharsPerPage, rendered)
	}
	out += " (documents.pdf.mode: " + p.Mode + ")"
	if (p.Mode == config.PDFModeText && !text) || (p.Mode == config.PDFModePages && !images) ||
		(p.Mode == config.PDFModeAuto && !text && !images) {
		out += "; this group cannot read it"
	}
	return out
}

// judgeSummary describes an application's judge policy: the effective policy, whether a
// request may override it, and the sample rate when auto can run.
func judgeSummary(cfg *config.Config, a config.Application) string {
	p := cfg.JudgePolicyOf(a)
	out := "judge policy " + p.String()
	if a.Judge.Policy == "" {
		out += " (roles.judge.policy)"
	}
	if a.Judge.AllowOverride {
		out += ", override allowed"
	} else {
		out += ", override not allowed"
	}
	if p == config.JudgeAuto || a.Judge.AllowOverride {
		out += fmt.Sprintf(", sample rate %g", a.Judge.Rate())
	}
	return out
}

// judgeHasClassifier reports whether a group serving the judge role has a classifier
// endpoint, whose rejections roles.judge.feedback_fallback concerns.
func judgeHasClassifier(cfg *config.Config) bool {
	_, groups := extract.RoleGroups(cfg)
	for _, name := range groups {
		if g := cfg.Groups[name]; g != nil {
			for _, entry := range g.Endpoints {
				if e := cfg.Endpoints[entry.Endpoint]; e != nil && e.Provider == config.ProviderClassifier {
					return true
				}
			}
		}
	}
	return false
}

// probeEndpoints reports the reachability of every endpoint as warnings; it never fails
// the check.
func probeEndpoints(ctx context.Context, stdout, stderr io.Writer, cfg *config.Config) {
	for _, name := range slices.Sorted(maps.Keys(cfg.Endpoints)) {
		e := cfg.Endpoints[name]
		probe, ok := probes[e.Provider]
		if !ok {
			fmt.Fprintf(stderr, "warning: endpoints.%s: probe not available for provider %q yet\n", name, e.Provider)
			continue
		}
		pctx, cancel := context.WithTimeout(ctx, probeTimeout)
		err := probe(pctx, e)
		cancel()
		if err != nil {
			fmt.Fprintf(stderr, "warning: endpoints.%s: unreachable: %v\n", name, err)
			continue
		}
		fmt.Fprintf(stdout, "probe: endpoints.%s: reachable\n", name)
	}
}
