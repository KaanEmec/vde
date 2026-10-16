package anthropic

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/provider"
)

const testKey = "sk-ant-test-key"

// Distinctive content the tests look for in error messages.
const (
	secretText = "SECRET-INSTRUCTIONS-4711"
	secretDoc  = "%PDF-1.7 SECRET-DOCUMENT-BYTES"
	secretAns  = "SECRET-ANSWER-0815"
)

// endpoint builds a config.Endpoint the way the service does, through config.Parse.
func endpoint(t *testing.T, baseURL string, timeout time.Duration) *config.Endpoint {
	t.Helper()
	yml := fmt.Sprintf(`
endpoints:
  main:
    provider: anthropic
    base_url: %q
    api_key_env: TEST_KEY
    timeout: %s
    models:
      claude-test:
        accepts: [application/pdf]
`, baseURL, timeout)
	cfg, err := config.Parse([]byte(yml), func(string) string { return testKey })
	if err != nil {
		t.Fatal(err)
	}
	return cfg.Endpoints["main"]
}

// captured is what the test server saw.
type captured struct {
	method string
	path   string
	header http.Header
	body   []byte
}

// serve starts a server answering every request with handler and records the last one.
func serve(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *captured) {
	t.Helper()
	got := &captured{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading request body: %v", err)
		}
		got.method, got.path, got.header, got.body = r.Method, r.URL.Path, r.Header.Clone(), body
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

func reply(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}
}

func successBody(text string) string {
	return fmt.Sprintf(`{"id":"msg_1","type":"message","role":"assistant","model":"claude-test-20260101",
		"content":[{"type":"text","text":%q}],"stop_reason":"end_turn",
		"usage":{"input_tokens":120,"output_tokens":7}}`, text)
}

func request() provider.Request {
	return provider.Request{
		Model:  "claude-test",
		System: "You are an extractor. " + secretText,
		Parts: []provider.Part{
			{Text: "Instructions: " + secretText},
			{Image: &provider.Blob{MediaType: "image/png", Data: []byte("png-bytes")}},
			{Document: &provider.Blob{MediaType: "application/pdf", Data: []byte(secretDoc)}},
			{Text: "Candidate: {}"},
		},
		MaxTokens:   1000,
		Temperature: 0.1,
	}
}

func complete(t *testing.T, srv *httptest.Server, req provider.Request) (provider.Response, error) {
	t.Helper()
	return New(endpoint(t, srv.URL, 5*time.Second)).Complete(t.Context(), req)
}

// wantProviderError asserts err is a *provider.Error of the kind and status, and that its
// text leaks none of the content.
func wantProviderError(t *testing.T, err error, kind provider.Kind, status int) {
	t.Helper()
	pe := asProviderError(t, err)
	if pe.Kind != kind || pe.Status != status {
		t.Errorf("error = %v, want kind %s status %d", err, kind, status)
	}
	for _, secret := range []string{secretText, secretDoc, secretAns, base64.StdEncoding.EncodeToString([]byte(secretDoc)), testKey} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("error message %q contains %q", err, secret)
		}
	}
}

func asProviderError(t *testing.T, err error) *provider.Error {
	t.Helper()
	var pe *provider.Error
	if !errors.As(err, &pe) {
		t.Fatalf("error = %v, want a *provider.Error", err)
	}
	return pe
}

func TestNewDefaultBaseURL(t *testing.T) {
	t.Parallel()
	c := New(&config.Endpoint{Timeout: time.Second})
	if c.baseURL != DefaultBaseURL {
		t.Errorf("baseURL = %q, want %q", c.baseURL, DefaultBaseURL)
	}
	c = New(&config.Endpoint{BaseURL: "http://gateway.local/", Timeout: time.Second})
	if c.baseURL != "http://gateway.local" {
		t.Errorf("baseURL = %q, want the trailing slash trimmed", c.baseURL)
	}
}

