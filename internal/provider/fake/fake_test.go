package fake_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/kaanemec/vde/internal/provider"
	"github.com/kaanemec/vde/internal/provider/fake"
)

func TestProvider_twoStepScriptRecordsRequests(t *testing.T) {
	t.Parallel()
	pdf := []byte("%PDF-1.7 fake bytes")
	p := fake.New(fake.Script{fake.Answer(`{"a": 1}`), fake.Answer(fake.AcceptVerdict)})
	parts := []provider.Part{
		{Text: "instructions"},
		{Document: &provider.Blob{MediaType: "application/pdf", Data: pdf}},
		{Image: &provider.Blob{MediaType: "image/png", Data: []byte{1, 2, 3}}},
	}
	ctx := context.Background()

	resp, err := p.Complete(ctx, provider.Request{Model: "m1", System: "extractor framing", Parts: parts})
	if err != nil || resp.Text != `{"a": 1}` || resp.Model != "m1" || resp.InputTokens != fake.InputTokens {
		t.Fatalf("first Complete() = %+v, %v", resp, err)
	}
	resp, err = p.Complete(ctx, provider.Request{Model: "m2", System: "judge framing", Parts: parts})
	if err != nil || resp.Text != fake.AcceptVerdict {
		t.Fatalf("second Complete() = %+v, %v", resp, err)
	}
	pdf[0] = 'X' // the recording must not alias the caller's buffers

	calls := p.Calls()
	if len(calls) != 2 {
		t.Fatalf("len(Calls()) = %d, want 2", len(calls))
	}
	wantParts := []fake.PartInfo{
		{Kind: "text", MediaType: "text/plain", Size: len("instructions")},
		{Kind: "document", MediaType: "application/pdf", Size: len(pdf)},
		{Kind: "image", MediaType: "image/png", Size: 3},
	}
	for i, want := range []struct{ model, system string }{{"m1", "extractor framing"}, {"m2", "judge framing"}} {
		c := calls[i]
		if c.Request.Model != want.model || c.Request.System != want.system {
			t.Errorf("call %d: model %q system %q, want %q %q", i, c.Request.Model, c.Request.System, want.model, want.system)
		}
		if !reflect.DeepEqual(c.Parts, wantParts) {
			t.Errorf("call %d: parts %+v, want %+v", i, c.Parts, wantParts)
		}
	}
	extractorDoc, judgeDoc := calls[0].Request.Parts[1].Document.Data, calls[1].Request.Parts[1].Document.Data
	if !bytes.Equal(extractorDoc, judgeDoc) || extractorDoc[0] != '%' {
		t.Errorf("recorded documents differ or alias the caller's buffer")
	}
	if n := p.Remaining(); n != 0 {
		t.Errorf("Remaining() = %d, want 0", n)
	}
}

func TestProvider_Complete(t *testing.T) {
	t.Parallel()
	retryAfter := fake.Fail(provider.Retryable, 429)
	retryAfter.Err.RetryAfter = 2 * time.Second
	tests := []struct {
		name     string
		script   fake.Script
		requests []provider.Request
		want     []string // answer text, or "error <kind> <status>"
	}{
		{
			name:     "steps in order then exhausted",
			script:   fake.Script{fake.Answer("one"), fake.Answer("two")},
			requests: []provider.Request{{}, {}, {}},
			want:     []string{"one", "two", "error permanent 0"},
		},
		{
			name:     "error then answer",
			script:   fake.Script{fake.Fail(provider.Retryable, 503), fake.Answer("ok")},
			requests: []provider.Request{{}, {}},
			want:     []string{"error retryable 503", "ok"},
		},
		{
			name:     "matched by role",
			script:   fake.Script{{Role: "JUDGE", Answer: "verdict"}, {Role: "EXTRACT", Answer: "candidate"}},
			requests: []provider.Request{{System: "you EXTRACT"}, {System: "you JUDGE"}},
			want:     []string{"candidate", "verdict"},
		},
		{
			name: "matched by model, repeated",
			script: fake.Script{
				{Model: "primary", Repeat: true, Err: &provider.Error{Kind: provider.Retryable, Status: 502}},
				fake.Answer("fallback"),
			},
			requests: []provider.Request{{Model: "primary"}, {Model: "secondary"}, {Model: "primary"}},
			want:     []string{"error retryable 502", "fallback", "error retryable 502"},
		},
		{
			name:     "always permanent",
			script:   fake.AlwaysPermanent(400),
			requests: []provider.Request{{}, {}},
			want:     []string{"error permanent 400", "error permanent 400"},
		},
		{
			name:     "retry after carried",
			script:   fake.Script{retryAfter},
			requests: []provider.Request{{}},
			want:     []string{"error retryable 429 2s"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := fake.New(tt.script)
			for i, req := range tt.requests {
				resp, err := p.Complete(context.Background(), req)
				if got := outcome(resp, err); got != tt.want[i] {
					t.Errorf("request %d = %q, want %q", i, got, tt.want[i])
				}
			}
		})
	}
}

func outcome(resp provider.Response, err error) string {
	if err == nil {
		return resp.Text
	}
	var pe *provider.Error
	if !errors.As(err, &pe) {
		return "error " + err.Error()
	}
	s := fmt.Sprintf("error %s %d", pe.Kind, pe.Status)
	if pe.RetryAfter > 0 {
		s += " " + pe.RetryAfter.String()
	}
	return s
}

func TestProvider_exhaustedWrapsSentinel(t *testing.T) {
	t.Parallel()
	_, err := fake.New(nil).Complete(context.Background(), provider.Request{})
	if !errors.Is(err, fake.ErrExhausted) || provider.Classify(err) != provider.Permanent {
		t.Errorf("Complete() error = %v, want permanent ErrExhausted", err)
	}
}

func TestProvider_delayHonoursContext(t *testing.T) {
	t.Parallel()
	p := fake.New(fake.Script{{Delay: time.Hour, Answer: "late"}})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := p.Complete(ctx, provider.Request{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Complete() error = %v, want deadline exceeded", err)
	}
	if time.Since(start) > time.Second {
		t.Errorf("Complete() waited %s past the deadline", time.Since(start))
	}
}

func TestProvider_doneContextIsRecordedButNotServed(t *testing.T) {
	t.Parallel()
	p := fake.New(fake.Script{fake.Answer("x")})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Complete(ctx, provider.Request{}); !errors.Is(err, context.Canceled) {
		t.Errorf("Complete() error = %v, want canceled", err)
	}
	if len(p.Calls()) != 1 || p.Remaining() != 1 {
		t.Errorf("Calls() = %d, Remaining() = %d, want 1 and 1", len(p.Calls()), p.Remaining())
	}
}

func TestProvider_concurrentCallsConsumeEachStepOnce(t *testing.T) {
	t.Parallel()
	const n = 50
	script := make(fake.Script, n)
	for i := range script {
		script[i] = fake.Answer("a")
	}
	p := fake.New(script)
	var wg sync.WaitGroup
	for range n + 10 {
		wg.Go(func() { _, _ = p.Complete(context.Background(), provider.Request{}) })
	}
	wg.Wait()
	if len(p.Calls()) != n+10 || p.Remaining() != 0 {
		t.Errorf("Calls() = %d, Remaining() = %d, want %d and 0", len(p.Calls()), p.Remaining(), n+10)
	}
}
