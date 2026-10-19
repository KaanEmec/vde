package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/document"
	"github.com/kaanemec/vde/internal/eval"
	"github.com/kaanemec/vde/internal/extract"
	"github.com/kaanemec/vde/internal/job"
	"github.com/kaanemec/vde/internal/pdf"
	"github.com/kaanemec/vde/internal/provider"
	"github.com/kaanemec/vde/internal/provider/fake"
)

const evalUsage = `Usage: vde eval --corpus dir [--config file | --url URL --token-env NAME]
                [--concurrency n] [--cases glob,...] [--judge always|never|auto]
                [--out dir] [--compare previous.json] [--provider-script file]
       vde eval import-travelinbox --from dir --to dir

Runs every case of a corpus (docs/operator/evaluation.md) through the engine, or through a
deployment with --url, compares each result with the case's expected.json under its
compare.yaml and prints a table and a summary: false accepts (accepted, but a compared
field differs from the ground truth, split by what accepted it: the judge or the grounding
check) first, then acceptance rate, verification, judge calls, field accuracy, false
rejects, durations and tokens. The JSON report is saved as <out>/<service_version>-<date>.json;
--compare prints the change against an earlier report. Only case ids, statuses, counts
and JSON pointers are printed or saved, never document content or result values.

--cases selects the cases whose id matches any of a comma-separated list of globs
('ti-0*,receipt-*'). --judge runs every case under that judge policy (default:
roles.judge.policy; ADR 0009) so always, never and auto can be compared on one corpus; the
sample of auto is decided on each document's bytes, so a rerun samples the same cases.
With --url it sends the judge part, which the application must allow
(applications[].judge.allow_override).

--provider-script replays a fake provider script for every case (a fresh fake per case),
so no key is needed; "{{expected}}" in an answer is replaced by the case's expected.json.

import-travelinbox converts the TravelInbox corpus (testdata/travelinbox or a TravelInbox
checkout) into travelinbox-* cases.

Exit codes: 0 every case ran, 1 usage or configuration error or a case the harness could
not run.

Flags:
`

// expectedPlaceholder in a --provider-script answer stands for the case's ground truth.
const expectedPlaceholder = "{{expected}}"

type evalOptions struct {
	corpus, config, url, tokenEnv string
	concurrency                   int
	cases, out, compare           string
	providerScript                string
	judge                         string // "" for the configured (or the application's) policy
}

