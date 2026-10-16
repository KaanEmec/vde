package bedrock

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/provider"
)

// TestLiveSmoke calls real Bedrock with the AWS default credentials. It is opt-in: set
// VDE_LIVE_BEDROCK_REGION and VDE_LIVE_BEDROCK_MODEL (a model or inference profile id), and
// optionally VDE_LIVE_BEDROCK_PDF (a path) to also exercise document input.
func TestLiveSmoke(t *testing.T) {
	region, model := os.Getenv("VDE_LIVE_BEDROCK_REGION"), os.Getenv("VDE_LIVE_BEDROCK_MODEL")
	if region == "" || model == "" {
		t.Skip("set VDE_LIVE_BEDROCK_REGION and VDE_LIVE_BEDROCK_MODEL to run")
	}
	c, err := New(&config.Endpoint{Region: region, Timeout: 2 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	parts := []provider.Part{{Text: "Reply with the single word: ready. Then describe any attached file in one short sentence."}}
	if p := os.Getenv("VDE_LIVE_BEDROCK_PDF"); p != "" {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		parts = append(parts, provider.Part{Document: &provider.Blob{MediaType: "application/pdf", Data: data}})
	}
	resp, err := c.Complete(context.Background(), provider.Request{Model: model, MaxTokens: 200, Parts: parts})
	if err != nil {
		t.Fatalf("Complete error: %v", err)
	}
	if resp.Text == "" || resp.InputTokens == 0 {
		t.Errorf("response = %+v", resp)
	}
}
