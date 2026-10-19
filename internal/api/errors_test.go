package api

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWriteError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		code   string
		status int
	}{
		{codeBadRequest, 400},
		{codeUnauthorized, 401},
		{codeForbidden, 403},
		{codeNotFound, 404},
		{codeMethodNotAllowed, 405},
		{codeConflict, 409},
		{codePayloadTooLarge, 413},
		{codeUnsupportedMediaType, 415},
		{codeUnsupportedDocument, 415},
		{codeInvalidSchema, 422},
		{codeIdempotencyMismatch, 422},
		{codeRateLimited, 429},
		{codeQuotaExhausted, 429},
		{codeInternal, 500},
		{codeProviderUnavailable, 502},
		{codeUnavailable, 503},
		{codeTimeout, 504},
	}
	if len(tests) != len(codeStatus) {
		t.Fatalf("table has %d codes, codeStatus %d: keep them in step", len(tests), len(codeStatus))
	}
	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			t.Parallel()
			w := httptest.NewRecorder()
			w.Header().Set(headerRequestID, "r_test")
			w.Header().Set("X-Quota-Remaining", "7") // set earlier by the quota step
			writeError(w, newError(tt.code, msgInternal))
			if w.Code != tt.status {
				t.Errorf("status = %d, want %d", w.Code, tt.status)
			}
			e := decodeError(t, w)
			if e.Code != tt.code || e.Message != msgInternal || e.RequestID != "r_test" {
				t.Errorf("error = %+v", e)
			}
			if w.Header().Get("X-Quota-Remaining") != "7" {
				t.Error("writeError dropped a header set before it")
			}
			if ra := w.Header().Get("Retry-After"); ra != "" {
				t.Errorf("Retry-After = %q without one given", ra)
			}
		})
	}
}

func TestWriteErrorRetryAfter(t *testing.T) {
	t.Parallel()
	tests := []struct {
		d    time.Duration
		want string
	}{
		{1200 * time.Millisecond, "2"},
		{30 * time.Second, "30"},
		{time.Millisecond, "1"},
	}
	for _, tt := range tests {
		w := httptest.NewRecorder()
		writeError(w, newError(codeRateLimited, msgInternal).withRetryAfter(tt.d))
		if got := w.Header().Get("Retry-After"); got != tt.want {
			t.Errorf("Retry-After for %s = %q, want %q", tt.d, got, tt.want)
		}
	}
}

// TestWriteErrorOther: an error that is not an apiError is answered as a 500 internal
// error, its text never reaching the body.
func TestWriteErrorOther(t *testing.T) {
	t.Parallel()
	w := httptest.NewRecorder()
	writeError(w, errors.New("SENTINEL store detail"))
	if e := decodeError(t, w); w.Code != 500 || e.Code != codeInternal || strings.Contains(w.Body.String(), "SENTINEL") {
		t.Errorf("status %d, body %s", w.Code, w.Body)
	}
}

func TestNewError(t *testing.T) {
	t.Parallel()
	if e := newError("no_such_code", msgInternal); e.status != 500 || e.code != codeInternal {
		t.Errorf("unknown code gave %+v, want internal 500", e)
	}
	const template = "The %s part is over the limit of %d bytes."
	if e := newError(codePayloadTooLarge, template, "document", 26214400); e.message != "The document part is over the limit of 26214400 bytes." {
		t.Errorf("message = %q", e.message)
	}
}
