package store

import (
	"context"
	"errors"
	"testing"
)

// A project of one is derived from the rows: a source with one repository.
func TestSourceRepoCount_AndSingleRepos(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	for _, k := range []string{"loom", "ipverse"} {
		if err := s.CreateSource(ctx, staticSource(k)); err != nil {
			t.Fatal(err)
		}
	}
	addRepos(t, s,
		SourceRepo{ProjectKey: "loom", Slug: "loom", CloneURL: "https://forge.example.invalid/x/loom"},
		SourceRepo{ProjectKey: "ipverse", Slug: "core", CloneURL: "https://forge.example.invalid/ipverse/core"},
		SourceRepo{ProjectKey: "ipverse", Slug: "lens", CloneURL: "https://forge.example.invalid/ipverse/lens"},
	)
	for key, want := range map[string]int{"loom": 1, "ipverse": 2, "nobody": 0} {
		if n, err := s.SourceRepoCount(ctx, key); err != nil || n != want {
			t.Errorf("SourceRepoCount(%s) = %d %v, want %d", key, n, err, want)
		}
	}
	single, err := s.SingleRepos(ctx)
	if err != nil || len(single) != 1 || single["loom"].Slug != "loom" {
		t.Fatalf("SingleRepos = %+v %v, want loom alone", single, err)
	}
}

// A static folder and its first repository come in one transaction:
// a repository refused takes the folder with it.
func TestCreateSourceWith(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if err := s.CreateSourceWith(ctx, staticSource("lens"), SourceRepo{ProjectKey: "lens", Slug: "lens",
		CloneURL: "https://forge.example.invalid/ipverse/lens.git"}); err != nil {
		t.Fatal(err)
	}
	repos, err := s.SourceRepos(ctx, "lens")
	if err != nil || len(repos) != 1 || repos[0].Slug != "lens" || repos[0].State != RepoPending {
		t.Fatalf("repos = %+v %v, want lens pending", repos, err)
	}
	var other *ClonedElsewhereError
	err = s.CreateSourceWith(ctx, staticSource("again"), SourceRepo{ProjectKey: "again", Slug: "lens",
		CloneURL: "https://forge.example.invalid/ipverse/lens"})
	if !errors.As(err, &other) {
		t.Fatalf("second clone of lens = %v, want cloned elsewhere", err)
	}
	if _, found, err := s.SourceByKey(ctx, "again"); err != nil || found {
		t.Fatalf("the folder outlived its refused repository: %v %v", found, err)
	}
}

func TestSlugFromURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://github.com/ipverse/lens-gateway.git":  "lens-gateway",
		"https://github.com/ipverse/lens-gateway/":     "lens-gateway",
		"https://github.com/ipverse/lens-gateway.git/": "lens-gateway",
		" https://forge.example/scm/abc/Def ":          "Def",
		"https://forge.example":                        "",
		"not a url\x7f":                                "",
	} {
		if got := SlugFromURL(in); got != want {
			t.Errorf("SlugFromURL(%q) = %q, want %q", in, got, want)
		}
	}
}
