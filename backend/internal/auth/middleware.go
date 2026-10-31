package auth

import (
	"net/http"
	"net/url"
	"strings"

	kitauth "github.com/trick77/ship/kit/auth"
	"github.com/trick77/ship/kit/httpx"

	"github.com/trick77/almanach/internal/authroute"
)

// The two /api routes a browser reaches before it has a session.
const (
	LoginPath    = authroute.Login
	CallbackPath = authroute.Callback
)

// Gate stands in front of everything under /api.
//
// A change (POST, PUT, PATCH, DELETE) must come from this site, in every
// mode: Go's CrossOriginProtection decides, the check kit's
// httpx.CrossOrigin wraps, built here directly to trust the redirect URL's
// origin beside the request's own host. In none mode that is all, and a
// client sending neither Sec-Fetch-Site nor Origin (curl) still writes.
//
// With sessions (oidc mode) two things more. Every route needs a session,
// so one added later is guarded without anyone remembering to; no valid
// cookie is a 401 JSON, which the UI answers with a full navigation to the
// login. And a change carrying neither header is refused too, where Go lets
// it through as a non-browser client: the session cookie is SameSite Lax,
// which still lets a sibling subdomain post, and every browser that holds
// it sends one. A refusal is a 403 JSON.
//
// The login and its callback pass, and so does everything outside /api:
// the webhook and the ping (bearer), the probes, and the SPA shell, which
// holds no data. The claims go on the request context (ReaderFrom).
func Gate(sessions *kitauth.Sessions, redirectURL string) func(http.Handler) http.Handler {
	cop := http.NewCrossOriginProtection()
	if o := origin(redirectURL); o != "" {
		if err := cop.AddTrustedOrigin(o); err != nil {
			panic("auth: redirect URL origin " + o + ": " + err.Error()) // config checked the URL
		}
	}
	refused := func(r *http.Request) bool {
		if !unsafe(r.Method) {
			return false
		}
		if cop.Check(r) != nil {
			return true
		}
		return sessions != nil && r.Header.Get("Sec-Fetch-Site") == "" && r.Header.Get("Origin") == ""
	}
	return func(next http.Handler) http.Handler {
		guarded := next
		if sessions != nil {
			guarded = sessions.Require("", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				httpx.WriteError(w, http.StatusUnauthorized, "sign-in required")
			}))(next)
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !gated(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			if refused(r) {
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

// origin is scheme://host of s as a browser writes it in Origin: lower
// case, the scheme's default port dropped (:443 for https, :80 for http);
// "" when s has no scheme or host.
func origin(s string) string {
	u, err := url.Parse(s)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	scheme, host := strings.ToLower(u.Scheme), strings.ToLower(u.Host)
	if p := u.Port(); (scheme == "https" && p == "443") || (scheme == "http" && p == "80") {
		host = strings.ToLower(u.Hostname())
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
	}
	return scheme + "://" + host
}
