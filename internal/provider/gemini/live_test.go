package gemini

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/provider"
)

// TestLiveSmoke calls the real Gemini API. It is opt-in: set VDE_LIVE_GEMINI_KEY and
// VDE_LIVE_GEMINI_MODEL (a current model id), and optionally VDE_LIVE_GEMINI_PDF and
// VDE_LIVE_GEMINI_IMAGE (paths to files) to also exercise inline document input.
func TestLiveSmoke(t *testing.T) {
	key, model := os.Getenv("VDE_LIVE_GEMINI_KEY"), os.Getenv("VDE_LIVE_GEMINI_MODEL")
	if key == "" || model == "" {
		t.Skip("set VDE_LIVE_GEMINI_KEY and VDE_LIVE_GEMINI_MODEL to run")
	}
	c := New(&config.Endpoint{Timeout: 2 * time.Minute})
	c.apiKey = key

	parts := []provider.Part{{Text: "Reply with the single word: ready. Then describe any attached file in one short sentence."}}
	if p := os.Getenv("VDE_LIVE_GEMINI_PDF"); p != "" {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		parts = append(parts, provider.Part{Document: &provider.Blob{MediaType: "application/pdf", Data: data}})
	}
	if p := os.Getenv("VDE_LIVE_GEMINI_IMAGE"); p != "" {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		mt := "image/png"
		if strings.HasSuffix(p, ".jpg") || strings.HasSuffix(p, ".jpeg") {
			mt = "image/jpeg"
		}
		parts = append(parts, provider.Part{Image: &provider.Blob{MediaType: mt, Data: data}})
	}
	resp, err := c.Complete(t.Context(), provider.Request{Model: model, Parts: parts, MaxTokens: 256})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text == "" || resp.InputTokens == 0 {
		t.Errorf("unexpected response %+v", resp)
	}
}
