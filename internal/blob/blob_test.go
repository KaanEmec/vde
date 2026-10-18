package blob

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const jobA, jobB = "j_01JA3V9Q6ZK7M4W8X2R5T1N0HB", "j_01JA3V9Q6ZK7M4W8X2R5T1N0ZZ"

func newFS(t *testing.T) *FS {
	t.Helper()
	s, err := NewFS(filepath.Join(t.TempDir(), "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// forEachStore runs fn as a subtest per implementation, each with a fresh, empty store:
// the filesystem always, S3 when VDE_TEST_S3_ENDPOINT is set (see newS3Test).
func forEachStore(t *testing.T, fn func(t *testing.T, s Store)) {
	t.Helper()
	t.Run("fs", func(t *testing.T) { fn(t, newFS(t)) })
	t.Run("s3", func(t *testing.T) { fn(t, newS3Test(t)) })
}

func put(t *testing.T, s Store, jobID, name, data string) {
	t.Helper()
	if err := s.Put(t.Context(), jobID, name, strings.NewReader(data)); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, s Store, jobID, name string) (string, error) {
	t.Helper()
	rc, err := s.Get(t.Context(), jobID, name)
	if err != nil {
		return "", err
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	return string(b), err
}

func TestPutGet(t *testing.T) {
	forEachStore(t, func(t *testing.T, s Store) {
		put(t, s, jobA, Document, "doc bytes")
		put(t, s, jobA, Page(1), "page one")
		put(t, s, jobA, Page(1), "page one, replaced")

		if got, err := read(t, s, jobA, Document); err != nil || got != "doc bytes" {
			t.Errorf("document = %q, %v", got, err)
		}
		if got, _ := read(t, s, jobA, Page(1)); got != "page one, replaced" {
			t.Errorf("page-01 = %q", got)
		}
		if _, err := read(t, s, jobA, Page(2)); !errors.Is(err, ErrNotFound) {
			t.Errorf("missing page: %v, want ErrNotFound", err)
		}
		if _, err := read(t, s, jobB, Document); !errors.Is(err, ErrNotFound) {
			t.Errorf("missing job: %v, want ErrNotFound", err)
		}
	})
}

func TestFSLayoutAndPermissions(t *testing.T) {
	s := newFS(t)
	put(t, s, jobA, Document, "doc bytes")
	put(t, s, jobA, Page(1), "page one")
	put(t, s, jobA, Page(1), "page one, replaced")

	dir := filepath.Join(s.root, "HB", jobA)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if strings.Join(names, ",") != "document,page-01" {
		t.Errorf("files = %v, want document and page-01 only (no temp files)", names)
	}
	for _, p := range []string{filepath.Join(s.root, "HB"), dir} {
		if info, _ := os.Stat(p); info.Mode().Perm() != 0o700 {
			t.Errorf("%s mode %o, want 700", p, info.Mode().Perm())
		}
	}
	if info, _ := os.Stat(filepath.Join(dir, Document)); info.Mode().Perm() != 0o600 {
		t.Errorf("file mode %o, want 600", info.Mode().Perm())
	}
}

func TestRejectsUnsafeNames(t *testing.T) {
	forEachStore(t, func(t *testing.T, s Store) {
		for _, tt := range []struct{ id, name string }{
			{"../../etc", Document}, {"j_", Document}, {jobA, "../document"}, {jobA, "page-1"}, {jobA, "page-1000"}, {jobA, "other"},
		} {
			if err := s.Put(t.Context(), tt.id, tt.name, strings.NewReader("x")); err == nil {
				t.Errorf("Put(%q, %q) accepted", tt.id, tt.name)
			}
			if _, err := s.Get(t.Context(), tt.id, tt.name); err == nil || errors.Is(err, ErrNotFound) {
				t.Errorf("Get(%q, %q) = %v, want a validation error", tt.id, tt.name, err)
			}
		}
		if err := s.DeleteAll(t.Context(), "../x"); err == nil {
			t.Error("DeleteAll accepted an unsafe id")
		}
	})
}

// TestPageNamesBeyond99: a request with more than 99 pages (limits.pages allows it)
// stores every page.
func TestPageNamesBeyond99(t *testing.T) {
	forEachStore(t, func(t *testing.T, s Store) {
		for _, n := range []int{99, 100, 999} {
			put(t, s, jobA, Page(n), "page")
			if got, err := read(t, s, jobA, Page(n)); err != nil || got != "page" {
				t.Errorf("%s = %q, %v", Page(n), got, err)
			}
		}
	})
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

func TestPutFailureLeavesNothing(t *testing.T) {
	forEachStore(t, func(t *testing.T, s Store) {
		if err := s.Put(t.Context(), jobA, Document, io.MultiReader(strings.NewReader("partial"), failingReader{})); err == nil {
			t.Fatal("Put succeeded on a failing reader")
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := s.Put(ctx, jobA, Page(1), bytes.NewReader([]byte("x"))); !errors.Is(err, context.Canceled) {
			t.Errorf("Put with cancelled context: %v", err)
		}
		for _, name := range []string{Document, Page(1)} {
			if _, err := read(t, s, jobA, name); !errors.Is(err, ErrNotFound) {
				t.Errorf("%s after a failed put: %v, want ErrNotFound", name, err)
			}
		}
		if fsStore, ok := s.(*FS); ok {
			entries, _ := os.ReadDir(filepath.Join(fsStore.root, "HB", jobA))
			if len(entries) != 0 {
				t.Errorf("%d files left after failed puts", len(entries))
			}
		}
	})
}

func TestDeleteAll(t *testing.T) {
	forEachStore(t, func(t *testing.T, s Store) {
		put(t, s, jobA, Document, "a")
		put(t, s, jobA, Page(1), "a1")
		put(t, s, jobB, Document, "b")
		for range 2 { // idempotent
			if err := s.DeleteAll(t.Context(), jobA); err != nil {
				t.Fatal(err)
			}
		}
		for _, name := range []string{Document, Page(1)} {
			if _, err := read(t, s, jobA, name); !errors.Is(err, ErrNotFound) {
				t.Errorf("%s after DeleteAll: %v, want ErrNotFound", name, err)
			}
		}
		if fsStore, ok := s.(*FS); ok {
			if _, err := os.Stat(filepath.Join(fsStore.root, "HB")); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("job directory or shard left behind: %v", err)
			}
		}
		if got, _ := read(t, s, jobB, Document); got != "b" {
			t.Error("another job's blob was deleted")
		}
	})
}

func TestSweepOrphans(t *testing.T) {
	s := newFS(t)
	const recent = "j_01JA3V9Q6ZK7M4W8X2R5T1N0RC"
	for _, id := range []string{jobA, jobB, recent} {
		put(t, s, id, Document, "x")
	}
	old := time.Now().Add(-2 * time.Hour)
	for _, id := range []string{jobA, jobB} {
		dir, _ := s.jobDir(id)
		if err := os.Chtimes(dir, old, old); err != nil {
			t.Fatal(err)
		}
	}
	keep := func(id string) bool { return id == jobB } // jobA's row is gone; recent is unknown but new
	n, err := s.SweepOrphans(t.Context(), keep)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("swept %d jobs, want 1", n)
	}
	if _, err := read(t, s, jobA, Document); !errors.Is(err, ErrNotFound) {
		t.Errorf("orphan survived: %v", err)
	}
	for _, id := range []string{jobB, recent} {
		if _, err := read(t, s, id, Document); err != nil {
			t.Errorf("%s swept: %v", id, err)
		}
	}
}
