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
	h := Gate(s, redirect, nil)(echo)
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
	if rec := serve(Gate(nil, "", nil)(echo), "/api/projects", nil); rec.Code != http.StatusOK || rec.Body.String() != "" {
		t.Errorf("none mode = %d %q, want through, anonymous", rec.Code, rec.Body.String())
	}
}

// In oidc mode a GET of the Markdown export passes on a known bearer;
// nothing else does, and a bearer the check refuses falls through to the
// session's 401.
func TestGateOpensTheExportToABearer(t *testing.T) {
	s := sessions(t)
	known := func(r *http.Request) bool { return r.Header.Get("Authorization") == "Bearer good" }
	h := Gate(s, redirect, known)(echo)
	call := func(method, p, token string) int {
		req := httptest.NewRequest(method, p, nil)
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	for _, p := range []string{"/api/projects/PAY.md", "/api/projects/PAY/repos/pay-api.md"} {
		for _, m := range []string{http.MethodGet, http.MethodHead} {
			if got := call(m, p, "good"); got != http.StatusOK {
				t.Errorf("%s %s with a team bearer = %d, want through", m, p, got)
			}
		}
		for _, token := range []string{"", "bad"} {
			if got := call(http.MethodGet, p, token); got != http.StatusUnauthorized {
				t.Errorf("GET %s with bearer %q = %d, want 401", p, token, got)
			}
		}
		if got := call(http.MethodPost, p, "good"); got != http.StatusUnauthorized {
			t.Errorf("POST %s with a team bearer = %d, want 401", p, got)
		}
	}
	for _, p := range []string{"/api/projects/PAY", "/api/projects", "/api/projects/PAY/repos/pay-api", "/api/projects/.md",
		"/api/projects/PAY/problems.md", "/api/projects/PAY/repos/x/notes/1.md", "/api/me", "/api/projects/A%2Frepos%2Fx.md"} {
		if got := call(http.MethodGet, p, "good"); got != http.StatusUnauthorized {
			t.Errorf("GET %s with a team bearer = %d, want 401", p, got)
		}
	}
	for p, want := range map[string]bool{
		"/api/projects/PAY.md": true, "/api/projects/PAY/repos/svc.md": true, "/api/projects/PAY/repos/.md": false,
		"/api/projects//repos/svc.md": false, "/api/projects/PAY/other/svc.md": false, "/api/x/PAY.md": false, "/api/projects/PAY%2Emd": false,
		"/api/projects/%2E%2E/repos/x.md": false, "/api/projects/a%2Fb/repos/x.md": false,
	} {
		if ExportPath(p) != want {
			t.Errorf("ExportPath(%q) = %v", p, !want)
		}
	}
	// The handler learns the bearer let the request in, for the export alone.
	var marked bool
	mark := Gate(s, redirect, known)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { marked = ExportOnly(r.Context()) }))
	req := httptest.NewRequest(http.MethodGet, "/api/projects/PAY.md", nil)
	req.Header.Set("Authorization", "Bearer good")
	mark.ServeHTTP(httptest.NewRecorder(), req)
	if !marked {
		t.Error("a bearer-let export request is not marked ExportOnly")
	}
	// None mode has no session to stand in for: the bearer is not asked.
	if got := serve(Gate(nil, "", known)(echo), "/api/projects/PAY.md", nil); got.Code != http.StatusOK {
		t.Errorf("none mode export = %d", got.Code)
	}
}

// None mode has no session, but its writes are still checked for their
// origin; a client sending neither header (curl) still writes.
func TestGateChecksOriginInNoneMode(t *testing.T) {
	h := Gate(nil, "", nil)(echo)
	for _, tc := range []struct {
		name string
		hdr  map[string]string
		want int
	}{
		{"same-origin fetch", map[string]string{"Sec-Fetch-Site": "same-origin"}, 200},
		{"other site", map[string]string{"Sec-Fetch-Site": "cross-site"}, 403},
		{"Origin elsewhere", map[string]string{"Origin": "https://evil.example.com"}, 403},
		{"Origin is the host", map[string]string{"Origin": "http://almanach.internal:8080"}, 200},
		{"curl, neither header", nil, 200},
	} {
		req := httptest.NewRequest(http.MethodPost, "/api/sources", nil)
		req.Host = "almanach.internal:8080"
		for k, v := range tc.hdr {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("%s: %d, want %d", tc.name, rec.Code, tc.want)
		}
	}
}

// The trusted origin is written as a browser writes Origin.
func TestOriginDropsTheDefaultPort(t *testing.T) {
	for in, want := range map[string]string{
		"https://Almanach.example.com:443/api/auth/callback": "https://almanach.example.com",
		"https://almanach.example.com:8443/x":                "https://almanach.example.com:8443",
		"http://localhost:80/x":                              "http://localhost",
		"http://localhost:443/x":                             "http://localhost:443",
		"https://[::1]:443/x":                                "https://[::1]",
		"https://almanach.example.com/x":                     "https://almanach.example.com",
		"no-scheme":                                          "",
	} {
		if got := origin(in); got != want {
			t.Errorf("origin(%q) = %q, want %q", in, got, want)
		}
	}
	// A browser on the default port sends no port: it is trusted.
	s := sessions(t)
	h := Gate(s, "https://almanach.example.com:443/api/auth/callback", nil)(echo)
	req := httptest.NewRequest(http.MethodPost, "/api/sources", nil)
	req.Host = "almanach.internal:8080"
	req.Header.Set("Origin", "https://almanach.example.com")
	req.AddCookie(sessionCookie(t, s, Claims{Subject: "sub-1"}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("default-port origin = %d %s", rec.Code, rec.Body.String())
	}
}

// A change must come from this site, by Sec-Fetch-Site or, failing that,
// Origin; a read and everything outside /api are not asked.
func TestGateRefusesCrossSiteChanges(t *testing.T) {
	s := sessions(t)
	h := Gate(s, redirect, nil)(echo)
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
