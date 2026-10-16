package worker_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kaanemec/vde/internal/blob"
	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/extract"
	"github.com/kaanemec/vde/internal/job"
	"github.com/kaanemec/vde/internal/provider"
	"github.com/kaanemec/vde/internal/provider/fake"
	"github.com/kaanemec/vde/internal/worker"
)

const testConfig = `
endpoints:
  ext:
    provider: fake
    models: {ext-model: {accepts: [text/plain, image/png]}}
  rev:
    provider: fake
    models: {judge-model: {accepts: [text/plain, image/png]}}
groups:
  extraction:
    endpoints: [{endpoint: ext, model: ext-model}]
    failover: {max_tries: 1, max_total_time: 1m}
  review:
    endpoints: [{endpoint: rev, model: judge-model}]
    failover: {max_tries: 1, max_total_time: 1m}
roles:
  extractor: {group: extraction}
  judge: {group: review}
limits: {max_attempts: 3, job_timeout: 5m}
applications:
  - {id: app, name: App, retention: {default: 24h}}
  - {id: tight, name: Tight, rate: {max_in_progress: 1}}
  - {id: forced, name: Forced, retention: {force_none: true}}
`

// Content sentinels: none may appear in the logs.
const (
	sentinel     = "SENTINEL"
	instructions = "SENTINEL-INSTRUCTIONS: extract the name."
	documentText = "SENTINEL-DOCUMENT: Ada Lovelace"
	schema       = `{"type":"object","required":["name"],"properties":{"name":{"type":"string","description":"SENTINEL-SCHEMA"}}}`
	candidate    = `{"name":"SENTINEL-RESULT"}`
	reject       = `{"verdict": "reject", "feedback": {"/name": "SENTINEL-FEEDBACK"}}`
)

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

type recorder struct {
	started  chan string
	mu       sync.Mutex
	finished []job.Status
}

func (r *recorder) JobStarted(app string) {
	select {
	case r.started <- app:
	default:
	}
}

func (r *recorder) JobFinished(_ string, status job.Status, _ int, _ time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.finished = append(r.finished, status)
}

type rig struct {
	store      *job.SQLite
	blobs      *blob.FS
	cfg        *config.Config
	ext, judge *fake.Provider
	obs        *recorder
	logs       *syncBuffer
	pool       *worker.Pool
}

