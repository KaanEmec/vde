package api

import (
	"context"
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

	dbMu      sync.Mutex // held through a database check
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
// process, dbOK otherwise) when the rate allows, else returns the last result. Checks
// run one at a time.
func (h *health) dbCheck(ctx context.Context) (int64, bool) {
	h.dbMu.Lock()
	defer h.dbMu.Unlock()
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
	if h.dbTokens < 1 && h.dbChecked {
		return h.dbAge, h.dbUp
	}
	h.dbTokens--
	if h.s.deps.Role == RoleAPI {
		h.dbAge, h.dbUp = h.queueAge(ctx)
	} else {
		h.dbAge, h.dbUp = 0, h.dbOK(ctx)
	}
	h.dbChecked = true
	return h.dbAge, h.dbUp
}

// queueAge is the database check of an API-only process: the age in whole seconds of the
// oldest queued job (one MIN over the queue index, 1 s timeout), and false when the
// database does not answer.
func (h *health) queueAge(ctx context.Context) (int64, bool) {
	if h.s.deps.Store == nil {
		return 0, false
	}
	ctx, cancel := context.WithTimeout(ctx, healthDBTimeout)
	defer cancel()
	oldest, err := h.s.deps.Store.OldestQueuedAt(ctx)
	if err != nil {
		h.s.log.LogAttrs(ctx, slog.LevelWarn, "healthz: database check failed",
			slog.String("request_id", requestIDFrom(ctx)), slog.String("error", err.Error()))
		return 0, false
	}
	if oldest.IsZero() {
		return 0, true
	}
	return max(int64(h.s.now().Sub(oldest)/time.Second), 0), true
}

// dbOK runs a one-row query on the jobs table, the queue.
func (h *health) dbOK(ctx context.Context) bool {
	if h.s.deps.Store == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, healthDBTimeout)
	defer cancel()
	if _, err := h.s.deps.Store.CountRunning(ctx, ""); err != nil {
		h.s.log.LogAttrs(ctx, slog.LevelWarn, "healthz: database check failed",
			slog.String("request_id", requestIDFrom(ctx)), slog.String("error", err.Error()))
		return false
	}
	return true
}

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

func healthy(c HealthChecker) bool { return c != nil && c.Healthy() }
