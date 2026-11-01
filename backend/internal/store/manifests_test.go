package store

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/trick77/almanach/internal/manifest"
)

func addRepos(t *testing.T, s *Store, repos ...SourceRepo) {
	t.Helper()
	for _, r := range repos {
		if err := s.AddSourceRepo(context.Background(), r); err != nil {
			t.Fatalf("AddSourceRepo(%s/%s): %v", r.ProjectKey, r.Slug, err)
		}
	}
}

func repoIDOf(t *testing.T, s *Store, key, slug string) int64 {
	t.Helper()
	_, rid, ok, err := s.RepoIDs(context.Background(), key, slug)
	if err != nil || !ok {
		t.Fatalf("RepoIDs(%s/%s) = %v %v", key, slug, ok, err)
	}
	return rid
}

func saveRead(t *testing.T, s *Store, key, slug, commit string, read manifest.Read) {
	t.Helper()
	if err := s.SaveManifests(context.Background(), repoIDOf(t, s, key, slug), commit, read); err != nil {
		t.Fatal(err)
	}
}

func TestManifests_SavedAndReplaced(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if err := s.CreateSource(ctx, staticSource("tools")); err != nil {
		t.Fatal(err)
	}
	addRepos(t, s, SourceRepo{ProjectKey: "tools", Slug: "wire", CloneURL: "https://forge.example.invalid/x/wire"})
	rid := repoIDOf(t, s, "tools", "wire")
	if _, ok, err := s.ManifestCommit(ctx, rid); err != nil || ok {
		t.Fatalf("ManifestCommit before a read = %v %v, want none", ok, err)
	}
	saveRead(t, s, "tools", "wire", "aaa", manifest.Read{Provides: []string{"go:example.invalid/wire"}})
	saveRead(t, s, "tools", "wire", "bbb", manifest.Read{})
	if at, ok, err := s.ManifestCommit(ctx, rid); err != nil || !ok || at != "bbb" {
		t.Fatalf("ManifestCommit = %q %v %v, want the second read", at, ok, err)
	}
	// The row goes, its manifests with it.
	if ok, err := s.DeleteSourceRepo(ctx, "tools", "wire"); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if _, ok, _ := s.ManifestCommit(ctx, rid); ok {
		t.Fatal("manifests outlived their source row")
	}
	if err := s.SaveManifests(ctx, rid, "ccc", manifest.Read{}); !errors.Is(err, ErrNoRepo) {
		t.Fatalf("SaveManifests on a gone row = %v, want ErrNoRepo", err)
	}
}

func TestDependencies(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	for _, k := range []string{"apps", "libs"} {
		if err := s.CreateSource(ctx, staticSource(k)); err != nil {
			t.Fatal(err)
		}
	}
	addRepos(t, s,
		SourceRepo{ProjectKey: "libs", Slug: "wire", CloneURL: "https://forge.example.invalid/x/wire"},
		SourceRepo{ProjectKey: "libs", Slug: "fetch", CloneURL: "https://forge.example.invalid/x/fetch"},
		SourceRepo{ProjectKey: "libs", Slug: "fork-a", CloneURL: "https://forge.example.invalid/x/fork-a"},
		SourceRepo{ProjectKey: "libs", Slug: "fork-b", CloneURL: "https://forge.example.invalid/x/fork-b"},
		SourceRepo{ProjectKey: "apps", Slug: "loom", CloneURL: "https://forge.example.invalid/x/loom"},
		SourceRepo{ProjectKey: "apps", Slug: "diag", CloneURL: "https://forge.example.invalid/x/diag"},
	)
	saveRead(t, s, "libs", "wire", "1", manifest.Read{Provides: []string{"go:example.invalid/wire", "go:example.invalid/wire/extra"}})
	saveRead(t, s, "libs", "fetch", "1", manifest.Read{Provides: []string{"go:example.invalid/fetch"}})
	saveRead(t, s, "libs", "fork-a", "1", manifest.Read{Provides: []string{"npm:shared"}})
	saveRead(t, s, "libs", "fork-b", "1", manifest.Read{Provides: []string{"npm:shared"}})
	saveRead(t, s, "apps", "loom", "1", manifest.Read{
		Provides: []string{"go:example.invalid/loom"},
		Requires: []manifest.Dep{
			{Coord: "go:example.invalid/fetch", Version: "v0.2.0"},
			{Coord: "go:example.invalid/wire", Version: "v0.1.8"},
			{Coord: "go:example.invalid/wire/extra", Version: "v0.1.7"},
			{Coord: "go:golang.org/x/sync", Version: "v0.9.0"},
			{Coord: "npm:shared", Version: "^1.0.0"},
		},
	})
	saveRead(t, s, "apps", "diag", "1", manifest.Read{Requires: []manifest.Dep{{Coord: "go:example.invalid/wire", Version: "v0.1.6"}}})

	g, err := s.Dependencies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	loom, diag := RepoRef{"apps", "loom"}, RepoRef{"apps", "diag"}
	wire, fetch := RepoRef{"libs", "wire"}, RepoRef{"libs", "fetch"}
	want := []Edge{
		{From: diag, To: wire, Coords: []string{"go:example.invalid/wire"}, Version: "v0.1.6"},
		{From: loom, To: fetch, Coords: []string{"go:example.invalid/fetch"}, Version: "v0.2.0"},
		// Two coordinates of one repository are one edge, the first one's
		// version on it.
		{From: loom, To: wire, Coords: []string{"go:example.invalid/wire", "go:example.invalid/wire/extra"}, Version: "v0.1.8"},
	}
	if !reflect.DeepEqual(g.Edges, want) {
		t.Fatalf("edges = %+v\nwant %+v", g.Edges, want)
	}
	if len(g.Ambiguous) != 1 || g.Ambiguous[0].Coord != "npm:shared" || len(g.Ambiguous[0].Providers) != 2 || g.Ambiguous[0].Twice {
		t.Fatalf("ambiguous = %+v, want npm:shared with two providers, two repositories", g.Ambiguous)
	}
	if len(g.Repos) != 6 {
		t.Fatalf("repos = %v, want the six read", g.Repos)
	}

	// A row removed takes its edges with it, at the next read.
	if ok, err := s.DeleteSourceRepo(ctx, "libs", "wire"); err != nil || !ok {
		t.Fatal(ok, err)
	}
	g, err = s.Dependencies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Edges) != 1 || g.Edges[0].To != fetch {
		t.Fatalf("edges after the removal = %+v, want loom → fetch alone", g.Edges)
	}
}

