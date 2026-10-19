package openai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/provider"
)

const (
	secretKey = "sk-test-secret-key"
	docText   = "CONFIDENTIAL-DOCUMENT-TEXT"
	answer    = "SECRET-ANSWER-TEXT"
)

var docBytes = []byte("%PDF-1.7 CONFIDENTIAL-PDF-BYTES")

// endpoint builds an endpoint through config.Parse, the only way to set a resolved key.
func endpoint(t *testing.T, baseURL, preset, key string) *config.Endpoint {
	t.Helper()
	yml := "endpoints:\n  e:\n    provider: openai_compatible\n    base_url: " + baseURL + "\n    timeout: 5s\n"
	if preset != "" {
		yml += "    preset: " + preset + "\n"
	}
	if key != "" {
		yml += "    api_key_env: TEST_KEY\n"
	}
	cfg, err := config.Parse([]byte(yml), func(string) string { return key })
	if err != nil {
		t.Fatalf("config.Parse: %v", err)
	}
	return cfg.Endpoints["e"]
}

// capture is a server that records the last request and replies with status and body.
type capture struct {
	header http.Header
	path   string
	body   map[string]any
}

func serve(t *testing.T, status int, reply string, hdr http.Header) (*httptest.Server, *capture) {
	t.Helper()
	c := &capture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.header = r.Header.Clone()
		c.path = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &c.body)
		for k, v := range hdr {
			w.Header()[k] = v
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(srv.Close)
	return srv, c
}

const okReply = `{"model":"gpt-x-2026","choices":[{"message":{"role":"assistant","content":"` + answer + `"},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":7}}`

func request() provider.Request {
	return provider.Request{
		Model:     "gpt-x",
		System:    "you are a reader",
		MaxTokens: 1000,
		Parts: []provider.Part{
			{Text: docText},
			{Image: &provider.Blob{MediaType: "image/png", Data: []byte("PNGDATA")}},
			{Document: &provider.Blob{MediaType: "application/pdf", Data: docBytes}},
		},
	}
}

func TestRequestShape(t *testing.T) {
	t.Parallel()
	srv, got := serve(t, 200, okReply, nil)
	c := New(endpoint(t, srv.URL+"/v1/", "", secretKey))
	if _, err := c.Complete(t.Context(), request()); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got.path != "/v1/chat/completions" {
		t.Errorf("path = %q", got.path)
	}
	if h := got.header.Get("Authorization"); h != "Bearer "+secretKey {
		t.Errorf("Authorization = %q", h)
	}
	if h := got.header.Get("Content-Type"); h != "application/json" {
		t.Errorf("Content-Type = %q", h)
	}
	if h := got.header.Get("User-Agent"); !strings.HasPrefix(h, "vde/") {
		t.Errorf("User-Agent = %q", h)
	}
	if got.header.Get("HTTP-Referer") != "" || got.header.Get("X-Title") != "" {
		t.Error("attribution headers sent without the openrouter preset")
	}
	b := got.body
	if b["model"] != "gpt-x" || b["max_tokens"] != float64(1000) || b["temperature"] != float64(0) {
		t.Errorf("scalar fields = %v", b)
	}
	if _, ok := b["provider"]; ok {
		t.Error("provider routing sent without the openrouter preset")
	}
	msgs := b["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want 2", len(msgs))
	}
	sys := msgs[0].(map[string]any)
	if sys["role"] != "system" || sys["content"] != "you are a reader" {
		t.Errorf("system message = %v", sys)
	}
	user := msgs[1].(map[string]any)
	if user["role"] != "user" {
		t.Errorf("user role = %v", user["role"])
	}
	parts := user["content"].([]any)
	if len(parts) != 3 {
		t.Fatalf("content parts = %d, want 3", len(parts))
	}
	text := parts[0].(map[string]any)
	if text["type"] != "text" || text["text"] != docText {
		t.Errorf("text part = %v", text)
	}
	img := parts[1].(map[string]any)
	wantImg := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("PNGDATA"))
	if img["type"] != "image_url" || img["image_url"].(map[string]any)["url"] != wantImg {
		t.Errorf("image part = %v", img)
	}
	file := parts[2].(map[string]any)
	f := file["file"].(map[string]any)
	wantPDF := "data:application/pdf;base64," + base64.StdEncoding.EncodeToString(docBytes)
	if file["type"] != "file" || f["filename"] != "document.pdf" || f["file_data"] != wantPDF {
		t.Errorf("file part = %v", file)
	}
}

