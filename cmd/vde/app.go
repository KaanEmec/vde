package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/kaanemec/vde/internal/audit"
	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/job"
)

// imageConfigPath is where the container image expects the configuration. The operator
// commands (app, token, healthcheck) fall back to it when --config is not given and
// vde.yaml does not exist, so `docker compose exec vde vde token …` and the image's
// HEALTHCHECK work without flags.
const imageConfigPath = "/etc/vde/vde.yaml"

const flagsHeading = "\nFlags:\n"

// operatorFlags registers --config on fs and returns a function giving the path to use
// after parsing.
func operatorFlags(set *flag.FlagSet) func() string {
	path := set.String("config", defaultConfigPath, "configuration file (falls back to "+imageConfigPath+")")
	return func() string {
		explicit := false
		set.Visit(func(f *flag.Flag) { explicit = explicit || f.Name == "config" })
		if explicit {
			return *path
		}
		if _, err := os.Stat(*path); errors.Is(err, fs.ErrNotExist) {
			if _, err := os.Stat(imageConfigPath); err == nil {
				return imageConfigPath
			}
		}
		return *path
	}
}

// loadOperatorConfig parses the configuration for the operator commands. They read only
// server, auth and applications, so the configuration is decoded (unknown keys and bad
// values are still errors) but not validated: provider keys need not be set.
func loadOperatorConfig(path string) (*config.Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return config.Parse(data, os.Getenv)
}

// openOperator loads the operator configuration at path and opens its database for the
// command name. On failure it prints the error (a database error after name) to stderr
// and reports false.
func openOperator(name, path string, stderr io.Writer) (*config.Config, *job.DB, bool) {
	cfg, err := loadOperatorConfig(path)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return nil, nil, false
	}
	store, err := openStore(context.Background(), cfg)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", name, err)
		return nil, nil, false
	}
	return cfg, store, true
}

// cliAudit is the recorder of the operator commands: the store, actor cli.
func cliAudit(store job.Store) *audit.Recorder {
	return audit.New(store, audit.Options{}).WithActor(audit.ActorCLI)
}

// storedApplication is the configuration entry that a store row stands for, with the
// fields that the service_version hashes.
func storedApplication(a job.Application) config.Application {
	return config.Application{
		ID: a.ID, MaxAttempts: a.MaxAttempts, Documents: config.AppDocuments{HTML: a.HTMLPolicy},
		Judge: config.AppJudge{
			Policy: config.JudgePolicy(a.JudgePolicy), AllowOverride: a.JudgeAllowOverride, SampleRate: a.JudgeSampleRate,
		},
	}
}

// syncApplications mirrors the configured applications into the store: each is
// upserted (every field of the entry, defaults applied, rewritten: the YAML wins over
// admin API changes), marked source config and enabled; every other enabled source config
// application is disabled (its tokens stop working, its jobs stay until they expire).
// Applications created over the admin API are left alone. Each change of state is
// audited through rec, actor config.
func syncApplications(ctx context.Context, store job.Store, rec *audit.Recorder, apps []config.Application, now time.Time) error {
	ids := make([]string, 0, len(apps))
	for _, a := range apps {
		prev, err := store.GetApplication(ctx, a.ID)
		if err != nil && !errors.Is(err, job.ErrNotFound) {
			return err
		}
		if err := store.UpsertApplication(ctx, job.ApplicationOf(a), now); err != nil {
			return err
		}
		if !prev.Enabled {
			if err := rec.Record(ctx, audit.ActorConfig, a.ID, "", audit.AppEnabled{}); err != nil {
				return err
			}
		}
		ids = append(ids, a.ID)
	}
	disabled, err := store.DisableApplicationsNotIn(ctx, ids, now)
	if err != nil {
		return err
	}
	for _, id := range disabled {
		if err := rec.Record(ctx, audit.ActorConfig, id, "", audit.AppDisabled{}); err != nil {
			return err
		}
	}
	return nil
}

const appUsage = `Usage: vde app list [--config path]

Lists the applications in the database (mirrored from the configuration by vde serve, or
created over the admin API): id, name, source, enabled, attempt limit, quota, rate limits,
retention and active tokens.
`

func runApp(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "list" {
		fmt.Fprint(stderr, appUsage)
		return exitUsage
	}
	set := newFlagSet("vde app list", appUsage+flagsHeading, stderr)
	configPath := operatorFlags(set)
	if code, done := parseArgs(set, args[1:], stderr); done {
		return code
	}
	_, store, ok := openOperator(set.Name(), configPath(), stderr)
	if !ok {
		return exitFailure
	}
	defer store.Close()
	if err := listApplications(context.Background(), stdout, store, time.Now()); err != nil {
		fmt.Fprintf(stderr, "vde app list: %v\n", err)
		return exitFailure
	}
	return exitOK
}

func listApplications(ctx context.Context, w io.Writer, store job.Store, now time.Time) error {
	apps, err := store.ListApplications(ctx)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tSOURCE\tENABLED\tATTEMPTS\tQUOTA\tREQ/MIN\tSUBMITS/MIN\tIN PROGRESS\tRETENTION\tTOKENS")
	for _, a := range apps {
		tokens, err := store.ListTokens(ctx, a.ID)
		if err != nil {
			return err
		}
		active := 0
		for _, t := range tokens {
			if t.Status(now) == "active" {
				active++
			}
		}
		retention := a.RetentionDefault
		if a.ForceNone {
			retention = "none (forced)"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%t\t%d\t%d/%s\t%d\t%d\t%d\t%s\t%d\n", a.ID, dash(a.Name), dash(a.Source), a.Enabled, a.MaxAttempts,
			a.QuotaLimit, a.QuotaWindow, a.RequestsPerMinute, a.SubmitsPerMinute, a.MaxInProgress, retention, active)
	}
	return tw.Flush()
}

func dash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}
