package main

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"syscall"
	"time"

	"github.com/kaanemec/vde/internal/api"
	"github.com/kaanemec/vde/internal/audit"
	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/document"
	"github.com/kaanemec/vde/internal/extract"
	"github.com/kaanemec/vde/internal/job"
	"github.com/kaanemec/vde/internal/metrics"
	"github.com/kaanemec/vde/internal/pdf"
	"github.com/kaanemec/vde/internal/provider"
	"github.com/kaanemec/vde/internal/router"
	"github.com/kaanemec/vde/internal/sweeper"
	"github.com/kaanemec/vde/internal/version"
	"github.com/kaanemec/vde/internal/webhook"
	"github.com/kaanemec/vde/internal/worker"
)

const serveUsage = `Usage: vde serve [--config path] [--role all|api|worker]

Validates the configuration, opens the database (server.data_dir/vde.db), mirrors the
configured applications into it (an application no longer configured is disabled and its
tokens stop working; applications created over the admin API are kept), starts
worker.size job workers and the expiry sweeper, then serves the /v1 API, /healthz and
/metrics on server.listen (/metrics on metrics.listen instead when set) and the admin API
(/admin/v1, admin tokens only) on admin.listen unless it is empty.
With server.tls the binary terminates TLS; otherwise it serves plain HTTP and refuses a
non-loopback listen address unless server.trusted_proxy (a reverse proxy terminates TLS)
or server.insecure_http (trusted private networks only) is true. SIGINT or SIGTERM stops
accepting connections, drains in-flight requests and running jobs for up to 30 s (a job
still running then resumes after the next start), and stops the sweeper.

--role splits the service across processes sharing one PostgreSQL database and one S3
bucket: api serves /v1, the admin API, /healthz (with the queue age) and /metrics and runs
no job; worker runs the worker pool and the sweeper (one worker process sweeps at a time)
and serves only /healthz and /metrics on server.listen; all (the default) runs everything.

Flags:
`

// Server timeouts. A request may upload up to limits.document_bytes plus pages, so the
// whole request read is bounded at 60 s and the response write after it.
const (
	readHeaderTimeout = 10 * time.Second
	readTimeout       = 60 * time.Second
	writeTimeout      = 90 * time.Second
	idleTimeout       = 120 * time.Second
	maxHeaderBytes    = 64 << 10
	drainTimeout      = 30 * time.Second
)

