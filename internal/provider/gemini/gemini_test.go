package gemini

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
	testKey    = "test-key-123"
	secretText = "SECRET-DOCUMENT-TEXT"
	secretBlob = "SECRET-BLOB-BYTES"
	secretAns  = "SECRET-ANSWER-TEXT"
)

// captured is what the fake Gemini server saw.
type captured struct {
	path   string
	rawURL string
	key    string
	body   []byte
}

// serve starts a server that records the request and replies with status and body.
func serve(t *testing.T, status int, reply string, hdr http.Header) (*Client, *captured) {
	t.Helper()
	got := &captured{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.path = r.URL.Path
		got.rawURL = r.URL.String()
		got.key = r.Header.Get("x-goog-api-key")
		got.body, _ = io.ReadAll(r.Body)
		for k, v := range hdr {
			w.Header()[k] = v
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(srv.Close)
	return newClient(t, srv.URL, 5*time.Second), got
}

func newClient(t *testing.T, baseURL string, timeout time.Duration) *Client {
	t.Helper()
	var ep config.Endpoint
	ep.BaseURL = baseURL
	ep.Timeout = timeout
	c := New(&ep)
	c.apiKey = testKey
	return c
}

func baseRequest() provider.Request {
	return provider.Request{
		Model:       "gemini-test-model",
		System:      "be careful",
		Temperature: 0.1,
		Parts: []provider.Part{
			{Text: secretText},
			{Image: &provider.Blob{MediaType: "image/png", Data: []byte(secretBlob)}},
			{Document: &provider.Blob{MediaType: "application/pdf", Data: []byte("%PDF-" + secretBlob)}},
		},
	}
}

const okReply = `{"candidates":[{"content":{"parts":[{"text":"SECRET-"},{"text":"ANSWER-TEXT"}],"role":"model"},"finishReason":"STOP"}],
"usageMetadata":{"promptTokenCount":11,"candidatesTokenCount":7},"modelVersion":"gemini-test-model-001"}`

func asProviderError(t *testing.T, err error) *provider.Error {
	t.Helper()
	var pe *provider.Error
	if !errors.As(err, &pe) {
		t.Fatalf("error %v is not a *provider.Error", err)
	}
	return pe
}

func assertNoContent(t *testing.T, err error) {
	t.Helper()
	msg := err.Error()
	for _, s := range []string{secretText, secretBlob, secretAns, "SECRET", base64.StdEncoding.EncodeToString([]byte(secretBlob))} {
		if strings.Contains(msg, s) {
			t.Errorf("error message contains content %q: %s", s, msg)
		}
	}
}

func TestCompleteRequest(t *testing.T) {
	t.Parallel()
	c, got := serve(t, http.StatusOK, okReply, nil)
	if _, err := c.Complete(t.Context(), baseRequest()); err != nil {
		t.Fatal(err)
	}

	if want := "/v1beta/models/gemini-test-model:generateContent"; got.path != want {
		t.Errorf("path = %q, want %q", got.path, want)
	}
	if got.key != testKey {
		t.Errorf("x-goog-api-key = %q, want the key", got.key)
	}
	if strings.Contains(got.rawURL, testKey) || strings.Contains(strings.ToLower(got.rawURL), "key=") {
		t.Errorf("URL carries the key: %q", got.rawURL)
	}

	var body struct {
		SystemInstruction struct {
			Parts []map[string]any `json:"parts"`
		} `json:"systemInstruction"`
		Contents []struct {
			Role  string           `json:"role"`
			Parts []map[string]any `json:"parts"`
		} `json:"contents"`
		GenerationConfig map[string]any `json:"generationConfig"`
	}
	if err := json.Unmarshal(got.body, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.SystemInstruction.Parts) != 1 || body.SystemInstruction.Parts[0]["text"] != "be careful" {
		t.Errorf("systemInstruction = %+v", body.SystemInstruction)
	}
	if len(body.Contents) != 1 || body.Contents[0].Role != "user" || len(body.Contents[0].Parts) != 3 {
		t.Fatalf("contents = %+v, want one user entry with 3 parts", body.Contents)
	}
	parts := body.Contents[0].Parts
	if parts[0]["text"] != secretText {
		t.Errorf("part 0 = %v, want the text", parts[0])
	}
	for i, tc := range []struct {
		mime string
		data string
	}{
		{"image/png", secretBlob},
		{"application/pdf", "%PDF-" + secretBlob},
	} {
		inline, _ := parts[i+1]["inlineData"].(map[string]any)
		if inline["mimeType"] != tc.mime {
			t.Errorf("part %d mimeType = %v, want %s", i+1, inline["mimeType"], tc.mime)
		}
		if inline["data"] != base64.StdEncoding.EncodeToString([]byte(tc.data)) {
			t.Errorf("part %d data is not the base64 of the blob", i+1)
		}
	}
	if got, want := body.GenerationConfig["maxOutputTokens"], float64(4096); got != want {
		t.Errorf("maxOutputTokens = %v, want default %v", got, want)
	}
	if got := body.GenerationConfig["temperature"]; got != 0.1 {
		t.Errorf("temperature = %v, want 0.1", got)
	}
}

func TestCompleteRequestMaxTokensAndNoSystem(t *testing.T) {
	t.Parallel()
	c, got := serve(t, http.StatusOK, okReply, nil)
	req := provider.Request{Model: "m", MaxTokens: 123, Parts: []provider.Part{{Text: "x"}}}
	if _, err := c.Complete(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(got.body, &body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["systemInstruction"]; ok {
		t.Error("systemInstruction sent for an empty System")
	}
	gc, _ := body["generationConfig"].(map[string]any)
	if gc["maxOutputTokens"] != float64(123) {
		t.Errorf("maxOutputTokens = %v, want 123", gc["maxOutputTokens"])
	}
}

func TestCompleteSuccess(t *testing.T) {
	t.Parallel()
	c, _ := serve(t, http.StatusOK, okReply, nil)
	resp, err := c.Complete(t.Context(), baseRequest())
	if err != nil {
		t.Fatal(err)
	}
	want := provider.Response{Text: secretAns, InputTokens: 11, OutputTokens: 7, Model: "gemini-test-model-001"}
	if resp != want {
		t.Errorf("response = %+v, want %+v", resp, want)
	}
}

func TestCompleteTruncated(t *testing.T) {
	t.Parallel()
	for reason, want := range map[string]bool{"MAX_TOKENS": true, "STOP": false} {
		reply := `{"candidates":[{"content":{"parts":[{"text":"{\"a\":"}]},"finishReason":"` + reason + `"}]}`
		c, _ := serve(t, http.StatusOK, reply, nil)
		resp, err := c.Complete(t.Context(), baseRequest())
		if err != nil {
			t.Fatalf("finishReason %q: %v", reason, err)
		}
		if resp.Text != `{"a":` || resp.Truncated != want {
			t.Errorf("finishReason %q: text %q truncated %v, want truncated %v", reason, resp.Text, resp.Truncated, want)
		}
	}
}

func TestCompleteModelFallsBackToRequested(t *testing.T) {
	t.Parallel()
	reply := `{"candidates":[{"content":{"parts":[{"text":"ok"}]}}]}`
	c, _ := serve(t, http.StatusOK, reply, nil)
	resp, err := c.Complete(t.Context(), baseRequest())
	if err != nil {
		t.Fatal(err)
	}
	if resp.Model != "gemini-test-model" {
		t.Errorf("Model = %q, want the requested model", resp.Model)
	}
}

func TestCompleteStatusErrors(t *testing.T) {
	t.Parallel()
	errBody := `{"error":{"message":"` + secretText + ` ` + secretAns + `"}}`
	tests := []struct {
		name       string
		status     int
		hdr        http.Header
		wantKind   provider.Kind
		retryAfter time.Duration
	}{
		{"429 with Retry-After", 429, http.Header{"Retry-After": {"7"}}, provider.Retryable, 7 * time.Second},
		{"429 without Retry-After", 429, nil, provider.Retryable, 0},
		{"400", 400, nil, provider.Permanent, 0},
		{"403", 403, nil, provider.Permanent, 0},
		{"503", 503, nil, provider.Retryable, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c, _ := serve(t, tc.status, errBody, tc.hdr)
			_, err := c.Complete(t.Context(), baseRequest())
			if err == nil {
				t.Fatal("expected an error")
			}
			pe := asProviderError(t, err)
			if pe.Kind != tc.wantKind || pe.Status != tc.status || pe.RetryAfter != tc.retryAfter {
				t.Errorf("got kind=%v status=%d retryAfter=%v, want kind=%v status=%d retryAfter=%v",
					pe.Kind, pe.Status, pe.RetryAfter, tc.wantKind, tc.status, tc.retryAfter)
			}
			assertNoContent(t, err)
		})
	}
}

func TestCompleteRefusals(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"blocked prompt":     `{"promptFeedback":{"blockReason":"SAFETY"}}`,
		"safety finish":      `{"candidates":[{"content":{"parts":[{"text":"` + secretAns + `"}]},"finishReason":"SAFETY"}]}`,
		"recitation finish":  `{"candidates":[{"finishReason":"RECITATION"}]}`,
		"prohibited content": `{"candidates":[{"finishReason":"PROHIBITED_CONTENT"}]}`,
		"no candidates":      `{"candidates":[]}`,
		"no text":            `{"candidates":[{"content":{"parts":[]},"finishReason":"STOP"}]}`,
		"malformed body":     `{"candidates": [` + secretAns,
	}
	for name, reply := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			c, _ := serve(t, http.StatusOK, reply, nil)
			_, err := c.Complete(t.Context(), baseRequest())
			if err == nil {
				t.Fatal("expected an error")
			}
			if pe := asProviderError(t, err); pe.Kind != provider.Permanent {
				t.Errorf("kind = %v, want permanent", pe.Kind)
			}
			assertNoContent(t, err)
		})
	}
}

