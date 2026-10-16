package api

import (
	"net/http"

	"github.com/kaanemec/vde/internal/metrics"
)

// metricsRoute is GET /metrics on the main listener (contract §12: protected by network
// or a separate token). With metrics.token_env set it requires that bearer token; with
// metrics.listen set the metrics live on that second listener (vde serve) and this
// route answers 404; with neither only loopback peers that did not come through a proxy
// get them. Without a registry (Deps.Metrics nil) the route answers 404.
func (s *server) metricsRoute() http.Handler {
	if s.deps.Metrics == nil {
		return http.HandlerFunc(routeNotFound)
	}
	var cfg struct{ tokenEnv, token, listen string }
	if c := s.deps.Config; c != nil {
		cfg.tokenEnv, cfg.token, cfg.listen = c.Metrics.TokenEnv, c.Metrics.Token(), c.Metrics.Listen
	}
	h := s.deps.Metrics.Handler()
	switch {
	case cfg.listen != "":
		return http.HandlerFunc(routeNotFound)
	case cfg.tokenEnv != "":
		return metrics.RequireToken(h, cfg.token) // an unset variable refuses everyone
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
	writeError(w, r, newError(codeNotFound, msgRouteNotFound))
}
