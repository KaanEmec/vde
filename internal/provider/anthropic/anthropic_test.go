package anthropic

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/provider"
	"github.com/kaanemec/vde/internal/provider/providertest"
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
	return providertest.Endpoint(t, testKey, "provider: anthropic", "base_url: "+strconv.Quote(baseURL),
		"api_key_env: TEST_KEY", "timeout: "+timeout.String(), "models:", "  claude-test:", "    accepts: [application/pdf]")
}

// reply answers 200 with the JSON body.
func reply(body string) http.HandlerFunc {
	return providertest.Reply(http.StatusOK, http.Header{"Content-Type": {"application/json"}}, body)
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
		MaxTokens: 1000,
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
	pe := providertest.AsError(t, err)
	if pe.Kind != kind || pe.Status != status {
		t.Errorf("error = %v, want kind %s status %d", err, kind, status)
	}
	providertest.AssertNoContent(t, err, secretText, secretDoc, secretAns, base64.StdEncoding.EncodeToString([]byte(secretDoc)), testKey)
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
	srv, got := providertest.Serve(t, reply(successBody("ok")))
	if _, err := complete(t, srv, request()); err != nil {
		t.Fatal(err)
	}

	if got.Method != http.MethodPost || got.URL.Path != "/v1/messages" {
		t.Errorf("request = %s %s, want POST /v1/messages", got.Method, got.URL.Path)
	}
	wantHeaders := map[string]string{
		"X-Api-Key":         testKey,
		"Anthropic-Version": "2023-06-01",
		"Content-Type":      "application/json",
		"User-Agent":        provider.UserAgent(),
	}
	for k, want := range wantHeaders {
		if v := got.Header.Get(k); v != want {
			t.Errorf("header %s = %q, want %q", k, v, want)
		}
	}
	if v := got.Header.Get("Anthropic-Beta"); v != "" {
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
	if err := json.Unmarshal(got.Body, &body); err != nil {
		t.Fatalf("request body is not JSON: %v", err)
	}
	if body.Model != "claude-test" || body.MaxTokens != 1000 || body.System != "You are an extractor. "+secretText {
		t.Errorf("model/max_tokens/system = %q/%d/%q", body.Model, body.MaxTokens, body.System)
	}
	if body.Temperature != nil {
		t.Errorf("temperature = %v, want none", *body.Temperature)
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
	if err := json.Unmarshal(got.Body, &fields); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"tools", "stream", "metadata", "cache_control", "thinking", "service_tier"} {
		if _, ok := fields[f]; ok {
			t.Errorf("request body has unexpected field %q", f)
		}
	}
	if strings.Contains(string(got.Body), "cache_control") {
		t.Error("request body mentions cache_control")
	}
}

func TestCompleteRequestDefaults(t *testing.T) {
	t.Parallel()
	srv, got := providertest.Serve(t, reply(successBody("ok")))
	req := provider.Request{Model: "claude-test", MaxTokens: 4096, Parts: []provider.Part{{Text: "hi"}}}
	if _, err := complete(t, srv, req); err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(got.Body, &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["max_tokens"]) != "4096" {
		t.Errorf("max_tokens = %s, want 4096", fields["max_tokens"])
	}
	for _, f := range []string{"system", "temperature"} {
		if _, ok := fields[f]; ok {
			t.Errorf("field %q sent although unset", f)
		}
	}
}

func TestBodyVertexForm(t *testing.T) {
	t.Parallel()
	body, err := Body(provider.Request{Model: "claude-test", MaxTokens: 10, Parts: []provider.Part{{Text: "hi"}}}, "vertex-1")
	if err != nil {
		t.Fatal(err)
	}
	want := `{"anthropic_version":"vertex-1","max_tokens":10,"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`
	if string(body) != want {
		t.Errorf("body = %s, want %s", body, want)
	}
}

func TestCompleteSuccess(t *testing.T) {
	t.Parallel()
	body := `{"model":"claude-test-20260101","stop_reason":"end_turn",
		"content":[{"type":"text","text":"{\"a\":"},{"type":"thinking","thinking":"x"},{"type":"text","text":"1}"}],
		"usage":{"input_tokens":120,"output_tokens":7}}`
	srv, _ := providertest.Serve(t, reply(body))
	got, err := complete(t, srv, request())
	if err != nil {
		t.Fatal(err)
	}
	want := provider.Response{Text: `{"a":1}`, InputTokens: 120, OutputTokens: 7}
	if got != want {
		t.Errorf("response = %+v, want %+v", got, want)
	}
}

func TestCompleteTruncated(t *testing.T) {
	t.Parallel()
	for reason, want := range map[string]bool{"max_tokens": true, "end_turn": false} {
		body := `{"model":"m","stop_reason":"` + reason + `","content":[{"type":"text","text":"{\"a\":"}],"usage":{"input_tokens":1,"output_tokens":4096}}`
		srv, _ := providertest.Serve(t, reply(body))
		got, err := complete(t, srv, request())
		if err != nil {
			t.Fatalf("stop_reason %q: %v", reason, err)
		}
		if got.Text != `{"a":` || got.Truncated != want {
			t.Errorf("stop_reason %q: text %q truncated %v, want truncated %v", reason, got.Text, got.Truncated, want)
		}
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
			srv, _ := providertest.Serve(t, func(w http.ResponseWriter, _ *http.Request) {
				if tt.header != "" {
					w.Header().Set("Retry-After", tt.header)
				}
				w.WriteHeader(tt.status)
				fmt.Fprint(w, tt.body)
			})
			_, err := complete(t, srv, request())
			wantProviderError(t, err, tt.want, tt.status)
			pe := providertest.AsError(t, err)
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
	srv, _ := providertest.Serve(t, reply(body))
	_, err := complete(t, srv, request())
	wantProviderError(t, err, provider.Permanent, 0)
	if pe := providertest.AsError(t, err); !strings.Contains(pe.Message, "refusal") {
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
			srv, _ := providertest.Serve(t, reply(`{"model":"m","stop_reason":"end_turn","content":`+tt.content+`,"usage":{}}`))
			_, err := complete(t, srv, request())
			wantProviderError(t, err, provider.Permanent, 0)
		})
	}
}

