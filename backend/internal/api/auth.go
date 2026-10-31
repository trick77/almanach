package api

import (
	"errors"
	"net/http"

	"github.com/trick77/almanach/internal/auth"
	"github.com/trick77/almanach/internal/config"
	"github.com/trick77/almanach/internal/httpapi"
	"github.com/trick77/ship/kit/httpx"
	"github.com/trick77/ship/kit/logging"
)

// errNoSubject is a verified token without a subject: no stable identity
// to put in a session.
var errNoSubject = errors.New("oidc claims carry no subject")

// OIDCService is the login half of the sign-in as the routes need it.
// auth.Provider satisfies it, and answers a provider not discovered yet
// itself; kit's *auth.OIDC does too, for a test.
type OIDCService interface {
	Start(w http.ResponseWriter, r *http.Request)
	Callback(w http.ResponseWriter, r *http.Request) (auth.Claims, error)
}

// registerAuth mounts the sign-in routes and, in oidc mode, the gate in
// front of /api. A mode and a store that disagree is a wiring bug: oidc
// without sessions would serve every route open.
func (d Deps) registerAuth(s *httpapi.Server) {
	if (d.AuthMode == config.AuthOIDC) != (d.Sessions != nil && d.OIDC != nil) {
		panic("api: oidc mode needs Sessions and OIDC, and only oidc mode has them")
	}
	s.Use(auth.Gate(d.Sessions, d.RedirectURL))
	s.HandleFunc("GET /api/me", d.me)
	s.HandleFunc("GET "+auth.LoginPath, d.login)
	s.HandleFunc("GET "+auth.CallbackPath, d.callback)
	s.HandleFunc("POST /api/auth/logout", d.logout)
}

type meBody struct {
	Mode string `json:"mode"`
	// User is the reader's name (preferred username, else email, else
	// subject), "" in none mode.
	User string `json:"user"`
}

// me is who reads, in every mode: the UI's session check. In oidc mode the
// gate has answered 401 already when there is no session.
func (d Deps) me(w http.ResponseWriter, r *http.Request) {
	d.reader(r)
	httpx.WriteJSON(w, http.StatusOK, meBody{Mode: string(d.AuthMode), User: d.user(r)})
}

// signInOff answers the sign-in routes in none mode: there is nobody to
// sign in. false means go on.
func (d Deps) signInOff(w http.ResponseWriter) bool {
	if d.OIDC != nil {
		return false
	}
	httpx.WriteError(w, http.StatusServiceUnavailable, "no sign-in in auth mode none")
	return true
}

// login hands the browser to the provider, state, nonce and PKCE in
// transient cookies, and remembers ?next=, the page to come back to.
func (d Deps) login(w http.ResponseWriter, r *http.Request) {
	if d.signInOff(w) {
		return
	}
	auth.SetNext(w, r.URL.Query().Get("next"))
	d.OIDC.Start(w, r)
}

// callback finishes a sign-in: kit checked state, PKCE, the ID token and
// its nonce; the claims become the session cookie and the browser returns
// to the page it started from. A failure lands on a URL that says so, and
// the UI halts there with a button rather than redirecting into the same
// failure again. The reason is logged, never shown.
func (d Deps) callback(w http.ResponseWriter, r *http.Request) {
	if d.signInOff(w) {
		return
	}
	ctx := r.Context()
	next := auth.TakeNext(w, r)
	claims, err := d.OIDC.Callback(w, r)
	if errors.Is(err, auth.ErrNotDiscovered) {
		http.Redirect(w, r, auth.UnavailableURL, http.StatusFound)
		return
	}
	if err == nil && claims.Subject == "" {
		err = errNoSubject
	}
	if err != nil {
		d.Log.WarnContext(ctx, "oidc callback failed", logging.Err(err))
		http.Redirect(w, r, "/?auth_error=oidc_callback_failed", http.StatusFound)
		return
	}
	if err := d.Sessions.Issue(w, claims); err != nil {
		d.Log.ErrorContext(ctx, "issue session failed", logging.Err(err))
		http.Redirect(w, r, "/?auth_error=session_failed", http.StatusFound)
		return
	}
	d.Log.InfoContext(ctx, "signed in", "user", claims.Username())
	http.Redirect(w, r, next, http.StatusFound) //nolint:gosec // TakeNext returns a same-site path (SafeNext) or "/"
}

type logoutBody struct {
	RedirectURL string `json:"redirect_url"`
}

// logout clears the cookie. Nothing to revoke: the claims travel in it.
// The provider's own session lives on, so the UI lands on a page that says
// so instead of signing straight back in.
func (d Deps) logout(w http.ResponseWriter, r *http.Request) {
	// The provider is not asked: logout works while it is down.
	if d.Sessions == nil {
		d.signInOff(w)
		return
	}
	d.reader(r)
	d.Sessions.Clear(w)
	httpx.WriteJSON(w, http.StatusOK, logoutBody{RedirectURL: "/?signed_out=1"})
}
