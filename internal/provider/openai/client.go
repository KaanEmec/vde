package openai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/provider"
)

const (
	defaultMaxTokens = 4096

	presetOpenRouter  = "openrouter"
	openRouterReferer = "https://github.com/kaanemec/vde"
	openRouterTitle   = "vde"

	mediaTypePDF = "application/pdf"
)

var _ provider.Provider = (*Client)(nil)

// Client calls the chat completions API of one OpenAI-compatible endpoint.
type Client struct {
	http   *http.Client
	url    string // {base_url}/chat/completions
	key    string // empty for local endpoints without authentication
	preset string
}

// New returns a client for ep. The base URL already includes the API version path
// (for example https://api.openai.com/v1).
func New(ep *config.Endpoint) *Client {
	return &Client{
		http:   provider.NewHTTPClient(ep.Timeout, provider.DefaultMaxResponseBytes),
		url:    strings.TrimRight(ep.BaseURL, "/") + "/chat/completions",
		key:    ep.APIKey(),
		preset: ep.Preset,
	}
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []message     `json:"messages"`
	MaxTokens   int           `json:"max_tokens"`
	Temperature float64       `json:"temperature"`
	Provider    *routingPrefs `json:"provider,omitempty"` // OpenRouter only
}

// routingPrefs is OpenRouter's provider routing object.
type routingPrefs struct {
	DataCollection string `json:"data_collection"`
}

type message struct {
	Role    string `json:"role"`
	Content any    `json:"content"` // string for system, []contentPart for user
}

type contentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *imageURL `json:"image_url,omitempty"`
	File     *filePart `json:"file,omitempty"`
}

type imageURL struct {
	URL string `json:"url"`
}

type filePart struct {
	Filename string `json:"filename"`
	FileData string `json:"file_data"`
}

type chatResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

// Complete sends req as one chat completion and returns the text of the first choice.
func (c *Client) Complete(ctx context.Context, req provider.Request) (provider.Response, error) {
	body, err := c.buildBody(req)
	if err != nil {
		return provider.Response{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return provider.Response{}, &provider.Error{Message: "invalid endpoint url", Err: err}
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	if c.key != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.key)
	}
	if c.preset == presetOpenRouter {
		httpReq.Header.Set("HTTP-Referer", openRouterReferer)
		httpReq.Header.Set("X-Title", openRouterTitle)
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return provider.Response{}, transportError("request failed", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return provider.Response{}, provider.StatusError(resp, "chat completions request failed")
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return provider.Response{}, transportError("reading response failed", err)
	}
	return parseResponse(raw, req.Model)
}

// buildBody maps a role request to the chat completions body.
func (c *Client) buildBody(req provider.Request) ([]byte, error) {
	parts := make([]contentPart, 0, len(req.Parts))
	for _, p := range req.Parts {
		part, err := toContentPart(p)
		if err != nil {
			return nil, err
		}
		parts = append(parts, part)
	}
	msgs := make([]message, 0, 2)
	if req.System != "" {
		msgs = append(msgs, message{Role: "system", Content: req.System})
	}
	msgs = append(msgs, message{Role: "user", Content: parts})

	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = defaultMaxTokens
	}
	cr := chatRequest{
		Model:       req.Model,
		Messages:    msgs,
		MaxTokens:   maxTokens,
		Temperature: req.Temperature,
	}
	if c.preset == presetOpenRouter {
		// OpenRouter's routing option: only providers that do not log or train on prompts.
		cr.Provider = &routingPrefs{DataCollection: "deny"}
	}
	body, err := json.Marshal(cr)
	if err != nil {
		return nil, &provider.Error{Message: "encoding request failed", Err: err}
	}
	return body, nil
}

func toContentPart(p provider.Part) (contentPart, error) {
	switch {
	case p.Image != nil:
		return contentPart{Type: "image_url", ImageURL: &imageURL{URL: dataURL(p.Image)}}, nil
	case p.Document != nil:
		if p.Document.MediaType != mediaTypePDF {
			return contentPart{}, &provider.Error{Message: "unsupported document media type"}
		}
		return contentPart{Type: "file", File: &filePart{Filename: "document.pdf", FileData: dataURL(p.Document)}}, nil
	default:
		return contentPart{Type: "text", Text: p.Text}, nil
	}
}

func dataURL(b *provider.Blob) string {
	return "data:" + b.MediaType + ";base64," + base64.StdEncoding.EncodeToString(b.Data)
}

// parseResponse extracts the answer. Errors never include the response content.
func parseResponse(raw []byte, requestedModel string) (provider.Response, error) {
	var cr chatResponse
	if json.Unmarshal(raw, &cr) != nil {
		// Not wrapped: json errors can quote a byte of the response.
		return provider.Response{}, &provider.Error{Message: "malformed response"}
	}
	if len(cr.Choices) == 0 {
		return provider.Response{}, &provider.Error{Message: "response has no choices"}
	}
	choice := cr.Choices[0]
	if choice.FinishReason == "content_filter" {
		return provider.Response{}, &provider.Error{Message: "response blocked by content filter"}
	}
	text := contentText(choice.Message.Content)
	if strings.TrimSpace(text) == "" {
		return provider.Response{}, &provider.Error{Message: "response has no content"}
	}
	model := cr.Model
	if model == "" {
		model = requestedModel
	}
	return provider.Response{
		Text:         text,
		InputTokens:  cr.Usage.PromptTokens,
		OutputTokens: cr.Usage.CompletionTokens,
		Model:        model,
		Truncated:    choice.FinishReason == "length",
	}, nil
}

// contentText reads message content given as a string or as an array of text parts.
func contentText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var sb strings.Builder
	for _, p := range parts {
		if p.Type == "" || p.Type == "text" {
			sb.WriteString(p.Text)
		}
	}
	return sb.String()
}

// transportError classifies a failure that produced no usable response.
func transportError(msg string, err error) *provider.Error {
	return &provider.Error{Kind: provider.Classify(err), Message: msg, Err: err}
}
