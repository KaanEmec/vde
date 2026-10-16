package job

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// postgresDSN returns VDE_TEST_POSTGRES or skips the test.
func postgresDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("VDE_TEST_POSTGRES")
	if dsn == "" {
		t.Skip("VDE_TEST_POSTGRES is not set")
	}
	return dsn
}

var pgSchemaSeq atomic.Int64

// pgTestSchema creates a fresh schema in the VDE_TEST_POSTGRES database, dropped at
// cleanup, and returns a DSN whose search_path is that schema.
func pgTestSchema(t *testing.T) (dsn, schema string) {
	t.Helper()
	base := postgresDSN(t)
	schema = fmt.Sprintf("vde_test_%d_%d", os.Getpid(), pgSchemaSeq.Add(1))
	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.ExecContext(t.Context(), `CREATE SCHEMA `+schema); err != nil {
		_ = admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.ExecContext(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
		_ = admin.Close()
	})
	if u, err := url.Parse(base); err == nil && (u.Scheme == "postgres" || u.Scheme == "postgresql") {
		q := u.Query()
		q.Set("search_path", schema)
		u.RawQuery = q.Encode()
		return u.String(), schema
	}
	return base + " search_path=" + schema, schema
}

// openTestPostgres opens a store in a fresh schema.
func openTestPostgres(t *testing.T) *Postgres {
	t.Helper()
	dsn, _ := pgTestSchema(t)
	s, err := OpenPostgres(t.Context(), dsn, 8)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() }) // runs before the schema is dropped
	return s
}

// pgSchemaOf returns the current schema's columns, indexes and constraints as text.
func pgSchemaOf(t *testing.T, db *sql.DB, schema string) string {
	t.Helper()
	var b strings.Builder
	for _, q := range []string{
		`SELECT table_name || '.' || column_name || ' ' || data_type || ' ' || is_nullable || ' ' ||
			COALESCE(column_default, '') || ' ' || is_identity
		 FROM information_schema.columns WHERE table_schema = current_schema() ORDER BY table_name, column_name`,
		`SELECT indexdef FROM pg_indexes WHERE schemaname = current_schema() ORDER BY indexname`,
		`SELECT conrelid::regclass::text || ' ' || conname || ' ' || pg_get_constraintdef(oid)
		 FROM pg_constraint WHERE connamespace = current_schema()::regnamespace ORDER BY conname`,
	} {
		rows, err := db.QueryContext(t.Context(), q)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				t.Fatal(err)
			}
			b.WriteString(strings.ReplaceAll(line, schema+".", "") + "\n")
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		_ = rows.Close()
	}
	return b.String()
}

func TestMigrationsLoadBothDialects(t *testing.T) {
	sqliteMs, err := loadMigrations(false)
	if err != nil {
		t.Fatal(err)
	}
	pgMs, err := loadMigrations(true)
	if err != nil {
		t.Fatal(err)
	}
	if len(sqliteMs) != len(pgMs) {
		t.Fatalf("%d SQLite migrations, %d PostgreSQL ones: every version needs both", len(sqliteMs), len(pgMs))
	}
	for i := range sqliteMs {
		if sqliteMs[i].name != pgMs[i].name {
			t.Errorf("migration %04d: %q on SQLite, %q on PostgreSQL", i+1, sqliteMs[i].name, pgMs[i].name)
		}
	}
}

// TestMigrateFromEachVersionPostgres is TestMigrateFromEachVersion on PostgreSQL.
func TestMigrateFromEachVersionPostgres(t *testing.T) {
	ctx := t.Context()
	freshDSN, freshSchema := pgTestSchema(t)
	fresh, err := OpenPostgres(ctx, freshDSN, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	want := pgSchemaOf(t, fresh.db, freshSchema)
	for _, table := range []string{"applications", "tokens", "jobs", "quota_usage", "audit_events", "schema_migrations"} {
		if !strings.Contains(want, table+".") {
			t.Errorf("table %s missing from a fresh database", table)
		}
	}
	if v, err := fresh.SchemaVersion(ctx); err != nil || v != 5 && v < 5 {
		t.Errorf("SchemaVersion = %d, %v", v, err)
	}
	ms, err := loadMigrations(true)
	if err != nil {
		t.Fatal(err)
	}
	for v := 0; v <= len(ms); v++ {
		dsn, schema := pgTestSchema(t)
		db, err := sql.Open("pgx", dsn)
		if err != nil {
			t.Fatal(err)
		}
		if err := migrate(ctx, db, true, v); err != nil {
			t.Fatalf("migrate to %d: %v", v, err)
		}
		var applied int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&applied); err != nil || applied != v {
			t.Fatalf("version %d: %d recorded, %v", v, applied, err)
		}
		_ = db.Close()
		s, err := OpenPostgres(ctx, dsn, 2)
		if err != nil {
			t.Fatalf("open from version %d: %v", v, err)
		}
		if got := pgSchemaOf(t, s.db, schema); got != want {
			t.Errorf("schema from version %d differs:\n%s\nwant:\n%s", v, got, want)
		}
		_ = s.Close()
	}
}