func runEval(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "import-travelinbox" {
		return runEvalImport(args[1:], stdout, stderr)
	}
	var o evalOptions
	fs := newFlagSet("vde eval", evalUsage, stderr)
	fs.StringVar(&o.corpus, "corpus", "", "the corpus directory (required)")
	fs.StringVar(&o.config, "config", defaultConfigPath, "configuration file (local runs)")
	fs.StringVar(&o.url, "url", "", "run against this deployment instead of locally")
	fs.StringVar(&o.tokenEnv, "token-env", "", "environment variable holding the bearer token (with --url)")
	fs.IntVar(&o.concurrency, "concurrency", 1, "cases run at the same time")
	fs.StringVar(&o.cases, "cases", "*", "run only the cases whose id matches one of these comma-separated globs")
	fs.StringVar(&o.judge, "judge", "", "judge policy of every case: always, never or auto (default: the configured one)")
	fs.StringVar(&o.out, "out", "eval-reports", `directory for the JSON report ("" to not save it)`)
	fs.StringVar(&o.compare, "compare", "", "an earlier JSON report to compare with")
	fs.StringVar(&o.providerScript, "provider-script", "", "replace every provider with a fake replaying this script")
	if code, done := parseFlags(fs, args); done {
		return code
	}
	configSet := false
	fs.Visit(func(f *flag.Flag) { configSet = configSet || f.Name == "config" })
	usageErr := func(format string, args ...any) int {
		fmt.Fprintf(stderr, "vde eval: "+format+"\n", args...)
		return exitUsage
	}
	switch {
	case fs.NArg() > 0:
		return usageErr("unexpected argument %q", fs.Arg(0))
	case o.corpus == "":
		return usageErr("--corpus is required")
	case o.concurrency < 1:
		return usageErr("--concurrency must be at least 1")
	case o.url != "" && configSet:
		return usageErr("--config and --url exclude each other")
	case o.url != "" && o.providerScript != "":
		return usageErr("--provider-script runs locally; it excludes --url")
	case o.url != "" && o.tokenEnv == "":
		return usageErr("--url needs --token-env")
	case o.url == "" && o.tokenEnv != "":
		return usageErr("--token-env is only for --url")
	}

	if o.judge != "" {
		if _, err := config.ParseJudgePolicy(o.judge); err != nil {
			return usageErr("--judge: %v", err)
		}
	}
	patterns, err := eval.ParseCases(o.cases)
	if err != nil {
		return usageErr("--cases: %v", err)
	}

	var prev *eval.Report
	if o.compare != "" {
		r, err := eval.LoadReport(o.compare)
		if err != nil {
			return usageErr("--compare: %v", err)
		}
		prev = &r
	}
	cases, err := eval.LoadCorpus(o.corpus, patterns...)
	if err != nil {
		return usageErr("%v", err)
	}
	if len(cases) == 0 {
		return usageErr("no case in %s matches %q", o.corpus, o.cases)
	}

	report := eval.Report{Corpus: o.corpus, Cases: o.cases, Concurrency: o.concurrency, Target: "local", JudgePolicy: o.judge}
	var runner eval.Runner
	if o.url != "" {
		token := os.Getenv(o.tokenEnv)
		if token == "" {
			return usageErr("--token-env %s: the variable is not set", o.tokenEnv)
		}
		base, err := url.Parse(strings.TrimSuffix(o.url, "/"))
		if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
			return usageErr("--url %q: want an http or https URL", o.url)
		}
		report.Target = base.String()
		runner = (&httpEngine{base: base.String(), token: token, judge: o.judge, client: &http.Client{Timeout: 2 * time.Minute}}).run
	} else {
		engine, err := newLocalEngine(o, stderr)
		if err != nil {
			fmt.Fprintf(stderr, "vde eval: %v\n", err)
			return exitUsage
		}
		report.ServiceVersion = engine.serviceVersion
		report.JudgePolicy = engine.judgePolicy.String()
		report.Extractor = groupPairs(engine.cfg, engine.cfg.Roles.Extractor.Group)
		report.Judge = groupPairs(engine.cfg, engine.cfg.Roles.Judge.Group)
		runner = engine.run
		defer func() { _ = engine.pdf.Close() }()
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	fmt.Fprintf(stderr, "vde eval: %d case(s) from %s, concurrency %d\n", len(cases), o.corpus, o.concurrency)
	report.StartedAt = time.Now().UTC()
	report.Results = eval.Run(ctx, cases, o.concurrency, runner, func(done int, r eval.CaseResult) {
		fmt.Fprintf(stderr, "vde eval: [%d/%d] %s\n", done, len(cases), describeCase(r))
	})
	report.FinishedAt = time.Now().UTC()
	report.Summary = eval.Summarize(report.Results)
	if report.ServiceVersion == "" {
		for _, r := range report.Results {
			if r.ServiceVersion != "" {
				report.ServiceVersion = r.ServiceVersion
				break
			}
		}
	}

	report.WriteTable(stdout)
	if prev != nil {
		report.WriteDiff(stdout, *prev)
	}
	if o.out != "" {
		p, err := report.Save(o.out)
		if err != nil {
			fmt.Fprintf(stderr, "vde eval: save report: %v\n", err)
			return exitFailure
		}
		fmt.Fprintf(stderr, "vde eval: report saved to %s\n", p)
	}
	if report.Summary.Errors > 0 {
		return exitFailure
	}
	return exitOK
}

// describeCase is one progress line: the case id, its outcome and counts, never content.
func describeCase(r eval.CaseResult) string {
	if r.Status == eval.StatusError {
		return fmt.Sprintf("%s: not run: %s", r.ID, r.HarnessError)
	}
	status := r.Status
	if r.ErrorCode != "" {
		status += " (" + r.ErrorCode + ")"
	}
	if r.Verification != "" && r.Verification != extract.VerificationNone {
		status += " by " + r.Verification
	}
	fields := fmt.Sprintf("%d/%d fields", r.Fields.Matched, r.Fields.Total)
	if r.Fields.NoResult {
		fields = "no result"
	}
	line := fmt.Sprintf("%s: %s, %d attempt(s), %s, %s", r.ID, status, r.Attempts,
		(time.Duration(r.DurationMS) * time.Millisecond).String(), fields)
	if r.JudgeCalls != nil {
		line += fmt.Sprintf(", %d judge call(s)", *r.JudgeCalls)
	}
	if r.Decision == extract.DecisionSampled {
		line += ", sampled"
	}
	if r.FalseAccept {
		line += ", FALSE ACCEPT"
	}
	return line
}

