package extract

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/document"
	"github.com/kaanemec/vde/internal/provider"
	"github.com/kaanemec/vde/internal/provider/fake"
)

// loopConfig: the extractor group fails over from primary to fallback; the judge group
// has one endpoint and one try.
const loopConfig = `
endpoints:
  primary:
    provider: fake
    models: {primary-model: {accepts: [text/plain, application/pdf, image/png]}}
  fallback:
    provider: fake
    models: {fallback-model: {accepts: [text/plain, application/pdf, image/png]}}
  review:
    provider: fake
    models: {judge-model: {accepts: [text/plain, application/pdf, image/png]}}
groups:
  extraction:
    endpoints: [{endpoint: primary, model: primary-model}, {endpoint: fallback, model: fallback-model}]
    failover: {max_tries: 2, max_total_time: 1m}
  review:
    endpoints: [{endpoint: review, model: judge-model}]
    failover: {max_tries: 1, max_total_time: 1m}
roles:
  extractor: {group: extraction}
  judge: {group: review}
limits: {max_attempts: 3, job_timeout: 5m, judge_reparse_tries: 1}
`

const (
	personSchema = `{"type": "object", "required": ["name"], "additionalProperties": false,
		"properties": {"name": {"type": "string"}, "born": {"type": "integer"}}}`
	// annotatedPersonSchema is personSchema with a grounding annotation, so the repair
	// runs under always too (ADR 0009, amendment 2026-10-18).
	annotatedPersonSchema = `{"type": "object", "required": ["name"], "additionalProperties": false,
		"properties": {"name": {"type": "string"}, "born": {"type": "integer", "x-vde-grounding": "critical"}}}`
	valid       = `{"name": "Ada Lovelace", "born": 1815}`
	valid2      = `{"name": "Ada King", "born": 1815}`
	invalid     = `{"name": "Ada Lovelace", "born": "1815"}`
	reject      = `{"verdict": "reject", "feedback": {"/name": "The document prints Ada King."}}`
	accept      = fake.AcceptVerdict
	notJSON     = "I am sorry, I cannot read this document."
	garbage     = `{"verdict": "maybe"}`
	pdfContents = "%PDF-1.7 not really a pdf"
)

func loadConfig(t *testing.T, yaml string) *config.Config {
	t.Helper()
	cfg, err := config.Parse([]byte(yaml), func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate().Err(false); err != nil {
		t.Fatal(err)
	}
	return cfg
}

// rig is a configured loop whose endpoints are fakes.
type rig struct {
	primary, fallback, judge *fake.Provider
	roles                    Roles
}

func newRig(t *testing.T, primary, fallback, judge fake.Script) *rig {
	t.Helper()
	r := &rig{primary: fake.New(primary), fallback: fake.New(fallback), judge: fake.New(judge)}
	cfg := loadConfig(t, loopConfig)
	r.roles = buildRoles(t, cfg, map[string]provider.Provider{"primary": r.primary, "fallback": r.fallback, "review": r.judge})
	return r
}

func buildRoles(t *testing.T, cfg *config.Config, providers map[string]provider.Provider) Roles {
	t.Helper()
	byEndpoint := map[*config.Endpoint]provider.Provider{}
	for name, p := range providers {
		byEndpoint[cfg.Endpoints[name]] = p
	}
	roles, err := NewRoles(cfg, func(e *config.Endpoint) (provider.Provider, error) {
		if p, ok := byEndpoint[e]; ok {
			return p, nil
		}
		return nil, errors.New("no provider")
	})
	if err != nil {
		t.Fatal(err)
	}
	return roles
}

// extractorCalls returns the calls the extractor group made, primary then fallback.
func (r *rig) extractorCalls() []fake.Call {
	return append(r.primary.Calls(), r.fallback.Calls()...)
}

func pdfRepresentation() document.Representation {
	return document.Representation{
		Kind:       document.Native,
		Parts:      []provider.Part{{Document: &provider.Blob{MediaType: "application/pdf", Data: []byte(pdfContents)}}},
		MediaTypes: []string{"application/pdf"},
	}
}

func input(t *testing.T, schema string) Input {
	t.Helper()
	in := Input{
		Representation:        pdfRepresentation(),
		ExtractorInstructions: "Extract the person's name and year of birth.\n",
		JudgeInstructions:     "Check the name and the year against the document.\n",
		MaxAttempts:           3,
	}
	if schema != "" {
		s, err := CompileSchema([]byte(schema))
		if err != nil {
			t.Fatal(err)
		}
		in.Schema = s
	}
	return in
}

// documentParts returns the parts of a call that follow the Document: separator.
func documentParts(t *testing.T, c fake.Call, n int) []provider.Part {
	t.Helper()
	for i, p := range c.Request.Parts {
		if p.Text == sepDocument && i+n < len(c.Request.Parts) {
			return c.Request.Parts[i+1 : i+1+n]
		}
	}
	t.Fatalf("no document section in %+v", c.Parts)
	return nil
}

// checkCalls asserts the role call counts and that every extractor and judge call saw
// the representation's document parts byte for byte.
func checkCalls(t *testing.T, in Input, extractor, judge []fake.Call, wantExtractor, wantJudge int) {
	t.Helper()
	if len(extractor) != wantExtractor || len(judge) != wantJudge {
		t.Fatalf("calls: extractor %d, judge %d; want %d, %d", len(extractor), len(judge), wantExtractor, wantJudge)
	}
	want := in.Representation.Parts
	for _, c := range append(append([]fake.Call{}, extractor...), judge...) {
		for i, p := range documentParts(t, c, len(want)) {
			w := want[i]
			if p.Text != w.Text || !sameBlob(p.Document, w.Document) || !sameBlob(p.Image, w.Image) {
				t.Fatalf("document part %d differs from the representation", i)
			}
		}
	}
}

func sameBlob(a, b *provider.Blob) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.MediaType == b.MediaType && bytes.Equal(a.Data, b.Data)
}

// checkOutcome asserts status, attempts and error code, and the acceptance invariant.
func checkOutcome(t *testing.T, in Input, out Outcome, status Status, attempts int, code string) {
	t.Helper()
	if out.Status != status || out.Attempts != attempts {
		t.Fatalf("outcome = %s after %d attempts (%+v), want %s after %d", out.Status, out.Attempts, out.Error, status, attempts)
	}
	gotCode := ""
	if out.Error != nil {
		gotCode = out.Error.Code
	}
	if gotCode != code {
		t.Fatalf("error = %+v, want code %q", out.Error, code)
	}
	if out.Feedback == nil {
		t.Error("feedback is nil, want an empty list")
	}
	checkAcceptance(t, in, out)
}

// checkAcceptance: accepted means a result, no error and, with a schema, a valid result.
func checkAcceptance(t *testing.T, in Input, out Outcome) {
	t.Helper()
	if out.Status != StatusAccepted {
		return
	}
	if out.Error != nil || out.Result == nil {
		t.Fatalf("accepted with error %+v, result %s", out.Error, out.Result)
	}
	if in.Schema != nil && in.Schema.Validate(out.Result) != nil {
		t.Fatalf("accepted an invalid result %s", out.Result)
	}
}

func sameJSON(t *testing.T, got json.RawMessage, want string) {
	t.Helper()
	if string(got) != want {
		t.Errorf("result = %s, want %s", got, want)
	}
}

