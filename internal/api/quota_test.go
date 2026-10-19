package api

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/kaanemec/vde/internal/job"
)

func TestQuotaWindow(t *testing.T) {
	t.Parallel()
	at := func(s string) time.Time {
		v, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	tests := []struct {
		now, window, start, reset string
	}{
		{"2026-10-16T12:00:00Z", "P1D", "2026-10-16T00:00:00Z", "2026-10-17T00:00:00Z"},
		{"2026-10-16T23:59:59Z", "P1D", "2026-10-16T00:00:00Z", "2026-10-17T00:00:00Z"},
		{"2026-10-17T00:00:00Z", "P1D", "2026-10-17T00:00:00Z", "2026-10-18T00:00:00Z"},
		{"2026-10-17T01:30:00+02:00", "P1D", "2026-10-16T00:00:00Z", "2026-10-17T00:00:00Z"}, // UTC, not local
		{"2026-10-16T12:34:56Z", "PT1H", "2026-10-16T12:00:00Z", "2026-10-16T13:00:00Z"},
		{"2026-10-16T12:00:00Z", "P7D", "2026-10-12T00:00:00Z", "2026-10-19T00:00:00Z"}, // Monday to Monday
		{"2026-10-16T12:00:00Z", "P1W", "2026-10-12T00:00:00Z", "2026-10-19T00:00:00Z"},
	}
	for _, tt := range tests {
		start, reset, err := quotaWindow(at(tt.now), tt.window)
		if err != nil {
			t.Fatal(err)
		}
		if !start.Equal(at(tt.start)) || !reset.Equal(at(tt.reset)) || start.Location() != time.UTC {
			t.Errorf("%s %s: window %s – %s, want %s – %s", tt.now, tt.window, start, reset, tt.start, tt.reset)
		}
	}
	if _, _, err := quotaWindow(time.Now(), "P1M"); err == nil {
		t.Error("a month window has no fixed length and must be refused")
	}
}

// TestQuotaExhaustion: contract §14 item 7. The quota is spent at exactly its limit;
// the next submission is 429 with the three headers; polls still work and are not
// charged; no model runs for the refused submission.
func TestQuotaExhaustion(t *testing.T) {
	t.Parallel()
	rg := newSubmitRig(t, submitRigOptions{pool: true, app: func(a *job.Application) { a.QuotaLimit = 3 }})
	for i := range 3 {
		w := rg.submit(t, validParts(), nil)
		rg.waitTerminal(t, decodeJob(t, w)["id"].(string))
		if got := w.Header().Get(headerQuotaRemaining); got != strconv.Itoa(2-i) {
			t.Errorf("submission %d: X-Quota-Remaining = %q, want %d", i+1, got, 2-i)
		}
	}
	calls := rg.providerCalls()
	w := rg.submit(t, validParts(), nil)
	if w.Code != http.StatusTooManyRequests || decodeError(t, w).Code != codeQuotaExhausted {
		t.Fatalf("status = %d body %s, want 429 quota_exhausted", w.Code, w.Body.String())
	}
	reset, err := time.Parse(time.RFC3339, w.Header().Get(headerQuotaReset))
	if err != nil || w.Header().Get(headerQuotaRemaining) != "0" {
		t.Fatalf("quota headers %q %q, want 0 and an RFC 3339 reset", w.Header().Get(headerQuotaRemaining),
			w.Header().Get(headerQuotaReset))
	}
	retry, err := strconv.Atoi(w.Header().Get("Retry-After"))
	if want := time.Until(reset).Seconds(); err != nil || float64(retry) < want-2 || float64(retry) > want+2 || retry < 1 {
		t.Errorf("Retry-After = %q, want the %.0f seconds to the reset", w.Header().Get("Retry-After"), want)
	}
	for range 5 {
		if p := rg.get("/v1/openapi.yaml"); p.Code != http.StatusOK || p.Header().Get(headerQuotaRemaining) != "0" {
			t.Errorf("poll after exhaustion: %d with remaining %q, want 200 and 0", p.Code, p.Header().Get(headerQuotaRemaining))
		}
	}
	if rg.quotaUsed(t) != 3 || rg.blobJobs(t) != 3 {
		t.Errorf("quota used %d, blob jobs %d; want 3 and 3", rg.quotaUsed(t), rg.blobJobs(t))
	}
	time.Sleep(50 * time.Millisecond)
	if rg.providerCalls() != calls {
		t.Errorf("provider calls %d after the refusal, want %d", rg.providerCalls(), calls)
	}
}

func TestQuotaResetBoundary(t *testing.T) {
	t.Parallel()
	rg := newSubmitRig(t, submitRigOptions{
		now: time.Date(2026, 10, 16, 23, 59, 59, 0, time.UTC),
		app: func(a *job.Application) { a.QuotaLimit = 1 },
	})
	decodeJob(t, rg.submit(t, validParts(), nil))
	w := rg.submit(t, validParts(), nil)
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "1" ||
		w.Header().Get(headerQuotaReset) != "2026-10-17T00:00:00Z" {
		t.Fatalf("before the reset: %d Retry-After %q reset %q, want 429, 1 and 2026-10-17T00:00:00Z",
			w.Code, w.Header().Get("Retry-After"), w.Header().Get(headerQuotaReset))
	}
	rg.clock.Set(time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC))
	if p := rg.get("/v1/openapi.yaml"); p.Header().Get(headerQuotaRemaining) != "1" ||
		p.Header().Get(headerQuotaReset) != "2026-10-18T00:00:00Z" {
		t.Errorf("after the reset: remaining %q reset %q, want 1 and 2026-10-18T00:00:00Z",
			p.Header().Get(headerQuotaRemaining), p.Header().Get(headerQuotaReset))
	}
	w = rg.submit(t, validParts(), nil)
	decodeJob(t, w)
	if w.Header().Get(headerQuotaRemaining) != "0" {
		t.Errorf("X-Quota-Remaining = %q, want 0", w.Header().Get(headerQuotaRemaining))
	}
}

