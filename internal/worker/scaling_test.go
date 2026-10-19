package worker_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kaanemec/vde/internal/blob"
	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/extract"
	"github.com/kaanemec/vde/internal/job"
	"github.com/kaanemec/vde/internal/provider"
	"github.com/kaanemec/vde/internal/worker"
)

// docRe finds the marker of a test document in a request.
var docRe = regexp.MustCompile(`DOC-\d+`)

// countingProvider serves both roles for every pool of a test, like the providers shared
// by replicas: it counts the extractor and judge calls per document marker. The first
// extractor call on the document named block waits until its context is done (a worker
// killed mid-call), after signalling on blocked.
type countingProvider struct {
	block   string
	blocked chan struct{}

	mu        sync.Mutex
	extractor map[string]int
	judge     map[string]int
}

func (p *countingProvider) Complete(ctx context.Context, req provider.Request) (provider.Response, error) {
	var text strings.Builder
	for _, part := range req.Parts {
		text.WriteString(part.Text)
	}
	doc := docRe.FindString(text.String())
	judge := strings.HasPrefix(req.System, "You are the judge")
	p.mu.Lock()
	counts := p.extractor
	if judge {
		counts = p.judge
	}
	counts[doc]++
	first := counts[doc] == 1
	p.mu.Unlock()
	if !judge && doc == p.block && first {
		close(p.blocked)
		<-ctx.Done()
		return provider.Response{}, ctx.Err()
	}
	time.Sleep(2 * time.Millisecond) // a little work, so both pools get some
	answer := `{"name":"Ada"}`
	if judge {
		answer = `{"verdict":"accept"}`
	}
	return provider.Response{Text: answer, InputTokens: 10, OutputTokens: 2}, nil
}

func (p *countingProvider) counts(doc string) (extractor, judge int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.extractor[doc], p.judge[doc]
}

// scaled is several worker pools ("processes") on one PostgreSQL schema and one blob
// directory.
type scaled struct {
	dsn       string
	blobs     *blob.FS
	cfg       *config.Config
	provider  *countingProvider
	submitter *job.DB
}

