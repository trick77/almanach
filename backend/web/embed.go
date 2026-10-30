// Package web serves the built wiki SPA out of the binary.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"regexp"

	"github.com/trick77/ship/kit/spa"
)

// The SPA is built into dist/ by `npm run build` (ui/vite.config.ts points
// its outDir here). A fresh clone has no build, so dist/ holds a tracked
// .gitkeep: `//go:embed all:dist` fails at COMPILE time on a missing or
// empty directory, which would break `go build` for anyone who has not run
// the frontend build.
//
//go:embed all:dist
var distFS embed.FS

// placeholderHTML ships separately from dist/: vite runs with emptyOutDir
// off so the tracked .gitkeep survives a build, and a placeholder inside
// dist/ would be overwritten by the first build.
//
//go:embed placeholder.html
var placeholderHTML []byte

// routes mirrors ui/src/routing.ts: an explicit allowlist, so an unknown
// path is a real 404 rather than 200 plus the shell. A project key and a
// repository slug are what Bitbucket allows: letters, digits, dot, dash,
// underscore, and the tilde of a personal project (~user); a PR id is a
// positive integer.
var routes = []*regexp.Regexp{
	regexp.MustCompile(`^/$`),
	regexp.MustCompile(`^/faq$`),
	regexp.MustCompile(`^/projekte/[A-Za-z0-9._~-]+$`),
	regexp.MustCompile(`^/projekte/[A-Za-z0-9._~-]+/repos/[A-Za-z0-9._-]+$`),
	regexp.MustCompile(`^/projekte/[A-Za-z0-9._~-]+/repos/[A-Za-z0-9._-]+/notizen/[1-9][0-9]{0,8}$`),
	regexp.MustCompile(`^/quellen$`),
	regexp.MustCompile(`^/quellen/[A-Za-z0-9._~-]+$`),
}

func isRoute(p string) bool {
	for _, re := range routes {
		if re.MatchString(p) {
			return true
		}
	}
	return false
}

func dist() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic(err) // unreachable: the embed pattern guarantees the directory
	}
	return sub
}

// Handler serves the SPA. It is registered on "GET /" as the catch-all;
// Go's ServeMux prefers the most specific pattern, so every explicit route
// (/api/..., /auth/ping, /webhooks/noergler, /healthz, /readyz) still wins
// over it.
func Handler() http.Handler { return handler(dist()) }

// handler is kit's SPA server with the route allowlist: /api and a missing
// /assets/ file stay 404s, the shell is revalidated on every load, and an
// unbuilt binary answers its routes with the placeholder and a 503.
func handler(sub fs.FS) http.Handler {
	return spa.Handler(sub, spa.Options{Routes: isRoute, Placeholder: placeholderHTML})
}
