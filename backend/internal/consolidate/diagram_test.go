package consolidate

import (
	"context"
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/trick77/almanach/internal/baseline"
	"github.com/trick77/almanach/internal/diagram"
	"github.com/trick77/almanach/internal/gitrepo"
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
	// null keeps the diagram: carried by id in code, never lost alone.
	if st := keep(nil); len(st.revisions) != 0 {
		t.Errorf("diagram null: revisions = %+v, want none, the diagram kept", st.revisions)
	}
}

// null keeps a stored diagram on the merge path while the text changes;
// the statement's diagram goes only with the statement.
func TestFoldNullKeepsTheDiagram(t *testing.T) {
	page := existingRepoPage()
	page.Statements[0].Diagram = seq("schreibt")
	st := newFakeStore()
	st.pages[pageKey(store.ScopeRepository, "PLAT", "files")] = page
	s := page.Statements[0]
	m := &fakeMerger{result: okResult(&inference.Description{Summary: page.Summary, Statements: []inference.Statement{
		{ID: str(s.ID), Kind: s.Kind, Name: s.Name, Text: "Nimmt Dateien an, neu.", Files: s.Files},
	}}, nil)}
	newWorker(st, m).Fold(context.Background(), queued("PLAT/files#42"))
	if len(st.revisions) != 1 || !diagram.Equal(st.revisions[0].Statements[0].Diagram, seq("schreibt")) {
		t.Fatalf("revisions = %+v, want the text changed and the diagram kept", st.revisions)
	}
}

// A cap lowered below what a page holds does not freeze it: a stored
// diagram over today's node cap is kept, echoed or not, and a page over
// today's count may keep its count but not grow it.
func TestFoldLoweredCaps(t *testing.T) {
	big := &diagram.Diagram{Kind: diagram.KindFlow, Direction: "TB", Edges: []diagram.Edge{}}
	for _, id := range []string{"a", "b", "c", "d", "e", "f"} {
		big.Nodes = append(big.Nodes, diagram.Node{ID: id, Label: id, Shape: "step"})
	}
	page := existingRepoPage()
	page.Statements[0].Diagram = big
	page.Statements = append(page.Statements, store.Statement{ID: "s2", Kind: "rule", Name: "R", Text: "T", Files: []string{}, Diagram: seq("x")})
	run := func(statements []inference.Statement) *fakeStore {
		st := newFakeStore()
		st.pages[pageKey(store.ScopeRepository, "PLAT", "files")] = page
		m := &fakeMerger{result: okResult(&inference.Description{Summary: "Neu formuliert.", Statements: statements}, nil)}
		// limits: 4 nodes, 1 diagram a page; the page holds 6 nodes and 2.
		newWorker(st, m).Fold(context.Background(), queued("PLAT/files#42"))
		return st
	}
	echo := func(s store.Statement, d *diagram.Diagram) inference.Statement {
		return inference.Statement{ID: str(s.ID), Kind: s.Kind, Name: s.Name, Text: s.Text, Files: s.Files, Diagram: d}
	}
	s1, s2 := page.Statements[0], page.Statements[1]
	if st := run([]inference.Statement{echo(s1, big), echo(s2, nil)}); len(st.revisions) != 1 {
		t.Errorf("echoed over-cap diagram: revisions = %+v, want the rewrite taken", st.revisions)
	}
	changed := *big
	changed.Nodes = append(slices.Clone(big.Nodes[:5]), diagram.Node{ID: "g", Label: "g", Shape: "step"})
	if st := run([]inference.Statement{echo(s1, &changed), echo(s2, nil)}); len(st.revisions) != 0 {
		t.Errorf("changed over-cap diagram: revisions = %+v, want rejected", st.revisions)
	}
	third := inference.Statement{Kind: "rule", Name: "R3", Text: "T", Diagram: seq("y")}
	if st := run([]inference.Statement{echo(s1, nil), echo(s2, nil), third}); len(st.revisions) != 0 {
		t.Errorf("a third diagram: revisions = %+v, want rejected, the page may not grow", st.revisions)
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

// The prompts show each diagram as JSON, for the model to read and correct.
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
		// The shared rules, once, their caps filled in.
		if !strings.Contains(out, "At most 4 nodes") || strings.Count(out, "## Diagrams") != 1 {
			t.Errorf("%s prompt lacks the diagram rules or has them twice", name)
		}
	}
}

