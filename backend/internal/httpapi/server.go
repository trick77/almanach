// Package httpapi is the HTTP surface's mux: probes and middleware from kit's
// httpx, routes added by the packages that own them. net/http only.
package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"slices"

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
}

// New builds the mux with the probes registered: /healthz is liveness,
// /readyz asks the database.
func New(ready func(context.Context) error, log *slog.Logger) *Server {
	s := &Server{log: log, mux: http.NewServeMux()}
	httpx.Health(s.mux, log, httpx.Check{Name: "database", Run: ready})
	return s
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
// carries them; then what Use added.
func (s *Server) Handler() http.Handler {
	mws := append([]func(http.Handler) http.Handler{httpx.AccessLog(s.log, httpx.AccessLogOptions{}),
		httpx.SecurityHeaders(framePolicy), httpx.Recover(s.log)}, s.inner...)
	return httpx.Chain(s.mux, mws...)
}
