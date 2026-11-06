package api

import (
	"net/http"
	"strings"

	"github.com/trick77/almanach/internal/auth"
	"github.com/trick77/almanach/internal/httpapi"
)

// logClass puts a request's access line at its level: a reader's request
// at Info (who read what, at the default level), a bearer route's refused
// token at Warn, the reads the UI polls and the static files at Debug.
func logClass(r *http.Request) httpapi.LogClass {
	p := r.URL.Path
	switch {
	case strings.HasPrefix(p, "/webhooks/") || p == "/auth/ping":
		return httpapi.LogBearer
	case p != "/api" && !strings.HasPrefix(p, "/api/"):
		// The SPA, its files, /metrics (scraped every few seconds; a
		// refused bearer there logs its own Warn).
		return httpapi.LogRoutine
	case r.Header.Get("Authorization") != "" && auth.ExportPath(r.URL.EscapedPath()):
		return httpapi.LogBearer
	case polled(r):
		return httpapi.LogRoutine
	}
	return httpapi.LogRead
}

// polled is a read the UI repeats on a timer: the nav's queue count from
// every page (15 s), the queue page and a source's page (3 s).
func polled(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	switch p := r.URL.Path; p {
	case "/api/queue", "/api/queue/count":
		return true
	default:
		key, ok := strings.CutPrefix(p, "/api/sources/")
		return ok && key != "" && !strings.Contains(key, "/")
	}
}
