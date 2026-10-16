package job

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // the "sqlite" database/sql driver
)

// maxOpenConns keeps the pool small: SQLite serializes writers, WAL lets readers run
// beside the one writer.
const maxOpenConns = 4

// SQLite is the Store on one SQLite database file in WAL mode.
type SQLite struct {
	sqlStore
}

var _ Store = (*SQLite)(nil)

// Open opens (creating it and its parent directories when missing) the database at
// path, sets the pragmas and applies every pending migration. The file is made
// readable by the owner only.
func Open(path string) (*SQLite, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("job: create database directory: %w", err)
	}
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Add("_pragma", "secure_delete(1)") // deleted content is overwritten, not left in free pages
	q.Set("_txlock", "immediate")        // take the write lock at BEGIN: no upgrade deadlocks
	db, err := sql.Open("sqlite", path+"?"+q.Encode())
	if err != nil {
		return nil, fmt.Errorf("job: open database: %w", err)
	}
	db.SetMaxOpenConns(maxOpenConns)
	db.SetMaxIdleConns(maxOpenConns)
	s := &SQLite{sqlStore{db: db}}
	ctx := context.Background()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("job: open database: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("job: database permissions: %w", err)
	}
	if err := migrate(ctx, db, false, -1); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// TryLock is the no-op twin of Postgres.TryLock: one process owns a SQLite database, so
// the lock is always granted.
func (s *SQLite) TryLock(context.Context, int64) (release func(), ok bool, err error) {
	return func() {}, true, nil
}
