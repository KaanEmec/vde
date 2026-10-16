package extract

import (
	"testing"

	"github.com/kaanemec/vde/internal/provider"
	"github.com/kaanemec/vde/internal/provider/fake"
)

// TestUsage: the outcome and each progress report carry the token usage of the calls
// that answered; a failed call adds nothing.
func TestUsage(t *testing.T) {
	t.Parallel()
	// Attempt 1: primary fails over to fallback (one answer), the judge rejects.
	// Attempt 2: fallback (primary is marked down) answers, the judge accepts. Four
	// answered calls of five.
	r := newRig(t,
		fake.Script{fake.Fail(provider.Retryable, 503)},
		fake.Script{fake.Answer(valid), fake.Answer(valid2)},
		fake.Script{fake.Answer(reject), fake.Answer(accept)})
	in := input(t, personSchema)
	var progress []Progress
	in.OnAttempt = func(p Progress) { progress = append(progress, p) }
	out := Run(t.Context(), in, r.roles)
	checkOutcome(t, in, out, StatusAccepted, 2, "")
	want := Usage{InputTokens: 4 * fake.InputTokens, OutputTokens: 4 * fake.OutputTokens, Calls: 4}
	if out.Usage != want {
		t.Errorf("outcome usage = %+v, want %+v", out.Usage, want)
	}
	if len(progress) != 1 || progress[0].Usage != (Usage{InputTokens: 2 * fake.InputTokens, OutputTokens: 2 * fake.OutputTokens, Calls: 2}) {
		t.Errorf("progress = %+v, want one report after two answered calls", progress)
	}
}
