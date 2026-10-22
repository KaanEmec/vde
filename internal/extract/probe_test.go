package extract

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/kaanemec/vde/internal/provider"
	"github.com/kaanemec/vde/internal/provider/fake"
)

func TestProbe(t *testing.T) {
	t.Parallel()
	const secret = "SECRET-ANSWER-TEXT"
	extracted := `<analysis>` + secret + `</analysis><json>{"invoice_number": "INV-2041", "issued": "2026-03-14", "total": 50, "currency": "EUR"}</json>`
	tests := []struct {
		name      string
		role      string
		step      fake.Step
		want      ProbeStatus
		wantOK    bool
		wantError string // substring of Err
	}{
		{"extractor ok", "extractor", fake.Answer(extracted), ProbeOK, true, ""},
		{"extractor unparseable", "extractor", fake.Answer(secret + " {\"total\": "), ProbeUnparseable, false, "incomplete"},
		{"extractor analysis without json block", "extractor", fake.Answer("<analysis>" + secret + "</analysis>"), ProbeUnparseable, false, "no <json> block"},
		{"provider error", "extractor", fake.Fail(provider.Permanent, 400), ProbeFailed, false, "status 400"},
		{"judge rejects the wrong value", "judge",
			fake.Answer(`<analysis>` + secret + `</analysis><verdict>{"verdict": "reject", "feedback": {"/total": "` + secret + `"}}</verdict>`),
			ProbeOK, true, ""},
		{"judge accepts the wrong candidate", "judge",
			fake.Answer(`<verdict>{"verdict": "accept", "feedback": {}}</verdict>`), ProbeAcceptsWrong, true, ""},
		{"judge rejects elsewhere", "judge",
			fake.Answer(`<verdict>{"verdict": "reject", "feedback": {"/issued": "` + secret + `"}}</verdict>`), ProbeRejectsElsewhere, true, ""},
		{"judge unparseable", "judge",
			fake.Answer(`<verdict>{"verdict": "reject", "feedback": {}}</verdict>`), ProbeUnparseable, false, "reject without feedback"},
		{"judge provider error", "judge", fake.Fail(provider.Retryable, 503), ProbeFailed, false, "status 503"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := fake.New(fake.Script{tt.step})
			r := Probe(context.Background(), tt.role, p, "m", 512)
			if r.Status != tt.want || r.OK() != tt.wantOK {
				t.Fatalf("status %q ok %v, want %q ok %v (err %v)", r.Status, r.OK(), tt.want, tt.wantOK, r.Err)
			}
			if tt.wantError == "" && r.Err != nil || tt.wantError != "" && (r.Err == nil || !strings.Contains(r.Err.Error(), tt.wantError)) {
				t.Errorf("err %v, want containing %q", r.Err, tt.wantError)
			}
			if r.Err != nil && strings.Contains(r.Err.Error(), secret) {
				t.Errorf("error carries answer text: %v", r.Err)
			}
			calls := p.Calls()
			if len(calls) != 1 {
				t.Fatalf("%d calls, want 1", len(calls))
			}
			req := calls[0].Request
			wantSystem := "You are the extractor"
			if tt.role == "judge" {
				wantSystem = "You are the judge"
			}
			if req.Model != "m" || req.MaxTokens != 512 || !strings.HasPrefix(req.System, wantSystem) {
				t.Errorf("request model %q, max tokens %d, system role %v", req.Model, req.MaxTokens, strings.HasPrefix(req.System, wantSystem))
			}
			last := req.Parts[len(req.Parts)-1].Text
			if hasCandidate := strings.HasPrefix(last, sepCandidate); hasCandidate != (tt.role == "judge") {
				t.Errorf("last part is the candidate: %v", hasCandidate)
			}
			if tt.step.Err == nil && (r.InputTokens != fake.InputTokens || r.OutputTokens != fake.OutputTokens) {
				t.Errorf("tokens %d/%d", r.InputTokens, r.OutputTokens)
			}
		})
	}
}

// TestProbeSample checks the embedded sample: the schema compiles, the extractor's right
// answer validates, and the judge's candidate validates but differs at the wrong pointer.
func TestProbeSample(t *testing.T) {
	t.Parallel()
	in := probeInput()
	right := json.RawMessage(`{"invoice_number": "INV-2041", "issued": "2026-03-14", "total": 50.00, "currency": "EUR"}`)
	for _, c := range []json.RawMessage{right, json.RawMessage(probeCandidate)} {
		if fb := in.Schema.Validate(c); len(fb) > 0 {
			t.Errorf("sample candidate does not validate: %v", fb)
		}
	}
	if !strings.Contains(probeDocument, "50.00") || strings.Contains(probeDocument, "80") {
		t.Error("the candidate's total must contradict the document")
	}
}
