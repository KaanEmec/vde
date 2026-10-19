package eval_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kaanemec/vde/internal/eval"
	"github.com/kaanemec/vde/internal/extract"
)

// testCase is a case with a two-field ground truth compared exactly.
func testCase(id string) eval.Case {
	return eval.Case{ID: id, Expected: json.RawMessage(`{"a": 1, "b": "x"}`)}
}

const (
	right = `{"a": 1, "b": "x"}`
	wrong = `{"a": 2, "b": "x"}`
)

// scripted returns a Runner answering each case id with its outcome after a delay.
func scripted(outcomes map[string]eval.Outcome, running *atomic.Int32, peak *atomic.Int32) eval.Runner {
	return func(ctx context.Context, c eval.Case) (eval.Outcome, error) {
		n := running.Add(1)
		defer running.Add(-1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		o, ok := outcomes[c.ID]
		if !ok {
			return eval.Outcome{}, errors.New("document: unreadable")
		}
		return o, nil
	}
}

func usage(in, out int) *eval.Usage { return &eval.Usage{Calls: 2, InputTokens: in, OutputTokens: out} }

func TestRunAndSummarize(t *testing.T) {
	t.Parallel()
	outcomes := map[string]eval.Outcome{
		"c1-correct-accept": {Status: string(extract.StatusAccepted), Result: json.RawMessage(right), Attempts: 1, ServiceVersion: "sv1", Usage: usage(100, 20)},
		"c2-false-accept":   {Status: string(extract.StatusAccepted), Result: json.RawMessage(wrong), Attempts: 2, Usage: usage(200, 40)},
		"c3-false-reject":   {Status: string(extract.StatusRejected), Result: json.RawMessage(right), Attempts: 3, Usage: usage(300, 60)},
		"c4-right-reject":   {Status: string(extract.StatusRejected), Result: json.RawMessage(wrong), Attempts: 3},
		"c5-failed":         {Status: string(extract.StatusFailed), ErrorCode: "provider_unavailable", Attempts: 1},
	}
	var cases []eval.Case
	for _, id := range []string{"c1-correct-accept", "c2-false-accept", "c3-false-reject", "c4-right-reject", "c5-failed", "c6-harness-error"} {
		cases = append(cases, testCase(id))
	}
	var running, peak atomic.Int32
	var progressed []int
	results := eval.Run(context.Background(), cases, 3, scripted(outcomes, &running, &peak), func(done int, r eval.CaseResult) {
		progressed = append(progressed, done)
	})

	if p := peak.Load(); p > 3 || p < 2 {
		t.Errorf("peak concurrency %d, want 2 or 3", p)
	}
	if len(progressed) != 6 || progressed[5] != 6 {
		t.Errorf("progress calls %v, want 1..6", progressed)
	}
	for i, r := range results {
		if r.ID != cases[i].ID {
			t.Fatalf("results[%d].ID = %s, want %s (order of cases)", i, r.ID, cases[i].ID)
		}
	}
	byID := map[string]eval.CaseResult{}
	for _, r := range results {
		byID[r.ID] = r
	}
	checks := []struct {
		id                             string
		correct, falseAccept, falseRej bool
		status                         string
	}{
		{"c1-correct-accept", true, false, false, string(extract.StatusAccepted)},
		{"c2-false-accept", false, true, false, string(extract.StatusAccepted)},
		{"c3-false-reject", true, false, true, string(extract.StatusRejected)},
		{"c4-right-reject", false, false, false, string(extract.StatusRejected)},
		{"c5-failed", false, false, false, string(extract.StatusFailed)},
		{"c6-harness-error", false, false, false, eval.StatusError},
	}
	for _, c := range checks {
		r := byID[c.id]
		if r.Correct != c.correct || r.FalseAccept != c.falseAccept || r.FalseReject != c.falseRej || r.Status != c.status {
			t.Errorf("%s: %+v", c.id, r)
		}
	}
	if r := byID["c6-harness-error"]; r.HarnessError == "" || !r.Fields.NoResult || r.FieldAccuracy != 0 {
		t.Errorf("harness error case: %+v", r)
	}
	if r := byID["c2-false-accept"]; r.FieldAccuracy != 0.5 || len(r.Fields.Mismatches) != 1 || r.Fields.Mismatches[0] != "/a" {
		t.Errorf("false accept fields: %+v", r.Fields)
	}

	s := eval.Summarize(results)
	want := eval.Summary{
		Cases: 6, Accepted: 2, Rejected: 2, Failed: 1, Errors: 1, Correct: 2,
		AcceptanceRate: 2.0 / 6, FalseAccepts: 1, FalseAcceptsJudge: 1, FalseAcceptRate: 0.5, FalseRejects: 1, FalseRejectRate: 0.5,
		// 1+2 accepted, 2+1 rejected, none for failed and error: 6 of 12 fields.
		FieldAccuracy: 0.5, AcceptedFieldAccuracy: 0.75, MeanAttempts: 10.0 / 6,
		UsageCases: 3, Calls: 6, InputTokens: 600, OutputTokens: 120,
	}
	s.DurationMedianMS, s.DurationP95MS = 0, 0
	if s != want {
		t.Errorf("Summarize() =\n%+v\nwant\n%+v", s, want)
	}
}

func calls(n int) *int { return &n }

// TestSummarize_judgePolicy checks the v0.4 totals on a mixed set: accepted by the judge,
// grounded, sampled with a disagreement, extracted and rejected (ADR 0009).
func TestSummarize_judgePolicy(t *testing.T) {
	t.Parallel()
	judged := func(result string, n int) eval.Outcome {
		return eval.Outcome{Status: string(extract.StatusAccepted), Result: json.RawMessage(result), Attempts: 1, JudgePolicy: "auto",
			Verification: extract.VerificationJudge, JudgeCalls: calls(n), Decision: extract.DecisionJudged}
	}
	grounded := func(result string) eval.Outcome {
		return eval.Outcome{Status: string(extract.StatusAccepted), Result: json.RawMessage(result), Attempts: 1, JudgePolicy: "auto",
			Verification: extract.VerificationGrounded, JudgeCalls: calls(0), Decision: extract.DecisionGrounded}
	}
	extracted := func(result string) eval.Outcome {
		return eval.Outcome{Status: string(extract.StatusExtracted), Result: json.RawMessage(result), Attempts: 1, JudgePolicy: "never",
			Verification: extract.VerificationNone, JudgeCalls: calls(0), Decision: extract.DecisionSkipped}
	}
	outcomes := map[string]eval.Outcome{
		"j1-judged":          judged(right, 1),
		"j2-judged-wrong":    judged(wrong, 1),
		"g1-grounded":        grounded(right),
		"g2-grounded-wrong":  grounded(wrong),
		"s1-sampled":         {Status: string(extract.StatusAccepted), Result: json.RawMessage(right), Attempts: 2, Verification: extract.VerificationJudge, JudgeCalls: calls(2), Decision: extract.DecisionSampled, Disagreements: 1},
		"x1-extracted":       extracted(right),
		"x2-extracted-wrong": extracted(wrong),
		"r1-rejected":        {Status: string(extract.StatusRejected), Result: json.RawMessage(wrong), Attempts: 3, Verification: extract.VerificationNone, JudgeCalls: calls(3), Decision: extract.DecisionJudged},
	}
	var cases []eval.Case
	for id := range outcomes {
		cases = append(cases, testCase(id))
	}
	var running, peak atomic.Int32
	results := eval.Run(context.Background(), cases, 2, scripted(outcomes, &running, &peak), nil)
	byID := map[string]eval.CaseResult{}
	for _, r := range results {
		byID[r.ID] = r
	}
	for _, id := range []string{"x1-extracted", "x2-extracted-wrong"} {
		if r := byID[id]; r.FalseAccept || r.FalseReject {
			t.Errorf("%s: an extracted case is a false accept or reject: %+v", id, r)
		}
	}
	if r := byID["g2-grounded-wrong"]; !r.FalseAccept || r.Verification != extract.VerificationGrounded || *r.JudgeCalls != 0 || r.JudgePolicy != "auto" {
		t.Errorf("grounded false accept: %+v", r)
	}

	s := eval.Summarize(results)
	s.DurationMedianMS, s.DurationP95MS = 0, 0
	want := eval.Summary{
		Cases: 8, Accepted: 5, Rejected: 1, Extracted: 2, Correct: 4,
		AcceptanceRate: 5.0 / 8,
		FalseAccepts:   2, FalseAcceptsJudge: 1, FalseAcceptsGrounded: 1, FalseAcceptRate: 0.4,
		// 2+1+2+1+2 accepted, 2+1 extracted, 1 rejected: 12 of 16 fields.
		FieldAccuracy: 0.75, AcceptedFieldAccuracy: 0.8, ExtractedFieldAccuracy: 0.75,
		VerifiedJudge: 3, VerifiedGrounded: 2, GroundedShare: 0.4,
		JudgeCallCases: 8, JudgeCalls: 7, Sampled: 1, Disagreements: 1,
		MeanAttempts: 11.0 / 8,
	}
	if s != want {
		t.Errorf("Summarize() =\n%+v\nwant\n%+v", s, want)
	}

	slices.SortFunc(results, func(a, b eval.CaseResult) int { return strings.Compare(a.ID, b.ID) })
	now := sampleReport("sv-auto", results...)
	now.JudgePolicy = "auto"
	var out bytes.Buffer
	now.WriteTable(&out)
	before := sampleReport("sv-always", eval.CaseResult{ID: "g1-grounded", Status: string(extract.StatusAccepted), Verification: extract.VerificationJudge, Correct: true})
	before.JudgePolicy = "always"
	now.WriteDiff(&out, before)
	text := out.String()
	for _, want := range []string{
		"FALSE ACCEPT (grounded)", "judge policy auto",
		"false accepts      2 of 5 accepted (40.0%): 1 judge, 1 grounded",
		"(5 accepted, 2 extracted, 1 rejected, 0 failed, 0 harness errors)",
		"verification       3 judge, 2 grounded (40.0% of accepted), 3 none",
		"judge calls        7 in 8 case(s); 1 sampled, 1 disagreement(s)",
		"75.0% on extracted",
		"judge policy always -> auto",
		"g1-grounded: accepted/judge correct -> accepted/grounded correct",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("output lacks %q:\n%s", want, text)
		}
	}
}

