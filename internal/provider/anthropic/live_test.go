package anthropic

import (
	"os"
	"strings"
	"testing"

	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/pdf/pdftest"
	"github.com/kaanemec/vde/internal/provider"
)

// TestLiveOnePagePDF calls the real API with a one-page PDF. It is skipped unless
// VDE_LIVE_ANTHROPIC=1 and ANTHROPIC_API_KEY are set; VDE_LIVE_ANTHROPIC_MODEL picks the model.
func TestLiveOnePagePDF(t *testing.T) {
	if os.Getenv("VDE_LIVE_ANTHROPIC") != "1" || os.Getenv("ANTHROPIC_API_KEY") == "" {
		t.Skip("set VDE_LIVE_ANTHROPIC=1 and ANTHROPIC_API_KEY to call the real API")
	}
	model := os.Getenv("VDE_LIVE_ANTHROPIC_MODEL")
	if model == "" {
		model = "claude-haiku-5-5"
	}
	cfg, err := config.Parse([]byte("endpoints:\n  live:\n    provider: anthropic\n    api_key_env: ANTHROPIC_API_KEY\n    timeout: 60s\n"), os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := New(cfg.Endpoints["live"]).Complete(t.Context(), provider.Request{
		Model: model,
		Parts: []provider.Part{
			{Document: &provider.Blob{MediaType: "application/pdf", Data: pdftest.Build(pdftest.TextPage("Invoice number VDE-4711"))}},
			{Text: "Reply with the invoice number in the document and nothing else."},
		},
		MaxTokens: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resp.Text, "4711") {
		t.Errorf("answer = %q, want it to contain the invoice number", resp.Text)
	}
	if resp.InputTokens == 0 || resp.OutputTokens == 0 {
		t.Errorf("response = %+v, want usage", resp)
	}
}
