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

//go:embed migrations/*.sql
var migrationFiles embed.FS

// migration is one numbered file migrations/NNNN_name.sql.
type migration struct {
	version int
	name    string
	sql     string
}

func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFiles, "migrations")
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
		data, err := migrationFiles.ReadFile("migrations/" + e.Name())
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

// migrate applies every migration up to version upTo (all when upTo < 0) that is not
// yet recorded in schema_migrations, each in its own transaction.
func migrate(ctx context.Context, db *sql.DB, upTo int) error {
	migrations, err := loadMigrations()
	if err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		name       TEXT    NOT NULL,
		applied_at INTEGER NOT NULL
	) STRICT`); err != nil {
		return fmt.Errorf("job: migrations: %w", err)
	}
	for _, m := range migrations {
		if upTo >= 0 && m.version > upTo {
			break
		}
		if err := apply(ctx, db, m); err != nil {
			return fmt.Errorf("job: migration %04d_%s: %w", m.version, m.name, err)
		}
	}
	return nil
}

func apply(ctx context.Context, db *sql.DB, m migration) error {
	tx, err := db.BeginTx(ctx, nil) // immediate: a concurrent process waits, then sees the row
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var applied int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, m.version).Scan(&applied); err != nil {
		return err
	}
	if applied > 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`,
		m.version, m.name, time.Now().UnixMilli()); err != nil {
		return err
	}
	return tx.Commit()
}
