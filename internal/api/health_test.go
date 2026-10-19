package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/job"
)

// switchHealth is a role group whose state a test flips.
type switchHealth struct{ up atomic.Bool }

func (s *switchHealth) Healthy() bool { return s.up.Load() }

// brokenStore fails every query, like a database that went away.
type brokenStore struct{ job.Store }

func (brokenStore) CountRunning(context.Context, string) (int, error) {
	return 0, errors.New("database is locked")
}

type healthResponse struct {
	Status  string       `json:"status"`
	Reasons []string     `json:"reasons"`
	Error   *errorDetail `json:"error"`
}

func getHealth(t *testing.T, h http.Handler) (int, healthResponse, string) {
	t.Helper()
	w := serve(h, http.MethodGet, "/healthz", nil)
	var body healthResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %q: %v", w.Body, err)
	}
	return w.Code, body, w.Body.String()
}

func TestHealthz(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	tests := []struct {
		name             string
		store            job.Store
		extractor, judge func() bool
		wantReasons      []string
	}{
		{name: "all up", store: store, extractor: fakeHealth(true), judge: fakeHealth(true)},
		{name: "extractor down", store: store, extractor: fakeHealth(false), judge: fakeHealth(true), wantReasons: []string{reasonExtractorDown}},
		{name: "judge down", store: store, extractor: fakeHealth(true), judge: fakeHealth(false), wantReasons: []string{reasonJudgeDown}},
		{name: "shared group down", store: store, extractor: fakeHealth(false), judge: fakeHealth(false), wantReasons: []string{reasonExtractorDown, reasonJudgeDown}},
		{name: "roles not configured", store: store, wantReasons: []string{reasonExtractorDown, reasonJudgeDown}},
		{name: "database down", store: brokenStore{store}, extractor: fakeHealth(true), judge: fakeHealth(true), wantReasons: []string{reasonDB}},
		{name: "no store", extractor: fakeHealth(true), judge: fakeHealth(true), wantReasons: []string{reasonDB}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := New(Deps{Store: tt.store, Logger: slog.New(slog.DiscardHandler), Extractor: tt.extractor, Judge: tt.judge})
			code, body, raw := getHealth(t, h)
			if tt.wantReasons == nil {
				if code != http.StatusOK || strings.TrimSpace(raw) != `{"status":"ok"}` {
					t.Errorf("got %d %s, want 200 {\"status\":\"ok\"}", code, raw)
				}
				return
			}
			if code != http.StatusServiceUnavailable || body.Status != "unavailable" {
				t.Errorf("got %d status %q, want 503 unavailable", code, body.Status)
			}
			if !slices.Equal(body.Reasons, tt.wantReasons) {
				t.Errorf("reasons = %q, want %q", body.Reasons, tt.wantReasons)
			}
			if body.Error == nil || body.Error.Code != codeUnavailable || body.Error.RequestID == "" {
				t.Errorf("error = %+v, want unavailable with a request id", body.Error)
			}
			if strings.Contains(raw, "locked") {
				t.Errorf("body carries the underlying error: %s", raw)
			}
		})
	}
}

// TestHealthzJudgeRecovery is the job's acceptance: the judge group marked down gives
// 503 role_judge_down, and its recovery 200, without a restart.
func TestHealthzJudgeRecovery(t *testing.T) {
	t.Parallel()
	judge := &switchHealth{}
	h := New(Deps{Store: newTestStore(t), Logger: slog.New(slog.DiscardHandler), Extractor: fakeHealth(true), Judge: judge.Healthy})
	if code, body, _ := getHealth(t, h); code != http.StatusServiceUnavailable || !slices.Equal(body.Reasons, []string{reasonJudgeDown}) {
		t.Errorf("judge down: %d %v, want 503 [role_judge_down]", code, body.Reasons)
	}
	judge.up.Store(true)
	if code, _, raw := getHealth(t, h); code != http.StatusOK {
		t.Errorf("judge recovered: %d %s, want 200", code, raw)
	}
}

