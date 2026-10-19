package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/kaanemec/vde/internal/config"
)

const healthcheckUsage = `Usage: vde healthcheck [--config path]

Requests /healthz on the configured listener (server.listen; an all-interfaces address
is reached on loopback) with a 3 s timeout. Exits 0 when it answers 200, 1 otherwise.
The container image's HEALTHCHECK runs it; the image has no curl.
`

const healthcheckTimeout = 3 * time.Second

func runHealthcheck(args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet("vde healthcheck", flag.ContinueOnError)
	set.SetOutput(stderr)
	set.Usage = func() {
		fmt.Fprint(stderr, healthcheckUsage+flagsHeading)
		set.PrintDefaults()
	}
	configPath := operatorFlags(set)
	if code, done := parseFlags(set, args); done {
		return code
	}
	if set.NArg() > 0 {
		fmt.Fprintf(stderr, "vde healthcheck: unexpected argument %q\n", set.Arg(0))
		return exitUsage
	}
	cfg, err := loadOperatorConfig(configPath())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitFailure
	}
	status, err := checkHealth(cfg.Server, healthcheckTimeout)
	if err != nil {
		fmt.Fprintf(stderr, "vde healthcheck: %v\n", err)
		return exitFailure
	}
	if status != http.StatusOK {
		fmt.Fprintf(stderr, "vde healthcheck: /healthz answered %d\n", status)
		return exitFailure
	}
	fmt.Fprintln(stdout, "ok")
	return exitOK
}

// healthURL is the /healthz URL of the listener s describes, from this host.
func healthURL(s config.Server) (string, error) {
	host, port, err := net.SplitHostPort(s.Listen)
	if err != nil {
		return "", fmt.Errorf("server.listen %q: %w", s.Listen, err)
	}
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "127.0.0.1"
	}
	scheme := "http"
	if s.TLS.CertFile != "" {
		scheme = "https"
	}
	return scheme + "://" + net.JoinHostPort(host, port) + "/healthz", nil
}

// checkHealth requests /healthz and returns the status code.
func checkHealth(s config.Server, timeout time.Duration) (int, error) {
	u, err := healthURL(s)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return 0, err
	}
	client := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			// The service's own certificate names its public host, not the loopback
			// address checked here; the check carries no credential.
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12},
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	return resp.StatusCode, nil
}