// One repository two sources cloned before the rule is told apart from a
// fork: the ambiguity says it is cloned twice.
func TestDependencies_ClonedTwice(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	for _, k := range []string{"apps", "libs"} {
		if err := s.CreateSource(ctx, staticSource(k)); err != nil {
			t.Fatal(err)
		}
	}
	addRepos(t, s,
		SourceRepo{ProjectKey: "libs", Slug: "wire", CloneURL: "https://forge.example.invalid/x/wire"},
		SourceRepo{ProjectKey: "apps", Slug: "wire", CloneURL: "https://forge.example.invalid/x/other"},
	)
	// The second row as it was written before the rule.
	if _, err := s.db.ExecContext(ctx, `UPDATE source_repos SET clone_url = 'https://forge.example.invalid/x/wire.git' WHERE repo_id = ?`,
		repoIDOf(t, s, "apps", "wire")); err != nil {
		t.Fatal(err)
	}
	saveRead(t, s, "libs", "wire", "1", manifest.Read{Provides: []string{"go:example.invalid/wire"}})
	saveRead(t, s, "apps", "wire", "1", manifest.Read{Provides: []string{"go:example.invalid/wire"}})
	// Nothing requires it yet: no line for it.
	g, err := s.Dependencies(ctx)
	if err != nil || len(g.Ambiguous) != 0 {
		t.Fatalf("ambiguous = %+v %v, want none while nothing requires it", g.Ambiguous, err)
	}
	addRepos(t, s, SourceRepo{ProjectKey: "apps", Slug: "loom", CloneURL: "https://forge.example.invalid/x/loom"})
	saveRead(t, s, "apps", "loom", "1", manifest.Read{Requires: []manifest.Dep{{Coord: "go:example.invalid/wire", Version: "v1"}}})
	g, err = s.Dependencies(ctx)
	if err != nil || len(g.Ambiguous) != 1 || !g.Ambiguous[0].Twice {
		t.Fatalf("ambiguous = %+v %v, want one cloned twice", g.Ambiguous, err)
	}
}

// Manifests read by an older reader are due again; an edge takes the first
// version one of its coordinates names.
func TestManifests_ReaderAndVersion(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if err := s.CreateSource(ctx, staticSource("libs")); err != nil {
		t.Fatal(err)
	}
	addRepos(t, s,
		SourceRepo{ProjectKey: "libs", Slug: "lib", CloneURL: "https://forge.example.invalid/x/lib"},
		SourceRepo{ProjectKey: "libs", Slug: "app", CloneURL: "https://forge.example.invalid/x/app"},
	)
	saveRead(t, s, "libs", "lib", "1", manifest.Read{Provides: []string{"maven:g:api", "maven:g:core"}})
	saveRead(t, s, "libs", "app", "1", manifest.Read{Requires: []manifest.Dep{{Coord: "maven:g:api"}, {Coord: "maven:g:core", Version: "1.2.0"}}})
	g, err := s.Dependencies(ctx)
	if err != nil || len(g.Edges) != 1 || g.Edges[0].Version != "1.2.0" {
		t.Fatalf("edges = %+v %v, want one at 1.2.0", g.Edges, err)
	}
	rid := repoIDOf(t, s, "libs", "lib")
	if _, err := s.db.ExecContext(ctx, `UPDATE repo_manifests SET reader = ? WHERE repo_id = ?`, manifest.Version-1, rid); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.ManifestCommit(ctx, rid); err != nil || ok {
		t.Fatalf("ManifestCommit after an older reader = %v %v, want due again", ok, err)
	}
}

