package worker

import (
	"context"
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
	"github.com/kaanemec/vde/internal/metrics"
)

const (
	defaultBatch = 100 // jobs per store round trip
	// grace: a job still running limits.job_timeout plus grace after created_at is
	// finished as failed/timeout; the margin is for the worker's own finish.
	grace              = time.Minute
	quotaWindowsToKeep = 2                   // per application, the current one included
	tokenRetention     = 90 * 24 * time.Hour // how long revoked or expired tokens are kept

	orphanInterval = 24 * time.Hour
	// rateWindowsKept: shared rate windows are one minute long; the current and the
	// previous one are kept.
	rateWindowsKept = 2 * time.Minute
)

// Locker serializes sweeps across processes sharing a database: *job.DB takes a
// PostgreSQL advisory lock, or grants it at once on SQLite (one process). The tests
// fake a lock another process holds.
type Locker interface {
	// TryLock returns release and ok true when this process holds key, ok false at once
	// when another one does.
	TryLock(ctx context.Context, key int64) (release func(), ok bool, err error)
}

// SweepOptions configure a Sweeper beside its configuration (retention.sweep_interval,
// limits.job_timeout, audit.retention, the applications' retention).
type SweepOptions struct {
	Batch  int              // jobs per store round trip; tests set it, 0 means 100
	Clock  func() time.Time // the time of each sweep of Run; nil means time.Now
	Logger *slog.Logger
	// Metrics counts expired jobs: their content, blobs included, deleted at expires_at
	// (or their row removed, for a job whose content the sweeper had not expired); nil
	// counts nothing.
	Metrics *metrics.Metrics
	Audit   *audit.Recorder // nil writes no audit events
	// Lock, when set, is taken (without waiting) around each sweep of Run, so that of
	// several worker processes one sweeps at a time; a sweep whose lock another process
	// holds is skipped until the next tick. Once is never locked.
	Lock Locker
}

// SweepResult counts what one sweep removed or finished.
type SweepResult struct {
	Expired     int // expired jobs: content and blobs deleted, or row removed (one job.expired each)
	Removed     int // job rows removed, past both expires_at and the idempotency window
	Stale       int // running jobs finished as failed/timeout
	QuotaRows   int
	RateRows    int // shared rate windows (rate.shared) older than two minutes
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
	cfg   *config.Config
	opts  SweepOptions
	// apps are the configured applications by id, for the retention of the jobs the
	// sweeper finishes itself (application and inputsAtTerminal, as for the worker's).
	apps  map[string]config.Application
	log   *slog.Logger
	audit *audit.Recorder

	mu         sync.Mutex // serializes sweeps; guards lastOrphan
	lastOrphan time.Time
}

