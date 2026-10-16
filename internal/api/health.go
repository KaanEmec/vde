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
)

type healthBody struct {
	Status  string       `json:"status"`
	Reasons []string     `json:"reasons,omitempty"`
	Error   *errorDetail `json:"error,omitempty"`
}

// health is GET /healthz. It is unauthenticated, so every check is cheap: a one-row
// query with a 1 s timeout, the role groups' in-memory marks, and a data directory
// write probe cached for 30 s. It is not rate limited; operators expose it to the
// orchestrator (docs/operator).
type health struct {
	s *server

	mu        sync.Mutex
	checkedAt time.Time
	writable  bool
}

func (s *server) healthHandler() http.Handler { return &health{s: s} }

// ServeHTTP answers 200 {"status":"ok"} when the database answers, the data directory
// is writable and every role group has a pair not marked down, else 503 with the
// reasons and the contract §9 error object.
func (h *health) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var reasons []string
	if !h.dbOK(r.Context()) {
		reasons = append(reasons, reasonDB)
	}
	if !h.dataDirOK() {
		reasons = append(reasons, reasonBlobs)
	}
	if !healthy(h.s.deps.Extractor) {
		reasons = append(reasons, reasonExtractorDown)
	}
	if !healthy(h.s.deps.Judge) {
		reasons = append(reasons, reasonJudgeDown)
	}
	if len(reasons) == 0 {
		writeJSON(w, http.StatusOK, healthBody{Status: "ok"})
		return
	}
	e := newError(codeUnavailable, msgServiceUnhealthy)
	writeJSON(w, e.status, healthBody{
		Status:  "unavailable",
		Reasons: reasons,
		Error:   &errorDetail{Code: e.code, Message: e.message, RequestID: w.Header().Get(headerRequestID)},
	})
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
