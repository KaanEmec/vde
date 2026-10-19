package worker_test

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kaanemec/vde/internal/audit"
	"github.com/kaanemec/vde/internal/blob"
	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/document"
	"github.com/kaanemec/vde/internal/extract"
	"github.com/kaanemec/vde/internal/job"
	"github.com/kaanemec/vde/internal/metrics"
	"github.com/kaanemec/vde/internal/pdf"
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
  - {id: lean, name: Lean, retention: {default: 24h, delete_blobs_at_terminal: true}}
`

// Content sentinels: none may appear in the logs.
const (
	sentinel     = "SENTINEL"
	instructions = "SENTINEL-INSTRUCTIONS: extract the name."
	documentText = "SENTINEL-DOCUMENT: Ada Lovelace, SENTINEL-RESULT" // grounds candidate
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

type rig struct {
	store      *job.DB
	blobs      *blob.FS
	cfg        *config.Config
	ext, judge *fake.Provider
	metrics    *metrics.Metrics
	logs       *syncBuffer
	pool       *worker.Pool
}

func newRig(t *testing.T, ext, judge fake.Script, opts worker.Options, w ...config.Worker) *rig {
	t.Helper()
	return newRigConfig(t, testConfig, ext, judge, opts, w...)
}

// newRigConfig is newRig on another configuration with the endpoints ext and rev; every
// other endpoint answers with the ext fake.
// The non-zero fields of w replace the worker settings after validation, which refuses
// the short leases and polls some tests need.
func newRigConfig(t *testing.T, yaml string, ext, judge fake.Script, opts worker.Options, w ...config.Worker) *rig {
	t.Helper()
	cfg, err := config.Parse([]byte(yaml), func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate().Err(false); err != nil {
		t.Fatal(err)
	}
	for _, w := range w {
		cfg.Worker.Size = cmp.Or(w.Size, cfg.Worker.Size)
		cfg.Worker.PollInterval = cmp.Or(w.PollInterval, cfg.Worker.PollInterval)
		cfg.Worker.Lease = cmp.Or(w.Lease, cfg.Worker.Lease)
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
		metrics: metrics.New(false), logs: &syncBuffer{}}
	providers := map[*config.Endpoint]provider.Provider{cfg.Endpoints["rev"]: r.judge}
	roles, err := extract.NewRoles(cfg, func(e *config.Endpoint) (provider.Provider, error) {
		if p, ok := providers[e]; ok {
			return p, nil
		}
		return r.ext, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	opts.Metrics = r.metrics
	opts.Logger = slog.New(slog.NewTextHandler(r.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	opts.Audit = audit.New(store, audit.Options{Logger: opts.Logger})
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

func TestDeleteBlobsAtTerminalForRetentionDefault(t *testing.T) {
	ext, judge := accepting()
	r := newRig(t, ext, judge, worker.Options{})
	lean := r.submit(t, "lean", func(j *job.Job) { j.Retention = job.RetentionDefault })
	r.start(t)

	deadline := time.Now().Add(10 * time.Second)
	var got job.Job
	for got.InputsDeletedAt.IsZero() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
		got, _ = r.store.GetJobAny(t.Context(), lean.ID)
	}
	// The result stays readable until expiry; only the inputs are gone.
	if got.Status != job.StatusAccepted || got.InputsDeletedAt.IsZero() || got.Schema != "" || got.ExtractorInstructions != "" ||
		string(got.Result) != candidate || !got.ContentDeletedAt.IsZero() || got.Retention != job.RetentionDefault {
		t.Errorf("delete_blobs_at_terminal row = %+v", got)
	}
	r.assertBlobs(t, lean.ID, false)
}

func TestJobFinishedIsAudited(t *testing.T) {
	ext, judge := accepting()
	r := newRig(t, ext, judge, worker.Options{})
	j := r.submit(t, "app", nil)
	r.start(t)
	r.waitTerminal(t, j.ID)

	events, err := r.store.ListAudit(t.Context(), job.AuditFilter{JobID: j.ID}, 0)
	if err != nil || len(events) != 1 {
		t.Fatalf("audit events = %d, %v", len(events), err)
	}
	e := events[0]
	d, err := audit.Decode(e)
	// The fake reports 100 input and 20 output tokens per answer: one extractor and one
	// judge call.
	want := audit.JobFinished{Status: "accepted", Attempts: 1, ServiceVersion: "vde-test", InputTokens: 200, OutputTokens: 40,
		Verification: "judge", JudgePolicy: "always"}
	if e.Kind != "job.finished" || e.Actor != audit.ActorWorker || e.ApplicationID != "app" || err != nil || d != want {
		t.Errorf("event = %+v, details %+v (%v), want %+v", e, d, err, want)
	}
	if strings.Contains(string(e.Details), sentinel) {
		t.Errorf("content in the audit details: %s", e.Details)
	}
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

// TestEscalationGroupCapabilities: the worker selects the representation as submit does,
// from what every group of a role reads (extract.RoleCapabilities). The extractor's own
// group reads PDFs natively but its escalation group only page images, so the job is
// submitted as pages and the worker must agree.
func TestEscalationGroupCapabilities(t *testing.T) {
	ext, judge := accepting()
	r := newRigConfig(t, `
