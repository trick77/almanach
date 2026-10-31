package auth

import (
	"context"

	kitauth "github.com/trick77/ship/kit/auth"
)

// Claims is the verified identity an OIDC login yields: kit's flow checked
// state, PKCE, the ID token and its nonce.
type Claims = kitauth.Claims

// Reader is the name the log carries for c: the email, else the subject.
func Reader(c Claims) string {
	if c.Email != "" {
		return c.Email
	}
	return c.Subject
}

// ReaderFrom is the signed-in reader of a request the gate let through, ""
// when there is none (none mode, or a route outside /api).
func ReaderFrom(ctx context.Context) string {
	c, ok := kitauth.FromContext(ctx)
	if !ok {
		return ""
	}
	return Reader(c)
}
