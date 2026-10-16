package metrics

import (
	"crypto/subtle"
	"net"
	"net/http"
	"strings"
)

// RequireToken wraps h so a request needs "Authorization: Bearer <token>". The token is
// compared in constant time. An empty token rejects every request.
func RequireToken(h http.Handler, token string) http.Handler {
	want := []byte(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || len(want) == 0 || subtle.ConstantTimeCompare([]byte(got), want) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="metrics"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// LoopbackOnly wraps h so only requests whose peer address (r.RemoteAddr, never a
// forwarded header) is a loopback address are served; others get 404, so the endpoint
// does not announce itself on a public listener.
func LoopbackOnly(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
			http.NotFound(w, r)
			return
		}
		h.ServeHTTP(w, r)
	})
}
