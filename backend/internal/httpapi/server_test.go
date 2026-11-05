package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A handler's panic answers 500 with the security headers all the same:
// they sit outside the recovery.
func TestPanicKeepsSecurityHeaders(t *testing.T) {
	s := New(func(context.Context) error { return nil }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.HandleFunc("GET /boom", func(http.ResponseWriter, *http.Request) { panic("boom") })
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/boom", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("code = %d, want 500", rec.Code)
	}
	if rec.Header().Get("X-Frame-Options") != "DENY" || rec.Header().Get("Content-Security-Policy") != framePolicy {
		t.Errorf("headers = %v, want the frame headers on the panic's 500", rec.Header())
	}
	if got := s.Patterns(); len(got) != 1 || got[0] != "GET /boom" {
		t.Errorf("Patterns = %v", got)
	}
}
