package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kaanemec/vde/internal/audit"
	"github.com/kaanemec/vde/internal/blob"
	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/document"
	"github.com/kaanemec/vde/internal/extract"
	"github.com/kaanemec/vde/internal/job"
)

// Defaults for Options.
const (
	DefaultSize         = 4
	DefaultPollInterval = 500 * time.Millisecond
	DefaultLease        = time.Minute
	defaultJobTimeout   = 5 * time.Minute // contract §6
	finishTimeout       = 10 * time.Second
)

// Observer receives job metrics (epic 07). Labels must come from configuration ids.
type Observer interface {
	// JobStarted is called when a worker leased a job.
	JobStarted(applicationID string)
	// JobFinished is called after the terminal state was written; duration is counted
	// from created_at, queue time included.
	JobFinished(applicationID string, status job.Status, attempts int, duration time.Duration)
}

// NopObserver discards everything.
type NopObserver struct{}

// JobStarted implements Observer.
func (NopObserver) JobStarted(string) {}

// JobFinished implements Observer.
func (NopObserver) JobFinished(string, job.Status, int, time.Duration) {}

// Options configure a Pool. Zero values take the defaults.
type Options struct {
	Size         int           // workers; DefaultSize
	PollInterval time.Duration // sleep between empty leases; DefaultPollInterval
	// Lease is how long a lease lasts without renewal; a running job renews it every
	// Lease/3, and a crashed worker's job is leased again once it passed.
	Lease    time.Duration
	WorkerID string // prefix of the worker ids written to leases; "vde-<pid>"
	Observer Observer
	Logger   *slog.Logger
	// Audit receives job.finished events; nil records none.
	Audit *audit.Recorder
	// OnFinish, when set, is called with the job after its terminal state was written,
	// audited and its retention applied: the completion hook that wakes POST
	// /v1/extract waiters (Notifier) and queues webhooks. It must not block.
	OnFinish func(job.Job)
}

// Pool is a fixed set of workers leasing jobs from the store and running the
// extraction loop on them (ADR 0003).
type Pool struct {
	store job.Store
	blobs blob.Store
	cfg   *config.Config
	roles extract.Roles
	opts  Options

	apps          map[string]config.Application
	maxInProgress map[string]int
	extractorCaps []config.Capability
	judgeCaps     []config.Capability
	jobTimeout    time.Duration

	wake      chan struct{}
	stopping  chan struct{} // closed by Stop: lease nothing more
	cancelRun context.CancelFunc
	wg        sync.WaitGroup
	start     sync.Once
	stop      sync.Once
}

// New returns a pool for cfg (validated) and its roles. Call Start to run it.
func New(store job.Store, blobs blob.Store, cfg *config.Config, roles extract.Roles, opts Options) *Pool {
	if opts.Size <= 0 {
		opts.Size = DefaultSize
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = DefaultPollInterval
	}
	if opts.Lease <= 0 {
		opts.Lease = DefaultLease
	}
	if opts.WorkerID == "" {
		opts.WorkerID = fmt.Sprintf("vde-%d", os.Getpid())
	}
	if opts.Observer == nil {
		opts.Observer = NopObserver{}
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	opts.Audit = opts.Audit.WithActor(audit.ActorWorker)
	p := &Pool{
		store: store, blobs: blobs, cfg: cfg, roles: roles, opts: opts,
		apps:          map[string]config.Application{},
		maxInProgress: map[string]int{},
		extractorCaps: cfg.Capabilities(cfg.Roles.Extractor.Group),
		judgeCaps:     cfg.Capabilities(cfg.Roles.Judge.Group),
		jobTimeout:    cfg.Limits.JobTimeout,
		wake:          make(chan struct{}, opts.Size),
		stopping:      make(chan struct{}),
	}
	if p.jobTimeout <= 0 {
		p.jobTimeout = defaultJobTimeout
	}
	for _, a := range cfg.Applications {
		p.apps[a.ID] = a
		if a.Rate.MaxInProgress > 0 {
			p.maxInProgress[a.ID] = a.Rate.MaxInProgress
		}
	}
	return p
}

// Start launches the workers. Cancelling ctx stops them at once, like Stop with an
// expired grace. Start runs the pool only once.
func (p *Pool) Start(ctx context.Context) {
	p.start.Do(func() {
		ctx, p.cancelRun = context.WithCancel(ctx)
		for i := range p.opts.Size {
			id := fmt.Sprintf("%s/%d", p.opts.WorkerID, i)
			p.wg.Go(func() { p.loop(ctx, id) })
		}
	})
}

// Wake tells an idle worker to lease now instead of at its next poll. The submit
// handler calls it after storing a job. It never blocks.
func (p *Pool) Wake() {
	select {
	case p.wake <- struct{}{}:
	default: // enough wake-ups pending
	}
}

// Stop stops leasing and waits for running jobs until ctx is done (the shutdown grace).
// Jobs still running then are abandoned with their lease and the persisted attempts;
// they are leased again after a restart. It returns ctx's error when the grace ran out.
func (p *Pool) Stop(ctx context.Context) error {
	p.stop.Do(func() { close(p.stopping) })
	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		if p.cancelRun != nil {
			p.cancelRun()
		}
		<-done
		return ctx.Err()
	}
}

