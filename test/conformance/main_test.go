package conformance

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"text/template"
	"time"
)

// Applications of every local instance. Each has a token with the jobs scope.
const (
	appDev     = "travelinbox-dev"    // the application under test: generous limits, retention default of devRetention
	appOther   = "travelinbox-other"  // another application, for ownership and per-application keys
	appQuota   = "travelinbox-quota"  // a quota small enough to spend in a test
	appRate    = "travelinbox-rate"   // a request rate small enough to exceed in a test
	appForced  = "travelinbox-forced" // retention forced to none by the operator
	quotaLimit = 3
	rateLimit  = 2
	// devRetention is appDev's configured retention for retention: default jobs; not
	// the 24 hours of retention: none, so the two cannot be confused.
	devRetention = 48 * time.Hour
)

var apps = []string{appDev, appOther, appQuota, appRate, appForced}

// env is the suite's environment, set up by TestMain.
var env struct {
	remote       bool
	root         string // repository root
	tmp          string
	binary       string
	llm          *fakeLLM
	main         *instance
	base         string // main service base URL
	token        string // jobs token of appDev (or VDE_CONFORMANCE_TOKEN)
	foreignToken string // jobs token of appOther (or VDE_CONFORMANCE_FOREIGN_TOKEN, may be empty)
}

func TestMain(m *testing.M) { os.Exit(runMain(m)) }

func runMain(m *testing.M) int {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		fmt.Fprintln(os.Stderr, "conformance:", err)
		return 1
	}
	env.root = root
	if u := os.Getenv("VDE_CONFORMANCE_URL"); u != "" {
		env.remote = true
		env.base = strings.TrimRight(u, "/")
		env.token = os.Getenv("VDE_CONFORMANCE_TOKEN")
		env.foreignToken = os.Getenv("VDE_CONFORMANCE_FOREIGN_TOKEN")
		if env.token == "" {
			fmt.Fprintln(os.Stderr, "conformance: VDE_CONFORMANCE_URL is set but VDE_CONFORMANCE_TOKEN is not")
			return 1
		}
		return m.Run()
	}

	env.tmp, err = os.MkdirTemp("", "vde-conformance-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "conformance:", err)
		return 1
	}
	defer os.RemoveAll(env.tmp)
	env.binary = filepath.Join(env.tmp, "vde")
	build := exec.Command("go", "build", "-o", env.binary, "./cmd/vde")
	build.Dir = root
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "conformance: building vde: %v\n%s", err, out)
		return 1
	}
	env.llm = newFakeLLM()
	defer env.llm.close()
	env.main, err = startInstance(instanceOptions{name: "main"})
	if err != nil {
		fmt.Fprintln(os.Stderr, "conformance: starting vde serve:", err)
		return 1
	}
	defer env.main.stop()
	env.base = env.main.base
	env.token = env.main.tokens[appDev]
	env.foreignToken = env.main.tokens[appOther]
	code := m.Run()
	if code != 0 {
		fmt.Fprintf(os.Stderr, "--- vde serve log (main instance, last 4 KiB) ---\n%s\n", env.main.logTail(4096))
	}
	return code
}

// requireLocal skips t in deployment mode.
func requireLocal(t *testing.T, why string) {
	t.Helper()
	if env.remote {
		t.Skip("needs the local service: " + why)
	}
}

// instanceOptions vary the configuration of one vde serve process.
type instanceOptions struct {
	name         string   // prefix of the instance's namespace on the fake provider
	unreachable  []string // endpoints whose base_url points at a closed port
	accepts      []string // media types every model accepts; default the five MUST types
	maxAttempts  int      // limits.max_attempts; default 3
	jobTimeout   string   // limits.job_timeout; default 5m
	failoverTime string   // failover.max_total_time of both groups; default 20s
	callTimeout  string   // endpoint timeout; default 10s
	trustedProxy bool     // server.trusted_proxy
	retention    string   // appDev's retention.default; default devRetention
	sweep        string   // retention.sweep_interval; default 1m
}

// instance is one running vde serve.
type instance struct {
	name    string
	base    string
	dataDir string
	config  string
	cmd     *exec.Cmd
	done    chan error
	logs    *syncBuffer
	tokens  map[string]string // application → jobs token
}

