package main

import (
	"context"
	"flag"
	"fmt"
	"io"
)

const migrateUsage = `Usage: vde migrate [--config path]

Applies the pending schema migrations of storage.driver (SQLite at
server.data_dir/vde.db, or PostgreSQL at the DSN in storage.dsn_env) and prints the
schema version. vde serve applies them at start as well; run vde migrate before rolling
a new version out to several replicas, or from a deploy job, to migrate explicitly.
Concurrent runs are safe: each migration is applied once. The configuration is decoded
but not validated, so provider keys need not be set.
`

func runMigrate(args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("vde migrate", flag.ContinueOnError)
	set.SetOutput(stderr)
	set.Usage = func() {
		fmt.Fprint(stderr, migrateUsage+flagsHeading)
		set.PrintDefaults()
	}
	configPath := operatorFlags(set)
	if code, done := parseFlags(set, args); done {
		return code
	}
	if set.NArg() > 0 {
		fmt.Fprintf(stderr, "vde migrate: unexpected argument %q\n", set.Arg(0))
		return exitUsage
	}
	cfg, err := loadOperatorConfig(configPath())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitFailure
	}
	ctx := context.Background()
	store, err := openStore(ctx, cfg)
	if err != nil {
		fmt.Fprintf(stderr, "vde migrate: %v\n", err)
		return exitFailure
	}
	defer store.Close()
	v, err := store.SchemaVersion(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "vde migrate: %v\n", err)
		return exitFailure
	}
	fmt.Fprintf(stdout, "storage %s: schema version %d\n", cfg.Storage.Driver, v)
	return exitOK
}
