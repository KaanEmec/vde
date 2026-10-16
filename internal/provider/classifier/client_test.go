package classifier

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/provider"
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
	var got Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != Path || r.Header.Get("Content-Type") != "application/json" {
			http.Error(w, "bad route", http.StatusNotFound)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(Response{Decisions: []Decision{
			{Question: "complete", Label: "yes", Probability: prob(0.9)},
			{Question: "accuracy", Label: "yes", Probability: prob(0.85)},
		}})
	}))
	defer srv.Close()

	c := New(endpoint(srv.URL))
	resp, err := c.Complete(t.Context(), provider.Request{
		Model:  "jev",
		System: "You are the judge",
		Parts:  []provider.Part{{Text: "Document:"}, {Text: "Ada"}, {Text: `Candidate:` + "\n\n" + `{"name": "Ada"}`}},
	})
	if err != nil {
		t.Fatal(err)
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
	if v.Verdict != "accept" || len(v.Feedback) != 0 || v.Confidence != 0.85 || resp.Model != "jev" {
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
		{name: "malformed", status: 200, body: "not json", kind: provider.Permanent, message: "malformed response"},
		{name: "missing decision", status: 200, body: `{"decisions": [{"question": "accuracy", "probability": 1}]}`,
			kind: provider.Permanent, message: "no decision for question complete"},
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
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			}))
			defer srv.Close()
			model := tt.model
			if model == "" {
				model = "jev"
			}
			parts := tt.parts
			if parts == nil {
				parts = []provider.Part{{Text: "x"}}
			}
			_, err := New(endpoint(srv.URL)).Complete(t.Context(), provider.Request{Model: model, Parts: parts})
			var pe *provider.Error
			if !errors.As(err, &pe) {
				t.Fatalf("error = %v, want a *provider.Error", err)
			}
			if pe.Kind != tt.kind || !strings.Contains(pe.Error(), tt.message) {
				t.Errorf("error = %v (kind %s), want kind %s containing %q", err, pe.Kind, tt.kind, tt.message)
			}
		})
	}
}

func TestClient_Complete_auth(t *testing.T) {
	t.Parallel()
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, `{"decisions": [{"question": "accuracy", "probability": 1}, {"question": "complete", "probability": 1}]}`)
	}))
	defer srv.Close()
	cfg, err := config.Parse([]byte(`
endpoints:
  jev:
    provider: classifier
    base_url: `+srv.URL+`
    api_key_env: CLASSIFIER_KEY
    models:
      jev:
        accepts: [text/plain]
        questions: [{id: accuracy, text: "Accurate?", threshold: 0.5}, {id: complete, text: "Complete?", threshold: 0.5}]
`), func(string) string { return "secret-key" })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(cfg.Endpoints["jev"]).Complete(t.Context(), provider.Request{Model: "jev", Parts: []provider.Part{{Text: "x"}}}); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer secret-key" {
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