func TestSummarize_durations(t *testing.T) {
	t.Parallel()
	var results []eval.CaseResult
	for _, d := range []int64{50, 10, 40, 20, 30, 100, 60, 70, 80, 90} {
		results = append(results, eval.CaseResult{Status: string(extract.StatusAccepted), DurationMS: d})
	}
	s := eval.Summarize(results)
	if s.DurationMedianMS != 55 || s.DurationP95MS != 100 {
		t.Errorf("median %d p95 %d, want 55 and 100", s.DurationMedianMS, s.DurationP95MS)
	}
	if z := eval.Summarize(nil); z.Cases != 0 || z.AcceptanceRate != 0 || z.DurationP95MS != 0 {
		t.Errorf("Summarize(nil) = %+v", z)
	}
}

func sampleReport(sv string, results ...eval.CaseResult) eval.Report {
	return eval.Report{
		ServiceVersion: sv, Target: "local", Corpus: "testdata/eval", Cases: "*",
		StartedAt: time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC),
		Summary:   eval.Summarize(results), Results: results,
	}
}

func TestReport_SaveLoad(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "eval-reports")
	r := sampleReport("vde-0.3.0+abc/def", eval.CaseResult{ID: "c1", Status: string(extract.StatusAccepted), Correct: true})
	first, err := r.Save(dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := r.Save(dir)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(first) != "vde-0.3.0+abc_def-2026-10-16.json" || filepath.Base(second) != "vde-0.3.0+abc_def-2026-10-16-2.json" {
		t.Errorf("Save() paths %s, %s", first, second)
	}
	got, err := eval.LoadReport(first)
	if err != nil {
		t.Fatal(err)
	}
	if got.ServiceVersion != r.ServiceVersion || len(got.Results) != 1 || got.Summary != r.Summary {
		t.Errorf("LoadReport() = %+v", got)
	}
	if _, err := eval.LoadReport(filepath.Join(dir, "missing.json")); err == nil {
		t.Error("LoadReport(missing) succeeded")
	}
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := eval.LoadReport(bad); err == nil {
		t.Error("LoadReport(invalid JSON) succeeded")
	}
}

