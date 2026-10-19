package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/kaanemec/vde/internal/audit"
	"github.com/kaanemec/vde/internal/auth"
	"github.com/kaanemec/vde/internal/job"
)

const tokenUsage = `Usage:
  vde token create --app id --scope jobs[,jobs:read] --expires date|duration [--label text] [--config path]
  vde token create --admin --expires date|duration [--label text] [--config path]
  vde token list (--app id | --admin) [--config path]
  vde token revoke [--config path] <token id>

Manages the access tokens of an application, and the admin tokens of the admin API, in
the database vde serve uses (server.data_dir/vde.db); a running service sees each change
on its next request. The commands work on the database directly, never over HTTP.

create prints the token once on stdout and nothing else; only its hash is stored, so a
lost token cannot be shown again: create another. --expires is required: a date
(2027-10-16, midnight UTC), an RFC 3339 time, or a duration from now (90d, 2160h), at
most auth.max_token_ttl away. Scopes: jobs (submit, poll, delete, quota) and jobs:read
(poll and quota only). --admin creates an admin token instead: no application, the single
scope admin, accepted only by the admin API (admin.listen) and refused on /v1; admin
tokens are created here only. Rotate by creating a new token before revoking the old one.
`

func runToken(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, tokenUsage)
		return exitUsage
	}
	switch args[0] {
	case "create":
		return runTokenCreate(args[1:], stdout, stderr)
	case "list":
		return runTokenList(args[1:], stdout, stderr)
	case "revoke":
		return runTokenRevoke(args[1:], stdout, stderr)
	case "help", "-h", "-help", "--help":
		fmt.Fprint(stdout, tokenUsage)
		return exitOK
	default:
		fmt.Fprintf(stderr, "vde token: unknown command %q\n\n%s", args[0], tokenUsage)
		return exitUsage
	}
}

func tokenFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	return newFlagSet("vde token "+name, tokenUsage+flagsHeading, stderr)
}

// openOperatorStore loads the configuration at path and opens its database.
func openOperatorStore(path string) (*job.DB, time.Duration, error) {
	cfg, err := loadOperatorConfig(path)
	if err != nil {
		return nil, 0, err
	}
	if cfg.Auth.MaxTokenTTL <= 0 {
		return nil, 0, errors.New("config: auth.max_token_ttl must be positive")
	}
	store, err := openStore(context.Background(), cfg)
	if err != nil {
		return nil, 0, err
	}
	return store, cfg.Auth.MaxTokenTTL, nil
}

func runTokenCreate(args []string, stdout, stderr io.Writer) int {
	set := tokenFlagSet("create", stderr)
	configPath := operatorFlags(set)
	app := set.String("app", "", "application id (required unless --admin)")
	scopes := set.String("scope", "", "comma-separated scopes: jobs, jobs:read (required unless --admin)")
	admin := set.Bool("admin", false, "create an admin token (admin API only) instead of an application token")
	expires := set.String("expires", "", "expiry: date, RFC 3339 time or duration such as 90d (required)")
	label := set.String("label", "", "free text shown by vde token list")
	if code, done := parseArgs(set, args, stderr); done {
		return code
	}
	var scopeList []string
	switch {
	case *admin && (*app != "" || *scopes != ""):
		fmt.Fprintln(stderr, "vde token create: --admin cannot be combined with --app or --scope")
		return exitUsage
	case *admin && *expires == "":
		fmt.Fprintln(stderr, "vde token create: --expires is required")
		return exitUsage
	case *admin:
		scopeList = []string{auth.ScopeAdmin}
	case *app == "" || *scopes == "" || *expires == "":
		fmt.Fprintln(stderr, "vde token create: --app, --scope and --expires are required (or --admin and --expires)")
		return exitUsage
	default:
		var err error
		if scopeList, err = auth.ParseScopes(*scopes); err != nil {
			fmt.Fprintf(stderr, "vde token create: %v\n", err)
			return exitUsage
		}
	}
	now := time.Now()
	expiresAt, err := auth.ParseExpires(*expires, now)
	if err != nil {
		fmt.Fprintf(stderr, "vde token create: %v\n", err)
		return exitUsage
	}
	store, maxTTL, err := openOperatorStore(configPath())
	if err != nil {
		fmt.Fprintf(stderr, "vde token create: %v\n", err)
		return exitFailure
	}
	defer store.Close()

	ctx := context.Background()
	token, row, err := auth.Create(ctx, store, auth.Request{
		ApplicationID: *app, Scopes: scopeList, ExpiresAt: expiresAt, Label: *label,
	}, maxTTL, now)
	if err != nil {
		fmt.Fprintf(stderr, "vde token create: %v\n", err)
		return exitFailure
	}
	if err := cliAudit(store).Record(ctx, "", row.ApplicationID, "", audit.TokenCreated{
		TokenID: row.ID, Scopes: row.Scopes, ExpiresAt: row.ExpiresAt.UTC(),
	}); err != nil {
		fmt.Fprintf(stderr, "vde token create: warning: audit event not written: %v\n", err)
	}
	owner := row.ApplicationID
	if owner == "" {
		owner = "the admin API"
	}
	fmt.Fprintln(stdout, token)
	fmt.Fprintf(stderr, "vde token create: token %s for %s (scopes %s) expires %s; it is shown once, store it as a secret\n",
		row.ID, owner, strings.Join(row.Scopes, ","), row.ExpiresAt.UTC().Format(time.RFC3339))
	return exitOK
}