func TestCompleteRequestShape(t *testing.T) {
	t.Parallel()
	srv, got := serve(t, reply(200, successBody("ok")))
	if _, err := complete(t, srv, request()); err != nil {
		t.Fatal(err)
	}

	if got.method != http.MethodPost || got.path != "/v1/messages" {
		t.Errorf("request = %s %s, want POST /v1/messages", got.method, got.path)
	}
	wantHeaders := map[string]string{
		"X-Api-Key":         testKey,
		"Anthropic-Version": "2023-06-01",
		"Content-Type":      "application/json",
		"User-Agent":        provider.UserAgent(),
	}
	for k, want := range wantHeaders {
		if v := got.header.Get(k); v != want {
			t.Errorf("header %s = %q, want %q", k, v, want)
		}
	}
	if v := got.header.Get("Anthropic-Beta"); v != "" {
		t.Errorf("anthropic-beta = %q, want none", v)
	}

	var body struct {
		Model       string   `json:"model"`
		MaxTokens   int      `json:"max_tokens"`
		System      string   `json:"system"`
		Temperature *float64 `json:"temperature"`
		Messages    []struct {
			Role    string `json:"role"`
			Content []struct {
				Type   string `json:"type"`
				Text   string `json:"text"`
				Source *struct {
					Type      string `json:"type"`
					MediaType string `json:"media_type"`
					Data      string `json:"data"`
				} `json:"source"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(got.body, &body); err != nil {
		t.Fatalf("request body is not JSON: %v", err)
	}
	if body.Model != "claude-test" || body.MaxTokens != 1000 || body.System != "You are an extractor. "+secretText {
		t.Errorf("model/max_tokens/system = %q/%d/%q", body.Model, body.MaxTokens, body.System)
	}
	if body.Temperature == nil || *body.Temperature != 0.1 {
		t.Errorf("temperature = %v, want 0.1", body.Temperature)
	}
	if len(body.Messages) != 1 || body.Messages[0].Role != "user" {
		t.Fatalf("messages = %+v, want one user message", body.Messages)
	}
	content := body.Messages[0].Content
	if len(content) != 4 {
		t.Fatalf("content has %d blocks, want 4 in part order", len(content))
	}
	if content[0].Type != "text" || content[0].Text != "Instructions: "+secretText {
		t.Errorf("block 0 = %+v, want the instructions text", content[0])
	}
	for i, want := range []struct{ typ, media, data string }{
		{"image", "image/png", "png-bytes"},
		{"document", "application/pdf", secretDoc},
	} {
		b := content[i+1]
		if b.Type != want.typ || b.Source == nil || b.Source.Type != "base64" || b.Source.MediaType != want.media {
			t.Errorf("block %d = %+v, want %s with a base64 %s source", i+1, b, want.typ, want.media)
			continue
		}
		if raw, err := base64.StdEncoding.DecodeString(b.Source.Data); err != nil || string(raw) != want.data {
			t.Errorf("block %d data = %q (err %v), want the base64 of %q", i+1, b.Source.Data, err, want.data)
		}
	}
	if content[3].Type != "text" || content[3].Text != "Candidate: {}" {
		t.Errorf("block 3 = %+v, want the candidate text", content[3])
	}

	// Nothing beyond the modelled fields: no tools, streaming, metadata or caching.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(got.body, &fields); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"tools", "stream", "metadata", "cache_control", "thinking", "service_tier"} {
		if _, ok := fields[f]; ok {
			t.Errorf("request body has unexpected field %q", f)
		}
	}
	if strings.Contains(string(got.body), "cache_control") {
		t.Error("request body mentions cache_control")
	}
}

func TestCompleteRequestDefaults(t *testing.T) {
	t.Parallel()
	srv, got := serve(t, reply(200, successBody("ok")))
	req := provider.Request{Model: "claude-test", Parts: []provider.Part{{Text: "hi"}}}
	if _, err := complete(t, srv, req); err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(got.body, &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["max_tokens"]) != "4096" {
		t.Errorf("max_tokens = %s, want the default 4096", fields["max_tokens"])
	}
	for _, f := range []string{"system", "temperature"} {
		if _, ok := fields[f]; ok {
			t.Errorf("field %q sent although unset", f)
		}
	}
}

func TestCompleteSuccess(t *testing.T) {
	t.Parallel()
	body := `{"model":"claude-test-20260101","stop_reason":"end_turn",
		"content":[{"type":"text","text":"{\"a\":"},{"type":"thinking","thinking":"x"},{"type":"text","text":"1}"}],
		"usage":{"input_tokens":120,"output_tokens":7}}`
	srv, _ := serve(t, reply(200, body))
	got, err := complete(t, srv, request())
	if err != nil {
		t.Fatal(err)
	}
	want := provider.Response{Text: `{"a":1}`, InputTokens: 120, OutputTokens: 7, Model: "claude-test-20260101"}
	if got != want {
		t.Errorf("response = %+v, want %+v", got, want)
	}
}

func TestCompleteStatusErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		status  int
		body    string
		header  string
		want    provider.Kind
		wantMsg string
		wantRA  time.Duration
	}{
		{name: "rate limited with retry-after", status: 429, header: "7", want: provider.Retryable,
			body: `{"type":"error","error":{"type":"rate_limit_error","message":"` + secretText + `"}}`, wantMsg: "rate_limit_error", wantRA: 7 * time.Second},
		{name: "invalid request", status: 400, want: provider.Permanent,
			body: `{"type":"error","error":{"type":"invalid_request_error","message":"` + secretDoc + `"}}`, wantMsg: "invalid_request_error"},
		{name: "unauthorized", status: 401, want: provider.Permanent,
			body: `{"type":"error","error":{"type":"authentication_error","message":"bad key"}}`, wantMsg: "authentication_error"},
		{name: "forbidden", status: 403, want: provider.Permanent, body: `{"error":{"type":"permission_error"}}`, wantMsg: "permission_error"},
		{name: "too large", status: 413, want: provider.Permanent, body: `{"error":{"type":"request_too_large"}}`, wantMsg: "request_too_large"},
		{name: "server error", status: 500, want: provider.Retryable, body: `{"error":{"type":"api_error"}}`, wantMsg: "api_error"},
		{name: "overloaded", status: 529, header: "2", want: provider.Retryable,
			body: `{"error":{"type":"overloaded_error"}}`, wantMsg: "overloaded_error", wantRA: 2 * time.Second},
		{name: "unknown error type is not echoed", status: 400, want: provider.Permanent,
			body: `{"error":{"type":"` + secretText + `"}}`, wantMsg: "error response"},
		{name: "html error body", status: 502, want: provider.Retryable, body: `<html>` + secretText + `</html>`, wantMsg: "error response"},
		{name: "empty error body", status: 503, want: provider.Retryable, wantMsg: "error response"},
		{name: "redirect is not followed", status: 302, want: provider.Permanent, wantMsg: "error response"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv, _ := serve(t, func(w http.ResponseWriter, _ *http.Request) {
				if tt.header != "" {
					w.Header().Set("Retry-After", tt.header)
				}
				w.WriteHeader(tt.status)
				fmt.Fprint(w, tt.body)
			})
			_, err := complete(t, srv, request())
			wantProviderError(t, err, tt.want, tt.status)
			pe := asProviderError(t, err)
			if !strings.Contains(pe.Message, tt.wantMsg) {
				t.Errorf("message = %q, want it to contain %q", pe.Message, tt.wantMsg)
			}
			if pe.RetryAfter != tt.wantRA {
				t.Errorf("RetryAfter = %s, want %s", pe.RetryAfter, tt.wantRA)
			}
		})
	}
}

func TestCompleteRefusal(t *testing.T) {
	t.Parallel()
	body := `{"model":"m","stop_reason":"refusal","stop_details":{"type":"refusal","category":"cyber","explanation":"` + secretText + `"},
		"content":[{"type":"text","text":"` + secretAns + `"}],"usage":{"input_tokens":1,"output_tokens":1}}`
	srv, _ := serve(t, reply(200, body))
	_, err := complete(t, srv, request())
	wantProviderError(t, err, provider.Permanent, 0)
	if pe := asProviderError(t, err); !strings.Contains(pe.Message, "refusal") {
		t.Errorf("message = %q, want it to say refusal", pe.Message)
	}
}

func TestCompleteEmptyAnswer(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, content string }{
		{name: "no blocks", content: `[]`},
		{name: "blank text", content: `[{"type":"text","text":"  \n"}]`},
		{name: "only thinking", content: `[{"type":"thinking","thinking":"` + secretAns + `"}]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv, _ := serve(t, reply(200, `{"model":"m","stop_reason":"end_turn","content":`+tt.content+`,"usage":{}}`))
			_, err := complete(t, srv, request())
			wantProviderError(t, err, provider.Permanent, 0)
		})
	}
}

