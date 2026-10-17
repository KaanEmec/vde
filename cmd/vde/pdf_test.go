package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kaanemec/vde/internal/extract"
)

// TestExtractPDFWithoutPages runs vde extract on booking.pdf, without --page, against
// groups that cannot read application/pdf: the service sends the text layer to a
// text-only group and rendered pages to an image-only one (ADR 0010).
func TestExtractPDFWithoutPages(t *testing.T) {
	t.Parallel()
	base, err := os.ReadFile(scenarioConfig)
	if err != nil {
		t.Fatal(err)
	}
	const pdfAccepts = "{accepts: [application/pdf, image/png, image/jpeg, text/plain, text/html]}"
	for _, tt := range []struct {
		name, accepts, wantLabel, wantPart string
	}{
		{"text-only", "{accepts: [text/plain]}", "text+text_layer", "--- page 1 ---"},
		{"image-only", "{accepts: [image/png, image/jpeg]}", "pages+rendered", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := filepath.Join(t.TempDir(), "vde.yaml")
			if err := os.WriteFile(cfg, []byte(strings.ReplaceAll(string(base), pdfAccepts, tt.accepts)), 0o600); err != nil {
				t.Fatal(err)
			}
			var got extract.Input
			o := extractOptions{
				config:                cfg,
				document:              filepath.Join(samples, "booking.pdf"),
				extractorInstructions: filepath.Join(samples, "extractor.md"),
				judgeInstructions:     filepath.Join(samples, "judge.md"),
				providerScript:        filepath.Join(scenarioDir, "accept-first-try.yaml"),
				run: func(ctx context.Context, in extract.Input, roles extract.Roles) extract.Outcome {
					got = in
					return extract.Run(ctx, in, roles)
				},
			}
			out, err := extractFile(t.Context(), o, io.Discard)
			if err != nil {
				t.Fatalf("extract: %v", err)
			}
			if out.Status != extract.StatusAccepted {
				t.Errorf("status = %s, want accepted", out.Status)
			}
			rep := got.Representation
			if rep.Label() != tt.wantLabel || !rep.Groundable || !strings.Contains(rep.GroundText, "HV-48213") {
				t.Errorf("representation %s, groundable %v; want %s with the booking reference", rep.Label(), rep.Groundable, tt.wantLabel)
			}
			if tt.wantPart != "" && !strings.Contains(rep.Parts[0].Text, tt.wantPart) {
				t.Errorf("text part lacks %q", tt.wantPart)
			}
			if tt.wantPart == "" && (len(rep.Parts) != 1 || rep.Parts[0].Image == nil) {
				t.Errorf("parts = %d, want one rendered page image", len(rep.Parts))
			}
		})
	}
}
