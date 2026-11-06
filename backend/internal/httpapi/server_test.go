package httpapi

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
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

// Each LogClass's line goes at its level: a reader's request at Info, a
// bearer route's 401 at Warn, a routine line at Debug; with no rule, every
// request is routine, as before.
func TestAccessLogClasses(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	s := New(func(context.Context) error { return nil }, log)
	s.HandleFunc("GET /read", func(http.ResponseWriter, *http.Request) {})
	s.HandleFunc("GET /poll", func(http.ResponseWriter, *http.Request) {})
	s.HandleFunc("GET /hook", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) })
	s.HandleFunc("GET /hook-ok", func(http.ResponseWriter, *http.Request) {})
	h := s.Handler()
	line := func(path string) string {
		t.Helper()
		buf.Reset()
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
		return buf.String()
	}
	if got := line("/read"); !strings.Contains(got, "level=DEBUG") {
		t.Errorf("no rule: %q, want every line routine", got)
	}

	s.Classify(func(r *http.Request) LogClass {
		switch r.URL.Path {
		case "/read":
			return LogRead
		case "/hook", "/hook-ok":
			return LogBearer
		}
		return LogRoutine
	})
	h = s.Handler()
	for path, want := range map[string]string{
		"/read": "level=INFO", "/poll": "level=DEBUG", "/hook": "level=WARN", "/hook-ok": "level=INFO",
	} {
		got := line(path)
		if !strings.Contains(got, want) || strings.Count(got, "http_request") != 1 {
			t.Errorf("%s: %q, want one line at %s", path, got, want)
		}
	}
}
