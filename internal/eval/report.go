package eval

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"
	"time"
)

// Report is one evaluation run, saved as JSON. It carries case ids, statuses, counts and
// JSON pointers only, never document content or result values.
type Report struct {
	ServiceVersion string       `json:"service_version"`
	Extractor      []string     `json:"extractor,omitempty"` // endpoint/model pairs, local runs
	Judge          []string     `json:"judge,omitempty"`
	Target         string       `json:"target"` // "local" or the service URL
	Corpus         string       `json:"corpus"`
	Cases          string       `json:"cases"` // the case id pattern
	Concurrency    int          `json:"concurrency"`
	StartedAt      time.Time    `json:"started_at"`
	FinishedAt     time.Time    `json:"finished_at"`
	Summary        Summary      `json:"summary"`
	Results        []CaseResult `json:"results"`
}

// Summary aggregates a run. Rates are fractions from 0 to 1.
type Summary struct {
	Cases    int `json:"cases"`
	Accepted int `json:"accepted"`
	Rejected int `json:"rejected"`
	Failed   int `json:"failed"`
	Errors   int `json:"errors"` // cases the harness could not run
	Correct  int `json:"correct"`
	// AcceptanceRate is accepted / cases.
	AcceptanceRate float64 `json:"acceptance_rate"`
	// FalseAccepts are accepted cases with a compared field that differs from the ground
	// truth: the headline number, the judge exists to keep it at zero.
	FalseAccepts int `json:"false_accepts"`
	// FalseAcceptRate is false accepts / accepted (0 when nothing was accepted).
	FalseAcceptRate float64 `json:"false_accept_rate"`
	// FalseRejects are rejected cases whose result matched on every compared field.
	FalseRejects int `json:"false_rejects"`
	// FalseRejectRate is false rejects / rejected (0 when nothing was rejected).
	FalseRejectRate float64 `json:"false_reject_rate"`
	// FieldAccuracy is matched / compared fields over every case (a case without a
	// result matches none); AcceptedFieldAccuracy over accepted cases only.
	FieldAccuracy         float64 `json:"field_accuracy"`
	AcceptedFieldAccuracy float64 `json:"accepted_field_accuracy"`
	MeanAttempts          float64 `json:"mean_attempts"`
	DurationMedianMS      int64   `json:"duration_median_ms"`
	DurationP95MS         int64   `json:"duration_p95_ms"`
	// Usage sums the cases whose usage is known (UsageCases of them).
	UsageCases   int `json:"usage_cases"`
	Calls        int `json:"calls"`
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	// CostUSD is a placeholder for a cost estimate from the token counts; null until
	// per-model prices are configured.
	CostUSD *float64 `json:"cost_usd"`
}

// Summarize aggregates per-case results.
func Summarize(results []CaseResult) Summary {
	s := Summary{Cases: len(results)}
	var matched, total, accMatched, accTotal, attempts int
	durations := make([]int64, 0, len(results))
	for _, r := range results {
		switch r.Status {
		case StatusAccepted:
			s.Accepted++
			accMatched += r.Fields.Matched
			accTotal += r.Fields.Total
		case StatusRejected:
			s.Rejected++
		case StatusFailed:
			s.Failed++
		default:
			s.Errors++
		}
		if r.Correct {
			s.Correct++
		}
		if r.FalseAccept {
			s.FalseAccepts++
		}
		if r.FalseReject {
			s.FalseRejects++
		}
		matched += r.Fields.Matched
		total += r.Fields.Total
		attempts += r.Attempts
		durations = append(durations, r.DurationMS)
		if r.Usage != nil {
			s.UsageCases++
			s.Calls += r.Usage.Calls
			s.InputTokens += r.Usage.InputTokens
			s.OutputTokens += r.Usage.OutputTokens
		}
	}
	s.AcceptanceRate = ratio(s.Accepted, s.Cases)
	s.FalseAcceptRate = ratio(s.FalseAccepts, s.Accepted)
	s.FalseRejectRate = ratio(s.FalseRejects, s.Rejected)
	s.FieldAccuracy = ratio(matched, total)
	s.AcceptedFieldAccuracy = ratio(accMatched, accTotal)
	if s.Cases > 0 {
		s.MeanAttempts = float64(attempts) / float64(s.Cases)
	}
	s.DurationMedianMS, s.DurationP95MS = median(durations), percentile(durations, 0.95)
	return s
}

