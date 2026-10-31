package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	kitauth "github.com/trick77/ship/kit/auth"
)

var testKey = []byte("0123456789abcdef0123456789abcdef")

func sessions(t *testing.T) *kitauth.Sessions {
	t.Helper()
	s, err := NewSessions(testKey, true)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func sessionCookie(t *testing.T, s *kitauth.Sessions, c Claims) *http.Cookie {
	t.Helper()
	rec := httptest.NewRecorder()
	if err := s.Issue(rec, c); err != nil {
		t.Fatal(err)
	}
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == SessionCookie {
			return ck
		}
	}
	t.Fatalf("no %s cookie in %v", SessionCookie, rec.Result().Cookies())
	return nil
}

func TestNewSessions(t *testing.T) {
	c := sessionCookie(t, sessions(t), Claims{Subject: "sub-1"})
	if !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode {
		t.Errorf("cookie = %+v, want Secure, HttpOnly, Lax", c)
	}
	if d := time.Until(c.Expires); d < SessionTTL-time.Minute || d > SessionTTL {
		t.Errorf("expires in %s, want %s", d, SessionTTL)
	}
	if _, err := NewSessions([]byte("short"), true); err == nil {
		t.Error("a short key was accepted")
	}
}

func TestOIDCConfig(t *testing.T) {
	got := OIDCConfig(Config{Issuer: "https://auth.example.com", ClientID: "almanach", ClientSecret: "s",
		RedirectURL: "https://almanach.example.com/api/auth/callback", Secure: true})
	if got.Issuer != "https://auth.example.com" || got.ClientID != "almanach" || got.ClientSecret != "s" ||
		got.RedirectURL != "https://almanach.example.com/api/auth/callback" || !got.Secure || got.Group != "" {
		t.Errorf("config = %+v", got)
	}
	if !slices.Equal(got.Scopes, []string{"openid", "profile", "email"}) || got.CookiePrefix != "almanach_" {
		t.Errorf("scopes %v, prefix %q", got.Scopes, got.CookiePrefix)
	}
}

func TestReader(t *testing.T) {
	if got := Reader(Claims{Subject: "sub-1", Email: "jan@example.com"}); got != "jan@example.com" {
		t.Errorf("with email = %q", got)
	}
	if got := Reader(Claims{Subject: "sub-1"}); got != "sub-1" {
		t.Errorf("without email = %q", got)
	}
	if got := ReaderFrom(t.Context()); got != "" {
		t.Errorf("no claims = %q", got)
	}
	if got := ReaderFrom(kitauth.NewContext(t.Context(), Claims{Subject: "sub-1"})); got != "sub-1" {
		t.Errorf("claims = %q", got)
	}
}

// echo answers 200 with the reader the gate put on the context.
var echo = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	_, _ = w.Write([]byte(ReaderFrom(r.Context())))
})

func serve(h http.Handler, path string, ck *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if ck != nil {
		req.AddCookie(ck)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestGate(t *testing.T) {
	s := sessions(t)
	h := Gate(s)(echo)
	for _, p := range []string{"/api", "/api/projects", "/api/sources/x/sync", "/api/me", "/api/auth/logout"} {
		rec := serve(h, p, nil)
		var body struct{ Error string }
		if rec.Code != http.StatusUnauthorized || json.Unmarshal(rec.Body.Bytes(), &body) != nil || body.Error == "" {
			t.Errorf("%s without a cookie = %d %q, want 401 JSON", p, rec.Code, rec.Body.String())
		}
	}
	// A forged cookie is no cookie.
	if rec := serve(h, "/api/projects", &http.Cookie{Name: SessionCookie, Value: "e30.forged"}); rec.Code != http.StatusUnauthorized {
		t.Errorf("forged cookie = %d", rec.Code)
	}
	ck := sessionCookie(t, s, Claims{Subject: "sub-1", Email: "jan@example.com"})
	if rec := serve(h, "/api/projects", ck); rec.Code != http.StatusOK || rec.Body.String() != "jan@example.com" {
		t.Errorf("with a cookie = %d %q, want 200 and the reader", rec.Code, rec.Body.String())
	}
	for _, p := range []string{"/api/auth/login", "/api/auth/callback", "/webhooks/noergler", "/auth/ping", "/healthz", "/readyz", "/", "/projekte/PAY", "/apix"} {
		if rec := serve(h, p, nil); rec.Code != http.StatusOK {
			t.Errorf("%s without a cookie = %d, want through", p, rec.Code)
		}
	}
}

func TestGateOffInNoneMode(t *testing.T) {
	if rec := serve(Gate(nil)(echo), "/api/projects", nil); rec.Code != http.StatusOK || rec.Body.String() != "" {
		t.Errorf("none mode = %d %q, want through, anonymous", rec.Code, rec.Body.String())
	}
}
