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
// text-only group and rendered pages to an image-only one (ADR 0010). Both are grounded
// on the text layer, so --judge auto accepts the script's grounded candidate without a
// judge call (ADR 0009). A natively read PDF is grounded through its text layer only
// under auto, never under always.
func TestExtractPDFWithoutPages(t *testing.T) {
	t.Parallel()
	base, err := os.ReadFile(scenarioConfig)
	if err != nil {
		t.Fatal(err)
	}
	const pdfAccepts = "{accepts: [application/pdf, image/png, image/jpeg, text/plain, text/html]}"
	for _, tt := range []struct {
		name, accepts, wantLabel, wantPart, judge string
		wantGroundable                            bool
		wantVerification                          string
	}{
		{"text-only", "{accepts: [text/plain]}", "text+text_layer", "--- page 1 ---", "", true, extract.VerificationJudge},
		{"image-only", "{accepts: [image/png, image/jpeg]}", "pages+rendered", "", "", true, extract.VerificationJudge},
		{"text-only auto", "{accepts: [text/plain]}", "text+text_layer", "--- page 1 ---", "auto", true, extract.VerificationGrounded},
		{"image-only auto", "{accepts: [image/png, image/jpeg]}", "pages+rendered", "", "auto", true, extract.VerificationGrounded},
		{"native", pdfAccepts, "native", "", "", false, extract.VerificationJudge},
		{"native auto", pdfAccepts, "native", "", "auto", true, extract.VerificationGrounded},
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
				judge:                 tt.judge,
				run: func(ctx context.Context, in extract.Input, roles extract.Roles) extract.Outcome {
					got = in
					return extract.Run(ctx, in, roles)
				},
			}
			out, err := extractFile(t.Context(), o, io.Discard)
			if err != nil {
				t.Fatalf("extract: %v", err)
			}
			wantCalls := 2
			if tt.wantVerification == extract.VerificationGrounded {
				wantCalls = 1 // no judge call
			}
			if out.Status != extract.StatusAccepted || out.Verification != tt.wantVerification || out.Usage.Calls != wantCalls {
				t.Errorf("status %s, verification %s, %d call(s); want accepted, %s, %d", out.Status, out.Verification,
					out.Usage.Calls, tt.wantVerification, wantCalls)
			}
			rep := got.Representation
			if rep.Label() != tt.wantLabel || rep.Groundable != tt.wantGroundable ||
				tt.wantGroundable && !strings.Contains(rep.GroundText, "HV-48213") {
				t.Errorf("representation %s, groundable %v; want %s, groundable %v with the booking reference",
					rep.Label(), rep.Groundable, tt.wantLabel, tt.wantGroundable)
			}
			if tt.wantPart != "" && !strings.Contains(rep.Parts[0].Text, tt.wantPart) {
				t.Errorf("text part lacks %q", tt.wantPart)
			}
			if tt.wantLabel == "pages+rendered" && (len(rep.Parts) != 1 || rep.Parts[0].Image == nil) {
				t.Errorf("parts = %d, want one rendered page image", len(rep.Parts))
			}
		})
	}
}
