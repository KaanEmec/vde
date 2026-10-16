package provider

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/kaanemec/vde/internal/version"
)

// DefaultMaxResponseBytes caps a provider response body. Model answers are text; a body
// beyond this is a fault, not an answer.
const DefaultMaxResponseBytes = 8 << 20

// ErrResponseTooLarge is returned by a response body read past the client's cap.
var ErrResponseTooLarge = errors.New("provider: response body exceeds the size cap")

// NewHTTPClient returns the client every adapter uses: a total timeout per call (the
// endpoint's configured timeout), no redirects (a 3xx is returned as the response, so an
// endpoint cannot forward a document elsewhere), a cap on the response body and the
// User-Agent vde/<version>.
func NewHTTPClient(timeout time.Duration, maxResponseBytes int64) *http.Client {
	base := http.DefaultTransport.(*http.Transport).Clone()
	return &http.Client{
		Timeout:   timeout,
		Transport: &transport{base: base, maxBody: maxResponseBytes},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// UserAgent is the User-Agent header sent to providers.
func UserAgent() string { return "vde/" + version.Version }

type transport struct {
	base    http.RoundTripper
	maxBody int64
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context()) // a RoundTripper must not modify the caller's request
	req.Header.Set("User-Agent", UserAgent())
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	resp.Body = &cappedBody{rc: resp.Body, remaining: t.maxBody}
	return resp, nil
}

// cappedBody fails with ErrResponseTooLarge once more than its cap has been read.
type cappedBody struct {
	rc        io.ReadCloser
	remaining int64
}

func (b *cappedBody) Read(p []byte) (int, error) {
	if b.remaining <= 0 {
		var one [1]byte
		n, err := b.rc.Read(one[:])
		if n > 0 {
			return 0, ErrResponseTooLarge
		}
		return 0, err
	}
	if int64(len(p)) > b.remaining {
		p = p[:b.remaining]
	}
	n, err := b.rc.Read(p)
	b.remaining -= int64(n)
	return n, err
}

func (b *cappedBody) Close() error { return b.rc.Close() }

// StatusError classifies a non-2xx response: 408, 429 and 5xx are Retryable, everything
// else Permanent. RetryAfter is taken from the Retry-After header (seconds or HTTP date).
// message must be safe to log; adapters pass the provider's error type or code, never the
// request content.
func StatusError(resp *http.Response, message string) *Error {
	kind := Permanent
	if resp.StatusCode == http.StatusRequestTimeout ||
		resp.StatusCode == http.StatusTooManyRequests ||
		resp.StatusCode >= 500 {
		kind = Retryable
	}
	return &Error{
		Kind:       kind,
		Status:     resp.StatusCode,
		Message:    message,
		RetryAfter: retryAfter(resp.Header.Get("Retry-After"), time.Now()),
	}
}

func retryAfter(v string, now time.Time) time.Duration {
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil && t.After(now) {
		return t.Sub(now)
	}
	return 0
}
