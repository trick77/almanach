package auth

import (
	"context"

	kitauth "github.com/trick77/ship/kit/auth"
)

// Claims is the verified identity an OIDC login yields: kit's flow checked
// state, PKCE, the ID token and its nonce.
type Claims = kitauth.Claims

// ReaderFrom is the signed-in reader of a request the gate let through, as
// kit names him (preferred username, else email, else subject); "" when
// there is none (none mode, or a route outside /api).
func ReaderFrom(ctx context.Context) string {
	c, ok := kitauth.FromContext(ctx)
	if !ok {
		return ""
	}
	return c.Username()
}
