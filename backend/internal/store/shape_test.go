package store

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/trick77/almanach/internal/manifest"
)

func shapeOf(t *testing.T, s *Store, key string) string {
	t.Helper()
	p, ok, err := s.CurrentPage(context.Background(), ScopeProject, key, "")
	if err != nil || !ok {
		t.Fatalf("project page %s: %v %v", key, ok, err)
	}
	return p.Shape
}

func projectRevision(t *testing.T, s *Store, key, shape string) {
	t.Helper()
	if _, err := s.InsertRevision(context.Background(), NewRevision{Scope: ScopeProject, ProjectKey: key, Summary: "Werkzeuge.",
		Statements: []Statement{}, SourceKind: FromBaseline, SourceCommit: "c0ffee", Shape: shape}); err != nil {
		t.Fatal(err)
	}
}

// A project revision's shape is its writer's judgment, or the page's carried
// on when it judged none (a note, a map, a departure); a repository
// revision holds none, by the column's check too.
func TestRevisionShape_CarriedForward(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	projectRevision(t, s, "tools", "")
	if got := shapeOf(t, s, "tools"); got != "" {
		t.Fatalf("unjudged page shape = %q", got)
	}
	projectRevision(t, s, "tools", ShapeCollection)
	projectRevision(t, s, "tools", "")
	if got := shapeOf(t, s, "tools"); got != ShapeCollection {
		t.Fatalf("carried shape = %q, want the collection judged before", got)
	}
	projectRevision(t, s, "tools", ShapeSystem)
	if got := shapeOf(t, s, "tools"); got != ShapeSystem {
		t.Fatalf("judged shape = %q", got)
	}
	// Another project's page carries nothing over.
	projectRevision(t, s, "apps", "")
	if got := shapeOf(t, s, "apps"); got != "" {
		t.Fatalf("a new project took another's shape: %q", got)
	}
	if _, err := s.InsertRevision(ctx, NewRevision{Scope: ScopeRepository, ProjectKey: "tools", RepoSlug: "svc", Summary: "Dienst.",
		Statements: []Statement{}, SourceKind: FromBaseline, SourceCommit: "c0ffee", Shape: ShapeSystem}); err != nil {
		t.Fatal(err)
	}
	repo, _, err := s.CurrentPage(ctx, ScopeRepository, "tools", "svc")
	if err != nil || repo.Shape != "" {
		t.Fatalf("repository page shape = %q %v", repo.Shape, err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE revisions SET shape = 'system' WHERE scope = 'repository'`); err == nil {
		t.Fatal("a repository revision took a shape")
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE revisions SET shape = 'heap' WHERE scope = 'project'`); err == nil {
		t.Fatal("a shape off the list was stored")
	}
}

// A first judgment that changes nothing but the label is set in place, on
// an unjudged current project revision only.
func TestJudgeShape(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	projectRevision(t, s, "tools", "")
	p, _, _ := s.CurrentPage(ctx, ScopeProject, "tools", "")
	if ok, err := s.JudgeShape(ctx, p.RevisionID, ShapeSystem); err != nil || !ok {
		t.Fatalf("JudgeShape = %v %v", ok, err)
	}
	after, _, _ := s.CurrentPage(ctx, ScopeProject, "tools", "")
	if after.Shape != ShapeSystem || after.Revision != p.Revision {
		t.Fatalf("page = revision %d shape %q, want revision %d judged in place", after.Revision, after.Shape, p.Revision)
	}
	// Judged already, or no longer current: nothing written.
	if ok, _ := s.JudgeShape(ctx, p.RevisionID, ShapeCollection); ok {
		t.Fatal("a judged revision was judged again in place")
	}
	projectRevision(t, s, "apps", "")
	old, _, _ := s.CurrentPage(ctx, ScopeProject, "apps", "")
	projectRevision(t, s, "apps", "")
	if ok, _ := s.JudgeShape(ctx, old.RevisionID, ShapeSystem); ok {
		t.Fatal("a revision no longer current was judged")
	}
}

// A project's inner edges are its repositories building on each other's
// code; a use of another project's repository is no part of them.
func TestInnerEdges(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	for _, k := range []string{"apps", "libs"} {
		if err := s.CreateSource(ctx, staticSource(k)); err != nil {
			t.Fatal(err)
		}
	}
	addRepos(t, s,
		SourceRepo{ProjectKey: "apps", Slug: "web", CloneURL: "https://forge.example.invalid/x/web"},
		SourceRepo{ProjectKey: "apps", Slug: "kit", CloneURL: "https://forge.example.invalid/x/kit"},
		SourceRepo{ProjectKey: "libs", Slug: "wire", CloneURL: "https://forge.example.invalid/x/wire"},
	)
	saveRead(t, s, "apps", "kit", "1", manifest.Read{Provides: []string{"npm:kit"}})
	saveRead(t, s, "libs", "wire", "1", manifest.Read{Provides: []string{"go:example.invalid/wire"}})
	saveRead(t, s, "apps", "web", "1", manifest.Read{Requires: []manifest.Dep{{Coord: "npm:kit"}, {Coord: "go:example.invalid/wire"}}})
	edges, err := s.InnerEdges(ctx, "apps")
	if err != nil {
		t.Fatal(err)
	}
	want := []Edge{{From: RepoRef{"apps", "web"}, To: RepoRef{"apps", "kit"}, Coords: []string{"npm:kit"}}}
	if !reflect.DeepEqual(edges, want) {
		t.Fatalf("inner edges = %+v, want %+v", edges, want)
	}
	if edges, err := s.InnerEdges(ctx, "libs"); err != nil || len(edges) != 0 {
		t.Fatalf("libs inner edges = %+v %v, want none", edges, err)
	}
}

// Each scope's page is due below its own epoch: a project bump queues the
// project pages alone, the repository pages restyled at the older one
// standing.
func TestRestyleState_EpochPerScope(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	sourceRepo(t, s, "tools", "svc")
	sourceRepo(t, s, "tools", "web")
	projectPage(t, s, "tools")
	if _, err := s.db.ExecContext(ctx, `UPDATE source_repos SET restyle_epoch = 1`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE projects SET restyle_epoch = 1`); err != nil {
		t.Fatal(err)
	}
	st, err := s.RestyleState(ctx, "tools", "svc", Epochs{Repo: 1, Project: 2}, time.Now())
	if err != nil || st.RepoDue || !st.ProjectDue {
		t.Fatalf("state = %+v %v, want the project page alone due", st, err)
	}
	if (Epochs{Repo: 1, Project: 2}).Run() != 2 || (Epochs{Repo: 3, Project: 2}).Run() != 3 {
		t.Fatal("Run is not the newer epoch")
	}
	// Failures count at the run's epoch: one at the old epoch spends nothing.
	r := restyleRun(t, s, "tools", "svc", 1)
	r.Step = RestyleProjectStep
	finish(t, s, r, RunFailed)
	for range RestyleTries - 1 {
		r := restyleRun(t, s, "tools", "web", 2)
		r.Step = RestyleProjectStep
		finish(t, s, r, RunFailed)
	}
	if st, _ := s.RestyleState(ctx, "tools", "svc", Epochs{Repo: 1, Project: 2}, time.Now().Add(time.Hour)); !st.ProjectDue {
		t.Fatalf("state = %+v: a failure at another epoch spent a try", st)
	}
}

// A restyle step that writes no revision sets a first judgment in place,
// with the run and the epoch.
func TestApplyRestyle_JudgesInPlace(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	sourceRepo(t, s, "tools", "svc")
	sourceRepo(t, s, "tools", "web")
	projectRevision(t, s, "tools", "")
	p, _, _ := s.CurrentPage(ctx, ScopeProject, "tools", "")
	r := restyleRun(t, s, "tools", "svc", 2)
	r.State = RunProject
	if _, ok, err := s.ApplyRestyle(ctx, r, ScopeProject, 2, Restyled{JudgeID: p.RevisionID, Shape: ShapeSystem}); err != nil || !ok {
		t.Fatalf("ApplyRestyle = %v %v", ok, err)
	}
	if got := shapeOf(t, s, "tools"); got != ShapeSystem {
		t.Fatalf("shape = %q, want judged in place", got)
	}
	if _, project, _ := epochs(s, "svc"); project != 2 {
		t.Fatalf("project epoch = %d", project)
	}
}