func hasText(c fake.Call, substr string) bool {
	for _, p := range c.Request.Parts {
		if strings.Contains(p.Text, substr) {
			return true
		}
	}
	return false
}

func TestRun_acceptFirstTry(t *testing.T) {
	r := newRig(t, fake.Script{fake.Answer(valid)}, nil, fake.Script{fake.Answer(accept)})
	in := input(t, personSchema)
	out := Run(t.Context(), in, r.roles)
	checkOutcome(t, in, out, StatusAccepted, 1, "")
	checkCalls(t, in, r.extractorCalls(), r.judge.Calls(), 1, 1)
	sameJSON(t, out.Result, valid)
	if len(out.Feedback) != 0 || out.JudgeConfidence != nil {
		t.Errorf("feedback %+v, confidence %v; want none", out.Feedback, out.JudgeConfidence)
	}
	ext, jdg := r.primary.Calls()[0].Request, r.judge.Calls()[0].Request
	if ext.Model != "primary-model" || jdg.Model != "judge-model" {
		t.Errorf("models: extractor %q, judge %q", ext.Model, jdg.Model)
	}
	wantExt, wantJudge := framings(false)
	if ext.System != wantExt || jdg.System != wantJudge {
		t.Error("roles were not given their framings")
	}
	if !hasText(r.judge.Calls()[0], sepCandidate+"\n\n"+valid) || !hasText(r.judge.Calls()[0], personSchema) {
		t.Error("the judge did not see the candidate and the schema")
	}
}

// TestRun_sharedGroup: one group and one fake serve both roles, told apart by framing.
func TestRun_sharedGroup(t *testing.T) {
	cfg := loadConfig(t, strings.Replace(loopConfig, "judge: {group: review}", "judge: {group: extraction}", 1))
	p := fake.New(fake.Script{
		{Role: "judge", Answer: accept},
		{Role: "extractor", Answer: valid},
	})
	roles := buildRoles(t, cfg, map[string]provider.Provider{"primary": p, "fallback": p, "review": p})
	if roles.Extractor != roles.Judge {
		t.Fatal("roles naming one group do not share it")
	}
	in := input(t, personSchema)
	out := Run(t.Context(), in, roles)
	checkOutcome(t, in, out, StatusAccepted, 1, "")
	calls := p.Calls()
	checkCalls(t, in, calls[:1], calls[1:], 1, 1)
}

func TestRun_schemaViolationWithoutJudge(t *testing.T) {
	r := newRig(t, fake.Script{fake.Answer(invalid), fake.Answer(valid)}, nil, fake.Script{fake.Answer(accept)})
	in := input(t, personSchema)
	var progress []Progress
	in.OnAttempt = func(p Progress) { progress = append(progress, p) }
	out := Run(t.Context(), in, r.roles)
	checkOutcome(t, in, out, StatusAccepted, 2, "")
	checkCalls(t, in, r.extractorCalls(), r.judge.Calls(), 2, 1)
	retry := r.primary.Calls()[1]
	if !hasText(retry, sepPrevious+"\n\n"+invalid) || !hasText(retry, `"/born" (schema): `+"`born`: got string, want integer (keyword type)") {
		t.Errorf("retry did not carry the candidate and its schema feedback: %+v", retry.Request.Parts)
	}
	if len(progress) != 1 || progress[0].Attempts != 1 || string(progress[0].Result) != invalid ||
		len(progress[0].Feedback) != 1 || progress[0].Feedback[0].Source != SourceSchema {
		t.Errorf("progress = %+v", progress)
	}
}

func TestRun_rejectRepaired(t *testing.T) {
	r := newRig(t, fake.Script{fake.Answer(valid), fake.Answer(valid2)}, nil, fake.Script{fake.Answer(reject), fake.Answer(accept)})
	in := input(t, personSchema)
	out := Run(t.Context(), in, r.roles)
	checkOutcome(t, in, out, StatusAccepted, 2, "")
	checkCalls(t, in, r.extractorCalls(), r.judge.Calls(), 2, 2)
	sameJSON(t, out.Result, valid2)
	retry := r.primary.Calls()[1]
	if !hasText(retry, sepPrevious+"\n\n"+valid) || !hasText(retry, `- "/name" (judge): The document prints Ada King.`) {
		t.Errorf("retry did not carry the judge's feedback: %+v", retry.Request.Parts)
	}
}

func TestRun_attemptsExhausted(t *testing.T) {
	last := `{"verdict": "reject", "feedback": {"/born": "The year is not printed.", "": "Check again."}, "confidence": 0.8}`
	r := newRig(t,
		fake.Script{fake.Answer(valid), fake.Answer(invalid), fake.Answer(valid2)}, nil,
		fake.Script{fake.Answer(reject), fake.Answer(last)})
	in := input(t, personSchema)
	out := Run(t.Context(), in, r.roles)
	checkOutcome(t, in, out, StatusRejected, 3, "")
	checkCalls(t, in, r.extractorCalls(), r.judge.Calls(), 3, 2)
	sameJSON(t, out.Result, valid2)
	want := []Feedback{
		{Pointer: "/born", Message: "The year is not printed.", Source: SourceJudge},
		{Pointer: "", Message: "Check again.", Source: SourceJudge},
	}
	if !equalFeedback(out.Feedback, want) {
		t.Errorf("feedback = %+v, want %+v", out.Feedback, want)
	}
	if out.JudgeConfidence == nil || *out.JudgeConfidence != 0.8 {
		t.Errorf("confidence = %v, want 0.8", out.JudgeConfidence)
	}
}

func equalFeedback(a, b []Feedback) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestRun_rejectedOnSchemaFeedback(t *testing.T) {
	r := newRig(t, fake.Script{fake.Answer(invalid), fake.Answer(notJSON)}, nil, nil)
	in := input(t, personSchema)
	in.MaxAttempts = 2
	out := Run(t.Context(), in, r.roles)
	checkOutcome(t, in, out, StatusRejected, 2, "")
	checkCalls(t, in, r.extractorCalls(), r.judge.Calls(), 2, 0)
	// The last parseable candidate, with the feedback on it rather than on the answer
	// that was not JSON.
	sameJSON(t, out.Result, invalid)
	if len(out.Feedback) != 1 || out.Feedback[0].Pointer != "/born" {
		t.Errorf("feedback = %+v, want the schema violation of the result", out.Feedback)
	}
}

func TestRun_extractorGroupDown(t *testing.T) {
	r := newRig(t, fake.AlwaysRetryable(503), fake.AlwaysRetryable(502), nil)
	in := input(t, personSchema)
	out := Run(t.Context(), in, r.roles)
	checkOutcome(t, in, out, StatusFailed, 1, CodeProviderUnavailable)
	checkCalls(t, in, r.extractorCalls(), r.judge.Calls(), 2, 0)
	if out.Result != nil {
		t.Errorf("result = %s, want none", out.Result)
	}
}

func TestRun_deadline(t *testing.T) {
	slow := fake.Answer(valid2)
	slow.Delay = 10 * time.Second
	r := newRig(t, fake.Script{fake.Answer(valid), slow}, nil, fake.Script{fake.Answer(reject)})
	in := input(t, personSchema)
	in.Deadline = time.Now().Add(300 * time.Millisecond)
	start := time.Now()
	out := Run(t.Context(), in, r.roles)
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("Run took %s past the deadline", elapsed)
	}
	checkOutcome(t, in, out, StatusFailed, 2, CodeTimeout)
	checkCalls(t, in, r.extractorCalls(), r.judge.Calls(), 2, 1)
	// The candidate and feedback produced so far are kept.
	sameJSON(t, out.Result, valid)
	if len(out.Feedback) != 1 || out.Feedback[0].Source != SourceJudge {
		t.Errorf("feedback = %+v, want the judge's", out.Feedback)
	}
}

