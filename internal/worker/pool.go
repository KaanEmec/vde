package worker

import (
	"cmp"
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
	"github.com/kaanemec/vde/internal/metrics"
	"github.com/kaanemec/vde/internal/pdf"
)

// finishTimeout bounds the terminal write, which must happen even when the shutdown
// grace ends.
const finishTimeout = 10 * time.Second

// Options configure a Pool beside its configuration (worker.size, worker.poll_interval,
// worker.lease, limits.job_timeout).
type Options struct {
	// WorkerID is the prefix of the worker ids written to leases; "vde-<pid>". Processes
	// sharing a database need distinct prefixes (vde serve uses its instance id: in a
	// container every process may be pid 1).
	WorkerID string
	// Metrics counts finished jobs and their judge decisions; nil counts nothing.
	Metrics *metrics.Metrics
	Logger  *slog.Logger
	// Audit receives job.finished events; nil records none.
	Audit *audit.Recorder
	// OnFinish, when set, is called with the job after its terminal state was written,
	// audited and its retention applied: the completion hook that wakes POST
	// /v1/extract waiters (Notifier) and queues webhooks. It must not block.
	OnFinish func(job.Job)
	// PDF prepares PDFs that no group reads natively (text layer or rendered pages, ADR
	// 0010) and reads the grounding text of the others under judge policy never and
	// auto; nil does neither (such PDFs need caller pages). It must be the engine submit selected with,
	// so the worker re-selects the same representation.
	PDF document.PDFEngine
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

	wake      chan struct{}
	stopping  chan struct{} // closed by Stop: lease nothing more
	cancelRun context.CancelFunc
	wg        sync.WaitGroup
	start     sync.Once
	stop      sync.Once
}

