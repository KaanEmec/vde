package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"syscall"
	"time"
)

// Part is one piece of user content. Exactly one field is set.
type Part struct {
	Text     string
	Image    *Blob // MediaType image/*
	Document *Blob // MediaType application/pdf (or others an adapter declares)
}

// Blob is binary content with its media type.
type Blob struct {
	MediaType string
	Data      []byte
}

// Request is one role call: a framing, the user content and the model to run it on.
type Request struct {
	Model     string
	System    string // the role framing
	Parts     []Part // document representation, schema, instructions, candidate, feedback
	MaxTokens int    // the router sets the pair's output-token limit
}

// Response is the model's text answer and its token usage.
type Response struct {
	Text         string
	InputTokens  int
	OutputTokens int
	// Truncated: the model stopped at the output-token limit, so Text may be cut off.
	Truncated bool
}

// Provider is the one interface every model call goes through. Implementations return
// an *Error (or an error Classify understands) on failure, and never put document
// content, instructions or answers into error messages.
type Provider interface {
	Complete(ctx context.Context, req Request) (Response, error)
}

// Kind tells the router whether failing over to the next endpoint can help.
type Kind int

const (
	// Permanent failures (4xx other than 408 and 429, refusal, unsupported input) are not
	// retried. It is the zero value, so an unclassified failure is never retried blindly.
	Permanent Kind = iota
	// Retryable failures (network, 5xx, 408, 429, timeout) may succeed elsewhere or later.
	Retryable
)

func (k Kind) String() string {
	if k == Retryable {
		return "retryable"
	}
	return "permanent"
}

// Error is a classified provider failure.
type Error struct {
	Kind       Kind
	Status     int           // HTTP status, 0 when no response was received
	Message    string        // safe to log: never document content
	RetryAfter time.Duration // from Retry-After, 0 when absent
	Err        error         // underlying cause, may be nil
}

func (e *Error) Error() string {
	msg := "provider: " + e.Kind.String()
	if e.Status != 0 {
		msg += fmt.Sprintf(" (status %d)", e.Status)
	}
	if e.Message != "" {
		msg += ": " + e.Message
	}
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	return msg
}

func (e *Error) Unwrap() error { return e.Err }

// Classify returns the Kind of err: the Kind of an *Error in its chain, Retryable for
// deadlines, timeouts, connection failures and truncated responses, Permanent for
// cancellation and anything else (TLS failures, bad URLs, oversized responses).
func Classify(err error) Kind {
	var pe *Error
	if errors.As(err, &pe) {
		return pe.Kind
	}
	// *url.Error, returned by http.Client, is always a net.Error; classify its cause.
	var ue *url.Error
	if errors.As(err, &ue) && !ue.Timeout() {
		err = ue.Err
	}
	var netErr net.Error
	switch {
	case errors.Is(err, context.Canceled):
		return Permanent // the caller gave up; trying elsewhere does not help
	case errors.Is(err, context.DeadlineExceeded),
		errors.As(err, &netErr),
		errors.Is(err, io.EOF),
		errors.Is(err, io.ErrUnexpectedEOF),
		errors.Is(err, syscall.ECONNRESET),
		errors.Is(err, syscall.ECONNREFUSED):
		return Retryable
	default:
		return Permanent
	}
}
