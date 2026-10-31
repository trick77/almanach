package auth

import (
	"net/http"
	"net/url"
	"strings"

	kitauth "github.com/trick77/ship/kit/auth"
	"github.com/trick77/ship/kit/httpx"
)

// The two /api routes a browser reaches before it has a session.
const (
	LoginPath    = "/api/auth/login"
	CallbackPath = "/api/auth/callback"
)

// Gate guards everything under /api with a session, so a route added later
// is guarded without anyone remembering to. No valid cookie is a 401 JSON;
// the UI answers any 401 from /api with a full navigation to the login.
//
// A change (POST, PUT, PATCH, DELETE) must also come from this site: the
// cookie is SameSite Lax, which still lets a sibling subdomain post. Go's
// CrossOriginProtection decides, the check kit's httpx.CrossOrigin wraps,
// built here directly to trust the redirect URL's origin beside the
// request's own host; plus one rule of ours: a change carrying neither Sec-Fetch-Site nor Origin is refused,
// where Go lets it through as a non-browser client; every browser that
// holds the cookie sends one. A refusal is a 403 JSON.
//
// The login and its callback pass, and so does everything outside /api:
// the webhook and the ping (bearer), the probes, and the SPA shell, which
// holds no data. The claims go on the request context (ReaderFrom). nil
// sessions (none mode) gate nothing.
func Gate(sessions *kitauth.Sessions, redirectURL string) func(http.Handler) http.Handler {
	if sessions == nil {
		return func(next http.Handler) http.Handler { return next }
	}
	cop := http.NewCrossOriginProtection()
	if o := origin(redirectURL); o != "" {
		if err := cop.AddTrustedOrigin(o); err != nil {
			panic("auth: redirect URL origin " + o + ": " + err.Error()) // config checked the URL
		}
	}
	require := sessions.Require("", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		httpx.WriteError(w, http.StatusUnauthorized, "sign-in required")
	}))
	return func(next http.Handler) http.Handler {
		guarded := require(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !gated(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			if unsafe(r.Method) && (cop.Check(r) != nil || (r.Header.Get("Sec-Fetch-Site") == "" && r.Header.Get("Origin") == "")) {
				httpx.WriteError(w, http.StatusForbidden, "cross-site request refused")
				return
			}
			guarded.ServeHTTP(w, r)
		})
	}
}

// gated reports whether p needs a session: /api and below, but the login's
// two routes.
func gated(p string) bool {
	if p != "/api" && !strings.HasPrefix(p, "/api/") {
		return false
	}
	return p != LoginPath && p != CallbackPath
}

func unsafe(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// origin is scheme://host of s, "" when s has none.
func origin(s string) string {
	u, err := url.Parse(s)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host)
}
