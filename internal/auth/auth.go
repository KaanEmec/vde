package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kaanemec/vde/internal/job"
)

// Reason is why a token was refused. It is the only detail of an auth.failed audit
// event; the response to the caller is always the same generic 401.
type Reason string

// Refusal reasons.
const (
	ReasonMissing     Reason = "missing"
	ReasonMalformed   Reason = "malformed"
	ReasonUnknown     Reason = "unknown_token"
	ReasonWrongSecret Reason = "wrong_secret"
	ReasonRevoked     Reason = "revoked"
	ReasonExpired     Reason = "expired"
	ReasonDisabled    Reason = "application_disabled"
)

// RefusedError is returned by Authenticate for a token that must get 401.
type RefusedError struct {
	Reason        Reason
	ApplicationID string // the token's application, "" when the token is unknown
}

func (e *RefusedError) Error() string { return "auth: token refused: " + string(e.Reason) }

// Reader is the part of the store Authenticate reads.
type Reader interface {
	GetTokenByID(ctx context.Context, id string) (job.Token, error)
	GetApplication(ctx context.Context, id string) (job.Application, error)
}

// Principal is an authenticated caller.
type Principal struct {
	Application job.Application
	Token       job.Token // the row: id, scopes, expiry; Hash is the stored hash only
}

// Authenticate checks a bearer token against the store: format, id, secret, revocation,
// expiry and the application being enabled. A refusal is a *RefusedError; any other
// error is the store failing. There is no cache: every request reads the store, so a
// revocation from the CLI takes effect on the next request.
func Authenticate(ctx context.Context, r Reader, token string, now time.Time) (Principal, error) {
	if token == "" {
		return Principal{}, &RefusedError{Reason: ReasonMissing}
	}
	id, secret, err := Parse(token)
	if err != nil {
		return Principal{}, &RefusedError{Reason: ReasonMalformed}
	}
	t, err := r.GetTokenByID(ctx, id)
	if errors.Is(err, job.ErrNotFound) {
		return Principal{}, &RefusedError{Reason: ReasonUnknown}
	}
	if err != nil {
		return Principal{}, err
	}
	refuse := func(reason Reason) (Principal, error) {
		return Principal{}, &RefusedError{Reason: reason, ApplicationID: t.ApplicationID}
	}
	switch {
	case !Verify(secret, t.Hash):
		return refuse(ReasonWrongSecret)
	case !t.RevokedAt.IsZero():
		return refuse(ReasonRevoked)
	case !now.Before(t.ExpiresAt):
		return refuse(ReasonExpired)
	}
	app, err := r.GetApplication(ctx, t.ApplicationID)
	if errors.Is(err, job.ErrNotFound) {
		return refuse(ReasonDisabled)
	}
	if err != nil {
		return Principal{}, err
	}
	if !app.Enabled {
		return refuse(ReasonDisabled)
	}
	return Principal{Application: app, Token: t}, nil
}

// Creator is the part of the store Create needs.
type Creator interface {
	GetApplication(ctx context.Context, id string) (job.Application, error)
	CreateToken(ctx context.Context, t job.Token) error
}

// Request describes a token to issue.
type Request struct {
	ApplicationID string
	Scopes        []string // from ParseScopes
	ExpiresAt     time.Time
	Label         string
}

const maxLabelChars = 200

// Create issues a token for an enabled application and stores its hash. It returns the
// token, to be shown once, and the stored row. Expiry is required, in the future and at
// most maxTTL from now.
func Create(ctx context.Context, s Creator, req Request, maxTTL time.Duration, now time.Time) (string, job.Token, error) {
	switch {
	case len(req.Scopes) == 0:
		return "", job.Token{}, errors.New("auth: at least one scope is required")
	case !req.ExpiresAt.After(now):
		return "", job.Token{}, errors.New("auth: the expiry must be in the future")
	case req.ExpiresAt.Sub(now) > maxTTL:
		return "", job.Token{}, fmt.Errorf("auth: the expiry is more than auth.max_token_ttl (%s) away", maxTTL)
	case len(req.Label) > maxLabelChars:
		return "", job.Token{}, fmt.Errorf("auth: the label is longer than %d characters", maxLabelChars)
	}
	for _, s := range req.Scopes {
		if _, err := ParseScopes(s); err != nil {
			return "", job.Token{}, err
		}
	}
	app, err := s.GetApplication(ctx, req.ApplicationID)
	if errors.Is(err, job.ErrNotFound) {
		return "", job.Token{}, fmt.Errorf("auth: application %q is not in the database; "+
			"add it to applications in the configuration and start vde serve once", req.ApplicationID)
	}
	if err != nil {
		return "", job.Token{}, err
	}
	if !app.Enabled {
		return "", job.Token{}, fmt.Errorf("auth: application %q is disabled (no longer in the configuration)", req.ApplicationID)
	}
	for range 3 { // an id collision (1 in 2^40 per existing token) gets a fresh token
		token, id, hash, err := Generate()
		if err != nil {
			return "", job.Token{}, err
		}
		t := job.Token{
			ID:            id,
			ApplicationID: app.ID,
			Hash:          hash,
			Scopes:        req.Scopes,
			Label:         req.Label,
			ExpiresAt:     req.ExpiresAt,
			CreatedAt:     now,
		}
		err = s.CreateToken(ctx, t)
		if errors.Is(err, job.ErrDuplicate) {
			continue
		}
		if err != nil {
			return "", job.Token{}, err
		}
		return token, t, nil
	}
	return "", job.Token{}, errors.New("auth: could not allocate a token id")
}