func TestHealthzDataDir(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root writes to read-only directories")
	}
	dir := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	h := New(Deps{
		Config: &config.Config{Server: config.Server{DataDir: dir}}, Store: newTestStore(t),
		Logger: slog.New(slog.DiscardHandler), Extractor: fakeHealth(true), Judge: fakeHealth(true), Now: clock,
	})
	if code, _, raw := getHealth(t, h); code != http.StatusOK {
		t.Fatalf("writable data dir: %d %s", code, raw)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if code, _, _ := getHealth(t, h); code != http.StatusOK {
		t.Errorf("within 30 s the cached result stands: got %d", code)
	}
	now = now.Add(31 * time.Second)
	if code, body, _ := getHealth(t, h); code != http.StatusServiceUnavailable || !slices.Equal(body.Reasons, []string{reasonBlobs}) {
		t.Errorf("read-only data dir after the cache: %d %v, want 503 [blobs]", code, body.Reasons)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("the probe left files: %v", entries)
	}
}

// countingStore counts the health check's database queries and fails them on demand.
type countingStore struct {
	job.Store
	queries atomic.Int64
	down    atomic.Bool
}

func (c *countingStore) CountRunning(ctx context.Context, appID string) (int, error) {
	c.queries.Add(1)
	if c.down.Load() {
		return 0, errors.New("database is locked")
	}
	return c.Store.CountRunning(ctx, appID)
}

// TestHealthzDatabaseRate: a flood of /healthz runs the database check at most
// healthDBBurst times, then healthDBRate times per second; every request still gets an
// answer from the last result (never 429), and the result follows the database once
// the rate allows a new check.
func TestHealthzDatabaseRate(t *testing.T) {
	t.Parallel()
	store := &countingStore{Store: newTestStore(t)}
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	advance := func(d time.Duration) { mu.Lock(); defer mu.Unlock(); now = now.Add(d) }
	h := New(Deps{Store: store, Logger: slog.New(slog.DiscardHandler), Extractor: fakeHealth(true), Judge: fakeHealth(true), Now: clock})
	for i := range 100 {
		if code, _, raw := getHealth(t, h); code != http.StatusOK {
			t.Fatalf("request %d: %d %s, want 200", i, code, raw)
		}
	}
	if got := store.queries.Load(); got != healthDBBurst {
		t.Errorf("100 requests at one instant ran %d queries, want %d", got, healthDBBurst)
	}
	store.down.Store(true)
	if code, _, _ := getHealth(t, h); code != http.StatusOK {
		t.Errorf("over the rate: %d, want the last result (200)", code)
	}
	advance(time.Second / healthDBRate)
	if code, body, _ := getHealth(t, h); code != http.StatusServiceUnavailable || !slices.Equal(body.Reasons, []string{reasonDB}) {
		t.Errorf("after one refill: %d %v, want 503 [db]", code, body.Reasons)
	}
	if got := store.queries.Load(); got != healthDBBurst+1 {
		t.Errorf("queries = %d, want %d", got, healthDBBurst+1)
	}
	advance(time.Minute)
	store.down.Store(false)
	for i := range healthDBBurst {
		if code, _, raw := getHealth(t, h); code != http.StatusOK {
			t.Fatalf("after a minute, request %d: %d %s, want 200", i, code, raw)
		}
	}
	if got := store.queries.Load(); got != 2*healthDBBurst+1 {
		t.Errorf("the bucket refills to its burst: queries = %d, want %d", got, 2*healthDBBurst+1)
	}
}

// ctxStore answers the health query as a driver does: with the context's error once the
// context is done, else by the function set (nil: healthy).
type ctxStore struct {
	job.Store
	queries atomic.Int64
	answer  func(ctx context.Context) error
}

func (c *ctxStore) CountRunning(ctx context.Context, _ string) (int, error) {
	c.queries.Add(1)
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if c.answer != nil {
		return 0, c.answer(ctx)
	}
	return 0, nil
}