func newScaled(t *testing.T, block string) *scaled {
	t.Helper()
	base := os.Getenv("VDE_TEST_POSTGRES")
	if base == "" {
		t.Skip("VDE_TEST_POSTGRES is not set")
	}
	schema := fmt.Sprintf("vde_worker_test_%d_%d", os.Getpid(), time.Now().UnixNano()%1e9)
	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.ExecContext(t.Context(), `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.ExecContext(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
		_ = admin.Close()
	})
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	cfg, err := config.Parse([]byte(testConfig), func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	blobs, err := blob.NewFS(filepath.Join(t.TempDir(), "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	s := &scaled{dsn: u.String(), blobs: blobs, cfg: cfg, provider: &countingProvider{
		block: block, blocked: make(chan struct{}), extractor: map[string]int{}, judge: map[string]int{},
	}}
	s.submitter = s.open(t)
	if err := s.submitter.UpsertApplication(t.Context(), job.Application{ID: "app", Name: "App", MaxAttempts: 3,
		QuotaLimit: 1000, QuotaWindow: "P1D", RequestsPerMinute: 300, SubmitsPerMinute: 300, MaxInProgress: 0,
		RetentionDefault: "24h"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	return s
}

// open is one process's connection pool.
func (s *scaled) open(t *testing.T) *job.DB {
	t.Helper()
	store, err := job.OpenPostgres(t.Context(), s.dsn, 8)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// pool starts a worker pool with its own store, as a separate process would; cancel
// kills it at once (no finish, the lease stays until it expires).
func (s *scaled) pool(t *testing.T, id string, lease time.Duration) (stop func()) {
	t.Helper()
	store := s.open(t)
	roles, err := extract.NewRoles(s.cfg, func(*config.Endpoint) (provider.Provider, error) { return s.provider, nil })
	if err != nil {
		t.Fatal(err)
	}
	cfg := *s.cfg // one process's worker settings
	cfg.Worker = config.Worker{Size: 4, PollInterval: 20 * time.Millisecond, Lease: lease}
	p := worker.New(store, s.blobs, &cfg, roles, worker.Options{WorkerID: id, Logger: slog.New(slog.DiscardHandler)})
	ctx, cancel := context.WithCancel(context.Background())
	p.Start(ctx)
	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			_ = p.Stop(context.Background())
		})
	}
	t.Cleanup(stop)
	return stop
}

func (s *scaled) submit(t *testing.T, n int) job.Job {
	t.Helper()
	doc := fmt.Sprintf("Booking DOC-%d for Ada", n)
	j := &job.Job{
		ID: job.NewID(), ApplicationID: "app", ServiceVersion: "vde-test", Representation: "text",
		MediaType: "text/plain; charset=utf-8", Size: int64(len(doc)), Retention: job.RetentionDefault,
		ExtractorInstructions: "extract", JudgeInstructions: "judge", Schema: schema,
	}
	if err := s.blobs.Put(t.Context(), j.ID, blob.Document, bytes.NewReader([]byte(doc))); err != nil {
		t.Fatal(err)
	}
	if err := s.submitter.CreateJob(t.Context(), j); err != nil {
		t.Fatal(err)
	}
	return *j
}

func (s *scaled) waitTerminal(t *testing.T, id string, within time.Duration) job.Job {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		j, err := s.submitter.GetJobAny(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if j.Status.Terminal() {
			return j
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %s still %s after %s", id, j.Status, within)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestTwoPoolsProcessEachJobOnce is the acceptance of job 07-01: two worker pools, each
// with its own connection pool (two processes), on one PostgreSQL database run 100 jobs;
// each is processed exactly once (one extractor and one judge call, one terminal write),
// and both pools take part.
func TestTwoPoolsProcessEachJobOnce(t *testing.T) {
	s := newScaled(t, "")
	const jobs = 100
	ids := make([]string, jobs)
	for i := range jobs {
		ids[i] = s.submit(t, i).ID
	}
	s.pool(t, "proc-a", 2*time.Minute)
	s.pool(t, "proc-b", 2*time.Minute)

	byProcess := map[string]int{}
	for i, id := range ids {
		j := s.waitTerminal(t, id, 30*time.Second)
		if j.Status != job.StatusAccepted || j.Attempts != 1 {
			t.Errorf("job %d: %s after %d attempts, want accepted after 1", i, j.Status, j.Attempts)
		}
		if j.InputTokens != 20 || j.OutputTokens != 4 {
			t.Errorf("job %d: usage %d/%d, want 20/4 (one extractor and one judge call)", i, j.InputTokens, j.OutputTokens)
		}
		process, _, _ := strings.Cut(j.WorkerID, "/")
		byProcess[process]++
		if ext, jdg := s.provider.counts(fmt.Sprintf("DOC-%d", i)); ext != 1 || jdg != 1 {
			t.Errorf("job %d: %d extractor and %d judge calls, want exactly 1 each", i, ext, jdg)
		}
	}
	if byProcess["proc-a"] == 0 || byProcess["proc-b"] == 0 || byProcess["proc-a"]+byProcess["proc-b"] != jobs {
		t.Errorf("jobs by process = %v, want both processes to share all %d", byProcess, jobs)
	}
}

// TestKilledWorkerResumesElsewhere: a process killed mid-job leaves its lease; the job is
// leased again by the other process once the lease expired and finishes there.
func TestKilledWorkerResumesElsewhere(t *testing.T) {
	const lease = 2 * time.Second
	s := newScaled(t, "DOC-0")
	j := s.submit(t, 0)
	killA := s.pool(t, "proc-a", lease)
	select {
	case <-s.provider.blocked: // proc-a is inside the extractor call
	case <-time.After(10 * time.Second):
		t.Fatal("proc-a did not lease the job")
	}
	s.pool(t, "proc-b", lease)
	killed := time.Now()
	killA()

	got := s.waitTerminal(t, j.ID, 3*lease)
	took := time.Since(killed)
	if got.Status != job.StatusAccepted || !strings.HasPrefix(got.WorkerID, "proc-b/") {
		t.Errorf("job = %s by %s, want accepted by proc-b", got.Status, got.WorkerID)
	}
	// The lease was renewed at most lease/3 before the kill, so it expired within lease;
	// add the poll interval and the run itself.
	if took > lease+time.Second {
		t.Errorf("resumed and finished %s after the kill, want within the %s lease (plus 1 s)", took, lease)
	}
	if ext, jdg := s.provider.counts("DOC-0"); ext != 2 || jdg != 1 {
		t.Errorf("%d extractor and %d judge calls, want 2 (the killed one and the resumed one) and 1", ext, jdg)
	}
}
