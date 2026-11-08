package store

import (
	"context"
	"strings"
	"testing"

	"github.com/trick77/almanach/internal/diagram"
)

// Respell rewrites the prose of every revision, the history's too, and its
// maps; a repository map drawn from the text stays drawn from it, so no run
// draws it again for spelling; a second pass finds nothing to do.
func TestRespell(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	sourceRepo(t, s, "PRJ", "svc")
	f := strings.NewReplacer("Dienst", "Service").Replace
	st := func(text string) []Statement {
		return []Statement{{ID: "s1", Kind: "component", Name: "Dienst", Text: text, Area: "Dienst und Daten",
			Diagram: &diagram.Diagram{Kind: diagram.KindFlow, Direction: "LR", Nodes: []diagram.Node{{ID: "a", Label: "Dienst", Shape: "step"}}}}}
	}
	for _, text := range []string{"Der Dienst rechnet.", "Der Dienst rechnet monatlich."} {
		if _, err := s.InsertRevision(ctx, NewRevision{Scope: ScopeRepository, ProjectKey: "PRJ", RepoSlug: "svc",
			Summary: "Ein Dienst.", Statements: st(text), SourceKind: FromBaseline, SourceCommit: "abc"}); err != nil {
			t.Fatal(err)
		}
	}
	page, _, err := s.CurrentPage(ctx, ScopeRepository, "PRJ", "svc")
	if err != nil {
		t.Fatal(err)
	}
	r := enqueue(t, s, "PRJ", "svc")
	r.State = RunDone
	m := &diagram.Diagram{Kind: diagram.KindFlow, Direction: "LR", Nodes: []diagram.Node{{ID: "api", Label: "Dienst-API", Shape: "step", Zone: "entry"}},
		Zones: []diagram.Zone{{ID: "entry", Title: "Eingang"}}}
	if ok, err := s.SetRepoMap(ctx, "PRJ", "svc", m, page.MapBasis(), r); err != nil || !ok {
		t.Fatalf("SetRepoMap: ok=%v err=%v", ok, err)
	}

	n, err := s.Respell(ctx, f)
	if err != nil || n != 2 {
		t.Fatalf("Respell = %d, %v; want both revisions", n, err)
	}
	p, _, _ := s.CurrentPage(ctx, ScopeRepository, "PRJ", "svc")
	got := p.Statements[0]
	if p.Summary != "Ein Service." || got.Name != "Service" || got.Text != "Der Service rechnet monatlich." || got.Area != "Service und Daten" ||
		got.Diagram.Nodes[0].Label != "Service" || p.RepoMap.Nodes[0].Label != "Service-API" {
		t.Errorf("current page = %+v, statement %+v", p, got)
	}
	if p.Revision != 2 || len(got.Sources) != len(page.Statements[0].Sources) {
		t.Errorf("respelling wrote a revision or touched provenance: revision %d, sources %+v", p.Revision, got.Sources)
	}
	if p.RepoMapBasis == "" || p.RepoMapBasis != p.MapBasis() {
		t.Errorf("the map is no longer current: basis %q, page %q", p.RepoMapBasis, p.MapBasis())
	}
	old, _, _ := s.Revision(ctx, ScopeRepository, "PRJ", "svc", 1)
	if old.Statements[0].Text != "Der Service rechnet." {
		t.Errorf("history not respelled: %q", old.Statements[0].Text)
	}
	if n, err := s.Respell(ctx, f); err != nil || n != 0 {
		t.Errorf("second Respell = %d, %v; want nothing left", n, err)
	}
}
