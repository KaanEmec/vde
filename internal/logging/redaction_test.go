package logging_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/document"
	"github.com/kaanemec/vde/internal/extract"
	"github.com/kaanemec/vde/internal/logging"
	"github.com/kaanemec/vde/internal/provider"
	"github.com/kaanemec/vde/internal/provider/fake"
)

// Planted sentinels. Each is unique per place so a failure names the leak's source.
const (
	sentDocument     = "SENTINEL-DOCUMENT-6f1c"
	sentExtractorIns = "SENTINEL-EXTRACTOR-INSTRUCTIONS-2b9e"
	sentJudgeIns     = "SENTINEL-JUDGE-INSTRUCTIONS-8d40"
	sentSchema       = "SENTINEL-SCHEMA-c3a7"
	sentCandidate    = "SENTINEL-CANDIDATE-51e2"
	sentFeedback     = "SENTINEL-FEEDBACK-e97b"
	sentGarbage      = "SENTINEL-NOTJSON-04ad"
)

var sentinels = []string{
	sentDocument, sentExtractorIns, sentJudgeIns, sentSchema, sentCandidate, sentFeedback, sentGarbage,
}

const cfgYAML = `
endpoints:
  primary:
    provider: fake
    models: {primary-model: {accepts: [text/plain]}}
  fallback:
    provider: fake
    models: {fallback-model: {accepts: [text/plain]}}
  review:
    provider: fake
    models: {judge-model: {accepts: [text/plain]}}
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

var schema = `{"type": "object", "description": "` + sentSchema + `", "required": ["name"],
	"additionalProperties": false, "properties": {"name": {"type": "string", "description": "` + sentSchema + `"}}}`

// panicker is a provider whose code fails with a runtime error.
type panicker struct{}

func (panicker) Complete(context.Context, provider.Request) (provider.Response, error) {
	var s []int
	return provider.Response{Text: strconv.Itoa(s[len(sentinels)])}, nil // index out of range
}

// lockedBuffer is a bytes.Buffer safe for the concurrent writes of the loop.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func answer(name string) string { return `{"name": "` + name + sentCandidate + `"}` }

func reject(msg string) string {
	return `{"verdict": "reject", "feedback": {"/name": "` + msg + `"}}`
}

// TestNoSentinelInLogs runs the extraction loop through its logging paths, with the
// service logger as Input.Logger and a separate capture as slog's default, and asserts that no
// content marker reaches a log line at debug level, the most verbose. The document is
// groundable text, so every run also goes through the grounding check (ADR 0009): its
// text (Representation.GroundText, the sentDocument line) never reaches a log, and its
// feedback entries never carry a value.
func TestNoSentinelInLogs(t *testing.T) {
	cfg, err := config.Parse([]byte(cfgYAML), func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate().Err(false); err != nil {
		t.Fatal(err)
	}
	doc, err := document.New(document.Input{
		MediaType: "text/plain; charset=utf-8",
		// The document prints the candidates answer("a") and answer("b"), so they are
		// grounded; answer("x") is not.
		Data: []byte("Contract for " + sentDocument + ", signed by a" + sentCandidate + " and b" + sentCandidate),
	}, cfg.Limits)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := document.Select(t.Context(), doc, cfg.Capabilities("extraction"), cfg.Capabilities("review"), document.VisibleText, document.ImagePolicy{}, document.PDFPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := extract.CompileSchema([]byte(schema))
	if err != nil {
		t.Fatal(err)
	}
	httpFail := func() fake.Step {
		s := fake.Fail(provider.Retryable, 503)
		s.Repeat = true
		return s
	}

	tests := []struct {
		name                     string
		primary, fallback, judge provider.Provider
		policy                   config.JudgePolicy
		wantStatus               extract.Status
		wantLogged               bool // the scenario hits a log statement
		withSchema               bool
	}{
		{
			name:       "grounding repair then accept",
			primary:    fake.New(fake.Script{fake.Answer(answer("x")), fake.Answer(answer("a"))}),
			judge:      fake.New(fake.Script{fake.Answer(fake.AcceptVerdict)}),
			withSchema: true,
			wantStatus: extract.StatusAccepted,
		},
		{
			name:       "never with advisory grounding entries",
			primary:    fake.New(fake.Script{fake.Answer(answer("x"))}),
			judge:      fake.New(nil),
			policy:     config.JudgeNever,
			withSchema: true,
			wantStatus: extract.StatusExtracted,
		},
		{
			name:       "auto grounded without a judge",
			primary:    fake.New(fake.Script{fake.Answer(answer("a"))}),
			judge:      fake.New(nil),
			policy:     config.JudgeAuto,
			withSchema: true,
			wantStatus: extract.StatusAccepted,
		},
		{
			name:       "auto ungrounded after the repair, judge rejects",
			primary:    fake.New(fake.Script{{Answer: answer("x"), Repeat: true}}),
			judge:      fake.New(fake.Script{{Answer: reject(sentFeedback), Repeat: true}}),
			policy:     config.JudgeAuto,
			wantStatus: extract.StatusRejected,
		},
		{
			name:       "accepted",
			primary:    fake.New(fake.Script{fake.Answer(answer("a"))}),
			judge:      fake.New(fake.Script{fake.Answer(fake.AcceptVerdict)}),
			withSchema: true,
			wantStatus: extract.StatusAccepted,
		},
		{
			name: "reject then accept with sentinel feedback",
			primary: fake.New(fake.Script{
				fake.Answer(answer("a")), fake.Answer(answer("b")),
			}),
			judge: fake.New(fake.Script{
				fake.Answer(reject(sentFeedback)), fake.Answer(fake.AcceptVerdict),
			}),
			withSchema: true,
			wantStatus: extract.StatusAccepted,
		},
		{
			name: "answers that are not JSON or not valid",
			primary: fake.New(fake.Script{
				fake.Answer(sentGarbage), fake.Answer(`{"name": 7, "x": "` + sentCandidate + `"}`),
				fake.Answer(sentGarbage),
			}),
			judge:      fake.New(nil),
			withSchema: true,
			wantStatus: extract.StatusRejected,
		},
		{
			name:       "judge reject on every attempt",
			primary:    fake.New(fake.Script{fake.Answer(answer("a")), fake.Answer(answer("a")), fake.Answer(answer("a"))}),
			judge:      fake.New(fake.Script{{Answer: reject(sentFeedback), Repeat: true}}),
			wantStatus: extract.StatusRejected,
		},
		{
			name:       "judge confidence is not a number",
			primary:    fake.New(fake.Script{fake.Answer(answer("a"))}),
			judge:      fake.New(fake.Script{fake.Answer(`{"verdict": "accept", "confidence": "` + sentFeedback + `", "feedback": {}}`)}),
			wantStatus: extract.StatusAccepted,
		},
		{
			name:       "unparseable verdicts",
			primary:    fake.New(fake.Script{fake.Answer(answer("a"))}),
			judge:      fake.New(fake.Script{{Answer: sentGarbage, Repeat: true}}),
			wantStatus: extract.StatusFailed,
		},
		{
			name:       "every endpoint fails",
			primary:    fake.New(fake.Script{httpFail()}),
			fallback:   fake.New(fake.Script{httpFail()}),
			judge:      fake.New(nil),
			wantStatus: extract.StatusFailed,
			wantLogged: true,
		},
		{
			name:       "permanent provider failure",
			primary:    fake.New(fake.Script{fake.Fail(provider.Permanent, 400)}),
			judge:      fake.New(nil),
			wantStatus: extract.StatusFailed,
		},
		{
			name:       "provider code panics",
			primary:    panicker{},
			judge:      fake.New(nil),
			wantStatus: extract.StatusFailed,
			wantLogged: true,
		},
	}

	var logged bool
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The loop logs through Input.Logger, as the worker and the CLI pass it; the
			// process default captures separately, so a call site that bypasses the
			// redacting logger fails the test instead of hiding behind it.
			var out, stray lockedBuffer
			prev := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&stray, &slog.HandlerOptions{Level: slog.LevelDebug})))
			t.Cleanup(func() { slog.SetDefault(prev) })

			fallback := tt.fallback
			if fallback == nil {
				fallback = fake.New(nil)
			}
			byEndpoint := map[*config.Endpoint]provider.Provider{
				cfg.Endpoints["primary"]:  tt.primary,
				cfg.Endpoints["fallback"]: fallback,
				cfg.Endpoints["review"]:   tt.judge,
			}
			roles, err := extract.NewRoles(cfg, func(e *config.Endpoint) (provider.Provider, error) {
				if p, ok := byEndpoint[e]; ok {
					return p, nil
				}
				return nil, errors.New("no provider")
			})
			if err != nil {
				t.Fatal(err)
			}
			in := extract.Input{
				Representation:        rep,
				ExtractorInstructions: "Extract the name. " + sentExtractorIns,
				JudgeInstructions:     "Check the name. " + sentJudgeIns,
				MaxAttempts:           3,
				JudgePolicy:           tt.policy,
				Logger:                logging.New(&out, slog.LevelDebug, "json"),
			}
			if tt.withSchema {
				in.Schema = compiled
			}
			got := extract.Run(t.Context(), in, roles)
			if got.Status != tt.wantStatus {
				t.Fatalf("status = %s (%+v), want %s", got.Status, got.Error, tt.wantStatus)
			}
			for _, f := range got.Feedback {
				for _, s := range sentinels {
					if f.Source == extract.SourceGrounding && strings.Contains(f.Pointer+f.Message, s) {
						t.Errorf("grounding feedback %+v carries %q", f, s)
					}
				}
			}
			if s := stray.String(); s != "" {
				t.Errorf("the loop logged through slog.Default, bypassing Input.Logger:\n%s", s)
			}
			text := out.String()
			if text != "" {
				logged = true
			}
			if tt.wantLogged && text == "" {
				t.Error("the scenario logged nothing, so the capture proves nothing")
			}
			for _, s := range sentinels {
				if strings.Contains(text, s) {
					t.Errorf("sentinel %q leaked into the log:\n%s", s, text)
				}
			}
		})
	}
	if !logged {
		t.Fatal("no scenario produced a log line; the capture is broken")
	}
}

// TestSentinelDroppedWhenMisused plants a sentinel the way a future mistake would (an
// unknown key, a long value under an allowed key) and asserts the safety net holds.
func TestSentinelDroppedWhenMisused(t *testing.T) {
	var out lockedBuffer
	l := logging.New(&out, slog.LevelDebug, "json")
	l.Error("worker: job finished", "job_id", "j", "document", sentDocument, "feedback", sentFeedback,
		"error", errors.New(strings.Repeat("e", 300)+sentCandidate))
	text := out.String()
	for _, s := range []string{sentDocument, sentFeedback, sentCandidate} {
		if strings.Contains(text, s) {
			t.Errorf("sentinel %q leaked: %s", s, text)
		}
	}
	if !strings.Contains(text, `"job_id":"j"`) {
		t.Errorf("allowed attribute missing: %s", text)
	}
}
