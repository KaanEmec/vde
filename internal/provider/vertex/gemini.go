package vertex

import (
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/kaanemec/vde/internal/provider"
)

// The wire shapes below are the Gemini generateContent ones, identical to those of
// internal/provider/gemini; they are repeated here because that package keeps them
// unexported.

type geminiRequest struct {
	SystemInstruction *geminiContent         `json:"systemInstruction,omitempty"`
	Contents          []geminiContent        `json:"contents"`
	GenerationConfig  geminiGenerationConfig `json:"generationConfig"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text       string            `json:"text,omitempty"`
	InlineData *geminiInlineData `json:"inlineData,omitempty"`
}

type geminiInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"` // base64
}

type geminiGenerationConfig struct {
	MaxOutputTokens int     `json:"maxOutputTokens"`
	Temperature     float64 `json:"temperature"` // always 0, sent explicitly
}

type geminiResponse struct {
	Candidates []struct {
		Content      geminiContent `json:"content"`
		FinishReason string        `json:"finishReason"`
	} `json:"candidates"`
	PromptFeedback struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
	UsageMetadata struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
	} `json:"usageMetadata"`
}

func buildGeminiBody(req provider.Request) ([]byte, error) {
	gr := geminiRequest{
		GenerationConfig: geminiGenerationConfig{MaxOutputTokens: req.MaxTokens},
	}
	if req.System != "" {
		gr.SystemInstruction = &geminiContent{Parts: []geminiPart{{Text: req.System}}}
	}
	user := geminiContent{Role: "user", Parts: make([]geminiPart, 0, len(req.Parts))}
	for _, p := range req.Parts {
		switch {
		case p.Image != nil:
			user.Parts = append(user.Parts, geminiBlob(p.Image))
		case p.Document != nil:
			user.Parts = append(user.Parts, geminiBlob(p.Document))
		default:
			user.Parts = append(user.Parts, geminiPart{Text: p.Text})
		}
	}
	gr.Contents = []geminiContent{user}
	body, err := json.Marshal(gr)
	if err != nil {
		return nil, &provider.Error{Message: "vertex: encoding request failed", Err: err}
	}
	return body, nil
}

func geminiBlob(b *provider.Blob) geminiPart {
	return geminiPart{InlineData: &geminiInlineData{
		MimeType: b.MediaType,
		Data:     base64.StdEncoding.EncodeToString(b.Data),
	}}
}

func parseGemini(raw []byte, status int) (provider.Response, error) {
	var gr geminiResponse
	if err := json.Unmarshal(raw, &gr); err != nil {
		// The decode error is dropped: it can quote response bytes.
		return provider.Response{}, &provider.Error{Status: status, Message: "vertex: malformed response"}
	}
	if gr.PromptFeedback.BlockReason != "" {
		return provider.Response{}, provider.Refused(status, "vertex: prompt blocked by the provider")
	}
	if len(gr.Candidates) == 0 {
		return provider.Response{}, provider.Refused(status, "vertex: response had no candidates")
	}
	cand := gr.Candidates[0]
	switch cand.FinishReason {
	case "SAFETY", "RECITATION", "PROHIBITED_CONTENT", "BLOCKLIST", "SPII":
		return provider.Response{}, provider.Refused(status, "vertex: answer blocked by the provider")
	}
	var text strings.Builder
	for _, p := range cand.Content.Parts {
		text.WriteString(p.Text)
	}
	if text.Len() == 0 {
		return provider.Response{}, provider.Refused(status, "vertex: response had no text")
	}
	return provider.Response{
		Text:         text.String(),
		InputTokens:  gr.UsageMetadata.PromptTokenCount,
		OutputTokens: gr.UsageMetadata.CandidatesTokenCount,
		Truncated:    cand.FinishReason == "MAX_TOKENS",
	}, nil
}