func TestReport_WriteTableAndDiff(t *testing.T) {
	t.Parallel()
	before := sampleReport("sv-before",
		eval.CaseResult{ID: "c1", Status: string(extract.StatusAccepted), Correct: true, Fields: eval.Fields{Total: 2, Matched: 2}},
		eval.CaseResult{ID: "c2", Status: string(extract.StatusRejected)},
		eval.CaseResult{ID: "gone", Status: string(extract.StatusAccepted), Correct: true},
	)
	now := sampleReport("sv-now",
		eval.CaseResult{ID: "c1", Status: string(extract.StatusAccepted), FalseAccept: true, Fields: eval.Fields{Total: 2, Matched: 1, Mismatches: []string{"/a"}}},
		eval.CaseResult{ID: "c2", Status: string(extract.StatusRejected), Usage: &eval.Usage{Calls: 1, InputTokens: 10, OutputTokens: 5}},
		eval.CaseResult{ID: "new", Status: string(extract.StatusFailed), ErrorCode: "timeout", Fields: eval.Fields{Total: 2, NoResult: true}},
	)
	var out bytes.Buffer
	now.WriteTable(&out)
	now.WriteDiff(&out, before)
	text := out.String()
	for _, want := range []string{
		"FALSE ACCEPT", "failed (timeout)", "no result", "false accepts      1 of 1 accepted (100.0%)",
		"tokens             10 input, 5 output in 1 call(s)",
		"compared with sv-before", "c1: accepted correct -> accepted FALSE ACCEPT", "new: new, failed -",
		"gone: not in this run", "3 case(s) changed",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("output lacks %q:\n%s", want, text)
		}
	}

	out.Reset()
	before.WriteDiff(&out, before)
	if !strings.Contains(out.String(), "no case changed") {
		t.Errorf("self diff:\n%s", out.String())
	}
}
