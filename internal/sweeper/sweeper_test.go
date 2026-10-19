package sweeper_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kaanemec/vde/internal/audit"
	"github.com/kaanemec/vde/internal/blob"
	"github.com/kaanemec/vde/internal/job"
	"github.com/kaanemec/vde/internal/metrics"
	"github.com/kaanemec/vde/internal/sweeper"
)

var t0 = time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)

const sentinel = "SENTINEL"

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// failingBlobs refuses to delete the blobs of the jobs in fail.
type failingBlobs struct {
	blob.Store
	mu   sync.Mutex
	fail map[string]bool
}

func (f *failingBlobs) DeleteAll(ctx context.Context, id string) error {
	f.mu.Lock()
	bad := f.fail[id]
	f.mu.Unlock()
	if bad {
		return errors.New("disk on fire")
	}
	return f.Store.DeleteAll(ctx, id)
}

func (f *failingBlobs) heal() {
	f.mu.Lock()
	defer f.mu.Unlock()
	clear(f.fail)
}

type rig struct {
	dir   string
	store *job.DB
	blobs *blob.FS
	fail  *failingBlobs
	m     *metrics.Metrics
	logs  *syncBuffer
	opts  sweeper.Options
}

func newRig(t *testing.T) *rig {
	t.Helper()
	dir := t.TempDir()
	store, err := job.Open(filepath.Join(dir, "vde.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	blobs, err := blob.NewFS(filepath.Join(dir, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range []job.Application{
		{ID: "app", QuotaLimit: 100, QuotaWindow: "P1D", RetentionDefault: "48h", MaxAttempts: 3},
		{ID: "hourly", QuotaLimit: 100, QuotaWindow: "PT1H", RetentionDefault: "48h", MaxAttempts: 3},
	} {
		if err := store.UpsertApplication(t.Context(), a, t0); err != nil {
			t.Fatal(err)
		}
	}
	r := &rig{dir: dir, store: store, blobs: blobs, fail: &failingBlobs{Store: blobs, fail: map[string]bool{}},
		m: metrics.New(false), logs: &syncBuffer{}}
	r.opts = sweeper.Options{
		JobTimeout: 5 * time.Minute, Grace: time.Minute, Metrics: r.m,
		Logger: slog.New(slog.NewTextHandler(r.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Audit:  audit.New(store, audit.Options{Clock: func() time.Time { return t0 }}),
	}
	t.Cleanup(func() {
		if logs := r.logs.String(); strings.Contains(logs, sentinel) {
			t.Errorf("content in the logs:\n%s", logs)
		}
	})
	return r
}

// scrape returns the rig's metrics in the Prometheus text format.
func (r *rig) scrape() string {
	w := httptest.NewRecorder()
	r.m.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	return w.Body.String()
}

func (r *rig) sweeper() *sweeper.Sweeper { return sweeper.New(r.store, r.fail, r.opts) }

// submit stores a job with a document blob and two pages, created at created.
func (r *rig) submit(t *testing.T, app string, retention job.Retention, created time.Time) job.Job {
	t.Helper()
	j := &job.Job{
		ID: job.NewID(), ApplicationID: app, ServiceVersion: "vde-test", Representation: "pages",
		MediaType: "application/pdf", Size: 10, PageCount: 2, Retention: retention, CreatedAt: created,
		ExtractorInstructions: sentinel, JudgeInstructions: sentinel, Schema: sentinel,
	}
	for _, name := range []string{blob.Document, blob.Page(1), blob.Page(2)} {
		if err := r.blobs.Put(t.Context(), j.ID, name, strings.NewReader(sentinel)); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.store.CreateJob(t.Context(), j); err != nil {
		t.Fatal(err)
	}
	return *j
}

func (r *rig) hasBlobs(id string) bool {
	_, err := r.blobs.Get(context.Background(), id, blob.Document)
	return err == nil
}

func (r *rig) exists(t *testing.T, id string) bool {
	t.Helper()
	_, err := r.store.GetJobAny(t.Context(), id)
	switch {
	case err == nil:
		return true
	case errors.Is(err, job.ErrNotFound):
		return false
	}
	t.Fatal(err)
	return false
}

func (r *rig) auditKinds(t *testing.T, kind audit.Kind) []job.AuditEvent {
	t.Helper()
	events, err := r.store.ListAudit(t.Context(), job.AuditFilter{Kind: string(kind)}, 0)
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func mustOnce(t *testing.T, s *sweeper.Sweeper, now time.Time) sweeper.Result {
	t.Helper()
	res, err := s.Once(t.Context(), now)
	if err != nil {
		t.Fatalf("Once: %v", err)
	}
	return res
}

func TestExpiredJobsAreRemovedWithTheirBlobs(t *testing.T) {
	r := newRig(t)
	none := r.submit(t, "app", job.RetentionNone, t0)                      // expires t0+24h
	def := r.submit(t, "app", job.RetentionDefault, t0)                    // expires t0+48h
	later := r.submit(t, "app", job.RetentionDefault, t0.Add(2*time.Hour)) // expires t0+50h
	s := r.sweeper()

	if res := mustOnce(t, s, t0.Add(23*time.Hour)); res.Expired != 0 {
		t.Fatalf("nothing is due at 23h, removed %d", res.Expired)
	}
	if res := mustOnce(t, s, t0.Add(25*time.Hour)); res.Expired != 1 {
		t.Fatalf("at 25h removed %d, want 1", res.Expired)
	}
	if r.exists(t, none.ID) || r.hasBlobs(none.ID) {
		t.Error("the retention: none job or its blobs survived its expiry")
	}
	if !r.exists(t, def.ID) || !r.hasBlobs(def.ID) {
		t.Error("an unexpired job lost its row or blobs")
	}

	if res := mustOnce(t, s, t0.Add(49*time.Hour)); res.Expired != 1 || r.exists(t, def.ID) || r.hasBlobs(def.ID) {
		t.Errorf("at 49h: %+v", res)
	}
	if !r.exists(t, later.ID) || !r.hasBlobs(later.ID) {
		t.Error("a job not yet due was removed")
	}
	if want := "vde_swept_jobs_total 2\n"; !strings.Contains(r.scrape(), want) {
		t.Errorf("metrics lack %s", want)
	}

	// Acceptance: no trace of the content in the data directory once everything is due.
	mustOnce(t, s, t0.Add(100*time.Hour))
	err := filepath.WalkDir(filepath.Join(r.dir, "blobs"), func(path string, d fs.DirEntry, err error) error {
		if err == nil && path != filepath.Join(r.dir, "blobs") {
			t.Errorf("blob store still holds %s", path)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	// The audit record stays: one job.expired per job, no content.
	events := r.auditKinds(t, audit.JobExpiredKind)
	if len(events) != 3 {
		t.Fatalf("%d job.expired events, want 3", len(events))
	}
	for _, e := range events {
		if e.Actor != audit.ActorSweeper || e.ApplicationID != "app" || e.JobID == "" || strings.Contains(string(e.Details), sentinel) {
			t.Errorf("event = %+v", e)
		}
	}
}

// TestShortRetentionExpiresContentAndKeepsRowForIdempotency: with a retention shorter
// than the idempotency window, the content and blobs go at expires_at and the row,
// metadata only, at created_at + 24h, so the key still names the job meanwhile.
func TestShortRetentionExpiresContentAndKeepsRowForIdempotency(t *testing.T) {
	r := newRig(t)
	ctx := t.Context()
	short := job.Application{ID: "short", QuotaLimit: 100, QuotaWindow: "P1D", RetentionDefault: "2s", MaxAttempts: 3}
	if err := r.store.UpsertApplication(ctx, short, t0); err != nil {
		t.Fatal(err)
	}
	j := &job.Job{
		ID: job.NewID(), ApplicationID: "short", ServiceVersion: "vde-test", Representation: "text",
		MediaType: "text/plain", Size: 10, IdempotencyKey: "k", CreatedAt: t0,
		ExtractorInstructions: sentinel, JudgeInstructions: sentinel, Schema: sentinel,
	}
	if err := r.blobs.Put(ctx, j.ID, blob.Document, strings.NewReader(sentinel)); err != nil {
		t.Fatal(err)
	}
	if err := r.store.CreateJob(ctx, j); err != nil {
		t.Fatal(err)
	}
	if _, err := r.store.LeaseNextQueued(ctx, "w", t0, t0.Add(time.Minute), nil); err != nil {
		t.Fatal(err)
	}
	if err := r.store.Finish(ctx, j.ID, "w", job.Outcome{Status: job.StatusAccepted, Attempts: 1,
		Result: json.RawMessage(`{"s":"` + sentinel + `"}`), Feedback: json.RawMessage(`[]`)}, t0.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	s := r.sweeper()

	if res := mustOnce(t, s, t0.Add(time.Second)); res.Expired != 0 || res.Removed != 0 {
		t.Fatalf("before expires_at: %+v", res)
	}
	if res := mustOnce(t, s, t0.Add(4*time.Second)); res.Expired != 1 || res.Removed != 0 {
		t.Fatalf("past expires_at: %+v, want the content expired and the row kept", res)
	}
	got, err := r.store.FindByIdempotencyKey(ctx, "short", "k")
	if err != nil {
		t.Fatalf("the key no longer names the job: %v", err)
	}
	if got.ID != j.ID || got.Status != job.StatusAccepted || got.ContentDeletedAt.IsZero() || got.Result != nil ||
		got.ExtractorInstructions != "" || r.hasBlobs(j.ID) {
		t.Errorf("kept row = %+v, blobs %v; want metadata only", got, r.hasBlobs(j.ID))
	}
	if res := mustOnce(t, s, t0.Add(job.IdempotencyWindow-time.Second)); res.Expired != 0 || res.Removed != 0 {
		t.Fatalf("inside the idempotency window: %+v", res)
	}
	if res := mustOnce(t, s, t0.Add(job.IdempotencyWindow)); res.Expired != 0 || res.Removed != 1 || r.exists(t, j.ID) {
		t.Fatalf("at created_at+24h: %+v, want the row removed", res)
	}
	if events := r.auditKinds(t, audit.JobExpiredKind); len(events) != 1 || events[0].JobID != j.ID {
		t.Errorf("job.expired events = %+v, want one for %s", events, j.ID)
	}
	if want := "vde_swept_jobs_total 1\n"; !strings.Contains(r.scrape(), want) {
		t.Errorf("metrics lack %s", want)
	}
}

func TestExpiredJobsAreRemovedInBatches(t *testing.T) {
	r := newRig(t)
	r.opts.Batch = 2
	var ids []string
	for i := range 5 {
		ids = append(ids, r.submit(t, "app", job.RetentionNone, t0.Add(time.Duration(i)*time.Minute)).ID)
	}
	if res := mustOnce(t, r.sweeper(), t0.Add(48*time.Hour)); res.Expired != 5 {
		t.Errorf("removed %d, want 5", res.Expired)
	}
	for _, id := range ids {
		if r.exists(t, id) {
			t.Errorf("%s survived", id)
		}
	}
}

func TestBlobFailureKeepsTheRowAndRetries(t *testing.T) {
	r := newRig(t)
	r.opts.Batch = 2
	var ids []string
	for i := range 4 {
		ids = append(ids, r.submit(t, "app", job.RetentionNone, t0.Add(time.Duration(i)*time.Minute)).ID)
	}
	r.fail.fail[ids[0]] = true
	r.fail.fail[ids[1]] = true // a whole batch fails: the loop must stop, not spin
	s := r.sweeper()
	now := t0.Add(48 * time.Hour)

	res, err := s.Once(t.Context(), now)
	if err == nil || !strings.Contains(err.Error(), ids[0]) {
		t.Errorf("error = %v, want one naming %s", err, ids[0])
	}
	if res.Expired != 0 || !r.exists(t, ids[0]) || !r.exists(t, ids[1]) {
		t.Errorf("result %+v: failing jobs must keep their rows", res)
	}
	r.fail.heal()
	if res := mustOnce(t, s, now); res.Expired != 4 {
		t.Errorf("after the disk recovered removed %d, want 4", res.Expired)
	}
}

func TestStaleRunningJobsAreFinishedAsTimeout(t *testing.T) {
	r := newRig(t)
	ctx := t.Context()
	stale := r.submit(t, "app", job.RetentionDefault, t0)
	fresh := r.submit(t, "app", job.RetentionDefault, t0.Add(10*time.Minute))
	young := r.submit(t, "app", job.RetentionDefault, t0.Add(20*time.Minute))
	done := r.submit(t, "app", job.RetentionDefault, t0.Add(30*time.Minute))

	lease := func(want string) {
		t.Helper()
		j, err := r.store.LeaseNextQueued(ctx, "w", t0, t0.Add(time.Minute), nil)
		if err != nil || j.ID != want {
			t.Fatalf("lease = %q, %v; want %q", j.ID, err, want)
		}
	}
	lease(stale.ID)
	lease(fresh.ID)
	// The stale job persisted one attempt before its worker disappeared.
	if err := r.store.RecordAttempt(ctx, stale.ID, "w",
		job.Progress{Attempts: 2, Result: json.RawMessage(`{"a":1}`), Feedback: json.RawMessage(`[{"x":1}]`)}, t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	lease(young.ID)
	lease(done.ID)
	if err := r.store.Finish(ctx, done.ID, "w", job.Outcome{Status: job.StatusAccepted, Attempts: 1}, t0.Add(31*time.Minute)); err != nil {
		t.Fatal(err)
	}
	s := r.sweeper()

	// Up to created+timeout+grace (6 minutes) nothing is stale.
	if res := mustOnce(t, s, t0.Add(6*time.Minute-time.Millisecond)); res.Stale != 0 {
		t.Fatalf("finished %d jobs before the limit", res.Stale)
	}
	res := mustOnce(t, s, t0.Add(6*time.Minute))
	if res.Stale != 1 {
		t.Fatalf("finished %d jobs, want 1", res.Stale)
	}
	got, err := r.store.GetJobAny(ctx, stale.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != job.StatusFailed || got.Attempts != 2 || string(got.Result) != `{"a":1}` ||
		string(got.Feedback) != `[{"x":1}]` || !got.FinishedAt.Equal(t0.Add(6*time.Minute)) {
		t.Errorf("stale job = status %s attempts %d result %s feedback %s finished %v",
			got.Status, got.Attempts, got.Result, got.Feedback, got.FinishedAt)
	}
	var jobErr struct{ Code, Message string }
	if err := json.Unmarshal(got.Error, &jobErr); err != nil || jobErr.Code != "timeout" || jobErr.Message == "" {
		t.Errorf("error = %s, %v", got.Error, err)
	}
	if other, _ := r.store.GetJobAny(ctx, fresh.ID); other.Status != job.StatusRunning {
		t.Errorf("a job inside its limit is %s", other.Status)
	}
	if other, _ := r.store.GetJobAny(ctx, done.ID); other.Status != job.StatusAccepted {
		t.Errorf("a terminal job became %s", other.Status)
	}

	events := r.auditKinds(t, audit.JobFinishedKind)
	if len(events) != 1 || events[0].JobID != stale.ID || events[0].Actor != audit.ActorSweeper {
		t.Fatalf("job.finished events = %+v", events)
	}
	d, err := audit.Decode(events[0])
	if want := (audit.JobFinished{Status: "failed", Attempts: 2, ErrorCode: "timeout", ServiceVersion: "vde-test", Verification: "none", JudgePolicy: "always"}); err != nil || d != want {
		t.Errorf("details = %+v, %v; want %+v", d, err, want)
	}

	// The sweeper finishes a job once: the next sweep finds only the job that is now over
	// its limit (fresh, created 10 minutes in; young, created at 20, is not yet).
	if res := mustOnce(t, s, t0.Add(16*time.Minute)); res.Stale != 1 {
		t.Errorf("later sweep finished %d, want only fresh", res.Stale)
	}
}

// A stale job gets the terminal-state deletion its retention calls for: the worker that
// would have done it is gone.
func TestStaleJobsLoseTheirInputsPerRetention(t *testing.T) {
	r := newRig(t)
	r.opts.DeleteInputsAtTerminal = map[string]bool{"hourly": true}
	ctx := t.Context()
	none := r.submit(t, "app", job.RetentionNone, t0)
	kept := r.submit(t, "app", job.RetentionDefault, t0.Add(time.Second))
	lean := r.submit(t, "hourly", job.RetentionDefault, t0.Add(2*time.Second))
	for range 3 {
		if _, err := r.store.LeaseNextQueued(ctx, "w", t0, t0.Add(time.Minute), nil); err != nil {
			t.Fatal(err)
		}
	}
	if res := mustOnce(t, r.sweeper(), t0.Add(10*time.Minute)); res.Stale != 3 {
		t.Fatalf("finished %d, want 3", res.Stale)
	}
	for _, tt := range []struct {
		j       job.Job
		cleared bool
	}{{none, true}, {kept, false}, {lean, true}} {
		got, err := r.store.GetJobAny(ctx, tt.j.ID)
		if err != nil {
			t.Fatal(err)
		}
		if cleared := !got.InputsDeletedAt.IsZero(); cleared != tt.cleared || (got.Schema == "") != tt.cleared || r.hasBlobs(tt.j.ID) == tt.cleared {
			t.Errorf("%s (%s): inputs cleared = %v (schema %q, blobs %v), want %v",
				tt.j.ID, tt.j.Retention, cleared, got.Schema, r.hasBlobs(tt.j.ID), tt.cleared)
		}
		if got.Status != job.StatusFailed || got.ContentDeletedAt.IsZero() == false {
			t.Errorf("%s: status %s, content deleted %v", tt.j.ID, got.Status, got.ContentDeletedAt)
		}
	}
}

func TestOldQuotaTokensAndAuditRowsAreRemoved(t *testing.T) {
	r := newRig(t)
	ctx := t.Context()
	now := t0.Add(10 * 24 * time.Hour)
	r.opts.TokenRetention = 90 * 24 * time.Hour
	r.opts.AuditRetention = 365 * 24 * time.Hour

	// Daily windows: starts at now-3d, now-2d-1ms (just too old), now-2d, now-1d, now.
	day := 24 * time.Hour
	for _, start := range []time.Time{now.Add(-3 * day), now.Add(-2*day - time.Millisecond), now.Add(-2 * day), now.Add(-day), now} {
		if _, err := r.store.IncrementQuota(ctx, "app", start, 10); err != nil {
			t.Fatal(err)
		}
	}
	// Hourly application: windows of an hour, two kept.
	for _, start := range []time.Time{now.Add(-3 * time.Hour), now.Add(-2 * time.Hour), now.Add(-time.Hour)} {
		if _, err := r.store.IncrementQuota(ctx, "hourly", start, 10); err != nil {
			t.Fatal(err)
		}
	}

	token := func(id string, expires time.Time) {
		t.Helper()
		if err := r.store.CreateToken(ctx, job.Token{ID: id, ApplicationID: "app", Hash: []byte("h"), ExpiresAt: expires, CreatedAt: t0}); err != nil {
			t.Fatal(err)
		}
	}
	token("expired-long-ago", now.Add(-91*day))
	token("expired-recently", now.Add(-89*day))
	token("revoked-long-ago", now.Add(30*day))
	token("revoked-recently", now.Add(30*day))
	token("live", now.Add(30*day))
	if err := r.store.RevokeToken(ctx, "revoked-long-ago", now.Add(-91*day)); err != nil {
		t.Fatal(err)
	}
	if err := r.store.RevokeToken(ctx, "revoked-recently", now.Add(-day)); err != nil {
		t.Fatal(err)
	}

	for _, at := range []time.Time{now.Add(-366 * day), now.Add(-364 * day), now} {
		if _, err := r.store.AppendAudit(ctx, job.AuditEvent{At: at, Kind: "config.loaded"}); err != nil {
			t.Fatal(err)
		}
	}

	res := mustOnce(t, r.sweeper(), now)
	if res.QuotaRows != 2+1 || res.Tokens != 2 || res.AuditEvents != 1 {
		t.Errorf("result = %+v, want 3 quota rows, 2 tokens, 1 audit event", res)
	}
	for start, want := range map[time.Time]int{
		now.Add(-3 * day): 0, now.Add(-2*day - time.Millisecond): 0, now.Add(-2 * day): 1, now.Add(-day): 1, now: 1,
	} {
		if n, _ := r.store.GetQuota(ctx, "app", start); n != want {
			t.Errorf("quota at %v = %d, want %d", start, n, want)
		}
	}
	for start, want := range map[time.Time]int{now.Add(-3 * time.Hour): 0, now.Add(-2 * time.Hour): 1, now.Add(-time.Hour): 1} {
		if n, _ := r.store.GetQuota(ctx, "hourly", start); n != want {
			t.Errorf("hourly quota at %v = %d, want %d", start, n, want)
		}
	}
	left, _ := r.store.ListTokens(ctx, "app")
	if len(left) != 3 {
		t.Errorf("%d tokens left, want 3", len(left))
	}
	if events, _ := r.store.ListAudit(ctx, job.AuditFilter{Kind: "config.loaded"}, 0); len(events) != 2 {
		t.Errorf("%d audit events left, want 2", len(events))
	}
}

func TestOrphanBlobsAreSweptAtStartAndDaily(t *testing.T) {
	r := newRig(t)
	s := r.sweeper()
	kept := r.submit(t, "app", job.RetentionDefault, t0)

	orphan := func() string {
		t.Helper()
		id := job.NewID()
		if err := r.blobs.Put(t.Context(), id, blob.Document, strings.NewReader(sentinel)); err != nil {
			t.Fatal(err)
		}
		return id
	}
	age := func(id string) {
		t.Helper()
		dir := filepath.Join(r.dir, "blobs", id[len(id)-2:], id)
		old := time.Now().Add(-3 * time.Hour)
		if err := os.Chtimes(dir, old, old); err != nil {
			t.Fatal(err)
		}
	}
	first, young := orphan(), orphan()
	age(first)

	res := mustOnce(t, s, t0)
	if res.Orphans != 1 || r.hasBlobs(first) {
		t.Fatalf("start sweep: %+v, orphan present %v", res, r.hasBlobs(first))
	}
	if !r.hasBlobs(young) || !r.hasBlobs(kept.ID) {
		t.Error("a young orphan (submission in progress) or a blob with a row was removed")
	}

	// Not again within the day, even for an old orphan.
	second := orphan()
	age(second)
	age(young)
	if res := mustOnce(t, s, t0.Add(23*time.Hour)); res.Orphans != 0 || !r.hasBlobs(second) {
		t.Errorf("orphan sweep ran again before a day: %+v", res)
	}
	if res := mustOnce(t, s, t0.Add(24*time.Hour)); res.Orphans != 2 || r.hasBlobs(second) || r.hasBlobs(young) {
		t.Errorf("daily sweep: %+v", res)
	}
	if !r.hasBlobs(kept.ID) {
		t.Error("the blobs of an existing job were removed as orphans")
	}
}

func TestRunSweepsRepeatedlyUntilCancelled(t *testing.T) {
	r := newRig(t)
	var mu sync.Mutex
	now := t0
	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	r.opts.Clock = clock
	r.opts.Interval = 5 * time.Millisecond
	first := r.submit(t, "app", job.RetentionNone, t0)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.sweeper().Run(ctx)
	}()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("Run did not return after cancel")
		}
	}()

	wait := func(id string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for r.exists(t, id) {
			if time.Now().After(deadline) {
				t.Fatalf("%s was not swept", id)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	// Time stands still before expiry; the job stays.
	time.Sleep(30 * time.Millisecond)
	if !r.exists(t, first.ID) {
		t.Fatal("swept before expiry")
	}
	mu.Lock()
	now = t0.Add(25 * time.Hour)
	mu.Unlock()
	wait(first.ID)

	// A job created later is swept by a later tick of the same loop.
	second := r.submit(t, "app", job.RetentionNone, t0.Add(26*time.Hour))
	mu.Lock()
	now = t0.Add(60 * time.Hour)
	mu.Unlock()
	wait(second.ID)
}

func TestNothingToSweepIsSilent(t *testing.T) {
	r := newRig(t)
	res := mustOnce(t, r.sweeper(), t0)
	if res != (sweeper.Result{}) {
		t.Errorf("result = %+v", res)
	}
	if logs := r.logs.String(); logs != "" {
		t.Errorf("an empty sweep logged:\n%s", logs)
	}
}

func TestLogsCarryCountsNeverContent(t *testing.T) {
	r := newRig(t)
	r.submit(t, "app", job.RetentionNone, t0)
	mustOnce(t, r.sweeper(), t0.Add(48*time.Hour))
	logs := r.logs.String()
	if !strings.Contains(logs, "count=1") || !strings.Contains(logs, "component=sweeper") {
		t.Errorf("log lacks the swept count:\n%s", logs)
	}
	if strings.Contains(logs, sentinel) {
		t.Errorf("content in the logs:\n%s", logs)
	}
}

func TestDefaults(t *testing.T) {
	r := newRig(t)
	r.opts = sweeper.Options{}
	s := sweeper.New(r.store, r.blobs, r.opts)
	j := r.submit(t, "app", job.RetentionNone, t0)
	if res, err := s.Once(t.Context(), t0.Add(25*time.Hour)); err != nil || res.Expired != 1 || r.exists(t, j.ID) {
		t.Errorf("default sweeper: %+v, %v", res, err)
	}
}
