// Package auth is the browser sign-in of oidc mode: kit's OIDC flow at the
// provider, kit's signed session cookie after it, and the gate in front of
// /api. No users table: the claims travel in the cookie, and the reader's
// name is for the log only, never a gate on what he reads or writes.
package auth

import (
	"time"

	kitauth "github.com/trick77/ship/kit/auth"
)

// SessionCookie carries kit's signed session.
const SessionCookie = "almanach_session"

// SessionTTL is how long a session stays valid after it is issued; nothing
// renews it. The provider runs its own, shorter window; this one bounds
// how long Almanach's cookie is worth anything if the provider is never
// asked again.
const SessionTTL = 30 * 24 * time.Hour

// NewSessions is kit's session store for Almanach: base64 JSON plus an
// HMAC in the cookie, no session table, nothing revokes a single session;
// rotating the key signs everyone out.
func NewSessions(key []byte, secure bool) (*kitauth.Sessions, error) {
	return kitauth.NewSessions(kitauth.SessionConfig{Name: SessionCookie, Key: key, TTL: SessionTTL, Secure: secure})
}

// Config is kit's OIDC flow as Almanach runs it.
type Config struct {
	Issuer, ClientID, ClientSecret, RedirectURL string
	Secure                                      bool
}

// OIDCConfig maps c onto kit's. No Group: who gets in at all is the
// provider's policy, and every signed-in reader may do everything.
func OIDCConfig(c Config) kitauth.OIDCConfig {
	return kitauth.OIDCConfig{
		Issuer:       c.Issuer,
		ClientID:     c.ClientID,
		ClientSecret: c.ClientSecret,
		RedirectURL:  c.RedirectURL,
		// Not kit's default: a provider client that does not list the
		// groups scope refuses the whole request, and nothing reads groups.
		Scopes: []string{"openid", "profile", "email"},
		// almanach_oidc_state and the rest: a sibling app on the parent
		// domain must not overwrite a half-finished login.
		CookiePrefix: "almanach_",
		Secure:       c.Secure,
	}
}
