package bitbucket

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/trick77/almanach/internal/gitrepo"
)

func TestListRepos_PagesSkipsArchivedAndSendsAuth(t *testing.T) {
	var auths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bb/rest/api/1.0/projects/SHOP/repos" {
			t.Errorf("path = %s", r.URL.Path)
		}
		auths = append(auths, r.Header.Get("Authorization"))
		switch r.URL.Query().Get("start") {
		case "0":
			fmt.Fprint(w, `{"values":[
			  {"slug":"ui","archived":false,"links":{"clone":[{"href":"ssh://git@x/shop/ui.git","name":"ssh"},{"href":"https://x/scm/shop/ui.git","name":"http"}]}},
			  {"slug":"old","archived":true,"links":{"clone":[{"href":"https://x/scm/shop/old.git","name":"http"}]}}
			],"isLastPage":false,"nextPageStart":2}`)
		case "2":
			fmt.Fprint(w, `{"values":[{"slug":"api","archived":false,"links":{"clone":[{"href":"https://x/scm/shop/api.git","name":"http"}]}}],"isLastPage":true}`)
		default:
			t.Errorf("unexpected start %q", r.URL.Query().Get("start"))
		}
	}))
	defer srv.Close()

	got, err := New(srv.Client()).ListRepos(context.Background(), srv.URL+"/bb/", "SHOP", gitrepo.Access{Token: "tok", Auth: "bearer", Origin: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Slug != "ui" || got[0].CloneURL != "https://x/scm/shop/ui.git" || got[1].Slug != "api" {
		t.Fatalf("ListRepos = %+v", got)
	}
	if len(auths) != 2 || auths[0] != "Bearer tok" {
		t.Fatalf("auth headers = %v", auths)
	}
}

// The listing sends the token only to the access's origin: a host bound
// elsewhere gets the request without it.
func TestListRepos_NoTokenOffTheOrigin(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		fmt.Fprint(w, `{"values":[],"isLastPage":true}`)
	}))
	defer srv.Close()
	a := gitrepo.Access{Token: "tok", Auth: "bearer", Origin: "https://forge.example.invalid"}
	if _, err := New(srv.Client()).ListRepos(context.Background(), srv.URL, "SHOP", a); err != nil {
		t.Fatal(err)
	}
	if auth != "" {
		t.Fatalf("token sent off its origin: %q", auth)
	}
}

func TestListRepos_BasicAuthAndFailures(t *testing.T) {
	status := http.StatusUnauthorized
	var user, pass string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Basic goes as the standard header, the same value git is handed.
		user, pass, _ = r.BasicAuth()
		w.WriteHeader(status)
		fmt.Fprint(w, `<html>login</html>`)
	}))
	defer srv.Close()
	l := New(srv.Client())
	_, err := l.ListRepos(context.Background(), srv.URL, "SHOP", gitrepo.Access{Token: "tok", Auth: "basic", User: "me", Origin: srv.URL})
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("401: err = %v", err)
	}
	if user != "me" || pass != "tok" {
		t.Fatalf("basic auth = %q:%q", user, pass)
	}
	status = http.StatusOK
	if _, err := l.ListRepos(context.Background(), srv.URL, "SHOP", gitrepo.Access{}); err == nil || !strings.Contains(err.Error(), "not a listing") {
		t.Fatalf("html body: err = %v", err)
	}
	if _, err := l.ListRepos(context.Background(), "ftp://x", "SHOP", gitrepo.Access{}); err == nil {
		t.Fatal("non-http host accepted")
	}
}

func TestListRepos_NoHTTPLinksIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"values":[{"slug":"ui","archived":false,"links":{"clone":[{"href":"ssh://git@x/shop/ui.git","name":"ssh"}]}}],"isLastPage":true}`)
	}))
	defer srv.Close()
	_, err := New(srv.Client()).ListRepos(context.Background(), srv.URL, "SHOP", gitrepo.Access{})
	if err == nil || !strings.Contains(err.Error(), "none with an http clone link") {
		t.Fatalf("err = %v", err)
	}
	// An empty project is simply empty.
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"values":[],"isLastPage":true}`)
	}))
	defer empty.Close()
	got, err := New(empty.Client()).ListRepos(context.Background(), empty.URL, "SHOP", gitrepo.Access{})
	if err != nil || len(got) != 0 {
		t.Fatalf("empty project: %v %v", got, err)
	}
}
