package consolidate

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/trick77/almanach/internal/inference"
	"github.com/trick77/almanach/internal/prompts"
	"github.com/trick77/almanach/internal/store"
)

// shapeWorld is a full run of svc whose project step answers res, over a
// project page with a component, a PR-taught rule and its map.
func shapeWorld(t *testing.T, page store.Page, res inference.DescriptionResult) world {
	t.Helper()
	x := newWorld(t, 16)
	x.queue(store.RunFull, "")
	x.st.pages[pageKey(store.ScopeProject, "tools", "")] = page
	x.models.catalogue = []inference.CatalogueResult{{Call: called, Catalogue: inference.Catalogue{Subjects: []inference.Subject{}}}}
	x.models.patches = []inference.PatchResult{{Call: called, Patch: inference.Patch{Summary: ref("Ein Service."), Statements: []inference.Statement{}}}}
	res.Call = called
	x.models.project = res
	return x
}

func systemPage(shape string) store.Page {
	return store.Page{Scope: store.ScopeProject, RevisionID: 1, Revision: 1, Summary: "Das System, lang und breit.", Shape: shape,
		Statements: []store.Statement{
			{ID: "s1", Kind: "component", Name: "Kern", Text: "Wie svc innen arbeitet.", Sources: []store.Source{svcRun}},
			{ID: "s2", Kind: "rule", Name: "Regel", Text: "Gilt über alles.", Sources: []store.Source{svcPR}},
		},
		Map: drawnMap("svc")}
}

// A project judged a collection keeps its summary alone: every statement
// off, the answer's too, no map; the prompt said what the page was and
// what the repositories build on. A quiet reconcile after it asks for no
// map.
func TestProjectStep_JudgesACollection(t *testing.T) {
	x := shapeWorld(t, systemPage(""), inference.DescriptionResult{
		Shape: inference.Shape{Kind: store.ShapeCollection, Reason: "Libraries für sich."},
		Description: &inference.Description{Summary: "Die Libraries des Teams.", Statements: []inference.Statement{stmt(ref("s2"), "rule", "Regel", "Neu.")},
			Added: []inference.Statement{stmt(nil, "interface", "API", "Neu.")}, Map: drawnMap("svc")}})
	x.st.inner = map[string][]store.Edge{"tools": {{From: store.RepoRef{ProjectKey: "tools", Slug: "web"}, To: store.RepoRef{ProjectKey: "tools", Slug: "svc"},
		Coords: []string{"npm:svc"}}}}
	x.w.drain(context.Background())

	page := x.projectPage()
	if page.Shape != store.ShapeCollection || page.Summary != "Die Libraries des Teams." || len(page.Statements) != 0 || page.Map != nil {
		t.Fatalf("project page = %+v, want the collection's summary alone", page)
	}
	var prompt string
	for _, p := range x.models.prompts {
		if strings.Contains(p, "## The project's shape") {
			prompt = p
		}
	}
	if !strings.Contains(prompt, "_Not judged yet") || !strings.Contains(prompt, "- `web` builds on `svc` (npm:svc)") {
		t.Errorf("project prompt lacks the shape or the inner edges:\n%s", prompt)
	}
	r := store.BaselineRun{ProjectKey: "tools", Slug: "svc", Mode: store.RunReconcile}
	if skip, _, err := x.w.projectStands(context.Background(), &r, runPlan{}, page); err != nil || !skip {
		t.Fatalf("quiet reconcile of a collection: skip=%v err=%v, want it skipped, no map due", skip, err)
	}
	// Judged a system again, the map is due at once: none carried over.
	page.Shape = store.ShapeSystem
	if skip, _, _ := x.w.projectStands(context.Background(), &r, runPlan{}, page); skip {
		t.Fatal("a system without a map was not folded for one")
	}
}