// groupPairs lists a group's endpoint/model pairs in failover order, nil for no group.
func groupPairs(cfg *config.Config, group string) []string {
	g := cfg.Groups[group]
	if g == nil {
		return nil
	}
	pairs := make([]string, 0, len(g.Endpoints))
	for _, e := range g.Endpoints {
		pairs = append(pairs, e.Endpoint+"/"+e.Model)
	}
	return pairs
}

// localEngine runs cases through extract.Run like vde extract: one fresh set of role
// groups per case, so each case's calls and tokens are its own.
type localEngine struct {
	*localRun
	policy document.HTMLPolicy
	script fake.Script // nil for the configured providers
	pdf    *pdf.Engine // one per process, started by the first PDF that needs it
}

func newLocalEngine(o evalOptions, stderr io.Writer) (*localEngine, error) {
	e := &localEngine{}
	if o.providerScript != "" {
		data, err := os.ReadFile(o.providerScript)
		if err != nil {
			return nil, err
		}
		if e.script, err = fake.Parse(data); err != nil {
			return nil, fmt.Errorf("%s: %w", o.providerScript, err)
		}
	}
	var err error
	if e.localRun, err = newLocalRun(o.config, o.judge, o.providerScript != "", stderr); err != nil {
		return nil, err
	}
	e.policy = document.HTMLPolicy(e.cfg.Documents.HTML)
	if e.pdf, err = newPDFEngine(e.cfg, cliPDFCacheDir()); err != nil {
		return nil, err
	}
	return e, nil
}

func (e *localEngine) run(ctx context.Context, c eval.Case) (eval.Outcome, error) {
	doc, err := readDocument(extractOptions{document: c.Document, pages: pageFlags(c.Pages)}, e.cfg.Limits)
	if err != nil {
		return eval.Outcome{}, err
	}
	files, err := readJobFiles(c.ExtractorInstructions, c.JudgeInstructions, c.Schema, eval.FileSchema)
	if err != nil {
		return eval.Outcome{}, err
	}
	rep, err := selectRepresentation(ctx, e.cfg, doc, e.policy, e.pdf, e.judgePolicy)
	if err != nil {
		return eval.Outcome{}, err
	}
	var p provider.Provider
	if e.script != nil {
		p = fake.New(withExpected(e.script, c.Expected))
	}
	obs := &observer{}
	roles, err := extract.NewRoles(e.cfg, providerFactory(e.cfg, p, obs))
	if err != nil {
		return eval.Outcome{}, err
	}
	res := extract.Run(ctx, e.input(doc, rep, files, e.cfg.Limits.MaxAttempts, e.cfg.Limits.JobTimeout), roles)
	calls, in, out := obs.usage()
	o := eval.Outcome{
		Status:         string(res.Status),
		Result:         res.Result,
		Attempts:       res.Attempts,
		ServiceVersion: e.serviceVersion,
		Usage:          &eval.Usage{Calls: calls, InputTokens: in, OutputTokens: out},
		JudgePolicy:    e.judgePolicy.String(),
		Verification:   res.Verification,
		JudgeCalls:     &res.JudgeCalls,
		Decision:       res.Decision,
		Disagreements:  res.Disagreements,
	}
	if res.Error != nil {
		o.ErrorCode = res.Error.Code
	}
	return o, nil
}

// withExpected returns a copy of script whose answers have expectedPlaceholder replaced
// by the compact ground truth.
func withExpected(script fake.Script, expected json.RawMessage) fake.Script {
	var compact bytes.Buffer
	if err := json.Compact(&compact, expected); err != nil {
		compact.Write(expected)
	}
	out := make(fake.Script, len(script))
	for i, s := range script {
		s.Answer = strings.ReplaceAll(s.Answer, expectedPlaceholder, compact.String())
		out[i] = s
	}
	return out
}

