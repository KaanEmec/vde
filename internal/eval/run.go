package eval

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/kaanemec/vde/internal/extract"
)

// StatusError is the status of a case the harness could not run. The terminal job
// statuses, verifications and decisions are internal/extract's (StatusExtracted is a job
// no judge was asked about: never a false accept or a false reject).
const StatusError = "error"

// Usage is the model usage of one case.
type Usage struct {
	Calls        int `json:"calls"`
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// Outcome is what running one case produced: the job's terminal state.
type Outcome struct {
	Status         string          // accepted, rejected, failed or extracted
	Result         json.RawMessage // the final candidate, nil when none
	Attempts       int
	ErrorCode      string // the job error code when failed
	ServiceVersion string
	Usage          *Usage // nil when not known (a run over HTTP)
	// JudgePolicy is the policy the job ran under, "" when not known (a run over HTTP
	// without --judge, which runs under the application's policy).
	JudgePolicy string
	// Verification is the job's verification: judge, grounded or none.
	Verification string
	// JudgeCalls counts the judge-role calls, nil when not known (a run over HTTP).
	JudgeCalls *int
	// Decision is how the last schema-valid candidate was decided (Decision*), "" when
	// none reached the decision or when not known (a run over HTTP); Disagreements counts
	// sampled judge calls that rejected a grounded candidate.
	Decision      string
	Disagreements int
}

// Runner runs one case to its terminal state. An error means the harness could not run
// the case at all (an unreadable file, an unsupported document, a transport failure); a
// failed job is an Outcome with status failed.
type Runner func(ctx context.Context, c Case) (Outcome, error)

// CaseResult is one case's line of a report. It never carries content.
type CaseResult struct {
	ID             string  `json:"id"`
	Status         string  `json:"status"`
	ErrorCode      string  `json:"error_code,omitempty"`
	HarnessError   string  `json:"harness_error,omitempty"`
	Attempts       int     `json:"attempts"`
	DurationMS     int64   `json:"duration_ms"`
	Usage          *Usage  `json:"usage"`
	JudgePolicy    string  `json:"judge_policy,omitempty"`
	Verification   string  `json:"verification,omitempty"`
	JudgeCalls     *int    `json:"judge_calls"` // null when not known (a run over HTTP)
	Decision       string  `json:"decision,omitempty"`
	Disagreements  int     `json:"disagreements,omitempty"`
	Fields         Fields  `json:"fields"`
	FieldAccuracy  float64 `json:"field_accuracy"`
	Correct        bool    `json:"correct"`      // a result and every compared field matched
	FalseAccept    bool    `json:"false_accept"` // accepted, but a compared field differs (by judge or grounding: Verification)
	FalseReject    bool    `json:"false_reject"` // rejected, but every compared field matched
	ServiceVersion string  `json:"service_version,omitempty"`
}

// Run runs the cases with at most concurrency at a time (at least 1) and returns their
// results in the order of cases. progress, when set, is called once per finished case,
// from one goroutine at a time.
func Run(ctx context.Context, cases []Case, concurrency int, run Runner, progress func(done int, r CaseResult)) []CaseResult {
	concurrency = max(concurrency, 1)
	results := make([]CaseResult, len(cases))
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		done int
	)
	sem := make(chan struct{}, concurrency)
	for i, c := range cases {
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			r := runCase(ctx, c, run)
			results[i] = r
			if progress != nil {
				mu.Lock()
				done++
				progress(done, r)
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	return results
}

func runCase(ctx context.Context, c Case, run Runner) CaseResult {
	start := time.Now()
	out, err := run(ctx, c)
	r := CaseResult{ID: c.ID, DurationMS: time.Since(start).Milliseconds()}
	if err != nil {
		r.Status = StatusError
		r.HarnessError = err.Error()
		out = Outcome{}
	} else {
		r.Status, r.ErrorCode, r.Attempts = out.Status, out.ErrorCode, out.Attempts
		r.Usage, r.ServiceVersion = out.Usage, out.ServiceVersion
		r.JudgePolicy, r.Verification, r.JudgeCalls = out.JudgePolicy, out.Verification, out.JudgeCalls
		r.Decision, r.Disagreements = out.Decision, out.Disagreements
	}
	return Score(r, c, out.Result)
}

// Score fills r's field comparison and verdict flags from result against c's ground
// truth. A ground truth that cannot be compared leaves r a harness error.
func Score(r CaseResult, c Case, result json.RawMessage) CaseResult {
	f, err := c.Rules.Compare(c.Expected, result)
	if err != nil {
		r.Status, r.HarnessError = StatusError, err.Error()
	}
	r.Fields = f
	r.FieldAccuracy = f.Accuracy()
	r.Correct = r.Status != StatusError && f.Correct()
	// An extracted case is neither accepted nor rejected: it is never a false accept or a
	// false reject, only its field accuracy counts.
	r.FalseAccept = r.Status == string(extract.StatusAccepted) && !r.Correct
	r.FalseReject = r.Status == string(extract.StatusRejected) && r.Correct
	return r
}
