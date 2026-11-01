package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func built() fstest.MapFS {
	return fstest.MapFS{
		"index.html":           {Data: []byte("<!doctype html><div id=root>")},
		"assets/index-abc.js":  {Data: []byte("console.log(1)")},
		"assets/index-abc.css": {Data: []byte("body{}")},
	}
}

func req(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

// A client route is served the shell so the SPA can render it; anything
// else is a real 404. A catch-all that answered 200 for every path would
// turn a typo into a blank page and hide it from any uptime check.
func TestKnownRoutesGetTheShellAndUnknownOnesA404(t *testing.T) {
	h := handler(built())
	for _, path := range []string{
		"/", "/faq", "/warteschlange", "/projekte/PLAT", "/projekte/pay.v2/repos/pay-api", "/projekte/PLAT/repos/files/notizen/42",
		"/projekte/PLAT/repos/files/notizen/123456789", "/projekte/~jan/repos/scratch",
	} {
		w := req(t, h, path)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "id=root") {
			t.Errorf("%s: status = %d, want 200 with the shell", path, w.Code)
		}
	}
	for _, path := range []string{
		"/nope", "/projekte", "/projekte/", "/projekte/PLAT/repos", "/projekte/PLAT/repos/files/notizen/0",
		"/projekte/PLAT/repos/files/notizen/abc", "/projekte/PLAT/repos/files/notizen/1234567890",
		"/projekte/a%20b", "/projekte/PLAT/repos/~x", "/FAQ", "/api/projects", "/auth/ping", "/webhooks/noergler",
		"/assets/missing.js",
	} {
		if code := req(t, h, path).Code; code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", path, code)
		}
	}
}

// A built asset is served as it is; an unbuilt dist answers its routes with
// the placeholder and a 503, never a blank 200 a healthcheck reads as fine.
func TestAssetsAndThePlaceholder(t *testing.T) {
	h := handler(built())
	if w := req(t, h, "/assets/index-abc.js"); w.Code != http.StatusOK || w.Body.String() != "console.log(1)" {
		t.Errorf("asset: %d %q", w.Code, w.Body.String())
	}
	unbuilt := handler(fstest.MapFS{".gitkeep": {Data: nil}})
	w := req(t, unbuilt, "/projekte/PLAT")
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "make fe-build") {
		t.Errorf("unbuilt: %d %q, want 503 with the placeholder", w.Code, w.Body.String())
	}
}
