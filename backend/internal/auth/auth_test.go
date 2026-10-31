package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	kitauth "github.com/trick77/ship/kit/auth"
)

var testKey = []byte("0123456789abcdef0123456789abcdef")

func sessions(t *testing.T) *kitauth.Sessions {
	t.Helper()
	s, err := NewSessions(testKey)
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
	if _, err := NewSessions([]byte("short")); err == nil {
		t.Error("a short key was accepted")
	}
}

func TestOIDCConfig(t *testing.T) {
	got := OIDCConfig(Config{Issuer: "https://auth.example.com", ClientID: "almanach", ClientSecret: "s",
		RedirectURL: "https://almanach.example.com/api/auth/callback"})
	if got.Issuer != "https://auth.example.com" || got.ClientID != "almanach" || got.ClientSecret != "s" ||
		got.RedirectURL != "https://almanach.example.com/api/auth/callback" || !got.Secure || got.Group != "" {
		t.Errorf("config = %+v", got)
	}
	if !slices.Equal(got.Scopes, []string{"openid", "profile", "email"}) || got.CookiePrefix != "almanach_" {
		t.Errorf("scopes %v, prefix %q", got.Scopes, got.CookiePrefix)
	}
}

// The reader is kit's Username: preferred username, else email, else
// subject.
func TestReaderFrom(t *testing.T) {
	if got := ReaderFrom(t.Context()); got != "" {
		t.Errorf("no claims = %q", got)
	}
	for want, c := range map[string]Claims{
		"jan":             {Subject: "sub-1", Email: "jan@example.com", PreferredUsername: "jan"},
		"jan@example.com": {Subject: "sub-1", Email: "jan@example.com"},
		"sub-1":           {Subject: "sub-1"},
	} {
		if got := ReaderFrom(kitauth.NewContext(t.Context(), c)); got != want {
			t.Errorf("%+v = %q, want %q", c, got, want)
		}
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

const redirect = "https://almanach.example.com/api/auth/callback"

func TestGate(t *testing.T) {
	s := sessions(t)
	h := Gate(s, redirect)(echo)
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
	if rec := serve(Gate(nil, "")(echo), "/api/projects", nil); rec.Code != http.StatusOK || rec.Body.String() != "" {
		t.Errorf("none mode = %d %q, want through, anonymous", rec.Code, rec.Body.String())
	}
}

// A change must come from this site, by Sec-Fetch-Site or, failing that,
// Origin; a read and everything outside /api are not asked.
func TestGateRefusesCrossSiteChanges(t *testing.T) {
	s := sessions(t)
	h := Gate(s, redirect)(echo)
	ck := sessionCookie(t, s, Claims{Subject: "sub-1"})
	for _, tc := range []struct {
		name, method, path string
		hdr                map[string]string
		want               int
	}{
		{"same-origin fetch", "POST", "/api/sources", map[string]string{"Sec-Fetch-Site": "same-origin"}, 200},
		{"typed by hand", "DELETE", "/api/sources/x", map[string]string{"Sec-Fetch-Site": "none"}, 200},
		{"sibling subdomain", "POST", "/api/auth/logout", map[string]string{"Sec-Fetch-Site": "same-site", "Origin": "https://evil.example.com"}, 403},
		{"other site", "PATCH", "/api/sources/x", map[string]string{"Sec-Fetch-Site": "cross-site"}, 403},
		{"Origin is the host", "PUT", "/api/sources/x", map[string]string{"Origin": "http://almanach.internal:8080"}, 200},
		{"Origin is the redirect's", "POST", "/api/sources", map[string]string{"Origin": "https://almanach.example.com"}, 200},
		{"Origin elsewhere", "POST", "/api/sources", map[string]string{"Origin": "https://evil.example.com"}, 403},
		{"Origin null", "POST", "/api/sources", map[string]string{"Origin": "null"}, 403},
		{"Origin without host", "POST", "/api/sources", map[string]string{"Origin": "almanach"}, 403},
		{"neither header", "POST", "/api/sources", nil, 403},
		{"a read", "GET", "/api/projects", map[string]string{"Sec-Fetch-Site": "cross-site"}, 200},
		{"outside /api", "POST", "/webhooks/noergler", map[string]string{"Sec-Fetch-Site": "cross-site"}, 200},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		req.Host = "almanach.internal:8080"
		req.AddCookie(ck)
		for k, v := range tc.hdr {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("%s: %d %s, want %d", tc.name, rec.Code, rec.Body.String(), tc.want)
		}
		if tc.want == 403 && !strings.Contains(rec.Body.String(), `"error"`) {
			t.Errorf("%s: body %q, want JSON", tc.name, rec.Body.String())
		}
	}
}
