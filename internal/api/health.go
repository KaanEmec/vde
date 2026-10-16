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
	Status  string   `json:"status"`
	Reasons []string `json:"reasons,omitempty"`
	// QueueAgeSeconds, on an API-only process (RoleAPI), is how long the oldest queued
	// job has waited, 0 when none waits: growing, it means no worker process leases.
	QueueAgeSeconds *int64       `json:"queue_age_seconds,omitempty"`
	Error           *errorDetail `json:"error,omitempty"`
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
// reasons and the contract §9 error object. An API-only process (RoleAPI) calls no
// provider, so it checks no role group; it reports queue_age_seconds instead, for the
// orchestrator to notice missing workers, and the age alone never makes it unhealthy.
func (h *health) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var reasons []string
	body := healthBody{Status: "ok"}
	if h.s.deps.Role == RoleAPI {
		age, ok := h.queueAge(r.Context())
		if !ok {
			reasons = append(reasons, reasonDB)
		} else {
			body.QueueAgeSeconds = &age
		}
	} else if !h.dbOK(r.Context()) {
		reasons = append(reasons, reasonDB)
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
