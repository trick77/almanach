package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"golang.org/x/oauth2"

	"github.com/trick77/almanach/internal/auth"
	"github.com/trick77/almanach/internal/config"
	"github.com/trick77/almanach/internal/httpapi"
	kitauth "github.com/trick77/ship/kit/auth"
)

// fakeOIDC stands in for kit's flow: Start redirects to a fixed URL,
// Callback answers what it is told.
type fakeOIDC struct {
	claims auth.Claims
	err    error
}

func (f *fakeOIDC) Start(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "https://auth.example.com/authorize", http.StatusFound)
}

func (f *fakeOIDC) Callback(http.ResponseWriter, *http.Request) (auth.Claims, error) {
	return f.claims, f.err
}

func testSessions(t *testing.T) *kitauth.Sessions {
	t.Helper()
	s, err := auth.NewSessions([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// oidcHandler is the API in oidc mode, its log written to buf, the access
// log's routine lines (debug) included.
func oidcHandler(t *testing.T, oidc OIDCService, sessions *kitauth.Sessions, buf *bytes.Buffer) http.Handler {
	t.Helper()
	log := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	srv := httpapi.New(func(context.Context) error { return nil }, log)
	Register(srv, Deps{Store: newFakeStore(), Keys: keysFile(t), Worker: &fakeWaker{}, AuthMode: config.AuthOIDC, ProblemWindow: 90,
		OIDC: oidc, Sessions: sessions, RedirectURL: "https://almanach.example.com/api/auth/callback", Log: log})
	return srv.Handler()
}

// get sends one request as the UI would: same-origin.
func get(h http.Handler, method, path string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func cookieNamed(t *testing.T, rec *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no %s cookie in %v", name, rec.Result().Cookies())
	return nil
}

func sessionFor(t *testing.T, s *kitauth.Sessions, c auth.Claims) *http.Cookie {
	t.Helper()
	rec := httptest.NewRecorder()
	if err := s.Issue(rec, c); err != nil {
		t.Fatal(err)
	}
	return cookieNamed(t, rec, auth.SessionCookie)
}

func TestOIDCGatesTheAPI(t *testing.T) {
	var buf bytes.Buffer
	s := testSessions(t)
	h := oidcHandler(t, &fakeOIDC{}, s, &buf)
	for _, p := range []string{"/api/projects", "/api/me", "/api/projects/PAY"} {
		if rec := get(h, http.MethodGet, p); rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), `"error"`) {
			t.Errorf("%s without a session = %d %s, want 401 JSON", p, rec.Code, rec.Body.String())
		}
	}
	if rec := get(h, http.MethodPost, "/api/auth/logout"); rec.Code != http.StatusUnauthorized {
		t.Errorf("logout without a session = %d", rec.Code)
	}
	ck := sessionFor(t, s, auth.Claims{Subject: "sub-1", Email: "jan@example.com"})
	if rec := get(h, http.MethodGet, "/api/projects", ck); rec.Code != http.StatusOK {
		t.Fatalf("/api/projects with a session = %d %s", rec.Code, rec.Body.String())
	}
	// The reader reaches the access log line, off the claims.
	if !strings.Contains(buf.String(), "user=jan@example.com") {
		t.Errorf("log lacks the reader:\n%s", buf.String())
	}
	// The SPA shell holds no data and needs no session.
	if rec := get(h, http.MethodGet, "/"); rec.Code == http.StatusUnauthorized {
		t.Errorf("/ = %d, want the shell", rec.Code)
	}
}

// Machine traffic never passes the sign-in: the bearer is the team.
func TestOIDCLeavesWebhookAndPingToTheBearer(t *testing.T) {
	var buf bytes.Buffer
	h := oidcHandler(t, &fakeOIDC{}, testSessions(t), &buf)
	code, body := do(t, h, "GET", "/auth/ping", "tok-platform", "", nil)
	if code != 200 || field(t, body, "team") != "platform" {
		t.Errorf("ping = %d %v", code, body)
	}
	code, body = do(t, h, "POST", "/webhooks/noergler", "tok-platform", validPayload(7), nil)
	if code != 202 || field(t, body, "status") != "accepted" {
		t.Errorf("webhook = %d %v", code, body)
	}
	if code, _ := do(t, h, "POST", "/webhooks/noergler", "", validPayload(7), nil); code != 401 {
		t.Errorf("webhook without a bearer = %d", code)
	}
}

func TestMe(t *testing.T) {
	h := handler(t, newFakeStore(), &fakeWaker{}, config.AuthNone)
	code, body := do(t, h, "GET", "/api/me", "", "", nil)
	if code != 200 || field(t, body, "mode") != "none" || field(t, body, "user") != "" {
		t.Errorf("none = %d %v", code, body)
	}
	var buf bytes.Buffer
	s := testSessions(t)
	h = oidcHandler(t, &fakeOIDC{}, s, &buf)
	rec := get(h, http.MethodGet, "/api/me", sessionFor(t, s, auth.Claims{Subject: "sub-1"}))
	if rec.Code != 200 || rec.Body.String() != `{"mode":"oidc","user":"sub-1"}`+"\n" {
		t.Errorf("oidc = %d %q", rec.Code, rec.Body.String())
	}
}

func TestSignInRoutesOffInNoneMode(t *testing.T) {
	h := handler(t, newFakeStore(), &fakeWaker{}, config.AuthNone)
	for _, r := range []struct{ method, path string }{{"GET", "/api/auth/login"}, {"GET", "/api/auth/callback"}, {"POST", "/api/auth/logout"}} {
		if code, _ := do(t, h, r.method, r.path, "", "", nil); code != http.StatusServiceUnavailable {
			t.Errorf("%s %s = %d, want 503", r.method, r.path, code)
		}
	}
}

func TestLogin(t *testing.T) {
	var buf bytes.Buffer
	rec := get(oidcHandler(t, &fakeOIDC{}, testSessions(t), &buf), http.MethodGet, "/api/auth/login")
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "https://auth.example.com/authorize" {
		t.Errorf("login = %d %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestCallbackSignsIn(t *testing.T) {
	var buf bytes.Buffer
	h := oidcHandler(t, &fakeOIDC{claims: auth.Claims{Subject: "sub-1", Email: "jan@example.com"}}, testSessions(t), &buf)
	rec := get(h, http.MethodGet, "/api/auth/callback?code=abc&state=xyz")
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/" {
		t.Fatalf("callback = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	ck := cookieNamed(t, rec, auth.SessionCookie)
	if ck.Value == "" || !ck.Secure || !ck.HttpOnly {
		t.Errorf("cookie = %+v, want a Secure HttpOnly value", ck)
	}
	me := get(h, http.MethodGet, "/api/me", ck)
	if me.Code != 200 || !strings.Contains(me.Body.String(), `"user":"jan@example.com"`) {
		t.Errorf("/api/me with the cookie = %d %s", me.Code, me.Body.String())
	}
}

// A deep link survives the provider's round trip; a foreign one does not.
func TestSignInReturnsToTheDeepLink(t *testing.T) {
	var buf bytes.Buffer
	h := oidcHandler(t, &fakeOIDC{claims: auth.Claims{Subject: "sub-1"}}, testSessions(t), &buf)
	for next, want := range map[string]string{
		"/projekte/PAY/repos/api?x=1":   "/projekte/PAY/repos/api?x=1",
		"//evil.example.com/":           "/",
		"https://evil.example.com/":     "/",
		"/\\evil.example.com":           "/",
		"/x\r\nSet-Cookie: a=b":         "/",
		"/x%0D%0Ay":                     "/x%0D%0Ay", // escaped: a path on this site
		"":                              "/",
		"%2F%2Fevil.example.com%2Fpath": "/",
	} {
		login := get(h, http.MethodGet, "/api/auth/login?next="+url.QueryEscape(next))
		ck := cookieNamed(t, login, auth.NextCookie)
		cb := get(h, http.MethodGet, "/api/auth/callback?code=abc&state=xyz", ck)
		if got := cb.Header().Get("Location"); got != want {
			t.Errorf("next %q: callback to %q, want %q", next, got, want)
		}
		if c := cookieNamed(t, cb, auth.NextCookie); c.MaxAge >= 0 {
			t.Errorf("next %q: cookie not cleared: %+v", next, c)
		}
	}
	// A next cookie planted by someone else is checked again.
	cb := get(h, http.MethodGet, "/api/auth/callback?code=abc&state=xyz", &http.Cookie{Name: auth.NextCookie, Value: url.QueryEscape("//evil.example.com")})
	if got := cb.Header().Get("Location"); got != "/" {
		t.Errorf("planted cookie: callback to %q", got)
	}
}

func TestCallbackFailureHalts(t *testing.T) {
	for name, f := range map[string]*fakeOIDC{
		"provider":   {err: errors.New("exchange oidc code: provider said no")},
		"no subject": {claims: auth.Claims{Email: "jan@example.com"}},
	} {
		var buf bytes.Buffer
		rec := get(oidcHandler(t, f, testSessions(t), &buf), http.MethodGet, "/api/auth/callback?state=bad")
		if got := rec.Header().Get("Location"); got != "/?auth_error=oidc_callback_failed" {
			t.Errorf("%s: Location = %q", name, got)
		}
		if strings.Contains(rec.Body.String(), "provider said no") {
			t.Errorf("%s: body names the failure: %q", name, rec.Body.String())
		}
		for _, c := range rec.Result().Cookies() {
			if c.Name == auth.SessionCookie {
				t.Errorf("%s: a session was issued", name)
			}
		}
		if !strings.Contains(buf.String(), "oidc callback failed") {
			t.Errorf("%s: failure not logged:\n%s", name, buf.String())
		}
	}
}

func TestLogoutClearsTheCookie(t *testing.T) {
	var buf bytes.Buffer
	s := testSessions(t)
	rec := get(oidcHandler(t, &fakeOIDC{}, s, &buf), http.MethodPost, "/api/auth/logout", sessionFor(t, s, auth.Claims{Subject: "sub-1"}))
	if rec.Code != 200 || rec.Body.String() != `{"redirect_url":"/?signed_out=1"}`+"\n" {
		t.Fatalf("logout = %d %q", rec.Code, rec.Body.String())
	}
	if c := cookieNamed(t, rec, auth.SessionCookie); c.Value != "" || c.MaxAge >= 0 {
		t.Errorf("cookie = %+v, want cleared", c)
	}
}

// A sibling site cannot sign the reader out, nor write a source.
func TestCrossSiteChangesRefused(t *testing.T) {
	var buf bytes.Buffer
	s := testSessions(t)
	h := oidcHandler(t, &fakeOIDC{}, s, &buf)
	ck := sessionFor(t, s, auth.Claims{Subject: "sub-1"})
	for _, path := range []string{"/api/auth/logout", "/api/sources"} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Sec-Fetch-Site", "same-site")
		req.AddCookie(ck)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s from a sibling = %d %s, want 403", path, rec.Code, rec.Body.String())
		}
		if len(rec.Result().Cookies()) != 0 {
			t.Errorf("%s: cookies changed: %v", path, rec.Result().Cookies())
		}
	}
	// The webhook is no browser's: no such check.
	if code, _ := do(t, h, "POST", "/webhooks/noergler", "tok-platform", validPayload(8), nil); code != 202 {
		t.Errorf("webhook = %d", code)
	}
}

// A provider down at startup takes down the sign-in only: the webhook and
// the ping work, the login lands on a page that halts with a button, the
// wiki stays behind its 401.
func TestProviderDownLeavesTheFeedUp(t *testing.T) {
	var buf bytes.Buffer
	never := auth.NewProvider(auth.Config{Issuer: "https://auth.example.com"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h := oidcHandler(t, never, testSessions(t), &buf)
	if code, body := do(t, h, "POST", "/webhooks/noergler", "tok-platform", validPayload(7), nil); code != 202 {
		t.Errorf("webhook = %d %v", code, body)
	}
	if code, _ := do(t, h, "GET", "/auth/ping", "tok-platform", "", nil); code != 200 {
		t.Errorf("ping = %d", code)
	}
	for _, p := range []string{"/api/auth/login?next=/faq", "/api/auth/callback?code=x&state=y"} {
		rec := get(h, http.MethodGet, p)
		if rec.Code != http.StatusFound || rec.Header().Get("Location") != auth.UnavailableURL {
			t.Errorf("%s = %d %q, want the halting page", p, rec.Code, rec.Header().Get("Location"))
		}
	}
	if rec := get(h, http.MethodGet, "/api/projects"); rec.Code != http.StatusUnauthorized {
		t.Errorf("/api/projects = %d, want 401", rec.Code)
	}
}

// The real provider: down at startup, discovered later, login then works.
func TestLoginOnceDiscovered(t *testing.T) {
	var up atomic.Bool
	var iss *httptest.Server
	iss = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if !up.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": iss.URL, "authorization_endpoint": iss.URL + "/authorize", "token_endpoint": iss.URL + "/token",
			"jwks_uri": iss.URL + "/jwks", "id_token_signing_alg_values_supported": []string{"RS256"},
		})
	}))
	defer iss.Close()
	var buf bytes.Buffer
	p := auth.NewProvider(auth.Config{Issuer: iss.URL, ClientID: "almanach", ClientSecret: "s",
		RedirectURL: "https://almanach.example.com/api/auth/callback"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := p.Discover(t.Context()); err == nil {
		t.Fatal("discovered a provider that is down")
	}
	h := oidcHandler(t, p, testSessions(t), &buf)
	if rec := get(h, http.MethodGet, "/api/auth/login"); rec.Header().Get("Location") != auth.UnavailableURL {
		t.Errorf("login while down = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	up.Store(true)
	if err := p.Discover(t.Context()); err != nil {
		t.Fatal(err)
	}
	rec := get(h, http.MethodGet, "/api/auth/login")
	if rec.Code != http.StatusFound || !strings.HasPrefix(rec.Header().Get("Location"), iss.URL+"/authorize?") {
		t.Errorf("login once discovered = %d %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestRegisterRefusesOIDCWithoutSessions(t *testing.T) {
	for name, d := range map[string]Deps{
		"oidc without sessions": {AuthMode: config.AuthOIDC, OIDC: &fakeOIDC{}},
		"none with sessions":    {AuthMode: config.AuthNone, OIDC: &fakeOIDC{}, Sessions: testSessions(t)},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: Register did not panic", name)
				}
			}()
			Register(httpapi.New(func(context.Context) error { return nil }, slog.New(slog.NewTextHandler(io.Discard, nil))), d)
		}()
	}
}

// fakeBackend is the network half of kit's flow: the token exchange and
// the ID token check. Everything else, state, nonce and PKCE in their
// cookies, is kit's own code. nonce is what the ID token would carry: the
// test copies it from login's cookie.
type fakeBackend struct {
	claims auth.Claims
	nonce  string
}

func (*fakeBackend) AuthCodeURL(state string, _ ...oauth2.AuthCodeOption) string {
	return "https://auth.example.com/authorize?state=" + url.QueryEscape(state)
}

func (*fakeBackend) Exchange(_ context.Context, code string, _ ...oauth2.AuthCodeOption) (*oauth2.Token, error) {
	if code != "good-code" {
		return nil, errors.New("bad code")
	}
	return &oauth2.Token{AccessToken: "at"}, nil
}

func (b *fakeBackend) Verify(context.Context, *oauth2.Token) (auth.Claims, string, error) {
	return b.claims, b.nonce, nil
}

// The whole sign-in through kit's real flow: login sets state, nonce and
// PKCE cookies, the callback checks them and issues the session.
func TestSignInThroughKitsFlow(t *testing.T) {
	b := &fakeBackend{claims: auth.Claims{Subject: "sub-1", Email: "jan@example.com"}}
	oidc := kitauth.NewOIDCWithBackend(b, auth.OIDCConfig(auth.Config{}))
	var buf bytes.Buffer
	h := oidcHandler(t, oidc, testSessions(t), &buf)

	start := get(h, http.MethodGet, "/api/auth/login")
	loc, err := url.Parse(start.Header().Get("Location"))
	if start.Code != http.StatusFound || err != nil {
		t.Fatalf("login = %d %q", start.Code, start.Header().Get("Location"))
	}
	state := loc.Query().Get("state")
	transient := start.Result().Cookies()
	b.nonce = cookieNamed(t, start, "almanach_oidc_nonce").Value

	cb := get(h, http.MethodGet, "/api/auth/callback?code=good-code&state="+url.QueryEscape(state), transient...)
	if cb.Code != http.StatusFound || cb.Header().Get("Location") != "/" {
		t.Fatalf("callback = %d %q\n%s", cb.Code, cb.Header().Get("Location"), buf.String())
	}
	me := get(h, http.MethodGet, "/api/me", cookieNamed(t, cb, auth.SessionCookie))
	if me.Code != 200 || !strings.Contains(me.Body.String(), `"user":"jan@example.com"`) {
		t.Errorf("/api/me = %d %s", me.Code, me.Body.String())
	}

	// A forged state is refused, whatever the code.
	bad := get(h, http.MethodGet, "/api/auth/callback?code=good-code&state=forged", transient...)
	if bad.Header().Get("Location") != "/?auth_error=oidc_callback_failed" {
		t.Errorf("forged state = %q", bad.Header().Get("Location"))
	}
}
