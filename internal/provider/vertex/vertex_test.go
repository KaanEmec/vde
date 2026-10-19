package vertex

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/provider"
)

const (
	testToken  = "ya29.test-token"
	secretText = "SECRET-DOCUMENT-TEXT"
	secretBlob = "SECRET-BLOB-BYTES"
	secretAns  = "SECRET-ANSWER-TEXT"
)

type fakeTokens struct {
	tok *oauth2.Token
	err error
}

func (f fakeTokens) Token() (*oauth2.Token, error) { return f.tok, f.err }

var okTokens = fakeTokens{tok: &oauth2.Token{AccessToken: testToken, TokenType: "Bearer"}}

type captured struct {
	path   string
	auth   string
	ctype  string
	header http.Header
	body   []byte
}

func serve(t *testing.T, status int, reply string, hdr http.Header) (*Client, *captured) {
	t.Helper()
	got := &captured{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.path = r.URL.EscapedPath() + "?" + r.URL.RawQuery
		got.auth = r.Header.Get("Authorization")
		got.ctype = r.Header.Get("Content-Type")
		got.header = r.Header.Clone()
		got.body, _ = io.ReadAll(r.Body)
		for k, v := range hdr {
			w.Header()[k] = v
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(srv.Close)
	return testClient(srv.URL, okTokens), got
}

func testClient(baseURL string, ts oauth2.TokenSource) *Client {
	return newClient(&config.Endpoint{
		BaseURL: baseURL, Project: "my-project", Region: "us-east5", Timeout: 5 * time.Second,
	}, ts)
}

func request(model string) provider.Request {
	return provider.Request{
		Model:     model,
		System:    "You are the extractor.",
		MaxTokens: 777,
		Parts: []provider.Part{
			{Text: secretText},
			{Image: &provider.Blob{MediaType: "image/png", Data: []byte(secretBlob)}},
			{Document: &provider.Blob{MediaType: "application/pdf", Data: []byte(secretBlob)}},
		},
	}
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func TestComplete_gemini(t *testing.T) {
	t.Parallel()
	c, got := serve(t, 200, `{"candidates":[{"content":{"role":"model","parts":[{"text":"`+secretAns+`"}]},"finishReason":"STOP"}],`+
		`"usageMetadata":{"promptTokenCount":12,"candidatesTokenCount":5},"modelVersion":"gemini-example-001"}`, nil)
	resp, err := c.Complete(context.Background(), request("gemini-example"))
	if err != nil {
		t.Fatalf("Complete error: %v", err)
	}
	if resp.Text != secretAns || resp.InputTokens != 12 || resp.OutputTokens != 5 || resp.Truncated {
		t.Errorf("response = %+v", resp)
	}
	if want := "/v1/projects/my-project/locations/us-east5/publishers/google/models/gemini-example:generateContent?"; got.path != want {
		t.Errorf("path = %q, want %q", got.path, want)
	}
	if got.auth != "Bearer "+testToken || got.ctype != "application/json" {
		t.Errorf("Authorization = %q, Content-Type = %q", got.auth, got.ctype)
	}
	if got.header.Get("User-Agent") != provider.UserAgent() {
		t.Errorf("User-Agent = %q", got.header.Get("User-Agent"))
	}

	var body struct {
		SystemInstruction struct {
			Parts []struct{ Text string }
		}
		Contents []struct {
			Role  string
			Parts []struct {
				Text       string
				InlineData *struct{ MimeType, Data string }
			}
		}
		GenerationConfig struct {
			MaxOutputTokens int
			Temperature     float64
		}
	}
	if err := json.Unmarshal(got.body, &body); err != nil {
		t.Fatal(err)
	}
	if body.SystemInstruction.Parts[0].Text != "You are the extractor." {
		t.Errorf("systemInstruction = %+v", body.SystemInstruction)
	}
	if len(body.Contents) != 1 || body.Contents[0].Role != "user" || len(body.Contents[0].Parts) != 3 {
		t.Fatalf("contents = %+v", body.Contents)
	}
	parts := body.Contents[0].Parts
	if parts[0].Text != secretText ||
		parts[1].InlineData == nil || parts[1].InlineData.MimeType != "image/png" || parts[1].InlineData.Data != b64(secretBlob) ||
		parts[2].InlineData == nil || parts[2].InlineData.MimeType != "application/pdf" {
		t.Errorf("parts = %+v", parts)
	}
	if body.GenerationConfig.MaxOutputTokens != 777 || body.GenerationConfig.Temperature != 0 {
		t.Errorf("generationConfig = %+v", body.GenerationConfig)
	}
}

func TestComplete_claude(t *testing.T) {
	t.Parallel()
	c, got := serve(t, 200, `{"model":"claude-example","stop_reason":"max_tokens","content":[{"type":"text","text":"`+secretAns+`"}],`+
		`"usage":{"input_tokens":20,"output_tokens":9}}`, nil)
	resp, err := c.Complete(context.Background(), request("claude-example@20260101"))
	if err != nil {
		t.Fatalf("Complete error: %v", err)
	}
	if resp.Text != secretAns || resp.InputTokens != 20 || resp.OutputTokens != 9 || !resp.Truncated {
		t.Errorf("response = %+v", resp)
	}
	if want := "/v1/projects/my-project/locations/us-east5/publishers/anthropic/models/claude-example@20260101:rawPredict?"; got.path != want {
		t.Errorf("path = %q, want %q", got.path, want)
	}
	if got.auth != "Bearer "+testToken {
		t.Errorf("Authorization = %q", got.auth)
	}

	var body map[string]any
	if err := json.Unmarshal(got.body, &body); err != nil {
		t.Fatal(err)
	}
	if body["anthropic_version"] != "vertex-2023-10-16" {
		t.Errorf("anthropic_version = %v", body["anthropic_version"])
	}
	if _, ok := body["model"]; ok {
		t.Error("body carries a model: Vertex takes it from the URL")
	}
	if body["max_tokens"] != float64(777) || body["system"] != "You are the extractor." || body["temperature"] != nil {
		t.Errorf("body = %v", body)
	}
	msgs := body["messages"].([]any)
	content := msgs[0].(map[string]any)["content"].([]any)
	if len(msgs) != 1 || msgs[0].(map[string]any)["role"] != "user" || len(content) != 3 {
		t.Fatalf("messages = %v", msgs)
	}
	img := content[1].(map[string]any)
	doc := content[2].(map[string]any)
	if content[0].(map[string]any)["text"] != secretText || img["type"] != "image" || doc["type"] != "document" {
		t.Errorf("content = %v", content)
	}
	src := doc["source"].(map[string]any)
	if src["type"] != "base64" || src["media_type"] != "application/pdf" || src["data"] != b64(secretBlob) {
		t.Errorf("document source = %v", src)
	}
}

func TestComplete_claudeOmitsZeroTemperature(t *testing.T) {
	t.Parallel()
	c, got := serve(t, 200, `{"content":[{"type":"text","text":"ok"}],"usage":{}}`, nil)
	req := provider.Request{Model: "claude-example", MaxTokens: 4096, Parts: []provider.Part{{Text: "hi"}}}
	if _, err := c.Complete(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got.body), "temperature") || !strings.Contains(string(got.body), `"max_tokens":4096`) {
		t.Errorf("body = %s", got.body)
	}
}

func TestEndpoints(t *testing.T) {
	t.Parallel()
	regional := newClient(&config.Endpoint{Project: "p", Region: "europe-west4"}, okTokens)
	if want := "https://europe-west4-aiplatform.googleapis.com/v1/projects/p/locations/europe-west4/publishers/google/models/gemini-x:generateContent"; regional.modelURL("google", "gemini-x", "generateContent") != want {
		t.Errorf("regional URL = %q", regional.modelURL("google", "gemini-x", "generateContent"))
	}
	global := newClient(&config.Endpoint{Project: "p", Region: "global"}, okTokens)
	if want := "https://aiplatform.googleapis.com/v1/projects/p/locations/global/publishers/anthropic/models/claude-x@1:rawPredict"; global.modelURL("anthropic", "claude-x@1", "rawPredict") != want {
		t.Errorf("global URL = %q", global.modelURL("anthropic", "claude-x@1", "rawPredict"))
	}
}

func TestComplete_statusErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		status int
		want   provider.Kind
	}{
		{400, provider.Permanent}, {401, provider.Permanent}, {403, provider.Permanent}, {404, provider.Permanent},
		{408, provider.Retryable}, {429, provider.Retryable}, {500, provider.Retryable}, {503, provider.Retryable},
	}
	for _, model := range []string{"gemini-example", "claude-example"} {
		for _, tt := range tests {
			t.Run(model+"/"+http.StatusText(tt.status), func(t *testing.T) {
				t.Parallel()
				c, _ := serve(t, tt.status, `{"error":{"message":"`+secretText+`"}}`, http.Header{"Retry-After": {"7"}})
				_, err := c.Complete(context.Background(), request(model))
				var pe *provider.Error
				if !errors.As(err, &pe) {
					t.Fatalf("error = %v, want *provider.Error", err)
				}
				if pe.Kind != tt.want || pe.Status != tt.status {
					t.Errorf("Kind = %v, Status = %d; want %v, %d", pe.Kind, pe.Status, tt.want, tt.status)
				}
				if tt.want == provider.Retryable && pe.RetryAfter != 7*time.Second {
					t.Errorf("RetryAfter = %v, want 7s", pe.RetryAfter)
				}
				for _, s := range []string{secretText, secretBlob, testToken} {
					if strings.Contains(err.Error(), s) {
						t.Errorf("error contains %q: %v", s, err)
					}
				}
			})
		}
	}
}

