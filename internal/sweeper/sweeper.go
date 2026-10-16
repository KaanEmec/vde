package sweeper

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/kaanemec/vde/internal/audit"
	"github.com/kaanemec/vde/internal/blob"
	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/extract"
	"github.com/kaanemec/vde/internal/job"
)

// Defaults for Options.
const (
	DefaultInterval           = time.Minute
	DefaultBatch              = 100
	DefaultJobTimeout         = 5 * time.Minute // the worker's default (contract §6)
	DefaultGrace              = time.Minute
	DefaultQuotaWindowsToKeep = 2
	DefaultTokenRetention     = 90 * 24 * time.Hour
	DefaultAuditRetention     = 365 * 24 * time.Hour

	orphanInterval = 24 * time.Hour
)

// Observer receives the sweeper's metric (epic 07).
type Observer interface {
	// AddSwept counts n expired jobs: their content, blobs included, deleted at
	// expires_at (or their row removed, for a job whose content the sweeper had not
	// expired).
	AddSwept(n int)
}

type nopObserver struct{}

func (nopObserver) AddSwept(int) {}

// Options configure a Sweeper. Zero values take the defaults.
type Options struct {
	Interval time.Duration // between sweeps (retention.sweep_interval)
	Batch    int           // jobs per store round trip
	// JobTimeout and Grace: a job still running this long after created_at is finished as
	// failed/timeout (limits.job_timeout plus a margin for the worker's own finish).
	JobTimeout         time.Duration
	Grace              time.Duration
	QuotaWindowsToKeep int           // quota windows kept per application, the current one included
	TokenRetention     time.Duration // how long revoked or expired tokens are kept
	AuditRetention     time.Duration // audit.retention
	Clock              func() time.Time
	Logger             *slog.Logger
	Observer           Observer
	Audit              *audit.Recorder // nil writes no audit events
	// DeleteInputsAtTerminal lists the applications whose retention: default jobs lose
	// their document, pages, instructions and schema at the terminal state: those with
	// retention.delete_blobs_at_terminal or retention.force_none. The sweeper applies it to
	// the jobs it finishes itself, as the worker does for the others. retention: none
	// jobs are always cleared.
	DeleteInputsAtTerminal map[string]bool
}

// Result counts what one sweep removed or finished.
type Result struct {
	Expired     int // expired jobs: content and blobs deleted, or row removed (one job.expired each)
	Removed     int // job rows removed, past both expires_at and the idempotency window
	Stale       int // running jobs finished as failed/timeout
	QuotaRows   int
	Tokens      int
	AuditEvents int
	Orphans     int // blob directories without a job row
}

// Sweeper removes what retention says is due: the content and blobs of expired jobs,
// their rows once the idempotency window is over too, stale
// running jobs, old quota, token and audit rows, and orphan blobs.
type Sweeper struct {
	store job.Store
	blobs blob.Store
	opts  Options
	log   *slog.Logger
	audit *audit.Recorder

	mu         sync.Mutex // serializes sweeps; guards lastOrphan
	lastOrphan time.Time
}

