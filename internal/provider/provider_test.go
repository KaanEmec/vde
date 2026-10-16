package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"syscall"
	"testing"
)

func TestError(t *testing.T) {
	t.Parallel()
	cause := errors.New("connection reset")
	err := &Error{Kind: Retryable, Status: 503, Message: "overloaded_error", Err: cause}
	if got, want := err.Error(), "provider: retryable (status 503): overloaded_error: connection reset"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if !errors.Is(err, cause) {
		t.Error("errors.Is(err, cause) = false, want Unwrap to expose the cause")
	}
	if got := (&Error{}).Error(); got != "provider: permanent" {
		t.Errorf("zero Error() = %q", got)
	}
	var target *Error
	if !errors.As(fmt.Errorf("extractor: %w", err), &target) || target.Status != 503 {
		t.Error("errors.As through a wrap did not find the *Error")
	}
}

func TestClassify(t *testing.T) {
	t.Parallel()
	urlErr := func(err error) error { return &url.Error{Op: "Post", URL: "https://api.example.com/v1", Err: err} }
	refused := &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
	tests := []struct {
		name string
		err  error
		want Kind
	}{
		{name: "nil", err: nil, want: Permanent},
		{name: "retryable provider error", err: &Error{Kind: Retryable, Status: 429}, want: Retryable},
		{name: "permanent provider error", err: &Error{Kind: Permanent, Status: 400}, want: Permanent},
		{name: "wrapped provider error", err: fmt.Errorf("judge: %w", &Error{Kind: Retryable, Status: 502}), want: Retryable},
		{name: "provider error wins over its cause", err: &Error{Kind: Permanent, Err: context.DeadlineExceeded}, want: Permanent},
		{name: "deadline exceeded", err: context.DeadlineExceeded, want: Retryable},
		{name: "wrapped deadline", err: fmt.Errorf("call: %w", context.DeadlineExceeded), want: Retryable},
		{name: "client timeout", err: urlErr(context.DeadlineExceeded), want: Retryable},
		{name: "canceled", err: context.Canceled, want: Permanent},
		{name: "canceled request", err: urlErr(context.Canceled), want: Permanent},
		{name: "connection refused", err: urlErr(refused), want: Retryable},
		{name: "net op error", err: refused, want: Retryable},
		{name: "dns timeout", err: &net.DNSError{Err: "timeout", IsTimeout: true}, want: Retryable},
		{name: "connection reset", err: fmt.Errorf("read: %w", syscall.ECONNRESET), want: Retryable},
		{name: "server closed connection", err: urlErr(io.EOF), want: Retryable},
		{name: "truncated body", err: io.ErrUnexpectedEOF, want: Retryable},
		{name: "unsupported scheme", err: urlErr(errors.New("unsupported protocol scheme \"ftp\"")), want: Permanent},
		{name: "response too large", err: ErrResponseTooLarge, want: Permanent},
		{name: "unknown error", err: errors.New("boom"), want: Permanent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := Classify(tt.err); got != tt.want {
				t.Errorf("Classify(%v) = %s, want %s", tt.err, got, tt.want)
			}
		})
	}
}
