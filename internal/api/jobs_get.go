package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/kaanemec/vde/internal/audit"
	"github.com/kaanemec/vde/internal/job"
)

// msgJobNotFound answers an unknown, foreign, expired or deleted job alike, so a caller
// cannot tell another application's job id from a missing one (contract §5).
const msgJobNotFound = "No job with this id exists for this application."

// getJob is GET /v1/jobs/{id} (contract §5). Polls are not charged to the quota; the
// rate limit step counts them. The first read of a terminal retention: none job deletes
// its content (contract §10, getJobFirstRead).
func (s *server) getJob(w http.ResponseWriter, r *http.Request) {
	j, ok := s.loadJob(w, r)
	if !ok {
		return
	}
	if j.Retention == job.RetentionNone && j.Status.Terminal() && !s.deleteOnFirstRead(w, r, j) {
		return
	}
	writeJSON(w, http.StatusOK, jobToResponse(&j))
}

// deleteJob is DELETE /v1/jobs/{id}: the blobs, then the row with its content, at once;
// 204. Only the audit record stays. Once started it finishes even if the client leaves.
func (s *server) deleteJob(w http.ResponseWriter, r *http.Request) {
	j, ok := s.loadJob(w, r)
	if !ok {
		return
	}
	ctx := context.WithoutCancel(r.Context())
	if s.deps.Blobs != nil {
		if err := s.deps.Blobs.DeleteAll(ctx, j.ID); err != nil {
			s.jobError(w, r, j, "job blobs not deleted", err)
			return
		}
	}
	switch err := s.deps.Store.DeleteJob(ctx, j.ID); {
	case errors.Is(err, job.ErrNotFound): // a concurrent DELETE won
		writeError(w, r, newError(codeNotFound, msgJobNotFound))
		return
	case err != nil:
		s.jobError(w, r, j, "job not deleted", err)
		return
	}
	s.auditDeleted(ctx, j, audit.DeletedByRequest)
	w.WriteHeader(http.StatusNoContent)
}

// loadJob reads the request's job, scoped to the authenticated application in SQL. A
// job that does not exist, belongs to another application, has expired or had its
// content deleted answers 404; it reports false after answering.
func (s *server) loadJob(w http.ResponseWriter, r *http.Request) (job.Job, bool) {
	app, _ := ApplicationFrom(r.Context())
	j, err := s.deps.Store.GetJob(r.Context(), app.ID, r.PathValue("id"))
	switch {
	case errors.Is(err, job.ErrNotFound):
	case err != nil:
		s.jobError(w, r, job.Job{ApplicationID: app.ID}, "job not readable", err)
		return job.Job{}, false
	case !j.ContentDeletedAt.IsZero() || !s.now().Before(j.ExpiresAt):
	default:
		return j, true
	}
	writeError(w, r, newError(codeNotFound, msgJobNotFound))
	return job.Job{}, false
}

// deleteOnFirstRead clears a terminal retention: none job's result, feedback, error,
// instructions and schema before the body read from the row is sent, and removes any
// blob left behind. The store clears only a job not cleared yet, in one statement, so
// of two concurrent first reads exactly one gets the body and the other 404: a poll
// sees the whole job or nothing. It reports false after answering.
func (s *server) deleteOnFirstRead(w http.ResponseWriter, r *http.Request, j job.Job) bool {
	switch err := s.clearFirstRead(r, j); {
	case errors.Is(err, job.ErrNotFound): // another read deleted it first
		writeError(w, r, newError(codeNotFound, msgJobNotFound))
		return false
	case err != nil:
		s.jobError(w, r, j, "job content not deleted", err)
		return false
	}
	return true
}

// clearFirstRead is the deletion of deleteOnFirstRead. It returns job.ErrNotFound when
// another read cleared the job first.
func (s *server) clearFirstRead(r *http.Request, j job.Job) error {
	ctx := context.WithoutCancel(r.Context())
	if err := s.deps.Store.MarkContentDeleted(ctx, j.ID, job.ContentAll, s.now()); err != nil {
		return err
	}
	if s.deps.Blobs != nil { // normally gone at the terminal state already
		if err := s.deps.Blobs.DeleteAll(ctx, j.ID); err != nil {
			s.log.LogAttrs(ctx, slog.LevelWarn, "job blobs not deleted; the sweeper retries at expiry",
				slog.String("request_id", requestIDFrom(ctx)), slog.String("job_id", j.ID), slog.String("error", err.Error()))
		}
	}
	s.auditDeleted(ctx, j, audit.DeletedFirstRead)
	return nil
}

// auditDeleted records job.deleted.
func (s *server) auditDeleted(ctx context.Context, j job.Job, reason string) {
	s.recordAudit(ctx, j.ApplicationID, j.ID, audit.JobDeleted{Reason: reason, Retention: string(j.Retention)})
}

// jobError logs a store or blob failure (the error text never carries content) and
// answers 500.
func (s *server) jobError(w http.ResponseWriter, r *http.Request, j job.Job, msg string, err error) {
	s.log.LogAttrs(r.Context(), slog.LevelError, msg,
		slog.String("request_id", requestIDFrom(r.Context())), slog.String("app_id", j.ApplicationID),
		slog.String("job_id", j.ID), slog.String("error", err.Error()))
	writeError(w, r, newError(codeInternal, msgInternal))
}