func runTokenList(args []string, stdout, stderr io.Writer) int {
	set := tokenFlagSet("list", stderr)
	configPath := operatorFlags(set)
	app := set.String("app", "", "application id")
	admin := set.Bool("admin", false, "list the admin tokens instead")
	if code, done := parseFlags(set, args); done {
		return code
	}
	if set.NArg() > 0 || (*app == "") == !*admin {
		fmt.Fprintln(stderr, "vde token list: give exactly one of --app and --admin, and no arguments")
		return exitUsage
	}
	store, _, err := openOperatorStore(configPath())
	if err != nil {
		fmt.Fprintf(stderr, "vde token list: %v\n", err)
		return exitFailure
	}
	defer store.Close()
	ctx := context.Background()
	if !*admin {
		if _, err := store.GetApplication(ctx, *app); err != nil {
			fmt.Fprintf(stderr, "vde token list: application %q: %v\n", *app, err)
			return exitFailure
		}
	}
	tokens, err := store.ListTokens(ctx, *app) // "" lists the admin tokens
	if err != nil {
		fmt.Fprintf(stderr, "vde token list: %v\n", err)
		return exitFailure
	}
	now := time.Now()
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSCOPES\tSTATUS\tEXPIRES\tLAST USED\tCREATED\tLABEL")
	for _, t := range tokens {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", t.ID, strings.Join(t.Scopes, ","), t.Status(now),
			stamp(t.ExpiresAt), stamp(t.LastUsedAt), stamp(t.CreatedAt), dash(t.Label))
	}
	tw.Flush()
	return exitOK
}

func runTokenRevoke(args []string, stdout, stderr io.Writer) int {
	set := tokenFlagSet("revoke", stderr)
	configPath := operatorFlags(set)
	if code, done := parseFlags(set, args); done {
		return code
	}
	if set.NArg() == 0 {
		fmt.Fprintln(stderr, "vde token revoke: want exactly one token id")
		return exitUsage
	}
	id := set.Arg(0)
	if code, done := parseFlags(set, set.Args()[1:]); done { // flags may follow the id
		return code
	}
	if set.NArg() > 0 {
		fmt.Fprintln(stderr, "vde token revoke: want exactly one token id")
		return exitUsage
	}
	if tokenID, _, err := auth.Parse(id); err == nil {
		id = tokenID // a whole token was given; only its id is used
	}
	store, _, err := openOperatorStore(configPath())
	if err != nil {
		fmt.Fprintf(stderr, "vde token revoke: %v\n", err)
		return exitFailure
	}
	defer store.Close()
	ctx := context.Background()
	t, revoked, err := auth.Revoke(ctx, store, id, time.Now())
	switch {
	case errors.Is(err, job.ErrNotFound):
		fmt.Fprintf(stderr, "vde token revoke: no token %q\n", id)
		return exitFailure
	case err != nil:
		fmt.Fprintf(stderr, "vde token revoke: %v\n", err)
		return exitFailure
	case !revoked:
		fmt.Fprintf(stdout, "token %s was already revoked at %s\n", t.ID, stamp(t.RevokedAt))
		return exitOK
	}
	if err := cliAudit(store).Record(ctx, "", t.ApplicationID, "", audit.TokenRevoked{TokenID: t.ID}); err != nil {
		fmt.Fprintf(stderr, "vde token revoke: warning: audit event not written: %v\n", err)
	}
	owner := t.ApplicationID
	if owner == "" {
		owner = "the admin API"
	}
	fmt.Fprintf(stdout, "token %s of %s revoked\n", t.ID, owner)
	return exitOK
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.UTC().Format(time.RFC3339)
}
