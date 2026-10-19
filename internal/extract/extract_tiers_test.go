package extract

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kaanemec/vde/internal/document"
	"github.com/kaanemec/vde/internal/provider"
	"github.com/kaanemec/vde/internal/provider/classifier"
	"github.com/kaanemec/vde/internal/provider/fake"
)

// tiersConfig has a primary and a strong group per role, a second judge, a classifier
// judge group and an LLM feedback group. The roles section is appended per test.
const tiersConfig = `
endpoints:
  primary: {provider: fake, models: {primary-model: {accepts: [text/plain, application/pdf]}}}
  strong: {provider: fake, models: {strong-model: {accepts: [text/plain, application/pdf]}}}
  review: {provider: fake, models: {judge-model: {accepts: [text/plain, application/pdf]}}}
  review_strong: {provider: fake, models: {strong-judge-model: {accepts: [text/plain, application/pdf]}}}
  second: {provider: fake, models: {second-model: {accepts: [text/plain, application/pdf]}}}
  llm: {provider: fake, models: {feedback-model: {accepts: [text/plain, application/pdf]}}}
  jev:
    provider: classifier
    base_url: CLASSIFIER_URL
    models:
      jev-1:
        accepts: [text/plain]
        questions: [{id: accuracy, text: "Is every value of the candidate what the document says?", threshold: 0.7}]
groups:
  extraction: {endpoints: [{endpoint: primary, model: primary-model}], failover: {max_tries: 1, max_total_time: 1m}}
  extraction_strong: {endpoints: [{endpoint: strong, model: strong-model}], failover: {max_tries: 1, max_total_time: 1m}}
  review: {endpoints: [{endpoint: review, model: judge-model}], failover: {max_tries: 1, max_total_time: 1m}}
  review_strong: {endpoints: [{endpoint: review_strong, model: strong-judge-model}], failover: {max_tries: 1, max_total_time: 1m}}
  second: {endpoints: [{endpoint: second, model: second-model}], failover: {max_tries: 1, max_total_time: 1m}}
  classify: {endpoints: [{endpoint: jev, model: jev-1}], failover: {max_tries: 1, max_total_time: 1m}}
  feedback: {endpoints: [{endpoint: llm, model: feedback-model}], failover: {max_tries: 1, max_total_time: 1m}}
limits: {max_attempts: 3, job_timeout: 5m, judge_reparse_tries: 1}
`

// tiers is a configured loop whose fake endpoints are named as in tiersConfig.
type tiers struct {
	fakes map[string]*fake.Provider
	roles Roles
}

func (r *tiers) calls(endpoint string) int { return len(r.fakes[endpoint].Calls()) }

// newTiers loads tiersConfig with roles and gives each fake endpoint its script; the
// classifier endpoint, when classifierURL is set, gets the real adapter.
func newTiers(t *testing.T, roles, classifierURL string, scripts map[string]fake.Script) *tiers {
	t.Helper()
	if classifierURL == "" {
		classifierURL = "http://classifier.invalid"
	}
	cfg := loadConfig(t, strings.Replace(tiersConfig, "CLASSIFIER_URL", classifierURL, 1)+roles)
	r := &tiers{fakes: map[string]*fake.Provider{}}
	providers := map[string]provider.Provider{"jev": classifier.New(cfg.Endpoints["jev"])}
	for name, e := range cfg.Endpoints {
		if e.Provider == "fake" {
			r.fakes[name] = fake.New(scripts[name])
			providers[name] = r.fakes[name]
		}
	}
	r.roles = buildRoles(t, cfg, providers)
	return r
}

const escalationRoles = `
roles:
  extractor:
    group: extraction
    escalation: [{from_attempt: 3, group: extraction_strong}]
  judge:
    group: review
    escalation: [{from_attempt: 3, group: review_strong}]
`

// TestRun_escalation: attempts 1 and 2 on the primary groups, attempt 3 on the strong ones.
func TestRun_escalation(t *testing.T) {
	r := newTiers(t, escalationRoles, "", map[string]fake.Script{
		"primary":       {fake.Answer(valid), fake.Answer(valid2)},
		"review":        {fake.Answer(reject), fake.Answer(reject)},
		"strong":        {fake.Answer(valid)},
		"review_strong": {fake.Answer(accept)},
	})
	in := input(t, personSchema)
	out := Run(t.Context(), in, r.roles)
	checkOutcome(t, in, out, StatusAccepted, 3, "")
	for endpoint, want := range map[string]int{"primary": 2, "review": 2, "strong": 1, "review_strong": 1} {
		if got := r.calls(endpoint); got != want {
			t.Errorf("%s calls = %d, want %d", endpoint, got, want)
		}
	}
	strong := r.fakes["strong"].Calls()[0]
	if strong.Request.Model != "strong-model" || !hasText(strong, `- "/name" (judge): The document prints Ada King.`) {
		t.Errorf("attempt 3 did not carry attempt 2's feedback to the strong group: %+v", strong.Parts)
	}
}

