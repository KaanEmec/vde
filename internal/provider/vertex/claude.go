package vertex

import (
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/kaanemec/vde/internal/provider"
)

// anthropicVersion is the body field Vertex requires in place of the anthropic-version
// header and the model name (the model is in the URL).
const anthropicVersion = "vertex-2023-10-16"

// The Messages API shapes, as in internal/provider/anthropic (unexported there).

type claudeRequest struct {
	AnthropicVersion string          `json:"anthropic_version"`
	MaxTokens        int             `json:"max_tokens"`
	System           string          `json:"system,omitempty"`
	Messages         []claudeMessage `json:"messages"`
}

type claudeMessage struct {
	Role    string        `json:"role"`
	Content []claudeBlock `json:"content"`
}

type claudeBlock struct {
	Type   string        `json:"type"`
	Text   string        `json:"text,omitempty"`
	Source *claudeSource `json:"source,omitempty"`
}

type claudeSource struct {
	Type      string `json:"type"` // always base64
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type claudeResponse struct {
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

func buildClaudeBody(req provider.Request) ([]byte, error) {
	blocks := make([]claudeBlock, 0, len(req.Parts))
	for _, p := range req.Parts {
		b, err := claudeBlockOf(p)
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, b)
	}
	r := claudeRequest{
		AnthropicVersion: anthropicVersion,
		MaxTokens:        req.MaxTokens,
		System:           req.System,
		Messages:         []claudeMessage{{Role: "user", Content: blocks}},
	}
	body, err := json.Marshal(r)
	if err != nil {
		return nil, &provider.Error{Message: "vertex: encoding request failed", Err: err}
	}
	return body, nil
}

// claudeBlockOf maps one part to a content block. Exactly one field of a Part must be set.
func claudeBlockOf(p provider.Part) (claudeBlock, error) {
	switch {
	case p.Text != "" && p.Image == nil && p.Document == nil:
		return claudeBlock{Type: "text", Text: p.Text}, nil
	case p.Image != nil && p.Text == "" && p.Document == nil:
		if p.Image.MediaType == "" || len(p.Image.Data) == 0 {
			return claudeBlock{}, &provider.Error{Message: "vertex: image part is empty or has no media type"}
		}
		return claudeBlock{Type: "image", Source: claudeSourceOf(p.Image)}, nil
	case p.Document != nil && p.Text == "" && p.Image == nil:
		if p.Document.MediaType != "application/pdf" {
			return claudeBlock{}, &provider.Error{Message: "vertex: document parts must be application/pdf for Claude models"}
		}
		if len(p.Document.Data) == 0 {
			return claudeBlock{}, &provider.Error{Message: "vertex: document part is empty"}
		}
		return claudeBlock{Type: "document", Source: claudeSourceOf(p.Document)}, nil
	default:
		return claudeBlock{}, &provider.Error{Message: "vertex: part must set exactly one of text, image or document"}
	}
}

func claudeSourceOf(b *provider.Blob) *claudeSource {
	return &claudeSource{Type: "base64", MediaType: b.MediaType, Data: base64.StdEncoding.EncodeToString(b.Data)}
}

func parseClaude(raw []byte, status int) (provider.Response, error) {
	var mr claudeResponse
	if err := json.Unmarshal(raw, &mr); err != nil {
		return provider.Response{}, &provider.Error{Status: status, Message: "vertex: malformed response"}
	}
	if mr.StopReason == "refusal" {
		return provider.Response{}, refused(status, "vertex: the model declined to answer (refusal)")
	}
	var text strings.Builder
	for _, b := range mr.Content {
		if b.Type == "text" {
			text.WriteString(b.Text)
		}
	}
	if strings.TrimSpace(text.String()) == "" {
		return provider.Response{}, &provider.Error{Status: status, Message: "vertex: the model returned no text"}
	}
	return provider.Response{
		Text:         text.String(),
		InputTokens:  mr.Usage.InputTokens,
		OutputTokens: mr.Usage.OutputTokens,
		Truncated:    mr.StopReason == "max_tokens",
	}, nil
}