func TestCompleteMalformedBody(t *testing.T) {
	t.Parallel()
	srv, _ := providertest.Serve(t, reply(`{"content": `+secretAns+`}`))
	_, err := complete(t, srv, request())
	wantProviderError(t, err, provider.Permanent, 0)
}

// TestCompleteCutOffBody: a 2xx body that ends early is retryable, so the router fails
// over; a syntax error (TestCompleteMalformedBody) stays permanent.
func TestCompleteCutOffBody(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]string{"empty": "", "cut off": `{"model":"m","content":[{"type":"text","text":"` + secretAns} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv, _ := providertest.Serve(t, reply(body))
			_, err := complete(t, srv, request())
			wantProviderError(t, err, provider.Retryable, 0)
		})
	}
}

// TestNamedErrors: Body in the Vertex form and Parse name the calling adapter, and Parse
// treats every unreadable body as permanent.
func TestNamedErrors(t *testing.T) {
	t.Parallel()
	_, err := Body(provider.Request{Model: "claude-test", Parts: []provider.Part{{}}}, "vertex-1")
	if pe := providertest.AsError(t, err); !strings.HasPrefix(pe.Message, "vertex: ") {
		t.Errorf("Body message = %q, want the vertex prefix", pe.Message)
	}
	for _, raw := range []string{"", `{"content":[{"type":"text","text":"` + secretAns, `{"stop_reason":"refusal"}`} {
		_, err := Parse([]byte(raw), "vertex")
		wantProviderError(t, err, provider.Permanent, 0)
		if pe := providertest.AsError(t, err); !strings.HasPrefix(pe.Message, "vertex: ") {
			t.Errorf("Parse message = %q, want the vertex prefix", pe.Message)
		}
	}
	if resp, err := Parse([]byte(successBody("ok")), "vertex"); err != nil || resp.Text != "ok" {
		t.Errorf("Parse = %+v, %v", resp, err)
	}
}

func TestCompleteTimeouts(t *testing.T) {
	t.Parallel()
	hang := func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }

	t.Run("context deadline", func(t *testing.T) {
		t.Parallel()
		srv, _ := providertest.Serve(t, hang)
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
		srv, _ := providertest.Serve(t, hang)
		_, err := New(endpoint(t, srv.URL, 50*time.Millisecond)).Complete(t.Context(), request())
		wantProviderError(t, err, provider.Retryable, 0)
	})
	t.Run("cancellation is permanent", func(t *testing.T) {
		t.Parallel()
		srv, _ := providertest.Serve(t, hang)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := New(endpoint(t, srv.URL, 5*time.Second)).Complete(ctx, request())
		wantProviderError(t, err, provider.Permanent, 0)
	})
	t.Run("connection refused", func(t *testing.T) {
		t.Parallel()
		srv, _ := providertest.Serve(t, hang)
		url := srv.URL
		srv.Close()
		_, err := New(endpoint(t, url, 5*time.Second)).Complete(t.Context(), request())
		wantProviderError(t, err, provider.Retryable, 0)
	})
}

func TestCompleteOversizedResponse(t *testing.T) {
	t.Parallel()
	srv, _ := providertest.Serve(t, func(w http.ResponseWriter, _ *http.Request) {
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
	srv, got := providertest.Serve(t, reply(successBody("ok")))
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
	if got.Method != "" {
		t.Errorf("invalid requests reached the server: %s", got.Method)
	}
}