// TestRun_escalationResume: a resumed job counts attempts on, so it escalates on time.
func TestRun_escalationResume(t *testing.T) {
	r := newTiers(t, escalationRoles, "", map[string]fake.Script{
		"strong":        {fake.Answer(valid)},
		"review_strong": {fake.Answer(accept)},
	})
	in := input(t, personSchema)
	in.PriorAttempts = 2
	out := Run(t.Context(), in, r.roles)
	checkOutcome(t, in, out, StatusAccepted, 3, "")
	if r.calls("primary") != 0 || r.calls("strong") != 1 {
		t.Errorf("calls: primary %d, strong %d; want 0, 1", r.calls("primary"), r.calls("strong"))
	}
}

const secondJudgeRoles = `
roles:
  extractor: {group: extraction}
  judge: {group: review}
  second_judge: {group: second}
`

func TestRun_secondJudge(t *testing.T) {
	advisory1 := `{"verdict": "accept", "feedback": {"/born": "Check the year."}, "confidence": 0.9}`
	advisory2 := `{"verdict": "accept", "feedback": {"/name": "Fine."}, "confidence": 0.6}`
	tests := []struct {
		name            string
		scripts         map[string]fake.Script
		status          Status
		attempts        int
		code            string
		judge, second   int
		feedback        []Feedback
		confidence      *float64
		secondFeedsNext bool
	}{
		{
			name:    "both accept",
			scripts: map[string]fake.Script{"primary": {fake.Answer(valid)}, "review": {fake.Answer(advisory1)}, "second": {fake.Answer(advisory2)}},
			status:  StatusAccepted, attempts: 1, judge: 1, second: 1,
			feedback: []Feedback{
				{Pointer: "/born", Message: "Check the year.", Source: SourceJudge},
				{Pointer: "/name", Message: "Fine.", Source: SourceJudge},
			},
			confidence: new(0.6),
		},
		{
			name: "second rejects, next attempt accepted by both",
			scripts: map[string]fake.Script{
				"primary": {fake.Answer(valid), fake.Answer(valid2)},
				"review":  {fake.Answer(accept), fake.Answer(accept)},
				"second":  {fake.Answer(reject), fake.Answer(accept)},
			},
			status: StatusAccepted, attempts: 2, judge: 2, second: 2, feedback: []Feedback{}, secondFeedsNext: true,
		},
		{
			name:    "first rejects, second not asked",
			scripts: map[string]fake.Script{"primary": {fake.Answer(valid)}, "review": {fake.Answer(reject)}},
			status:  StatusRejected, attempts: 1, judge: 1, second: 0,
			feedback: []Feedback{{Pointer: "/name", Message: "The document prints Ada King.", Source: SourceJudge}},
		},
		{
			name:    "second rejects the last attempt",
			scripts: map[string]fake.Script{"primary": {fake.Answer(valid)}, "review": {fake.Answer(accept)}, "second": {fake.Answer(reject)}},
			status:  StatusRejected, attempts: 1, judge: 1, second: 1,
			feedback: []Feedback{{Pointer: "/name", Message: "The document prints Ada King.", Source: SourceJudge}},
		},
		{
			name:    "second unparseable",
			scripts: map[string]fake.Script{"primary": {fake.Answer(valid)}, "review": {fake.Answer(accept)}, "second": {fake.Answer(garbage), fake.Answer(garbage)}},
			status:  StatusFailed, attempts: 1, code: CodeJudgeFailed, judge: 1, second: 2,
		},
		{
			name:    "second group down",
			scripts: map[string]fake.Script{"primary": {fake.Answer(valid)}, "review": {fake.Answer(accept)}, "second": fake.AlwaysRetryable(503)},
			status:  StatusFailed, attempts: 1, code: CodeProviderUnavailable, judge: 1, second: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newTiers(t, secondJudgeRoles, "", tt.scripts)
			in := input(t, personSchema)
			if tt.attempts == 1 {
				in.MaxAttempts = 1
			}
			out := Run(t.Context(), in, r.roles)
			checkOutcome(t, in, out, tt.status, tt.attempts, tt.code)
			if r.calls("review") != tt.judge || r.calls("second") != tt.second {
				t.Errorf("calls: judge %d, second %d; want %d, %d", r.calls("review"), r.calls("second"), tt.judge, tt.second)
			}
			if tt.feedback != nil && !equalFeedback(out.Feedback, tt.feedback) {
				t.Errorf("feedback = %+v, want %+v", out.Feedback, tt.feedback)
			}
			if !reflect.DeepEqual(out.JudgeConfidence, tt.confidence) {
				t.Errorf("confidence = %v, want %v", out.JudgeConfidence, tt.confidence)
			}
			if tt.secondFeedsNext && !hasText(r.fakes["primary"].Calls()[1], `- "/name" (judge): The document prints Ada King.`) {
				t.Error("the second judge's feedback did not reach the next attempt")
			}
			second := r.fakes["second"].Calls()
			if len(second) > 0 {
				_, judgeSystem := framings(false)
				if second[0].Request.System != judgeSystem ||
					!hasText(second[0], in.JudgeInstructions) || !hasText(second[0], sepCandidate) {
					t.Error("the second judge did not get the judge request")
				}
			}
		})
	}
}

