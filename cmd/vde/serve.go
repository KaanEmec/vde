package main

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"errors"
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
	"github.com/kaanemec/vde/internal/blob"
	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/document"
	"github.com/kaanemec/vde/internal/extract"
	"github.com/kaanemec/vde/internal/job"
	"github.com/kaanemec/vde/internal/metrics"
	"github.com/kaanemec/vde/internal/pdf"
	"github.com/kaanemec/vde/internal/provider"
	"github.com/kaanemec/vde/internal/router"
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
	fs := newFlagSet("vde serve", serveUsage, stderr)
	path := fs.String("config", defaultConfigPath, "configuration file")
	role := fs.String("role", api.RoleAll, "process role: all, api or worker")
	if code, done := parseArgs(fs, args, stderr); done {
		return code
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
	p := &process{cfg: cfg, role: role, logger: logger, metrics: metrics.New(cfg.Metrics.GoRuntime)}
	if p.roles, err = extract.NewRoles(cfg, build); err != nil {
		return err
	}
	meter(p.roles, p.metrics)
	p.instance = instanceID()
	if p.runsJobs() {
		p.metrics.WatchEndpoints(p.instance, endpointStates(p.roles))
	}
	if p.store, err = openStore(ctx, cfg); err != nil {
		return err
	}
	defer p.store.Close()
	if p.blobs, err = openBlobs(ctx, cfg); err != nil {
		return err
	}
	logger.Info("storage opened", storageAttrs(cfg)...)
	// One pdfium engine per process (ADR 0010), started by the first PDF that needs it:
	// submit selects with it (--role api and all), workers re-select and render with it.
	if p.pdf, err = newPDFEngine(cfg, pdf.CacheDir(cfg.Server.DataDir)); err != nil {
		return err
	}
	defer func() { _ = p.pdf.Close() }()
	warmPDF(ctx, logger, p.pdf, cfg.Documents.PDF.Mode != "native")

	deps, err := p.buildDeps(ctx)
	if err != nil {
		return err
	}
	if err := p.newServers(deps, adminLn); err != nil {
		return err
	}
	// The pool and the sweeper outlive the signal: they stop after the HTTP drain.
	bgCtx, stopBackground := context.WithCancel(context.WithoutCancel(ctx))
	defer stopBackground()
	p.stopBackground = stopBackground
	p.startServers(bgCtx, ln, adminLn, deps.ServiceVersion)
	return p.drain(p.wait(ctx))
}

// process is one vde serve process: what serveAs opens, the workers and servers wired
// from it, and what drain stops.
type process struct {
	cfg      *config.Config
	role     string
	instance string
	logger   *slog.Logger
	metrics  *metrics.Metrics
	roles    extract.Roles
	store    *job.DB
	blobs    blob.Store
	pdf      *pdf.Engine

	pool  *worker.Pool
	sweep *worker.Sweeper
	hooks *webhook.Dispatcher

	srv, adminSrv, metricsSrv *http.Server // adminSrv and metricsSrv nil when not configured
	metricsLn                 net.Listener
	errc                      chan error // the Serve result of every started server
	servers                   int        // started servers whose result is still to be read
	sweepDone                 chan struct{}
	stopBackground            context.CancelFunc
}

// runsJobs reports whether the process runs the worker pool and the sweeper (roles all
// and worker).
func (p *process) runsJobs() bool { return p.role != api.RoleAPI }

