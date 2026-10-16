package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kaanemec/vde/internal/config"
)

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
	path := writeConfig(t, noPDFConfig)
	cfg, _, err := loadConfigEnv(path, false, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var logs syncBuffer
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serve(ctx, cfg, ln, slog.New(slog.NewJSONHandler(&logs, nil))) }()

	resp, err := http.Get("http://" + ln.Addr().String() + "/healthz")
	if err != nil {
		cancel()
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

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("serve = %v, want nil after shutdown", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not return after the context ended")
	}
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
