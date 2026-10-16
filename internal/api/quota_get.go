package api

import (
	"log/slog"
	"net/http"
)

// quotaBody is GET /v1/quota (contract §8): the numbers of the quota headers.
type quotaBody struct {
	Limit     int    `json:"limit"`
	Remaining int    `json:"remaining"`
	Reset     string `json:"reset"`  // RFC 3339 UTC, second precision
	Window    string `json:"window"` // ISO 8601 duration as configured
}

// getQuota is GET /v1/quota. It reads the current window (quota.go) and changes nothing:
// quota reads are not charged, and the rate limit step before it counts the request like
// any other. The headers are set again from the same read, so body and headers agree
// even when a submission lands between the limit step and this handler.
func (s *server) getQuota(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	app, _ := ApplicationFrom(ctx)
	q, err := s.currentQuota(ctx, app)
	if err != nil {
		s.log.LogAttrs(ctx, slog.LevelError, "quota not readable",
			slog.String("request_id", requestIDFrom(ctx)), slog.String("error", err.Error()))
		writeError(w, r, newError(codeInternal, msgInternal))
		return
	}
	s.setQuotaHeaders(w.Header(), app, q)
	writeJSON(w, http.StatusOK, quotaBody{
		Limit:     q.Limit,
		Remaining: q.Remaining,
		Reset:     formatTime(q.Reset),
		Window:    q.Window,
	})
}
