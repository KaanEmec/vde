package api

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"github.com/kaanemec/vde/internal/job"
)

const (
	headerRequestID      = "X-Request-Id"
	headerServiceVersion = "X-Service-Version"
	maxRequestIDChars    = 128
)

// wrap applies the middleware chain, outermost first: panic recovery, request id,
// HTTPS enforcement, access log. The per-route steps, authentication (auth.go) and rate
// limit and quota (ratelimit.go, quota.go), run inside the mux (routes). The admin listener is reached
// directly, never through the reverse proxy, so server.trusted_proxy does not apply to it.
func (s *server) wrap(h http.Handler) http.Handler {
	srv := s.deps.Config.Server
	return s.recoverPanics(requestID(requireHTTPS(srv.TrustedProxy && !s.admin, srv.InsecureHTTP, s.accessLog(h))))
}

// recoverPanics answers a panicking handler with a 500 internal error body (never the
// panic value or stack) and logs the stack. A runtime error's text is logged; any other
// panic value only by type, since it could carry content.
func (s *server) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w}
		defer func() {
			v := recover()
			if v == nil {
				return
			}
			if err, ok := v.(error); ok && errors.Is(err, http.ErrAbortHandler) {
				panic(v) // net/http's own signal to abort the response
			}
			panicked := "non-runtime panic"
			if re, ok := v.(runtime.Error); ok {
				panicked = re.Error()
			}
			_, pattern := s.mux.Handler(r)
			s.log.Error("handler panic",
				"method", r.Method,
				"route", pattern,
				"request_id", w.Header().Get(headerRequestID),
				"panic", panicked,
				"stack", string(debug.Stack()))
			if !sw.wroteHeader {
				writeError(sw, newError(codeInternal, msgInternal))
			}
		}()
		next.ServeHTTP(sw, r)
	})
}

// requestID echoes the client's X-Request-Id when it is 1 to 128 visible ASCII
// characters, else generates r_<ULID>, and sets it on the response before anything is
// written (contract §2).
func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(headerRequestID)
		if !validRequestID(id) {
			id = "r_" + job.NewULID()
		}
		h := w.Header()
		h.Set(headerRequestID, id)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Cache-Control", "no-store") // job bodies carry personal data
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
	})
}

type requestIDKey struct{}

// requestIDFrom returns the request id the requestID middleware put in ctx, or "".
func requestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

func validRequestID(id string) bool {
	if id == "" || len(id) > maxRequestIDChars {
		return false
	}
	for i := range len(id) {
		if id[i] < 0x21 || id[i] > 0x7e {
			return false
		}
	}
	return true
}

// requireHTTPS refuses plain-HTTP requests outside loopback (contract §2). A request on a
// TLS connection passes. Behind a trusted proxy every other request must carry
// X-Forwarded-Proto: https, from loopback too, since the proxy itself usually connects
// from loopback. Without a trusted proxy only loopback peers may use plain HTTP; vde
// serve also refuses to bind such a listener to a non-loopback address. insecure lets
// every request through (server.insecure_http, trusted private networks only). GET
// /healthz is exempt: it carries nothing sensitive and container health checks and
// orchestrator probes reach it directly, without the proxy's X-Forwarded-Proto.
func requireHTTPS(trustedProxy, insecure bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		healthz := r.URL.Path == "/healthz" && (r.Method == http.MethodGet || r.Method == http.MethodHead)
		if healthz || insecure || r.TLS != nil || (trustedProxy && forwardedHTTPS(r)) || (!trustedProxy && loopbackPeer(r)) {
			next.ServeHTTP(w, r)
			return
		}
		writeError(w, newError(codeBadRequest, msgHTTPSRequired))
	})
}

// forwardedHTTPS reports whether the nearest proxy's X-Forwarded-Proto is https.
func forwardedHTTPS(r *http.Request) bool {
	proto, _, _ := strings.Cut(r.Header.Get("X-Forwarded-Proto"), ",")
	return strings.EqualFold(strings.TrimSpace(proto), "https")
}

func loopbackPeer(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	addr, err := netip.ParseAddr(host)
	return err == nil && addr.Unmap().IsLoopback()
}

// logFields carries what inner steps learn for the access log line: authenticate sets
// the application id.
type logFields struct{ appID string }

type logFieldsKey struct{}

// accessLog writes one line per request with a fixed set of keys. It never logs the
// query string, headers other than the request id, or the body. app_id is "" when the
// request did not authenticate.
func (s *server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		fields := &logFields{}
		r = r.WithContext(context.WithValue(r.Context(), logFieldsKey{}, fields))
		next.ServeHTTP(sw, r)
		status := sw.code()
		s.log.LogAttrs(r.Context(), slog.LevelInfo, "request",
			slog.String("method", r.Method),
			slog.String("route", r.Pattern), // "" when no route matched
			slog.Int("status", status),
			slog.Int64("duration_ms", time.Since(start).Milliseconds()),
			slog.String("request_id", requestIDFrom(r.Context())),
			slog.String("app_id", fields.appID))
		s.deps.Metrics.ObserveHTTP(r.Pattern, status) // the route pattern, never the path
	})
}

// limit is the per-route rate limit and quota step, after authenticate: it sets
// X-Quota-Remaining and X-Quota-Reset before the handler runs, so every authenticated
// response carries them, errors included (quota.go), then takes a token from the
// application's requests-per-minute bucket and answers 429 rate_limited with
// Retry-After when it is empty (ratelimit.go). The submit handler charges the quota
// and applies the submission limits itself.
func (s *server) limit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		app, ok := ApplicationFrom(r.Context())
		if !ok { // authenticate always runs first; fail closed if it did not
			writeError(w, newError(codeInternal, msgInternal))
			return
		}
		if !s.quotaHeaders(w, r, app) {
			return
		}
		if wait, ok := s.takeRate(r.Context(), app.ID, bucketRequests, app.RequestsPerMinute, s.now()); !ok {
			closeIfBodyPending(w, r)
			writeError(w, newError(codeRateLimited, msgRateLimited).withRetryAfter(wait))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// statusWriter records whether and with which status the header was written.
type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusWriter) WriteHeader(status int) {
	if !w.wroteHeader {
		w.status, w.wroteHeader = status, true
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.status, w.wroteHeader = http.StatusOK, true
	}
	return w.ResponseWriter.Write(b)
}

// code is the status written, 200 when the handler wrote nothing.
func (w *statusWriter) code() int {
	if !w.wroteHeader {
		return http.StatusOK
	}
	return w.status
}

// Unwrap lets http.ResponseController reach the connection's writer.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
