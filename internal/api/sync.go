package api

import (
	"net/http"
	"time"

	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/job"
)

// syncPollInterval is how often POST /v1/extract reads the store while it waits, which
// is how it sees a job another replica finished (the notifier is in-process only).
const syncPollInterval = time.Second

// syncDeadlineSlack is added to sync.max_wait for the connection's read and write
// deadlines, which the server's timeouts would otherwise end before the wait does.
const syncDeadlineSlack = 30 * time.Second

// extractSync is POST /v1/extract: the submission of POST /v1/jobs (createJob, same
// body, limits, quota and rate limits), then a wait of up to sync.max_wait for the
// terminal state. A terminal job answers 200 and counts as its first read (a
// retention: none job's content is deleted as by GET /v1/jobs/{id}); otherwise 202
// with the job as it is and Location, to poll. A replayed Idempotency-Key answers as
// POST /v1/jobs does.
func (s *server) extractSync(w http.ResponseWriter, r *http.Request) {
	created, done := s.createJob(w, r)
	if done {
		return
	}
	maxWait := config.MaxSyncWait
	if c := s.deps.Config; c != nil && c.Sync.MaxWait > 0 {
		maxWait = min(c.Sync.MaxWait, config.MaxSyncWait)
	}
	// The body is read: keep the server's ReadTimeout (whose background read would
	// cancel the request) and WriteTimeout from ending the wait. Unsupported writers
	// (tests) have neither.
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(maxWait + syncDeadlineSlack))
	_ = rc.SetWriteDeadline(time.Now().Add(maxWait + syncDeadlineSlack))

	w.Header().Set("Location", "/v1/jobs/"+created.ID)
	j, terminal := s.waitTerminal(r, *created, maxWait)
	if !terminal {
		writeJSON(w, http.StatusAccepted, jobToResponse(&j))
		return
	}
	if j.Retention == job.RetentionNone && !s.deleteOnFirstRead(w, r, j) {
		return
	}
	writeJSON(w, http.StatusOK, jobToResponse(&j))
}

// waitTerminal waits for j to become terminal: woken by the in-process notifier, or
// by a store read every syncPollInterval. It returns the last row read (j itself when
// no read succeeded) and whether it is terminal. A client that goes away ends the wait;
// the job runs on.
func (s *server) waitTerminal(r *http.Request, j job.Job, maxWait time.Duration) (job.Job, bool) {
	ctx := r.Context()
	var finished <-chan struct{}
	if n := s.deps.Completions; n != nil {
		ch, cancel := n.Subscribe(j.ID)
		defer cancel()
		finished = ch
	}
	limit := time.NewTimer(maxWait)
	defer limit.Stop()
	tick := time.NewTicker(syncPollInterval)
	defer tick.Stop()
	for {
		// Read first: the job may have finished before the subscription.
		if cur, err := s.deps.Store.GetJob(ctx, j.ApplicationID, j.ID); err == nil {
			j = cur
			if j.Status.Terminal() {
				return j, true
			}
		}
		select {
		case <-finished:
			finished = nil // closed: read once more, then poll
		case <-tick.C:
		case <-limit.C:
			return j, false
		case <-ctx.Done():
			return j, false
		}
	}
}
