package main

import (
	"context"
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
	"path/filepath"
	"syscall"
	"time"

	"github.com/kaanemec/vde/internal/api"
	"github.com/kaanemec/vde/internal/audit"
	"github.com/kaanemec/vde/internal/blob"
	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/extract"
	"github.com/kaanemec/vde/internal/job"
	"github.com/kaanemec/vde/internal/logging"
	"github.com/kaanemec/vde/internal/metrics"
	"github.com/kaanemec/vde/internal/provider"
	"github.com/kaanemec/vde/internal/router"
	"github.com/kaanemec/vde/internal/sweeper"
	"github.com/kaanemec/vde/internal/version"
	"github.com/kaanemec/vde/internal/worker"
)

const serveUsage = `Usage: vde serve [--config path]

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

Flags:
`

// Server timeouts. A request may upload up to limits.document_bytes plus pages, so the
// whole request read is bounded at 60 s (job 01) and the response write after it.
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

	cfg, issues, err := loadConfig(*path, false)
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
	if cfg.Admin.Listen != "" {
		if adminLn, err = net.Listen("tcp", cfg.Admin.Listen); err != nil {
			ln.Close()
			fmt.Fprintf(stderr, "vde serve: admin.listen: %v\n", err)
			return exitFailure
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := serve(ctx, cfg, ln, adminLn, newLogger(stderr, cfg.Logging), newProvider); err != nil {
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

// serve opens the store, mirrors the configured applications into it, starts the worker
// pool and the expiry sweeper, and runs the HTTP server on ln (and the metrics listener
// of metrics.listen, and the admin API on adminLn unless it is nil) until ctx is done. Then it drains in-flight requests, stops the
// pool (running jobs get the rest of the drain time to finish, or keep their lease and
// persisted attempts for the next start) and the sweeper. build makes each endpoint's
// provider. It returns nil after a clean shutdown.
func serve(ctx context.Context, cfg *config.Config, ln, adminLn net.Listener, logger *slog.Logger,
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
	roles, err := extract.NewRoles(cfg, meteredFactory(cfg, build, m))
	if err != nil {
		return err
	}
	for _, g := range []*router.Group{roles.Extractor, roles.Judge} { // may be one group
		g.OnFailover = func(req provider.Request, from, to router.Served) {
			m.ObserveFailover(roleOf(req.System), from.Endpoint, to.Endpoint)
		}
	}
	store, err := job.Open(cfg.Server.DatabasePath())
	if err != nil {
		return err
	}
	defer store.Close()
	blobs, err := blob.NewFS(filepath.Join(cfg.Server.DataDir, "blobs"))
	if err != nil {
		return err
	}

	if len(cfg.Applications) == 0 {
		logger.Warn("no applications are configured: every /v1 request is refused")
	}
	if cfg.Server.InsecureHTTP {
		logger.Warn("SERVER.INSECURE_HTTP IS ON: plain HTTP is accepted from any peer, so tokens and " +
			"documents cross the network unencrypted; use it only on a trusted private network")
	}
	if cfg.Debug.LogContent { // config validation refuses it with a non-loopback listener
		contentLog, err := logging.OpenContentLog(filepath.Join(cfg.Server.DataDir, "content.log"))
		if err != nil {
			return err
		}
		defer contentLog.Close()
		logger.Warn("DEBUG.LOG_CONTENT IS ON: document content, instructions and model answers are " +
			"written to content.log in server.data_dir; never use it in production")
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
	// The service_version of an application's jobs follows its stored row (attempt limit
	// and HTML policy), so applications created or changed over the admin API get theirs.
	appVersion := func(a job.Application) string {
		return extract.ApplicationServiceVersion(cfg, config.Application{
			MaxAttempts: a.MaxAttempts, Documents: config.AppDocuments{HTML: a.HTMLPolicy},
		}, version.Version)
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
		logger.Info("application enabled", "app_id", a.ID, "source", a.Source, "service_version", appVersion(a))
		if n, err := store.CountInProgress(ctx, a.ID); err == nil {
			m.SetJobsInProgress(a.ID, n) // jobs left queued or running by a previous process
		}
	}

	pool := worker.New(store, blobs, cfg, roles, worker.Options{
		Size:         cfg.Worker.Size,
		PollInterval: cfg.Worker.PollInterval,
		Observer:     progressObserver{Metrics: m, store: store},
		Logger:       logger,
		Audit:        rec.WithActor(audit.ActorWorker),
	})
	sweep := sweeper.New(store, blobs, sweeper.Options{
		Interval:               cfg.Retention.SweepInterval,
		JobTimeout:             cfg.Limits.JobTimeout,
		AuditRetention:         cfg.Audit.Retention,
		Logger:                 logger,
		Observer:               m,
		Audit:                  rec.WithActor(audit.ActorSweeper),
		DeleteInputsAtTerminal: inputsAtTerminal,
	})
	deps := api.Deps{
		Config:            cfg,
		Store:             store,
		ServiceVersion:    serviceVersion,
		AppServiceVersion: appVersion,
		Logger:            logger,
		Extractor:         roles.Extractor,
		Judge:             roles.Judge,
		Blobs:             blobs,
		Wake:              pool.Wake,
		Metrics:           m,
		Audit:             rec.WithActor(audit.ActorAPI),
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
	pool.Start(bgCtx)
	sweepDone := make(chan struct{})
	go func() {
		defer close(sweepDone)
		sweep.Run(bgCtx)
	}()

	logger.Info("vde serve starting",
		"service_version", serviceVersion,
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

// meteredFactory wraps build so every provider call is counted and timed in m by role,
// endpoint id, model id and outcome; nothing from the request or the answer is used.
func meteredFactory(cfg *config.Config, build func(*config.Endpoint) (provider.Provider, error), m *metrics.Metrics) func(*config.Endpoint) (provider.Provider, error) {
	names := make(map[*config.Endpoint]string, len(cfg.Endpoints))
	for name, e := range cfg.Endpoints {
		names[e] = name
	}
	return func(e *config.Endpoint) (provider.Provider, error) {
		p, err := build(e)
		if err != nil {
			return nil, err
		}
		return metered{endpoint: names[e], p: p, m: m}, nil
	}
}

type metered struct {
	endpoint string
	p        provider.Provider
	m        *metrics.Metrics
}

func (p metered) Complete(ctx context.Context, req provider.Request) (provider.Response, error) {
	start := time.Now()
	resp, err := p.p.Complete(ctx, req)
	outcome := "ok"
	if err != nil {
		outcome = provider.Classify(err).String()
	}
	p.m.ObserveProviderCall(roleOf(req.System), p.endpoint, req.Model, outcome, time.Since(start))
	return resp, err
}