func (p *Pool) loop(ctx context.Context, workerID string) {
	timer := time.NewTimer(p.opts.PollInterval)
	defer timer.Stop()
	for {
		select {
		case <-p.stopping:
			return
		case <-ctx.Done():
			return
		default:
		}
		now := time.Now()
		j, err := p.store.LeaseNextQueued(ctx, workerID, now, now.Add(p.opts.Lease), p.maxInProgress)
		if err == nil {
			p.run(ctx, workerID, j)
			continue
		}
		if !errors.Is(err, job.ErrNotFound) && ctx.Err() == nil {
			p.opts.Logger.Error("worker: lease failed", "worker_id", workerID, "error", err)
		}
		timer.Reset(p.opts.PollInterval)
		select {
		case <-p.stopping:
			return
		case <-ctx.Done():
			return
		case <-p.wake:
		case <-timer.C:
		}
	}
}

// run takes one leased job to its terminal state, unless the pool is cancelled or the
// lease is lost, in which case the job is left for the next lease.
func (p *Pool) run(ctx context.Context, workerID string, j job.Job) {
	log := p.opts.Logger.With("job_id", j.ID, "application_id", j.ApplicationID, "worker_id", workerID)
	p.opts.Observer.JobStarted(j.ApplicationID)
	deadline := j.CreatedAt.Add(p.jobTimeout)
	app := p.application(ctx, j.ApplicationID)
	remaining := p.maxAttempts(app, j) - j.Attempts

	var out job.Outcome
	switch {
	case !time.Now().Before(deadline):
		out = fromRow(j, job.StatusFailed, extract.CodeTimeout) // no provider call
	case remaining <= 0:
		out = exhausted(j)
	default:
		var ok bool
		if out, ok = p.extract(ctx, log, workerID, app, j, deadline, remaining); !ok {
			return
		}
	}

	// The terminal write must happen even when the shutdown grace ends right now.
	fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), finishTimeout)
	defer cancel()
	finishedAt := time.Now()
	switch err := p.store.Finish(fctx, j.ID, workerID, out, finishedAt); {
	case errors.Is(err, job.ErrConflict):
		log.Warn("worker: finish refused, job already terminal or leased elsewhere")
		return
	case err != nil:
		log.Error("worker: finish failed", "error", err)
		return
	}
	log.Info("worker: job finished", "status", string(out.Status), "attempts", out.Attempts, "error_code", errorCode(out))
	p.opts.Observer.JobFinished(j.ApplicationID, out.Status, out.Attempts, time.Since(j.CreatedAt))
	j.Status, j.Attempts, j.Error, j.FinishedAt = out.Status, out.Attempts, out.Error, finishedAt
	p.opts.Audit.JobFinished(fctx, j)
	p.applyRetention(fctx, log, app, j)
	if p.opts.OnFinish != nil {
		p.opts.OnFinish(j)
	}
}