const configTemplate = `
server:
  listen: "{{.Listen}}"
  data_dir: "{{.DataDir}}"
  trusted_proxy: {{.TrustedProxy}}
admin:
  listen: "{{.AdminListen}}"
endpoints:
{{- range .Endpoints}}
  {{.Name}}:
    provider: openai_compatible
    base_url: "{{.URL}}"
    timeout: {{$.CallTimeout}}
    models:
      {{.Model}}: {accepts: [{{$.Accepts}}]}
{{- end}}
groups:
  extraction:
    endpoints:
      - {endpoint: primary, model: extractor-primary}
      - {endpoint: fallback, model: extractor-fallback}
    failover: {max_tries: 3, max_total_time: {{.FailoverTime}}}
  review:
    endpoints:
      - {endpoint: judge, model: judge-primary}
      - {endpoint: judge-fallback, model: judge-fallback}
    failover: {max_tries: 3, max_total_time: {{.FailoverTime}}}
roles:
  extractor: {group: extraction}
  judge: {group: review}
limits: {max_attempts: {{.MaxAttempts}}, job_timeout: {{.JobTimeout}}}
worker: {size: 4, poll_interval: 20ms}
retention: {sweep_interval: {{.Sweep}}}
logging: {level: debug, format: json}
applications:
  - id: travelinbox-dev
    quota: {limit: 100000, window: P1D}
    rate: {requests_per_minute: 1000000, submits_per_minute: 100000, max_in_progress: 1000}
    retention: {default: {{.Retention}}}
  - id: travelinbox-other
    rate: {requests_per_minute: 1000000, submits_per_minute: 100000, max_in_progress: 1000}
  - id: travelinbox-quota
    quota: {limit: {{.QuotaLimit}}, window: P1D}
    rate: {requests_per_minute: 1000000, submits_per_minute: 100000, max_in_progress: 1000}
  - id: travelinbox-rate
    rate: {requests_per_minute: {{.RateLimit}}}
  - id: travelinbox-forced
    rate: {requests_per_minute: 1000000, submits_per_minute: 100000, max_in_progress: 1000}
    retention: {force_none: true}
`

// defaultAccepts are the document media types contract §4 says the service MUST accept.
var defaultAccepts = []string{"application/pdf", "image/png", "image/jpeg", "text/plain", "text/html"}

// endpoints and the model each serves; the role follows from the endpoint.
var endpoints = []struct{ name, model, role string }{
	{"primary", "extractor-primary", roleExtractor},
	{"fallback", "extractor-fallback", roleExtractor},
	{"judge", "judge-primary", roleJudge},
	{"judge-fallback", "judge-fallback", roleJudge},
}

