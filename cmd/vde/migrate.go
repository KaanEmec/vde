package main

import (
	"context"
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
	set := newFlagSet("vde migrate", migrateUsage+flagsHeading, stderr)
	configPath := operatorFlags(set)
	if code, done := parseArgs(set, args, stderr); done {
		return code
	}
	cfg, store, ok := openOperator(set.Name(), configPath(), stderr)
	if !ok {
		return exitFailure
	}
	defer store.Close()
	v, err := store.SchemaVersion(context.Background())
	if err != nil {
		fmt.Fprintf(stderr, "vde migrate: %v\n", err)
		return exitFailure
	}
	fmt.Fprintf(stdout, "storage %s: schema version %d\n", cfg.Storage.Driver, v)
	return exitOK
}
