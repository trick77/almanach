package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeIssuer serves a discovery document while up is true and 503 before.
func fakeIssuer(t *testing.T, up *atomic.Bool) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !up.Load() || r.URL.Path != "/.well-known/openid-configuration" {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                srv.URL,
			"authorization_endpoint":                srv.URL + "/authorize",
			"token_endpoint":                        srv.URL + "/token",
			"jwks_uri":                              srv.URL + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// syncBuffer is a log sink Run's goroutine and the test may share.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func newSyncBuffer() *syncBuffer { return &syncBuffer{} }

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestProviderDiscoversLater(t *testing.T) {
	var up atomic.Bool
	iss := fakeIssuer(t, &up)
	logs := newSyncBuffer()
	p := NewProvider(Config{Issuer: iss.URL, ClientID: "almanach", ClientSecret: "s", RedirectURL: redirect},
		slog.New(slog.NewTextHandler(logs, nil)))
	p.First, p.Max = time.Millisecond, 4*time.Millisecond

	if err := p.Discover(t.Context()); err == nil || errors.Is(err, ErrMisconfigured) || p.flow.Load() != nil {
		t.Fatalf("Discover against a provider that is down = %v, want a retryable error", err)
	}
	rec := httptest.NewRecorder()
	p.Start(rec, httptest.NewRequest(http.MethodGet, LoginPath, nil))
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != UnavailableURL {
		t.Errorf("Start before discovery = %d %q, want the halting page", rec.Code, rec.Header().Get("Location"))
	}
	if _, err := p.Callback(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, CallbackPath, nil)); !errors.Is(err, ErrNotDiscovered) {
		t.Errorf("Callback before discovery = %v", err)
	}

	done := make(chan struct{})
	go func() { p.Run(t.Context()); close(done) }()
	// The first attempt at once, a few failed rounds, logged, then the
	// provider comes up.
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(logs.String(), "attempt=3") && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	up.Store(true)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop after discovery succeeded")
	}
	if p.flow.Load() == nil || !strings.Contains(logs.String(), "OIDC: OK") || !strings.Contains(logs.String(), "provider discovery failed") {
		t.Fatalf("log:\n%s", logs.String())
	}
	rec = httptest.NewRecorder()
	p.Start(rec, httptest.NewRequest(http.MethodGet, LoginPath, nil))
	if rec.Code != http.StatusFound || !strings.HasPrefix(rec.Header().Get("Location"), iss.URL+"/authorize?") {
		t.Errorf("Start after discovery = %d %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestProviderRunStopsWithItsContext(t *testing.T) {
	var up atomic.Bool
	p := NewProvider(Config{Issuer: fakeIssuer(t, &up).URL}, slog.New(slog.NewTextHandler(newSyncBuffer(), nil)))
	p.First = time.Hour
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	p.Run(ctx)
	if p.flow.Load() != nil {
		t.Error("a flow without discovery")
	}
}

// The answers that are configuration, not reachability: an error once, no
// retry, the login and the callback say misconfigured.
func TestProviderMisconfiguredIsAFault(t *testing.T) {
	for name, serve := range map[string]func(w http.ResponseWriter){
		"issuer mismatch": func(w http.ResponseWriter) {
			_ = json.NewEncoder(w).Encode(map[string]any{"issuer": "https://other.example.com", "authorization_endpoint": "x",
				"token_endpoint": "x", "jwks_uri": "x"})
		},
		"404":               func(w http.ResponseWriter) { http.Error(w, "not found", http.StatusNotFound) },
		"403":               func(w http.ResponseWriter) { http.Error(w, "no", http.StatusForbidden) },
		"200, no discovery": func(w http.ResponseWriter) { _, _ = w.Write([]byte("<html>login</html>")) },
	} {
		var hits atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			hits.Add(1)
			serve(w)
		}))
		logs := newSyncBuffer()
		p := NewProvider(Config{Issuer: srv.URL}, slog.New(slog.NewTextHandler(logs, nil)))
		p.First = time.Millisecond
		done := make(chan struct{})
		go func() { p.Run(t.Context()); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: Run kept retrying", name)
		}
		srv.Close()
		if hits.Load() != 1 {
			t.Errorf("%s: %d attempts, want 1", name, hits.Load())
		}
		if l := logs.String(); strings.Count(l, "level=ERROR") != 1 || !strings.Contains(l, "disagrees with the configuration") || strings.Contains(l, "level=WARN") {
			t.Errorf("%s: log:\n%s", name, l)
		}
		rec := httptest.NewRecorder()
		p.Start(rec, httptest.NewRequest(http.MethodGet, LoginPath, nil))
		if rec.Header().Get("Location") != MisconfiguredURL {
			t.Errorf("%s: Start = %q", name, rec.Header().Get("Location"))
		}
		if _, err := p.Callback(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, CallbackPath, nil)); !errors.Is(err, ErrMisconfigured) {
			t.Errorf("%s: Callback = %v", name, err)
		}
	}
}

// Reachability keeps the warning and the retry: a 5xx, a refused
// connection.
func TestProviderUnreachableIsRetried(t *testing.T) {
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "busy", http.StatusBadGateway)
	}))
	defer down.Close()
	gone := httptest.NewServer(http.NotFoundHandler())
	gone.Close()
	for name, issuer := range map[string]string{"502": down.URL, "refused": gone.URL} {
		p := NewProvider(Config{Issuer: issuer}, slog.New(slog.NewTextHandler(newSyncBuffer(), nil)))
		err := p.Discover(t.Context())
		if err == nil || errors.Is(err, ErrMisconfigured) {
			t.Errorf("%s: err = %v, want retryable", name, err)
		}
		rec := httptest.NewRecorder()
		p.Start(rec, httptest.NewRequest(http.MethodGet, LoginPath, nil))
		if rec.Header().Get("Location") != UnavailableURL {
			t.Errorf("%s: Start = %q", name, rec.Header().Get("Location"))
		}
	}
}
