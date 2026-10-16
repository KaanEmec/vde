package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"slices"
	"text/tabwriter"
	"time"

	"github.com/kaanemec/vde/internal/audit"
	"github.com/kaanemec/vde/internal/job"
)

const auditUsage = `Usage: vde audit list [--app id] [--since 24h|7d|RFC 3339] [--kind kind] [--limit n] [--config path]

Prints the audit log (the audit_events table in server.data_dir/vde.db), oldest first:
time, kind, application, actor, job and the kind's fixed details (never content). --since
is a duration back from now or a time, the last 24 hours by default; --limit caps the
rows (default 100, 0 for all). Kinds: token.created, token.revoked, app.enabled,
app.disabled, job.created, job.finished, job.deleted, job.expired, auth.failed,
config.loaded. The admin API serves the same listing at GET /admin/v1/audit.
`

func runAudit(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "list" {
		if len(args) > 0 && (args[0] == "-h" || args[0] == "help" || args[0] == "--help") {
			fmt.Fprint(stdout, auditUsage)
			return exitOK
		}
		fmt.Fprint(stderr, auditUsage)
		return exitUsage
	}
	set := flag.NewFlagSet("vde audit list", flag.ContinueOnError)
	set.SetOutput(stderr)
	set.Usage = func() {
		fmt.Fprint(stderr, auditUsage+flagsHeading)
		set.PrintDefaults()
	}
	configPath := operatorFlags(set)
	app := set.String("app", "", "only this application's events")
	since := set.String("since", "24h", "events at or after: a duration back from now (24h, 7d) or an RFC 3339 time")
	kind := set.String("kind", "", "only events of this kind")
	limit := set.Int("limit", 100, "at most this many events (0 for all)")
	if code, done := parseFlags(set, args[1:]); done {
		return code
	}
	if set.NArg() > 0 {
		fmt.Fprintf(stderr, "vde audit list: unexpected argument %q\n", set.Arg(0))
		return exitUsage
	}
	now := time.Now()
	from, err := audit.ParseSince(*since, now)
	if err != nil {
		fmt.Fprintf(stderr, "vde audit list: %v\n", err)
		return exitUsage
	}
	if *kind != "" && !slices.Contains(audit.Kinds(), audit.Kind(*kind)) {
		fmt.Fprintf(stderr, "vde audit list: unknown kind %q\n", *kind)
		return exitUsage
	}
	if *limit < 0 {
		fmt.Fprintln(stderr, "vde audit list: --limit must not be negative")
		return exitUsage
	}
	cfg, err := loadOperatorConfig(configPath())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitFailure
	}
	store, err := openStore(cfg)
	if err != nil {
		fmt.Fprintf(stderr, "vde audit list: %v\n", err)
		return exitFailure
	}
	defer store.Close()
	events, err := store.ListAudit(context.Background(), job.AuditFilter{ApplicationID: *app, Kind: *kind, Since: from}, *limit)
	if err != nil {
		fmt.Fprintf(stderr, "vde audit list: %v\n", err)
		return exitFailure
	}
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "AT\tKIND\tAPP\tACTOR\tJOB\tDETAILS")
	for _, e := range events {
		details := string(e.Details)
		if details == "" {
			details = "{}"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", e.At.UTC().Format(time.RFC3339), e.Kind,
			dash(e.ApplicationID), dash(e.Actor), dash(e.JobID), details)
	}
	if err := tw.Flush(); err != nil {
		fmt.Fprintf(stderr, "vde audit list: %v\n", err)
		return exitFailure
	}
	return exitOK
}