// classifierServer is a fake classifier speaking the classify contract: each call takes
// the next probability for question accuracy (threshold 0.7).
type classifierServer struct {
	*httptest.Server
	mu    sync.Mutex
	probs []float64
	texts []string
}

func newClassifierServer(t *testing.T, probs ...float64) *classifierServer {
	t.Helper()
	s := &classifierServer{probs: probs}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req classifier.Request
		if r.URL.Path != classifier.Path || json.NewDecoder(r.Body).Decode(&req) != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		s.texts = append(s.texts, req.Text)
		if len(s.probs) == 0 {
			http.Error(w, "exhausted", http.StatusServiceUnavailable)
			return
		}
		p := s.probs[0]
		s.probs = s.probs[1:]
		_ = json.NewEncoder(w).Encode(classifier.Response{Decisions: []classifier.Decision{
			{Question: req.Questions[0].ID, Label: "yes", Probability: &p},
		}})
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *classifierServer) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.texts)
}

func textInput(t *testing.T) Input {
	t.Helper()
	in := input(t, personSchema)
	in.Representation = document.Representation{
		Kind:       document.Text,
		Parts:      []provider.Part{{Text: "Ada King, born 1815."}},
		MediaTypes: []string{"text/plain"},
	}
	return in
}

const classifierRoles = `
roles:
  extractor: {group: extraction}
  judge: {group: classify%s}
`

// checkRejectRule asserts contract §7 on the outcome: a judge rejection carries at least
// one feedback entry.
func checkRejectRule(t *testing.T, out Outcome) {
	t.Helper()
	if out.Status == StatusRejected && len(out.Feedback) == 0 {
		t.Fatal("rejected without feedback")
	}
	for _, f := range out.Feedback {
		if f.Source == SourceJudge && f.Message == "" {
			t.Fatalf("empty judge message in %+v", out.Feedback)
		}
	}
}

func TestRun_classifierWholeResult(t *testing.T) {
	for _, mode := range []string{"", ", feedback_fallback: whole_result"} {
		t.Run("mode"+mode, func(t *testing.T) {
			srv := newClassifierServer(t, 0.2, 0.3)
			r := newTiers(t, fmt.Sprintf(classifierRoles, mode), srv.URL, map[string]fake.Script{
				"primary": {fake.Answer(valid), fake.Answer(valid2)},
			})
			in := textInput(t)
			in.MaxAttempts = 2
			out := Run(t.Context(), in, r.roles)
			checkOutcome(t, in, out, StatusRejected, 2, "")
			checkRejectRule(t, out)
			if len(out.Feedback) != 1 || out.Feedback[0].Pointer != "" || out.Feedback[0].Source != SourceJudge ||
				!strings.HasPrefix(out.Feedback[0].Message, classifier.WholeResultMessage) ||
				!strings.Contains(out.Feedback[0].Message, "0.30") {
				t.Errorf("feedback = %+v, want one whole-result entry with the confidence", out.Feedback)
			}
			if out.JudgeConfidence == nil || *out.JudgeConfidence != 0.3 {
				t.Errorf("confidence = %v, want 0.3", out.JudgeConfidence)
			}
			if !hasText(r.fakes["primary"].Calls()[1], classifier.WholeResultMessage) {
				t.Error("the whole-result entry did not reach attempt 2")
			}
			if srv.calls() != 2 || !strings.Contains(srv.texts[0], "Ada King, born 1815.") || !strings.Contains(srv.texts[0], valid) {
				t.Errorf("classifier texts = %q, want the document and the candidate", srv.texts)
			}
		})
	}
}

