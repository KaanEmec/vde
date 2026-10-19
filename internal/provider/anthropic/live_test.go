package anthropic

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/kaanemec/vde/internal/config"
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
			{Document: &provider.Blob{MediaType: "application/pdf", Data: onePagePDF("Invoice number VDE-4711")}},
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

// onePagePDF builds a minimal valid single-page PDF showing line.
func onePagePDF(line string) []byte {
	stream := fmt.Sprintf("BT /F1 18 Tf 72 720 Td (%s) Tj ET", line)
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	var b strings.Builder
	b.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objs))
	for i, o := range objs {
		offsets[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, off := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xref)
	return []byte(b.String())
}