func ratio(n, d int) float64 {
	if d == 0 {
		return 0
	}
	return float64(n) / float64(d)
}

func median(v []int64) int64 {
	if len(v) == 0 {
		return 0
	}
	s := slices.Sorted(slices.Values(v))
	if n := len(s); n%2 == 1 {
		return s[n/2]
	}
	return (s[len(s)/2-1] + s[len(s)/2]) / 2
}

// percentile is the nearest-rank percentile p (0 < p <= 1).
func percentile(v []int64, p float64) int64 {
	if len(v) == 0 {
		return 0
	}
	s := slices.Sorted(slices.Values(v))
	i := int(math.Ceil(p*float64(len(s)))) - 1
	return s[min(max(i, 0), len(s)-1)]
}

// Save writes the report as <dir>/<service_version>-<date>.json (date of StartedAt, UTC),
// adding -2, -3, ... when that file exists, and returns the path.
func (r Report) Save(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", err
	}
	data = append(data, '\n')
	base := fileSafe(r.ServiceVersion) + "-" + r.StartedAt.UTC().Format(time.DateOnly)
	for n := 1; ; n++ {
		name := base + ".json"
		if n > 1 {
			name = fmt.Sprintf("%s-%d.json", base, n)
		}
		p := filepath.Join(dir, name)
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		_, werr := f.Write(data)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		return p, werr
	}
}

func fileSafe(s string) string {
	if s == "" {
		return "unknown"
	}
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_', r == '+':
			return r
		}
		return '_'
	}, s)
}

// LoadReport reads a saved report.
func LoadReport(path string) (Report, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Report{}, err
	}
	var r Report
	if err := json.Unmarshal(data, &r); err != nil {
		return Report{}, fmt.Errorf("%s: %w", path, err)
	}
	return r, nil
}

// WriteTable writes the per-case table and the summary for humans.
func (r Report) WriteTable(w io.Writer) {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "CASE\tSTATUS\tATTEMPTS\tDURATION\tTOKENS\tFIELDS\tVERDICT")
	for _, c := range r.Results {
		status := c.Status
		if c.ErrorCode != "" {
			status += " (" + c.ErrorCode + ")"
		}
		tokens := "-"
		if c.Usage != nil {
			tokens = fmt.Sprint(c.Usage.InputTokens + c.Usage.OutputTokens)
		}
		fields := fmt.Sprintf("%d/%d", c.Fields.Matched, c.Fields.Total)
		if c.Fields.NoResult {
			fields = "no result"
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\t%s\t%s\n", c.ID, status, c.Attempts,
			time.Duration(c.DurationMS)*time.Millisecond, tokens, fields, verdict(c))
	}
	tw.Flush()

	s := r.Summary
	fmt.Fprintf(w, "\nservice_version %s, %s, %d case(s)\n", r.ServiceVersion, r.Target, s.Cases)
	if len(r.Extractor) > 0 {
		fmt.Fprintf(w, "extractor %s; judge %s\n", strings.Join(r.Extractor, ", "), strings.Join(r.Judge, ", "))
	}
	fmt.Fprintf(w, "false accepts      %d of %d accepted (%s)\n", s.FalseAccepts, s.Accepted, pct(s.FalseAcceptRate))
	fmt.Fprintf(w, "acceptance rate    %s (%d accepted, %d rejected, %d failed, %d harness errors)\n",
		pct(s.AcceptanceRate), s.Accepted, s.Rejected, s.Failed, s.Errors)
	fmt.Fprintf(w, "field accuracy     %s overall, %s on accepted\n", pct(s.FieldAccuracy), pct(s.AcceptedFieldAccuracy))
	fmt.Fprintf(w, "false rejects      %d of %d rejected (%s)\n", s.FalseRejects, s.Rejected, pct(s.FalseRejectRate))
	fmt.Fprintf(w, "correct cases      %d of %d\n", s.Correct, s.Cases)
	fmt.Fprintf(w, "attempts           %.2f mean\n", s.MeanAttempts)
	fmt.Fprintf(w, "duration           median %s, p95 %s\n", ms(s.DurationMedianMS), ms(s.DurationP95MS))
	if s.UsageCases > 0 {
		fmt.Fprintf(w, "tokens             %d input, %d output in %d call(s) (cost estimate: not configured)\n",
			s.InputTokens, s.OutputTokens, s.Calls)
	} else {
		fmt.Fprintln(w, "tokens             not reported")
	}
}