func TestCompleteMalformedBody(t *testing.T) {
	t.Parallel()
	srv, _ := serve(t, reply(200, `{"content": `+secretAns+`}`))
	_, err := complete(t, srv, request())
	wantProviderError(t, err, provider.Permanent, 0)
}

func TestCompleteTimeouts(t *testing.T) {
	t.Parallel()
	hang := func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }

	t.Run("context deadline", func(t *testing.T) {
		t.Parallel()
		srv, _ := serve(t, hang)
		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()
		_, err := New(endpoint(t, srv.URL, 5*time.Second)).Complete(ctx, request())
		wantProviderError(t, err, provider.Retryable, 0)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("error = %v, want it to wrap context.DeadlineExceeded", err)
		}
	})
	t.Run("endpoint timeout", func(t *testing.T) {
		t.Parallel()
		srv, _ := serve(t, hang)
		_, err := New(endpoint(t, srv.URL, 50*time.Millisecond)).Complete(t.Context(), request())
		wantProviderError(t, err, provider.Retryable, 0)
	})
	t.Run("cancellation is permanent", func(t *testing.T) {
		t.Parallel()
		srv, _ := serve(t, hang)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := New(endpoint(t, srv.URL, 5*time.Second)).Complete(ctx, request())
		wantProviderError(t, err, provider.Permanent, 0)
	})
	t.Run("connection refused", func(t *testing.T) {
		t.Parallel()
		srv, _ := serve(t, hang)
		url := srv.URL
		srv.Close()
		_, err := New(endpoint(t, url, 5*time.Second)).Complete(t.Context(), request())
		wantProviderError(t, err, provider.Retryable, 0)
	})
}