// New returns a pool for cfg (validated) and its roles. Call Start to run it.
func New(store job.Store, blobs blob.Store, cfg *config.Config, roles extract.Roles, opts Options) *Pool {
	if opts.WorkerID == "" {
		opts.WorkerID = fmt.Sprintf("vde-%d", os.Getpid())
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	opts.Audit = opts.Audit.WithActor(audit.ActorWorker)
	p := &Pool{
		store: store, blobs: blobs, cfg: cfg, roles: roles, opts: opts,
		apps:          map[string]config.Application{},
		maxInProgress: map[string]int{},
		wake:          make(chan struct{}, cfg.Worker.Size),
		stopping:      make(chan struct{}),
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
		for i := range p.cfg.Worker.Size {
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
	timer := time.NewTimer(p.cfg.Worker.PollInterval)
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
		j, err := p.store.LeaseNextQueued(ctx, workerID, now, now.Add(p.cfg.Worker.Lease), p.maxInProgress)
		if err == nil {
			p.run(ctx, workerID, j)
			continue
		}
		if !errors.Is(err, job.ErrNotFound) && ctx.Err() == nil {
			p.opts.Logger.Error("worker: lease failed", "worker_id", workerID, "error", err)
		}
		timer.Reset(p.cfg.Worker.PollInterval)
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
	deadline := j.CreatedAt.Add(p.cfg.Limits.JobTimeout)
	app := application(ctx, p.store, p.apps, j.ApplicationID)
	remaining := p.maxAttempts(app, j) - j.Attempts

	var out job.Outcome
	var loop extract.Outcome // the loop's metadata (judge decision), zero when it did not run
	switch {
	case !time.Now().Before(deadline):
		out = fromRow(j, job.StatusFailed, extract.CodeTimeout) // no provider call
	case remaining <= 0:
		out = exhausted(j)
	default:
		var ok bool
		if out, loop, ok = p.extract(ctx, log, workerID, app, j, deadline, remaining); !ok {
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
	j.Status, j.Attempts, j.Error, j.FinishedAt = out.Status, out.Attempts, out.Error, finishedAt
	j.InputTokens, j.OutputTokens = out.InputTokens, out.OutputTokens
	j.Verification = cmp.Or(out.Verification, job.DefaultVerification(out.Status))
	j.JudgePolicy = cmp.Or(j.JudgePolicy, job.DefaultJudgePolicy)
	log.Info("worker: job finished", "status", string(out.Status), "verification", j.Verification, "attempts", out.Attempts,
		"error_code", job.ErrorCode(out.Error), "input_tokens", out.InputTokens, "output_tokens", out.OutputTokens)
	p.opts.Metrics.JobFinished(j.ApplicationID, out.Status, out.Attempts, time.Since(j.CreatedAt))
	if loop.Decision != "" {
		p.opts.Metrics.JudgeDecision(j.JudgePolicy, loop.Decision, loop.Disagreements)
	}
	p.opts.Audit.JobFinished(fctx, j)
	p.applyRetention(fctx, log, app, j)
	if p.opts.OnFinish != nil {
		p.opts.OnFinish(j)
	}
}

// extract runs the loop for the remaining attempts. ok is false when the job must be
// left alone: the pool was cancelled or the lease was lost.
func (p *Pool) extract(ctx context.Context, log *slog.Logger, workerID string, app config.Application, j job.Job, deadline time.Time, remaining int) (job.Outcome, extract.Outcome, bool) {
	// Preparing the document (the PDF engine) runs under the job's deadline too.
	prepCtx, cancelPrep := context.WithDeadline(ctx, deadline)
	in, reason, err := p.input(prepCtx, app, j)
	cancelPrep()
	if err != nil {
		switch {
		case ctx.Err() != nil:
			return job.Outcome{}, extract.Outcome{}, false
		case !time.Now().Before(deadline):
			return fromRow(j, job.StatusFailed, extract.CodeTimeout), extract.Outcome{}, true
		case errors.Is(err, pdf.ErrBusy):
			// Every PDF engine instance stayed busy for the engine's wait: not the job's
			// fault. Release the lease without an attempt, so the job is leased again (by
			// any worker) until it gets an instance or its deadline ends it as timeout.
			log.Info("worker: pdf engine busy, job left for the next lease")
			p.release(ctx, log, j.ID, workerID)
			return job.Outcome{}, extract.Outcome{}, false
		}
		log.Error("worker: cannot prepare the job", "reason", reason)
		return fromRow(j, job.StatusFailed, extract.CodeInternal), extract.Outcome{}, true
	}
	in.Deadline, in.Logger = deadline, log
	// A resumed job continues from its persisted state: the loop counts on from
	// j.Attempts up to the limit and sends the last candidate and feedback in its
	// first request, as a retry does.
	in.MaxAttempts = j.Attempts + remaining
	in.PriorAttempts, in.PriorCandidate, in.PriorRepaired = j.Attempts, j.Result, j.GroundingRepaired
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
		loseLease(p.store.RecordAttempt(jobCtx, j.ID, workerID, progress(j, pr), time.Now().Add(p.cfg.Worker.Lease)))
	}
	var heartbeat sync.WaitGroup
	heartbeat.Go(func() {
		t := time.NewTicker(p.cfg.Worker.Lease / 3)
		defer t.Stop()
		for {
			select {
			case <-jobCtx.Done():
				return
			case <-t.C:
				loseLease(p.store.RenewLease(jobCtx, j.ID, workerID, time.Now().Add(p.cfg.Worker.Lease)))
			}
		}
	})
	o := extract.Run(jobCtx, in, p.roles)
	cancelJob()
	heartbeat.Wait()
	switch {
	case lost.Load():
		log.Warn("worker: lease lost, result discarded")
		return job.Outcome{}, extract.Outcome{}, false
	case ctx.Err() != nil:
		log.Info("worker: stopped during the job, left for the next lease")
		return job.Outcome{}, extract.Outcome{}, false
	}
	return merge(j, o), o, true
}

// release ends this worker's lease on a running job now, so the next lease takes it
// again; its attempts and saved progress are unchanged.
func (p *Pool) release(ctx context.Context, log *slog.Logger, id, workerID string) {
	if err := p.store.RenewLease(context.WithoutCancel(ctx), id, workerID, time.Now()); err != nil {
		log.Warn("worker: releasing the lease failed; the job waits for it to expire", "error", err)
	}
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
	// The same selection as at submit, with the process's engine (a PDF chosen as text at
	// submit is read as text again).
	rep, pdfPolicy, err := extract.SelectRepresentation(ctx, p.cfg, doc, policy, app.Documents.PDF.Mode, p.opts.PDF)
	if err != nil {
		return in, "representation", err
	}
	if j.Representation != "" && rep.Label() != j.Representation {
		return in, "representation changed since submit", errors.New("worker: representation changed")
	}
	judgePolicy := config.JudgePolicy(cmp.Or(j.JudgePolicy, job.DefaultJudgePolicy))
	if judgePolicy != config.JudgeAlways {
		// A PDF sent natively or as caller pages is opened for its text layer only here.
		if rep.GroundText, rep.Groundable, err = document.GroundingText(ctx, doc, rep, pdfPolicy); err != nil {
			return in, "grounding text", err
		}
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
		JudgePolicy:           judgePolicy,
		SampleRate:            app.Judge.Rate(),
		SampleKey:             []byte(j.ID), // a resumed job keeps its sampling decision
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
// the YAML or created over the admin API, as changed by PATCH) for the attempt limit,
// the HTML policy and the judge sample rate over the configuration entry, which alone
// carries delete_blobs_at_terminal and the PDF mode; force_none from either deletes the
// inputs at the terminal state. When the row cannot be read the configuration entry of
// apps is used. The pool and the sweeper read it the same way.
func application(ctx context.Context, store job.Store, apps map[string]config.Application, id string) config.Application {
	a := apps[id]
	if row, err := store.GetApplication(ctx, id); err == nil {
		a.MaxAttempts, a.Documents.HTML, a.Judge.SampleRate = row.MaxAttempts, row.HTMLPolicy, row.JudgeSampleRate
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
	if j.MaxAttempts > 0 && j.MaxAttempts < limit {
		return j.MaxAttempts
	}
	return limit
}

// applyRetention deletes the inputs of a job the worker finished, when its retention says
// so (inputsAtTerminal).
func (p *Pool) applyRetention(ctx context.Context, log *slog.Logger, app config.Application, j job.Job) {
	if !inputsAtTerminal(app, j) {
		return
	}
	if err := deleteInputs(ctx, p.blobs, p.store, j.ID, time.Now()); err != nil {
		log.Error("worker: deleting inputs failed", "error", err)
	}
}

// inputsAtTerminal reports whether a job loses its inputs at its terminal state (contract
// §10): every retention: none job, and a retention: default job of an application with
// delete_blobs_at_terminal or force_none. The other default jobs keep their inputs until
// the sweeper removes them at expires_at.
func inputsAtTerminal(app config.Application, j job.Job) bool {
	r := app.Retention
	return j.Retention == job.RetentionNone || r.ForceNone || r.DeleteBlobsAtTerminal
}

// deleteInputs deletes a job's inputs: the blobs first, then the instruction and schema
// columns.
func deleteInputs(ctx context.Context, blobs blob.Store, store job.Store, id string, now time.Time) error {
	if err := blobs.DeleteAll(ctx, id); err != nil {
		return fmt.Errorf("delete blobs of %s: %w", id, err)
	}
	if err := store.MarkContentDeleted(ctx, id, job.ContentInputs, now); err != nil {
		return fmt.Errorf("delete inputs of %s: %w", id, err)
	}
	return nil
}

func jobError(code string) json.RawMessage {
	b, _ := json.Marshal(extract.NewJobError(code))
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
// candidate and feedback. Under judge policy always a persisted grounding repair's
// entries are dropped, as the loop drops them: contract v1 sources only.
func fromRow(j job.Job, status job.Status, code string) job.Outcome {
	o := job.Outcome{Status: status, Attempts: j.Attempts, Result: j.Result, Feedback: j.Feedback,
		InputTokens: j.InputTokens, OutputTokens: j.OutputTokens}
	o.Feedback = extract.VisibleFeedback(j.JudgePolicy, j.Feedback)
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
// already include those of the run before a re-lease, its usage does not: the row's
// usage (of earlier runs, as leased) is added. Tokens spent after the last persisted
// attempt of a run that died are not counted.
func progress(j job.Job, pr extract.Progress) job.Progress {
	return job.Progress{Attempts: pr.Attempts, Result: pr.Result, Feedback: feedbackJSON(pr.Feedback), GroundingRepaired: pr.Repaired,
		InputTokens:  j.InputTokens + int64(pr.Usage.InputTokens),
		OutputTokens: j.OutputTokens + int64(pr.Usage.OutputTokens)}
}

// merge turns the loop's outcome, which already counts the attempts and holds the
// candidate of a run before a re-lease, into the row's terminal state, with the usage
// of the leased row added as in progress.
func merge(j job.Job, o extract.Outcome) job.Outcome {
	out := job.Outcome{
		Status:          job.Status(o.Status),
		Attempts:        o.Attempts,
		Result:          o.Result,
		Feedback:        feedbackJSON(o.Feedback),
		JudgeConfidence: o.JudgeConfidence,
		Verification:    o.Verification,
		InputTokens:     j.InputTokens + int64(o.Usage.InputTokens),
		OutputTokens:    j.OutputTokens + int64(o.Usage.OutputTokens),
	}
	if o.Error != nil {
		out.Error, _ = json.Marshal(o.Error)
	}
	return out
}