endpoints:
  ext: {provider: fake, models: {ext-model: {accepts: [text/plain, application/pdf, image/png]}}}
  big: {provider: fake, models: {big-model: {accepts: [text/plain, image/png]}}}
  rev: {provider: fake, models: {judge-model: {accepts: [text/plain, application/pdf, image/png]}}}
groups:
  extraction: {endpoints: [{endpoint: ext, model: ext-model}], failover: {max_tries: 1, max_total_time: 1m}}
  strong: {endpoints: [{endpoint: big, model: big-model}], failover: {max_tries: 1, max_total_time: 1m}}
  review: {endpoints: [{endpoint: rev, model: judge-model}], failover: {max_tries: 1, max_total_time: 1m}}
roles:
  extractor: {group: extraction, escalation: [{from_attempt: 2, group: strong}]}
  judge: {group: review}
limits: {max_attempts: 3, job_timeout: 5m}
applications:
  - {id: app, name: App, retention: {default: 24h}}
`, ext, judge, worker.Options{})
	j := r.submit(t, "app", func(j *job.Job) {
		j.MediaType, j.Representation, j.PageCount = "application/pdf", "pages", 2
	})
	r.start(t)
	if got := r.waitTerminal(t, j.ID); got.Status != job.StatusAccepted {
		t.Fatalf("status %s, error %s; want accepted on the pages representation", got.Status, got.Error)
	}
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
	r.crash(t, j, job.Progress{Attempts: 2, Result: json.RawMessage(`{"name":"earlier"}`), Feedback: json.RawMessage(`[]`),
		InputTokens: 1000, OutputTokens: 300})
	r.start(t)

	got := r.waitTerminal(t, j.ID)
	if got.Status != job.StatusRejected || got.Attempts != 3 || string(got.Result) != candidate {
		t.Errorf("row = %+v", got)
	}
	if n := len(r.ext.Calls()); n != 1 {
		t.Errorf("%d extractor calls after resuming at attempt 2 of 3, want 1", n)
	}
	// The usage persisted by the crashed run plus this run's two answered calls.
	if got.InputTokens != 1000+2*fake.InputTokens || got.OutputTokens != 300+2*fake.OutputTokens {
		t.Errorf("usage = %d/%d, want the crashed run's 1000/300 plus this run's", got.InputTokens, got.OutputTokens)
	}
}

func TestResumeCarriesPriorCandidateAndFeedback(t *testing.T) {
	r := newRig(t, fake.Script{{Answer: candidate, Repeat: true}}, fake.Script{{Answer: reject, Repeat: true}}, worker.Options{})
	j := r.submit(t, "app", nil)
	r.crash(t, j, job.Progress{Attempts: 1, Result: json.RawMessage(`{"name":"earlier"}`),
		Feedback: json.RawMessage(`[{"pointer":"/name","message":"PRIOR-FEEDBACK","source":"judge"}]`)})
	r.start(t)

	got := r.waitTerminal(t, j.ID)
	if got.Status != job.StatusRejected || got.Attempts != 3 {
		t.Errorf("row = %+v, want rejected after 3 attempts (the limit)", got)
	}
	calls := r.ext.Calls()
	if len(calls) != 2 {
		t.Fatalf("%d extractor calls after 1 persisted attempt of 3, want 2", len(calls))
	}
	var text []string
	for _, p := range calls[0].Request.Parts {
		text = append(text, p.Text)
	}
	all := strings.Join(text, "\n")
	for _, want := range []string{"Previous candidate:\n\n" + `{"name":"earlier"}`, "Feedback:", `- "/name" (judge): PRIOR-FEEDBACK`} {
		if !strings.Contains(all, want) {
			t.Errorf("first request after the resume lacks %q:\n%s", want, all)
		}
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
	r := newRig(t, ext, judge, worker.Options{}, config.Worker{Size: 2, PollInterval: time.Hour})
	r.pool.Start(t.Context())
	time.Sleep(50 * time.Millisecond) // the workers found nothing and sleep for an hour

	r.submit(t, "app", nil)
	start := time.Now()
	r.pool.Wake()
	for len(r.ext.Calls()) == 0 {
		if time.Since(start) > 2*time.Second {
			t.Fatal("job not picked up after Wake")
		}
		time.Sleep(time.Millisecond)
	}
	if d := time.Since(start); d >= 100*time.Millisecond {
		t.Errorf("job picked up after %v, want < 100ms", d)
	}
}

func TestMaxInProgress(t *testing.T) {
	r := newRig(t, fake.Script{{Answer: candidate, Delay: 50 * time.Millisecond, Repeat: true}},
		fake.Script{{Answer: fake.AcceptVerdict, Repeat: true}}, worker.Options{}, config.Worker{Size: 3, PollInterval: 10 * time.Millisecond})
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
		fake.Script{{Answer: fake.AcceptVerdict, Repeat: true}}, worker.Options{}, config.Worker{Size: 1})
	j := r.submit(t, "app", nil)
	r.start(t)
	r.waitStarted(t)

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
		fake.Script{{Answer: fake.AcceptVerdict, Repeat: true}}, worker.Options{}, config.Worker{Size: 1})
	j := r.submit(t, "app", nil)
	r.start(t)
	r.waitStarted(t)

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
	// A 600 ms lease renews every 200 ms, leaving 400 ms of slack for scheduler noise under
	// -race on a shared runner; the job outlives several leases.
	r := newRig(t, fake.Script{{Answer: candidate, Delay: 1500 * time.Millisecond, Repeat: true}},
		fake.Script{{Answer: fake.AcceptVerdict, Repeat: true}},
		worker.Options{}, config.Worker{Size: 2, PollInterval: 10 * time.Millisecond, Lease: 600 * time.Millisecond})
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
		worker.Options{}, config.Worker{Size: 1, Lease: 30 * time.Millisecond})
	j := r.submit(t, "app", nil)
	r.start(t)
	r.waitStarted(t)
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

// TestJudgePolicies: each job runs under its stored judge policy (ADR 0009) and ends with
// the expected status and verification in the store, with one judge decision observed.
// The application samples nothing (sample_rate 0) unless the case says otherwise; the
// sample key is the job id, so a rate of 1 always samples.
func TestJudgePolicies(t *testing.T) {
	const ungrounded = `{"name":"SENTINEL-INVENTED"}`
	for _, tt := range []struct {
		name             string
		policy           string
		rate             float64
		answer           string
		wantStatus       job.Status
		wantVerification string
		wantExtractor    int
		wantJudge        int
		wantDecision     string
	}{
		{"always", "always", 0, candidate, job.StatusAccepted, job.VerificationJudge, 1, 1, `vde_judge_decisions_total{decision="judged",policy="always"} 1`},
		{"never", "never", 0, candidate, job.StatusExtracted, job.VerificationNone, 1, 0, `vde_judge_decisions_total{decision="skipped",policy="never"} 1`},
		{"auto grounded", "auto", 0, candidate, job.StatusAccepted, job.VerificationGrounded, 1, 0, `vde_judge_decisions_total{decision="grounded",policy="auto"} 1`},
		{"auto sampled", "auto", 1, candidate, job.StatusAccepted, job.VerificationJudge, 1, 1, `vde_judge_decisions_total{decision="sampled",policy="auto"} 1`},
		// Repair, then still ungrounded: the judge decides, never grounding.
		{"auto ungrounded", "auto", 0, ungrounded, job.StatusAccepted, job.VerificationJudge, 2, 1, `vde_judge_decisions_total{decision="judged",policy="auto"} 1`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t, fake.Script{{Answer: tt.answer, Repeat: true}}, fake.Script{{Answer: fake.AcceptVerdict, Repeat: true}}, worker.Options{})
			app, err := r.store.GetApplication(t.Context(), "app")
			if err != nil {
				t.Fatal(err)
			}
			app.JudgeSampleRate = &tt.rate
			if err := r.store.UpsertApplication(t.Context(), app, time.Now()); err != nil {
				t.Fatal(err)
			}
			j := r.submit(t, "app", func(j *job.Job) { j.JudgePolicy = tt.policy; j.Retention = job.RetentionDefault })
			r.start(t)
			got := r.waitTerminal(t, j.ID)
			if got.Status != tt.wantStatus || got.Verification != tt.wantVerification || got.JudgePolicy != tt.policy {
				t.Errorf("row %s, verification %q, policy %q; want %s, %s, %s", got.Status, got.Verification, got.JudgePolicy,
					tt.wantStatus, tt.wantVerification, tt.policy)
			}
			if len(r.ext.Calls()) != tt.wantExtractor || len(r.judge.Calls()) != tt.wantJudge {
				t.Errorf("calls: extractor %d, judge %d; want %d, %d", len(r.ext.Calls()), len(r.judge.Calls()), tt.wantExtractor, tt.wantJudge)
			}
			waitFor(t, func() bool { return strings.Contains(r.scrape(t), "vde_judge_decisions_total{") })
			if out := r.scrape(t); !strings.Contains(out, tt.wantDecision+"\n") ||
				!strings.Contains(out, "vde_grounding_disagreements_total 0\n") {
				t.Errorf("metrics lack %s:\n%s", tt.wantDecision, out)
			}
			var feedback []extract.Feedback
			if err := json.Unmarshal(got.Feedback, &feedback); err != nil {
				t.Fatal(err)
			}
			for _, f := range feedback {
				if f.Source == extract.SourceGrounding && tt.policy == "always" {
					t.Errorf("grounding entry %+v in an always job's feedback", f)
				}
			}
		})
	}
}

// TestResumeAfterGroundingRepair: a job that crashed right after its grounding repair
// was persisted resumes without a second repair (the persisted grounding entries say it
// was made): the judge is asked on the next candidate, and the always job's final
// feedback carries no grounding entry.
func TestResumeAfterGroundingRepair(t *testing.T) {
	r := newRig(t, fake.Script{{Answer: `{"name":"SENTINEL-INVENTED"}`, Repeat: true}},
		fake.Script{{Answer: fake.AcceptVerdict, Repeat: true}}, worker.Options{})
	j := r.submit(t, "app", func(j *job.Job) { j.Retention = job.RetentionDefault })
	r.crash(t, j, job.Progress{Attempts: 1, Result: json.RawMessage(`{"name":"SENTINEL-INVENTED"}`),
		Feedback: json.RawMessage(`[{"pointer":"/name","message":"value not found in the document","source":"grounding"}]`)})
	r.start(t)

	got := r.waitTerminal(t, j.ID)
	if got.Status != job.StatusAccepted || got.Attempts != 2 || got.Verification != job.VerificationJudge {
		t.Errorf("row %s after %d attempts, verification %q; want accepted after 2, judge", got.Status, got.Attempts, got.Verification)
	}
	if len(r.ext.Calls()) != 1 || len(r.judge.Calls()) != 1 {
		t.Errorf("calls: extractor %d, judge %d; want 1, 1 (no second repair)", len(r.ext.Calls()), len(r.judge.Calls()))
	}
	if string(got.Feedback) != "[]" {
		t.Errorf("feedback %s, want none", got.Feedback)
	}
}

// TestTimeoutAfterGroundingRepairDropsEntries: an always job whose deadline passed after
// a persisted repair ends failed without the repair's grounding entries.
func TestTimeoutAfterGroundingRepairDropsEntries(t *testing.T) {
	ext, judge := accepting()
	r := newRig(t, ext, judge, worker.Options{})
	j := r.submit(t, "app", func(j *job.Job) { j.CreatedAt = time.Now().Add(-time.Hour); j.Retention = job.RetentionDefault })
	r.crash(t, j, job.Progress{Attempts: 1, Result: json.RawMessage(`{"name":"x"}`),
		Feedback: json.RawMessage(`[{"pointer":"/name","message":"value not found in the document","source":"grounding"}]`)})
	r.start(t)
	got := r.waitTerminal(t, j.ID)
	if got.Status != job.StatusFailed || errorCode(t, got) != extract.CodeTimeout || string(got.Feedback) != "[]" {
		t.Errorf("row %s (%s), feedback %s; want failed timeout without grounding entries", got.Status, errorCode(t, got), got.Feedback)
	}
}

// waitStarted waits until a worker leased a job and called the extractor.
func (r *rig) waitStarted(t *testing.T) {
	t.Helper()
	waitFor(t, func() bool { return len(r.ext.Calls()) > 0 })
}

// scrape returns the rig's metrics in the Prometheus text format.
func (r *rig) scrape(t *testing.T) string {
	t.Helper()
	w := httptest.NewRecorder()
	r.metrics.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	return w.Body.String()
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatal("condition not met in time")
}

// stubPDF is a document.PDFEngine stub: every PDF has one page whose text is text and
// which renders to a 2x2 PNG.
type stubPDF struct {
	text  string
	opens atomic.Int32
	busy  atomic.Int32 // Opens that report pdf.ErrBusy before one succeeds
}

func (e *stubPDF) Open(context.Context, []byte) (document.PDFDoc, error) {
	e.opens.Add(1)
	if e.busy.Add(-1) >= 0 {
		return nil, fmt.Errorf("%w: no instance free", pdf.ErrBusy)
	}
	return stubPDFDoc{e}, nil
}

type stubPDFDoc struct{ e *stubPDF }

func (stubPDFDoc) PageCount() int                              { return 1 }
func (d stubPDFDoc) Text(context.Context, int) (string, error) { return d.e.text, nil }
func (stubPDFDoc) Render(context.Context, int, int) ([]byte, error) {
	var b bytes.Buffer
	err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2)))
	return b.Bytes(), err
}
func (stubPDFDoc) Close() {}

// TestPDFPreparedByService: a PDF without pages for groups that read no application/pdf
// is selected again with the PDF engine (ADR 0010): a text-rich one is sent as its text
// layer, a thin one as its rendered pages, and both are grounded on the text layer, so
// under auto the candidate is accepted without a judge.
func TestPDFPreparedByService(t *testing.T) {
	rich := strings.Repeat("Booking for SENTINEL-RESULT. ", 10)
	for _, tt := range []struct {
		name, policy, text, label string
	}{
		{"text layer", "always", rich, "text+text_layer"},
		{"text layer auto", "auto", rich, "text+text_layer"},
		{"rendered", "always", "SENTINEL-RESULT", "pages+rendered"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ext, judge := accepting()
			engine := &stubPDF{text: tt.text}
			r := newRig(t, ext, judge, worker.Options{PDF: engine})
			j := r.submit(t, "app", func(j *job.Job) {
				j.MediaType, j.Representation, j.JudgePolicy = "application/pdf", tt.label, tt.policy
			})
			r.start(t)
			got := r.waitTerminal(t, j.ID)
			wantVerification, wantJudge := job.VerificationJudge, 1
			if tt.policy == "auto" {
				wantVerification, wantJudge = job.VerificationGrounded, 0
			}
			if got.Status != job.StatusAccepted || got.Verification != wantVerification || len(r.judge.Calls()) != wantJudge {
				t.Fatalf("row %s (%s), verification %q, %d judge call(s); want accepted, %s, %d",
					got.Status, got.Error, got.Verification, len(r.judge.Calls()), wantVerification, wantJudge)
			}
			calls := r.ext.Calls()
			if len(calls) != 1 {
				t.Fatalf("%d extractor calls, want 1", len(calls))
			}
			if tt.label == "text+text_layer" && !hasPart(calls[0], "--- page 1 ---") {
				t.Error("the extractor did not read the text layer")
			}
			if tt.label == "pages+rendered" && !slices.ContainsFunc(calls[0].Parts, func(p fake.PartInfo) bool {
				return p.Kind == "image" && p.MediaType == "image/png"
			}) {
				t.Errorf("the extractor did not read the rendered page: %+v", calls[0].Parts)
			}
			if engine.opens.Load() != 1 {
				t.Errorf("the PDF was opened %d times, want once (selection reads the text layer)", engine.opens.Load())
			}
		})
	}
}

// TestPDFEngineBusy: a PDF engine with no instance free (pdf.ErrBusy) costs the job no
// attempt and does not fail it: the lease is released, the job is leased again and runs
// once an instance is free.
func TestPDFEngineBusy(t *testing.T) {
	ext, judge := accepting()
	engine := &stubPDF{text: strings.Repeat("Booking for SENTINEL-RESULT. ", 10)}
	engine.busy.Store(2)
	r := newRig(t, ext, judge, worker.Options{PDF: engine})
	j := r.submit(t, "app", func(j *job.Job) {
		j.MediaType, j.Representation, j.Retention = "application/pdf", "text+text_layer", job.RetentionDefault
	})
	r.start(t)
	got := r.waitTerminal(t, j.ID)
	if got.Status != job.StatusAccepted || got.Attempts != 1 || len(r.ext.Calls()) != 1 {
		t.Fatalf("row %s (%s) after %d attempts, %d extractor calls; want accepted after 1", got.Status, got.Error, got.Attempts, len(r.ext.Calls()))
	}
	if n := engine.opens.Load(); n != 3 {
		t.Errorf("the PDF was opened %d times, want 3 (two busy, then one)", n)
	}
	if logs := r.logs.String(); !strings.Contains(logs, "pdf engine busy") || strings.Contains(logs, "cannot prepare the job") {
		t.Errorf("logs:\n%s", logs)
	}
}

// TestResumeKeepsGroundingRepairFlag: a job whose persisted progress says it made its
// grounding repair, while its saved feedback is the judge's, resumes without a second
// repair (migration 0010): the next ungrounded candidate goes to the judge.
func TestResumeKeepsGroundingRepairFlag(t *testing.T) {
	r := newRig(t, fake.Script{{Answer: `{"name":"SENTINEL-INVENTED"}`, Repeat: true}},
		fake.Script{{Answer: fake.AcceptVerdict, Repeat: true}}, worker.Options{})
	j := r.submit(t, "app", func(j *job.Job) { j.Retention = job.RetentionDefault })
	r.crash(t, j, job.Progress{Attempts: 1, Result: json.RawMessage(`{"name":"SENTINEL-INVENTED"}`), GroundingRepaired: true,
		Feedback: json.RawMessage(`[{"pointer":"/name","message":"wrong","source":"judge"}]`)})
	r.start(t)
	got := r.waitTerminal(t, j.ID)
	if got.Status != job.StatusAccepted || got.Attempts != 2 || len(r.ext.Calls()) != 1 || len(r.judge.Calls()) != 1 {
		t.Errorf("row %s after %d attempts, %d extractor and %d judge calls; want accepted after 2, 1 and 1 (no second repair)",
			got.Status, got.Attempts, len(r.ext.Calls()), len(r.judge.Calls()))
	}
}

func hasPart(c fake.Call, substr string) bool {
	for _, p := range c.Request.Parts {
		if strings.Contains(p.Text, substr) {
			return true
		}
	}
	return false
}