// A collection judged a system again draws its map in the same answer;
// one that answers none leaves it NULL, due, never the sentinel.
func TestProjectStep_CollectionBecomesASystem(t *testing.T) {
	collection := store.Page{Scope: store.ScopeProject, RevisionID: 1, Revision: 1, Summary: "Libraries.", Shape: store.ShapeCollection,
		Statements: []store.Statement{}}
	for name, c := range map[string]struct {
		answer  *inference.Description
		mapDrew bool
	}{
		"with a map": {&inference.Description{Summary: "Ein System.", Statements: []inference.Statement{},
			Added: []inference.Statement{stmt(nil, "interface", "API", "svc bietet sie web.")}, Map: drawnMap("svc", "web")}, true},
		"without":   {&inference.Description{Summary: "Ein System.", Statements: []inference.Statement{}}, false},
		"no change": {nil, false},
	} {
		t.Run(name, func(t *testing.T) {
			x := shapeWorld(t, collection, inference.DescriptionResult{Shape: inference.Shape{Kind: store.ShapeSystem}, Description: c.answer})
			x.w.drain(context.Background())
			page := x.projectPage()
			if page.Shape != store.ShapeSystem || (page.Map != nil) != c.mapDrew || page.Revision != 2 {
				t.Fatalf("page = revision %d shape %q map %v, want a system's revision, map drawn %v", page.Revision, page.Shape, page.Map, c.mapDrew)
			}
		})
	}
}

// A page written before shapes, judged a system with no change, is set so
// in place: no revision for a label. A change of shape alone is a revision.
func TestProjectStep_ShapeAloneWrites(t *testing.T) {
	x := shapeWorld(t, systemPage(""), inference.DescriptionResult{Shape: inference.Shape{Kind: store.ShapeSystem}})
	x.w.drain(context.Background())
	if page := x.projectPage(); page.Revision != 1 || page.Shape != store.ShapeSystem || x.st.judged != 1 {
		t.Fatalf("page = revision %d shape %q, judged %d; want revision 1 judged in place", page.Revision, page.Shape, x.st.judged)
	}
	// A system judged a collection with no rewrite would keep a system's
	// summary as the collection's: refused, the page stands, the step
	// skipped so the next reconcile folds the project again.
	x = shapeWorld(t, systemPage(store.ShapeSystem), inference.DescriptionResult{Shape: inference.Shape{Kind: store.ShapeCollection}})
	x.w.drain(context.Background())
	if page := x.projectPage(); page.Revision != 1 || page.Shape != store.ShapeSystem || len(page.Statements) != 2 || x.run().Skipped != 1 {
		t.Fatalf("page = revision %d shape %q, %d statements, run skipped %d; want the page standing, the step skipped",
			page.Revision, page.Shape, len(page.Statements), x.run().Skipped)
	}
	// With a summary of its own it is a collection's revision.
	x = shapeWorld(t, systemPage(store.ShapeSystem), inference.DescriptionResult{Shape: inference.Shape{Kind: store.ShapeCollection},
		Description: &inference.Description{Summary: "Kurz.", Statements: []inference.Statement{}}})
	x.w.drain(context.Background())
	if page := x.projectPage(); page.Revision != 2 || page.Shape != store.ShapeCollection || len(page.Statements) != 0 ||
		page.Summary != "Kurz." || page.Map != nil {
		t.Fatalf("page = %+v, want a collection's revision", page)
	}
	// A first page judged a collection with an empty summary is refused:
	// never a blank page.
	x = shapeWorld(t, store.Page{}, inference.DescriptionResult{Shape: inference.Shape{Kind: store.ShapeCollection},
		Description: &inference.Description{Summary: " ", Statements: []inference.Statement{}}})
	delete(x.st.pages, pageKey(store.ScopeProject, "tools", ""))
	x.w.drain(context.Background())
	if _, ok := x.st.pages[pageKey(store.ScopeProject, "tools", "")]; ok {
		t.Fatal("a blank collection page was written")
	}
	// An answer naming no shape keeps the page's.
	collection := store.Page{Scope: store.ScopeProject, RevisionID: 1, Revision: 1, Summary: "Libraries.", Shape: store.ShapeCollection,
		Statements: []store.Statement{}}
	x = shapeWorld(t, collection, inference.DescriptionResult{})
	x.w.drain(context.Background())
	if page := x.projectPage(); page.Shape != store.ShapeCollection || page.Revision != 1 {
		t.Fatalf("page = revision %d shape %q, want the collection as stored", page.Revision, page.Shape)
	}
}