// A repository requiring what it provides itself (a monorepo's modules
// read as one) draws no edge to itself.
func TestDependencies_NoSelfEdge(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if err := s.CreateSource(ctx, staticSource("libs")); err != nil {
		t.Fatal(err)
	}
	addRepos(t, s, SourceRepo{ProjectKey: "libs", Slug: "mono", CloneURL: "https://forge.example.invalid/x/mono"})
	saveRead(t, s, "libs", "mono", "1", manifest.Read{Provides: []string{"npm:a"}, Requires: []manifest.Dep{{Coord: "npm:a"}}})
	g, err := s.Dependencies(ctx)
	if err != nil || len(g.Edges) != 0 {
		t.Fatalf("edges = %+v %v, want none", g.Edges, err)
	}
}

func TestCloneKey(t *testing.T) {
	for in, want := range map[string]string{
		"https://GitHub.com/Acme/Wire.git":     "https://github.com/acme/wire",
		"https://github.com:443/acme/wire/":    "https://github.com/acme/wire",
		"https://github.com/acme/wire.git/":    "https://github.com/acme/wire",
		"https://forge.example/scm/abc/def":    "https://forge.example/scm/abc/def",
		"https://forge.example:8443/scm/a.git": "https://forge.example:8443/scm/a",
		"not a url":                            "",
	} {
		if got := CloneKey(in); got != want {
			t.Errorf("CloneKey(%q) = %q, want %q", in, got, want)
		}
	}
}

// One repository is one source row, whatever source: a second add, a
// rename onto its URL and a listing naming it are refused or left out.
func TestCloneURL_OneRowAcrossSources(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	for _, k := range []string{"apps", "libs"} {
		if err := s.CreateSource(ctx, staticSource(k)); err != nil {
			t.Fatal(err)
		}
	}
	addRepos(t, s,
		SourceRepo{ProjectKey: "libs", Slug: "wire", CloneURL: "https://forge.example.invalid/x/wire.git"},
		SourceRepo{ProjectKey: "apps", Slug: "loom", CloneURL: "https://forge.example.invalid/x/loom"},
	)
	var other *ClonedElsewhereError
	err := s.AddSourceRepo(ctx, SourceRepo{ProjectKey: "apps", Slug: "wire", CloneURL: "https://Forge.example.invalid/x/Wire"})
	if !errors.As(err, &other) || other.ProjectKey != "libs" || other.Slug != "wire" {
		t.Fatalf("second add = %v, want cloned as libs/wire", err)
	}
	if err := s.RenameRepo(ctx, "apps", "loom", "loom", "https://forge.example.invalid/x/wire"); !errors.As(err, &other) {
		t.Fatalf("rename onto its URL = %v, want cloned elsewhere", err)
	}
	// A row's own URL, written again, is no conflict.
	if err := s.RenameRepo(ctx, "libs", "wire", "wire", "https://forge.example.invalid/x/wire"); err != nil {
		t.Fatalf("rename to its own URL: %v", err)
	}

	if err := s.CreateSource(ctx, bitbucketSource("SHOP")); err != nil {
		t.Fatal(err)
	}
	res, err := s.ApplyListing(ctx, "SHOP", []ListedRepo{
		{Slug: "wire", CloneURL: "https://forge.example.invalid/x/wire.git", ForgeID: 7},
		{Slug: "cart", CloneURL: "https://forge.example.invalid/x/cart.git", ForgeID: 8},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Added != 1 || !reflect.DeepEqual(res.Elsewhere, []string{"wire (as libs/wire)"}) {
		t.Fatalf("listing = %+v, want cart added and wire left out", res)
	}
	repos, err := s.SourceRepos(ctx, "SHOP")
	if err != nil || len(repos) != 1 || repos[0].Slug != "cart" {
		t.Fatalf("SHOP repos = %+v %v, want cart alone", repos, err)
	}
}

// A row gone missing from its listing holds its URL no longer: the
// repository moved to a folder is taken there.
func TestCloneURL_MissingRowHoldsNothing(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if err := s.CreateSource(ctx, bitbucketSource("SHOP")); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSource(ctx, staticSource("libs")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyListing(ctx, "SHOP", []ListedRepo{{Slug: "wire", CloneURL: "https://forge.example.invalid/x/wire.git", ForgeID: 7}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyListing(ctx, "SHOP", nil); err != nil {
		t.Fatal(err)
	}
	addRepos(t, s, SourceRepo{ProjectKey: "libs", Slug: "wire", CloneURL: "https://forge.example.invalid/x/wire.git"})
	// Listed again in SHOP: now the folder's, left out there, never missing
	// twice over.
	res, err := s.ApplyListing(ctx, "SHOP", []ListedRepo{{Slug: "wire", CloneURL: "https://forge.example.invalid/x/wire.git", ForgeID: 7}})
	if err != nil || !reflect.DeepEqual(res.Elsewhere, []string{"wire (as libs/wire)"}) || res.Missing != 0 {
		t.Fatalf("listing again = %+v %v, want wire left out, nothing newly missing", res, err)
	}
}
