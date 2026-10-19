package api

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/job"
)

// Quota headers of contract §8, on every authenticated response.
const (
	headerQuotaRemaining = "X-Quota-Remaining"
	headerQuotaReset     = "X-Quota-Reset"
)

// quotaState is an application's quota in the current window: the numbers of the quota
// headers and of the GET /v1/quota body.
type quotaState struct {
	Limit     int
	Remaining int
	Reset     time.Time // end of the window, UTC
	Window    string    // as configured, an ISO 8601 duration such as P1D
	start     time.Time // the window's key in quota_usage
}

// quotaWindow returns the window containing now: windows of length window are aligned
// to UTC boundaries counted from 0001-01-01T00:00:00Z, so a day window starts at 00:00
// UTC, an hour window on the hour and a week window on Monday 00:00 UTC.
func quotaWindow(now time.Time, window string) (start, reset time.Time, err error) {
	d, err := config.ParseWindow(window)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	start = now.UTC().Truncate(d)
	return start, start.Add(d), nil
}

// currentQuota reads the application's quota in the window containing the server's now.
// It changes nothing.
func (s *server) currentQuota(ctx context.Context, app job.Application) (quotaState, error) {
	start, reset, err := quotaWindow(s.now(), app.QuotaWindow)
	if err != nil {
		return quotaState{}, fmt.Errorf("quota window of application %s: %w", app.ID, err)
	}
	used, err := s.deps.Store.GetQuota(ctx, app.ID, start)
	if err != nil {
		return quotaState{}, err
	}
	return quotaState{
		Limit:     app.QuotaLimit,
		Remaining: max(app.QuotaLimit-used, 0),
		Reset:     reset,
		Window:    app.QuotaWindow,
		start:     start,
	}, nil
}

// chargeQuota charges one submission to the window of q and returns q with the new
// remaining count; job.ErrQuotaExhausted when the window is spent.
func (s *server) chargeQuota(ctx context.Context, app job.Application, q quotaState) (quotaState, error) {
	used, err := s.deps.Store.IncrementQuota(ctx, app.ID, q.start, q.Limit)
	if err != nil {
		return q, err
	}
	q.Remaining = max(q.Limit-used, 0)
	return q, nil
}

// setQuotaHeaders writes X-Quota-Remaining and X-Quota-Reset for q, and reports the
// remaining quota to the metrics.
func (s *server) setQuotaHeaders(h http.Header, app job.Application, q quotaState) {
	h.Set(headerQuotaRemaining, strconv.Itoa(q.Remaining))
	h.Set(headerQuotaReset, formatTime(q.Reset))
	s.deps.Metrics.SetQuotaRemaining(app.ID, q.Remaining)
}

// quotaHeaders is the first half of the limit step: it puts the quota headers on the
// response before the handler runs, so errors carry them too. It reports false after
// answering 500 when the quota cannot be read.
func (s *server) quotaHeaders(w http.ResponseWriter, r *http.Request, app job.Application) bool {
	q, err := s.currentQuota(r.Context(), app)
	if err != nil {
		s.log.LogAttrs(r.Context(), slog.LevelError, "quota not readable",
			slog.String("request_id", requestIDFrom(r.Context())), slog.String("error", err.Error()))
		closeIfBodyPending(w, r)
		writeError(w, r, newError(codeInternal, msgInternal))
		return false
	}
	s.setQuotaHeaders(w.Header(), app, q)
	return true
}

// untilReset is the Retry-After of a spent quota: whole seconds to the reset, at least 1.
func untilReset(now, reset time.Time) time.Duration {
	return wholeSeconds(reset.Sub(now))
}

// wholeSeconds rounds d up to whole seconds, at least one.
func wholeSeconds(d time.Duration) time.Duration {
	secs := math.Ceil(d.Seconds())
	if secs < 1 {
		secs = 1
	}
	return time.Duration(secs) * time.Second
}