// buildDeps writes the startup warnings and the configuration's audit event, mirrors the
// configured applications into the store, builds the workers and returns the API's
// dependencies.
func (p *process) buildDeps(ctx context.Context) (api.Deps, error) {
	cfg, logger := p.cfg, p.logger
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
	rec := audit.New(p.store, audit.Options{Logger: auditLogger})
	if err := syncApplications(ctx, p.store, rec, cfg.Applications, time.Now()); err != nil {
		return api.Deps{}, err
	}
	serviceVersion := extract.ServiceVersion(cfg, version.Version)
	if err := rec.Record(ctx, audit.ActorConfig, "", "", audit.ConfigLoaded{ServiceVersion: serviceVersion}); err != nil {
		logger.Warn("audit event not written", "kind", string(audit.ConfigLoadedKind), "error", err)
	}
	if err := p.logApplications(ctx); err != nil {
		return api.Deps{}, err
	}
	completions := p.newWorkers(rec)
	deps := api.Deps{
		Config:            cfg,
		Store:             p.store,
		ServiceVersion:    serviceVersion,
		AppServiceVersion: p.appVersion,
		Logger:            logger,
		Extractor:         p.roles.ExtractorHealthy,
		Judge:             p.roles.JudgeHealthy,
		Blobs:             p.blobs,
		Metrics:           p.metrics,
		Audit:             rec.WithActor(audit.ActorAPI),
		PDF:               document.PDFEngineOf(p.pdf),
		Role:              p.role,
	}
	if p.runsJobs() {
		// Within the process a submission wakes a worker at once; other processes' workers
		// find it at their next poll (worker.poll_interval).
		deps.Wake, deps.Completions = p.pool.Wake, completions
	}
	return deps, nil
}

// appVersion is the service_version of an application's jobs: it follows the stored row
// (attempt limit, HTML policy and judge settings) and the job's judge policy, so
// applications created or changed over the admin API get theirs.
func (p *process) appVersion(a job.Application, policy config.JudgePolicy) string {
	app := storedApplication(a)
	app.Documents.PDF.Mode = p.cfg.PDFModeOf(a.ID)
	if policy == "" {
		policy = p.cfg.JudgePolicyOf(app)
	}
	return extract.ApplicationServiceVersion(p.cfg, app, policy, version.Version)
}

// logApplications logs every enabled application with its service_version and sets its
// jobs in progress, those left queued or running by a previous process.
func (p *process) logApplications(ctx context.Context) error {
	apps, err := p.store.ListApplications(ctx)
	if err != nil {
		return err
	}
	for _, a := range apps {
		if !a.Enabled {
			continue
		}
		p.logger.Info("application enabled", "app_id", a.ID, "source", a.Source, "service_version", p.appVersion(a, ""))
		if n, err := p.store.CountInProgress(ctx, a.ID); err == nil {
			p.metrics.SetJobsInProgress(a.ID, n) // jobs left queued or running by a previous process
		}
	}
	return nil
}

// newWorkers builds the worker pool, the sweeper and the webhook sender, and returns the
// completion notifier. The completion hook wakes POST /v1/extract waiters and queues the
// webhooks: a worker process sends the webhooks of the jobs it finished; an API-only
// process has no in-process completions and POST /v1/extract polls the store.
func (p *process) newWorkers(rec *audit.Recorder) *worker.Notifier {
	completions := worker.NewNotifier()
	p.hooks = webhook.New(webhookTargets(p.cfg), webhook.Options{Audit: rec, Logger: p.logger})
	p.pool = worker.New(p.store, p.blobs, p.cfg, p.roles, worker.Options{
		WorkerID: p.instance,
		Metrics:  p.metrics,
		Logger:   p.logger,
		Audit:    rec.WithActor(audit.ActorWorker),
		OnFinish: func(j job.Job) {
			completions.Notify(j.ID)
			p.hooks.JobFinished(j)
			// vde_jobs_in_progress (queued plus running), one indexed COUNT.
			if n, err := p.store.CountInProgress(context.Background(), j.ApplicationID); err == nil {
				p.metrics.SetJobsInProgress(j.ApplicationID, n)
			}
		},
		PDF: document.PDFEngineOf(p.pdf),
	})
	p.sweep = worker.NewSweeper(p.store, p.blobs, p.cfg, worker.SweepOptions{
		Logger:  p.logger,
		Metrics: p.metrics,
		Audit:   rec.WithActor(audit.ActorSweeper),
		Lock:    p.store, // one worker process sweeps at a time
	})
	return completions
}