// A note's fold of a collection's page takes its summary alone, the
// statements returned left out; the prompt says what the page is.
func TestFold_CollectionTakesTheSummaryAlone(t *testing.T) {
	st := newFakeStore()
	st.pages[pageKey(store.ScopeProject, "PLAT", "")] = store.Page{Scope: store.ScopeProject, ProjectKey: "PLAT", RevisionID: 3, Revision: 1,
		Summary: "Libraries.", Shape: store.ShapeCollection, Statements: []store.Statement{}}
	project := &inference.Description{Summary: "Libraries, eine mehr.", Statements: []inference.Statement{
		{Kind: "interface", Name: "API", Text: "Neu.", Files: []string{"api.go"}},
	}}
	m := &fakeMerger{result: okResult(nil, project)}
	newWorker(st, m).Fold(context.Background(), queued("PLAT/files#42"))
	var rev *store.NewRevision
	for i := range st.revisions {
		if st.revisions[i].Scope == store.ScopeProject {
			rev = &st.revisions[i]
		}
	}
	if rev == nil || rev.Summary != "Libraries, eine mehr." || len(rev.Statements) != 0 || rev.Shape != "" {
		t.Fatalf("project revision = %+v, want the summary alone, the shape carried by the store", rev)
	}
	if !strings.Contains(m.prompts[0], collectionProject) {
		t.Error("merge prompt does not say the project is a collection")
	}
}

// The project restyle reads every repository's summary and contracts and
// the inner edges, judges the shape, and drops a repository's detail, a
// PR-taught one too; an interface split out of a component it drops
// carries the component's sources. A repository's restyle drops nothing.
func TestRestyle_ProjectJudgesAndDrops(t *testing.T) {
	x := restyleWorld(t)
	x.st.repoEpoch = prompts.RepoEpoch
	x.st.pages[pageKey(store.ScopeProject, "tools", "")] = systemPage("")
	x.st.others = []store.RepoSummary{{Slug: "svc", Summary: "Der Service."}, {Slug: "web", Summary: "Die Oberfläche."}}
	x.models.restyles = []inference.RestyleResult{{Call: called, Shape: inference.Shape{Kind: store.ShapeSystem, Reason: "web ruft svc."},
		Patch: inference.Patch{Statements: []inference.Statement{},
			Added: []inference.Statement{{Kind: "interface", Name: "API", Text: "svc bietet sie web.", Area: "Schnittstellen", From: []string{"s1"}}},
			Drop:  []string{"s1", "s2"}}}}
	x.w.drain(context.Background())

	if !slices.Equal(x.models.restyledProject, []bool{true}) {
		t.Fatalf("restyle calls = %v, want the project's alone (the repository page at its epoch)", x.models.restyledProject)
	}
	if p := x.models.prompts[0]; !strings.Contains(p, "Der Service.") || !strings.Contains(p, "Die Oberfläche.") || !strings.Contains(p, "_None: no repository") ||
		!strings.Contains(p, "_Not judged yet") {
		t.Errorf("project restyle prompt lacks the summaries, the edges or the shape:\n%s", p)
	}
	page := x.projectPage()
	if page.Shape != store.ShapeSystem || len(page.Statements) != 1 {
		t.Fatalf("project page = %+v, want the interface alone, a system", page)
	}
	if api := page.Statements[0]; api.Name != "API" || !slices.Equal(api.Sources, []store.Source{svcRun}) {
		t.Errorf("interface = %+v, want the component's sources", api)
	}
	if page.Map == nil || x.st.projectEpoch != prompts.Epoch {
		t.Errorf("map %v, project epoch %d", page.Map, x.st.projectEpoch)
	}
}

