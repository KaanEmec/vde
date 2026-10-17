package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/document"
	"github.com/kaanemec/vde/internal/extract"
	"github.com/kaanemec/vde/internal/provider"
	"github.com/kaanemec/vde/internal/provider/fake"
	"github.com/kaanemec/vde/internal/version"
)

// repoRoot is the repository root seen from this package's directory.
const repoRoot = "../.."

var (
	scenarioDir    = filepath.Join(repoRoot, "testdata", "scenarios")
	scenarioConfig = filepath.Join(scenarioDir, "config", "vde.yaml")
	samples        = filepath.Join(repoRoot, "examples", "samples")
)

// scenario is the second YAML document of a testdata/scenarios file; the first is the
// fake provider script, which fake.Load and --provider-script read alone. Every scenario
// runs with testdata/scenarios/config/vde.yaml: endpoints primary (model primary-model)
// and fallback (fallback-model) serve the extractor, judge (judge-model) the judge.
//
//	input:                       # what vde extract is given; paths from the repository root
//	  document: examples/samples/booking.txt
//	  type: text/plain           # optional, default from the extension
//	  pages: [...]               # optional page images
//	  schema: examples/samples/schema.json  # optional, absent means schema-less
//	  max_attempts: 3            # optional
//	  timeout: 1s                # optional job time limit
//	  html: visible_text         # optional HTML policy
//	expect:
//	  status: accepted           # accepted, rejected or failed
//	  attempts: 1
//	  error: ""                  # the job error code when failed
//	  judge_calls: 1
//	  extractor_calls: {primary: 1}  # per endpoint
//	  judge_confidence: null     # or the number of the last verdict
//	  feedback: [...]            # optional: the exact final feedback
//	  result: '{"a": 1}'         # optional: the final result, compared as JSON
//	  document_parts: [text/plain]  # optional: media types of the document parts sent
//	  same_text_as: examples/samples/booking.html  # optional: the text part ends with its text
type scenario struct {
	Input struct {
		Document    string        `yaml:"document"`
		Type        string        `yaml:"type"`
		Pages       []string      `yaml:"pages"`
		Schema      string        `yaml:"schema"`
		MaxAttempts int           `yaml:"max_attempts"`
		Timeout     time.Duration `yaml:"timeout"`
		HTML        string        `yaml:"html"`
	} `yaml:"input"`
	Expect struct {
		Status          extract.Status     `yaml:"status"`
		Attempts        int                `yaml:"attempts"`
		Error           string             `yaml:"error"` // job error code
		JudgeCalls      int                `yaml:"judge_calls"`
		ExtractorCalls  map[string]int     `yaml:"extractor_calls"` // per endpoint
		JudgeConfidence *float64           `yaml:"judge_confidence"`
		Feedback        []extract.Feedback `yaml:"feedback"` // compared when present
		Result          string             `yaml:"result"`   // JSON, compared when present
		DocumentParts   []string           `yaml:"document_parts"`
		SameTextAs      string             `yaml:"same_text_as"`
	} `yaml:"expect"`
}

func loadScenario(t *testing.T, path string) scenario {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var script yaml.Node
	if err := dec.Decode(&script); err != nil {
		t.Fatalf("script document: %v", err)
	}
	dec.KnownFields(true)
	var sc scenario
	if err := dec.Decode(&sc); err != nil {
		t.Fatalf("scenario document (after ---): %v", err)
	}
	if sc.Input.Document == "" || sc.Expect.Status == "" {
		t.Fatal("scenario needs input.document and expect.status")
	}
	return sc
}

// observedRun is what one run of a scenario produced.
type observedRun struct {
	out            jobOutput
	judgeCalls     int
	extractorCalls map[string]int
}

// TestScenarios runs every testdata/scenarios file through extract.Run directly and
// through the vde binary built by TestMain, and checks both against the scenario's
// expectations and against each other.
func TestScenarios(t *testing.T) {
	t.Parallel()
	paths, err := filepath.Glob(filepath.Join(scenarioDir, "*.yaml"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no scenarios: %v", err)
	}
	for _, path := range paths {
		name := strings.TrimSuffix(filepath.Base(path), ".yaml")
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			sc := loadScenario(t, path)
			direct := runScenarioDirect(t, path, sc)
			checkScenario(t, "extract.Run", sc, direct)
			cli := runScenarioCLI(t, path, sc)
			checkScenario(t, "vde extract", sc, cli)
			compareRuns(t, direct.out, cli.out)
			t.Logf("%s after %d attempt(s), judge calls %d, extractor calls %v",
				summary(cli.out), cli.out.Attempts, cli.judgeCalls, cli.extractorCalls)
		})
	}
}

