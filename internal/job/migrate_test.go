package job

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// schemaOf returns the database's schema as SQL text, ordered by object name.
func schemaOf(t *testing.T, db *sql.DB) string {
	t.Helper()
	rows, err := db.QueryContext(t.Context(),
		`SELECT name, sql FROM sqlite_master WHERE sql IS NOT NULL AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var name, text string
		if err := rows.Scan(&name, &text); err != nil {
			t.Fatal(err)
		}
		b.WriteString(name + ": " + text + "\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestMigrationsLoad(t *testing.T) {
	ms, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) < 3 {
		t.Fatalf("%d migrations, want at least 3", len(ms))
	}
}

// TestMigrateFromEachVersion applies the migrations up to every intermediate version,
// then opens the database normally, and expects the schema of a fresh database.
func TestMigrateFromEachVersion(t *testing.T) {
	fresh := openTest(t)
	want := schemaOf(t, fresh.db)
	for _, table := range []string{"applications", "tokens", "jobs", "quota_usage", "audit_events", "schema_migrations"} {
		if !strings.Contains(want, table+": CREATE TABLE") {
			t.Errorf("table %s missing from a fresh database", table)
		}
	}
	ms, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	for v := 0; v <= len(ms); v++ {
		path := filepath.Join(t.TempDir(), "vde.db")
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		if err := migrate(t.Context(), db, v); err != nil {
			t.Fatalf("migrate to %d: %v", v, err)
		}
		var applied int
		if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM schema_migrations`).Scan(&applied); err != nil || applied != v {
			t.Fatalf("version %d: %d recorded, %v", v, applied, err)
		}
		_ = db.Close()

		s, err := Open(path)
		if err != nil {
			t.Fatalf("open from version %d: %v", v, err)
		}
		if got := schemaOf(t, s.db); got != want {
			t.Errorf("schema from version %d differs:\n%s\nwant:\n%s", v, got, want)
		}
		_ = s.Close()
	}
}
