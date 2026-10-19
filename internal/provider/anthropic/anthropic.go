package anthropic

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/provider"
)

const (
	// DefaultBaseURL is used when the endpoint configures none.
	DefaultBaseURL = "https://api.anthropic.com"

	// apiVersion is the value of the anthropic-version header.
	apiVersion = "2023-06-01"
)

// Client calls the Anthropic Messages API. It is safe for concurrent use.
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

var _ provider.Provider = (*Client)(nil)

// New returns a client for the endpoint: its base URL (DefaultBaseURL when empty), key,
// and per-call timeout.
func New(ep *config.Endpoint) *Client {
	base := strings.TrimRight(ep.BaseURL, "/")
	if base == "" {
		base = DefaultBaseURL
	}
	return &Client{
		baseURL: base,
		apiKey:  ep.APIKey(),
		http:    provider.NewHTTPClient(ep.Timeout, provider.DefaultMaxResponseBytes),
	}
}

// messagesRequest is the POST /v1/messages body. Only the fields vde uses are modelled:
// no tools, streaming, metadata, caching or thinking.
type messagesRequest struct {
	Model     string        `json:"model"`
	MaxTokens int           `json:"max_tokens"`
	System    string        `json:"system,omitempty"`
	Messages  []userMessage `json:"messages"`
}

type userMessage struct {
	Role    string         `json:"role"`
	Content []contentBlock `json:"content"`
}

type contentBlock struct {
	Type   string  `json:"type"`
	Text   string  `json:"text,omitempty"`
	Source *source `json:"source,omitempty"`
}

type source struct {
	Type      string `json:"type"` // always base64
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type messagesResponse struct {
	Model      string `json:"model"`
	StopReason string `json:"stop_reason"`
	Content    []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

type errorResponse struct {
	Error struct {
		Type string `json:"type"`
	} `json:"error"`
}

// errorTypes are the error types the API documents. Only these are echoed into messages,
// so an endpoint cannot smuggle arbitrary text into logs through the error body.
var errorTypes = map[string]bool{
	"invalid_request_error": true,
	"authentication_error":  true,
	"billing_error":         true,
	"permission_error":      true,
	"not_found_error":       true,
	"request_too_large":     true,
	"rate_limit_error":      true,
	"timeout_error":         true,
	"api_error":             true,
	"overloaded_error":      true,
}

// Complete sends the request as one user message and returns the concatenated text blocks.
// It sends no temperature: current Claude models reject non-default sampling parameters.
func (c *Client) Complete(ctx context.Context, req provider.Request) (provider.Response, error) {
	body, err := buildBody(req)
	if err != nil {
		return provider.Response{}, err
	}
	header := http.Header{}
	header.Set("anthropic-version", apiVersion)
	if c.apiKey != "" {
		header.Set("x-api-key", c.apiKey)
	}
	raw, _, err := provider.PostJSON(ctx, c.http, c.baseURL+"/v1/messages", header, body, "anthropic")
	if err != nil {
		return provider.Response{}, statusError(raw, err)
	}
	return parseResponse(raw)
}

// buildBody maps the request to the Messages API body.
func buildBody(req provider.Request) ([]byte, error) {
	if req.Model == "" {
		return nil, &provider.Error{Message: "anthropic: request has no model"}
	}
	if len(req.Parts) == 0 {
		return nil, &provider.Error{Message: "anthropic: request has no content parts"}
	}
	blocks := make([]contentBlock, 0, len(req.Parts))
	for _, p := range req.Parts {
		b, err := block(p)
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, b)
	}
	r := messagesRequest{
		Model:     req.Model,
		MaxTokens: req.MaxTokens,
		System:    req.System,
		Messages:  []userMessage{{Role: "user", Content: blocks}},
	}
	body, err := json.Marshal(r)
	if err != nil {
		return nil, &provider.Error{Message: "anthropic: encoding request failed", Err: err}
	}
	return body, nil
}

// block maps one part to a content block. Exactly one field of a Part must be set.
func block(p provider.Part) (contentBlock, error) {
	switch {
	case p.Text != "" && p.Image == nil && p.Document == nil:
		return contentBlock{Type: "text", Text: p.Text}, nil
	case p.Image != nil && p.Text == "" && p.Document == nil:
		if p.Image.MediaType == "" || len(p.Image.Data) == 0 {
			return contentBlock{}, &provider.Error{Message: "anthropic: image part is empty or has no media type"}
		}
		return contentBlock{Type: "image", Source: base64Source(p.Image)}, nil
	case p.Document != nil && p.Text == "" && p.Image == nil:
		if p.Document.MediaType != "application/pdf" {
			return contentBlock{}, &provider.Error{Message: "anthropic: document parts must be application/pdf"}
		}
		if len(p.Document.Data) == 0 {
			return contentBlock{}, &provider.Error{Message: "anthropic: document part is empty"}
		}
		return contentBlock{Type: "document", Source: base64Source(p.Document)}, nil
	default:
		return contentBlock{}, &provider.Error{Message: "anthropic: part must set exactly one of text, image or document"}
	}
}

func base64Source(b *provider.Blob) *source {
	return &source{Type: "base64", MediaType: b.MediaType, Data: base64.StdEncoding.EncodeToString(b.Data)}
}

// statusError names a non-2xx response by its documented error type, never by the body;
// other errors pass unchanged.
func statusError(raw []byte, err error) error {
	var pe *provider.Error
	if !errors.As(err, &pe) || pe.Status == 0 {
		return err
	}
	pe.Message = "anthropic: error response"
	var er errorResponse
	if json.Unmarshal(raw, &er) == nil && errorTypes[er.Error.Type] {
		pe.Message = "anthropic: " + er.Error.Type
	}
	return pe
}

// parseResponse reads a 2xx body. A refusal or an answer without text is a permanent
// failure: asking again elsewhere would not change a content decision, and an empty
// answer is not a usable extraction.
func parseResponse(raw []byte) (provider.Response, error) {
	var mr messagesResponse
	if json.Unmarshal(raw, &mr) != nil {
		// Not wrapped: a syntax error can quote bytes of the response.
		return provider.Response{}, &provider.Error{Message: "anthropic: unreadable response body"}
	}
	if mr.StopReason == "refusal" {
		return provider.Response{}, &provider.Error{Message: "anthropic: the model declined to answer (refusal)"}
	}
	var text strings.Builder
	for _, b := range mr.Content {
		if b.Type == "text" {
			text.WriteString(b.Text)
		}
	}
	if strings.TrimSpace(text.String()) == "" {
		return provider.Response{}, &provider.Error{Message: "anthropic: the model returned no text"}
	}
	return provider.Response{
		Text:         text.String(),
		InputTokens:  mr.Usage.InputTokens,
		OutputTokens: mr.Usage.OutputTokens,
		Truncated:    mr.StopReason == "max_tokens",
	}, nil
}