func summary(out jobOutput) string {
	if out.Error != nil {
		return string(out.Status) + " (" + out.Error.Code + ")"
	}
	return string(out.Status)
}

// runScenarioDirect wires the engine as the command does, without the command: a fake
// from the script behind every endpoint, then document.SelectWith and extract.Run.
func runScenarioDirect(t *testing.T, path string, sc scenario) observedRun {
	t.Helper()
	cfg, _, err := loadConfig(scenarioConfig, false)
	if err != nil {
		t.Fatal(err)
	}
	f, err := fake.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	roles, err := extract.NewRoles(cfg, func(*config.Endpoint) (provider.Provider, error) { return f, nil })
	if err != nil {
		t.Fatal(err)
	}
	doc := newScenarioDocument(t, sc.Input.Document, sc.Input.Type, sc.Input.Pages, cfg.Limits)
	policy := document.HTMLPolicy(cfg.Documents.HTML)
	if sc.Input.HTML != "" {
		policy = document.HTMLPolicy(sc.Input.HTML)
	}
	// Every group that may serve a role (escalation, second judge, feedback group) must
	// read the representation.
	extractorCaps, judgeCaps := extract.RoleCapabilities(cfg)
	rep, err := document.SelectWith(doc, extractorCaps, judgeCaps, policy, document.NewImagePolicy(cfg.Documents.Images))
	if err != nil {
		t.Fatal(err)
	}
	var schema *extract.Schema
	if sc.Input.Schema != "" {
		if schema, err = extract.CompileSchema(readFile(t, sc.Input.Schema)); err != nil {
			t.Fatal(err)
		}
	}
	timeout := cfg.Limits.JobTimeout
	if sc.Input.Timeout > 0 {
		timeout = sc.Input.Timeout
	}
	maxAttempts := cfg.Limits.MaxAttempts
	if sc.Input.MaxAttempts > 0 {
		maxAttempts = sc.Input.MaxAttempts
	}
	res := extract.Run(context.Background(), extract.Input{
		Representation:        rep,
		ExtractorInstructions: string(readFile(t, "examples/samples/extractor.md")),
		JudgeInstructions:     string(readFile(t, "examples/samples/judge.md")),
		Schema:                schema,
		MaxAttempts:           maxAttempts,
		Deadline:              time.Now().Add(timeout),
	}, roles)

	endpointOf := map[string]string{} // model -> endpoint; models are unique in the config
	for name, e := range cfg.Endpoints {
		for model := range e.Models {
			endpointOf[model] = name
		}
	}
	run := observedRun{
		out: jobOutput{
			Status: res.Status, Result: res.Result, Feedback: res.Feedback, Attempts: res.Attempts,
			ServiceVersion: extract.ServiceVersion(cfg, version.Version), JudgeConfidence: res.JudgeConfidence, Error: res.Error,
			Verification: res.Verification,
		},
		extractorCalls: map[string]int{},
	}
	calls := f.Calls()
	// The command's usage counts every call, failed ones (no tokens) included.
	run.out.Usage = extract.Usage{InputTokens: res.Usage.InputTokens, OutputTokens: res.Usage.OutputTokens, Calls: len(calls)}
	for _, c := range calls {
		if roleOf(c.Request.System) == roleJudge {
			run.judgeCalls++
		} else {
			run.extractorCalls[endpointOf[c.Request.Model]]++
		}
	}
	if len(calls) > 0 {
		checkDocumentParts(t, sc, calls[0], policy)
	}
	return run
}