// A project restyle judging a collection keeps a summary alone; a split
// left out keeps what it came from, never dropped.
func TestRestyle_ProjectCollection(t *testing.T) {
	x := restyleWorld(t)
	x.st.repoEpoch = prompts.RepoEpoch
	x.st.pages[pageKey(store.ScopeProject, "tools", "")] = systemPage(store.ShapeSystem)
	x.models.restyles = []inference.RestyleResult{{Call: called, Shape: inference.Shape{Kind: store.ShapeCollection},
		Patch: inference.Patch{Summary: ref("Zwei Werkzeuge."), Statements: []inference.Statement{}}}}
	x.w.drain(context.Background())
	page := x.projectPage()
	if page.Shape != store.ShapeCollection || page.Summary != "Zwei Werkzeuge." || len(page.Statements) != 0 || page.Map != nil {
		t.Fatalf("project page = %+v, want the collection's summary alone", page)
	}

	x = restyleWorld(t)
	x.st.repoEpoch = prompts.RepoEpoch
	x.st.pages[pageKey(store.ScopeProject, "tools", "")] = systemPage(store.ShapeSystem)
	x.models.restyles = []inference.RestyleResult{{Call: called, Shape: inference.Shape{Kind: store.ShapeSystem},
		Patch: inference.Patch{Statements: []inference.Statement{},
			Added: []inference.Statement{{Kind: "interface", Name: "Erfunden", Text: "Aus nichts.", From: []string{"s9"}},
				{Kind: "interface", Name: "Leer", Text: " ", From: []string{"s1"}}},
			Drop: []string{"s1"}}}}
	x.w.drain(context.Background())
	if got := ids(x.projectPage().Statements); !slices.Equal(got, []string{"s1", "s2"}) {
		t.Fatalf("statements = %v, want s1 kept: what a left-out split came from", got)
	}
}

// A project restyle turning a system into a collection with no summary of
// its own fails the step: the page stands, a try spent, never a system's
// summary kept under a collection.
func TestRestyle_CollectionWithoutASummary(t *testing.T) {
	x := restyleWorld(t)
	x.st.repoEpoch = prompts.RepoEpoch
	x.st.pages[pageKey(store.ScopeProject, "tools", "")] = systemPage(store.ShapeSystem)
	x.models.restyles = []inference.RestyleResult{{Call: called, Shape: inference.Shape{Kind: store.ShapeCollection},
		Patch: inference.Patch{Statements: []inference.Statement{}}}}
	x.w.drain(context.Background())
	if r := x.run(); r.State != store.RunFailed || r.Step != store.RestyleProjectStep || !strings.Contains(r.Error, "without a summary") {
		t.Fatalf("run = %+v, want failed on the project page", r)
	}
	if p := x.projectPage(); p.Shape != store.ShapeSystem || len(p.Statements) != 2 || x.st.projectEpoch != 0 {
		t.Fatalf("page = %+v, epoch %d: want it standing, still due", p, x.st.projectEpoch)
	}
}

// A departure of a collection's repository keeps the collection: its
// summary rewritten, no statement, no map.
func TestDeparture_KeepsTheCollection(t *testing.T) {
	x := departureWorld(t, 16)
	key := pageKey(store.ScopeProject, "tools", "")
	page := x.st.pages[key]
	page.Shape, page.Map, page.Statements = store.ShapeCollection, nil, []store.Statement{}
	x.st.pages[key] = page
	x.models.project = inference.DescriptionResult{Call: called, Description: &inference.Description{Summary: "Werkzeuge aus svc und web.",
		Statements: []inference.Statement{}}}
	x.w.drain(context.Background())
	if p := x.projectPage(); p.Shape != store.ShapeCollection || p.Summary != "Werkzeuge aus svc und web." || p.Map != nil || len(p.Statements) != 0 {
		t.Fatalf("project page = %+v, want the collection kept", p)
	}
}

// The prompt shows the shape as the page holds it.
func TestShapeShown(t *testing.T) {
	for want, p := range map[string]store.Page{
		"_None yet: judge it._": {},
		"_Not judged yet":       {RevisionID: 1},
		"`collection`":          {RevisionID: 1, Shape: store.ShapeCollection},
	} {
		if got := shapeShown(p); !strings.HasPrefix(got, want) {
			t.Errorf("shapeShown(%+v) = %q, want %q", p, got, want)
		}
	}
}