// TestHealthzContextErrors: a client that hangs up cannot fail the database check (it
// runs without the request's cancellation), and a check that fails with a context error
// is answered but never becomes the result that rate-limited requests reuse.
func TestHealthzContextErrors(t *testing.T) {
	t.Parallel()
	t.Run("cancelled request", func(t *testing.T) {
		t.Parallel()
		store := &ctxStore{Store: newTestStore(t)}
		h := New(Deps{Store: store, Logger: slog.New(slog.DiscardHandler), Extractor: fakeHealth(true), Judge: fakeHealth(true)})
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		w := serve(h, http.MethodGet, "/healthz", func(r *http.Request) { *r = *r.WithContext(ctx) })
		if w.Code != http.StatusOK || store.queries.Load() != 1 {
			t.Fatalf("aborted request: %d %s after %d queries, want 200 from one query", w.Code, w.Body, store.queries.Load())
		}
		for range 2 * healthDBBurst { // over the rate: the kept result is the healthy one
			if code, _, raw := getHealth(t, h); code != http.StatusOK {
				t.Fatalf("after the aborted request: %d %s, want 200", code, raw)
			}
		}
	})
	t.Run("the check's own timeout is kept as down", func(t *testing.T) {
		t.Parallel()
		now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC) // fixed: no refill
		store := &ctxStore{Store: newTestStore(t)}
		h := New(Deps{Store: store, Logger: slog.New(slog.DiscardHandler), Extractor: fakeHealth(true), Judge: fakeHealth(true),
			Now: func() time.Time { return now }})
		if code, _, raw := getHealth(t, h); code != http.StatusOK {
			t.Fatalf("first check: %d %s, want 200", code, raw)
		}
		store.answer = func(context.Context) error { return context.DeadlineExceeded } // a hung database
		for i := 1; i < healthDBBurst; i++ {                                           // the rest of the burst fails on the check's deadline
			if code, body, _ := getHealth(t, h); code != http.StatusServiceUnavailable || !slices.Equal(body.Reasons, []string{reasonDB}) {
				t.Fatalf("check %d: %d %v, want 503 [db]", i+1, code, body.Reasons)
			}
		}
		if code, body, _ := getHealth(t, h); code != http.StatusServiceUnavailable || store.queries.Load() != healthDBBurst {
			t.Errorf("over the rate: %d %v after %d queries, want the kept 503 [db] from %d", code, body.Reasons, store.queries.Load(), healthDBBurst)
		}
	})
}

// TestHealthzSlowDatabaseDoesNotQueue: while a database check is running, other requests
// answer at once with the last result instead of waiting for it.
func TestHealthzSlowDatabaseDoesNotQueue(t *testing.T) {
	t.Parallel()
	entered, unblock := make(chan struct{}), make(chan struct{})
	var block atomic.Bool
	store := &ctxStore{Store: newTestStore(t), answer: func(context.Context) error {
		if block.Load() {
			close(entered)
			<-unblock
		}
		return nil
	}}
	h := New(Deps{Store: store, Logger: slog.New(slog.DiscardHandler), Extractor: fakeHealth(true), Judge: fakeHealth(true)})
	if code, _, raw := getHealth(t, h); code != http.StatusOK {
		t.Fatalf("first check: %d %s", code, raw)
	}
	block.Store(true)
	done := make(chan int, 1)
	go func() { done <- serve(h, http.MethodGet, "/healthz", nil).Code }()
	<-entered
	start := time.Now()
	for range 5 {
		if code, _, raw := getHealth(t, h); code != http.StatusOK {
			t.Errorf("during a slow check: %d %s, want the last result 200", code, raw)
		}
	}
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Errorf("requests during a slow check took %v: they waited for it", d)
	}
	if n := store.queries.Load(); n != 2 {
		t.Errorf("queries = %d, want 2: no check starts while one runs", n)
	}
	close(unblock)
	if code := <-done; code != http.StatusOK {
		t.Errorf("the slow check answered %d, want 200", code)
	}
}
