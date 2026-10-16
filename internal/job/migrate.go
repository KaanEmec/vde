package job

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"slices"
	"strconv"
	"strings"
	"time"
)

// migrations/ holds the SQLite schema, migrations_postgres/ the same versions for
// PostgreSQL: a version number means the same schema on both.
//
//go:embed migrations/*.sql migrations_postgres/*.sql
var migrationFiles embed.FS

// migrationDir returns the directory of the dialect's migrations.
func migrationDir(pg bool) string {
	if pg {
		return "migrations_postgres"
	}
	return "migrations"
}

// pgMigrationLock is the advisory lock key that serializes migrations of concurrent
// processes on PostgreSQL (SQLite does it with BEGIN IMMEDIATE).
const pgMigrationLock = 0x76646531 // "vde1"

// migration is one numbered file migrations/NNNN_name.sql.
type migration struct {
	version int
	name    string
	sql     string
}

func loadMigrations(pg bool) ([]migration, error) {
	dir := migrationDir(pg)
	entries, err := fs.ReadDir(migrationFiles, dir)
	if err != nil {
		return nil, fmt.Errorf("job: migrations: %w", err)
	}
	var out []migration
	for _, e := range entries {
		num, name, ok := strings.Cut(strings.TrimSuffix(e.Name(), ".sql"), "_")
		v, err := strconv.Atoi(num)
		if !ok || err != nil || len(num) != 4 {
			return nil, fmt.Errorf("job: migration file %q is not NNNN_name.sql", e.Name())
		}
		data, err := migrationFiles.ReadFile(dir + "/" + e.Name())
		if err != nil {
			return nil, fmt.Errorf("job: migrations: %w", err)
		}
		out = append(out, migration{version: v, name: name, sql: string(data)})
	}
	slices.SortFunc(out, func(a, b migration) int { return a.version - b.version })
	for i, m := range out {
		if m.version != i+1 {
			return nil, fmt.Errorf("job: migration %04d missing or duplicated", i+1)
		}
	}
	return out, nil
}

// migrate applies every migration of the dialect up to version upTo (all when upTo < 0)
// that is not yet recorded in schema_migrations, each in its own transaction.
func migrate(ctx context.Context, db *sql.DB, pg bool, upTo int) error {
	migrations, err := loadMigrations(pg)
	if err != nil {
		return err
	}
	table := `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		name       TEXT    NOT NULL,
		applied_at INTEGER NOT NULL
	) STRICT`
	if pg {
		table = `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		name       TEXT    NOT NULL,
		applied_at BIGINT  NOT NULL
	)`
	}
	if err := inTx(ctx, db, pg, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, table)
		return err
	}); err != nil {
		return fmt.Errorf("job: migrations: %w", err)
	}
	s := &sqlStore{db: db, pg: pg}
	for _, m := range migrations {
		if upTo >= 0 && m.version > upTo {
			break
		}
		if err := inTx(ctx, db, pg, func(tx *sql.Tx) error { return s.apply(ctx, tx, m) }); err != nil {
			return fmt.Errorf("job: migration %04d_%s: %w", m.version, m.name, err)
		}
	}
	return nil
}

// inTx runs fn in a transaction that concurrent migrators wait for: BEGIN IMMEDIATE on
// SQLite, a transaction-scoped advisory lock on PostgreSQL. A waiting process then sees
// the version the other one recorded.
func inTx(ctx context.Context, db *sql.DB, pg bool, fn func(*sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if pg {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(`+strconv.Itoa(pgMigrationLock)+`)`); err != nil {
			return err
		}
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *sqlStore) apply(ctx context.Context, tx *sql.Tx, m migration) error {
	var applied int
	if err := tx.QueryRowContext(ctx, s.q(`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`), m.version).Scan(&applied); err != nil {
		return err
	}
	if applied > 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, s.q(`INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`),
		m.version, m.name, time.Now().UnixMilli())
	return err
}
