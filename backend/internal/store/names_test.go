package store

import (
	"context"
	"errors"
	"testing"
)

// A change of case on a project's own key is a rename like any other:
// afterwards a note delivered under the new spelling lands on the source
// repository and its pages, and the old key still finds the project.
func TestRenameProject_CaseOnly(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	sourceRepo(t, s, "ACME", "svc")
	if _, err := s.InsertRevision(ctx, NewRevision{Scope: ScopeRepository, ProjectKey: "ACME", RepoSlug: "svc", Summary: "kept", SourcePR: "ACME/svc#1"}); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.IsSourceRepo(ctx, "acme", "svc"); ok {
		t.Fatal("acme is a source repository before the rename")
	}
	if _, err := s.RenameProject(ctx, "ACME", "acme"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if ok, err := s.IsSourceRepo(ctx, "acme", "svc"); err != nil || !ok {
		t.Fatalf("acme/svc after the rename: %v %v", ok, err)
	}
	ingest(t, s, noteFor("acme", "svc", 2))
	notes, err := s.Notes(ctx, "ACME", "svc", 10)
	if err != nil || len(notes) != 1 || notes[0].PRKey != "acme/svc#2" {
		t.Errorf("notes under the old key: %+v %v", notes, err)
	}
	p, ok, err := s.CurrentPage(ctx, ScopeRepository, "acme", "svc")
	if err != nil || !ok || p.Summary != "kept" || p.ProjectKey != "acme" {
		t.Errorf("page under the new key: %+v ok=%v err=%v", p, ok, err)
	}
	if key, ok, _ := s.ProjectKeyOf(ctx, "ACME"); !ok || key != "acme" {
		t.Errorf("ProjectKeyOf(ACME) = %q %v", key, ok)
	}
	src, ok, _ := s.SourceByKey(ctx, "ACME")
	if !ok || src.ProjectKey != "acme" {
		t.Errorf("source by the old key: %+v %v", src, ok)
	}
	// And back: its own names, in any case.
	if _, err := s.RenameProject(ctx, "acme", "ACME"); err != nil {
		t.Errorf("rename back: %v", err)
	}
}

// A key another project ever had, in any case, is never given away.
func TestRenameProject_NameNeverReused(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	sourceRepo(t, s, "ONE", "a")
	sourceRepo(t, s, "TWO", "b")
	if _, err := s.RenameProject(ctx, "ONE", "FIRST"); err != nil {
		t.Fatal(err)
	}
	for _, to := range []string{"ONE", "one", "first"} {
		if _, err := s.RenameProject(ctx, "TWO", to); !errors.Is(err, ErrNameTaken) {
			t.Errorf("TWO → %s: %v, want ErrNameTaken", to, err)
		}
	}
	if err := s.CreateSource(ctx, staticSource("one")); !errors.Is(err, ErrNameTaken) {
		t.Errorf("a new source under a former key in another case: %v, want ErrNameTaken", err)
	}
	if _, err := s.RenameProject(ctx, "NOPE", "x"); !errors.Is(err, ErrNoProject) {
		t.Errorf("unknown key: %v, want ErrNoProject", err)
	}
}

// A hand-entered repository renamed keeps its pages, notes and runs; the
// old slug still finds it; a new clone URL queues a sync, refused while a
// run is open (the re-clone would pull the folder from under it); a slug
// another repository had refuses the rename.
func TestRenameRepo(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	sourceRepo(t, s, "PRJ", "svc")
	sourceRepo(t, s, "PRJ", "other")
	if err := s.MarkRepoReady(ctx, "PRJ", "svc", "main", "abc"); err != nil {
		t.Fatal(err)
	}
	ingest(t, s, noteFor("PRJ", "svc", 1))
	if _, err := s.InsertRevision(ctx, NewRevision{Scope: ScopeRepository, ProjectKey: "PRJ", RepoSlug: "svc", Summary: "kept", SourcePR: "PRJ/svc#1"}); err != nil {
		t.Fatal(err)
	}
	run := enqueue(t, s, "PRJ", "svc")
	if err := s.RenameRepo(ctx, "PRJ", "svc", "core", "https://forge.example.invalid/core.git"); !errors.Is(err, ErrRunOpen) {
		t.Fatalf("new clone URL with an open run: %v, want ErrRunOpen", err)
	}
	// A slug alone touches no run: everything it holds is keyed by id.
	if err := s.RenameRepo(ctx, "PRJ", "svc", "svc2", ""); err != nil {
		t.Fatalf("slug only with an open run: %v", err)
	}
	if r, ok, _ := s.RunByID(ctx, run.ID); !ok || r.Slug != "svc2" || !r.Open() {
		t.Fatalf("the open run after the rename: %+v %v", r, ok)
	}
	finish(t, s, run, RunDone)

	if err := s.RenameRepo(ctx, "PRJ", "svc", "core", "https://forge.example.invalid/core.git"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	r, ok, err := s.SourceRepoByName(ctx, "PRJ", "svc")
	if err != nil || !ok || r.Slug != "core" || r.CloneURL != "https://forge.example.invalid/core.git" || r.State != RepoPending {
		t.Errorf("clone by the old slug: %+v ok=%v err=%v", r, ok, err)
	}
	p, ok, _ := s.CurrentPage(ctx, ScopeRepository, "PRJ", "core")
	if !ok || p.Summary != "kept" || p.RepoSlug != "core" {
		t.Errorf("page under the new slug: %+v %v", p, ok)
	}
	if n, _ := s.Notes(ctx, "PRJ", "core", 10); len(n) != 1 || n[0].RepoSlug != "core" || n[0].PRKey != "PRJ/svc#1" {
		t.Errorf("notes under the new slug, PR key as merged: %+v", n)
	}
	if doc, ok, _ := s.DocumentedRun(ctx, "PRJ", "core"); !ok || doc.ID != run.ID || doc.Slug != "core" {
		t.Errorf("documented run under the new slug: %+v %v", doc, ok)
	}
	if key, slug, ok, _ := s.RepoNamesOf(ctx, "PRJ", "svc"); !ok || key != "PRJ" || slug != "core" {
		t.Errorf("RepoNamesOf(svc) = %s/%s %v", key, slug, ok)
	}
	for _, to := range []string{"other", "OTHER"} {
		if err := s.RenameRepo(ctx, "PRJ", "core", to, ""); !errors.Is(err, ErrNameTaken) {
			t.Errorf("core → %s: %v, want ErrNameTaken", to, err)
		}
	}
	if err := s.RenameRepo(ctx, "PRJ", "other", "SVC", ""); !errors.Is(err, ErrNameTaken) {
		t.Errorf("other → SVC (core's former slug): %v, want ErrNameTaken", err)
	}
	if err := s.AddSourceRepo(ctx, SourceRepo{ProjectKey: "PRJ", Slug: "Svc", CloneURL: "https://forge.example.invalid/x.git"}); !errors.Is(err, ErrNameTaken) {
		t.Errorf("a new repository under a former slug in another case: %v, want ErrNameTaken", err)
	}
	if err := s.RenameRepo(ctx, "PRJ", "core", "Core", ""); err != nil {
		t.Errorf("a change of case of its own slug: %v", err)
	}
	if err := s.RenameRepo(ctx, "PRJ", "nope", "x", ""); !errors.Is(err, ErrNoRepo) {
		t.Errorf("unknown slug: %v, want ErrNoRepo", err)
	}
}

// A repository renamed in Bitbucket is the same repository by its forge id:
// slug changed, nothing added, nothing missing, the old slug a name of it.
// A new repository under a former slug of another is left out.
func TestApplyListing_FollowsRenameByForgeID(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if err := s.CreateSource(ctx, bitbucketSource("SHOP")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyListing(ctx, "SHOP", []ListedRepo{{Slug: "ui", CloneURL: "https://forge.example.invalid/ui.git", ForgeID: 11}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertRevision(ctx, NewRevision{Scope: ScopeRepository, ProjectKey: "SHOP", RepoSlug: "ui", Summary: "kept", SourcePR: "SHOP/ui#1"}); err != nil {
		t.Fatal(err)
	}
	res, err := s.ApplyListing(ctx, "SHOP", []ListedRepo{{Slug: "web", CloneURL: "https://forge.example.invalid/web.git", ForgeID: 11}})
	if err != nil || res.Added != 0 || res.Missing != 0 || res.Renamed != 1 || len(res.Conflicts) != 0 {
		t.Fatalf("listing after the rename: %+v %v", res, err)
	}
	repos, _ := s.SourceRepos(ctx, "SHOP")
	if len(repos) != 1 || repos[0].Slug != "web" || repos[0].CloneURL != "https://forge.example.invalid/web.git" {
		t.Errorf("repos: %+v", repos)
	}
	if p, ok, _ := s.CurrentPage(ctx, ScopeRepository, "SHOP", "ui"); !ok || p.Summary != "kept" || p.RepoSlug != "web" {
		t.Errorf("page by the old slug: %+v %v", p, ok)
	}

	res, err = s.ApplyListing(ctx, "SHOP", []ListedRepo{
		{Slug: "web", CloneURL: "https://forge.example.invalid/web.git", ForgeID: 11},
		{Slug: "UI", CloneURL: "https://forge.example.invalid/ui-new.git", ForgeID: 12},
	})
	if err != nil || res.Added != 0 || len(res.Conflicts) != 1 || res.Conflicts[0] != "UI" {
		t.Fatalf("a new repository under a former slug: %+v %v", res, err)
	}
	if repos, _ := s.SourceRepos(ctx, "SHOP"); len(repos) != 1 {
		t.Errorf("the conflicting repository was added: %+v", repos)
	}
}

// A repository deleted in Bitbucket and created again under its slug (a new
// forge id) is the same repository here, as a hand-entered slug added again:
// it takes the new id instead of being left out for good.
func TestApplyListing_SlugTakenAgainInBitbucket(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if err := s.CreateSource(ctx, bitbucketSource("SHOP")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyListing(ctx, "SHOP", []ListedRepo{{Slug: "svc", CloneURL: "u", ForgeID: 10}}); err != nil {
		t.Fatal(err)
	}
	res, err := s.ApplyListing(ctx, "SHOP", []ListedRepo{{Slug: "svc", CloneURL: "u2", ForgeID: 42}})
	if err != nil || res.Added != 0 || res.Missing != 0 || len(res.Conflicts) != 0 {
		t.Fatalf("listing with svc under a new id: %+v %v", res, err)
	}
	if n := count(t, s, "repos", "slug = 'svc' AND forge_id = 42"); n != 1 {
		t.Errorf("svc did not take the new forge id")
	}
}

// A repository renamed in Bitbucket onto a slug another repository had is
// left out of the rename, but not marked missing: Bitbucket still lists it.
func TestApplyListing_RenameOntoAnothersSlugStaysListed(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if err := s.CreateSource(ctx, bitbucketSource("SHOP")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyListing(ctx, "SHOP", []ListedRepo{{Slug: "old", CloneURL: "u", ForgeID: 7}, {Slug: "new", CloneURL: "n", ForgeID: 8}}); err != nil {
		t.Fatal(err)
	}
	// new (8) is deleted in Bitbucket, old (7) renamed onto its slug.
	res, err := s.ApplyListing(ctx, "SHOP", []ListedRepo{{Slug: "new", CloneURL: "u", ForgeID: 7}})
	if err != nil || len(res.Conflicts) != 1 || res.Missing != 1 {
		t.Fatalf("listing: %+v %v; want one conflict, only the deleted one missing", res, err)
	}
	r, ok, _ := s.SourceRepoByName(ctx, "SHOP", "old")
	if !ok || r.Slug != "old" || r.State == RepoMissing {
		t.Errorf("old after the refused rename: %+v %v", r, ok)
	}
}

// A note or a revision under a key in another case lands on the project
// that has it: a name in any case belongs to one project. A claim
// registers nothing.
func TestRegistration_CaseVariantsAndClaims(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	ingest(t, s, noteFor("ACME", "svc", 1))
	n := noteFor("acme", "SVC", 2)
	ok, err := s.Ingest(ctx, deliveryFor(n), n, callFor(n), []Claim{{ProjectKey: "OTHER", RepoSlug: "x"}, {ProjectKey: "OTHER"}})
	if err != nil || !ok {
		t.Fatalf("ingest: %v %v", ok, err)
	}
	if got := count(t, s, "projects", ""); got != 1 {
		t.Errorf("projects = %d, want 1 (acme is ACME, a claim registers nothing)", got)
	}
	if got := count(t, s, "repos", ""); got != 1 {
		t.Errorf("repos = %d, want 1 (SVC is svc)", got)
	}
	notes, err := s.Notes(ctx, "ACME", "svc", 10)
	if err != nil || len(notes) != 2 {
		t.Errorf("notes of ACME/svc: %+v %v; want both", notes, err)
	}
	if got := count(t, s, "claims", "project_key = 'OTHER'"); got != 2 {
		t.Errorf("claims kept as sent = %d, want 2", got)
	}
}

// A repository without a forge id (a note brought it first) takes the one
// the first listing that names it by its slug gives.
func TestApplyListing_BackfillsForgeIDBySlug(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if err := s.CreateSource(ctx, bitbucketSource("SHOP")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyListing(ctx, "SHOP", []ListedRepo{{Slug: "ui", CloneURL: "u"}}); err != nil {
		t.Fatal(err)
	}
	if n := count(t, s, "repos", "slug = 'ui' AND forge_id IS NULL"); n != 1 {
		t.Fatalf("ui without a forge id = %d, want 1", n)
	}
	res, err := s.ApplyListing(ctx, "SHOP", []ListedRepo{{Slug: "ui", CloneURL: "u", ForgeID: 11}})
	if err != nil || res.Added != 0 || res.Renamed != 0 {
		t.Fatalf("listing with a forge id: %+v %v", res, err)
	}
	if n := count(t, s, "repos", "slug = 'ui' AND forge_id = 11"); n != 1 {
		t.Errorf("ui's forge id not recorded")
	}
}