func TestComplete_unusableAnswers(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, model, reply string
	}{
		{"gemini prompt blocked", "gemini-x", `{"promptFeedback":{"blockReason":"SAFETY"},"candidates":[]}`},
		{"gemini safety finish", "gemini-x", `{"candidates":[{"content":{"parts":[{"text":"` + secretAns + `"}]},"finishReason":"SAFETY"}]}`},
		{"gemini no candidates", "gemini-x", `{"candidates":[]}`},
		{"gemini no text", "gemini-x", `{"candidates":[{"content":{"parts":[]},"finishReason":"STOP"}]}`},
		{"gemini malformed", "gemini-x", `{"candidates":[` + secretAns},
		{"claude refusal", "claude-x", `{"stop_reason":"refusal","content":[{"type":"text","text":"` + secretAns + `"}]}`},
		{"claude no text", "claude-x", `{"stop_reason":"end_turn","content":[]}`},
		{"claude malformed", "claude-x", `{"content":` + secretAns},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c, _ := serve(t, 200, tt.reply, nil)
			_, err := c.Complete(context.Background(), provider.Request{Model: tt.model, Parts: []provider.Part{{Text: "x"}}})
			if err == nil || provider.Classify(err) != provider.Permanent {
				t.Fatalf("error = %v, want permanent", err)
			}
			if strings.Contains(err.Error(), secretAns) {
				t.Errorf("error contains response content: %v", err)
			}
		})
	}
}

