package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/kaanemec/vde/internal/auth"
	"github.com/kaanemec/vde/internal/job"
)

const tokenUsage = `Usage:
  vde token create --app id --scope jobs[,jobs:read] --expires date|duration [--label text] [--config path]
  vde token list --app id [--config path]
  vde token revoke [--config path] <token id>

Manages the access tokens of an application in the database vde serve uses
(server.data_dir/vde.db); a running service sees each change on its next request.

create prints the token once on stdout and nothing else; only its hash is stored, so a
lost token cannot be shown again: create another. --expires is required: a date
(2027-10-16, midnight UTC), an RFC 3339 time, or a duration from now (90d, 2160h), at
most auth.max_token_ttl away. Scopes: jobs (submit, poll, delete, quota) and jobs:read
(poll and quota only). Rotate by creating a new token before revoking the old one.
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
	set := flag.NewFlagSet("vde token "+name, flag.ContinueOnError)
	set.SetOutput(stderr)
	set.Usage = func() {
		fmt.Fprint(stderr, tokenUsage+flagsHeading)
		set.PrintDefaults()
	}
	return set
}

// openOperatorStore loads the configuration at path and opens its database.
func openOperatorStore(path string) (*job.SQLite, time.Duration, error) {
	cfg, err := loadOperatorConfig(path)
	if err != nil {
		return nil, 0, err
	}
	if cfg.Auth.MaxTokenTTL <= 0 {
		return nil, 0, errors.New("config: auth.max_token_ttl must be positive")
	}
	store, err := openStore(cfg)
	if err != nil {
		return nil, 0, err
	}
	return store, cfg.Auth.MaxTokenTTL, nil
}

func runTokenCreate(args []string, stdout, stderr io.Writer) int {
	set := tokenFlagSet("create", stderr)
	configPath := operatorFlags(set)
	app := set.String("app", "", "application id (required)")
	scopes := set.String("scope", "", "comma-separated scopes: jobs, jobs:read (required)")
	expires := set.String("expires", "", "expiry: date, RFC 3339 time or duration such as 90d (required)")
	label := set.String("label", "", "free text shown by vde token list")
	if code, done := parseFlags(set, args); done {
		return code
	}
	if set.NArg() > 0 {
		fmt.Fprintf(stderr, "vde token create: unexpected argument %q\n", set.Arg(0))
		return exitUsage
	}
	if *app == "" || *scopes == "" || *expires == "" {
		fmt.Fprintln(stderr, "vde token create: --app, --scope and --expires are required")
		return exitUsage
	}
	scopeList, err := auth.ParseScopes(*scopes)
	if err != nil {
		fmt.Fprintf(stderr, "vde token create: %v\n", err)
		return exitUsage
	}
	now := time.Now()
	expiresAt, err := parseExpires(*expires, now)
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
	if err := appendAudit(ctx, store, job.AuditEvent{
		At: now, Kind: auditTokenCreated, ApplicationID: row.ApplicationID, Actor: actorCLI,
	}, tokenDetails{TokenID: row.ID, Scopes: row.Scopes, ExpiresAt: row.ExpiresAt.UTC().Format(time.RFC3339)}); err != nil {
		fmt.Fprintf(stderr, "vde token create: warning: audit event not written: %v\n", err)
	}
	fmt.Fprintln(stdout, token)
	fmt.Fprintf(stderr, "vde token create: token %s for %s (scopes %s) expires %s; it is shown once, store it as a secret\n",
		row.ID, row.ApplicationID, strings.Join(row.Scopes, ","), row.ExpiresAt.UTC().Format(time.RFC3339))
	return exitOK
}

// tokenDetails is the audit detail of token events: never the token or its hash.
type tokenDetails struct {
	TokenID   string   `json:"token_id"`
	Scopes    []string `json:"scopes,omitempty"`
	ExpiresAt string   `json:"expires_at,omitempty"`
}

func runTokenList(args []string, stdout, stderr io.Writer) int {
	set := tokenFlagSet("list", stderr)
	configPath := operatorFlags(set)
	app := set.String("app", "", "application id (required)")
	if code, done := parseFlags(set, args); done {
		return code
	}
	if set.NArg() > 0 || *app == "" {
		fmt.Fprintln(stderr, "vde token list: --app is required and takes no arguments")
		return exitUsage
	}
	store, _, err := openOperatorStore(configPath())
	if err != nil {
		fmt.Fprintf(stderr, "vde token list: %v\n", err)
		return exitFailure
	}
	defer store.Close()
	ctx := context.Background()
	if _, err := store.GetApplication(ctx, *app); err != nil {
		fmt.Fprintf(stderr, "vde token list: application %q: %v\n", *app, err)
		return exitFailure
	}
	tokens, err := store.ListTokens(ctx, *app)
	if err != nil {
		fmt.Fprintf(stderr, "vde token list: %v\n", err)
		return exitFailure
	}
	now := time.Now()
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSCOPES\tSTATUS\tEXPIRES\tLAST USED\tCREATED\tLABEL")
	for _, t := range tokens {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", t.ID, strings.Join(t.Scopes, ","), tokenStatus(t, now),
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
	t, err := store.GetTokenByID(ctx, id)
	if errors.Is(err, job.ErrNotFound) {
		fmt.Fprintf(stderr, "vde token revoke: no token %q\n", id)
		return exitFailure
	}
	if err != nil {
		fmt.Fprintf(stderr, "vde token revoke: %v\n", err)
		return exitFailure
	}
	if !t.RevokedAt.IsZero() {
		fmt.Fprintf(stdout, "token %s was already revoked at %s\n", t.ID, stamp(t.RevokedAt))
		return exitOK
	}
	now := time.Now()
	if err := store.RevokeToken(ctx, id, now); err != nil {
		fmt.Fprintf(stderr, "vde token revoke: %v\n", err)
		return exitFailure
	}
	if err := appendAudit(ctx, store, job.AuditEvent{
		At: now, Kind: auditTokenRevoked, ApplicationID: t.ApplicationID, Actor: actorCLI,
	}, tokenDetails{TokenID: t.ID}); err != nil {
		fmt.Fprintf(stderr, "vde token revoke: warning: audit event not written: %v\n", err)
	}
	fmt.Fprintf(stdout, "token %s of %s revoked\n", t.ID, t.ApplicationID)
	return exitOK
}

func tokenStatus(t job.Token, now time.Time) string {
	switch {
	case !t.RevokedAt.IsZero():
		return "revoked"
	case !now.Before(t.ExpiresAt):
		return "expired"
	}
	return "active"
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.UTC().Format(time.RFC3339)
}

// parseExpires reads --expires: a date (midnight UTC), an RFC 3339 time, a number of days
// (90d) or a Go duration (2160h), the last two counted from now.
func parseExpires(s string, now time.Time) (time.Time, error) {
	if t, err := time.Parse(time.DateOnly, s); err == nil {
		return t, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if days, ok := strings.CutSuffix(s, "d"); ok {
		if n, err := strconv.Atoi(days); err == nil && n > 0 {
			return now.Add(time.Duration(n) * 24 * time.Hour), nil
		}
	}
	if d, err := time.ParseDuration(s); err == nil && d > 0 {
		return now.Add(d), nil
	}
	return time.Time{}, fmt.Errorf("--expires %q is not a date (2027-10-16), an RFC 3339 time or a positive duration (90d, 2160h)", s)
}