// extract runs the loop for the remaining attempts. ok is false when the job must be
// left alone: the pool was cancelled or the lease was lost.
func (p *Pool) extract(ctx context.Context, log *slog.Logger, workerID string, app config.Application, j job.Job, deadline time.Time, remaining int) (job.Outcome, bool) {
	in, reason, err := p.input(ctx, app, j)
	if err != nil {
		if ctx.Err() != nil {
			return job.Outcome{}, false
		}
		log.Error("worker: cannot prepare the job", "reason", reason)
		return fromRow(j, job.StatusFailed, extract.CodeInternal), true
	}
	in.Deadline = deadline
	// A resumed job continues from its persisted state: the loop counts on from
	// j.Attempts up to the limit and sends the last candidate and feedback in its
	// first request, as a retry does.
	in.MaxAttempts = j.Attempts + remaining
	in.PriorAttempts, in.PriorCandidate = j.Attempts, j.Result
	if len(j.Feedback) > 0 {
		_ = json.Unmarshal(j.Feedback, &in.PriorFeedback) // a malformed column leaves the retry without feedback
	}

	jobCtx, cancelJob := context.WithCancel(ctx)
	defer cancelJob()
	var lost atomic.Bool
	loseLease := func(err error) {
		if errors.Is(err, job.ErrConflict) || errors.Is(err, job.ErrNotFound) {
			lost.Store(true)
			cancelJob()
		} else if err != nil && jobCtx.Err() == nil {
			log.Warn("worker: lease write failed", "error", err)
		}
	}
	in.OnAttempt = func(pr extract.Progress) {
		loseLease(p.store.RecordAttempt(jobCtx, j.ID, workerID, progress(pr), time.Now().Add(p.opts.Lease)))
	}
	var heartbeat sync.WaitGroup
	heartbeat.Go(func() {
		t := time.NewTicker(p.opts.Lease / 3)
		defer t.Stop()
		for {
			select {
			case <-jobCtx.Done():
				return
			case <-t.C:
				loseLease(p.store.RenewLease(jobCtx, j.ID, workerID, time.Now().Add(p.opts.Lease)))
			}
		}
	})
	o := extract.Run(jobCtx, in, p.roles)
	cancelJob()
	heartbeat.Wait()
	switch {
	case lost.Load():
		log.Warn("worker: lease lost, result discarded")
		return job.Outcome{}, false
	case ctx.Err() != nil:
		log.Info("worker: stopped during the job, left for the next lease")
		return job.Outcome{}, false
	}
	return merge(o), true
}

// input builds the loop input from the row and its blobs. reason names the failing
// step for the log; errors may quote content and are never logged.
func (p *Pool) input(ctx context.Context, app config.Application, j job.Job) (in extract.Input, reason string, err error) {
	data, err := p.read(ctx, j.ID, blob.Document)
	if err != nil {
		return in, "document blob", err
	}
	pages := make([]document.Page, j.PageCount)
	for i := range pages {
		if pages[i].Data, err = p.read(ctx, j.ID, blob.Page(i+1)); err != nil {
			return in, "page blob", err
		}
	}
	doc, err := document.New(document.Input{MediaType: j.MediaType, Data: data, Pages: pages}, p.cfg.Limits)
	if err != nil {
		return in, "document", err
	}
	policy, err := document.ResolveHTMLPolicy(p.cfg.Documents.HTML, app.Documents.HTML)
	if err != nil {
		return in, "html policy", err
	}
	rep, err := document.Select(doc, p.extractorCaps, p.judgeCaps, policy)
	if err != nil {
		return in, "representation", err
	}
	if j.Representation != "" && rep.Kind.String() != j.Representation {
		return in, "representation changed since submit", errors.New("worker: representation changed")
	}
	var schema *extract.Schema
	if j.Schema != "" {
		if schema, err = extract.CompileSchema([]byte(j.Schema)); err != nil {
			return in, "schema", err
		}
	}
	return extract.Input{
		Representation:        rep,
		ExtractorInstructions: j.ExtractorInstructions,
		JudgeInstructions:     j.JudgeInstructions,
		Schema:                schema,
	}, "", nil
}