// TestComplete_unusableAnswerStatus: each family reports a 2xx it cannot use as its
// direct adapter does, Gemini with the status and Claude without; Gemini also refuses the
// BLOCKLIST and SPII finish reasons.
func TestComplete_unusableAnswerStatus(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, model, reply string
		status             int
	}{
		{"gemini blocklist finish", "gemini-x", `{"candidates":[{"content":{"parts":[{"text":"` + secretAns + `"}]},"finishReason":"BLOCKLIST"}]}`, 200},
		{"gemini spii finish", "gemini-x", `{"candidates":[{"content":{"parts":[{"text":"` + secretAns + `"}]},"finishReason":"SPII"}]}`, 200},
		{"gemini malformed", "gemini-x", `{"candidates":[` + secretAns, 200},
		{"claude refusal", "claude-x", `{"stop_reason":"refusal","content":[{"type":"text","text":"` + secretAns + `"}]}`, 0},
		{"claude no text", "claude-x", `{"stop_reason":"end_turn","content":[]}`, 0},
		{"claude malformed", "claude-x", `{"content":` + secretAns, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c, _ := serve(t, 200, tt.reply, nil)
			_, err := c.Complete(context.Background(), provider.Request{Model: tt.model, Parts: []provider.Part{{Text: "x"}}})
			var pe *provider.Error
			if !errors.As(err, &pe) || pe.Kind != provider.Permanent || pe.Status != tt.status {
				t.Fatalf("error = %v, want permanent with status %d", err, tt.status)
			}
		})
	}
}