func newRig(t *testing.T, ext, judge fake.Script, opts worker.Options) *rig {
	t.Helper()
	cfg, err := config.Parse([]byte(testConfig), func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate().Err(false); err != nil {
		t.Fatal(err)
	}
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
	for _, a := range cfg.Applications {
		row := job.Application{
			ID: a.ID, Name: a.Name, MaxAttempts: a.MaxAttempts, QuotaLimit: a.Quota.Limit, QuotaWindow: a.Quota.Window,
			RequestsPerMinute: a.Rate.RequestsPerMinute, SubmitsPerMinute: a.Rate.SubmitsPerMinute,
			MaxInProgress: a.Rate.MaxInProgress, RetentionDefault: a.Retention.Default,
			// forced is stored unforced: the configuration forces none after submit.
		}
		if err := store.UpsertApplication(t.Context(), row, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	r := &rig{store: store, blobs: blobs, cfg: cfg, ext: fake.New(ext), judge: fake.New(judge),
		obs: &recorder{started: make(chan string, 16)}, logs: &syncBuffer{}}
	providers := map[*config.Endpoint]provider.Provider{cfg.Endpoints["ext"]: r.ext, cfg.Endpoints["rev"]: r.judge}
	roles, err := extract.NewRoles(cfg, func(e *config.Endpoint) (provider.Provider, error) { return providers[e], nil })
	if err != nil {
		t.Fatal(err)
	}
	opts.Observer = r.obs
	opts.Logger = slog.New(slog.NewTextHandler(r.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	r.pool = worker.New(store, blobs, cfg, roles, opts)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := r.pool.Stop(ctx); err != nil {
			t.Errorf("Stop: %v", err)
		}
		if logs := r.logs.String(); strings.Contains(logs, sentinel) {
			t.Errorf("content in the logs:\n%s", logs)
		}
	})
	return r
}

// submit stores a text job the way the submit handler does: blobs, then the row.
func (r *rig) submit(t *testing.T, app string, edit func(*job.Job)) job.Job {
	t.Helper()
	j := &job.Job{
		ID: job.NewID(), ApplicationID: app, ServiceVersion: "vde-test", Representation: "text",
		MediaType: "text/plain; charset=utf-8", Size: int64(len(documentText)), Retention: job.RetentionNone,
		ExtractorInstructions: instructions, JudgeInstructions: instructions, Schema: schema,
	}
	doc := []byte(documentText)
	var pages [][]byte
	if edit != nil {
		edit(j)
		if j.MediaType == "application/pdf" {
			doc = []byte("%PDF-1.7 SENTINEL-PDF")
			for range j.PageCount {
				pages = append(pages, pngPage(t))
			}
		}
	}
	if err := r.blobs.Put(t.Context(), j.ID, blob.Document, bytes.NewReader(doc)); err != nil {
		t.Fatal(err)
	}
	for i, p := range pages {
		if err := r.blobs.Put(t.Context(), j.ID, blob.Page(i+1), bytes.NewReader(p)); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.store.CreateJob(t.Context(), j); err != nil {
		t.Fatal(err)
	}
	return *j
}

func pngPage(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func (r *rig) start(t *testing.T) {
	t.Helper()
	r.pool.Start(t.Context())
	r.pool.Wake()
}

func (r *rig) waitTerminal(t *testing.T, id string) job.Job {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		j, err := r.store.GetJobAny(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		// Retention runs right after Finish; wait for it on retention: none jobs too.
		if j.Status.Terminal() && (j.Retention != job.RetentionNone || !j.InputsDeletedAt.IsZero()) {
			return j
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("job %s not terminal in time", id)
	return job.Job{}
}

func (r *rig) assertBlobs(t *testing.T, id string, present bool) {
	t.Helper()
	rc, err := r.blobs.Get(t.Context(), id, blob.Document)
	if err == nil {
		_ = rc.Close()
	}
	if present && err != nil {
		t.Errorf("document blob missing: %v", err)
	}
	if !present && !errors.Is(err, blob.ErrNotFound) {
		t.Errorf("document blob still there: %v", err)
	}
}

func errorCode(t *testing.T, j job.Job) string {
	t.Helper()
	if j.Error == nil {
		return ""
	}
	var e extract.JobError
	if err := json.Unmarshal(j.Error, &e); err != nil {
		t.Fatal(err)
	}
	return e.Code
}

func accepting() (fake.Script, fake.Script) {
	return fake.Script{{Answer: candidate, Repeat: true}}, fake.Script{{Answer: fake.AcceptVerdict, Repeat: true}}
}

func TestRetentionNoneDeletesInputs(t *testing.T) {
	ext, judge := accepting()
	r := newRig(t, ext, judge, worker.Options{})
	j := r.submit(t, "app", nil)
	r.start(t)

	got := r.waitTerminal(t, j.ID)
	if got.Status != job.StatusAccepted || got.Attempts != 1 || string(got.Result) != candidate ||
		string(got.Feedback) != "[]" || got.Error != nil || got.FinishedAt.IsZero() {
		t.Errorf("terminal row = %+v", got)
	}
	if got.ExtractorInstructions != "" || got.JudgeInstructions != "" || got.Schema != "" {
		t.Error("instructions or schema survived the terminal state")
	}
	if !got.ContentDeletedAt.IsZero() {
		t.Error("result deleted before the first read")
	}
	r.assertBlobs(t, j.ID, false)
}

func TestRetentionDefaultKeepsInputs(t *testing.T) {
	ext, judge := accepting()
	r := newRig(t, ext, judge, worker.Options{})
	kept := r.submit(t, "app", func(j *job.Job) { j.Retention = job.RetentionDefault })
	forced := r.submit(t, "forced", func(j *job.Job) { j.Retention = job.RetentionDefault })
	r.start(t)

	got := r.waitTerminal(t, kept.ID)
	if got.Status != job.StatusAccepted || got.ExtractorInstructions != instructions || !got.InputsDeletedAt.IsZero() {
		t.Errorf("retention default row = %+v", got)
	}
	r.assertBlobs(t, kept.ID, true)

	// The application forces none in the configuration: the worker deletes anyway.
	deadline := time.Now().Add(10 * time.Second)
	for {
		got, _ = r.store.GetJobAny(t.Context(), forced.ID)
		if !got.InputsDeletedAt.IsZero() || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got.Status != job.StatusAccepted || got.InputsDeletedAt.IsZero() || got.Schema != "" {
		t.Errorf("force_none row = %+v", got)
	}
	r.assertBlobs(t, forced.ID, false)
}

func TestPagesRepresentation(t *testing.T) {
	ext, judge := accepting()
	r := newRig(t, ext, judge, worker.Options{})
	j := r.submit(t, "app", func(j *job.Job) {
		j.MediaType, j.Representation, j.PageCount = "application/pdf", "pages", 2
	})
	r.start(t)

	if got := r.waitTerminal(t, j.ID); got.Status != job.StatusAccepted {
		t.Fatalf("status %s, error %s", got.Status, got.Error)
	}
	calls := r.ext.Calls()
	if len(calls) != 1 {
		t.Fatalf("%d extractor calls, want 1", len(calls))
	}
	var images int
	for _, p := range calls[0].Parts {
		if p.Kind == "image" && p.MediaType == "image/png" {
			images++
		}
	}
	if images != 2 {
		t.Errorf("extractor saw %d page images, want 2: %+v", images, calls[0].Parts)
	}
	r.assertBlobs(t, j.ID, false)
}

func TestRepresentationChangedFails(t *testing.T) {
	ext, judge := accepting()
	r := newRig(t, ext, judge, worker.Options{})
	j := r.submit(t, "app", func(j *job.Job) { j.Representation = "native" }) // config now says text
	r.start(t)
	got := r.waitTerminal(t, j.ID)
	if got.Status != job.StatusFailed || errorCode(t, got) != extract.CodeInternal || len(r.ext.Calls()) != 0 {
		t.Errorf("row = %+v, %d calls", got, len(r.ext.Calls()))
	}
	r.assertBlobs(t, j.ID, false)
}

func TestDeadlinePassedAtLease(t *testing.T) {
	ext, judge := accepting()
	r := newRig(t, ext, judge, worker.Options{})
	j := r.submit(t, "app", func(j *job.Job) { j.CreatedAt = time.Now().Add(-6 * time.Minute) })
	r.start(t)

	got := r.waitTerminal(t, j.ID)
	if got.Status != job.StatusFailed || errorCode(t, got) != extract.CodeTimeout || got.Attempts != 0 {
		t.Errorf("row = %+v", got)
	}
	if n := len(r.ext.Calls()) + len(r.judge.Calls()); n != 0 {
		t.Errorf("%d provider calls for a job past its deadline", n)
	}
	r.assertBlobs(t, j.ID, false)
}

// crash leases j as a worker that records progress and disappears; its lease is over.
func (r *rig) crash(t *testing.T, j job.Job, p job.Progress) {
	t.Helper()
	ctx := t.Context()
	past := time.Now().Add(-time.Minute)
	if _, err := r.store.LeaseNextQueued(ctx, "crashed", past, past.Add(time.Second), nil); err != nil {
		t.Fatal(err)
	}
	if err := r.store.RecordAttempt(ctx, j.ID, "crashed", p, past.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
}

func TestResumeAfterCrash(t *testing.T) {
	r := newRig(t, fake.Script{{Answer: candidate, Repeat: true}}, fake.Script{{Answer: reject, Repeat: true}}, worker.Options{})
	j := r.submit(t, "app", func(j *job.Job) { j.CreatedAt = time.Now().Add(-2 * time.Minute) })
	r.crash(t, j, job.Progress{Attempts: 2, Result: json.RawMessage(`{"name":"earlier"}`), Feedback: json.RawMessage(`[]`)})
	r.start(t)

	got := r.waitTerminal(t, j.ID)
	if got.Status != job.StatusRejected || got.Attempts != 3 || string(got.Result) != candidate {
		t.Errorf("row = %+v", got)
	}
	if n := len(r.ext.Calls()); n != 1 {
		t.Errorf("%d extractor calls after resuming at attempt 2 of 3, want 1", n)
	}
}

func TestExhaustedAtLease(t *testing.T) {
	ext, judge := accepting()
	r := newRig(t, ext, judge, worker.Options{})
	j := r.submit(t, "app", func(j *job.Job) { j.MaxAttempts = 2 })
	r.crash(t, j, job.Progress{Attempts: 2, Result: json.RawMessage(`{"name":"last"}`),
		Feedback: json.RawMessage(`[{"pointer":"/name","message":"m","source":"judge"}]`)})
	empty := r.submit(t, "app", func(j *job.Job) { j.MaxAttempts = 1 })
	r.crash(t, empty, job.Progress{Attempts: 1, Feedback: json.RawMessage(`[]`)})
	r.start(t)

	got := r.waitTerminal(t, j.ID)
	if got.Status != job.StatusRejected || got.Attempts != 2 || string(got.Result) != `{"name":"last"}` || got.Error != nil {
		t.Errorf("row = %+v", got)
	}
	got = r.waitTerminal(t, empty.ID)
	if got.Status != job.StatusFailed || errorCode(t, got) != extract.CodeExtractorFailed || got.Attempts != 1 {
		t.Errorf("row without candidate = %+v", got)
	}
	if n := len(r.ext.Calls()); n != 0 {
		t.Errorf("%d extractor calls for exhausted jobs", n)
	}
}

func TestWakeLatency(t *testing.T) {
	ext, judge := accepting()
	r := newRig(t, ext, judge, worker.Options{Size: 2, PollInterval: time.Hour})
	r.pool.Start(t.Context())
	time.Sleep(50 * time.Millisecond) // the workers found nothing and sleep for an hour

	r.submit(t, "app", nil)
	start := time.Now()
	r.pool.Wake()
	select {
	case <-r.obs.started:
		if d := time.Since(start); d >= 100*time.Millisecond {
			t.Errorf("job picked up after %v, want < 100ms", d)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("job not picked up after Wake")
	}
}

func TestMaxInProgress(t *testing.T) {
	r := newRig(t, fake.Script{{Answer: candidate, Delay: 50 * time.Millisecond, Repeat: true}},
		fake.Script{{Answer: fake.AcceptVerdict, Repeat: true}}, worker.Options{Size: 3, PollInterval: 10 * time.Millisecond})
	var ids []string
	for range 3 {
		ids = append(ids, r.submit(t, "tight", nil).ID)
	}

	ctx, cancel := context.WithCancel(t.Context())
	var maxRunning int
	var watch sync.WaitGroup
	watch.Go(func() {
		for ctx.Err() == nil {
			n, err := r.store.CountRunning(context.Background(), "tight")
			if err == nil && n > maxRunning {
				maxRunning = n
			}
			time.Sleep(2 * time.Millisecond)
		}
	})
	r.start(t)
	for _, id := range ids {
		if got := r.waitTerminal(t, id); got.Status != job.StatusAccepted {
			t.Errorf("job %s: %s", id, got.Status)
		}
	}
	cancel()
	watch.Wait()
	if maxRunning != 1 {
		t.Errorf("max running jobs of the application = %d, want 1 (max_in_progress)", maxRunning)
	}
}

func TestStopWaitsForRunningJobs(t *testing.T) {
	r := newRig(t, fake.Script{{Answer: candidate, Delay: 200 * time.Millisecond, Repeat: true}},
		fake.Script{{Answer: fake.AcceptVerdict, Repeat: true}}, worker.Options{Size: 1})
	j := r.submit(t, "app", nil)
	r.start(t)
	<-r.obs.started

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := r.pool.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if got, _ := r.store.GetJobAny(t.Context(), j.ID); got.Status != job.StatusAccepted {
		t.Errorf("status after graceful stop = %s, want accepted", got.Status)
	}
}

func TestStopAbandonsAfterGrace(t *testing.T) {
	r := newRig(t, fake.Script{{Answer: candidate, Delay: time.Minute, Repeat: true}},
		fake.Script{{Answer: fake.AcceptVerdict, Repeat: true}}, worker.Options{Size: 1})
	j := r.submit(t, "app", nil)
	r.start(t)
	<-r.obs.started

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := r.pool.Stop(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Stop = %v, want DeadlineExceeded", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("Stop took %v", d)
	}
	got, _ := r.store.GetJobAny(t.Context(), j.ID)
	if got.Status != job.StatusRunning || got.LeaseUntil.IsZero() || got.Attempts != 0 || !got.FinishedAt.IsZero() {
		t.Errorf("abandoned row = %+v, want running with its lease", got)
	}
	r.assertBlobs(t, j.ID, true)
}

// TestHeartbeatKeepsLease: a job running longer than the lease is not leased a second
// time by another worker, because the running worker renews it.
func TestHeartbeatKeepsLease(t *testing.T) {
	r := newRig(t, fake.Script{{Answer: candidate, Delay: 300 * time.Millisecond, Repeat: true}},
		fake.Script{{Answer: fake.AcceptVerdict, Repeat: true}},
		worker.Options{Size: 2, PollInterval: 10 * time.Millisecond, Lease: 60 * time.Millisecond})
	j := r.submit(t, "app", nil)
	r.start(t)
	if got := r.waitTerminal(t, j.ID); got.Status != job.StatusAccepted {
		t.Errorf("status %s", got.Status)
	}
	if n := len(r.ext.Calls()); n != 1 {
		t.Errorf("%d extractor calls, want 1: the lease was taken while the job ran", n)
	}
}

// TestLeaseLostAbandonsRun: when the job is finished elsewhere (the sweeper), the
// worker's next renewal fails and it stops without writing.
func TestLeaseLostAbandonsRun(t *testing.T) {
	r := newRig(t, fake.Script{{Answer: candidate, Delay: time.Minute, Repeat: true}},
		fake.Script{{Answer: fake.AcceptVerdict, Repeat: true}},
		worker.Options{Size: 1, Lease: 30 * time.Millisecond})
	j := r.submit(t, "app", nil)
	r.start(t)
	<-r.obs.started
	timeout := job.Outcome{Status: job.StatusFailed, Error: json.RawMessage(`{"code":"timeout","message":"m"}`), Feedback: json.RawMessage(`[]`)}
	if err := r.store.Finish(t.Context(), j.ID, "", timeout, time.Now()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(r.logs.String(), "lease lost") {
		if time.Now().After(deadline) {
			t.Fatal("worker did not notice the lost lease")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got, _ := r.store.GetJobAny(t.Context(), j.ID); got.Status != job.StatusFailed || errorCode(t, got) != "timeout" {
		t.Errorf("row = %+v", got)
	}
}
