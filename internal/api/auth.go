package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/kaanemec/vde/internal/audit"
	"github.com/kaanemec/vde/internal/auth"
	"github.com/kaanemec/vde/internal/job"
)

// Audit event kinds written by the HTTP layer.
const auditAuthFailed = string(audit.AuthFailedKind)

// lastUsedInterval bounds how often a token's last_used_at is written, so polls do not
// write on every request.
const lastUsedInterval = time.Minute

type principalKey struct{}

// ApplicationFrom returns the application the request authenticated as. Every handler
// behind a /v1 route can rely on it being present.
func ApplicationFrom(ctx context.Context) (job.Application, bool) {
	p, ok := ctx.Value(principalKey{}).(auth.Principal)
	return p.Application, ok
}

// ScopesFrom returns the scopes of the request's token, nil when unauthenticated.
func ScopesFrom(ctx context.Context) []string {
	p, _ := ctx.Value(principalKey{}).(auth.Principal)
	return p.Token.Scopes
}

// authenticate is the per-route authentication step: it resolves the bearer token
// through the store (no cache: a SQLite read per request is cheap, and a revocation by
// the CLI in another process takes effect on the next request), refuses with a generic
// 401 or a 403 when the token's scopes do not include need ("" accepts any scope), and
// puts the principal in the context. The request body is never read here; a refused
// request that announced a body gets Connection: close so the server does not drain it.
func (s *server) authenticate(need string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if s.deps.Store == nil { // a wiring mistake: fail closed
			writeError(w, r, newError(codeInternal, msgInternal))
			return
		}
		now := s.now()
		token, ok := bearerToken(r.Header)
		var p auth.Principal
		var err error
		if !ok {
			err = &auth.RefusedError{Reason: auth.ReasonMalformed}
		} else {
			p, err = auth.Authenticate(ctx, s.deps.Store, token, now)
		}
		var refused *auth.RefusedError
		switch {
		case errors.As(err, &refused):
			s.recordAudit(ctx, refused.ApplicationID, "", audit.AuthFailed{Reason: string(refused.Reason)})
			closeIfBodyPending(w, r)
			w.Header().Set("WWW-Authenticate", `Bearer realm="vde"`)
			writeError(w, r, newError(codeUnauthorized, msgUnauthorized))
			return
		case err != nil:
			s.log.LogAttrs(ctx, slog.LevelError, "authentication store error",
				slog.String("request_id", requestIDFrom(ctx)), slog.String("error", err.Error()))
			closeIfBodyPending(w, r)
			writeError(w, r, newError(codeInternal, msgInternal))
			return
		}
		if f, ok := ctx.Value(logFieldsKey{}).(*logFields); ok {
			f.appID = p.Application.ID
		}
		if !auth.Permits(p.Token.Scopes, need) {
			closeIfBodyPending(w, r)
			writeError(w, r, newError(codeForbidden, msgForbidden))
			return
		}
		if now.Sub(p.Token.LastUsedAt) >= lastUsedInterval {
			if err := s.deps.Store.TouchTokenLastUsed(ctx, p.Token.ID, now); err != nil {
				s.log.LogAttrs(ctx, slog.LevelWarn, "token last_used_at not updated",
					slog.String("request_id", requestIDFrom(ctx)), slog.String("error", err.Error()))
			}
		}
		if v := s.deps.ServiceVersions[p.Application.ID]; v != "" && w.Header().Get(headerServiceVersion) != "" {
			w.Header().Set(headerServiceVersion, v)
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, principalKey{}, p)))
	})
}

// bearerToken returns the token of the request's single Authorization: Bearer header:
// "" and true when there is none, false when the header is not a bearer credential.
func bearerToken(h http.Header) (string, bool) {
	values := h.Values("Authorization")
	switch len(values) {
	case 0:
		return "", true
	case 1:
	default:
		return "", false
	}
	scheme, token, found := strings.Cut(values[0], " ")
	token = strings.TrimSpace(token)
	if !found || !strings.EqualFold(scheme, "Bearer") || token == "" {
		return "", false
	}
	return token, true
}

// closeIfBodyPending asks the server to close the connection after the response when
// the request announced a body that the refusal leaves unread.
func closeIfBodyPending(w http.ResponseWriter, r *http.Request) {
	if r.ContentLength != 0 || len(r.TransferEncoding) > 0 {
		w.Header().Set("Connection", "close")
	}
}

// recordAudit appends one audit event through the audit package's typed details (never
// content), tagged with the API actor, with Deps.Audit or, when that is nil, a recorder
// on the store (authentication failures and deletions are always audited). A failure is
// logged and does not change the response.
func (s *server) recordAudit(ctx context.Context, applicationID, jobID string, d audit.Details) {
	rec := s.deps.Audit
	if rec == nil {
		rec = audit.New(s.deps.Store, audit.Options{Clock: s.now})
	}
	if err := rec.Record(context.WithoutCancel(ctx), audit.ActorAPI, applicationID, jobID, d); err != nil {
		s.log.LogAttrs(ctx, slog.LevelWarn, "audit event not written",
			slog.String("kind", string(d.AuditKind())), slog.String("job_id", jobID), slog.String("error", err.Error()))
	}
}

func (s *server) now() time.Time {
	if s.deps.Now != nil {
		return s.deps.Now()
	}
	return time.Now()
}