func TestRun_deadlineDuringJudge(t *testing.T) {
	slow := fake.Answer(accept)
	slow.Delay = 10 * time.Second
	r := newRig(t, fake.Script{fake.Answer(valid)}, nil, fake.Script{slow})
	in := input(t, personSchema)
	in.Deadline = time.Now().Add(200 * time.Millisecond)
	out := Run(t.Context(), in, r.roles)
	checkOutcome(t, in, out, StatusFailed, 1, CodeTimeout)
	sameJSON(t, out.Result, valid)
}

func TestRun_deadlinePassed(t *testing.T) {
	r := newRig(t, fake.Script{fake.Answer(valid)}, nil, nil)
	in := input(t, personSchema)
	in.Deadline = time.Now().Add(-time.Second)
	out := Run(t.Context(), in, r.roles)
	checkOutcome(t, in, out, StatusFailed, 0, CodeTimeout)
	checkCalls(t, in, r.extractorCalls(), r.judge.Calls(), 0, 0)
}

func TestRun_cancelled(t *testing.T) {
	r := newRig(t, fake.Script{fake.Answer(valid)}, nil, nil)
	in := input(t, personSchema)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	out := Run(ctx, in, r.roles)
	checkOutcome(t, in, out, StatusFailed, 0, CodeTimeout)
}

func TestRun_judgeUnparseable(t *testing.T) {
	tests := []struct {
		name      string
		judge     fake.Script
		status    Status
		code      string
		wantCalls int
	}{
		{"garbage twice", fake.Script{fake.Answer(garbage), fake.Answer("not even JSON")}, StatusFailed, CodeJudgeFailed, 2},
		{"reject without entries twice", fake.Script{fake.Answer(`{"verdict": "reject", "feedback": {}}`), fake.Answer(garbage)}, StatusFailed, CodeJudgeFailed, 2},
		{"refusal twice", fake.AlwaysPermanent(400), StatusFailed, CodeJudgeFailed, 2},
		{"garbage then accept", fake.Script{fake.Answer(garbage), fake.Answer(accept)}, StatusAccepted, "", 2},
		{"refusal then accept", fake.Script{fake.Fail(provider.Permanent, 400), fake.Answer(accept)}, StatusAccepted, "", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t, fake.Script{fake.Answer(valid)}, nil, tt.judge)
			in := input(t, personSchema)
			out := Run(t.Context(), in, r.roles)
			checkOutcome(t, in, out, tt.status, 1, tt.code)
			calls := r.judge.Calls()
			checkCalls(t, in, r.extractorCalls(), calls, 1, tt.wantCalls)
			sameJSON(t, out.Result, valid)
			if hasText(calls[0], reaskReminder) || !hasText(calls[1], reaskReminder) {
				t.Error("the re-ask must carry the reminder, the first ask must not")
			}
		})
	}
}

func TestRun_judgeReparseTries(t *testing.T) {
	r := newRig(t, fake.Script{fake.Answer(valid)}, nil, fake.Script{fake.Answer(garbage), fake.Answer(garbage), fake.Answer(accept)})
	r.roles.JudgeReparseTries = 2
	in := input(t, personSchema)
	out := Run(t.Context(), in, r.roles)
	checkOutcome(t, in, out, StatusAccepted, 1, "")
	checkCalls(t, in, r.extractorCalls(), r.judge.Calls(), 1, 3)
}

func TestRun_unparseableThenAccept(t *testing.T) {
	r := newRig(t, fake.Script{fake.Answer(notJSON), fake.Answer("```json\n" + valid + "\n```")}, nil, fake.Script{fake.Answer(accept)})
	in := input(t, personSchema)
	out := Run(t.Context(), in, r.roles)
	checkOutcome(t, in, out, StatusAccepted, 2, "")
	checkCalls(t, in, r.extractorCalls(), r.judge.Calls(), 2, 1)
	sameJSON(t, out.Result, valid)
	retry := r.primary.Calls()[1]
	if hasText(retry, sepPrevious) || !hasText(retry, `- "" (schema): The answer was not a single JSON object: no JSON value was found.`) {
		t.Errorf("retry = %+v, want only the parse feedback", retry.Request.Parts)
	}
}

func TestRun_extractorFailed(t *testing.T) {
	tests := []struct {
		name      string
		primary   fake.Script
		wantParse bool // the feedback reports the last answer that was not JSON
	}{
		{"never JSON", fake.Script{fake.Answer(notJSON), fake.Answer(""), fake.Answer(`{"name": `)}, true},
		{"always refused", fake.AlwaysPermanent(400), false},
		{"refused and not JSON", fake.Script{fake.Fail(provider.Permanent, 400), fake.Answer(notJSON), fake.Fail(provider.Permanent, 400)}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t, tt.primary, nil, fake.Script{{Answer: accept, Repeat: true}})
			in := input(t, personSchema)
			var progress int
			in.OnAttempt = func(Progress) { progress++ }
			out := Run(t.Context(), in, r.roles)
			checkOutcome(t, in, out, StatusFailed, 3, CodeExtractorFailed)
			checkCalls(t, in, r.extractorCalls(), r.judge.Calls(), 3, 0)
			if out.Result != nil || progress != 3 {
				t.Errorf("result %s, progress %d; want none and 3", out.Result, progress)
			}
			if got := len(out.Feedback) == 1 && out.Feedback[0].Pointer == ""; got != tt.wantParse {
				t.Errorf("feedback = %+v", out.Feedback)
			}
		})
	}
}

// cutOff is an answer the model stopped writing at its output-token limit.
func cutOff(text string) fake.Step {
	s := fake.Answer(text)
	s.Truncated = true
	return s
}

func TestRun_extractorTruncated(t *testing.T) {
	r := newRig(t, fake.Script{cutOff(`{"name": "Ada Love`), fake.Answer(valid)}, nil, fake.Script{fake.Answer(accept)})
	in := input(t, "")
	out := Run(t.Context(), in, r.roles)
	checkOutcome(t, in, out, StatusAccepted, 2, "")
	checkCalls(t, in, r.extractorCalls(), r.judge.Calls(), 2, 1)
	calls := r.primary.Calls()
	if got, want := calls[0].Request.MaxTokens, config.DefaultMaxOutputTokens; got != want {
		t.Errorf("max tokens = %d, want the configured default %d", got, want)
	}
	if !hasText(calls[1], "cut off at the output-token limit") || !hasText(calls[1], "no prose, no repetition") {
		t.Errorf("retry = %+v, want feedback about the cut-off answer", calls[1].Request.Parts)
	}
	// An answer that is not JSON but not cut off keeps the plain parse feedback.
	r = newRig(t, fake.Script{fake.Answer(notJSON), fake.Answer(valid)}, nil, fake.Script{fake.Answer(accept)})
	Run(t.Context(), in, r.roles)
	if hasText(r.primary.Calls()[1], "cut off") {
		t.Error("feedback on an answer that was not truncated mentions a cut-off")
	}
}

