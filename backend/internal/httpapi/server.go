// Package httpapi is the HTTP surface's mux: probes and middleware from kit's
// httpx, routes added by the packages that own them. net/http only.
package httpapi

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"syscall"

	"github.com/trick77/ship/kit/httpx"
)

// Server holds what the handlers share.
type Server struct {
	log *slog.Logger
	mux *http.ServeMux
	// inner wraps the mux inside the access log: the sign-in gate, whose
	// 401 must reach the log line and whose reader the handlers name there.
	inner    []func(http.Handler) http.Handler
	patterns []string
	// classify says at which level a request's access line goes; nil puts
	// every request in LogRoutine.
	classify func(*http.Request) LogClass
}

// LogClass is which access-line levels a request's line follows.
type LogClass int

const (
	// LogRoutine is kit's default: a 2xx, a 401 and a 404 at Debug. Static
	// files, and the reads the UI polls (every 3 s, every 15 s from every
	// page), which at Info would bury the rest.
	LogRoutine LogClass = iota
	// LogRead is a reader's request: Info, so who read what is in the log
	// at the default level.
	LogRead
	// LogBearer is a bearer route (the webhook, the ping, a Markdown
	// export sent a bearer): a 401 at Warn, a wrong or guessed token is
	// what an operator looks for; the rest at Info.
	LogBearer
)

// Classify sets the rule that puts each request in a LogClass. Before
// Handler.
func (s *Server) Classify(f func(*http.Request) LogClass) { s.classify = f }

// New builds the mux with the probes registered: /healthz is liveness,
// /readyz asks the database, then more, in order.
func New(ready func(context.Context) error, log *slog.Logger, more ...httpx.Check) *Server {
	s := &Server{log: log, mux: http.NewServeMux()}
	httpx.Health(s.mux, log, append([]httpx.Check{{Name: "database", Run: ready}}, more...)...)
	return s
}

// MinFreeBytes is the room the data folder must have for /readyz to say
// ready: SQLite answers a ping from a full disk, and the worker's writes
// then fail one after another, each after a paid model call.
const MinFreeBytes = 64 << 20

// DiskFree is a readiness check that the filesystem holding dir has at
// least minFree bytes free.
func DiskFree(dir string, minFree uint64) func(context.Context) error {
	return func(context.Context) error {
		var st syscall.Statfs_t
		if err := syscall.Statfs(dir, &st); err != nil {
			return fmt.Errorf("statfs %s: %w", dir, err)
		}
		free := freeBytes(&st)
		if free < minFree {
			return fmt.Errorf("%s: %d bytes free, under %d", dir, free, minFree)
		}
		return nil
	}
}

// Handle registers a pattern on the mux.
func (s *Server) Handle(pattern string, h http.Handler) {
	s.patterns = append(s.patterns, pattern)
	s.mux.Handle(pattern, h)
}

// HandleFunc registers a handler function on the mux.
func (s *Server) HandleFunc(pattern string, h http.HandlerFunc) {
	s.patterns = append(s.patterns, pattern)
	s.mux.HandleFunc(pattern, h)
}

// Patterns are the patterns registered through Handle and HandleFunc, in
// order, the probes not among them: what the API test holds openapi.yaml
// against.
func (s *Server) Patterns() []string { return slices.Clone(s.patterns) }

// Use adds a middleware between the chain below and the mux, outermost
// first.
func (s *Server) Use(mw func(http.Handler) http.Handler) { s.inner = append(s.inner, mw) }

// framePolicy is the Content-Security-Policy every response carries: no
// framing, so no other site lays the Quellen page's buttons (a removal, a
// paid "Neu beschreiben") under a decoy. Framing alone, not kit's
// DefaultCSP: what the bundle loads (ELK, the fonts) was never held
// against a script or a worker policy. kit's SecurityHeaders adds
// X-Frame-Options DENY, X-Content-Type-Options nosniff and
// Referrer-Policy same-origin (a link to the forge sends no Referer).
const framePolicy = "frame-ancestors 'none'"

// Handler is the mux wrapped in the middleware chain: access log outermost,
// so a panic's log line and its 500 carry the request id; the security
// headers outside the recovery, so a panic's 500 keeps them (kit's Recover
// restores the headers it saw arrive), and outside the gate, so its 401
// carries them; then what Use added. kit's level rule sees the status
// only, so each LogClass has its own access log around the same chain,
// and the request picks one: one line, one request id.
func (s *Server) Handler() http.Handler {
	h := httpx.Chain(s.mux, append([]func(http.Handler) http.Handler{httpx.SecurityHeaders(framePolicy), httpx.Recover(s.log)}, s.inner...)...)
	routine := httpx.AccessLog(s.log, httpx.AccessLogOptions{})(h)
	if s.classify == nil {
		return routine
	}
	logs := map[LogClass]http.Handler{
		LogRoutine: routine,
		LogRead:    httpx.AccessLog(s.log, httpx.AccessLogOptions{RoutineLevel: slog.LevelInfo})(h),
		LogBearer:  httpx.AccessLog(s.log, httpx.AccessLogOptions{Level: bearerLevel})(h),
	}
	classify := s.classify
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next, ok := logs[classify(r)]
		if !ok {
			next = routine
		}
		next.ServeHTTP(w, r)
	})
}

// bearerLevel is a bearer route's access line: a refused token at Warn,
// the rest as a reader's.
func bearerLevel(status int) slog.Level {
	if status == http.StatusUnauthorized {
		return slog.LevelWarn
	}
	return httpx.StatusLevel(status, slog.LevelInfo)
}