// checkDocumentParts checks the parts after the "Document:" separator of the first call.
func checkDocumentParts(t *testing.T, sc scenario, c fake.Call, policy document.HTMLPolicy) {
	t.Helper()
	start := slices.IndexFunc(c.Request.Parts, func(p provider.Part) bool { return p.Text == "Document:" })
	if start < 0 {
		t.Fatal("no Document: separator in the first request")
	}
	var types []string
	var text string
	for _, p := range c.Request.Parts[start+1:] {
		switch {
		case p.Document != nil:
			types = append(types, p.Document.MediaType)
		case p.Image != nil:
			types = append(types, p.Image.MediaType)
		default:
			types = append(types, "text/plain")
			text = p.Text
		}
		if p.Document == nil && p.Image == nil {
			break // the document is one text part; what follows is the previous candidate or feedback
		}
	}
	if sc.Expect.DocumentParts != nil && !slices.Equal(types, sc.Expect.DocumentParts) {
		t.Errorf("document parts = %v, want %v", types, sc.Expect.DocumentParts)
	}
	if sc.Expect.SameTextAs != "" {
		other := newScenarioDocument(t, sc.Expect.SameTextAs, "", nil, config.Limits{})
		want, err := other.Text(policy)
		if err != nil {
			t.Fatal(err)
		}
		// An email's body keeps the message's CRLF line endings; the HTML file has LF.
		if !strings.HasSuffix(strings.ReplaceAll(text, "\r\n", "\n"), "\n"+want) {
			t.Errorf("the document text part does not end with the text of %s (%d bytes vs %d)", sc.Expect.SameTextAs, len(text), len(want))
		}
	}
}

// runScenarioCLI runs the scenario through the vde binary with --verbose and counts the
// calls from its progress lines.
func runScenarioCLI(t *testing.T, path string, sc scenario) observedRun {
	t.Helper()
	args := []string{"extract",
		"--config", scenarioConfig,
		"--document", filepath.Join(repoRoot, sc.Input.Document),
		"--extractor-instructions", filepath.Join(samples, "extractor.md"),
		"--judge-instructions", filepath.Join(samples, "judge.md"),
		"--provider-script", path,
		"--verbose",
	}
	if sc.Input.Type != "" {
		args = append(args, "--type", sc.Input.Type)
	}
	for _, p := range sc.Input.Pages {
		args = append(args, "--page", filepath.Join(repoRoot, p))
	}
	if sc.Input.Schema != "" {
		args = append(args, "--schema", filepath.Join(repoRoot, sc.Input.Schema))
	}
	if sc.Input.MaxAttempts > 0 {
		args = append(args, "--max-attempts", fmt.Sprint(sc.Input.MaxAttempts))
	}
	if sc.Input.Timeout > 0 {
		args = append(args, "--timeout", sc.Input.Timeout.String())
	}
	if sc.Input.HTML != "" {
		args = append(args, "--html", sc.Input.HTML)
	}
	cmd := exec.Command(vdeBinary, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code = exitErr.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}

	run := observedRun{extractorCalls: map[string]int{}}
	if err := json.Unmarshal(stdout.Bytes(), &run.out); err != nil {
		t.Fatalf("stdout is not a job JSON object: %v\nstderr:\n%s", err, &stderr)
	}
	wantCode := map[extract.Status]int{extract.StatusAccepted: 0, extract.StatusRejected: 2, extract.StatusFailed: 3}[run.out.Status]
	if code != wantCode {
		t.Errorf("exit code = %d, want %d for %s", code, wantCode, run.out.Status)
	}
	if run.out.FinishedAt.IsZero() {
		t.Error("finished_at is missing")
	}
	for _, secret := range []string{"HV-48213", "Jane Example", "Ada Lovelace", "Harbour View"} {
		if strings.Contains(stderr.String(), secret) {
			t.Errorf("stderr contains document or candidate content %q", secret)
		}
	}
	for _, m := range callLine.FindAllStringSubmatch(stderr.String(), -1) {
		if m[1] == roleJudge {
			run.judgeCalls++
		} else {
			run.extractorCalls[m[2]]++
		}
	}
	return run
}

// callLine matches the --verbose line of one model call: role and endpoint.
var callLine = regexp.MustCompile(`(?m)^vde: (extractor|judge) call ([^/\s]+)/`)

