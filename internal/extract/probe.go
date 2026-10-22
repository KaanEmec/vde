package extract

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"time"

	"github.com/kaanemec/vde/internal/document"
	"github.com/kaanemec/vde/internal/provider"
)

// The probe's synthetic input (vde config check --probe): the same tiny document, schema
// and instructions for every pair, never a caller document. The judge's candidate has one
// deliberately wrong value, at probeWrongPointer.
const (
	probeDocument = "INVOICE INV-2041\n" +
		"Issued: 14 March 2026\n" +
		"Seller: Example Supplies Ltd, 1 Sample Street, Testville\n" +
		"4 boxes of copy paper at 12.50 EUR each\n" +
		"Total due: 50.00 EUR\n"
	probeSchema = `{"type": "object", "properties": {` +
		`"invoice_number": {"type": "string"}, ` +
		`"issued": {"type": "string", "format": "date"}, ` +
		`"total": {"type": "number"}, ` +
		`"currency": {"type": "string"}}, ` +
		`"required": ["invoice_number", "issued", "total", "currency"], "additionalProperties": false}`
	probeExtractorInstructions = "Extract the invoice number, the issue date as YYYY-MM-DD, " +
		"the total due as a number and its ISO 4217 currency code."
	probeJudgeInstructions = "Check the invoice number, the issue date (YYYY-MM-DD), " +
		"the total due and its ISO 4217 currency code against the document."
	probeCandidate    = `{"invoice_number": "INV-2041", "issued": "2026-03-14", "total": 80.00, "currency": "EUR"}`
	probeWrongPointer = "/total"
)

// ProbeStatus is the outcome of one probe call.
type ProbeStatus string

// Probe outcomes. ProbeUnparseable and ProbeFailed fail the probe; the two judge
// warnings do not: the answer parsed, but the model missed the deliberate error.
const (
	ProbeOK               ProbeStatus = "ok"
	ProbeUnparseable      ProbeStatus = "unparseable"
	ProbeFailed           ProbeStatus = "failed" // the provider call failed; Err is its error
	ProbeAcceptsWrong     ProbeStatus = "accepts a wrong candidate"
	ProbeRejectsElsewhere ProbeStatus = "rejects, but without feedback at " + probeWrongPointer
)

// ProbeResult is one probe call's outcome. Nothing in it carries the answer text: Err is
// the parser's error (a position, no content) or the provider's.
type ProbeResult struct {
	Status       ProbeStatus
	Err          error
	Latency      time.Duration
	InputTokens  int
	OutputTokens int
	Truncated    bool // the answer stopped at the output-token limit
}

// OK reports whether the result passes: ok, or one of the judge warnings.
func (r ProbeResult) OK() bool { return r.Status != ProbeUnparseable && r.Status != ProbeFailed }

var probeInput = sync.OnceValue(func() *Input {
	schema, err := CompileSchema([]byte(probeSchema))
	if err != nil {
		panic(err) // a constant; TestProbe compiles it
	}
	return &Input{
		Representation: document.Representation{
			Kind:       document.Text,
			Parts:      []provider.Part{{Text: probeDocument}},
			MediaTypes: []string{"text/plain"},
		},
		ExtractorInstructions: probeExtractorInstructions,
		JudgeInstructions:     probeJudgeInstructions,
		Schema:                schema,
	}
})

// Probe sends one request in role ("judge", else the extractor) to model on p, through
// the role's real framing, request assembly and answer parser, with the embedded
// synthetic sample, and reports whether the answer parses. The judge is given a candidate
// with one wrong value; it passes cleanly only by rejecting it with feedback at that
// value. One try, no failover: ctx bounds the call. maxTokens is the pair's output limit.
func Probe(ctx context.Context, role string, p provider.Provider, model string, maxTokens int) ProbeResult {
	in := probeInput()
	extractorSystem, judgeSystem := framings(false)
	req := provider.Request{Model: model, MaxTokens: maxTokens, System: extractorSystem, Parts: assembleExtractor(in, nil, nil)}
	judge := role == "judge"
	if judge {
		req.System, req.Parts = judgeSystem, assembleJudge(in, json.RawMessage(probeCandidate), false)
	}
	start := time.Now()
	resp, err := p.Complete(ctx, req)
	r := ProbeResult{
		Latency: time.Since(start), InputTokens: resp.InputTokens, OutputTokens: resp.OutputTokens,
		Truncated: resp.Truncated,
	}
	if err != nil {
		r.Status, r.Err = ProbeFailed, err
		return r
	}
	if !judge {
		if _, err := parseJSON(resp.Text, in.Schema.allowArray); err != nil {
			r.Status, r.Err = ProbeUnparseable, err
			return r
		}
		r.Status = ProbeOK
		return r
	}
	v, err := parseVerdict(resp.Text, DefaultFeedbackMessageChars)
	switch {
	case err != nil:
		r.Status, r.Err = ProbeUnparseable, err
	case v.accept:
		r.Status = ProbeAcceptsWrong
	case !slices.ContainsFunc(v.feedback, func(f Feedback) bool { return f.Pointer == probeWrongPointer || f.Pointer == "" }):
		r.Status = ProbeRejectsElsewhere
	default:
		r.Status = ProbeOK
	}
	return r
}
