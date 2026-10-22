package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/extract"
	"github.com/kaanemec/vde/internal/provider"
)

// probe runs extract.Probe once per role and endpoint-model pair of the role's groups
// (extract.RoleGroups: escalation, second judge and feedback groups included), each pair
// on its own with one try bounded by its endpoint's timeout, and writes one line per
// pair: never the answer text. It returns the number of pairs that failed (unparseable,
// provider failure or no adapter); a judge that misses the wrong value is a warning.
func probe(ctx context.Context, cfg *config.Config, build func(*config.Endpoint) (provider.Provider, error), w io.Writer) (failed int) {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "\nprobe: one request per role and pair, the embedded sample; answers are not printed")
	providers := map[string]provider.Provider{}
	extractorGroups, judgeGroups := extract.RoleGroups(cfg)
	pairs := 0
	for _, role := range []struct {
		name   string
		groups []string
	}{{roleExtractor, extractorGroups}, {roleJudge, judgeGroups}} {
		seen := map[string]bool{}
		for _, group := range role.groups {
			for _, entry := range cfg.Groups[group].Endpoints {
				pair := entry.Endpoint + "/" + entry.Model
				if seen[pair] {
					continue
				}
				seen[pair] = true
				pairs++
				e := cfg.Endpoints[entry.Endpoint]
				p, ok := providers[entry.Endpoint]
				if !ok {
					var err error
					if p, err = build(e); err != nil {
						failed++
						fmt.Fprintf(tw, "  %s\t%s\t-\t-\tno adapter: %v\n", role.name, pair, err)
						continue
					}
					providers[entry.Endpoint] = p
				}
				callCtx, cancel := context.WithTimeout(ctx, cmp.Or(e.Timeout, cfg.Limits.JobTimeout))
				r := extract.Probe(callCtx, role.name, p, entry.Model, e.Models[entry.Model].OutputTokens())
				cancel()
				if !r.OK() {
					failed++
				}
				fmt.Fprintf(tw, "  %s\t%s\t%s\t%d in, %d out\t%s\n", role.name, pair,
					r.Latency.Round(time.Millisecond), r.InputTokens, r.OutputTokens, probeLine(r))
			}
		}
	}
	fmt.Fprintf(tw, "probe: %d of %d pairs failed\n", failed, pairs)
	tw.Flush()
	return failed
}

// probeLine describes a probe result without content: the parser's error names a
// position, a provider failure its class and HTTP status.
func probeLine(r extract.ProbeResult) string {
	out := string(r.Status)
	switch r.Status {
	case extract.ProbeUnparseable:
		out += ": " + r.Err.Error()
	case extract.ProbeFailed:
		out = provider.Classify(r.Err).String() + " provider failure"
		var pe *provider.Error
		switch {
		case errors.As(r.Err, &pe) && pe.Status != 0:
			out += fmt.Sprintf(" (status %d)", pe.Status)
		case errors.Is(r.Err, context.DeadlineExceeded):
			out += " (timeout)"
		}
	case extract.ProbeAcceptsWrong, extract.ProbeRejectsElsewhere:
		out = "warning: " + out
	}
	if r.Truncated {
		out += " (answer cut off at the output-token limit)"
	}
	return out
}