func checkScenario(t *testing.T, via string, sc scenario, run observedRun) {
	t.Helper()
	e, out := sc.Expect, run.out
	if out.Status != e.Status || out.Attempts != e.Attempts {
		t.Errorf("%s: %s after %d attempt(s), want %s after %d", via, out.Status, out.Attempts, e.Status, e.Attempts)
	}
	code := ""
	if out.Error != nil {
		code = out.Error.Code
	}
	if code != e.Error {
		t.Errorf("%s: error code %q, want %q", via, code, e.Error)
	}
	if (out.Error != nil) != (out.Status == extract.StatusFailed) {
		t.Errorf("%s: error %+v with status %s", via, out.Error, out.Status)
	}
	if out.Status == extract.StatusAccepted && out.Result == nil {
		t.Errorf("%s: accepted without a result", via)
	}
	if run.judgeCalls != e.JudgeCalls {
		t.Errorf("%s: %d judge call(s), want %d", via, run.judgeCalls, e.JudgeCalls)
	}
	if !maps.Equal(run.extractorCalls, e.ExtractorCalls) {
		t.Errorf("%s: extractor calls %v, want %v", via, run.extractorCalls, e.ExtractorCalls)
	}
	if !equalConfidence(out.JudgeConfidence, e.JudgeConfidence) {
		t.Errorf("%s: judge_confidence %v, want %v", via, deref(out.JudgeConfidence), deref(e.JudgeConfidence))
	}
	if e.Feedback != nil && !reflect.DeepEqual(out.Feedback, e.Feedback) {
		t.Errorf("%s: feedback %+v, want %+v", via, out.Feedback, e.Feedback)
	}
	if e.Result != "" && !equalJSON(t, out.Result, json.RawMessage(e.Result)) {
		t.Errorf("%s: result %s, want %s", via, out.Result, e.Result)
	}
}

// compareRuns checks that the command printed what extract.Run returned.
func compareRuns(t *testing.T, direct, cli jobOutput) {
	t.Helper()
	if direct.Status != cli.Status || direct.Attempts != cli.Attempts ||
		!reflect.DeepEqual(direct.Error, cli.Error) || !reflect.DeepEqual(direct.Feedback, cli.Feedback) ||
		!equalConfidence(direct.JudgeConfidence, cli.JudgeConfidence) || direct.ServiceVersion != cli.ServiceVersion ||
		!equalJSON(t, direct.Result, cli.Result) || direct.Usage != cli.Usage || direct.Verification != cli.Verification {
		t.Errorf("extract.Run and vde extract differ:\n%+v\n%+v", direct, cli)
	}
}

func equalConfidence(a, b *float64) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

func deref(f *float64) any {
	if f == nil {
		return nil
	}
	return *f
}

func equalJSON(t *testing.T, a, b json.RawMessage) bool {
	t.Helper()
	decode := func(m json.RawMessage) any {
		if m == nil {
			return nil
		}
		var v any
		if err := json.Unmarshal(m, &v); err != nil {
			t.Fatalf("invalid JSON %s: %v", m, err)
		}
		return v
	}
	return reflect.DeepEqual(decode(a), decode(b))
}

