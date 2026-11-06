package api

import (
	"net/http"
	"strings"

	"github.com/trick77/almanach/internal/auth"
	"github.com/trick77/almanach/internal/httpapi"
	"github.com/trick77/ship/kit/httpx"
)

// pollHeader marks a request the UI repeats on a timer (useFetch's
// refetch, ui/src/api.ts pollHeader): the nav's queue count from every
// page, the queue page, a running source's page. The first load and a
// reload carry none, so a reader's own view stays at Info. It only ever
// lowers a line to Debug, so a caller sending it hides nothing but its
// own routine lines.
const pollHeader = "X-Almanach-Poll"

// logClass puts a request's access line at its level: a reader's request
// at Info (who read what, at the default level), a bearer route's refused
// token at Warn, the UI's polls and the static files at Debug.
func logClass(r *http.Request) httpapi.LogClass {
	p := r.URL.Path
	switch {
	case strings.HasPrefix(p, "/webhooks/") || p == "/auth/ping":
		return httpapi.LogBearer
	case p != "/api" && !strings.HasPrefix(p, "/api/"):
		// The SPA, its files, /metrics (scraped every few seconds; a
		// refused bearer there logs its own Warn).
		return httpapi.LogRoutine
	case exportBearer(r):
		return httpapi.LogBearer
	case r.Method == http.MethodGet && r.Header.Get(pollHeader) != "":
		return httpapi.LogRoutine
	}
	return httpapi.LogRead
}

// exportBearer is a Markdown export sent a bearer, as the gate reads one
// (kit's Bearer): an agent's request, not a browser's.
func exportBearer(r *http.Request) bool {
	_, ok := httpx.Bearer(r)
	return ok && auth.ExportPath(r.URL.EscapedPath())
}