func TestCompleteDeadlineIsRetryable(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		<-release
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })

	c := newClient(t, srv.URL, 5*time.Second)
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	_, err := c.Complete(ctx, baseRequest())
	if err == nil {
		t.Fatal("expected an error")
	}
	if pe := asProviderError(t, err); pe.Kind != provider.Retryable {
		t.Errorf("kind = %v, want retryable", pe.Kind)
	}
	assertNoContent(t, err)
}

func TestCompleteConnectionFailureIsRetryable(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.NotFoundHandler())
	base := srv.URL
	srv.Close() // nothing listens any more

	c := newClient(t, base, 5*time.Second)
	_, err := c.Complete(t.Context(), baseRequest())
	if err == nil {
		t.Fatal("expected an error")
	}
	if pe := asProviderError(t, err); pe.Kind != provider.Retryable {
		t.Errorf("kind = %v, want retryable", pe.Kind)
	}
	assertNoContent(t, err)
}

func TestCompleteCancelIsPermanent(t *testing.T) {
	t.Parallel()
	c := newClient(t, "http://127.0.0.1:1", 5*time.Second)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := c.Complete(ctx, baseRequest())
	if err == nil {
		t.Fatal("expected an error")
	}
	if pe := asProviderError(t, err); pe.Kind != provider.Permanent {
		t.Errorf("kind = %v, want permanent", pe.Kind)
	}
}

func TestNewDefaults(t *testing.T) {
	t.Parallel()
	c := New(&config.Endpoint{Timeout: time.Second})
	if c.baseURL != DefaultBaseURL {
		t.Errorf("baseURL = %q, want %q", c.baseURL, DefaultBaseURL)
	}
	c = New(&config.Endpoint{BaseURL: "http://example.test/", Timeout: time.Second})
	if c.baseURL != "http://example.test" {
		t.Errorf("baseURL = %q, want trailing slash trimmed", c.baseURL)
	}
}