func readFile(t *testing.T, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot, rel))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func newScenarioDocument(t *testing.T, rel, mediaType string, pages []string, limits config.Limits) *document.Document {
	t.Helper()
	if mediaType == "" {
		mediaType = mediaTypesByExtension[strings.ToLower(filepath.Ext(rel))]
	}
	in := document.Input{MediaType: mediaType, Data: readFile(t, rel), Filename: filepath.Base(rel)}
	for _, p := range pages {
		in.Pages = append(in.Pages, document.Page{Data: readFile(t, p)})
	}
	doc, err := document.New(in, limits)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

// TestRunExtract covers the command's flags, usage errors and exit codes in process.
func TestRunExtract(t *testing.T) {
	t.Parallel()
	script := filepath.Join(scenarioDir, "accept-first-try.yaml")
	rejected := filepath.Join(scenarioDir, "three-rejections.yaml")
	failed := filepath.Join(scenarioDir, "judge-garbage-twice.yaml")
	common := func(doc string, extra ...string) []string {
		return append([]string{"extract", "--config", scenarioConfig, "--document", doc,
			"--extractor-instructions", filepath.Join(samples, "extractor.md"),
			"--judge-instructions", filepath.Join(samples, "judge.md")}, extra...)
	}
	txt := filepath.Join(samples, "booking.txt")
	schema := filepath.Join(samples, "schema.json")
	badSchema := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(badSchema, []byte(`{"type": 12}`), 0o600); err != nil {
		t.Fatal(err)
	}
	unknownExt := filepath.Join(t.TempDir(), "booking.xyz")
	if err := os.WriteFile(unknownExt, []byte("Guest: Jane Example, 3 nights"), 0o600); err != nil { // grounds the script's candidate
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout []string
		wantStderr []string
	}{
		{
			name:     "accepted",
			args:     common(txt, "--provider-script", script),
			wantCode: exitOK,
			wantStdout: []string{`"status": "accepted"`, `"attempts": 1`, `"service_version": "vde-0.0.0-dev+`, `"finished_at": "`, `"judge_confidence": null`, `"error": null`,
				`"usage": {
    "input_tokens": 200,
    "output_tokens": 40,
    "calls": 2
  }`},
			wantStderr: []string{`service_version=vde-0.0.0-dev+`},
		},
		{
			name:       "rejected exits 2",
			args:       common(txt, "--provider-script", rejected, "--schema", schema),
			wantCode:   exitRejected,
			wantStdout: []string{`"status": "rejected"`, `"attempts": 3`, `"source": "judge"`},
		},
		{
			name:       "failed exits 3",
			args:       common(txt, "--provider-script", failed),
			wantCode:   exitFailed,
			wantStdout: []string{`"status": "failed"`, `"code": "judge_failed"`},
		},
		{
			name:       "verbose progress",
			args:       common(txt, "--provider-script", rejected, "--schema", schema, "--verbose"),
			wantCode:   exitRejected,
			wantStderr: []string{"vde: document text/plain, text representation", "vde: extractor call primary/primary-model: ok", "vde: attempt 1: judge rejected the candidate, 1 feedback entries", "vde: attempt 3: rejected", "vde: usage: 6 call(s), 600 input tokens, 120 output tokens"},
		},
		{
			name:       "email lists attachments",
			args:       common(filepath.Join(samples, "booking.eml"), "--provider-script", script),
			wantCode:   exitOK,
			wantStdout: []string{`"attachments": [`, `"name": "booking.pdf"`, `"media_type": "application/pdf"`},
		},
		{
			name:       "explicit type",
			args:       common(unknownExt, "--type", "text/plain", "--provider-script", script),
			wantCode:   exitOK,
			wantStdout: []string{`"status": "accepted"`},
		},
		{
			name:       "unknown extension needs --type",
			args:       common(unknownExt, "--provider-script", script),
			wantCode:   exitUsage,
			wantStderr: []string{`--type is required: no media type known for extension ".xyz"`},
		},
		{
			name:       "fake endpoints need a script",
			args:       common(txt),
			wantCode:   exitUsage,
			wantStderr: []string{"provider fake needs --provider-script"},
		},
		{
			name:       "invalid schema",
			args:       common(txt, "--provider-script", script, "--schema", badSchema),
			wantCode:   exitUsage,
			wantStderr: []string{"invalid schema"},
		},
		{
			name:       "max attempts above the configured limit",
			args:       common(txt, "--provider-script", script, "--max-attempts", "9"),
			wantCode:   exitUsage,
			wantStderr: []string{"--max-attempts 9: must be from 1 to limits.max_attempts (3)"},
		},
		{
			name:       "unknown html policy",
			args:       common(txt, "--provider-script", script, "--html", "pretty"),
			wantCode:   exitUsage,
			wantStderr: []string{`unknown HTML policy "pretty"`},
		},
		{
			name:       "page that is not an image",
			args:       common(filepath.Join(samples, "booking.pdf"), "--provider-script", script, "--page", txt),
			wantCode:   exitUsage,
			wantStderr: []string{"vde extract: document:"},
		},
		{
			name:       "missing script",
			args:       common(txt, "--provider-script", filepath.Join(t.TempDir(), "none.yaml")),
			wantCode:   exitUsage,
			wantStderr: []string{"no such file"},
		},
		{
			name:       "missing document flag",
			args:       []string{"extract", "--extractor-instructions", "x", "--judge-instructions", "y"},
			wantCode:   exitUsage,
			wantStderr: []string{"--document is required"},
		},
		{
			name:       "missing instructions flag",
			args:       []string{"extract", "--document", txt, "--judge-instructions", "y"},
			wantCode:   exitUsage,
			wantStderr: []string{"--extractor-instructions is required"},
		},
		{
			name:       "unexpected argument",
			args:       []string{"extract", "extra"},
			wantCode:   exitUsage,
			wantStderr: []string{`unexpected argument "extra"`},
		},
		{
			name:       "help",
			args:       []string{"extract", "-h"},
			wantCode:   exitOK,
			wantStderr: []string{"Exit codes: 0 accepted, 2 rejected, 3 failed, 4 extracted", "-provider-script", "-judge"},
		},
		{
			name: "bad judge policy",
			args: []string{"extract", "--document", "d.txt", "--extractor-instructions", "e.md",
				"--judge-instructions", "j.md", "--judge", "sometimes"},
			wantCode:   exitUsage,
			wantStderr: []string{`vde extract: --judge: judge policy "sometimes" is not one of always, never, auto`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			code := run(tt.args, &stdout, &stderr)
			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s", code, tt.wantCode, &stdout, &stderr)
			}
			for _, w := range tt.wantStdout {
				if !strings.Contains(stdout.String(), w) {
					t.Errorf("stdout does not contain %q:\n%s", w, &stdout)
				}
			}
			for _, w := range tt.wantStderr {
				if !strings.Contains(stderr.String(), w) {
					t.Errorf("stderr does not contain %q:\n%s", w, &stderr)
				}
			}
		})
	}
}