func runServe(args []string, _, stderr io.Writer) int {
	fs := flag.NewFlagSet("vde serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprint(stderr, serveUsage)
		fs.PrintDefaults()
	}
	path := fs.String("config", defaultConfigPath, "configuration file")
	role := fs.String("role", api.RoleAll, "process role: all, api or worker")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "vde serve: unexpected argument %q\n", fs.Arg(0))
		return exitUsage
	}
	if !slices.Contains([]string{api.RoleAll, api.RoleAPI, api.RoleWorker}, *role) {
		fmt.Fprintf(stderr, "vde serve: --role %q is not all, api or worker\n", *role)
		return exitUsage
	}

	cfg, issues, err := loadConfigEnv(*path, false, os.Getenv)
	for _, i := range issues {
		fmt.Fprintln(stderr, i)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitFailure
	}
	if err := checkListen(cfg.Server); err != nil {
		fmt.Fprintf(stderr, "vde serve: %v\n", err)
		return exitFailure
	}
	ln, err := net.Listen("tcp", cfg.Server.Listen)
	if err != nil {
		fmt.Fprintf(stderr, "vde serve: %v\n", err)
		return exitFailure
	}
	var adminLn net.Listener // config validation refused a plain non-loopback admin.listen
	if cfg.Admin.Listen != "" && *role != api.RoleWorker {
		if adminLn, err = net.Listen("tcp", cfg.Admin.Listen); err != nil {
			ln.Close()
			fmt.Fprintf(stderr, "vde serve: admin.listen: %v\n", err)
			return exitFailure
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// The redacting logger is also the process default, so nothing that logs through
	// package-level slog (the job store's WAL checkpoint) or the log package bypasses it.
	logger := newLogger(stderr, cfg.Logging)
	slog.SetDefault(logger)
	if err := serveAs(ctx, *role, cfg, ln, adminLn, logger, newProvider); err != nil {
		fmt.Fprintf(stderr, "vde serve: %v\n", err)
		return exitFailure
	}
	return exitOK
}

// checkListen refuses plain HTTP on a non-loopback address unless a trusted reverse
// proxy terminates TLS in front of the service (contract §2) or the operator declared
// the network trusted with server.insecure_http.
func checkListen(s config.Server) error {
	if s.TLS.CertFile != "" || s.TrustedProxy || s.InsecureHTTP {
		return nil
	}
	host, _, err := net.SplitHostPort(s.Listen)
	if err != nil {
		return fmt.Errorf("server.listen %q: %w", s.Listen, err)
	}
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("server.listen %q is not a loopback address: set server.tls, or server.trusted_proxy when a reverse proxy terminates TLS", s.Listen)
}

// serveAs opens the store, mirrors the configured applications into it, starts the worker
// pool and the expiry sweeper (roles all and worker), and runs the HTTP server on ln (and
// the metrics listener of metrics.listen, and the admin API on adminLn unless it is nil)
// until ctx is done. Then it drains in-flight requests, stops the pool (running jobs get
// the rest of the drain time to finish, or keep their lease and persisted attempts for the
// next lease, by this process or another) and the sweeper. build makes each endpoint's
// provider. It returns nil after a clean shutdown.
func serveAs(ctx context.Context, role string, cfg *config.Config, ln, adminLn net.Listener, logger *slog.Logger,
	build func(*config.Endpoint) (provider.Provider, error)) (err error) {
	defer func() {
		if err != nil {
			ln.Close()
			if adminLn != nil {
				adminLn.Close()
			}
		}
	}()
	var metricOpts []metrics.Option
	if cfg.Metrics.GoRuntime {
		metricOpts = append(metricOpts, metrics.WithGoRuntime())
	}
	m := metrics.New(metricOpts...)
	roles, err := extract.NewRoles(cfg, build)
	if err != nil {
		return err
	}
	meter(roles, m)
	runsJobs := role != api.RoleAPI
	instance := instanceID()
	if runsJobs {
		m.WatchEndpoints(instance, endpointStates(roles))
	}
	store, err := openStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer store.Close()
	blobs, err := openBlobs(ctx, cfg)
	if err != nil {
		return err
	}
	logger.Info("storage opened", storageAttrs(cfg)...)
	// One pdfium engine per process (ADR 0010), started by the first PDF that needs it:
	// submit selects with it (--role api and all), workers re-select and render with it.
	pdfEngine, err := newPDFEngine(cfg, pdf.CacheDir(cfg.Server.DataDir))
	if err != nil {
		return err
	}
	defer func() { _ = pdfEngine.Close() }()
	warmPDF(ctx, logger, pdfEngine, cfg.Documents.PDF.Mode != "native")

	if len(cfg.Applications) == 0 {
		logger.Warn("no applications are configured: every /v1 request is refused")
	}
	if cfg.Server.InsecureHTTP {
		logger.Warn("SERVER.INSECURE_HTTP IS ON: plain HTTP is accepted from any peer, so tokens and " +
			"documents cross the network unencrypted; use it only on a trusted private network")
	}
	var auditLogger *slog.Logger
	if cfg.Audit.Log {
		auditLogger = logger
	}
	rec := audit.New(store, audit.Options{Logger: auditLogger})
	if err := syncApplications(ctx, store, rec, cfg.Applications, time.Now()); err != nil {
		return err
	}
	serviceVersion := extract.ServiceVersion(cfg, version.Version)
	if err := rec.Record(ctx, audit.ActorConfig, "", "", audit.ConfigLoaded{ServiceVersion: serviceVersion}); err != nil {
		logger.Warn("audit event not written", "kind", string(audit.ConfigLoadedKind), "error", err)
	}
	// The service_version of an application's jobs follows its stored row (attempt limit,
	// HTML policy and judge settings) and the job's judge policy, so applications created
	// or changed over the admin API get theirs.
	appVersion := func(a job.Application, policy config.JudgePolicy) string {
		app := storedApplication(a)
		app.Documents.PDF.Mode = cfg.PDFModeOf(a.ID)
		if policy == "" {
			policy = cfg.JudgePolicyOf(app)
		}
		return extract.ApplicationServiceVersion(cfg, app, policy, version.Version)
	}
	inputsAtTerminal := map[string]bool{}
	for _, a := range cfg.Applications {
		if a.Retention.ForceNone || a.Retention.DeleteBlobsAtTerminal {
			inputsAtTerminal[a.ID] = true
		}
	}
	apps, err := store.ListApplications(ctx)
	if err != nil {
		return err
	}
	for _, a := range apps {
		if !a.Enabled {
			continue
		}
		logger.Info("application enabled", "app_id", a.ID, "source", a.Source, "service_version", appVersion(a, ""))
		if n, err := store.CountInProgress(ctx, a.ID); err == nil {
			m.SetJobsInProgress(a.ID, n) // jobs left queued or running by a previous process
		}
	}

	// The completion hook: wake POST /v1/extract waiters, queue the webhooks. A worker
	// process sends the webhooks of the jobs it finished; an API-only process has no
	// in-process completions and POST /v1/extract polls the store.
	completions := worker.NewNotifier()
	hooks := webhook.New(webhookTargets(cfg), webhook.Options{Audit: rec, Logger: logger})
	pool := worker.New(store, blobs, cfg, roles, worker.Options{
		Size:         cfg.Worker.Size,
		PollInterval: cfg.Worker.PollInterval,
		Lease:        cfg.Worker.Lease,
		WorkerID:     instance,
		Observer:     progressObserver{Metrics: m, store: store},
		Logger:       logger,
		Audit:        rec.WithActor(audit.ActorWorker),
		OnFinish: func(j job.Job) {
			completions.Notify(j.ID)
			hooks.JobFinished(j)
		},
		PDF: document.PDFEngineOf(pdfEngine),
	})
	sweep := sweeper.New(store, blobs, sweeper.Options{
		Interval:               cfg.Retention.SweepInterval,
		JobTimeout:             cfg.Limits.JobTimeout,
		AuditRetention:         cfg.Audit.Retention,
		Logger:                 logger,
		Observer:               m,
		Audit:                  rec.WithActor(audit.ActorSweeper),
		DeleteInputsAtTerminal: inputsAtTerminal,
		Lock:                   store, // one worker process sweeps at a time
	})
	deps := api.Deps{
		Config:            cfg,
		Store:             store,
		ServiceVersion:    serviceVersion,
		AppServiceVersion: appVersion,
		Logger:            logger,
		Extractor:         allHealthy(extractorGroups(roles)),
		Judge:             allHealthy(judgeGroups(roles)),
		Blobs:             blobs,
		Metrics:           m,
		Audit:             rec.WithActor(audit.ActorAPI),
		PDF:               document.PDFEngineOf(pdfEngine),
		Role:              role,
	}
	if runsJobs {
		// Within the process a submission wakes a worker at once; other processes' workers
		// find it at their next poll (worker.poll_interval).
		deps.Wake, deps.Completions = pool.Wake, completions
	}
	useTLS := cfg.Server.TLS.CertFile != ""
	srv := newHTTPServer(api.New(deps), logger)
	var adminSrv *http.Server // same certificate as the main listener with server.tls
	if adminLn != nil {
		adminSrv = newHTTPServer(api.NewAdmin(deps), logger)
	}
	if useTLS {
		srv.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12} // Go's default suites otherwise
		if adminSrv != nil {
			adminSrv.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		}
	}
	var metricsSrv *http.Server
	var metricsLn net.Listener
	if cfg.Metrics.Listen != "" {
		if metricsLn, err = net.Listen("tcp", cfg.Metrics.Listen); err != nil {
			return fmt.Errorf("metrics.listen: %w", err)
		}
		metricsSrv = newHTTPServer(metricsHandler(m, cfg.Metrics), logger)
	}

	// The pool and the sweeper outlive the signal: they stop after the HTTP drain.
	bgCtx, stopBackground := context.WithCancel(context.WithoutCancel(ctx))
	defer stopBackground()
	sweepDone := make(chan struct{})
	if runsJobs {
		pool.Start(bgCtx)
		go func() {
			defer close(sweepDone)
			sweep.Run(bgCtx)
		}()
	} else {
		close(sweepDone)
	}

	logger.Info("vde serve starting",
		"service_version", serviceVersion,
		"process_role", role,
		"instance", instance,
		"listen", ln.Addr().String(),
		"tls", useTLS,
		"trusted_proxy", cfg.Server.TrustedProxy,
		"insecure_http", cfg.Server.InsecureHTTP)
	errc := make(chan error, 3)
	start := func(srv *http.Server, ln net.Listener) {
		if useTLS {
			errc <- srv.ServeTLS(ln, cfg.Server.TLS.CertFile, cfg.Server.TLS.KeyFile)
			return
		}
		errc <- srv.Serve(ln)
	}
	go start(srv, ln)
	servers := 1
	if adminSrv != nil {
		servers++
		logger.Info("admin listener starting", "listen", adminLn.Addr().String(), "tls", useTLS)
		go start(adminSrv, adminLn)
	}
	if metricsSrv != nil {
		servers++
		logger.Info("metrics listener starting", "listen", metricsLn.Addr().String())
		go func() { errc <- metricsSrv.Serve(metricsLn) }()
	}

	var serveErr error
	select {
	case serveErr = <-errc: // a listener failed: stop everything
		servers--
	case <-ctx.Done():
	}
	logger.Info("vde serve stopping", "drain_timeout", drainTimeout.String())
	drainCtx, cancel := context.WithTimeout(context.Background(), drainTimeout)
	defer cancel()
	err = shutdown(drainCtx, srv)
	if metricsSrv != nil {
		err = errors.Join(err, shutdown(drainCtx, metricsSrv))
	}
	if adminSrv != nil {
		err = errors.Join(err, shutdown(drainCtx, adminSrv))
	}
	// After the HTTP drain no job is submitted any more; running jobs finish within the
	// rest of the drain time or keep their lease for the next start.
	if perr := pool.Stop(drainCtx); perr != nil {
		logger.Warn("worker pool stopped before its jobs finished; they resume after a restart", "error", perr)
	}
	if werr := hooks.Stop(drainCtx); werr != nil {
		logger.Warn("webhook deliveries still pending at shutdown were dropped", "error", werr)
	}
	stopBackground()
	<-sweepDone
	for range servers {
		if serr := <-errc; !errors.Is(serr, http.ErrServerClosed) {
			err = errors.Join(err, serr)
		}
	}
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		err = errors.Join(serveErr, err)
	}
	if err == nil {
		logger.Info("vde serve stopped")
	}
	return err
}

