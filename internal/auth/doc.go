// Package auth issues and checks the access tokens of applications: generation, SHA-256
// hashing with constant-time comparison, expiry, revocation and scopes. Tokens are
// vde_<id>_<secret>; only the id and the hash of the secret are stored.
package auth