// New returns a sweeper. Call Run, or Once from a test.
func New(store job.Store, blobs blob.Store, opts Options) *Sweeper {
	if opts.Interval <= 0 {
		opts.Interval = DefaultInterval
	}
	if opts.Batch <= 0 {
		opts.Batch = DefaultBatch
	}
	if opts.JobTimeout <= 0 {
		opts.JobTimeout = DefaultJobTimeout
	}
	if opts.Grace <= 0 {
		opts.Grace = DefaultGrace
	}
	if opts.QuotaWindowsToKeep <= 0 {
		opts.QuotaWindowsToKeep = DefaultQuotaWindowsToKeep
	}
	if opts.TokenRetention <= 0 {
		opts.TokenRetention = DefaultTokenRetention
	}
	if opts.AuditRetention <= 0 {
		opts.AuditRetention = DefaultAuditRetention
	}
	if opts.Clock == nil {
		opts.Clock = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Observer == nil {
		opts.Observer = nopObserver{}
	}
	return &Sweeper{
		store: store, blobs: blobs, opts: opts, log: opts.Logger.With("component", "sweeper"),
		audit: opts.Audit.WithActor(audit.ActorSweeper),
	}
}

// Run sweeps at once and then every Interval until ctx is done. A failed sweep is
// logged and retried at the next tick.
func (s *Sweeper) Run(ctx context.Context) {
	ticker := time.NewTicker(s.opts.Interval)
	defer ticker.Stop()
	for {
		if _, err := s.Once(ctx, s.opts.Clock()); err != nil && ctx.Err() == nil {
			s.log.Error("sweep failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Once runs one sweep as of now. The steps are independent: one that fails does not stop
// the others, and the errors come back joined. The orphan blob sweep runs on the first
// call and then once a day.
func (s *Sweeper) Once(ctx context.Context, now time.Time) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var (
		r    Result
		errs []error
	)
	step := func(name string, n *int, f func() (int, error)) {
		var err error
		*n, err = f()
		if *n > 0 {
			s.log.Info("swept "+name, "count", *n)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("sweeper: %s: %w", name, err))
		}
	}
	step("expired jobs", &r.Expired, func() (int, error) { return s.expired(ctx, now) })
	var lateExpired int // rows removed whose content the sweeper had not expired
	step("expired job rows", &r.Removed, func() (int, error) {
		var (
			n   int
			err error
		)
		n, lateExpired, err = s.removeRows(ctx, now)
		return n, err
	})
	r.Expired += lateExpired
	step("stale running jobs", &r.Stale, func() (int, error) { return s.stale(ctx, now) })
	step("quota rows", &r.QuotaRows, func() (int, error) { return s.quota(ctx, now) })
	step("tokens", &r.Tokens, func() (int, error) {
		n, err := s.store.DeleteTokensBefore(ctx, now.Add(-s.opts.TokenRetention))
		return int(n), err
	})
	step("audit events", &r.AuditEvents, func() (int, error) {
		n, err := s.store.DeleteAuditBefore(ctx, now.Add(-s.opts.AuditRetention))
		return int(n), err
	})
	if s.lastOrphan.IsZero() || !now.Before(s.lastOrphan.Add(orphanInterval)) {
		step("orphan blobs", &r.Orphans, func() (int, error) { return s.orphans(ctx) })
		if ctx.Err() == nil {
			s.lastOrphan = now
		}
	}
	if r.Expired > 0 {
		s.opts.Observer.AddSwept(r.Expired)
	}
	return r, errors.Join(errs...)
}

// expired deletes the blobs and then the content (instructions, schema, result,
// feedback, error) of every terminal job past its expires_at, batch by batch until a
// batch comes back short, and records job.expired. The row stays, metadata only, until
// removeRows: GET already answers 404, and an idempotent replay still names the job. A
// job still queued or running at its expires_at is expired once it is terminal. A job
// whose blobs cannot be removed keeps its content, so the next sweep retries it.
func (s *Sweeper) expired(ctx context.Context, now time.Time) (int, error) {
	var (
		total int
		errs  []error
	)
	for {
		batch, err := s.store.ListContentExpired(ctx, now, s.opts.Batch)
		if err != nil {
			return total, errors.Join(append(errs, err)...)
		}
		done := 0
		for _, j := range batch {
			if err := ctx.Err(); err != nil {
				return total + done, errors.Join(append(errs, err)...)
			}
			if err := s.blobs.DeleteAll(ctx, j.ID); err != nil {
				errs = append(errs, fmt.Errorf("delete blobs of %s: %w", j.ID, err))
				continue
			}
			switch err := s.store.MarkContentDeleted(ctx, j.ID, job.ContentAll, now); {
			case errors.Is(err, job.ErrNotFound): // deleted by someone else meanwhile
			case err != nil:
				errs = append(errs, fmt.Errorf("delete content of %s: %w", j.ID, err))
			default:
				done++
				s.audit.JobExpired(ctx, j)
			}
		}
		total += done
		if len(batch) < s.opts.Batch || done == 0 {
			return total, errors.Join(errs...)
		}
	}
}

// removeRows removes the blobs (normally gone already) and then the row of every job
// past both its expires_at and created_at + job.IdempotencyWindow, batch by batch. The
// audit record stays. A row whose content the sweeper had not expired (deleted at the
// first read, or a job not terminal at its expires_at) gets its job.expired here; late
// counts those. A job whose blobs cannot be removed keeps its row for the next sweep.
func (s *Sweeper) removeRows(ctx context.Context, now time.Time) (removed, late int, err error) {
	var errs []error
	for {
		batch, err := s.store.ListExpired(ctx, now, s.opts.Batch)
		if err != nil {
			return removed, late, errors.Join(append(errs, err)...)
		}
		n := 0
		for _, j := range batch {
			if err := ctx.Err(); err != nil {
				return removed + n, late, errors.Join(append(errs, err)...)
			}
			if err := s.blobs.DeleteAll(ctx, j.ID); err != nil {
				errs = append(errs, fmt.Errorf("delete blobs of %s: %w", j.ID, err))
				continue
			}
			switch err := s.store.DeleteJob(ctx, j.ID); {
			case errors.Is(err, job.ErrNotFound): // deleted by someone else meanwhile
			case err != nil:
				errs = append(errs, fmt.Errorf("delete job %s: %w", j.ID, err))
			default:
				n++
				if j.ContentDeletedAt.IsZero() || j.ContentDeletedAt.Before(j.ExpiresAt) {
					late++
					s.audit.JobExpired(ctx, j)
				}
			}
		}
		removed += n
		if len(batch) < s.opts.Batch || n == 0 {
			return removed, late, errors.Join(errs...)
		}
	}
}

// timeoutMessage is the job error message of code timeout, as the worker writes it.
const timeoutMessage = "The job's time limit expired."

// stale finishes the running jobs whose time limit and grace are over: their worker is
// lost. The persisted candidate and feedback are kept.
func (s *Sweeper) stale(ctx context.Context, now time.Time) (int, error) {
	cutoff := now.Add(-(s.opts.JobTimeout + s.opts.Grace))
	var (
		total int
		errs  []error
	)
	for {
		batch, err := s.store.ListStaleRunning(ctx, cutoff, s.opts.Batch)
		if err != nil {
			return total, errors.Join(append(errs, err)...)
		}
		finished := 0
		for _, j := range batch {
			if err := ctx.Err(); err != nil {
				return total + finished, errors.Join(append(errs, err)...)
			}
			out := timeoutOutcome(j)
			switch err := s.store.Finish(ctx, j.ID, "", out, now); {
			case errors.Is(err, job.ErrConflict), errors.Is(err, job.ErrNotFound): // finished or removed meanwhile
			case err != nil:
				errs = append(errs, fmt.Errorf("finish job %s: %w", j.ID, err))
			default:
				finished++
				j.Status, j.Attempts, j.Error, j.FinishedAt = out.Status, out.Attempts, out.Error, now
				s.audit.JobFinished(ctx, j)
				if err := s.clearInputs(ctx, j, now); err != nil {
					errs = append(errs, err)
				}
			}
		}
		total += finished
		if len(batch) < s.opts.Batch || finished == 0 {
			return total, errors.Join(errs...)
		}
	}
}

// clearInputs is the terminal-state deletion of contract §10 for a job the worker did
// not finish: the blobs, then the instruction and schema columns.
func (s *Sweeper) clearInputs(ctx context.Context, j job.Job, now time.Time) error {
	if j.Retention != job.RetentionNone && !s.opts.DeleteInputsAtTerminal[j.ApplicationID] {
		return nil
	}
	if err := s.blobs.DeleteAll(ctx, j.ID); err != nil {
		return fmt.Errorf("delete blobs of %s: %w", j.ID, err)
	}
	if err := s.store.MarkContentDeleted(ctx, j.ID, job.ContentInputs, now); err != nil {
		return fmt.Errorf("delete inputs of %s: %w", j.ID, err)
	}
	return nil
}

func timeoutOutcome(j job.Job) job.Outcome {
	e, _ := json.Marshal(extract.JobError{Code: extract.CodeTimeout, Message: timeoutMessage})
	o := job.Outcome{Status: job.StatusFailed, Attempts: j.Attempts, Result: j.Result, Feedback: j.Feedback, Error: e}
	if o.Feedback == nil {
		o.Feedback = json.RawMessage(`[]`)
	}
	return o
}

// quota removes each application's quota windows older than QuotaWindowsToKeep windows.
func (s *Sweeper) quota(ctx context.Context, now time.Time) (int, error) {
	apps, err := s.store.ListApplications(ctx)
	if err != nil {
		return 0, err
	}
	var (
		total int
		errs  []error
	)
	for _, a := range apps {
		window, err := config.ParseWindow(a.QuotaWindow)
		if err != nil {
			errs = append(errs, fmt.Errorf("application %s: %w", a.ID, err))
			continue
		}
		n, err := s.store.DeleteQuotaBefore(ctx, a.ID, now.Add(-time.Duration(s.opts.QuotaWindowsToKeep)*window))
		total += int(n)
		if err != nil {
			errs = append(errs, err)
		}
	}
	return total, errors.Join(errs...)
}

// orphans removes blob directories whose job row does not exist (a crash between the
// blobs and the row, or a restored database). A row that cannot be read keeps its blobs.
func (s *Sweeper) orphans(ctx context.Context) (int, error) {
	var lookup error
	keep := func(id string) bool {
		switch _, err := s.store.GetJobAny(ctx, id); {
		case err == nil:
			return true
		case errors.Is(err, job.ErrNotFound):
			return false
		default:
			lookup = err
			return true
		}
	}
	n, err := s.blobs.SweepOrphans(ctx, keep)
	return n, errors.Join(err, lookup)
}
