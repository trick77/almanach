package auth

import (
	"net/http"
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
// is guarded without anyone remembering to: no cookie is a 401 JSON, which
// the UI turns into a full navigation to the login. The login and its
// callback pass, and so does everything outside /api: the webhook and the
// ping (bearer), the probes, and the SPA shell, which holds no data. The
// claims go on the request context (ReaderFrom). nil sessions (none mode)
// gate nothing.
func Gate(sessions *kitauth.Sessions) func(http.Handler) http.Handler {
	if sessions == nil {
		return func(next http.Handler) http.Handler { return next }
	}
	require := sessions.Require("", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		httpx.WriteError(w, http.StatusUnauthorized, "sign-in required")
	}))
	return func(next http.Handler) http.Handler {
		guarded := require(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if gated(r.URL.Path) {
				guarded.ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(w, r)
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