// HTTP polling (docs/api/guide.md): start at 2 s, double up to 30 s, give up on a case
// after 10 minutes or three failed polls in a row. Variables so tests can shorten them.
var (
	pollFirst       = 2 * time.Second
	pollMax         = 30 * time.Second
	jobWait         = 10 * time.Minute
	submitRetryWait = 5 * time.Second
)

const (
	pollFailures  = 3
	submitRetries = 5
)

// httpEngine runs cases against a deployment: submit, poll, read the terminal job.
type httpEngine struct {
	base   string
	token  string
	judge  string // the judge part, "" to send none
	client *http.Client
}

// remoteJob is the part of the job resource the harness reads.
type remoteJob struct {
	ID             string          `json:"id"`
	Status         string          `json:"status"`
	Result         json.RawMessage `json:"result"`
	Attempts       int             `json:"attempts"`
	ServiceVersion string          `json:"service_version"`
	Verification   *string         `json:"verification"`
	Error          *struct {
		Code string `json:"code"`
	} `json:"error"`
}

func (e *httpEngine) run(ctx context.Context, c eval.Case) (eval.Outcome, error) {
	body, contentType, err := multipartJob(c, e.judge)
	if err != nil {
		return eval.Outcome{}, err
	}
	job, err := e.submit(ctx, body, contentType)
	if err != nil {
		if e.judge != "" && errors.Is(err, errJudgeRefused) {
			return eval.Outcome{}, fmt.Errorf("--judge %s: %w", e.judge, err)
		}
		return eval.Outcome{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, jobWait)
	defer cancel()
	wait, failures := pollFirst, 0
	for !remoteTerminal(job.Status) {
		select {
		case <-ctx.Done():
			return eval.Outcome{}, fmt.Errorf("job %s: not terminal: %w", job.ID, ctx.Err())
		case <-time.After(wait):
		}
		wait = min(2*wait, pollMax)
		next, status, err := e.get(ctx, job.ID)
		switch {
		case err == nil:
			job, failures = next, 0
		case status == http.StatusTooManyRequests || status >= 500 || status == 0:
			if failures++; failures >= pollFailures {
				return eval.Outcome{}, fmt.Errorf("job %s: poll: %w", job.ID, err)
			}
		default:
			return eval.Outcome{}, fmt.Errorf("job %s: poll: %w", job.ID, err)
		}
	}
	o := eval.Outcome{Status: job.Status, Result: job.Result, Attempts: job.Attempts, ServiceVersion: job.ServiceVersion, JudgePolicy: e.judge}
	if job.Error != nil {
		o.ErrorCode = job.Error.Code
	}
	if job.Verification != nil {
		o.Verification = *job.Verification
	}
	return o, nil
}

// remoteTerminal reports whether a job status is terminal: the job store's set
// (contract §5 and the superset status extracted).
func remoteTerminal(status string) bool { return job.Status(status).Terminal() }

// errJudgeRefused is a submit answered 400 about the judge part: the application does
// not allow a per-request judge policy (or the value is unknown).
var errJudgeRefused = errors.New("the deployment refused the judge part: the application must set judge.allow_override")

// multipartJob builds the POST /v1/jobs body of a case, with retention none and the judge
// part when judge is set.
func multipartJob(c eval.Case, judge string) ([]byte, string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	addFile := func(name, path, mediaType string) error {
		if mediaType == "" {
			ext := strings.ToLower(filepath.Ext(path))
			if mediaType = mediaTypesByExtension[ext]; mediaType == "" {
				return fmt.Errorf("no media type known for extension %q", ext)
			}
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		h := make(textproto.MIMEHeader)
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, name, filepath.Base(path)))
		h.Set("Content-Type", mediaType)
		part, err := w.CreatePart(h)
		if err != nil {
			return err
		}
		_, err = part.Write(data)
		return err
	}
	if err := addFile("document", c.Document, ""); err != nil {
		return nil, "", err
	}
	for _, p := range c.Pages {
		if err := addFile("page", p, ""); err != nil {
			return nil, "", err
		}
	}
	if err := addFile("extractor_instructions", c.ExtractorInstructions, "text/markdown"); err != nil {
		return nil, "", err
	}
	if err := addFile("judge_instructions", c.JudgeInstructions, "text/markdown"); err != nil {
		return nil, "", err
	}
	if c.Schema != "" {
		if err := addFile("schema", c.Schema, "application/schema+json"); err != nil {
			return nil, "", err
		}
	}
	if err := w.WriteField("retention", "none"); err != nil {
		return nil, "", err
	}
	if judge != "" {
		if err := w.WriteField("judge", judge); err != nil {
			return nil, "", err
		}
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), w.FormDataContentType(), nil
}