func TestDefaultsAndNoSystem(t *testing.T) {
	t.Parallel()
	srv, got := serve(t, 200, okReply, nil)
	c := New(endpoint(t, srv.URL, "", ""))
	req := provider.Request{Model: "m", MaxTokens: 4096, Parts: []provider.Part{{Text: "hi"}}}
	if _, err := c.Complete(t.Context(), req); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got.body["max_tokens"] != float64(4096) || got.body["temperature"] != float64(0) {
		t.Errorf("max_tokens, temperature = %v, %v, want 4096, 0", got.body["max_tokens"], got.body["temperature"])
	}
	if n := len(got.body["messages"].([]any)); n != 1 {
		t.Errorf("messages = %d, want only the user message", n)
	}
	if _, ok := got.header["Authorization"]; ok {
		t.Errorf("Authorization sent without a key: %q", got.header.Get("Authorization"))
	}
}

func TestOpenRouterPreset(t *testing.T) {
	t.Parallel()
	srv, got := serve(t, 200, okReply, nil)
	c := New(endpoint(t, srv.URL, "openrouter", secretKey))
	if _, err := c.Complete(t.Context(), request()); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if h := got.header.Get("HTTP-Referer"); h != "https://github.com/kaanemec/vde" {
		t.Errorf("HTTP-Referer = %q", h)
	}
	if h := got.header.Get("X-Title"); h != "vde" {
		t.Errorf("X-Title = %q", h)
	}
	prov, ok := got.body["provider"].(map[string]any)
	if !ok || prov["data_collection"] != "deny" {
		t.Errorf("provider option = %v, want data_collection deny", got.body["provider"])
	}
}

func TestSuccessParsing(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		reply string
		text  string
	}{
		{
			name:  "string content",
			reply: okReply,
			text:  answer,
		},
		{
			name:  "array content",
			reply: `{"choices":[{"message":{"content":[{"type":"text","text":"ab"},{"type":"text","text":"cd"}]},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":7}}`,
			text:  "abcd",
		},
		{
			name:  "minimal Ollama-style reply without usage",
			reply: `{"model":"llava","choices":[{"index":0,"message":{"role":"assistant","content":"ok"}}]}`,
			text:  "ok",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv, _ := serve(t, 200, tt.reply, nil)
			resp, err := New(endpoint(t, srv.URL, "", "")).Complete(t.Context(), request())
			if err != nil {
				t.Fatalf("Complete: %v", err)
			}
			if resp.Text != tt.text {
				t.Errorf("got text %q, want %q", resp.Text, tt.text)
			}
			if strings.Contains(tt.reply, "usage") && (resp.InputTokens != 11 || resp.OutputTokens != 7) {
				t.Errorf("usage = %d/%d, want 11/7", resp.InputTokens, resp.OutputTokens)
			}
		})
	}
}

func TestTruncatedAnswer(t *testing.T) {
	t.Parallel()
	for reason, want := range map[string]bool{"length": true, "stop": false} {
		reply := `{"choices":[{"message":{"content":"{\"a\":"},"finish_reason":"` + reason + `"}]}`
		srv, _ := serve(t, 200, reply, nil)
		resp, err := New(endpoint(t, srv.URL, "", "")).Complete(t.Context(), request())
		if err != nil {
			t.Fatalf("finish_reason %q: %v", reason, err)
		}
		if resp.Text != `{"a":` || resp.Truncated != want {
			t.Errorf("finish_reason %q: text %q truncated %v, want truncated %v", reason, resp.Text, resp.Truncated, want)
		}
	}
}

