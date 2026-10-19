// Package providertest holds what the adapter tests share: a recording test server, an
// endpoint parsed the way the service parses it, and checks on provider errors.
package providertest

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/provider"
)

// Captured is the last request a Serve server saw.
type Captured struct {
	Method string
	URL    *url.URL
	Header http.Header
	Body   []byte
}

// Serve starts a server that records each request into the returned Captured and then
// answers it with handler. The server closes when the test ends.
func Serve(t testing.TB, handler http.HandlerFunc) (*httptest.Server, *Captured) {
	t.Helper()
	got := &Captured{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading request body: %v", err)
		}
		got.Method, got.URL, got.Header, got.Body = r.Method, r.URL, r.Header.Clone(), body
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

// Reply answers with status, the header and body.
func Reply(status int, header http.Header, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		for k, v := range header {
			w.Header()[k] = v
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

// Endpoint parses one endpoint from its YAML lines, unindented, the way the service does;
// every api_key_env resolves to key.
func Endpoint(t testing.TB, key string, lines ...string) *config.Endpoint {
	t.Helper()
	yml := "endpoints:\n  e:\n    " + strings.Join(lines, "\n    ") + "\n"
	cfg, err := config.Parse([]byte(yml), func(string) string { return key })
	if err != nil {
		t.Fatalf("config.Parse: %v", err)
	}
	return cfg.Endpoints["e"]
}

// AsError returns err as a *provider.Error and fails the test when it is not one.
func AsError(t testing.TB, err error) *provider.Error {
	t.Helper()
	var pe *provider.Error
	if !errors.As(err, &pe) {
		t.Fatalf("error = %v, want a *provider.Error", err)
	}
	return pe
}

// AssertNoContent fails the test when the error message contains any of secrets.
func AssertNoContent(t testing.TB, err error, secrets ...string) {
	t.Helper()
	msg := err.Error()
	for _, s := range secrets {
		if strings.Contains(msg, s) {
			t.Errorf("error message %q contains %q", msg, s)
		}
	}
}
