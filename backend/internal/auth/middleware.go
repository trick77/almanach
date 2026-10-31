package auth

import (
	"context"
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
//
// One door more in oidc mode: a GET or HEAD of the Markdown export
// (ExportPath) passes on a team bearer, which bearer checks (the webhook's
// lookup, constant time, any team), so an agent outside a browser can read
// a page. It opens nothing else: every other route, the JSON of the same
// page included, still needs the session, and a bearer that is no team's
// falls through to it. A request let in so carries ExportOnly, and the
// handler serves it the export even where a slug ending in .md names a
// repository of its own.
func Gate(sessions *kitauth.Sessions, redirectURL string, bearer func(*http.Request) bool) func(http.Handler) http.Handler {
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
			if sessions != nil && bearer != nil && (r.Method == http.MethodGet || r.Method == http.MethodHead) &&
				ExportPath(r.URL.EscapedPath()) && bearer(r) {
				next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), exportOnlyKey{}, true)))
				return
			}
			guarded.ServeHTTP(w, r)
		})
	}
}

type exportOnlyKey struct{}

// ExportOnly reports whether the gate let the request in on a team bearer,
// for the Markdown export alone.
func ExportOnly(ctx context.Context) bool {
	v, _ := ctx.Value(exportOnlyKey{}).(bool)
	return v
}

// gated reports whether p needs a session: /api and below, but the login's
// two routes.
func gated(p string) bool {
	if p != "/api" && !strings.HasPrefix(p, "/api/") {
		return false
	}
	return p != LoginPath && p != CallbackPath
}

// ExportPath reports whether p, as escaped on the wire, is one of the two
// Markdown export routes: /api/projects/<key>.md and
// /api/projects/<key>/repos/<slug>.md, each a single non-empty segment. A
// key and a slug are plain path segments: one with an escape (a slash or a
// dot spelled %2F, %2E) never matches, so a path the mux reads otherwise is
// never opened by a bearer.
func ExportPath(p string) bool {
	rest, ok := strings.CutPrefix(p, "/api/projects/")
	if !ok {
		return false
	}
	segs := strings.Split(rest, "/")
	switch len(segs) {
	case 1:
		return markdownSegment(segs[0])
	case 3:
		return segs[0] != "" && !strings.Contains(segs[0], "%") && segs[1] == "repos" && markdownSegment(segs[2])
	}
	return false
}

func markdownSegment(s string) bool {
	return len(s) > len(".md") && strings.HasSuffix(s, ".md") && !strings.Contains(s, "%")
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
