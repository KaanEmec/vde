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
	"syscall"
	"time"

	"github.com/kaanemec/vde/internal/api"
	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/extract"
	"github.com/kaanemec/vde/internal/job"
	"github.com/kaanemec/vde/internal/provider"
	"github.com/kaanemec/vde/internal/version"
)

const serveUsage = `Usage: vde serve [--config path]

Validates the configuration, opens the database (server.data_dir/vde.db), mirrors the
configured applications into it (an application no longer configured is disabled and its
tokens stop working), then serves the /v1 API, /healthz and /metrics on server.listen.
With server.tls the binary terminates TLS; otherwise it serves plain HTTP and refuses a
non-loopback listen address unless server.trusted_proxy (a reverse proxy terminates TLS)
or server.insecure_http (trusted private networks only) is true. SIGINT or SIGTERM stops
accepting connections and drains in-flight requests for up to 30 s.

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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := serve(ctx, cfg, ln, newLogger(stderr, cfg.Logging), newProvider); err != nil {
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

// serve opens the store, mirrors the configured applications into it and runs the HTTP
// server on ln until ctx is done, then drains in-flight requests. build makes each
// endpoint's provider. It returns nil after a clean shutdown.
func serve(ctx context.Context, cfg *config.Config, ln net.Listener, logger *slog.Logger,
	build func(*config.Endpoint) (provider.Provider, error)) error {
	roles, err := extract.NewRoles(cfg, build)
	if err != nil {
		ln.Close()
		return err
	}
	store, err := job.Open(cfg.Server.DatabasePath())
	if err != nil {
		ln.Close()
		return err
	}
	defer store.Close()
	if err := syncApplications(ctx, store, cfg.Applications, time.Now()); err != nil {
		ln.Close()
		return err
	}
	if len(cfg.Applications) == 0 {
		logger.Warn("no applications are configured: every /v1 request is refused")
	}
	if cfg.Server.InsecureHTTP {
		logger.Warn("SERVER.INSECURE_HTTP IS ON: plain HTTP is accepted from any peer, so tokens and " +
			"documents cross the network unencrypted; use it only on a trusted private network")
	}
	serviceVersion := extract.ServiceVersion(cfg, version.Version)
	versions := make(map[string]string, len(cfg.Applications))
	for _, a := range cfg.Applications {
		versions[a.ID] = extract.ApplicationServiceVersion(cfg, a, version.Version)
		logger.Info("application enabled", "app_id", a.ID, "service_version", versions[a.ID])
	}
	handler := api.New(api.Deps{
		Config:          cfg,
		Store:           store,
		ServiceVersion:  serviceVersion,
		ServiceVersions: versions,
		Logger:          logger,
		Extractor:       roles.Extractor,
		Judge:           roles.Judge,
	})
	useTLS := cfg.Server.TLS.CertFile != ""
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}
	if useTLS {
		srv.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12} // Go's default suites otherwise
	}
	// Epic 03: start the worker pool here, before accepting requests.

	logger.Info("vde serve starting",
		"service_version", serviceVersion,
		"listen", ln.Addr().String(),
		"tls", useTLS,
		"trusted_proxy", cfg.Server.TrustedProxy,
		"insecure_http", cfg.Server.InsecureHTTP)
	errc := make(chan error, 1)
	go func() {
		if useTLS {
			errc <- srv.ServeTLS(ln, cfg.Server.TLS.CertFile, cfg.Server.TLS.KeyFile)
			return
		}
		errc <- srv.Serve(ln)
	}()

	select {
	case err := <-errc:
		return err // the listener failed; nothing is in flight to drain
	case <-ctx.Done():
	}
	logger.Info("vde serve stopping", "drain_timeout", drainTimeout.String())
	drainCtx, cancel := context.WithTimeout(context.Background(), drainTimeout)
	defer cancel()
	err = srv.Shutdown(drainCtx)
	if err != nil {
		srv.Close() // drain timed out: cut the remaining connections
		err = fmt.Errorf("drain: %w", err)
	}
	// Epic 03: stop the worker here, after the HTTP drain, so no new job is submitted
	// while running jobs finish or give their lease back (drainCtx bounds it).
	if serr := <-errc; !errors.Is(serr, http.ErrServerClosed) {
		err = errors.Join(err, serr)
	}
	if err == nil {
		logger.Info("vde serve stopped")
	}
	return err
}
