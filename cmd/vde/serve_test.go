package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kaanemec/vde/internal/api"
	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/provider"
)

// serveOptions are startServe's choices; the zero value runs every role with the
// configured providers and no environment, and discards the log.
type serveOptions struct {
	role   string                                            // default api.RoleAll
	build  func(*config.Endpoint) (provider.Provider, error) // default newProvider
	getenv func(string) string                               // default: nothing set
	logs   io.Writer                                         // the JSON log; default discarded
	admin  bool                                              // also serve the admin API
}

// serveWith answers every endpoint with p.
func serveWith(p provider.Provider) func(*config.Endpoint) (provider.Provider, error) {
	return func(*config.Endpoint) (provider.Provider, error) { return p, nil }
}

// startServe runs vde serve with the configuration at cfgPath on a loopback port (the
// admin API on a second one with opts.admin) until /healthz answers, so the applications
// are synced. It returns both base URLs ("" without admin) and a stop that ends serve
// and checks it returned nil in time; stop also runs at cleanup.
func startServe(t *testing.T, cfgPath string, opts serveOptions) (base, admin string, stop func()) {
	t.Helper()
	if opts.role == "" {
		opts.role = api.RoleAll
	}
	if opts.build == nil {
		opts.build = newProvider
	}
	if opts.getenv == nil {
		opts.getenv = func(string) string { return "" }
	}
	logger := slog.New(slog.DiscardHandler)
	if opts.logs != nil {
		logger = slog.New(slog.NewJSONHandler(opts.logs, nil))
	}
	cfg, issues, err := loadConfigEnv(cfgPath, false, opts.getenv)
	if err != nil {
		t.Fatalf("%v: %v", err, issues)
	}
	listen := func() net.Listener {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		return ln
	}
	ln := listen()
	var adminLn net.Listener
	if opts.admin {
		adminLn = listen()
		admin = "http://" + adminLn.Addr().String()
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serveAs(ctx, opts.role, cfg, ln, adminLn, logger, opts.build) }()
	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("serve = %v, want nil after shutdown", err)
				}
			case <-time.After(e2eWait(10 * time.Second)):
				t.Error("serve did not return after the context ended")
			}
		})
	}
	t.Cleanup(stop)
	base = "http://" + ln.Addr().String()
	waitHealthz(t, base)
	return base, admin, stop
}

func TestCheckListen(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		server  config.Server
		wantErr bool
	}{
		{name: "IPv4 loopback", server: config.Server{Listen: "127.0.0.1:8090"}},
		{name: "IPv6 loopback", server: config.Server{Listen: "[::1]:8090"}},
		{name: "localhost", server: config.Server{Listen: "localhost:8090"}},
		{name: "all interfaces refused", server: config.Server{Listen: ":8090"}, wantErr: true},
		{name: "public address refused", server: config.Server{Listen: "203.0.113.7:8090"}, wantErr: true},
		{name: "trusted proxy", server: config.Server{Listen: ":8090", TrustedProxy: true}},
		{name: "TLS", server: config.Server{Listen: ":8443", TLS: config.TLS{CertFile: "c.pem", KeyFile: "k.pem"}}},
		{name: "insecure http", server: config.Server{Listen: ":8090", InsecureHTTP: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if err := checkListen(tt.server); (err != nil) != tt.wantErr {
				t.Errorf("checkListen(%+v) = %v, want error %t", tt.server, err, tt.wantErr)
			}
		})
	}
}

func TestRunServeRefusesPlainNonLoopback(t *testing.T) {
	t.Parallel()
	path := writeConfig(t, noPDFConfig+"server: {listen: \"0.0.0.0:0\"}\n")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"serve", "--config", path}, &stdout, &stderr); code != exitFailure {
		t.Errorf("exit = %d, want %d", code, exitFailure)
	}
	if !strings.Contains(stderr.String(), "not a loopback address") {
		t.Errorf("stderr = %q, want the loopback refusal", stderr.String())
	}
}

func TestServeAndShutdown(t *testing.T) {
	t.Parallel()
	path := writeConfig(t, noPDFConfig+"server: {data_dir: "+strconv.Quote(t.TempDir())+"}\n")
	var logs syncBuffer
	base, _, stop := startServe(t, path, serveOptions{logs: &logs})
	resp, err := http.Get(base + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || strings.TrimSpace(string(body)) != `{"status":"ok"}` {
		t.Errorf("GET /healthz = %d %s, want 200 {\"status\":\"ok\"}", resp.StatusCode, body)
	}
	if resp.Header.Get("X-Request-Id") == "" {
		t.Error("no X-Request-Id")
	}
	stop()
	for _, want := range []string{`"service_version":"vde-`, `"msg":"vde serve stopped"`} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log lacks %s: %s", want, logs.String())
		}
	}
}

// syncBuffer is a bytes.Buffer safe for the server's goroutines to log into.
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
