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
	"github.com/kaanemec/vde/internal/provider"
	"github.com/kaanemec/vde/internal/provider/fake"
	"github.com/kaanemec/vde/internal/version"
)

const evalUsage = `Usage: vde eval --corpus dir [--config file | --url URL --token-env NAME]
                [--concurrency n] [--cases glob] [--out dir] [--compare previous.json]
                [--provider-script file]
       vde eval import-travelinbox --from dir --to dir

Runs every case of a corpus (docs/operator/evaluation.md) through the engine, or through a
deployment with --url, compares each result with the case's expected.json under its
compare.yaml and prints a table and a summary: false accepts (accepted, but a compared
field differs from the ground truth) first, then acceptance rate, field accuracy, false
rejects, durations and tokens. The JSON report is saved as <out>/<service_version>-<date>.json;
--compare prints the change against an earlier report. Only case ids, statuses, counts
and JSON pointers are printed or saved, never document content or result values.

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
}

func runEval(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "import-travelinbox" {
		return runEvalImport(args[1:], stdout, stderr)
	}
	var o evalOptions
	fs := flag.NewFlagSet("vde eval", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprint(stderr, evalUsage)
		fs.PrintDefaults()
	}
	fs.StringVar(&o.corpus, "corpus", "", "the corpus directory (required)")
	fs.StringVar(&o.config, "config", defaultConfigPath, "configuration file (local runs)")
	fs.StringVar(&o.url, "url", "", "run against this deployment instead of locally")
	fs.StringVar(&o.tokenEnv, "token-env", "", "environment variable holding the bearer token (with --url)")
	fs.IntVar(&o.concurrency, "concurrency", 1, "cases run at the same time")
	fs.StringVar(&o.cases, "cases", "*", "run only the cases whose id matches this glob")
	fs.StringVar(&o.out, "out", "eval-reports", `directory for the JSON report ("" to not save it)`)
	fs.StringVar(&o.compare, "compare", "", "an earlier JSON report to compare with")
	fs.StringVar(&o.providerScript, "provider-script", "", "replace every provider with a fake replaying this script")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
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

	var prev *eval.Report
	if o.compare != "" {
		r, err := eval.LoadReport(o.compare)
		if err != nil {
			return usageErr("--compare: %v", err)
		}
		prev = &r
	}
	cases, err := eval.LoadCorpus(o.corpus, o.cases)
	if err != nil {
		return usageErr("%v", err)
	}
	if len(cases) == 0 {
		return usageErr("no case in %s matches %q", o.corpus, o.cases)
	}

	report := eval.Report{Corpus: o.corpus, Cases: o.cases, Concurrency: o.concurrency, Target: "local"}
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
		runner = (&httpEngine{base: base.String(), token: token, client: &http.Client{Timeout: 2 * time.Minute}}).run
	} else {
		engine, err := newLocalEngine(o, stderr)
		if err != nil {
			fmt.Fprintf(stderr, "vde eval: %v\n", err)
			return exitUsage
		}
		report.ServiceVersion = engine.serviceVersion
		report.Extractor = groupPairs(engine.cfg, engine.cfg.Roles.Extractor.Group)
		report.Judge = groupPairs(engine.cfg, engine.cfg.Roles.Judge.Group)
		runner = engine.run
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
	fields := fmt.Sprintf("%d/%d fields", r.Fields.Matched, r.Fields.Total)
	if r.Fields.NoResult {
		fields = "no result"
	}
	line := fmt.Sprintf("%s: %s, %d attempt(s), %s, %s", r.ID, status, r.Attempts,
		(time.Duration(r.DurationMS) * time.Millisecond).String(), fields)
	if r.FalseAccept {
		line += ", FALSE ACCEPT"
	}
	return line
}

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
	cfg            *config.Config
	serviceVersion string
	policy         document.HTMLPolicy
	script         fake.Script // nil for the configured providers
}

func newLocalEngine(o evalOptions, stderr io.Writer) (*localEngine, error) {
	e := &localEngine{}
	getenv := os.Getenv
	if o.providerScript != "" {
		data, err := os.ReadFile(o.providerScript)
		if err != nil {
			return nil, err
		}
		if e.script, err = fake.Parse(data); err != nil {
			return nil, fmt.Errorf("%s: %w", o.providerScript, err)
		}
		getenv = func(k string) string {
			if v := os.Getenv(k); v != "" {
				return v
			}
			return "unused-with-provider-script"
		}
	}
	cfg, issues, err := loadConfigEnv(o.config, false, getenv)
	for _, i := range issues {
		fmt.Fprintln(stderr, i)
	}
	if err != nil {
		return nil, err
	}
	e.cfg = cfg
	e.serviceVersion = extract.ServiceVersion(cfg, version.Version)
	e.policy = document.HTMLPolicy(cfg.Documents.HTML)
	return e, nil
}

func (e *localEngine) run(ctx context.Context, c eval.Case) (eval.Outcome, error) {
	doc, err := readDocument(extractOptions{document: c.Document, pages: pageFlags(c.Pages)}, e.cfg.Limits)
	if err != nil {
		return eval.Outcome{}, err
	}
	extractor, err := os.ReadFile(c.ExtractorInstructions)
	if err != nil {
		return eval.Outcome{}, err
	}
	judge, err := os.ReadFile(c.JudgeInstructions)
	if err != nil {
		return eval.Outcome{}, err
	}
	var schema *extract.Schema
	if c.Schema != "" {
		b, err := os.ReadFile(c.Schema)
		if err != nil {
			return eval.Outcome{}, err
		}
		if schema, err = extract.CompileSchema(b); err != nil {
			return eval.Outcome{}, fmt.Errorf("%s: %w", eval.FileSchema, err)
		}
	}
	rep, err := document.Select(doc, e.cfg.Capabilities(e.cfg.Roles.Extractor.Group), e.cfg.Capabilities(e.cfg.Roles.Judge.Group), e.policy)
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
	res := extract.Run(ctx, extract.Input{
		Representation:        rep,
		ExtractorInstructions: string(extractor),
		JudgeInstructions:     string(judge),
		Schema:                schema,
		MaxAttempts:           e.cfg.Limits.MaxAttempts,
		Deadline:              time.Now().Add(e.cfg.Limits.JobTimeout),
	}, roles)
	calls, in, out := obs.usage()
	o := eval.Outcome{
		Status:         string(res.Status),
		Result:         res.Result,
		Attempts:       res.Attempts,
		ServiceVersion: e.serviceVersion,
		Usage:          &eval.Usage{Calls: calls, InputTokens: in, OutputTokens: out},
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
	client *http.Client
}

// remoteJob is the part of the job resource the harness reads.
type remoteJob struct {
	ID             string          `json:"id"`
	Status         string          `json:"status"`
	Result         json.RawMessage `json:"result"`
	Attempts       int             `json:"attempts"`
	ServiceVersion string          `json:"service_version"`
	Error          *struct {
		Code string `json:"code"`
	} `json:"error"`
}

func (e *httpEngine) run(ctx context.Context, c eval.Case) (eval.Outcome, error) {
	body, contentType, err := multipartJob(c)
	if err != nil {
		return eval.Outcome{}, err
	}
	job, err := e.submit(ctx, body, contentType)
	if err != nil {
		return eval.Outcome{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, jobWait)
	defer cancel()
	wait, failures := pollFirst, 0
	for job.Status != eval.StatusAccepted && job.Status != eval.StatusRejected && job.Status != eval.StatusFailed {
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
	o := eval.Outcome{Status: job.Status, Result: job.Result, Attempts: job.Attempts, ServiceVersion: job.ServiceVersion}
	if job.Error != nil {
		o.ErrorCode = job.Error.Code
	}
	return o, nil
}

// multipartJob builds the POST /v1/jobs body of a case, with retention none.
func multipartJob(c eval.Case) ([]byte, string, error) {
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
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(data, &body)
		retryAfter := submitRetryWait
		if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s > 0 {
			retryAfter = time.Duration(s) * time.Second
		}
		return remoteJob{}, resp.StatusCode, retryAfter, fmt.Errorf("status %d %s", resp.StatusCode, body.Error.Code)
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
	fs := flag.NewFlagSet("vde eval import-travelinbox", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprint(stderr, evalImportUsage)
		fs.PrintDefaults()
	}
	from := fs.String("from", "", "the TravelInbox assets or checkout (required)")
	to := fs.String("to", "", "the corpus directory to write (required)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
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
