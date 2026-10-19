package classifier

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/provider"
	"github.com/kaanemec/vde/internal/provider/providertest"
)

var questions = []config.Question{
	{ID: "accuracy", Text: "Is every value what the document says?", Threshold: 0.8},
	{ID: "complete", Text: "Is nothing asked for missing?", Threshold: 0.5},
}

func endpoint(url string) *config.Endpoint {
	return &config.Endpoint{
		Provider: config.ProviderClassifier,
		BaseURL:  url + "/",
		Timeout:  5 * time.Second,
		Models:   map[string]config.Model{"jev": {Accepts: []string{"text/plain"}, Questions: questions}},
	}
}

func prob(p float64) *float64 { return &p }

type verdictOut struct {
	Verdict    string            `json:"verdict"`
	Feedback   map[string]string `json:"feedback"`
	Confidence float64           `json:"confidence"`
}

func TestClient_Complete(t *testing.T) {
	t.Parallel()
	srv, seen := providertest.Serve(t, providertest.Reply(http.StatusOK, nil,
		`{"decisions": [{"question": "complete", "label": "yes", "probability": 0.9}, {"question": "accuracy", "label": "yes", "probability": 0.85}]}`))

	c := New(endpoint(srv.URL))
	resp, err := c.Complete(t.Context(), provider.Request{
		Model:  "jev",
		System: "You are the judge",
		Parts:  []provider.Part{{Text: "Document:"}, {Text: "Ada"}, {Text: `Candidate:` + "\n\n" + `{"name": "Ada"}`}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen.Method != http.MethodPost || seen.URL.Path != Path || seen.Header.Get("Content-Type") != "application/json" {
		t.Errorf("request = %s %s (Content-Type %q), want POST %s as JSON", seen.Method, seen.URL.Path, seen.Header.Get("Content-Type"), Path)
	}
	var got Request
	if err := json.Unmarshal(seen.Body, &got); err != nil {
		t.Fatalf("request body: %v", err)
	}
	if got.Model != "jev" || len(got.Questions) != 2 || got.Questions[0] != (Question{ID: "accuracy", Text: questions[0].Text}) {
		t.Errorf("request = %+v", got)
	}
	if got.Text != "Document:\n\nAda\n\nCandidate:\n\n{\"name\": \"Ada\"}" {
		t.Errorf("text = %q", got.Text)
	}
	if strings.Contains(got.Text, "You are the judge") {
		t.Error("the framing was sent")
	}
	var v verdictOut
	if err := json.Unmarshal([]byte(resp.Text), &v); err != nil {
		t.Fatal(err)
	}
	if v.Verdict != "accept" || len(v.Feedback) != 0 || v.Confidence != 0.85 {
		t.Errorf("verdict = %s", resp.Text)
	}
}

func TestClient_Complete_errors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		status  int
		body    string
		auth    string
		parts   []provider.Part
		model   string
		kind    provider.Kind
		message string
	}{
		{name: "server error", status: 503, body: "{}", kind: provider.Retryable},
		{name: "rate limited", status: 429, body: "{}", kind: provider.Retryable},
		{name: "bad request", status: 400, body: "{}", kind: provider.Permanent},
		{name: "malformed", status: 200, body: "not json", kind: provider.Permanent, message: "classifier: malformed response"},
		{name: "missing decision", status: 200, body: `{"decisions": [{"question": "accuracy", "probability": 1}]}`,
			kind: provider.Permanent, message: "classifier: no decision for question complete"},
		{name: "no probability", status: 200, body: `{"decisions": [{"question": "accuracy"}, {"question": "complete", "probability": 1}]}`,
			kind: provider.Permanent},
		{name: "probability above 1", status: 200, body: `{"decisions": [{"question": "accuracy", "probability": 1.5}, {"question": "complete", "probability": 1}]}`,
			kind: provider.Permanent},
		{name: "image part", parts: []provider.Part{{Image: &provider.Blob{MediaType: "image/png"}}}, kind: provider.Permanent, message: "text only"},
		{name: "unknown model", model: "other", kind: provider.Permanent, message: "no questions"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv, _ := providertest.Serve(t, providertest.Reply(tt.status, nil, tt.body))
			model := tt.model
			if model == "" {
				model = "jev"
			}
			parts := tt.parts
			if parts == nil {
				parts = []provider.Part{{Text: "x"}}
			}
			_, err := New(endpoint(srv.URL)).Complete(t.Context(), provider.Request{Model: model, Parts: parts})
			pe := providertest.AsError(t, err)
			if pe.Kind != tt.kind || !strings.Contains(pe.Error(), tt.message) {
				t.Errorf("error = %v (kind %s), want kind %s containing %q", err, pe.Kind, tt.kind, tt.message)
			}
		})
	}
}

func TestClient_Complete_auth(t *testing.T) {
	t.Parallel()
	srv, got := providertest.Serve(t, providertest.Reply(http.StatusOK, nil,
		`{"decisions": [{"question": "accuracy", "probability": 1}, {"question": "complete", "probability": 1}]}`))
	ep := providertest.Endpoint(t, "secret-key", "provider: classifier", "base_url: "+srv.URL, "api_key_env: CLASSIFIER_KEY",
		"models:", "  jev:", "    accepts: [text/plain]",
		`    questions: [{id: accuracy, text: "Accurate?", threshold: 0.5}, {id: complete, text: "Complete?", threshold: 0.5}]`)
	if _, err := New(ep).Complete(t.Context(), provider.Request{Model: "jev", Parts: []provider.Part{{Text: "x"}}}); err != nil {
		t.Fatal(err)
	}
	if auth := got.Header.Get("Authorization"); auth != "Bearer secret-key" {
		t.Errorf("Authorization = %q", auth)
	}
}

func TestVerdict(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		accuracy   float64
		complete   float64
		verdict    string
		confidence float64
	}{
		{"all pass", 0.95, 0.6, "accept", 0.6},
		{"at the threshold passes", 0.8, 0.5, "accept", 0.5},
		{"one below", 0.79, 0.99, "reject", 0.79},
		{"both below", 0.1, 0.2, "reject", 0.1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			text, err := Verdict(questions, []Decision{
				{Question: "accuracy", Probability: prob(tt.accuracy)},
				{Question: "complete", Probability: prob(tt.complete)},
				{Question: "unknown", Probability: prob(0)}, // ignored
			})
			if err != nil {
				t.Fatal(err)
			}
			var v verdictOut
			if err := json.Unmarshal([]byte(text), &v); err != nil {
				t.Fatal(err)
			}
			if v.Verdict != tt.verdict || v.Confidence != tt.confidence {
				t.Errorf("verdict = %s", text)
			}
			// Contract §7: a reject has at least one feedback entry; here exactly one, at "".
			if tt.verdict == "reject" {
				if len(v.Feedback) != 1 || !strings.HasPrefix(v.Feedback[""], WholeResultMessage) {
					t.Errorf("reject feedback = %v", v.Feedback)
				}
			} else if len(v.Feedback) != 0 {
				t.Errorf("accept feedback = %v", v.Feedback)
			}
		})
	}
}

func TestVerdict_duplicate(t *testing.T) {
	t.Parallel()
	_, err := Verdict(questions, []Decision{{Question: "accuracy", Probability: prob(1)}, {Question: "accuracy", Probability: prob(1)}})
	if err == nil {
		t.Fatal("duplicate decisions accepted")
	}
}