func startInstance(o instanceOptions) (*instance, error) {
	var lastErr error
	for range 3 { // the free port can be taken between probing and binding
		inst, err := tryStartInstance(o)
		if err == nil {
			return inst, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// instanceSeq makes every instance's namespace on the fake provider unique, so that a
// rerun (-count) or a retried start never sees another instance's calls.
var instanceSeq atomic.Int64

func tryStartInstance(o instanceOptions) (*instance, error) {
	o.name = fmt.Sprintf("%s-%d", o.name, instanceSeq.Add(1))
	dir, err := os.MkdirTemp(env.tmp, o.name+"-")
	if err != nil {
		return nil, err
	}
	port, err := freePort()
	if err != nil {
		return nil, err
	}
	closed, err := freePort()
	if err != nil {
		return nil, err
	}
	adminPort, err := freePort()
	if err != nil {
		return nil, err
	}
	type ep struct{ Name, Model, URL string }
	data := struct {
		Listen, AdminListen, DataDir, Accepts, JobTimeout, FailoverTime, CallTimeout string
		Retention, Sweep                                                             string
		TrustedProxy                                                                 bool
		MaxAttempts, QuotaLimit, RateLimit                                           int
		Endpoints                                                                    []ep
	}{
		Listen: "127.0.0.1:" + port, AdminListen: "127.0.0.1:" + adminPort, DataDir: filepath.Join(dir, "data"), TrustedProxy: o.trustedProxy,
		Accepts:    strings.Join(defaultAccepts, ", "),
		JobTimeout: or(o.jobTimeout, "5m"), FailoverTime: or(o.failoverTime, "20s"), CallTimeout: or(o.callTimeout, "10s"),
		MaxAttempts: or(o.maxAttempts, 3), QuotaLimit: quotaLimit, RateLimit: rateLimit, Retention: or(o.retention, devRetention.String()), Sweep: or(o.sweep, "1m"),
	}
	if o.accepts != nil {
		data.Accepts = strings.Join(o.accepts, ", ")
	}
	for _, e := range endpoints {
		u := env.llm.url() + "/" + o.name + "/" + e.name + "/v1"
		for _, down := range o.unreachable {
			if down == e.name {
				u = "http://127.0.0.1:" + closed + "/v1" // nothing listens there: connection refused
			}
		}
		data.Endpoints = append(data.Endpoints, ep{e.name, e.model, u})
	}
	var cfg bytes.Buffer
	if err := template.Must(template.New("vde.yaml").Parse(configTemplate)).Execute(&cfg, data); err != nil {
		return nil, err
	}
	inst := &instance{
		name: o.name, base: "http://" + data.Listen, dataDir: data.DataDir,
		config: filepath.Join(dir, "vde.yaml"), logs: &syncBuffer{}, done: make(chan error, 1),
		tokens: map[string]string{},
	}
	if err := os.WriteFile(inst.config, cfg.Bytes(), 0o600); err != nil {
		return nil, err
	}
	inst.cmd = exec.Command(env.binary, "serve", "--config", inst.config)
	inst.cmd.Stdout, inst.cmd.Stderr = inst.logs, inst.logs
	if err := inst.cmd.Start(); err != nil {
		return nil, err
	}
	go func() { inst.done <- inst.cmd.Wait() }()
	if err := inst.waitReady(15 * time.Second); err != nil {
		inst.stop()
		return nil, fmt.Errorf("%w\n%s", err, inst.logTail(4096))
	}
	for _, app := range apps {
		tok, _, err := inst.createToken(app, "jobs", "1d")
		if err != nil {
			inst.stop()
			return nil, err
		}
		inst.tokens[app] = tok
	}
	return inst, nil
}

func (inst *instance) waitReady(limit time.Duration) error {
	client := &http.Client{Timeout: time.Second}
	for deadline := time.Now().Add(limit); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		select {
		case err := <-inst.done:
			inst.done <- err
			return fmt.Errorf("vde serve exited: %w", err)
		default:
		}
		resp, err := client.Get(inst.base + "/healthz")
		if err == nil {
			resp.Body.Close()
			return nil
		}
	}
	return errors.New("vde serve did not answer /healthz in time")
}

var tokenIDRe = regexp.MustCompile(`token (\S+) for `)

// createToken runs vde token create and returns the token and its id.
func (inst *instance) createToken(app, scope, expires string) (string, string, error) {
	stdout, stderr, err := inst.cli("token", "create", "--config", inst.config,
		"--app", app, "--scope", scope, "--expires", expires)
	if err != nil {
		return "", "", fmt.Errorf("vde token create --app %s: %w: %s", app, err, stderr)
	}
	m := tokenIDRe.FindStringSubmatch(stderr)
	if m == nil {
		return "", "", fmt.Errorf("vde token create printed no token id: %s", stderr)
	}
	return strings.TrimSpace(stdout), m[1], nil
}

// cli runs the vde binary and returns its stdout and stderr.
func (inst *instance) cli(args ...string) (string, string, error) {
	return runCLI("", nil, args...)
}

func runCLI(dir string, extraEnv []string, args ...string) (string, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, env.binary, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), extraEnv...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func (inst *instance) stop() {
	if inst.cmd == nil || inst.cmd.Process == nil {
		return
	}
	_ = inst.cmd.Process.Signal(os.Interrupt)
	select {
	case <-inst.done:
	case <-time.After(15 * time.Second):
		_ = inst.cmd.Process.Kill()
		<-inst.done
	}
	inst.cmd = nil
}

func (inst *instance) logTail(n int) string {
	s := inst.logs.String()
	if len(s) > n {
		s = s[len(s)-n:]
	}
	return s
}

// blobDir is where the service keeps a job's document and pages (internal/blob layout).
func (inst *instance) blobDir(id string) string {
	return filepath.Join(inst.dataDir, "blobs", id[len(id)-2:], id)
}

// newInstance starts an instance for one test and stops it when the test ends.
func newInstance(t *testing.T, o instanceOptions) *instance {
	t.Helper()
	requireLocal(t, "starts its own vde serve")
	inst, err := startInstance(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("vde serve log (%s, last 4 KiB):\n%s", o.name, inst.logTail(4096))
		}
		inst.stop()
	})
	return inst
}

func freePort() (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer ln.Close()
	_, port, err := net.SplitHostPort(ln.Addr().String())
	return port, err
}

func or[T comparable](v, def T) T {
	var zero T
	if v == zero {
		return def
	}
	return v
}

// syncBuffer is a bytes.Buffer safe for the process's output copier and the tests.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
