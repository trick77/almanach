package consolidate

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/trick77/almanach/internal/diagram"
	"github.com/trick77/almanach/internal/inference"
	"github.com/trick77/almanach/internal/prompts"
	"github.com/trick77/almanach/internal/store"
)

// seq is a statement diagram: two parts and the call between them.
func seq(label string) *diagram.Diagram {
	return &diagram.Diagram{Kind: diagram.KindSequence,
		Actors: []diagram.Actor{{ID: "api", Label: "API"}, {ID: "db", Label: "Datenbank"}},
		Rows:   []diagram.Row{{Type: diagram.RowMessage, From: "api", To: "db", Label: label, Line: "solid", Head: "filled"}}}
}

// A statement's diagram is stored with it, its labels Swiss; a diagram
// that breaks a rule, or one too many on the page, rejects the rewrite
// whole.
func TestFoldStatementDiagram(t *testing.T) {
	st := newFakeStore()
	m := &fakeMerger{result: okResult(&inference.Description{Summary: "Der Dienst.", Statements: []inference.Statement{
		{Kind: "component", Name: "Ablage", Text: "Legt ab.", Diagram: seq("  Größe prüfen ")},
	}}, nil)}
	newWorker(st, m).Fold(context.Background(), queued("PLAT/files#42"))
	if len(st.revisions) != 1 {
		t.Fatalf("revisions = %+v", st.revisions)
	}
	if d := st.revisions[0].Statements[0].Diagram; d == nil || d.Rows[0].Label != "Grösse prüfen" {
		t.Errorf("diagram = %+v", d)
	}

	dangling := seq("x")
	dangling.Rows[0].To = "queue"
	for name, statements := range map[string][]inference.Statement{
		"dangling end": {{Kind: "rule", Name: "R", Text: "T", Diagram: dangling}},
		"over the page's cap": {
			{Kind: "rule", Name: "R1", Text: "T", Diagram: seq("a")},
			{Kind: "rule", Name: "R2", Text: "T", Diagram: seq("b")},
		},
	} {
		st := newFakeStore()
		m := &fakeMerger{result: okResult(&inference.Description{Summary: "Der Dienst.", Statements: statements}, nil)}
		newWorker(st, m).Fold(context.Background(), queued("PLAT/files#42"))
		if len(st.revisions) != 0 {
			t.Errorf("%s: revisions = %+v, want the rewrite rejected", name, st.revisions)
		}
	}
}

// A diagram change is a change: a revision, the PR appended to the
// statement's sources; the same diagram with the same text is none.
func TestFoldDiagramChangeIsARevision(t *testing.T) {
	page := existingRepoPage()
	page.Statements[0].Diagram = seq("schreibt")
	keep := func(d *diagram.Diagram) *fakeStore {
		st := newFakeStore()
		st.pages[pageKey(store.ScopeRepository, "PLAT", "files")] = page
		s := page.Statements[0]
		m := &fakeMerger{result: okResult(&inference.Description{Summary: page.Summary, Statements: []inference.Statement{
			{ID: str(s.ID), Kind: s.Kind, Name: s.Name, Text: s.Text, Files: s.Files, Diagram: d},
		}}, nil)}
		newWorker(st, m).Fold(context.Background(), queued("PLAT/files#42"))
		return st
	}
	if st := keep(seq("schreibt")); len(st.revisions) != 0 {
		t.Errorf("same diagram: revisions = %+v, want none", st.revisions)
	}
	st := keep(seq("liest"))
	if len(st.revisions) != 1 {
		t.Fatalf("changed diagram: revisions = %+v, want one", st.revisions)
	}
	if s := st.revisions[0].Statements[0]; len(s.Sources) != 2 || s.Sources[1].PRKey != "PLAT/files#42" {
		t.Errorf("sources = %+v, want the PR appended", s.Sources)
	}
	if st := keep(nil); len(st.revisions) != 1 || st.revisions[0].Statements[0].Diagram != nil {
		t.Errorf("diagram left out: revisions = %+v, want one without it", st.revisions)
	}
}

// The merge never draws the map: a project revision from a note carries the
// current one forward, a repository revision has none.
func TestFoldCarriesTheMapForward(t *testing.T) {
	st := newFakeStore()
	current := drawnMap("files")
	st.pages[pageKey(store.ScopeProject, "PLAT", "")] = store.Page{RevisionID: 3, Revision: 1, Summary: "Alt.", Map: current}
	m := &fakeMerger{result: okResult(
		&inference.Description{Summary: "Repo.", Statements: []inference.Statement{}},
		&inference.Description{Summary: "Neu.", Statements: []inference.Statement{}},
	)}
	newWorker(st, m).Fold(context.Background(), queued("PLAT/files#42"))
	if len(st.revisions) != 2 {
		t.Fatalf("revisions = %+v", st.revisions)
	}
	if st.revisions[0].Map != nil {
		t.Errorf("repository revision map = %+v", st.revisions[0].Map)
	}
	if !diagram.Equal(st.revisions[1].Map, current) {
		t.Errorf("project revision map = %+v, want the current one", st.revisions[1].Map)
	}
}