func TestRun_judgeTruncated(t *testing.T) {
	r := newRig(t, fake.Script{fake.Answer(valid)}, nil,
		fake.Script{cutOff(`{"verdict": "reject", "feedback": {"/name": "The docu`), fake.Answer(accept)})
	in := input(t, personSchema)
	out := Run(t.Context(), in, r.roles)
	checkOutcome(t, in, out, StatusAccepted, 1, "")
	calls := r.judge.Calls()
	checkCalls(t, in, r.extractorCalls(), calls, 1, 2)
	if !hasText(calls[1], reaskReminder) {
		t.Error("the re-ask after a truncated verdict must carry the reminder")
	}
}

func TestRun_schemaless(t *testing.T) {
	list := `[{"name": "Ada Lovelace"}, {"name": "Charles Babbage"}]`
	r := newRig(t, fake.Script{fake.Answer("Here you go:\n" + list)}, nil, fake.Script{fake.Answer(accept)})
	in := input(t, "")
	out := Run(t.Context(), in, r.roles)
	checkOutcome(t, in, out, StatusAccepted, 1, "")
	checkCalls(t, in, r.extractorCalls(), r.judge.Calls(), 1, 1)
	sameJSON(t, out.Result, list)
	wantExt, wantJudge := framings(true)
	ext, jdg := r.primary.Calls()[0], r.judge.Calls()[0]
	if ext.Request.System != wantExt || jdg.Request.System != wantJudge {
		t.Error("schema-less mode did not use the schema-less framings")
	}
	if hasText(ext, sepSchema) || hasText(jdg, sepSchema) {
		t.Error("schema-less requests carry a schema section")
	}
}

func TestRun_schemalessReject(t *testing.T) {
	r := newRig(t, fake.Script{fake.Answer(`{"a": 1}`), fake.Answer("[1]")}, nil, fake.Script{fake.Answer(reject), fake.Answer(fake.RejectVerdict)})
	in := input(t, "")
	in.MaxAttempts = 2
	out := Run(t.Context(), in, r.roles)
	checkOutcome(t, in, out, StatusRejected, 2, "")
	sameJSON(t, out.Result, "[1]")
}

// TestRun_schemaArrays: with a schema, the parser takes a top-level array only when the
// schema allows one.
func TestRun_schemaArrays(t *testing.T) {
	r := newRig(t, fake.Script{fake.Answer("[1, 2]")}, nil, fake.Script{fake.Answer(accept)})
	in := input(t, `{"type": "array", "items": {"type": "integer"}}`)
	checkOutcome(t, in, Run(t.Context(), in, r.roles), StatusAccepted, 1, "")

	r = newRig(t, fake.Script{fake.Answer("[1, 2]")}, nil, fake.Script{fake.Answer(accept)})
	in = input(t, personSchema)
	in.MaxAttempts = 1
	checkOutcome(t, in, Run(t.Context(), in, r.roles), StatusFailed, 1, CodeExtractorFailed)
}

func TestRun_confidence(t *testing.T) {
	tests := []struct {
		name    string
		verdict string
		want    *float64
	}{
		{"present", `{"verdict": "accept", "feedback": {}, "confidence": 0.9}`, new(0.9)},
		{"absent", accept, nil},
		{"out of range", `{"verdict": "accept", "feedback": {}, "confidence": 7}`, nil},
		{"zero", `{"verdict": "accept", "feedback": {}, "confidence": 0}`, new(0.0)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t, fake.Script{fake.Answer(valid)}, nil, fake.Script{fake.Answer(tt.verdict)})
			in := input(t, personSchema)
			out := Run(t.Context(), in, r.roles)
			checkOutcome(t, in, out, StatusAccepted, 1, "")
			if (out.JudgeConfidence == nil) != (tt.want == nil) || (tt.want != nil && *out.JudgeConfidence != *tt.want) {
				t.Errorf("confidence = %v, want %v", out.JudgeConfidence, tt.want)
			}
		})
	}
}

func TestRun_advisoryFeedback(t *testing.T) {
	r := newRig(t, fake.Script{fake.Answer(valid)}, nil,
		fake.Script{fake.Answer(`{"verdict": "accept", "feedback": {"/born": "Only the year is printed."}}`)})
	in := input(t, personSchema)
	out := Run(t.Context(), in, r.roles)
	checkOutcome(t, in, out, StatusAccepted, 1, "")
	want := []Feedback{{Pointer: "/born", Message: "Only the year is printed.", Source: SourceJudge}}
	if !equalFeedback(out.Feedback, want) {
		t.Errorf("feedback = %+v, want %+v", out.Feedback, want)
	}
}

func TestRun_feedbackCap(t *testing.T) {
	long := strings.Repeat("x", 100)
	r := newRig(t, fake.Script{fake.Answer(`{"name": "` + long + `", "born": "` + long + `"}`)}, nil, nil)
	r.roles.FeedbackMessageChars = 20
	in := input(t, personSchema)
	in.MaxAttempts = 1
	out := Run(t.Context(), in, r.roles)
	checkOutcome(t, in, out, StatusRejected, 1, "")
	if len(out.Feedback) != 1 || len([]rune(out.Feedback[0].Message)) != 20 || !strings.HasSuffix(out.Feedback[0].Message, "…") {
		t.Errorf("feedback = %+v, want one message of 20 characters", out.Feedback)
	}
}

func TestRun_maxAttemptsDefault(t *testing.T) {
	r := newRig(t, fake.Script{{Answer: invalid, Repeat: true}}, nil, nil)
	in := input(t, personSchema)
	in.MaxAttempts = 0
	out := Run(t.Context(), in, r.roles)
	checkOutcome(t, in, out, StatusRejected, DefaultMaxAttempts, "")
}

// panicky is a provider whose code panics.
type panicky struct{}

func (panicky) Complete(_ context.Context, req provider.Request) (provider.Response, error) {
	return provider.Response{Text: req.Parts[len(req.Parts)].Text}, nil // index out of range
}

func TestRun_panic(t *testing.T) {
	r := newRig(t, fake.Script{fake.Answer(valid)}, nil, nil)
	cfg := loadConfig(t, loopConfig)
	roles := buildRoles(t, cfg, map[string]provider.Provider{"primary": r.primary, "fallback": r.fallback, "review": panicky{}})
	in := input(t, personSchema)
	out := Run(t.Context(), in, roles)
	checkOutcome(t, in, out, StatusFailed, 1, CodeInternal)
	sameJSON(t, out.Result, valid)
	if strings.Contains(out.Error.Message, "index") {
		t.Errorf("error message %q is not generic", out.Error.Message)
	}

	// A panic in the caller's progress callback ends the same way.
	r = newRig(t, fake.Script{fake.Answer(invalid)}, nil, nil)
	in.OnAttempt = func(Progress) { panic("callback") }
	out = Run(t.Context(), in, r.roles)
	checkOutcome(t, in, out, StatusFailed, 1, CodeInternal)
}

func TestRun_missingRoles(t *testing.T) {
	in := input(t, personSchema)
	out := Run(t.Context(), in, Roles{})
	checkOutcome(t, in, out, StatusFailed, 0, CodeInternal)
}

