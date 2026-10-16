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

	"github.com/oklog/ulid/v2"
)

const (
	headerRequestID   = "X-Request-Id"
	maxRequestIDChars = 128
)

// wrap applies the middleware chain, outermost first: panic recovery, request id,
// HTTPS enforcement, access log. The per-route steps, authentication (epic 02) and rate
// limit and quota (epic 05), run inside the mux (routes).
func (s *server) wrap(h http.Handler) http.Handler {
	trustedProxy := s.deps.Config != nil && s.deps.Config.Server.TrustedProxy
	return s.recoverPanics(requestID(requireHTTPS(trustedProxy, s.accessLog(h))))
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
				writeError(sw, r, newError(codeInternal, msgInternal))
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
			id = "r_" + ulid.Make().String()
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
// serve also refuses to bind such a listener to a non-loopback address.
func requireHTTPS(trustedProxy bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS != nil || (trustedProxy && forwardedHTTPS(r)) || (!trustedProxy && loopbackPeer(r)) {
			next.ServeHTTP(w, r)
			return
		}
		writeError(w, r, newError(codeBadRequest, msgHTTPSRequired))
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

// accessLog writes one line per request with a fixed set of keys. It never logs the
// query string, headers other than the request id, or the body. Epic 02 adds app_id.
func (s *server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		status := sw.status
		if !sw.wroteHeader {
			status = http.StatusOK
		}
		s.log.LogAttrs(r.Context(), slog.LevelInfo, "request",
			slog.String("method", r.Method),
			slog.String("route", r.Pattern), // "" when no route matched
			slog.Int("status", status),
			slog.Int64("duration_ms", time.Since(start).Milliseconds()),
			slog.String("request_id", requestIDFrom(r.Context())))
	})
}

// authenticate is the per-route authentication step. Epic 02 replaces it: bearer token
// lookup by hash, expiry and revocation, scope check, the application in the context;
// 401 unauthorized and 403 forbidden through writeError.
func (s *server) authenticate(next http.Handler) http.Handler { return next }

// limit is the per-route rate limit and quota step, after authenticate. Epic 05 replaces
// it: X-Quota-Remaining and X-Quota-Reset on every authenticated response, 429
// rate_limited or quota_exhausted with Retry-After.
func (s *server) limit(next http.Handler) http.Handler { return next }

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

// Unwrap lets http.ResponseController reach the connection's writer.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