func TestErrors(t *testing.T) {
	t.Parallel()
	leaky := `{"error":{"message":"` + docText + ` ` + answer + `","type":"invalid_request_error"}}`
	tests := []struct {
		name       string
		status     int
		reply      string
		hdr        http.Header
		want       provider.Kind
		retryAfter time.Duration
	}{
		{name: "429 with Retry-After", status: 429, reply: leaky, hdr: http.Header{"Retry-After": {"7"}}, want: provider.Retryable, retryAfter: 7 * time.Second},
		{name: "400", status: 400, reply: leaky, want: provider.Permanent},
		{name: "401", status: 401, reply: leaky, want: provider.Permanent},
		{name: "503", status: 503, reply: leaky, want: provider.Retryable},
		{name: "content filter", status: 200, want: provider.Permanent,
			reply: `{"choices":[{"message":{"content":"` + answer + `"},"finish_reason":"content_filter"}]}`},
		{name: "empty content", status: 200, want: provider.Permanent,
			reply: `{"choices":[{"message":{"content":""},"finish_reason":"stop"}]}`},
		{name: "null content with refusal", status: 200, want: provider.Permanent,
			reply: `{"choices":[{"message":{"content":null,"refusal":"` + answer + `"},"finish_reason":"stop"}]}`},
		{name: "no choices", status: 200, reply: `{"choices":[]}`, want: provider.Permanent},
		{name: "malformed body", status: 200, reply: `{"choices":` + answer, want: provider.Permanent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv, _ := serve(t, tt.status, tt.reply, tt.hdr)
			_, err := New(endpoint(t, srv.URL, "", secretKey)).Complete(t.Context(), request())
			var pe *provider.Error
			if !errors.As(err, &pe) {
				t.Fatalf("err = %v, want *provider.Error", err)
			}
			if pe.Kind != tt.want {
				t.Errorf("Kind = %v, want %v", pe.Kind, tt.want)
			}
			if tt.status >= 300 && pe.Status != tt.status {
				t.Errorf("Status = %d, want %d", pe.Status, tt.status)
			}
			if pe.RetryAfter != tt.retryAfter {
				t.Errorf("RetryAfter = %v, want %v", pe.RetryAfter, tt.retryAfter)
			}
			assertNoContent(t, err)
		})
	}
}

func TestContextDeadlineIsRetryable(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	_, err := New(endpoint(t, srv.URL, "", "")).Complete(ctx, request())
	var pe *provider.Error
	if !errors.As(err, &pe) || pe.Kind != provider.Retryable {
		t.Fatalf("err = %v, want a Retryable *provider.Error", err)
	}
	assertNoContent(t, err)
}

func TestConnectionRefusedIsRetryable(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	_, err := New(endpoint(t, url, "", secretKey)).Complete(t.Context(), request())
	var pe *provider.Error
	if !errors.As(err, &pe) || pe.Kind != provider.Retryable {
		t.Fatalf("err = %v, want a Retryable *provider.Error", err)
	}
	assertNoContent(t, err)
}

func TestUnsupportedDocumentIsPermanent(t *testing.T) {
	t.Parallel()
	srv, got := serve(t, 200, okReply, nil)
	req := provider.Request{Model: "m", Parts: []provider.Part{
		{Document: &provider.Blob{MediaType: "application/msword", Data: docBytes}},
	}}
	_, err := New(endpoint(t, srv.URL, "", "")).Complete(t.Context(), req)
	var pe *provider.Error
	if !errors.As(err, &pe) || pe.Kind != provider.Permanent {
		t.Fatalf("err = %v, want a Permanent *provider.Error", err)
	}
	if got.body != nil {
		t.Error("a request was sent for an unsupported document")
	}
	assertNoContent(t, err)
}

// assertNoContent fails when an error message carries document bytes, answer text or the key.
func assertNoContent(t *testing.T, err error) {
	t.Helper()
	msg := err.Error()
	for _, s := range []string{docText, answer, string(docBytes), "CONFIDENTIAL-PDF-BYTES", secretKey,
		base64.StdEncoding.EncodeToString(docBytes)} {
		if strings.Contains(msg, s) {
			t.Errorf("error message %q contains %q", msg, s)
		}
	}
}