func TestRun_classifierAccept(t *testing.T) {
	srv := newClassifierServer(t, 0.95)
	r := newTiers(t, fmt.Sprintf(classifierRoles, ", feedback_fallback: llm, feedback_group: feedback"), srv.URL,
		map[string]fake.Script{"primary": {fake.Answer(valid)}})
	in := textInput(t)
	out := Run(t.Context(), in, r.roles)
	checkOutcome(t, in, out, StatusAccepted, 1, "")
	if r.calls("llm") != 0 {
		t.Error("the feedback group was called on an accept")
	}
	if out.JudgeConfidence == nil || *out.JudgeConfidence != 0.95 || len(out.Feedback) != 0 {
		t.Errorf("confidence %v, feedback %+v", out.JudgeConfidence, out.Feedback)
	}
}

func TestRun_classifierLLMFeedback(t *testing.T) {
	llmReject := `{"verdict": "reject", "feedback": {"/name": "The document prints Ada King."}}`
	tests := []struct {
		name      string
		llm       fake.Script
		llmCalls  int
		wantPtr   string
		wantInMsg string
	}{
		{"pointer-level feedback", fake.Script{fake.Answer(llmReject), fake.Answer(llmReject)}, 2, "/name", "Ada King"},
		{"LLM accepts: the reject stands", fake.Script{fake.Answer(accept), fake.Answer(accept)}, 2, "", classifier.WholeResultMessage},
		{"LLM unparseable", fake.Script{{Answer: garbage, Repeat: true}}, 4, "", classifier.WholeResultMessage},
		{"LLM group down", fake.AlwaysRetryable(503), 2, "", classifier.WholeResultMessage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newClassifierServer(t, 0.1, 0.2)
			r := newTiers(t, fmt.Sprintf(classifierRoles, ", feedback_fallback: llm, feedback_group: feedback"), srv.URL,
				map[string]fake.Script{"primary": {fake.Answer(valid), fake.Answer(valid2)}, "llm": tt.llm})
			in := textInput(t)
			in.MaxAttempts = 2
			out := Run(t.Context(), in, r.roles)
			checkOutcome(t, in, out, StatusRejected, 2, "")
			checkRejectRule(t, out)
			if r.calls("llm") != tt.llmCalls {
				t.Errorf("feedback group calls = %d, want %d", r.calls("llm"), tt.llmCalls)
			}
			if len(out.Feedback) != 1 || out.Feedback[0].Pointer != tt.wantPtr || !strings.Contains(out.Feedback[0].Message, tt.wantInMsg) {
				t.Errorf("feedback = %+v", out.Feedback)
			}
			if !hasText(r.fakes["primary"].Calls()[1], tt.wantInMsg) {
				t.Error("attempt 2 did not get the feedback")
			}
			if out.JudgeConfidence == nil || *out.JudgeConfidence != 0.2 {
				t.Errorf("confidence = %v, want the classifier's 0.2", out.JudgeConfidence)
			}
			if c := r.fakes["llm"].Calls(); len(c) > 0 {
				if _, judgeSystem := framings(false); c[0].Request.System != judgeSystem || !hasText(c[0], sepCandidate) {
					t.Error("the feedback group did not get the judge request")
				}
			}
		})
	}
}

// TestRun_classifierLLMFeedbackDeadline: the job's deadline during the feedback call
// fails the job with timeout rather than reporting a rejection.
func TestRun_classifierLLMFeedbackDeadline(t *testing.T) {
	srv := newClassifierServer(t, 0.1)
	slow := fake.Answer(`{"verdict": "reject", "feedback": {"/name": "x"}}`)
	slow.Delay = 10 * time.Second
	r := newTiers(t, fmt.Sprintf(classifierRoles, ", feedback_fallback: llm, feedback_group: feedback"), srv.URL,
		map[string]fake.Script{"primary": {fake.Answer(valid)}, "llm": {slow}})
	in := textInput(t)
	in.Deadline = time.Now().Add(300 * time.Millisecond)
	out := Run(t.Context(), in, r.roles)
	checkOutcome(t, in, out, StatusFailed, 1, CodeTimeout)
}

// TestRun_classifierDown: a classifier that fails never yields acceptance.
func TestRun_classifierDown(t *testing.T) {
	srv := newClassifierServer(t) // every call 503
	r := newTiers(t, fmt.Sprintf(classifierRoles, ""), srv.URL, map[string]fake.Script{"primary": {fake.Answer(valid)}})
	in := textInput(t)
	out := Run(t.Context(), in, r.roles)
	checkOutcome(t, in, out, StatusFailed, 1, CodeProviderUnavailable)
}
