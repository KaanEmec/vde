package gemini

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/provider"
	"github.com/kaanemec/vde/internal/provider/providertest"
)

const (
	testKey    = "test-key-123"
	secretText = "SECRET-DOCUMENT-TEXT"
	secretBlob = "SECRET-BLOB-BYTES"
	secretAns  = "SECRET-ANSWER-TEXT"
)

// secrets must never appear in an error message.
var secrets = []string{secretText, secretBlob, secretAns, "SECRET", base64.StdEncoding.EncodeToString([]byte(secretBlob))}

// serve starts a server that records the request and replies with status and body.
func serve(t *testing.T, status int, reply string, hdr http.Header) (*Client, *providertest.Captured) {
	t.Helper()
	srv, got := providertest.Serve(t, providertest.Reply(status, hdr, reply))
	return newClient(t, srv.URL), got
}

func newClient(t *testing.T, baseURL string) *Client {
	t.Helper()
	var ep config.Endpoint
	ep.BaseURL = baseURL
	ep.Timeout = 5 * time.Second
	c := New(&ep)
	c.apiKey = testKey
	return c
}

func baseRequest() provider.Request {
	return provider.Request{
		Model:     "gemini-test-model",
		System:    "be careful",
		MaxTokens: 4096,
		Parts: []provider.Part{
			{Text: secretText},
			{Image: &provider.Blob{MediaType: "image/png", Data: []byte(secretBlob)}},
			{Document: &provider.Blob{MediaType: "application/pdf", Data: []byte("%PDF-" + secretBlob)}},
		},
	}
}

const okReply = `{"candidates":[{"content":{"parts":[{"text":"SECRET-"},{"text":"ANSWER-TEXT"}],"role":"model"},"finishReason":"STOP"}],
"usageMetadata":{"promptTokenCount":11,"candidatesTokenCount":7},"modelVersion":"gemini-test-model-001"}`

func TestCompleteRequest(t *testing.T) {
	t.Parallel()
	c, got := serve(t, http.StatusOK, okReply, nil)
	if _, err := c.Complete(t.Context(), baseRequest()); err != nil {
		t.Fatal(err)
	}

	if want := "/v1beta/models/gemini-test-model:generateContent"; got.URL.Path != want {
		t.Errorf("path = %q, want %q", got.URL.Path, want)
	}
	if got.Header.Get("x-goog-api-key") != testKey {
		t.Errorf("x-goog-api-key = %q, want the key", got.Header.Get("x-goog-api-key"))
	}
	if strings.Contains(got.URL.String(), testKey) || strings.Contains(strings.ToLower(got.URL.String()), "key=") {
		t.Errorf("URL carries the key: %q", got.URL.String())
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
	if err := json.Unmarshal(got.Body, &body); err != nil {
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
		t.Errorf("maxOutputTokens = %v, want %v", got, want)
	}
	if got, ok := body.GenerationConfig["temperature"]; !ok || got != float64(0) {
		t.Errorf("temperature = %v, want 0 sent explicitly", got)
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
	if err := json.Unmarshal(got.Body, &body); err != nil {
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
	want := provider.Response{Text: secretAns, InputTokens: 11, OutputTokens: 7}
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
			pe := providertest.AsError(t, err)
			if pe.Kind != tc.wantKind || pe.Status != tc.status || pe.RetryAfter != tc.retryAfter {
				t.Errorf("got kind=%v status=%d retryAfter=%v, want kind=%v status=%d retryAfter=%v",
					pe.Kind, pe.Status, pe.RetryAfter, tc.wantKind, tc.status, tc.retryAfter)
			}
			providertest.AssertNoContent(t, err, secrets...)
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
		"blocklist finish":   `{"candidates":[{"content":{"parts":[{"text":"` + secretAns + `"}]},"finishReason":"BLOCKLIST"}]}`,
		"spii finish":        `{"candidates":[{"content":{"parts":[{"text":"` + secretAns + `"}]},"finishReason":"SPII"}]}`,
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
			if pe := providertest.AsError(t, err); pe.Kind != provider.Permanent {
				t.Errorf("kind = %v, want permanent", pe.Kind)
			}
			providertest.AssertNoContent(t, err, secrets...)
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

	c := newClient(t, srv.URL)
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	_, err := c.Complete(ctx, baseRequest())
	if err == nil {
		t.Fatal("expected an error")
	}
	if pe := providertest.AsError(t, err); pe.Kind != provider.Retryable {
		t.Errorf("kind = %v, want retryable", pe.Kind)
	}
	providertest.AssertNoContent(t, err, secrets...)
}

func TestCompleteConnectionFailureIsRetryable(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.NotFoundHandler())
	base := srv.URL
	srv.Close() // nothing listens any more

	c := newClient(t, base)
	_, err := c.Complete(t.Context(), baseRequest())
	if err == nil {
		t.Fatal("expected an error")
	}
	if pe := providertest.AsError(t, err); pe.Kind != provider.Retryable {
		t.Errorf("kind = %v, want retryable", pe.Kind)
	}
	providertest.AssertNoContent(t, err, secrets...)
}

func TestCompleteCancelIsPermanent(t *testing.T) {
	t.Parallel()
	c := newClient(t, "http://127.0.0.1:1")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := c.Complete(ctx, baseRequest())
	if err == nil {
		t.Fatal("expected an error")
	}
	if pe := providertest.AsError(t, err); pe.Kind != provider.Permanent {
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
