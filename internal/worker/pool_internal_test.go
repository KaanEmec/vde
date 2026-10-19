package worker

import (
	"encoding/json"
	"testing"

	"github.com/kaanemec/vde/internal/extract"
	"github.com/kaanemec/vde/internal/job"
)

// TestFromRowFeedback: a timed-out job's saved feedback becomes its final feedback,
// without a grounding repair's entries under judge policy always (contract v1 sources
// only, ADR 0009) and with them under auto.
func TestFromRowFeedback(t *testing.T) {
	saved := json.RawMessage(`[{"pointer":"/born","message":"value not found in the document","source":"grounding"},` +
		`{"pointer":"/name","message":"m","source":"judge"}]`)
	tests := []struct {
		policy, want string
	}{
		{"", `[{"pointer":"/name","message":"m","source":"judge"}]`},
		{"always", `[{"pointer":"/name","message":"m","source":"judge"}]`},
		{"auto", string(saved)},
		{"never", string(saved)},
	}
	for _, tt := range tests {
		o := fromRow(job.Job{JudgePolicy: tt.policy, Attempts: 2, Feedback: saved}, job.StatusFailed, extract.CodeTimeout)
		if string(o.Feedback) != tt.want {
			t.Errorf("policy %q: feedback %s, want %s", tt.policy, o.Feedback, tt.want)
		}
	}
	only := json.RawMessage(`[{"pointer":"/born","message":"value not found in the document","source":"grounding"}]`)
	if o := fromRow(job.Job{JudgePolicy: "always", Feedback: only}, job.StatusFailed, extract.CodeTimeout); string(o.Feedback) != `[]` {
		t.Errorf("only grounding entries: feedback %s, want []", o.Feedback)
	}
}