// submit posts the job, retrying 429 and 503 answers after their Retry-After.
func (e *httpEngine) submit(ctx context.Context, body []byte, contentType string) (remoteJob, error) {
	for try := 1; ; try++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.base+"/v1/jobs", bytes.NewReader(body))
		if err != nil {
			return remoteJob{}, err
		}
		req.Header.Set("Content-Type", contentType)
		job, status, retryAfter, err := e.do(req, http.StatusAccepted)
		if err == nil || try >= submitRetries || (status != http.StatusTooManyRequests && status != http.StatusServiceUnavailable) {
			if err != nil {
				return remoteJob{}, fmt.Errorf("submit: %w", err)
			}
			return job, nil
		}
		select {
		case <-ctx.Done():
			return remoteJob{}, ctx.Err()
		case <-time.After(retryAfter):
		}
	}
}

func (e *httpEngine) get(ctx context.Context, id string) (remoteJob, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.base+"/v1/jobs/"+url.PathEscape(id), nil)
	if err != nil {
		return remoteJob{}, 0, err
	}
	job, status, _, err := e.do(req, http.StatusOK)
	return job, status, err
}

// do sends req with the token and decodes the job. An error names the status and the
// contract error code only; the response body is never shown.
func (e *httpEngine) do(req *http.Request, want int) (remoteJob, int, time.Duration, error) {
	req.Header.Set("Authorization", "Bearer "+e.token)
	resp, err := e.client.Do(req)
	if err != nil {
		return remoteJob{}, 0, 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return remoteJob{}, 0, 0, err
	}
	if resp.StatusCode != want {
		var body struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(data, &body)
		retryAfter := submitRetryWait
		if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s > 0 {
			retryAfter = time.Duration(s) * time.Second
		}
		err := fmt.Errorf("status %d %s", resp.StatusCode, body.Error.Code)
		// The service's own messages name the refused part; only that is read from them.
		if resp.StatusCode == http.StatusBadRequest && strings.Contains(strings.ToLower(body.Error.Message), "judge part") {
			err = fmt.Errorf("%w: %w", err, errJudgeRefused)
		}
		return remoteJob{}, resp.StatusCode, retryAfter, err
	}
	var job remoteJob
	if err := json.Unmarshal(data, &job); err != nil {
		return remoteJob{}, resp.StatusCode, 0, fmt.Errorf("job: %w", err)
	}
	if job.ServiceVersion == "" {
		job.ServiceVersion = resp.Header.Get("X-Service-Version")
	}
	return job, resp.StatusCode, 0, nil
}

const evalImportUsage = `Usage: vde eval import-travelinbox --from dir --to dir

Converts the TravelInbox corpus into evaluation cases travelinbox-<nnn>-<kind> under --to.
--from is this repository's testdata/travelinbox or a TravelInbox checkout. Existing cases
are refreshed; none is removed.

Flags:
`

func runEvalImport(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("vde eval import-travelinbox", evalImportUsage, stderr)
	from := fs.String("from", "", "the TravelInbox assets or checkout (required)")
	to := fs.String("to", "", "the corpus directory to write (required)")
	if code, done := parseFlags(fs, args); done {
		return code
	}
	if fs.NArg() > 0 || *from == "" || *to == "" {
		fmt.Fprintln(stderr, "vde eval import-travelinbox: --from and --to are required, no arguments")
		return exitUsage
	}
	n, err := eval.ImportTravelInbox(*from, *to)
	if err != nil {
		fmt.Fprintf(stderr, "vde eval import-travelinbox: %v\n", err)
		return exitFailure
	}
	fmt.Fprintf(stdout, "%d case(s) written to %s\n", n, *to)
	return exitOK
}