func verdict(c CaseResult) string {
	switch {
	case c.FalseAccept:
		return "FALSE ACCEPT"
	case c.FalseReject:
		return "false reject"
	case c.Correct:
		return "correct"
	}
	return "-"
}

func pct(f float64) string { return fmt.Sprintf("%.1f%%", 100*f) }

func ms(v int64) string { return (time.Duration(v) * time.Millisecond).String() }

// WriteDiff writes how r differs from prev: the summary deltas and every case whose
// status or correctness changed, or that is in only one of the two.
func (r Report) WriteDiff(w io.Writer, prev Report) {
	a, b := prev.Summary, r.Summary
	fmt.Fprintf(w, "\ncompared with %s (%s)\n", prev.ServiceVersion, prev.StartedAt.UTC().Format(time.DateOnly))
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "METRIC\tBEFORE\tNOW\tCHANGE")
	rate := func(name string, x, y float64) {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%+.1f pt\n", name, pct(x), pct(y), 100*(y-x))
	}
	count := func(name string, x, y int) { fmt.Fprintf(tw, "%s\t%d\t%d\t%+d\n", name, x, y, y-x) }
	count("false accepts", a.FalseAccepts, b.FalseAccepts)
	rate("false accept rate", a.FalseAcceptRate, b.FalseAcceptRate)
	rate("acceptance rate", a.AcceptanceRate, b.AcceptanceRate)
	rate("field accuracy", a.FieldAccuracy, b.FieldAccuracy)
	count("false rejects", a.FalseRejects, b.FalseRejects)
	count("correct cases", a.Correct, b.Correct)
	delta := ms(b.DurationMedianMS - a.DurationMedianMS)
	if b.DurationMedianMS >= a.DurationMedianMS {
		delta = "+" + delta
	}
	fmt.Fprintf(tw, "median duration\t%s\t%s\t%s\n", ms(a.DurationMedianMS), ms(b.DurationMedianMS), delta)
	count("input tokens", a.InputTokens, b.InputTokens)
	count("output tokens", a.OutputTokens, b.OutputTokens)
	tw.Flush()

	before := make(map[string]CaseResult, len(prev.Results))
	for _, c := range prev.Results {
		before[c.ID] = c
	}
	var changes []string
	seen := make(map[string]bool, len(r.Results))
	for _, c := range r.Results {
		seen[c.ID] = true
		p, ok := before[c.ID]
		switch {
		case !ok:
			changes = append(changes, fmt.Sprintf("  %s: new, %s %s", c.ID, c.Status, verdict(c)))
		case p.Status != c.Status || verdict(p) != verdict(c):
			changes = append(changes, fmt.Sprintf("  %s: %s %s -> %s %s", c.ID, p.Status, verdict(p), c.Status, verdict(c)))
		}
	}
	for _, p := range prev.Results {
		if !seen[p.ID] {
			changes = append(changes, fmt.Sprintf("  %s: not in this run", p.ID))
		}
	}
	if len(changes) == 0 {
		fmt.Fprintln(w, "no case changed status or verdict")
		return
	}
	fmt.Fprintf(w, "%d case(s) changed:\n%s\n", len(changes), strings.Join(changes, "\n"))
}