func TestMediaTypesByExtension(t *testing.T) {
	t.Parallel()
	for ext, mt := range mediaTypesByExtension {
		if _, _, err := document.LookupMediaType(mt); err != nil {
			t.Errorf("%s: %s is not an accepted media type: %v", ext, mt, err)
		}
	}
	for _, mt := range document.MediaTypes() {
		if !slices.Contains(slices.Collect(maps.Values(mediaTypesByExtension)), mt.Name) {
			t.Errorf("no extension maps to accepted media type %s", mt.Name)
		}
	}
}

// TestExtractJudgeFlag: --judge reaches extract.Input and the service_version of the run
// is the policy's; the result carries verification. On booking.txt with the grounded
// candidate of accept-first-try, always asks the judge, never ends extracted without it,
// and auto accepts on grounding alone (the document's bytes are not sampled at 5 %).
func TestExtractJudgeFlag(t *testing.T) {
	t.Parallel()
	cfg, _, err := loadConfigEnv(scenarioConfig, false, func(string) string { return "unused" })
	if err != nil {
		t.Fatal(err)
	}
	never, auto := *cfg, *cfg
	never.Roles.Judge.Policy = config.JudgeNever
	auto.Roles.Judge.Policy = config.JudgeAuto
	for _, tt := range []struct {
		flag             string
		wantPolicy       config.JudgePolicy
		wantVersion      string
		wantStatus       extract.Status
		wantVerification string
		wantCalls        int // provider calls: the extractor's, and the judge's when asked
	}{
		{"", config.JudgeAlways, extract.ServiceVersion(cfg, version.Version), extract.StatusAccepted, extract.VerificationJudge, 2},
		{"never", config.JudgeNever, extract.ServiceVersion(&never, version.Version), extract.StatusExtracted, extract.VerificationNone, 1},
		{"auto", config.JudgeAuto, extract.ServiceVersion(&auto, version.Version), extract.StatusAccepted, extract.VerificationGrounded, 1},
	} {
		t.Run("judge="+tt.flag, func(t *testing.T) {
			t.Parallel()
			var got extract.Input
			o := extractOptions{
				config:                scenarioConfig,
				document:              filepath.Join(samples, "booking.txt"),
				extractorInstructions: filepath.Join(samples, "extractor.md"),
				judgeInstructions:     filepath.Join(samples, "judge.md"),
				providerScript:        filepath.Join(scenarioDir, "accept-first-try.yaml"),
				judge:                 tt.flag,
				run: func(ctx context.Context, in extract.Input, roles extract.Roles) extract.Outcome {
					got = in
					return extract.Run(ctx, in, roles)
				},
			}
			out, err := extractFile(t.Context(), o, io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			if got.JudgePolicy != tt.wantPolicy {
				t.Errorf("Input.JudgePolicy = %q, want %q", got.JudgePolicy, tt.wantPolicy)
			}
			if out.ServiceVersion != tt.wantVersion {
				t.Errorf("service_version = %s, want %s", out.ServiceVersion, tt.wantVersion)
			}
			if out.Status != tt.wantStatus || out.Verification != tt.wantVerification || out.Usage.Calls != tt.wantCalls {
				t.Errorf("status %s, verification %q, %d call(s); want %s, %s, %d", out.Status, out.Verification,
					out.Usage.Calls, tt.wantStatus, tt.wantVerification, tt.wantCalls)
			}
			if len(out.Feedback) != 0 {
				t.Errorf("feedback %+v, want none on a grounded candidate", out.Feedback)
			}
		})
	}
	if extract.ServiceVersion(cfg, version.Version) == extract.ServiceVersion(&never, version.Version) {
		t.Error("never and always share a service_version")
	}
}
