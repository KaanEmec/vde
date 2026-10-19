package provider

import (
	"bytes"
	"context"
	"errors"
	"io"
	"maps"
	"net/http"
	"strconv"
	"time"

	"github.com/kaanemec/vde/internal/version"
)

// DefaultMaxResponseBytes caps a provider response body. Model answers are text; a body
// beyond this is a fault, not an answer.
const DefaultMaxResponseBytes = 8 << 20

// maxErrorBodyBytes bounds how much of a non-2xx body PostJSON returns.
const maxErrorBodyBytes = 64 << 10

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

// PostJSON posts body as JSON to endpoint with the extra header and returns the body and
// status of a 2xx response. Every failure is an *Error whose message starts with name and
// never quotes the response: an invalid URL is Permanent, a failed call or body read is
// classified by Classify, and a non-2xx response is StatusError. With a non-2xx response
// the returned bytes are at most 64 KiB of its body, for an adapter that names a
// documented error code; they must never reach an error message.
func PostJSON(ctx context.Context, client *http.Client, endpoint string, header http.Header, body []byte, name string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, 0, &Error{Message: name + ": invalid endpoint URL"}
	}
	maps.Copy(req.Header, header)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, &Error{Kind: Classify(err), Message: name + ": request failed", Err: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
		return data, resp.StatusCode, StatusError(resp, name+": request rejected")
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		msg := name + ": reading response failed"
		if errors.Is(err, ErrResponseTooLarge) {
			msg = name + ": response body too large"
		}
		return nil, 0, &Error{Kind: Classify(err), Message: msg, Err: err}
	}
	return raw, resp.StatusCode, nil
}

// Refused is the Permanent error of a 2xx answer that cannot be used (a refusal, a
// blocked answer, no text): asking again with the same input gets the same answer.
func Refused(status int, msg string) *Error {
	return &Error{Kind: Permanent, Status: status, Message: msg}
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