// NewSweeper returns a sweeper for cfg (validated). Call Run, or Once from a test.
func NewSweeper(store job.Store, blobs blob.Store, cfg *config.Config, opts SweepOptions) *Sweeper {
	if opts.Batch <= 0 {
		opts.Batch = defaultBatch
	}
	if opts.Clock == nil {
		opts.Clock = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	apps := map[string]config.Application{}
	for _, a := range cfg.Applications {
		apps[a.ID] = a
	}
	return &Sweeper{
		store: store, blobs: blobs, cfg: cfg, opts: opts, apps: apps,
		log: opts.Logger.With("component", "sweeper"), audit: opts.Audit.WithActor(audit.ActorSweeper),
	}
}

// Run sweeps at once and then every retention.sweep_interval until ctx is done. A failed
// sweep is logged and retried at the next tick.
func (s *Sweeper) Run(ctx context.Context) {
	ticker := time.NewTicker(s.cfg.Retention.SweepInterval)
	defer ticker.Stop()
	for {
		s.lockedOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// lockedOnce runs Once under SweepOptions.Lock, or skips the sweep when another process
// holds the lock.
func (s *Sweeper) lockedOnce(ctx context.Context) {
	if s.opts.Lock != nil {
		release, ok, err := s.opts.Lock.TryLock(ctx, job.SweepLock)
		switch {
		case err != nil:
			if ctx.Err() == nil {
				s.log.Error("sweep lock failed", "error", err)
			}
			return
		case !ok:
			s.log.Debug("sweep skipped: another process holds the sweep lock")
			return
		}
		defer release()
	}
	if _, err := s.Once(ctx, s.opts.Clock()); err != nil && ctx.Err() == nil {
		s.log.Error("sweep failed", "error", err)
	}
}

// Once runs one sweep as of now. The steps are independent: one that fails does not stop
// the others, and the errors come back joined. The orphan blob sweep runs on the first
// call and then once a day.
func (s *Sweeper) Once(ctx context.Context, now time.Time) (SweepResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var (
		r    SweepResult
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
	step("rate windows", &r.RateRows, func() (int, error) {
		n, err := s.store.DeleteRateBefore(ctx, now.Add(-rateWindowsKept))
		return int(n), err
	})
	step("tokens", &r.Tokens, func() (int, error) {
		n, err := s.store.DeleteTokensBefore(ctx, now.Add(-tokenRetention))
		return int(n), err
	})
	step("audit events", &r.AuditEvents, func() (int, error) {
		n, err := s.store.DeleteAuditBefore(ctx, now.Add(-s.cfg.Audit.Retention))
		return int(n), err
	})
	if s.lastOrphan.IsZero() || !now.Before(s.lastOrphan.Add(orphanInterval)) {
		step("orphan blobs", &r.Orphans, func() (int, error) { return s.orphans(ctx) })
		if ctx.Err() == nil {
			s.lastOrphan = now
		}
	}
	s.opts.Metrics.AddSwept(r.Expired)
	return r, errors.Join(errs...)
}

// batched runs one pass over the jobs list returns, batch by batch until a batch comes
// back short or nothing in it was done: each handles one job and reports whether it
// counts. An error of each is collected and the pass goes on; a list error or the end
// of ctx stops it.
func (s *Sweeper) batched(ctx context.Context, list func() ([]job.Job, error), each func(job.Job) (bool, error)) (int, error) {
	var (
		total int
		errs  []error
	)
	for {
		batch, err := list()
		if err != nil {
			return total, errors.Join(append(errs, err)...)
		}
		done := 0
		for _, j := range batch {
			if err := ctx.Err(); err != nil {
				return total + done, errors.Join(append(errs, err)...)
			}
			counted, err := each(j)
			if counted {
				done++
			}
			if err != nil {
				errs = append(errs, err)
			}
		}
		total += done
		if len(batch) < s.opts.Batch || done == 0 {
			return total, errors.Join(errs...)
		}
	}
}

// expired deletes the blobs and then the content (instructions, schema, result,
// feedback, error) of every terminal job past its expires_at and records job.expired.
// The row stays, metadata only, until removeRows: GET already answers 404, and an
// idempotent replay still names the job. A job still queued or running at its expires_at
// is expired once it is terminal. A job whose blobs cannot be removed keeps its content,
// so the next sweep retries it.
func (s *Sweeper) expired(ctx context.Context, now time.Time) (int, error) {
	list := func() ([]job.Job, error) { return s.store.ListContentExpired(ctx, now, s.opts.Batch) }
	return s.batched(ctx, list, func(j job.Job) (bool, error) {
		if err := s.blobs.DeleteAll(ctx, j.ID); err != nil {
			return false, fmt.Errorf("delete blobs of %s: %w", j.ID, err)
		}
		switch err := s.store.MarkContentDeleted(ctx, j.ID, job.ContentAll, now); {
		case errors.Is(err, job.ErrNotFound): // deleted by someone else meanwhile
			return false, nil
		case err != nil:
			return false, fmt.Errorf("delete content of %s: %w", j.ID, err)
		}
		s.audit.JobExpired(ctx, j)
		return true, nil
	})
}

// removeRows removes the blobs (normally gone already) and then the row of every job
// past both its expires_at and created_at + job.IdempotencyWindow. The audit record
// stays. A row whose content the sweeper had not expired (deleted at the first read, or
// a job not terminal at its expires_at) gets its job.expired here; late counts those. A
// job whose blobs cannot be removed keeps its row for the next sweep.
func (s *Sweeper) removeRows(ctx context.Context, now time.Time) (removed, late int, err error) {
	list := func() ([]job.Job, error) { return s.store.ListExpired(ctx, now, s.opts.Batch) }
	removed, err = s.batched(ctx, list, func(j job.Job) (bool, error) {
		if err := s.blobs.DeleteAll(ctx, j.ID); err != nil {
			return false, fmt.Errorf("delete blobs of %s: %w", j.ID, err)
		}
		switch err := s.store.DeleteJob(ctx, j.ID); {
		case errors.Is(err, job.ErrNotFound): // deleted by someone else meanwhile
			return false, nil
		case err != nil:
			return false, fmt.Errorf("delete job %s: %w", j.ID, err)
		}
		if j.ContentDeletedAt.IsZero() || j.ContentDeletedAt.Before(j.ExpiresAt) {
			late++
			s.audit.JobExpired(ctx, j)
		}
		return true, nil
	})
	return removed, late, err
}

// stale finishes the running jobs whose time limit and grace are over: their worker is
// lost. The persisted candidate and feedback are kept.
func (s *Sweeper) stale(ctx context.Context, now time.Time) (int, error) {
	cutoff := now.Add(-(s.cfg.Limits.JobTimeout + grace))
	list := func() ([]job.Job, error) { return s.store.ListStaleRunning(ctx, cutoff, s.opts.Batch) }
	return s.batched(ctx, list, func(j job.Job) (bool, error) {
		out := fromRow(j, job.StatusFailed, extract.CodeTimeout)
		switch err := s.store.Finish(ctx, j.ID, "", out, now); {
		case errors.Is(err, job.ErrConflict), errors.Is(err, job.ErrNotFound): // finished or removed meanwhile
			return false, nil
		case err != nil:
			return false, fmt.Errorf("finish job %s: %w", j.ID, err)
		}
		j.Status, j.Attempts, j.Error, j.FinishedAt = out.Status, out.Attempts, out.Error, now
		s.audit.JobFinished(ctx, j)
		return true, s.clearInputs(ctx, j, now)
	})
}

// clearInputs is the terminal-state deletion of contract §10 for a job the worker did
// not finish.
func (s *Sweeper) clearInputs(ctx context.Context, j job.Job, now time.Time) error {
	if !inputsAtTerminal(application(ctx, s.store, s.apps, j.ApplicationID), j) {
		return nil
	}
	return deleteInputs(ctx, s.blobs, s.store, j.ID, now)
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
		n, err := s.store.DeleteQuotaBefore(ctx, a.ID, now.Add(-quotaWindowsToKeep*window))
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
