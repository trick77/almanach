// Package httpapi is the HTTP surface's mux: probes and middleware from kit's
// httpx, routes added by the packages that own them. net/http only.
package httpapi

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/trick77/ship/kit/httpx"
)

// Server holds what the handlers share.
type Server struct {
	log *slog.Logger
	mux *http.ServeMux
}

// New builds the mux with the probes registered: /healthz is liveness,
// /readyz asks the database.
func New(ready func(context.Context) error, log *slog.Logger) *Server {
	s := &Server{log: log, mux: http.NewServeMux()}
	httpx.Health(s.mux, log, httpx.Check{Name: "database", Run: ready})
	return s
}

// Handle registers a pattern on the mux.
func (s *Server) Handle(pattern string, h http.Handler) { s.mux.Handle(pattern, h) }

// HandleFunc registers a handler function on the mux.
func (s *Server) HandleFunc(pattern string, h http.HandlerFunc) { s.mux.HandleFunc(pattern, h) }

// Handler is the mux wrapped in the middleware chain: access log outermost,
// so a panic's log line and its 500 carry the request id.
func (s *Server) Handler() http.Handler {
	return httpx.Chain(s.mux, httpx.AccessLog(s.log, httpx.AccessLogOptions{}), httpx.Recover(s.log))
}
