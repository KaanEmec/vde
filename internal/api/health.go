package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"
)

// Reasons /healthz reports when the service cannot accept jobs: codes only, never the
// underlying error (contract §12).
const (
	reasonDB            = "db"    // the database (and so the job queue) does not answer
	reasonBlobs         = "blobs" // the data directory is not writable
	reasonExtractorDown = "role_extractor_down"
	reasonJudgeDown     = "role_judge_down"
)

const (
	healthDBTimeout   = time.Second
	dataDirCheckEvery = 30 * time.Second
	// The database check runs at most healthDBBurst times at once and healthDBRate times
	// per second after that, for the whole process; a request over the rate gets the last
	// database result. Orchestrator probes (a few per minute) never reach the limit, and
	// a flood of unauthenticated requests cannot turn into a flood of queries, nor make
	// the probes fail as a 429 would.
	healthDBRate  = 5
	healthDBBurst = 10
)

type healthBody struct {
	Status  string   `json:"status"`
	Reasons []string `json:"reasons,omitempty"`
	// QueueAgeSeconds, on an API-only process (RoleAPI), is how long the oldest queued
	// job has waited, 0 when none waits: growing, it means no worker process leases.
	QueueAgeSeconds *int64       `json:"queue_age_seconds,omitempty"`
	Error           *errorDetail `json:"error,omitempty"`
}

// health is GET /healthz. It is unauthenticated, so every check is cheap: a one-row
// query with a 1 s timeout under a global rate (healthDBRate), the role groups'
// in-memory marks, and a data directory write probe cached for 30 s. Requests are not
// refused: over the rate, the last database result is reused (docs/security.md).
type health struct {
	s *server

	mu        sync.Mutex
	checkedAt time.Time
	writable  bool

	dbMu      sync.Mutex // guards the fields below, never held through a query
	dbBusy    bool       // a database check is running; others reuse the last result
	dbTokens  float64    // database checks available now, at most healthDBBurst
	dbAt      time.Time  // when dbTokens was last refilled; zero before the first check
	dbChecked bool       // dbAge and dbUp hold a result
	dbAge     int64
	dbUp      bool
}

func (s *server) healthHandler() http.Handler { return &health{s: s} }

// ServeHTTP answers 200 {"status":"ok"} when the database answers, the data directory
// is writable and every role group has a pair not marked down, else 503 with the
// reasons and the contract §9 error object. An API-only process (RoleAPI) calls no
// provider, so it checks no role group; it reports queue_age_seconds instead, for the
// orchestrator to notice missing workers, and the age alone never makes it unhealthy.
func (h *health) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var reasons []string
	body := healthBody{Status: "ok"}
	age, ok := h.dbCheck(r.Context())
	switch {
	case !ok:
		reasons = append(reasons, reasonDB)
	case h.s.deps.Role == RoleAPI:
		body.QueueAgeSeconds = &age
	}
	if !h.dataDirOK() {
		reasons = append(reasons, reasonBlobs)
	}
	if h.s.deps.Role != RoleAPI {
		if !healthy(h.s.deps.Extractor) {
			reasons = append(reasons, reasonExtractorDown)
		}
		if !healthy(h.s.deps.Judge) {
			reasons = append(reasons, reasonJudgeDown)
		}
	}
	if len(reasons) == 0 {
		writeJSON(w, http.StatusOK, body)
		return
	}
	e := newError(codeUnavailable, msgServiceUnhealthy)
	body.Status, body.Reasons = "unavailable", reasons
	body.Error = &errorDetail{Code: e.code, Message: e.message, RequestID: w.Header().Get(headerRequestID)}
	writeJSON(w, e.status, body)
}

// dbCheck runs the database check of the process's role (queueAge for an API-only
// process, dbOK otherwise) when the rate allows and no other check is running, else
// returns the last result, so a slow database never queues requests behind a check. The
// check runs without the request's cancellation, under its own healthDBTimeout: a client
// that hangs up cannot fail it. A result that failed for a context error is returned but
// not kept.
func (h *health) dbCheck(ctx context.Context) (int64, bool) {
	h.dbMu.Lock()
	now := h.s.now()
	switch {
	case h.dbAt.IsZero():
		h.dbTokens = healthDBBurst
	case now.After(h.dbAt):
		h.dbTokens = min(h.dbTokens+now.Sub(h.dbAt).Seconds()*healthDBRate, healthDBBurst)
	}
	if !now.Before(h.dbAt) {
		h.dbAt = now // a clock that went back refills nothing until it passes dbAt again
	}
	if h.dbChecked && (h.dbTokens < 1 || h.dbBusy) {
		defer h.dbMu.Unlock()
		return h.dbAge, h.dbUp
	}
	h.dbTokens--
	h.dbBusy = true
	h.dbMu.Unlock()

	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), healthDBTimeout)
	defer cancel()
	var age int64
	var err error
	if h.s.deps.Role == RoleAPI {
		age, err = h.queueAge(ctx)
	} else {
		err = h.dbOK(ctx)
	}
	if err != nil {
		h.s.log.LogAttrs(ctx, slog.LevelWarn, "healthz: database check failed",
			slog.String("request_id", requestIDFrom(ctx)), slog.String("error", err.Error()))
	}

	h.dbMu.Lock()
	defer h.dbMu.Unlock()
	h.dbBusy = false
	// The check runs detached from the request (WithoutCancel), so a context error here is
	// the check's own 1 s deadline, a real signal: a hung database is kept as down.
	h.dbAge, h.dbUp, h.dbChecked = age, err == nil, true
	return age, err == nil
}

// queueAge is the database check of an API-only process: the age in whole seconds of the
// oldest queued job (one MIN over the queue index), 0 when none waits.
func (h *health) queueAge(ctx context.Context) (int64, error) {
	if h.s.deps.Store == nil {
		return 0, errNoStore
	}
	oldest, err := h.s.deps.Store.OldestQueuedAt(ctx)
	if err != nil || oldest.IsZero() {
		return 0, err
	}
	return max(int64(h.s.now().Sub(oldest)/time.Second), 0), nil
}

// dbOK runs a one-row query on the jobs table, the queue.
func (h *health) dbOK(ctx context.Context) error {
	if h.s.deps.Store == nil {
		return errNoStore
	}
	_, err := h.s.deps.Store.CountRunning(ctx, "")
	return err
}

var errNoStore = errors.New("no job store")

// dataDirOK reports whether a file can be created in server.data_dir, rechecking at most
// every 30 s. Without a data directory (tests) there is nothing to check.
func (h *health) dataDirOK() bool {
	if h.s.deps.Config == nil || h.s.deps.Config.Server.DataDir == "" {
		return true
	}
	now := h.s.now()
	h.mu.Lock()
	defer h.mu.Unlock()
	if age := now.Sub(h.checkedAt); !h.checkedAt.IsZero() && age >= 0 && age < dataDirCheckEvery {
		return h.writable
	}
	h.checkedAt, h.writable = now, probeWritable(h.s.deps.Config.Server.DataDir)
	if !h.writable {
		h.s.log.Warn("healthz: data directory is not writable")
	}
	return h.writable
}

// probeWritable creates and removes a temporary file in dir.
func probeWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".healthz-*")
	if err != nil {
		return false
	}
	name := f.Name()
	closeErr := f.Close()
	removeErr := os.Remove(name)
	return closeErr == nil && removeErr == nil
}

func healthy(c func() bool) bool { return c != nil && c() }
