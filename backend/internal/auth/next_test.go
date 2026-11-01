package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSafeNext(t *testing.T) {
	for in, want := range map[string]string{
		"/projects/PAY":                 "/projects/PAY",
		"/projects/PAY/repos/api?x=1#s": "/projects/PAY/repos/api?x=1#s",
		"/":                             "/",
		"":                              "/",
		"projects":                      "/",
		"//evil.example.com/":           "/",
		"/\\evil.example.com":           "/",
		"https://evil.example.com/":     "/",
		"/\t/evil.example.com":          "/",
		"/x\r\nSet-Cookie: a=b":         "/",
		"/x\x7f":                        "/",
		"/%zz":                          "/",
	} {
		if got := SafeNext(in); got != want {
			t.Errorf("SafeNext(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNextRoundTrip(t *testing.T) {
	rec := httptest.NewRecorder()
	SetNext(rec, "/projects/PAY?tab=1&x=a b")
	set := rec.Result().Cookies()
	if len(set) != 1 || set[0].Name != NextCookie || !set[0].HttpOnly || !set[0].Secure ||
		set[0].SameSite != http.SameSiteLaxMode || set[0].MaxAge != 600 {
		t.Fatalf("cookie = %+v", set)
	}
	req := httptest.NewRequest(http.MethodGet, CallbackPath, nil)
	req.AddCookie(set[0])
	rec = httptest.NewRecorder()
	if got := TakeNext(rec, req); got != "/projects/PAY?tab=1&x=a b" {
		t.Errorf("TakeNext = %q", got)
	}
	if c := rec.Result().Cookies(); len(c) != 1 || c[0].Name != NextCookie || c[0].MaxAge >= 0 {
		t.Errorf("cleared = %+v", c)
	}
	// No cookie, a forged one, an undecodable one: home.
	for _, ck := range []*http.Cookie{nil, {Name: NextCookie, Value: "%2F%2Fevil.example.com"}, {Name: NextCookie, Value: "%zz"}} {
		req := httptest.NewRequest(http.MethodGet, CallbackPath, nil)
		if ck != nil {
			req.AddCookie(ck)
		}
		if got := TakeNext(httptest.NewRecorder(), req); got != "/" {
			t.Errorf("TakeNext with %+v = %q, want /", ck, got)
		}
	}
}