// newServers builds the HTTP server, the admin server when adminLn is set (same
// certificate as the main listener with server.tls) and the metrics server with its
// listener when metrics.listen is set.
func (p *process) newServers(deps api.Deps, adminLn net.Listener) error {
	p.srv = newHTTPServer(api.New(deps), p.logger)
	if adminLn != nil {
		p.adminSrv = newHTTPServer(api.NewAdmin(deps), p.logger)
	}
	if p.cfg.Server.TLS.CertFile != "" {
		p.srv.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12} // Go's default suites otherwise
		if p.adminSrv != nil {
			p.adminSrv.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		}
	}
	if p.cfg.Metrics.Listen != "" {
		var err error
		if p.metricsLn, err = net.Listen("tcp", p.cfg.Metrics.Listen); err != nil {
			return fmt.Errorf("metrics.listen: %w", err)
		}
		p.metricsSrv = newHTTPServer(api.NewMetrics(p.metrics, p.cfg.Metrics), p.logger)
	}
	return nil
}

// startServers starts the pool and the sweeper on bgCtx (roles all and worker), then
// every server on its listener.
func (p *process) startServers(bgCtx context.Context, ln, adminLn net.Listener, serviceVersion string) {
	cfg := p.cfg
	p.sweepDone = make(chan struct{})
	if p.runsJobs() {
		p.pool.Start(bgCtx)
		go func() {
			defer close(p.sweepDone)
			p.sweep.Run(bgCtx)
		}()
	} else {
		close(p.sweepDone)
	}

	useTLS := cfg.Server.TLS.CertFile != ""
	p.logger.Info("vde serve starting",
		"service_version", serviceVersion,
		"process_role", p.role,
		"instance", p.instance,
		"listen", ln.Addr().String(),
		"tls", useTLS,
		"trusted_proxy", cfg.Server.TrustedProxy,
		"insecure_http", cfg.Server.InsecureHTTP)
	p.errc = make(chan error, 3)
	start := func(srv *http.Server, ln net.Listener) {
		if useTLS {
			p.errc <- srv.ServeTLS(ln, cfg.Server.TLS.CertFile, cfg.Server.TLS.KeyFile)
			return
		}
		p.errc <- srv.Serve(ln)
	}
	go start(p.srv, ln)
	p.servers = 1
	if p.adminSrv != nil {
		p.servers++
		p.logger.Info("admin listener starting", "listen", adminLn.Addr().String(), "tls", useTLS)
		go start(p.adminSrv, adminLn)
	}
	if p.metricsSrv != nil {
		p.servers++
		p.logger.Info("metrics listener starting", "listen", p.metricsLn.Addr().String())
		go func() { p.errc <- p.metricsSrv.Serve(p.metricsLn) }()
	}
}

// wait returns when ctx is done (nil) or a server stopped (its Serve error).
func (p *process) wait(ctx context.Context) error {
	select {
	case serveErr := <-p.errc: // a listener failed: stop everything
		p.servers--
		return serveErr
	case <-ctx.Done():
		return nil
	}
}

// drain shuts down within drainTimeout: the servers first, then the pool, the webhook
// deliveries and the sweeper. It joins every error with serveErr, the error that ended
// serving (nil on a signal).
func (p *process) drain(serveErr error) (err error) {
	logger := p.logger
	logger.Info("vde serve stopping", "drain_timeout", drainTimeout.String())
	drainCtx, cancel := context.WithTimeout(context.Background(), drainTimeout)
	defer cancel()
	err = shutdown(drainCtx, p.srv)
	if p.metricsSrv != nil {
		err = errors.Join(err, shutdown(drainCtx, p.metricsSrv))
	}
	if p.adminSrv != nil {
		err = errors.Join(err, shutdown(drainCtx, p.adminSrv))
	}
	// After the HTTP drain no job is submitted any more; running jobs finish within the
	// rest of the drain time or keep their lease for the next start.
	if perr := p.pool.Stop(drainCtx); perr != nil {
		logger.Warn("worker pool stopped before its jobs finished; they resume after a restart", "error", perr)
	}
	if werr := p.hooks.Stop(drainCtx); werr != nil {
		logger.Warn("webhook deliveries still pending at shutdown were dropped", "error", werr)
	}
	p.stopBackground()
	<-p.sweepDone
	for range p.servers {
		if serr := <-p.errc; !errors.Is(serr, http.ErrServerClosed) {
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