func TestCompleteOversizedResponse(t *testing.T) {
	t.Parallel()
	srv, _ := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"model":"m","stop_reason":"end_turn","content":[{"type":"text","text":"`)
		chunk := strings.Repeat("x", 1<<20)
		for range provider.DefaultMaxResponseBytes/(1<<20) + 1 {
			fmt.Fprint(w, chunk)
		}
		fmt.Fprint(w, `"}],"usage":{}}`)
	})
	_, err := complete(t, srv, request())
	wantProviderError(t, err, provider.Permanent, 0)
	if !errors.Is(err, provider.ErrResponseTooLarge) {
		t.Errorf("error = %v, want it to wrap ErrResponseTooLarge", err)
	}
}

func TestCompleteInvalidRequests(t *testing.T) {
	t.Parallel()
	srv, got := serve(t, reply(200, successBody("ok")))
	blob := &provider.Blob{MediaType: "application/pdf", Data: []byte(secretDoc)}
	tests := []struct {
		name string
		req  provider.Request
	}{
		{name: "no model", req: provider.Request{Parts: []provider.Part{{Text: "x"}}}},
		{name: "no parts", req: provider.Request{Model: "m"}},
		{name: "empty part", req: provider.Request{Model: "m", Parts: []provider.Part{{}}}},
		{name: "two fields", req: provider.Request{Model: "m", Parts: []provider.Part{{Text: secretText, Document: blob}}}},
		{name: "non-pdf document", req: provider.Request{Model: "m", Parts: []provider.Part{{Document: &provider.Blob{MediaType: "text/plain", Data: []byte(secretDoc)}}}}},
		{name: "empty document", req: provider.Request{Model: "m", Parts: []provider.Part{{Document: &provider.Blob{MediaType: "application/pdf"}}}}},
		{name: "image without media type", req: provider.Request{Model: "m", Parts: []provider.Part{{Image: &provider.Blob{Data: []byte(secretDoc)}}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := complete(t, srv, tt.req)
			wantProviderError(t, err, provider.Permanent, 0)
		})
	}
	if got.method != "" {
		t.Errorf("invalid requests reached the server: %s", got.method)
	}
}
