package api

import (
	"net/http"

	"github.com/kaanemec/vde/internal/config"
	"github.com/kaanemec/vde/internal/metrics"
)

// NewMetrics returns the handler of the metrics.listen listener: GET /metrics alone,
// behind the metrics token when metrics.token_env is set. Its requests are counted under
// the route GET /metrics.
func NewMetrics(m *metrics.Metrics, c config.Metrics) http.Handler {
	h := m.Handler()
	if c.TokenEnv != "" {
		h = metrics.RequireToken(h, c.Token())
	}
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", h)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w}
		mux.ServeHTTP(sw, r)
		m.ObserveHTTP(r.Pattern, sw.code())
	})
}

// metricsRoute is GET /metrics on the main listener (contract §12: protected by network
// or a separate token). With metrics.token_env set it requires that bearer token; with
// metrics.listen set the metrics live on that second listener (vde serve) and this
// route answers 404; with neither only loopback peers that did not come through a proxy
// get them. Without a registry (Deps.Metrics nil) the route answers 404.
func (s *server) metricsRoute() http.Handler {
	if s.deps.Metrics == nil {
		return http.HandlerFunc(routeNotFound)
	}
	c := s.deps.Config.Metrics
	h := s.deps.Metrics.Handler()
	switch {
	case c.Listen != "":
		return http.HandlerFunc(routeNotFound)
	case c.TokenEnv != "":
		return metrics.RequireToken(h, c.Token()) // an unset variable refuses everyone
	default:
		return notProxied(metrics.LoopbackOnly(h))
	}
}

// notProxied answers 404 to a request that carries a proxy's forwarding header: behind
// a reverse proxy on the same host every request has a loopback peer, so LoopbackOnly
// alone would publish the metrics. The headers are only ever used to refuse.
func notProxied(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, h := range []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Proto", "X-Forwarded-Host", "X-Real-Ip"} {
			if r.Header.Get(h) != "" {
				http.NotFound(w, r)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func routeNotFound(w http.ResponseWriter, r *http.Request) {
	writeError(w, newError(codeNotFound, msgRouteNotFound))
}
