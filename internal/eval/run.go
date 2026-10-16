package eval

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

// Terminal job statuses as a Runner reports them, and the status of a case the harness
// could not run.
const (
	StatusAccepted = "accepted"
	StatusRejected = "rejected"
	StatusFailed   = "failed"
	StatusError    = "error"
)

// Usage is the model usage of one case.
type Usage struct {
	Calls        int `json:"calls"`
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// Outcome is what running one case produced: the job's terminal state.
type Outcome struct {
	Status         string          // accepted, rejected or failed
	Result         json.RawMessage // the final candidate, nil when none
	Attempts       int
	ErrorCode      string // the job error code when failed
	ServiceVersion string
	Usage          *Usage // nil when not known (a run over HTTP)
}

// Runner runs one case to its terminal state. An error means the harness could not run
// the case at all (an unreadable file, an unsupported document, a transport failure); a
// failed job is an Outcome with StatusFailed.
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
	Fields         Fields  `json:"fields"`
	FieldAccuracy  float64 `json:"field_accuracy"`
	Correct        bool    `json:"correct"`      // a result and every compared field matched
	FalseAccept    bool    `json:"false_accept"` // accepted, but a compared field differs
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
	r.FalseAccept = r.Status == StatusAccepted && !r.Correct
	r.FalseReject = r.Status == StatusRejected && r.Correct
	return r
}
