package gemini

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/provider"
)

const (
	// DefaultBaseURL is the Gemini API host used when the endpoint sets no base_url.
	DefaultBaseURL = "https://generativelanguage.googleapis.com"

	apiKeyHeader = "x-goog-api-key"
)

// Client calls the Gemini generateContent API. It is safe for concurrent use.
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

var _ provider.Provider = (*Client)(nil)

// New returns a Client for the endpoint. The API key travels in the x-goog-api-key
// header, never in the URL, so it cannot land in access logs or error messages.
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

// Wire types for the parts of generateContent this adapter uses.

type generateRequest struct {
	SystemInstruction *content         `json:"systemInstruction,omitempty"`
	Contents          []content        `json:"contents"`
	GenerationConfig  generationConfig `json:"generationConfig"`
}

type content struct {
	Role  string `json:"role,omitempty"`
	Parts []part `json:"parts"`
}

type part struct {
	Text       string      `json:"text,omitempty"`
	InlineData *inlineData `json:"inlineData,omitempty"`
}

type inlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"` // base64
}

type generationConfig struct {
	MaxOutputTokens int     `json:"maxOutputTokens"`
	Temperature     float64 `json:"temperature"` // always 0, sent explicitly
}

type generateResponse struct {
	Candidates []struct {
		Content      content `json:"content"`
		FinishReason string  `json:"finishReason"`
	} `json:"candidates"`
	PromptFeedback struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
	UsageMetadata struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
	} `json:"usageMetadata"`
}

// Complete runs one generateContent call. Errors never contain request or response
// content.
func (c *Client) Complete(ctx context.Context, req provider.Request) (provider.Response, error) {
	body, err := Body(req)
	if err != nil {
		return provider.Response{}, err
	}
	endpoint := c.baseURL + "/v1beta/models/" + url.PathEscape(req.Model) + ":generateContent"
	header := http.Header{}
	header.Set(apiKeyHeader, c.apiKey)
	raw, status, err := provider.PostJSON(ctx, c.http, endpoint, header, body, "gemini")
	if err != nil {
		return provider.Response{}, err
	}
	return Parse(raw, status)
}

// Body maps the request to the generateContent body, the same on the Gemini API and on
// Vertex AI.
func Body(req provider.Request) ([]byte, error) {
	gr := generateRequest{
		GenerationConfig: generationConfig{MaxOutputTokens: req.MaxTokens},
	}
	if req.System != "" {
		gr.SystemInstruction = &content{Parts: []part{{Text: req.System}}}
	}
	user := content{Role: "user", Parts: make([]part, 0, len(req.Parts))}
	for _, p := range req.Parts {
		switch {
		case p.Image != nil:
			user.Parts = append(user.Parts, blobPart(p.Image))
		case p.Document != nil:
			user.Parts = append(user.Parts, blobPart(p.Document))
		default:
			user.Parts = append(user.Parts, part{Text: p.Text})
		}
	}
	gr.Contents = []content{user}
	body, err := json.Marshal(gr)
	if err != nil {
		return nil, &provider.Error{Message: "gemini: encoding request failed", Err: err}
	}
	return body, nil
}

func blobPart(b *provider.Blob) part {
	return part{InlineData: &inlineData{
		MimeType: b.MediaType,
		Data:     base64.StdEncoding.EncodeToString(b.Data),
	}}
}

// Parse reads a 2xx generateContent body; its errors carry status. A blocked prompt or
// answer, or an answer without text, is permanent.
func Parse(raw []byte, status int) (provider.Response, error) {
	var gr generateResponse
	if err := json.Unmarshal(raw, &gr); err != nil {
		// The decode error is dropped: it can quote response bytes.
		return provider.Response{}, &provider.Error{
			Status:  status,
			Message: "gemini: malformed response",
		}
	}
	if gr.PromptFeedback.BlockReason != "" {
		return provider.Response{}, provider.Refused(status, "gemini: prompt blocked by the provider")
	}
	if len(gr.Candidates) == 0 {
		return provider.Response{}, provider.Refused(status, "gemini: response had no candidates")
	}
	cand := gr.Candidates[0]
	switch cand.FinishReason {
	case "SAFETY", "RECITATION", "PROHIBITED_CONTENT", "BLOCKLIST", "SPII":
		return provider.Response{}, provider.Refused(status, "gemini: answer blocked by the provider")
	}
	var text strings.Builder
	for _, p := range cand.Content.Parts {
		text.WriteString(p.Text)
	}
	if text.Len() == 0 {
		return provider.Response{}, provider.Refused(status, "gemini: response had no text")
	}
	return provider.Response{
		Text:         text.String(),
		InputTokens:  gr.UsageMetadata.PromptTokenCount,
		OutputTokens: gr.UsageMetadata.CandidatesTokenCount,
		Truncated:    cand.FinishReason == "MAX_TOKENS",
	}, nil
}