// The prompts show each diagram as JSON, so keeping it means repeating it.
func TestPromptsShowDiagrams(t *testing.T) {
	p := existingRepoPage()
	p.Statements[0].Diagram = seq("schreibt")
	want := `Diagram: {"kind":"sequence","actors":[{"id":"api","label":"API"},{"id":"db","label":"Datenbank"}],"rows":[{"type":"message","from":"api","to":"db","label":"schreibt","line":"solid","head":"filled"}]}`
	if got := describe(p); !strings.Contains(got, want) {
		t.Errorf("describe = %s", got)
	}
	if got := describeSourced(p, nil); !strings.Contains(got, want) {
		t.Errorf("describeSourced = %s", got)
	}
	if got := projectMap(store.Page{}); !strings.Contains(got, "None yet") {
		t.Errorf("projectMap without a map = %q", got)
	}
}

// Every placeholder of the baseline prompts is filled.
func TestBaselinePromptsFilled(t *testing.T) {
	r := store.BaselineRun{ProjectKey: "PLAT", Slug: "files", Commit: head}
	left := regexp.MustCompile(`\{[a-z_]+\}`)
	for name, out := range map[string]string{
		"describe": renderDescribe(r, existingRepoPage(), limits, 1, 2, 1, "the overview", "", "_None._", nil),
		"project":  renderProject(r, existingRepoPage(), store.Page{Map: drawnMap("files")}, "_None._", []string{"files", "portal"}, limits),
		"merge":    render(prompts.Merge, queued("PLAT/files#42"), existingRepoPage(), store.Page{}, limits),
	} {
		if ph := left.FindAllString(out, -1); len(ph) > 0 {
			t.Errorf("%s prompt: placeholders left: %v", name, ph)
		}
		if !strings.Contains(out, "at most 4 nodes") {
			t.Errorf("%s prompt lacks the diagram caps", name)
		}
	}
}

// A project without a map is not left as it is: an answer of no change is
// the step skipped, redone at the next run, and a rewrite draws the map.
func TestRun_ProjectWithoutAMap(t *testing.T) {
	x := newWorld(t, 16)
	delete(x.st.pages, pageKey(store.ScopeProject, "tools", ""))
	x.queue(store.RunFull, "")
	x.models.catalogue = []inference.CatalogueResult{{Call: called, Catalogue: inference.Catalogue{Subjects: []inference.Subject{}}}}
	x.models.patches = []inference.PatchResult{{Call: called, Patch: inference.Patch{Summary: ref("Ein Dienst."), Statements: []inference.Statement{}}}}
	x.models.project = inference.DescriptionResult{Call: called}
	x.w.drain(context.Background())
	if r := x.run(); r.State != store.RunDone || r.Skipped != 1 || !strings.Contains(string(r.Plan), "the project") {
		t.Fatalf("run = %+v, want the project skipped", r)
	}
	if _, ok := x.st.pages[pageKey(store.ScopeProject, "tools", "")]; ok {
		t.Error("a project page was written")
	}
}

// The map is checked like a statement: an invented slug or a dangling edge
// refuses the rewrite and the project stays; a changed map alone is a
// revision, the same map with the same text none.
func TestRun_ProjectMap(t *testing.T) {
	project := func(m *diagram.Diagram) world {
		x := newWorld(t, 16)
		x.st.others = []store.RepoSummary{{Slug: "svc"}, {Slug: "portal"}}
		x.st.pages[pageKey(store.ScopeProject, "tools", "")] = store.Page{RevisionID: 1, Revision: 1, Summary: "Werkzeuge.",
			Statements: []store.Statement{}, Map: drawnMap("svc")}
		x.queue(store.RunFull, "")
		x.models.catalogue = []inference.CatalogueResult{{Call: called, Catalogue: inference.Catalogue{Subjects: []inference.Subject{}}}}
		x.models.patches = []inference.PatchResult{{Call: called, Patch: inference.Patch{Summary: ref("Ein Dienst."), Statements: []inference.Statement{}}}}
		x.models.project = inference.DescriptionResult{Call: called, Description: &inference.Description{Summary: "Werkzeuge.",
			Statements: []inference.Statement{}, Map: m}}
		x.w.drain(context.Background())
		return x
	}
	projectRevisions := func(x world) []store.NewRevision {
		var out []store.NewRevision
		for _, r := range x.st.revisions {
			if r.Scope == store.ScopeProject {
				out = append(out, r)
			}
		}
		return out
	}

	invented := drawnMap("svc", "billing")
	dangling := drawnMap("svc")
	dangling.Edges = append(dangling.Edges, diagram.Edge{From: "svc", To: "nowhere", Label: "HTTP", Head: "end"})
	for name, m := range map[string]*diagram.Diagram{"invented slug": invented, "dangling edge": dangling} {
		x := project(m)
		if revs := projectRevisions(x); len(revs) != 0 || x.run().Skipped != 1 {
			t.Errorf("%s: project revisions %+v, skipped %d; want refused", name, revs, x.run().Skipped)
		}
	}

	if revs := projectRevisions(project(drawnMap("svc"))); len(revs) != 0 {
		t.Errorf("same map, same text: revisions = %+v, want none", revs)
	}
	revs := projectRevisions(project(drawnMap("svc", "portal")))
	if len(revs) != 1 || len(revs[0].Map.Nodes) != 4 {
		t.Fatalf("changed map: revisions = %+v, want one with the new map", revs)
	}
}
