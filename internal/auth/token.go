package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Token format: vde_<id>_<secret>. The id is 8 characters of lowercase base32, stored in
// clear for lookup and display; the secret is 32 random bytes in unpadded base64url
// (43 characters). The secret may contain "_", so Parse splits by position.
const (
	Prefix      = "vde_"
	IDLength    = 8
	secretBytes = 32
	secretChars = 43 // base64.RawURLEncoding.EncodedLen(secretBytes)
	tokenChars  = len(Prefix) + IDLength + 1 + secretChars
)

// Scopes. jobs covers submit, poll, delete and quota; jobs:read only poll and quota.
const (
	ScopeJobs     = "jobs"
	ScopeJobsRead = "jobs:read"
)

var knownScopes = []string{ScopeJobs, ScopeJobsRead}

// ErrMalformed is returned by Parse for anything that is not a well-formed token.
var ErrMalformed = errors.New("auth: malformed token")

// Generate returns a new token, its id and the SHA-256 hash of its secret. The token is
// shown to the operator once; only id and hash are stored.
func Generate() (token, id string, hash []byte, err error) {
	id = strings.ToLower(rand.Text()[:IDLength]) // base32: A-Z and 2-7, 5 bits each
	b := make([]byte, secretBytes)
	if _, err := rand.Read(b); err != nil {
		return "", "", nil, fmt.Errorf("auth: generate token: %w", err)
	}
	secret := base64.RawURLEncoding.EncodeToString(b)
	return Prefix + id + "_" + secret, id, Hash(secret), nil
}

// Parse splits a token into its id and secret, or returns ErrMalformed.
func Parse(token string) (id, secret string, err error) {
	if len(token) != tokenChars || !strings.HasPrefix(token, Prefix) {
		return "", "", ErrMalformed
	}
	id = token[len(Prefix) : len(Prefix)+IDLength]
	if !validID(id) || token[len(Prefix)+IDLength] != '_' {
		return "", "", ErrMalformed
	}
	secret = token[len(Prefix)+IDLength+1:]
	if b, err := base64.RawURLEncoding.Strict().DecodeString(secret); err != nil || len(b) != secretBytes {
		return "", "", ErrMalformed
	}
	return id, secret, nil
}

func validID(id string) bool {
	for i := range len(id) {
		if c := id[i]; (c < 'a' || c > 'z') && (c < '2' || c > '7') {
			return false
		}
	}
	return len(id) == IDLength
}

// Hash returns the SHA-256 of a token's secret part. A 256-bit random secret needs no
// slow password hash.
func Hash(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

// Verify reports whether secret hashes to hash, comparing in constant time.
func Verify(secret string, hash []byte) bool {
	return subtle.ConstantTimeCompare(Hash(secret), hash) == 1
}

// ParseScopes parses a comma-separated scope list, refusing unknown and empty ones. The
// result is sorted and without duplicates.
func ParseScopes(list string) ([]string, error) {
	var scopes []string
	for s := range strings.SplitSeq(list, ",") {
		s = strings.TrimSpace(s)
		if !slices.Contains(knownScopes, s) {
			return nil, fmt.Errorf("auth: unknown scope %q; known: %s", s, strings.Join(knownScopes, ", "))
		}
		scopes = append(scopes, s)
	}
	slices.Sort(scopes)
	return slices.Compact(scopes), nil
}

// Permits reports whether a token holding scopes may make a request that needs need.
// jobs implies jobs:read; need "" is satisfied by any scope.
func Permits(scopes []string, need string) bool {
	switch need {
	case "":
		return len(scopes) > 0
	case ScopeJobsRead:
		return slices.Contains(scopes, ScopeJobsRead) || slices.Contains(scopes, ScopeJobs)
	default:
		return slices.Contains(scopes, need)
	}
}