func (p *Pool) read(ctx context.Context, jobID, name string) ([]byte, error) {
	rc, err := p.blobs.Get(ctx, jobID, name)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// application is the job's application as the API sees it: the store row (declared in
// the YAML or created over the admin API, as changed by PATCH) for the attempt limit
// and the HTML policy over the configuration entry, which alone carries
// delete_blobs_at_terminal; force_none from either deletes the inputs at the terminal
// state. When the row cannot be read the configuration entry is used.
func (p *Pool) application(ctx context.Context, id string) config.Application {
	a := p.apps[id]
	if row, err := p.store.GetApplication(ctx, id); err == nil {
		a.MaxAttempts, a.Documents.HTML = row.MaxAttempts, row.HTMLPolicy
		a.Retention.ForceNone = a.Retention.ForceNone || row.ForceNone
	}
	return a
}

// maxAttempts is the job's attempt limit: its own, capped by the application's, which
// defaults to limits.max_attempts.
func (p *Pool) maxAttempts(app config.Application, j job.Job) int {
	limit := app.MaxAttempts
	if limit <= 0 {
		limit = p.cfg.Limits.MaxAttempts
	}
	if limit <= 0 {
		limit = extract.DefaultMaxAttempts
	}
	if j.MaxAttempts > 0 && j.MaxAttempts < limit {
		return j.MaxAttempts
	}
	return limit
}

// applyRetention deletes the inputs of a job at its terminal state (contract §10): blobs
// first, then the instruction and schema columns. That is every retention: none job, and
// a retention: default job of an application with delete_blobs_at_terminal; the other
// default jobs keep their inputs until the sweeper removes them at expires_at.
func (p *Pool) applyRetention(ctx context.Context, log *slog.Logger, app config.Application, j job.Job) {
	r := app.Retention
	if j.Retention != job.RetentionNone && !r.ForceNone && !r.DeleteBlobsAtTerminal {
		return
	}
	if err := p.blobs.DeleteAll(ctx, j.ID); err != nil {
		log.Error("worker: deleting blobs failed", "error", err)
		return
	}
	if err := p.store.MarkContentDeleted(ctx, j.ID, job.ContentInputs, time.Now()); err != nil {
		log.Error("worker: deleting inputs failed", "error", err)
	}
}

// Job error messages, generic as in extract.
var errorMessages = map[string]string{
	extract.CodeExtractorFailed: "The extractor gave no usable answer on any attempt.",
	extract.CodeTimeout:         "The job's time limit expired.",
	extract.CodeInternal:        "Internal error.",
}

func jobError(code string) json.RawMessage {
	b, _ := json.Marshal(extract.JobError{Code: code, Message: errorMessages[code]})
	return b
}

func feedbackJSON(f []extract.Feedback) json.RawMessage {
	if f == nil {
		f = []extract.Feedback{}
	}
	b, _ := json.Marshal(f)
	return b
}

// fromRow is a terminal state written without running the loop, keeping the persisted
// candidate and feedback.
func fromRow(j job.Job, status job.Status, code string) job.Outcome {
	o := job.Outcome{Status: status, Attempts: j.Attempts, Result: j.Result, Feedback: j.Feedback}
	if o.Feedback == nil {
		o.Feedback = feedbackJSON(nil)
	}
	if code != "" {
		o.Error = jobError(code)
	}
	return o
}

// exhausted ends a job leased again after its last attempt was persisted: rejected
// with the last candidate, or extractor_failed when there was none (as extract.Run).
func exhausted(j job.Job) job.Outcome {
	if j.Result == nil {
		return fromRow(j, job.StatusFailed, extract.CodeExtractorFailed)
	}
	return fromRow(j, job.StatusRejected, "")
}

// progress is the state to persist after an attempt; the loop's counts and candidate
// already include those of the run before a re-lease.
func progress(pr extract.Progress) job.Progress {
	return job.Progress{Attempts: pr.Attempts, Result: pr.Result, Feedback: feedbackJSON(pr.Feedback)}
}

// merge turns the loop's outcome, which already counts the attempts and holds the
// candidate of a run before a re-lease, into the row's terminal state.
func merge(o extract.Outcome) job.Outcome {
	out := job.Outcome{
		Status:          job.Status(o.Status),
		Attempts:        o.Attempts,
		Result:          o.Result,
		Feedback:        feedbackJSON(o.Feedback),
		JudgeConfidence: o.JudgeConfidence,
	}
	if o.Error != nil {
		out.Error, _ = json.Marshal(o.Error)
	}
	return out
}

func errorCode(o job.Outcome) string {
	var e extract.JobError
	if o.Error == nil || json.Unmarshal(o.Error, &e) != nil {
		return ""
	}
	return e.Code
}
