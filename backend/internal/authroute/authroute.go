// Package authroute names the sign-in's routes, a leaf both config (which
// checks the redirect URL against the callback) and auth import, so config
// does not pull in the OIDC client.
package authroute

// The two /api routes a browser reaches before it has a session.
const (
	Login    = "/api/auth/login"
	Callback = "/api/auth/callback"
)