// TestRun_neverAcceptedOtherwise enumerates every path that is not a parsed accept on a
// schema-valid candidate and asserts that none ends accepted. The judge always has an
// accept left, so only the loop's rules keep these jobs from acceptance.
func TestRun_neverAcceptedOtherwise(t *testing.T) {
	acceptLater := fake.Script{{Answer: accept, Repeat: true}}
	tests := []struct {
		name               string
		primary, fallback  fake.Script
		judge              fake.Script
		schemaless         bool
		deadline           time.Duration
		status             Status
		code               string
		wantJudgeCallsZero bool
	}{
		{name: "schema invalid every attempt", primary: fake.Script{{Answer: invalid, Repeat: true}}, judge: acceptLater, status: StatusRejected, wantJudgeCallsZero: true},
		{name: "not JSON every attempt", primary: fake.Script{{Answer: notJSON, Repeat: true}}, judge: acceptLater, status: StatusFailed, code: CodeExtractorFailed, wantJudgeCallsZero: true},
		{name: "array where an object is required", primary: fake.Script{{Answer: `[` + valid + `]`, Repeat: true}}, judge: acceptLater, status: StatusFailed, code: CodeExtractorFailed, wantJudgeCallsZero: true},
		{name: "truncated JSON", primary: fake.Script{{Answer: valid[:20], Repeat: true}}, judge: acceptLater, status: StatusFailed, code: CodeExtractorFailed, wantJudgeCallsZero: true},
		{name: "extractor refuses", primary: fake.AlwaysPermanent(400), judge: acceptLater, status: StatusFailed, code: CodeExtractorFailed, wantJudgeCallsZero: true},
		{name: "extractor group down", primary: fake.AlwaysRetryable(503), fallback: fake.AlwaysRetryable(503), judge: acceptLater, status: StatusFailed, code: CodeProviderUnavailable, wantJudgeCallsZero: true},
		{name: "judge rejects every attempt", primary: fake.Script{{Answer: valid, Repeat: true}}, judge: fake.Script{{Answer: reject, Repeat: true}}, status: StatusRejected},
		{name: "judge garbage", primary: fake.Script{{Answer: valid, Repeat: true}}, judge: fake.Script{fake.Answer(garbage), fake.Answer(garbage), {Answer: accept, Repeat: true}}, status: StatusFailed, code: CodeJudgeFailed},
		{name: "judge says accepted", primary: fake.Script{{Answer: valid, Repeat: true}}, judge: fake.Script{fake.Answer(`{"verdict": "accepted", "feedback": {}}`), fake.Answer(`{"verdict": "ACCEPT", "feedback": {}}`), {Answer: accept, Repeat: true}}, status: StatusFailed, code: CodeJudgeFailed},
		{name: "judge accepts in prose only", primary: fake.Script{{Answer: valid, Repeat: true}}, judge: fake.Script{fake.Answer("I accept."), fake.Answer("accept"), {Answer: accept, Repeat: true}}, status: StatusFailed, code: CodeJudgeFailed},
		{name: "judge empty answers", primary: fake.Script{{Answer: valid, Repeat: true}}, judge: fake.Script{fake.Answer(""), fake.Answer(" "), {Answer: accept, Repeat: true}}, status: StatusFailed, code: CodeJudgeFailed},
		{name: "judge refuses", primary: fake.Script{{Answer: valid, Repeat: true}}, judge: fake.AlwaysPermanent(400), status: StatusFailed, code: CodeJudgeFailed},
		{name: "judge group down", primary: fake.Script{{Answer: valid, Repeat: true}}, judge: fake.AlwaysRetryable(503), status: StatusFailed, code: CodeProviderUnavailable},
		{name: "judge too slow", primary: fake.Script{{Answer: valid, Repeat: true}}, judge: fake.Script{{Answer: accept, Repeat: true, Delay: 10 * time.Second}}, deadline: 100 * time.Millisecond, status: StatusFailed, code: CodeTimeout},
		{name: "schema-less judge rejects", primary: fake.Script{{Answer: valid, Repeat: true}}, judge: fake.Script{{Answer: reject, Repeat: true}}, schemaless: true, status: StatusRejected},
		{name: "schema-less judge down", primary: fake.Script{{Answer: valid, Repeat: true}}, judge: fake.AlwaysRetryable(500), schemaless: true, status: StatusFailed, code: CodeProviderUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t, tt.primary, tt.fallback, tt.judge)
			schema := personSchema
			if tt.schemaless {
				schema = ""
			}
			in := input(t, schema)
			if tt.deadline > 0 {
				in.Deadline = time.Now().Add(tt.deadline)
			}
			out := Run(t.Context(), in, r.roles)
			if out.Status == StatusAccepted {
				t.Fatalf("accepted: %+v", out)
			}
			if out.Status != tt.status || (out.Error == nil) != (tt.code == "") || (out.Error != nil && out.Error.Code != tt.code) {
				t.Fatalf("outcome = %s %+v, want %s %q", out.Status, out.Error, tt.status, tt.code)
			}
			if tt.wantJudgeCallsZero && len(r.judge.Calls()) != 0 {
				t.Errorf("judge called %d times", len(r.judge.Calls()))
			}
		})
	}
}

