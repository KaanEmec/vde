package provider

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// get returns the body and status of a GET; the response itself stays inside.
func get(t *testing.T, c *http.Client, url string) ([]byte, int, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return body, resp.StatusCode, err
}

func TestNewHTTPClient(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/ua", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, r.Header.Get("User-Agent"))
	})
	mux.HandleFunc("/redirect", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ua", http.StatusFound)
	})
	mux.HandleFunc("/body/{n}", func(w http.ResponseWriter, r *http.Request) {
		n := len(r.PathValue("n"))
		fmt.Fprint(w, strings.Repeat("x", n))
	})
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	client := NewHTTPClient(time.Second, 8)

	t.Run("user agent", func(t *testing.T) {
		t.Parallel()
		body, _, err := get(t, client, srv.URL+"/ua")
		if err != nil {
			t.Fatal(err)
		}
		if got := string(body); got != "vde/dev" {
			t.Errorf("User-Agent = %q, want vde/dev", got)
		}
	})
	t.Run("redirects are not followed", func(t *testing.T) {
		t.Parallel()
		_, status, err := get(t, NewHTTPClient(time.Second, DefaultMaxResponseBytes), srv.URL+"/redirect")
		if err != nil {
			t.Fatal(err)
		}
		if status != http.StatusFound {
			t.Errorf("status = %d, want 302 returned to the caller", status)
		}
		if got := StatusError(&http.Response{StatusCode: status}, "redirect").Kind; got != Permanent {
			t.Errorf("redirect classified %s, want permanent", got)
		}
	})
	t.Run("body at the cap", func(t *testing.T) {
		t.Parallel()
		body, _, err := get(t, client, srv.URL+"/body/12345678")
		if err != nil || len(body) != 8 {
			t.Errorf("body = %q, err = %v, want 8 bytes and no error", body, err)
		}
	})
	t.Run("body over the cap", func(t *testing.T) {
		t.Parallel()
		_, _, err := get(t, client, srv.URL+"/body/123456789")
		if !errors.Is(err, ErrResponseTooLarge) {
			t.Errorf("err = %v, want ErrResponseTooLarge", err)
		}
	})
	t.Run("timeout is retryable", func(t *testing.T) {
		t.Parallel()
		_, _, err := get(t, NewHTTPClient(50*time.Millisecond, 8), srv.URL+"/slow")
		if err == nil {
			t.Fatal("err = nil, want a timeout")
		}
		if got := Classify(err); got != Retryable {
			t.Errorf("Classify(%v) = %s, want retryable", err, got)
		}
	})
	t.Run("connection refused is retryable", func(t *testing.T) {
		t.Parallel()
		closed := httptest.NewServer(http.NotFoundHandler())
		closed.Close()
		_, _, err := get(t, client, closed.URL)
		if got := Classify(err); err == nil || got != Retryable {
			t.Errorf("Classify(%v) = %s, want retryable", err, got)
		}
	})
}

func TestStatusError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		status         int
		retryAfter     string
		wantKind       Kind
		wantRetryAfter time.Duration
	}{
		{name: "bad request", status: 400, wantKind: Permanent},
		{name: "unauthorized", status: 401, wantKind: Permanent},
		{name: "not found", status: 404, wantKind: Permanent},
		{name: "payload too large", status: 413, wantKind: Permanent},
		{name: "request timeout", status: 408, wantKind: Retryable},
		{name: "rate limited", status: 429, retryAfter: "7", wantKind: Retryable, wantRetryAfter: 7 * time.Second},
		{name: "internal error", status: 500, wantKind: Retryable},
		{name: "overloaded", status: 529, retryAfter: "soon", wantKind: Retryable},
		{name: "unavailable with date", status: 503, retryAfter: time.Now().Add(time.Hour).UTC().Format(http.TimeFormat), wantKind: Retryable, wantRetryAfter: time.Hour},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resp := &http.Response{StatusCode: tt.status, Header: http.Header{}}
			if tt.retryAfter != "" {
				resp.Header.Set("Retry-After", tt.retryAfter)
			}
			err := StatusError(resp, "error_type")
			if err.Kind != tt.wantKind || err.Status != tt.status || err.Message != "error_type" {
				t.Errorf("StatusError() = %+v, want kind %s", err, tt.wantKind)
			}
			if d := err.RetryAfter - tt.wantRetryAfter; d < -2*time.Second || d > 0 {
				t.Errorf("RetryAfter = %s, want about %s", err.RetryAfter, tt.wantRetryAfter)
			}
		})
	}
}
