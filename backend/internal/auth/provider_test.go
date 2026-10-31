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

	if err := p.Discover(t.Context()); err == nil || p.Ready() {
		t.Fatalf("Discover against a provider that is down = %v, ready %v", err, p.Ready())
	}
	rec := httptest.NewRecorder()
	p.Start(rec, httptest.NewRequest(http.MethodGet, LoginPath, nil))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"error"`) {
		t.Errorf("Start before discovery = %d %q, want 503 JSON", rec.Code, rec.Body.String())
	}
	if _, err := p.Callback(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, CallbackPath, nil)); !errors.Is(err, ErrNotDiscovered) {
		t.Errorf("Callback before discovery = %v", err)
	}

	done := make(chan struct{})
	go func() { p.Run(t.Context()); close(done) }()
	// A few failed rounds, logged, then the provider comes up.
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
	if !p.Ready() || !strings.Contains(logs.String(), "OIDC: OK") || !strings.Contains(logs.String(), "provider discovery failed") {
		t.Fatalf("ready %v, log:\n%s", p.Ready(), logs.String())
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
	if p.Ready() {
		t.Error("ready without discovery")
	}
}