// TestRun_travelInbox runs a corpus ground truth through the loop with the TravelInbox
// schema and instructions.
func TestRun_travelInbox(t *testing.T) {
	read := func(path string) string {
		data, err := os.ReadFile("../../testdata/travelinbox/" + path)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	result := read("corpus/001-flight/truth.json")
	broken := strings.Replace(result, `"provider": 1.0,`, "", 1)
	r := newRig(t, fake.Script{fake.Answer(broken), fake.Answer("```json\n" + result + "```")}, nil, fake.Script{fake.Answer(accept)})
	in := Input{
		Representation: document.Representation{
			Kind: document.Text, Parts: []provider.Part{{Text: read("corpus/001-flight/notes.md")}}, MediaTypes: []string{"text/plain"},
		},
		ExtractorInstructions: read("corpus/extractor.md"),
		JudgeInstructions:     read("corpus/judge.md"),
		Schema:                travelSchema(t),
	}
	out := Run(t.Context(), in, r.roles)
	checkOutcome(t, in, out, StatusAccepted, 2, "")
	checkCalls(t, in, r.extractorCalls(), r.judge.Calls(), 2, 1)
	if !hasText(r.primary.Calls()[0], sepInstructions+"\n\n"+in.ExtractorInstructions) ||
		!hasText(r.judge.Calls()[0], sepInstructions+"\n\n"+in.JudgeInstructions) {
		t.Error("caller instructions did not reach the roles verbatim")
	}
	if !hasText(r.primary.Calls()[1], `"/reservations/0/confidence" (schema): `+"`confidence`: missing required property 'provider'") {
		t.Errorf("retry lacks the schema feedback: %+v", r.primary.Calls()[1].Parts)
	}
}

func TestOutcome_json(t *testing.T) {
	r := newRig(t, fake.AlwaysRetryable(503), fake.AlwaysRetryable(503), nil)
	in := input(t, personSchema)
	data, err := json.Marshal(Run(t.Context(), in, r.roles))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"status":"failed","result":null,"feedback":[],"attempts":1,"judge_confidence":null,` +
		`"error":{"code":"provider_unavailable","message":"Every endpoint of a role's group failed."},"verification":"none"}`
	if string(data) != want {
		t.Errorf("JSON = %s\nwant   %s", data, want)
	}
}

func TestNewRoles_errors(t *testing.T) {
	build := func(*config.Endpoint) (provider.Provider, error) { return fake.New(nil), nil }
	cfg := loadConfig(t, loopConfig)
	cfg.Roles.Extractor.Group = "missing"
	if _, err := NewRoles(cfg, build); err == nil {
		t.Error("NewRoles with a missing extractor group succeeded")
	}
	cfg = loadConfig(t, loopConfig)
	cfg.Roles.Judge.Group = "missing"
	if _, err := NewRoles(cfg, build); err == nil {
		t.Error("NewRoles with a missing judge group succeeded")
	}
	cfg = loadConfig(t, loopConfig)
	roles, err := NewRoles(cfg, build)
	if err != nil {
		t.Fatal(err)
	}
	if roles.JudgeReparseTries != 1 || roles.FeedbackMessageChars != 0 || roles.Extractor.Name() != "extraction" || roles.Judge.Name() != "review" {
		t.Errorf("roles = %+v", roles)
	}
}

func TestRun_resume(t *testing.T) {
	// A fresh run: attempt 1 is rejected, attempt 2 is the request a resumed run must match.
	fresh := newRig(t, fake.Script{fake.Answer(valid), fake.Answer(valid2)}, nil, fake.Script{fake.Answer(reject), fake.Answer(accept)})
	Run(t.Context(), input(t, personSchema), fresh.roles)
	want := fresh.primary.Calls()[1].Request.Parts

	prior := []Feedback{{Pointer: "/name", Message: "The document prints Ada King.", Source: SourceJudge}}
	resume := func(r *rig, attempts int) (Input, Outcome) {
		in := input(t, personSchema)
		in.PriorAttempts, in.PriorCandidate, in.PriorFeedback = attempts, json.RawMessage(valid), prior
		return in, Run(t.Context(), in, r.roles)
	}

	t.Run("first prompt is a retry's", func(t *testing.T) {
		r := newRig(t, fake.Script{fake.Answer(valid2)}, nil, fake.Script{fake.Answer(accept)})
		var progress []int
		in := input(t, personSchema)
		in.PriorAttempts, in.PriorCandidate, in.PriorFeedback = 1, json.RawMessage(valid), prior
		in.OnAttempt = func(p Progress) { progress = append(progress, p.Attempts) }
		out := Run(t.Context(), in, r.roles)
		checkOutcome(t, in, out, StatusAccepted, 2, "")
		if got := r.primary.Calls(); len(got) != 1 || !reflect.DeepEqual(got[0].Request.Parts, want) {
			t.Errorf("first request %+v, want attempt 2 of a fresh run %+v", got, want)
		}
		sameJSON(t, out.Result, valid2)
		if len(progress) != 0 {
			t.Errorf("progress %v for an accepted attempt", progress)
		}
	})

	t.Run("attempts never exceed the limit", func(t *testing.T) {
		r := newRig(t, fake.Script{{Answer: valid2, Repeat: true}}, nil, fake.Script{{Answer: reject, Repeat: true}})
		var progress []int
		in := input(t, personSchema)
		in.PriorAttempts, in.PriorCandidate, in.PriorFeedback = 1, json.RawMessage(valid), prior
		in.OnAttempt = func(p Progress) { progress = append(progress, p.Attempts) }
		out := Run(t.Context(), in, r.roles)
		checkOutcome(t, in, out, StatusRejected, 3, "")
		if n := len(r.primary.Calls()); n != 2 {
			t.Errorf("%d extractor calls after 1 prior attempt of 3, want 2", n)
		}
		if !reflect.DeepEqual(progress, []int{2, 3}) {
			t.Errorf("progress attempts %v, want [2 3]", progress)
		}
	})

	t.Run("prior attempts at the limit end rejected without a call", func(t *testing.T) {
		r := newRig(t, nil, nil, nil)
		in, out := resume(r, 3)
		checkOutcome(t, in, out, StatusRejected, 3, "")
		sameJSON(t, out.Result, valid)
		if !equalFeedback(out.Feedback, prior) {
			t.Errorf("feedback = %+v, want %+v", out.Feedback, prior)
		}
		if n := len(r.extractorCalls()) + len(r.judge.Calls()); n != 0 {
			t.Errorf("%d provider calls, want none", n)
		}
	})

	t.Run("no candidate at the limit fails", func(t *testing.T) {
		r := newRig(t, nil, nil, nil)
		in := input(t, personSchema)
		in.PriorAttempts = 3
		in.PriorFeedback = []Feedback{schemaFeedback("", "The answer was not a single JSON object: x.")}
		out := Run(t.Context(), in, r.roles)
		checkOutcome(t, in, out, StatusFailed, 3, CodeExtractorFailed)
		if out.Result != nil || len(out.Feedback) != 1 || len(r.extractorCalls()) != 0 {
			t.Errorf("result %s, feedback %+v, calls %d", out.Result, out.Feedback, len(r.extractorCalls()))
		}
	})

	t.Run("feedback without a candidate", func(t *testing.T) {
		r := newRig(t, fake.Script{fake.Answer(valid)}, nil, fake.Script{fake.Answer(accept)})
		in := input(t, personSchema)
		in.PriorAttempts, in.PriorFeedback = 1, prior
		out := Run(t.Context(), in, r.roles)
		checkOutcome(t, in, out, StatusAccepted, 2, "")
		c := r.primary.Calls()[0]
		if hasText(c, sepPrevious) || !hasText(c, sepFeedback) {
			t.Errorf("request %+v: want feedback and no previous candidate", c.Request.Parts)
		}
	})
}

// TestRun_verification: under the default policy accepted pairs with judge and every other
// terminal status with none (ADR 0009).
func TestRun_verification(t *testing.T) {
	tests := []struct {
		name   string
		judge  fake.Script
		status Status
		want   string
	}{
		{"accepted", fake.Script{fake.Answer(accept)}, StatusAccepted, VerificationJudge},
		{"rejected", fake.Script{fake.Answer(reject)}, StatusRejected, VerificationNone},
		{"failed", fake.AlwaysRetryable(503), StatusFailed, VerificationNone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t, fake.Script{fake.Answer(valid)}, nil, tt.judge)
			in := input(t, personSchema)
			in.MaxAttempts = 1
			out := Run(t.Context(), in, r.roles)
			if out.Status != tt.status || out.Verification != tt.want {
				t.Errorf("status %s, verification %q; want %s, %q", out.Status, out.Verification, tt.status, tt.want)
			}
		})
	}
}

// groundedInput is input(personSchema) on a text document that prints valid (Ada
// Lovelace, 1815) and valid2's name, with its grounding text.
func groundedInput(t *testing.T, policy config.JudgePolicy) Input {
	t.Helper()
	return groundableInput(t, personSchema, "Ada Lovelace, later Ada King, was born in 1815.", policy)
}

// annotatedInput is groundedInput with annotatedPersonSchema: under always an ungrounded
// candidate takes the repair.
func annotatedInput(t *testing.T, policy config.JudgePolicy) Input {
	t.Helper()
	return groundableInput(t, annotatedPersonSchema, "Ada Lovelace, later Ada King, was born in 1815.", policy)
}

// groundableInput is input(schema) on a groundable plain-text document.
func groundableInput(t *testing.T, schema, text string, policy config.JudgePolicy) Input {
	t.Helper()
	in := input(t, schema)
	in.Representation = document.Representation{
		Kind: document.Text, Parts: []provider.Part{{Text: text}}, MediaTypes: []string{"text/plain"},
		GroundText: text, Groundable: true,
	}
	in.JudgePolicy = policy
	return in
}

// invented has a year the document does not print.
const invented = `{"name": "Ada Lovelace", "born": 1816}`

// TestRun_autoNeverGroundsInvented is the epic's exit criterion as a property: under auto,
// whatever the attempt limit, the sample rate, an earlier schema violation or the judge's
// answer, a candidate with an invented value is never accepted without a judge, and
// accepted only after a judge call that answered.
func TestRun_autoNeverGroundsInvented(t *testing.T) {
	judges := map[string]fake.Script{
		"accepts": {{Answer: accept, Repeat: true}},
		"rejects": {{Answer: reject, Repeat: true}},
		"fails":   fake.AlwaysRetryable(503),
		"garbage": {{Answer: garbage, Repeat: true}},
	}
	for name, judge := range judges {
		for _, maxAttempts := range []int{1, 2, 3} {
			for _, schemaFirst := range []bool{false, true} {
				for _, rate := range []float64{0, 1} {
					extractor := fake.Script{{Answer: invented, Repeat: true}}
					if schemaFirst {
						extractor = fake.Script{fake.Answer(invalid), {Answer: invented, Repeat: true}}
					}
					r := newRig(t, extractor, nil, judge)
					in := groundedInput(t, config.JudgeAuto)
					in.MaxAttempts, in.SampleRate, in.SampleKey = maxAttempts, rate, []byte("key")
					out := Run(t.Context(), in, r.roles)
					if out.Verification == VerificationGrounded || out.Decision == DecisionGrounded {
						t.Errorf("judge %s, max %d, schema first %v, rate %g: accepted on grounding: %+v", name, maxAttempts, schemaFirst, rate, out)
					}
					if out.Status == StatusAccepted && (out.JudgeCalls == 0 || len(r.judge.Calls()) == 0) {
						t.Errorf("judge %s, max %d: accepted without a judge call", name, maxAttempts)
					}
				}
			}
		}
	}
}

// TestRun_judgePolicy covers the loop's policy paths on a groundable text document: the
// outcome, the calls, the decision for the metrics and where grounding entries go. The
// scenarios in testdata/scenarios/judge-policy cover the same paths end to end.
func TestRun_judgePolicy(t *testing.T) {
	tests := []struct {
		name               string
		policy             config.JudgePolicy
		rate               float64
		maxAttempts        int
		extractor, judge   fake.Script
		schemaless         bool
		annotated          bool // annotatedPersonSchema: the repair runs under always
		wantStatus         Status
		wantVerification   string
		wantExtractorCalls int
		wantJudgeCalls     int
		wantDecision       string
		wantGrounding      int // grounding entries in the outcome's feedback
	}{
		{"always grounded", "", 0, 3, fake.Script{fake.Answer(valid)}, fake.Script{fake.Answer(accept)}, false, false,
			StatusAccepted, VerificationJudge, 1, 1, DecisionJudged, 0},
		{"always repaired", config.JudgeAlways, 0, 3, fake.Script{fake.Answer(invented), fake.Answer(valid)}, fake.Script{fake.Answer(accept)}, false, true,
			StatusAccepted, VerificationJudge, 2, 1, DecisionJudged, 0},
		{"always schema-less, no repair", config.JudgeAlways, 0, 3, fake.Script{fake.Answer(invented)}, fake.Script{fake.Answer(accept)}, true, false,
			StatusAccepted, VerificationJudge, 1, 1, DecisionJudged, 0},
		{"never advisory", config.JudgeNever, 0, 3, fake.Script{fake.Answer(invented)}, nil, false, false,
			StatusExtracted, VerificationNone, 1, 0, DecisionSkipped, 1},
		{"never schema-less", config.JudgeNever, 0, 3, fake.Script{fake.Answer(`{"anything": "Ada King"}`)}, nil, true, false,
			StatusExtracted, VerificationNone, 1, 0, DecisionSkipped, 0},
		{"never failed extractor", config.JudgeNever, 0, 2, fake.Script{{Answer: notJSON, Repeat: true}}, nil, false, false,
			StatusFailed, VerificationNone, 2, 0, "", 0},
		{"auto grounded", config.JudgeAuto, 0, 3, fake.Script{fake.Answer(valid)}, nil, false, false,
			StatusAccepted, VerificationGrounded, 1, 0, DecisionGrounded, 0},
		{"auto sampled, the judge disagrees", config.JudgeAuto, 1, 3, fake.Script{fake.Answer(valid), fake.Answer(valid)},
			fake.Script{fake.Answer(reject), fake.Answer(accept)}, false, false, StatusAccepted, VerificationJudge, 2, 2, DecisionSampled, 0},
		{"auto judge rejects then grounded answer to judge feedback", config.JudgeAuto, 0, 3,
			fake.Script{fake.Answer(invented), fake.Answer(invented), fake.Answer(valid)},
			fake.Script{fake.Answer(reject), fake.Answer(accept)}, false, false, StatusAccepted, VerificationJudge, 3, 2, DecisionJudged, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t, tt.extractor, nil, tt.judge)
			in := groundedInput(t, tt.policy)
			if tt.annotated {
				in = annotatedInput(t, tt.policy)
			}
			if tt.schemaless {
				in.Schema = nil
			}
			in.MaxAttempts, in.SampleRate, in.SampleKey = tt.maxAttempts, tt.rate, []byte("key")
			var progress []Progress
			in.OnAttempt = func(p Progress) { progress = append(progress, p) }
			out := Run(t.Context(), in, r.roles)
			code := ""
			if tt.wantStatus == StatusFailed {
				code = CodeExtractorFailed
			}
			checkOutcome(t, in, out, tt.wantStatus, tt.wantExtractorCalls, code)
			if out.Verification != tt.wantVerification || out.Decision != tt.wantDecision {
				t.Errorf("verification %q, decision %q; want %q, %q", out.Verification, out.Decision, tt.wantVerification, tt.wantDecision)
			}
			if len(r.primary.Calls()) != tt.wantExtractorCalls || len(r.judge.Calls()) != tt.wantJudgeCalls || out.JudgeCalls != tt.wantJudgeCalls {
				t.Errorf("calls: extractor %d, judge %d (outcome %d); want %d, %d", len(r.primary.Calls()), len(r.judge.Calls()),
					out.JudgeCalls, tt.wantExtractorCalls, tt.wantJudgeCalls)
			}
			grounding := 0
			for _, f := range out.Feedback {
				if f.Source == SourceGrounding {
					grounding++
					if f.Message != "value not found in the document" || f.Pointer != "/born" {
						t.Errorf("grounding entry %+v", f)
					}
				}
			}
			if grounding != tt.wantGrounding {
				t.Errorf("%d grounding entries in %+v, want %d", grounding, out.Feedback, tt.wantGrounding)
			}
			if out.Status == StatusAccepted && out.Verification == VerificationGrounded && out.JudgeConfidence != nil {
				t.Error("a grounded acceptance carries a judge confidence")
			}
			// The repair's entries reach the extractor (and OnAttempt, which persists them).
			for i, c := range r.primary.Calls() {
				if i > 0 && tt.name == "always repaired" && !hasText(c, `- "/born" (grounding): value not found in the document`) {
					t.Errorf("extractor call %d lacks the grounding feedback", i+1)
				}
			}
			if tt.name == "always repaired" && (len(progress) != 1 || progress[0].Feedback[0].Source != SourceGrounding) {
				t.Errorf("progress after the repair = %+v", progress)
			}
		})
	}
}

// TestRun_groundingRepairOnResume: a resumed job whose persisted feedback holds grounding
// entries already had its repair, so an ungrounded candidate goes to the judge.
func TestRun_groundingRepairOnResume(t *testing.T) {
	r := newRig(t, fake.Script{fake.Answer(invented)}, nil, fake.Script{fake.Answer(accept)})
	in := groundedInput(t, config.JudgeAlways)
	in.PriorAttempts, in.PriorCandidate = 1, json.RawMessage(invented)
	in.PriorFeedback = []Feedback{{Pointer: "/born", Message: "value not found in the document", Source: SourceGrounding}}
	out := Run(t.Context(), in, r.roles)
	if out.Status != StatusAccepted || out.Attempts != 2 || len(r.primary.Calls()) != 1 || len(r.judge.Calls()) != 1 || len(out.Feedback) != 0 {
		t.Errorf("outcome %+v after %d extractor and %d judge calls; want accepted at once, no second repair",
			out, len(r.primary.Calls()), len(r.judge.Calls()))
	}
}

// TestRun_groundingRepairDroppedOnFailure: under always a repair's entries never reach
// the outcome, even when the attempt after the repair fails.
func TestRun_groundingRepairDroppedOnFailure(t *testing.T) {
	r := newRig(t, fake.Script{fake.Answer(invented), fake.Fail(provider.Permanent, 400), fake.Fail(provider.Permanent, 400)}, nil, nil)
	in := annotatedInput(t, config.JudgeAlways)
	out := Run(t.Context(), in, r.roles)
	if out.Status != StatusRejected || len(out.Feedback) != 0 || out.Feedback == nil {
		t.Errorf("outcome %s with feedback %+v; want rejected with no entries", out.Status, out.Feedback)
	}
}

// TestRun_alwaysRepairNeedsAnnotation (ADR 0009, amendment 2026-10-18): under always a
// candidate with a derived value the document does not print (the country code GB of a
// London address) goes to the judge at once when the schema carries no x-vde-grounding
// annotation, so the contract path pays no extra extractor call; the same candidate with
// an annotation elsewhere in the schema still takes the one repair.
func TestRun_alwaysRepairNeedsAnnotation(t *testing.T) {
	const (
		text      = "Booking for Ada Lovelace, 12 Quay Street, London."
		candidate = `{"name": "Ada Lovelace", "country": "GB"}`
		props     = `"name": {"type": "string"}, "country": {"type": "string"}`
	)
	tests := []struct {
		name                     string
		schema                   string
		wantExtractor, wantJudge int
		wantRepaired             bool
	}{
		{"unannotated", `{"type": "object", "properties": {` + props + `}}`, 1, 1, false},
		{"annotated", `{"type": "object", "properties": {` + props + `, "notes": {"type": "string", "x-vde-grounding": "skip"}}}`, 2, 1, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t, fake.Script{{Answer: candidate, Repeat: true}}, nil, fake.Script{fake.Answer(accept)})
			in := groundableInput(t, tt.schema, text, config.JudgeAlways)
			var repaired bool
			in.OnAttempt = func(p Progress) { repaired = repaired || p.Repaired }
			out := Run(t.Context(), in, r.roles)
			if out.Status != StatusAccepted || out.Verification != VerificationJudge || len(out.Feedback) != 0 {
				t.Errorf("outcome %s, verification %q, feedback %+v; want accepted by the judge, no entries", out.Status, out.Verification, out.Feedback)
			}
			if len(r.primary.Calls()) != tt.wantExtractor || len(r.judge.Calls()) != tt.wantJudge || out.JudgeCalls != tt.wantJudge {
				t.Errorf("calls: extractor %d, judge %d (outcome %d); want %d, %d", len(r.primary.Calls()), len(r.judge.Calls()),
					out.JudgeCalls, tt.wantExtractor, tt.wantJudge)
			}
			if repaired != tt.wantRepaired {
				t.Errorf("repaired %v, want %v", repaired, tt.wantRepaired)
			}
		})
	}
}

// TestRun_autoNothingChecked: under auto a candidate with no checked critical leaf ({},
// only nulls or booleans) gives grounding no evidence, so the judge decides it; it is
// never accepted as grounded.
func TestRun_autoNothingChecked(t *testing.T) {
	for _, candidate := range []string{`{}`, `{"name": null, "born": null}`, `{"alive": false, "spouse": null}`} {
		t.Run(candidate, func(t *testing.T) {
			r := newRig(t, fake.Script{fake.Answer(candidate)}, nil, fake.Script{fake.Answer(accept)})
			in := groundedInput(t, config.JudgeAuto)
			in.Schema = nil // schema-less, so {} and nulls validate
			in.SampleKey = []byte("key")
			out := Run(t.Context(), in, r.roles)
			if out.Status != StatusAccepted || out.Verification != VerificationJudge || out.Decision != DecisionJudged ||
				len(r.judge.Calls()) != 1 {
				t.Errorf("outcome %s, verification %q, decision %q after %d judge calls; want accepted by the judge",
					out.Status, out.Verification, out.Decision, len(r.judge.Calls()))
			}
		})
	}
}

// TestRun_groundingRepairFlag: the repair is reported in Progress.Repaired, and a resumed
// job with PriorRepaired (whose saved feedback is the judge's, not the repair's) never
// repairs again: every later ungrounded candidate goes to the judge.
func TestRun_groundingRepairFlag(t *testing.T) {
	t.Run("reported", func(t *testing.T) {
		r := newRig(t, fake.Script{fake.Answer(invented), fake.Answer(valid)}, nil, fake.Script{fake.Answer(accept)})
		in := annotatedInput(t, config.JudgeAlways)
		var progress []Progress
		in.OnAttempt = func(p Progress) { progress = append(progress, p) }
		Run(t.Context(), in, r.roles)
		if len(progress) != 1 || !progress[0].Repaired {
			t.Errorf("progress = %+v, want one entry with Repaired", progress)
		}
	})
	t.Run("resumed", func(t *testing.T) {
		r := newRig(t, fake.Script{{Answer: invented, Repeat: true}}, nil, fake.Script{{Answer: reject, Repeat: true}})
		in := groundedInput(t, config.JudgeAlways)
		in.MaxAttempts, in.PriorAttempts, in.PriorRepaired = 4, 2, true
		in.PriorCandidate = json.RawMessage(invented)
		in.PriorFeedback = []Feedback{{Pointer: "/born", Message: "wrong year", Source: SourceJudge}}
		var progress []Progress
		in.OnAttempt = func(p Progress) { progress = append(progress, p) }
		out := Run(t.Context(), in, r.roles)
		if out.Status != StatusRejected || out.Attempts != 4 || len(r.primary.Calls()) != 2 || len(r.judge.Calls()) != 2 {
			t.Errorf("outcome %s after %d attempts, %d extractor and %d judge calls; want rejected after 2 judged attempts",
				out.Status, out.Attempts, len(r.primary.Calls()), len(r.judge.Calls()))
		}
		for _, p := range progress {
			if !p.Repaired {
				t.Errorf("progress %+v lost the repair flag", p)
			}
		}
	})
}
