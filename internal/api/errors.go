package api

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"
)

// Error codes of contract §9. The service may add codes within /v1; codeMethodNotAllowed
// is such an addition.
const (
	codeBadRequest           = "bad_request"
	codeUnauthorized         = "unauthorized"
	codeForbidden            = "forbidden"
	codeNotFound             = "not_found"
	codeMethodNotAllowed     = "method_not_allowed"
	codePayloadTooLarge      = "payload_too_large"
	codeUnsupportedMediaType = "unsupported_media_type"
	codeUnsupportedDocument  = "unsupported_document"
	codeInvalidSchema        = "invalid_schema"
	codeIdempotencyMismatch  = "idempotency_mismatch"
	codeRateLimited          = "rate_limited"
	codeQuotaExhausted       = "quota_exhausted"
	codeInternal             = "internal"
	codeProviderUnavailable  = "provider_unavailable"
	codeUnavailable          = "unavailable"
	codeTimeout              = "timeout"
)

// codeStatus is the HTTP status of each code, so a code is never sent with another status.
var codeStatus = map[string]int{
	codeBadRequest:           http.StatusBadRequest,
	codeUnauthorized:         http.StatusUnauthorized,
	codeForbidden:            http.StatusForbidden,
	codeNotFound:             http.StatusNotFound,
	codeMethodNotAllowed:     http.StatusMethodNotAllowed,
	codePayloadTooLarge:      http.StatusRequestEntityTooLarge,
	codeUnsupportedMediaType: http.StatusUnsupportedMediaType,
	codeUnsupportedDocument:  http.StatusUnsupportedMediaType,
	codeInvalidSchema:        http.StatusUnprocessableEntity,
	codeIdempotencyMismatch:  http.StatusUnprocessableEntity,
	codeRateLimited:          http.StatusTooManyRequests,
	codeQuotaExhausted:       http.StatusTooManyRequests,
	codeInternal:             http.StatusInternalServerError,
	codeProviderUnavailable:  http.StatusBadGateway,
	codeUnavailable:          http.StatusServiceUnavailable,
	codeTimeout:              http.StatusGatewayTimeout,
}

// Messages are fixed templates. A template interpolates at most one value, a part name
// (%s, only a name the service itself defines) or a limit (%d), and never anything read
// from a document, instructions, schema or model output (contract §2, §9). The lint test
// in errors_lint_test.go keeps every error built from these constants.
const (
	msgRouteNotFound    = "No route matches the request path."
	msgMethodNotAllowed = "The route does not allow this method."
	msgHTTPSRequired    = "HTTPS is required."
	msgUnauthorized     = "A valid bearer token is required."
	msgForbidden        = "The token's scopes do not allow this request."
	msgInternal         = "The service failed to handle the request."
	msgServiceUnhealthy = "The service cannot accept jobs right now."
)

// apiError is one error response: status, contract code, fixed message and, for 429 and
// 503, an optional Retry-After. Build it with newError only.
type apiError struct {
	status     int
	code       string
	message    string
	retryAfter time.Duration
}

// newError returns the error for code with the message template filled with args. The
// status comes from the code.
func newError(code, template string, args ...any) apiError {
	status, ok := codeStatus[code]
	if !ok {
		status, code = http.StatusInternalServerError, codeInternal
	}
	msg := template
	if len(args) > 0 {
		msg = fmt.Sprintf(template, args...)
	}
	return apiError{status: status, code: code, message: msg}
}

// withRetryAfter returns e with a Retry-After of d, rounded up to whole seconds.
func (e apiError) withRetryAfter(d time.Duration) apiError {
	e.retryAfter = d
	return e
}

type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

// writeError writes e in the contract §9 shape. It keeps headers already set (the quota
// headers of epic 05, X-Service-Version), adds Retry-After when e has one, and takes
// request_id from the X-Request-Id response header set by the request id middleware.
func writeError(w http.ResponseWriter, _ *http.Request, e apiError) {
	h := w.Header()
	if e.retryAfter > 0 {
		h.Set("Retry-After", strconv.Itoa(int(math.Ceil(e.retryAfter.Seconds()))))
	}
	h.Del("Content-Length")
	writeJSON(w, e.status, errorBody{Error: errorDetail{
		Code:      e.code,
		Message:   e.message,
		RequestID: h.Get(headerRequestID),
	}})
}

// writeJSON writes v as the JSON body with status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v) // the client went away; nothing left to tell it
}