// TestQuotaHeaders: on every authenticated response (200, 404, 429), never on 401, and
// reads never charge.
func TestQuotaHeaders(t *testing.T) {
	t.Parallel()
	rg := newSubmitRig(t, submitRigOptions{
		now: time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC),
		app: func(a *job.Application) { a.RequestsPerMinute = 3 },
	})
	notFound := requestID(rg.s.authenticate("", rg.s.limit(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, newError(codeNotFound, msgRouteNotFound))
	}))))
	auth := func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+rg.token) }
	check := func(name string, w interface {
		Header() http.Header
	}, wantRemaining string) {
		t.Helper()
		if got := w.Header().Get(headerQuotaRemaining); got != wantRemaining {
			t.Errorf("%s: X-Quota-Remaining = %q, want %q", name, got, wantRemaining)
		}
		if got := w.Header().Get(headerQuotaReset); wantRemaining != "" && got != "2026-10-17T00:00:00Z" {
			t.Errorf("%s: X-Quota-Reset = %q", name, got)
		}
	}
	ok := rg.get("/v1/openapi.yaml")
	if ok.Code != http.StatusOK {
		t.Fatalf("status = %d", ok.Code)
	}
	check("200", ok, "100")
	nf := serve(notFound, http.MethodGet, "/v1/jobs/j_missing", auth)
	if nf.Code != http.StatusNotFound {
		t.Fatalf("status = %d", nf.Code)
	}
	check("404", nf, "100")
	rg.get("/v1/openapi.yaml")
	limited := rg.get("/v1/openapi.yaml")
	if limited.Code != http.StatusTooManyRequests || decodeError(t, limited).Code != codeRateLimited {
		t.Fatalf("status = %d, want 429 rate_limited", limited.Code)
	}
	check("429", limited, "100")
	unauth := serve(rg.h, http.MethodGet, "/v1/openapi.yaml", nil)
	if unauth.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", unauth.Code)
	}
	check("401", unauth, "")
	if unauth.Header().Get(headerQuotaReset) != "" {
		t.Error("401 carries X-Quota-Reset")
	}
	if rg.quotaUsed(t) != 0 {
		t.Errorf("reads charged %d", rg.quotaUsed(t))
	}
}

// TestQuotaSpentRefusedBeforeDocumentWork: with the quota spent, a submission is refused
// 429 quota_exhausted after the part rules but before the document checks, the schema
// compilation and the PDF engine, so a spent quota buys no work: an invalid schema gets
// the 429, not 400 invalid_schema.
func TestQuotaSpentRefusedBeforeDocumentWork(t *testing.T) {
	t.Parallel()
	rg := newSubmitRig(t, submitRigOptions{app: func(a *job.Application) { a.QuotaLimit = 1 }})
	decodeJob(t, rg.submit(t, validParts(), nil))
	badSchema := withPart(validParts(), mpart{name: partSchema, ctype: "application/schema+json", data: []byte(`{"type":"nonsense"}`)})
	w := rg.submit(t, badSchema, nil)
	if w.Code != http.StatusTooManyRequests || decodeError(t, w).Code != codeQuotaExhausted ||
		w.Header().Get(headerQuotaRemaining) != "0" || w.Header().Get("Retry-After") == "" {
		t.Fatalf("status %d body %s remaining %q, want 429 quota_exhausted with 0 and Retry-After", w.Code, w.Body,
			w.Header().Get(headerQuotaRemaining))
	}
	if rg.quotaUsed(t) != 1 || rg.blobJobs(t) != 1 {
		t.Errorf("quota used %d, blob jobs %d; want 1 and 1", rg.quotaUsed(t), rg.blobJobs(t))
	}
}