// TestConcurrentMigrate: several processes starting at once apply each migration once.
func TestConcurrentMigrate(t *testing.T) {
	dsn, _ := pgTestSchema(t)
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Go(func() {
			s, err := OpenPostgres(t.Context(), dsn, 2)
			if err == nil {
				_ = s.Close()
			}
			errs[i] = err
		})
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatal(err)
	}
}

// TestConcurrentLeasing: workers leasing at the same time never take the same job and
// together take every job (FOR UPDATE SKIP LOCKED on PostgreSQL, the single writer on
// SQLite).
func TestConcurrentLeasing(t *testing.T) {
	forEachDriver(t, func(t *testing.T, s *sqlStore) {
		ctx := t.Context()
		seedApp(t, s, testApp("app"))
		const jobs, workers = 40, 8
		for i := range jobs {
			j := newJob("app")
			j.CreatedAt = t0.Add(time.Duration(i) * time.Millisecond)
			createJob(t, s, j)
		}
		var mu sync.Mutex
		var leased []string
		var wg sync.WaitGroup
		errs := make([]error, workers)
		for w := range workers {
			wg.Go(func() {
				for {
					j, err := s.LeaseNextQueued(ctx, fmt.Sprintf("w%d", w), t0.Add(time.Second), t0.Add(time.Hour), nil)
					if errors.Is(err, ErrNotFound) {
						return
					}
					if err != nil {
						errs[w] = err
						return
					}
					mu.Lock()
					leased = append(leased, j.ID)
					mu.Unlock()
				}
			})
		}
		wg.Wait()
		if err := errors.Join(errs...); err != nil {
			t.Fatal(err)
		}
		slices.Sort(leased)
		if len(leased) != jobs || len(slices.Compact(slices.Clone(leased))) != jobs {
			t.Errorf("%d leases of %d distinct jobs, want %d of %d", len(leased), len(slices.Compact(leased)), jobs, jobs)
		}
	})
}

func TestRebind(t *testing.T) {
	for in, want := range map[string]string{
		`SELECT 1`:                         `SELECT 1`,
		`a = ? AND b = ? LIMIT ?`:          `a = $1 AND b = $2 LIMIT $3`,
		`a = ?1 OR ?1 = '' AND c < ?12`:    `a = $1 OR $1 = '' AND c < $12`,
		`VALUES (?, ?, TRUE, 'config', ?)`: `VALUES ($1, $2, TRUE, 'config', $3)`,
	} {
		if got := rebind(in); got != want {
			t.Errorf("rebind(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRedactDSN(t *testing.T) {
	for _, dsn := range []string{
		"postgres://vde:s3cr%40t@db.internal:5432/vde?sslmode=require",
		"host=db.internal user=vde password=s3cr@t dbname=vde",
		"host=db.internal user=vde password='s3cr@t' dbname=vde",
	} {
		err := redactDSN(fmt.Errorf("dial %s: password s3cr@t (s3cr%%40t) refused", dsn), dsn)
		if strings.Contains(err.Error(), "s3cr") {
			t.Errorf("redactDSN(%q) = %q: the password survives", dsn, err)
		}
	}
	plain := errors.New("connection refused")
	if got := redactDSN(plain, "postgres://db/vde"); got != plain { //nolint:errorlint // identity: the same error comes back
		t.Errorf("an error without secrets was rewritten: %v", got)
	}
	if _, err := OpenPostgres(t.Context(), "postgres://vde:hunter2@127.0.0.1:1/vde?connect_timeout=1", 1); err == nil ||
		strings.Contains(err.Error(), "hunter2") {
		t.Errorf("OpenPostgres on a closed port = %v, want an error without the password", err)
	}
}

// TestTryLockPostgres: the advisory lock is held by one session at a time, refused at once
// to the others, and free again after release or when the holder's connection closes.
func TestTryLockPostgres(t *testing.T) {
	dsn, _ := pgTestSchema(t)
	a, err := OpenPostgres(t.Context(), dsn, 2)
	if err != nil {
		t.Fatal(err)
	}
	b, err := OpenPostgres(t.Context(), dsn, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	const key = SweepLock + 1000 // not the key of a sweeper of another test run
	release, ok, err := a.TryLock(t.Context(), key)
	if err != nil || !ok {
		t.Fatalf("first TryLock = %t, %v", ok, err)
	}
	if _, ok, err := b.TryLock(t.Context(), key); err != nil || ok {
		t.Fatalf("second TryLock while held = %t, %v; want refused", ok, err)
	}
	release()
	release2, ok, err := b.TryLock(t.Context(), key)
	if err != nil || !ok {
		t.Fatalf("TryLock after release = %t, %v", ok, err)
	}
	release2()
	if _, ok, _ := a.TryLock(t.Context(), key); !ok {
		t.Fatal("TryLock on the first store again refused")
	}
	defer a.Close()
	// The holder dies without releasing: its session ends, and the lock with it.
	if _, err := b.db.ExecContext(t.Context(), `SELECT pg_terminate_backend(pid) FROM pg_locks
		WHERE locktype = 'advisory' AND classid = 0 AND objid = $1 AND pid <> pg_backend_pid()`, key); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		release3, ok, err := b.TryLock(t.Context(), key)
		if err == nil && ok {
			release3()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("lock of a closed store still held: %t, %v", ok, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