// A quiet reconcile of a project described before maps existed still runs
// the project step, so the map gets drawn; with a map it ends at once.
func TestRun_QuietReconcileDrawsAMissingMap(t *testing.T) {
	quiet := func(drawn bool) world {
		x := newWorld(t, 16)
		if !drawn {
			x.st.pages[pageKey(store.ScopeProject, "tools", "")] = store.Page{RevisionID: 1, Revision: 1, Summary: "Werkzeuge.", Statements: []store.Statement{}}
		}
		plan, _ := json.Marshal(runPlan{Plan: baseline.Plan{Subjects: []baseline.Subject{{Name: "A", Purpose: "p",
			Files: []baseline.File{{Path: "auth/login.go", Size: 30}}}}}})
		x.st.documented = &store.BaselineRun{Commit: older40, Plan: plan, State: store.RunDone}
		x.st.pages[pageKey(store.ScopeRepository, "tools", "svc")] = store.Page{RevisionID: 1, Revision: 1, Summary: "Ein Dienst.", Statements: []store.Statement{}}
		x.st.others = []store.RepoSummary{{Slug: "svc"}}
		x.code.diff = []gitrepo.Entry{{Path: "go.sum", Mode: "100644", Size: 6}}
		x.queue(store.RunReconcile, older40)
		x.models.project = inference.DescriptionResult{Call: called, Description: &inference.Description{Summary: "Werkzeuge.",
			Statements: []inference.Statement{}, Map: drawnMap("svc")}}
		x.w.drain(context.Background())
		return x
	}
	if x := quiet(true); x.run().State != store.RunDone || len(x.st.calls) != 0 {
		t.Errorf("with a map: run %+v, %d calls; want done without one", x.run(), len(x.st.calls))
	}
	x := quiet(false)
	if x.run().State != store.RunDone || len(x.st.calls) != 1 || x.st.calls[0].Purpose != store.PurposeProject {
		t.Fatalf("without a map: run %+v, calls %+v; want the project step", x.run(), x.st.calls)
	}
	if p := x.st.pages[pageKey(store.ScopeProject, "tools", "")]; !diagram.Equal(p.Map, drawnMap("svc")) {
		t.Errorf("project map = %+v", p.Map)
	}
}

// null keeps a stored diagram on the baseline paths too: a step's edit and
// the project step's rewrite carry it by id.
func TestRun_NullKeepsTheDiagram(t *testing.T) {
	x := newWorld(t, 16)
	src := []store.Source{{Kind: store.FromBaseline, Commit: older40, Repo: "svc"}}
	x.st.pages[pageKey(store.ScopeRepository, "tools", "svc")] = store.Page{RevisionID: 1, Revision: 1, Summary: "Ein Dienst.",
		Statements: []store.Statement{{ID: "s1", Kind: "component", Name: "Dienst", Text: "Alt.", Files: []string{"README.md"}, Sources: src, Diagram: seq("ruft")}}}
	x.st.pages[pageKey(store.ScopeProject, "tools", "")] = store.Page{RevisionID: 1, Revision: 1, Summary: "Werkzeuge.", Map: drawnMap("svc"),
		Statements: []store.Statement{{ID: "s1", Kind: "entity", Name: "Werkzeug", Text: "Alt.", Files: []string{}, Sources: src, Diagram: seq("liest")}}}
	x.st.others = []store.RepoSummary{{Slug: "svc"}}
	x.queue(store.RunFull, "")
	x.models.catalogue = []inference.CatalogueResult{{Call: called, Catalogue: inference.Catalogue{Subjects: []inference.Subject{}}}}
	x.models.patches = []inference.PatchResult{{Call: called, Patch: inference.Patch{Summary: ref("Ein Dienst, neu."),
		Statements: []inference.Statement{stmt(ref("s1"), "component", "Dienst", "Neu.", "README.md")}}}}
	x.models.project = inference.DescriptionResult{Call: called, Description: &inference.Description{Summary: "Werkzeuge.",
		Statements: []inference.Statement{stmt(ref("s1"), "entity", "Werkzeug", "Neu.")}, Map: drawnMap("svc")}}
	x.w.drain(context.Background())
	repo := x.st.pages[pageKey(store.ScopeRepository, "tools", "svc")].Statements[0]
	if repo.Text != "Neu." || !diagram.Equal(repo.Diagram, seq("ruft")) {
		t.Errorf("repository statement = %+v, want the text changed and the diagram kept", repo)
	}
	project := x.st.pages[pageKey(store.ScopeProject, "tools", "")].Statements[0]
	if project.Text != "Neu." || !diagram.Equal(project.Diagram, seq("liest")) {
		t.Errorf("project statement = %+v, want the text changed and the diagram kept", project)
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