// newHTTPServer returns a server with the service's timeouts and its error log on logger.
func newHTTPServer(h http.Handler, logger *slog.Logger) *http.Server {
	return &http.Server{
		Handler:           h,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}
}

// shutdown drains srv until ctx is done, then cuts the remaining connections.
func shutdown(ctx context.Context, srv *http.Server) error {
	if err := srv.Shutdown(ctx); err != nil {
		srv.Close()
		return fmt.Errorf("drain: %w", err)
	}
	return nil
}

// metricsHandler serves GET /metrics alone on the metrics.listen listener, behind the
// metrics token when metrics.token_env is set. Its requests are counted under the route
// GET /metrics.
func metricsHandler(m *metrics.Metrics, c config.Metrics) http.Handler {
	h := m.Handler()
	if c.TokenEnv != "" {
		h = metrics.RequireToken(h, c.Token())
	}
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", h)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		mux.ServeHTTP(sw, r)
		m.ObserveHTTP(r.Pattern, sw.status)
	})
}

// progressObserver is the worker pool's observer: the metrics, plus the application's
// vde_jobs_in_progress gauge refreshed from the store (queued plus running) after each
// finished job, one indexed COUNT.
type progressObserver struct {
	*metrics.Metrics
	store job.Store
}

func (o progressObserver) JobFinished(app string, status job.Status, attempts int, d time.Duration) {
	o.Metrics.JobFinished(app, status, attempts, d)
	if n, err := o.store.CountInProgress(context.Background(), app); err == nil {
		o.SetJobsInProgress(app, n)
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (w *statusRecorder) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

// meter counts and times every provider call of every group in m by role, group,
// endpoint id, model id and outcome, with the tokens the provider reported, and counts
// failovers; nothing from the request or the answer is used.
func meter(roles extract.Roles, m *metrics.Metrics) {
	for _, g := range roles.Groups() {
		group := g.Name()
		g.OnCall = func(req provider.Request, served router.Served, resp provider.Response, err error, d time.Duration) {
			outcome := "ok"
			if err != nil {
				outcome = provider.Classify(err).String()
			}
			m.ObserveProviderCall(metrics.ProviderCall{
				Role: roleOf(req.System), Group: group, Endpoint: served.Endpoint, Model: served.Model,
				Outcome: outcome, Duration: d, InputTokens: resp.InputTokens, OutputTokens: resp.OutputTokens,
			})
		}
		g.OnFailover = func(req provider.Request, from, to router.Served) {
			m.ObserveFailover(roleOf(req.System), from.Endpoint, to.Endpoint)
		}
	}
}

// endpointStates reads every group's pair health for vde_endpoint_up.
func endpointStates(roles extract.Roles) func() []metrics.Endpoint {
	groups := roles.Groups()
	return func() []metrics.Endpoint {
		now := time.Now()
		var out []metrics.Endpoint
		for _, g := range groups {
			for _, p := range g.Health() {
				out = append(out, metrics.Endpoint{Group: g.Name(), Endpoint: p.Endpoint, Model: p.Model, Up: !now.Before(p.DownUntil)})
			}
		}
		return out
	}
}

// extractorGroups and judgeGroups split roles.Groups() by the role they serve, for
// /healthz: the extractor's group and escalation groups; the judge's group, escalation
// groups, second judge and feedback group. A group serving both is in both.
func extractorGroups(r extract.Roles) []*router.Group {
	out := []*router.Group{r.Extractor}
	for _, e := range r.ExtractorEscalation {
		out = append(out, e.Group)
	}
	return out
}

func judgeGroups(r extract.Roles) []*router.Group {
	out := []*router.Group{r.Judge}
	for _, e := range r.JudgeEscalation {
		out = append(out, e.Group)
	}
	for _, g := range []*router.Group{r.SecondJudge, r.FeedbackJudge} {
		if g != nil {
			out = append(out, g)
		}
	}
	return out
}

// allHealthy is healthy while every group is: a role whose escalation step or second
// judge has every pair marked down fails the jobs that reach it.
type allHealthy []*router.Group

func (gs allHealthy) Healthy() bool {
	for _, g := range gs {
		if g == nil || !g.Healthy() {
			return false
		}
	}
	return len(gs) > 0
}

// instanceID names this process in leases (worker ids) and in the instance label of
// vde_endpoint_up: the host name, the pid and a random suffix, so that containers that
// all run as pid 1, or a restart reusing a host name, never share a lease owner.
func instanceID() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "vde"
	}
	var b [3]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%s-%d-%x", host, os.Getpid(), b)
}

// webhookTargets are the configured applications' completion webhooks, with their
// secrets resolved from the environment at load.
func webhookTargets(cfg *config.Config) map[string]webhook.Target {
	targets := map[string]webhook.Target{}
	for _, a := range cfg.Applications {
		if a.Webhook.URL != "" {
			targets[a.ID] = webhook.Target{URL: a.Webhook.URL, Secret: []byte(a.Webhook.Secret())}
		}
	}
	return targets
}