func TestComplete_badRequests(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		req  provider.Request
	}{
		{"no model", provider.Request{Parts: []provider.Part{{Text: "x"}}}},
		{"no parts", provider.Request{Model: "gemini-x"}},
		{"unsupported family", provider.Request{Model: "llama-x", Parts: []provider.Part{{Text: "x"}}}},
		{"claude text document", provider.Request{Model: "claude-x", Parts: []provider.Part{{Document: &provider.Blob{MediaType: "text/plain", Data: []byte(secretBlob)}}}}},
		{"claude empty part", provider.Request{Model: "claude-x", Parts: []provider.Part{{}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			called := false
			srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
			defer srv.Close()
			_, err := testClient(srv.URL, okTokens).Complete(context.Background(), tt.req)
			if err == nil || provider.Classify(err) != provider.Permanent {
				t.Fatalf("error = %v, want permanent", err)
			}
			if called {
				t.Error("the server was called for an invalid request")
			}
			if strings.Contains(err.Error(), secretBlob) {
				t.Errorf("error contains content: %v", err)
			}
		})
	}
}

func TestComplete_tokenFailure(t *testing.T) {
	t.Parallel()
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	defer srv.Close()
	c := testClient(srv.URL, fakeTokens{err: context.DeadlineExceeded})
	_, err := c.Complete(context.Background(), request("gemini-x"))
	if err == nil || provider.Classify(err) != provider.Retryable {
		t.Errorf("error = %v, want retryable for a timed-out token fetch", err)
	}
	c = testClient(srv.URL, fakeTokens{err: errors.New("invalid_grant")})
	_, err = c.Complete(context.Background(), request("gemini-x"))
	if err == nil || provider.Classify(err) != provider.Permanent {
		t.Errorf("error = %v, want permanent for a rejected credential", err)
	}
	if called {
		t.Error("the server was called without a token")
	}
}

func TestComplete_networkFailureIsRetryable(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()
	_, err := testClient(url, okTokens).Complete(context.Background(), request("gemini-x"))
	if err == nil || provider.Classify(err) != provider.Retryable {
		t.Errorf("error = %v, want retryable", err)
	}
	if strings.Contains(err.Error(), testToken) || strings.Contains(err.Error(), secretText) {
		t.Errorf("error leaks: %v", err)
	}
}

func TestNew_withoutCredentials(t *testing.T) {
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", t.TempDir()+"/missing.json")
	if _, err := New(&config.Endpoint{Project: "p", Region: "us-east5"}); err == nil {
		t.Error("New succeeded without credentials")
	}
}

func TestNew_withCredentialsFile(t *testing.T) {
	// An authorized_user file needs no network until a token is requested.
	dir := t.TempDir()
	file := dir + "/adc.json"
	creds := `{"type":"authorized_user","client_id":"id","client_secret":"secret","refresh_token":"refresh"}`
	if err := writeFile(file, creds); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", file)
	c, err := New(&config.Endpoint{Project: "p", Region: "us-east5"})
	if err != nil || c == nil || c.tokens == nil {
		t.Fatalf("New = %v, %v", c, err)
	}
	if c.baseURL != "https://us-east5-aiplatform.googleapis.com" {
		t.Errorf("baseURL = %q", c.baseURL)
	}
}

func writeFile(name, content string) error { return os.WriteFile(name, []byte(content), 0o600) }
