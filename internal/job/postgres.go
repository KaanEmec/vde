package job

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // the "pgx" database/sql driver
)

// Postgres pool defaults. Every worker holds a connection only for the duration of one
// statement, so a pool a few times worker.size is plenty; connections are recycled so a
// failover of a managed database or a PgBouncer restart heals on its own.
const (
	DefaultPostgresConns = 16
	pgConnMaxLifetime    = 30 * time.Minute
	pgConnMaxIdleTime    = 5 * time.Minute
	pgConnectTimeout     = 10 * time.Second
)

// Postgres is the Store on a PostgreSQL database (13 or later), for several replicas
// sharing one queue. It runs the same statements as SQLite with $N placeholders; jobs
// are leased with FOR UPDATE SKIP LOCKED. Deleted content stays in dead row versions
// until autovacuum reclaims them; there is no checkpoint to force.
type Postgres struct {
	sqlStore
}

var _ Store = (*Postgres)(nil)

// OpenPostgres connects to the database named by dsn (a postgres:// URL or key=value
// string), sizes the pool (maxOpenConns <= 0 means DefaultPostgresConns) and applies
// every pending migration. Errors never contain the DSN's password.
func OpenPostgres(ctx context.Context, dsn string, maxOpenConns int) (*Postgres, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("job: open postgres: %w", redactDSN(err, dsn))
	}
	if maxOpenConns <= 0 {
		maxOpenConns = DefaultPostgresConns
	}
	db.SetMaxOpenConns(maxOpenConns)
	db.SetMaxIdleConns(max(1, maxOpenConns/2))
	db.SetConnMaxLifetime(pgConnMaxLifetime)
	db.SetConnMaxIdleTime(pgConnMaxIdleTime)
	pingCtx, cancel := context.WithTimeout(ctx, pgConnectTimeout)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("job: open postgres: %w", redactDSN(err, dsn))
	}
	if err := migrate(ctx, db, true, -1); err != nil {
		_ = db.Close()
		return nil, redactDSN(err, dsn)
	}
	return &Postgres{sqlStore{db: db, pg: true}}, nil
}

var dsnPasswordRe = regexp.MustCompile(`password\s*=\s*('(?:[^'\\]|\\.)*'|\S+)`)

// redactDSN returns err with the DSN and its password replaced by xxxxx wherever they
// appear in the message. pgx already redacts its own messages; this covers the rest.
func redactDSN(err error, dsn string) error {
	if err == nil || dsn == "" {
		return err
	}
	msg := err.Error()
	var secrets []string
	if u, perr := url.Parse(dsn); perr == nil && u.User != nil {
		if pw, ok := u.User.Password(); ok && pw != "" {
			secrets = append(secrets, pw, url.QueryEscape(pw), url.PathEscape(pw))
		}
	}
	for _, m := range dsnPasswordRe.FindAllStringSubmatch(dsn, -1) {
		pw := strings.Trim(m[1], "'")
		secrets = append(secrets, m[1], pw, url.QueryEscape(pw))
	}
	redacted := strings.ReplaceAll(msg, dsn, "xxxxx")
	for _, s := range secrets {
		if s != "" {
			redacted = strings.ReplaceAll(redacted, s, "xxxxx")
		}
	}
	if redacted == msg {
		return err
	}
	return errors.New(redacted)
}
