package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/kaanemec/vde/internal/config"
)

const defaultConfigPath = "vde.yaml"

const configUsage = `Usage: vde config check [--config path] [--strict] [--probe]

Validates the configuration without network calls and prints a summary of endpoints,
groups, roles and the media types each role can read. Exits 1 and lists every error when
the configuration is invalid. Secrets are never printed.

Flags:
`

// probes holds the reachability check of each provider adapter, keyed by provider name.
// Adapters add theirs here as they land; a probe makes one cheap request (a models list or
// a one-token completion) and never sends a document.
var probes = map[string]func(ctx context.Context, e *config.Endpoint) error{}

const probeTimeout = 15 * time.Second

func runConfig(args []string, stdout, stderr io.Writer) int {
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

	cfg, issues, err := loadConfig(*path, *strict)
	for _, i := range issues {
		fmt.Fprintln(stderr, i)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitFailure
	}
	printSummary(stdout, *path, cfg)
	if *probe {
		probeEndpoints(context.Background(), stdout, stderr, cfg)
	}
	return exitOK
}

// loadConfig loads and validates the configuration. Every command that needs a
// configuration (config check, serve, extract) goes through it and refuses to run when it
// returns an error.
func loadConfig(path string, strict bool) (*config.Config, config.Issues, error) {
	cfg, err := config.Load(path)
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
	for _, role := range []struct{ name, group string }{
		{"extractor", cfg.Roles.Extractor.Group},
		{"judge", cfg.Roles.Judge.Group},
	} {
		fmt.Fprintf(tw, "  %s: group %s\n", role.name, role.group)
		for _, c := range cfg.Capabilities(role.group) {
			via, pairs := c.Via, strings.Join(c.Pairs, ", ")
			if via == "" {
				via, pairs = "-", "cannot read"
			}
			fmt.Fprintf(tw, "    %s\t%s\t%s\n", c.MediaType, via, pairs)
		}
	}
	l := cfg.Limits
	fmt.Fprintf(tw, "\nlimits\n  max_attempts %d, job_timeout %s, judge_reparse_tries %d\n",
		l.MaxAttempts, l.JobTimeout, l.JudgeReparseTries)
	fmt.Fprintf(tw, "  document_bytes %d, page_bytes %d, pages %d\n", l.DocumentBytes, l.PageBytes, l.Pages)
	fmt.Fprintf(tw, "\napplications %d, documents.html %s\n", len(cfg.Applications), cfg.Documents.HTML)
	fmt.Fprintln(tw, "service_version: not computed yet (v0.1 epic 05, job 07)")
	tw.Flush()
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
