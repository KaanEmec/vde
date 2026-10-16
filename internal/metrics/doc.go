// Package metrics exposes the service's Prometheus series (contract §12) from a
// private registry. Label values come from configuration ids (application, endpoint,
// model, route pattern) and fixed vocabularies (status, outcome, role), never from
// request data, so a series can neither carry content nor grow without bound.
package metrics
