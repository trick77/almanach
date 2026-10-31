package consolidate

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
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

// dangling is a sequence diagram that breaks a rule: a message to a part
// never declared.
func dangling() *diagram.Diagram {
	d := seq("x")
	d.Rows[0].To = "queue"
	return d
}

// A statement's diagram is stored with it, its labels Swiss; a diagram
// that breaks a rule, or one too many on the page, is left out.
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

	// A diagram that breaks a rule costs that diagram, not the rewrite: a
	// new statement goes without it, a kept one keeps its stored diagram,
	// its sources and its last check, its text unchanged.
	page := existingRepoPage()
	page.Statements[0].Diagram, page.Statements[0].Checked = seq("schreibt"), head
	s := page.Statements[0]
	st = newFakeStore()
	st.pages[pageKey(store.ScopeRepository, "PLAT", "files")] = page
	m = &fakeMerger{result: okResult(&inference.Description{Summary: "Der Dienst, neu.", Statements: []inference.Statement{
		{ID: str(s.ID), Kind: s.Kind, Name: s.Name, Text: s.Text, Files: s.Files, Diagram: dangling()},
		{Kind: "rule", Name: "R", Text: "T", Diagram: dangling()}}}, nil)}
	newWorker(st, m).Fold(context.Background(), queued("PLAT/files#42"))
	if len(st.revisions) != 1 {
		t.Fatalf("dangling end: revisions = %+v, want the rewrite taken", st.revisions)
	}
	got := st.revisions[0].Statements
	if !diagram.Equal(got[0].Diagram, seq("schreibt")) || len(got[0].Sources) != 1 || got[0].Checked != head {
		t.Errorf("kept statement = %+v, want its diagram, sources and check as stored", got[0])
	}
	if got[1].Diagram != nil || got[1].Text != "T" {
		t.Errorf("new statement = %+v, want it kept without the diagram", got[1])
	}

	// With its text changed in the same rewrite the stored picture goes
	// too, logged: it would show what the statement no longer says.
	st = newFakeStore()
	st.pages[pageKey(store.ScopeRepository, "PLAT", "files")] = page
	m = &fakeMerger{result: okResult(&inference.Description{Summary: page.Summary, Statements: []inference.Statement{
		{ID: str(s.ID), Kind: s.Kind, Name: s.Name, Text: "Nimmt Dateien an, neu.", Files: s.Files, Diagram: dangling()}}}, nil)}
	w := newWorker(st, m)
	var logs strings.Builder
	w.log = slog.New(slog.NewTextHandler(&logs, nil))
	w.Fold(context.Background(), queued("PLAT/files#42"))
	if len(st.revisions) != 1 || st.revisions[0].Statements[0].Diagram != nil || st.revisions[0].Statements[0].Text != "Nimmt Dateien an, neu." {
		t.Fatalf("changed text: revisions = %+v, want the text taken and the stored diagram dropped", st.revisions)
	}
	if !strings.Contains(logs.String(), "diagram breaking a rule left out") || !strings.Contains(logs.String(), "stored diagram dropped with a changed name or text") {
		t.Errorf("log lacks the dropped stored diagram:\n%s", logs.String())
	}

	// A rewrite the ceilings reject logs its rejection, not the diagram it
	// would have left out.
	st = newFakeStore()
	m = &fakeMerger{result: okResult(&inference.Description{Summary: strings.Repeat("x", 500), Statements: []inference.Statement{
		{Kind: "rule", Name: "R", Text: "T", Diagram: dangling()}}}, nil)}
	w = newWorker(st, m)
	logs.Reset()
	w.log = slog.New(slog.NewTextHandler(&logs, nil))
	w.Fold(context.Background(), queued("PLAT/files#42"))
	if len(st.revisions) != 0 || !strings.Contains(logs.String(), "rewrite rejected") || strings.Contains(logs.String(), "left out") {
		t.Errorf("rejected rewrite: revisions %+v, log:\n%s", st.revisions, logs.String())
	}

	// One diagram past the page's count (1 here) costs that diagram, not the
	// rewrite: the later one is left out, its statement kept.
	st = newFakeStore()
	m = &fakeMerger{result: okResult(&inference.Description{Summary: "Der Dienst.", Statements: []inference.Statement{
		{Kind: "rule", Name: "R1", Text: "T", Diagram: seq("a")},
		{Kind: "rule", Name: "R2", Text: "T", Diagram: seq("b")},
	}}, nil)}
	newWorker(st, m).Fold(context.Background(), queued("PLAT/files#42"))
	if len(st.revisions) != 1 {
		t.Fatalf("over the page's count: revisions = %+v, want one", st.revisions)
	}
	if got := st.revisions[0].Statements; got[0].Diagram == nil || got[1].Diagram != nil || got[1].Text != "T" {
		t.Errorf("statements = %+v, want R2 kept without its diagram", got)
	}
}

// {"kind": "none"} removes a statement's diagram and keeps the statement;
// on a new statement it is no diagram.
func TestFoldNoneRemovesTheDiagram(t *testing.T) {
	page := existingRepoPage()
	page.Statements[0].Diagram = seq("schreibt")
	st := newFakeStore()
	st.pages[pageKey(store.ScopeRepository, "PLAT", "files")] = page
	s := page.Statements[0]
	none := &diagram.Diagram{Kind: diagram.KindNone}
	m := &fakeMerger{result: okResult(&inference.Description{Summary: page.Summary, Statements: []inference.Statement{
		{ID: str(s.ID), Kind: s.Kind, Name: s.Name, Text: s.Text, Files: s.Files, Diagram: none},
		{Kind: "rule", Name: "Neu", Text: "T", Diagram: none},
	}}, nil)}
	newWorker(st, m).Fold(context.Background(), queued("PLAT/files#42"))
	if len(st.revisions) != 1 {
		t.Fatalf("revisions = %+v, want one", st.revisions)
	}
	got := st.revisions[0].Statements
	if got[0].Diagram != nil || got[0].Text != s.Text || got[1].Diagram != nil {
		t.Errorf("statements = %+v, want both without a diagram", got)
	}
	if len(got[0].Sources) != 2 {
		t.Errorf("sources = %+v, want the removal counted as a change", got[0].Sources)
	}
}

// A diagram over the page's count on a baseline step is left out; the
// reading goes on, no ceiling reached.
func TestRun_DiagramOverTheCountKeepsReading(t *testing.T) {
	x := newWorld(t, 16)
	x.w.limits.MaxDiagrams = 1
	x.queue(store.RunFull, "")
	x.models.catalogue = []inference.CatalogueResult{{Call: called, Catalogue: inference.Catalogue{Subjects: []inference.Subject{
		{Name: "A", Purpose: "p", Paths: []string{"auth/"}}}}}}
	x.models.patches = []inference.PatchResult{
		{Call: called, Patch: inference.Patch{Summary: ref("Ein Dienst."), Statements: []inference.Statement{
			{Kind: "component", Name: "Eins", Text: "T", Files: []string{"README.md"}, Diagram: seq("a")},
			{Kind: "component", Name: "Zwei", Text: "T", Files: []string{"README.md"}, Diagram: seq("b")}}}},
		{Call: called, Patch: inference.Patch{Statements: []inference.Statement{stmt(nil, "rule", "Login", "Zweistufig.", "auth/login.go")}}},
	}
	x.models.project = inference.DescriptionResult{Call: called}
	x.w.drain(context.Background())
	p := x.plan(t)
	if r := x.run(); r.State != store.RunDone || len(p.Skipped) != 0 {
		t.Fatalf("run %+v, skipped %v; want every part read", r, p.Skipped)
	}
	page := x.st.pages[pageKey(store.ScopeRepository, "tools", "svc")]
	if len(page.Statements) != 3 || page.Statements[0].Diagram == nil || page.Statements[1].Diagram != nil {
		t.Errorf("page = %+v, want three statements, the second without its diagram", page.Statements)
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

// On the merge path null keeps a stored diagram only while the name and
// text stay: with a changed text it goes, logged, a picture of what is no
// longer said; returned again with the changed text it stays.
func TestFoldNullKeepsTheDiagram(t *testing.T) {
	page := existingRepoPage()
	page.Statements[0].Diagram = seq("schreibt")
	s := page.Statements[0]
	fold := func(d *diagram.Diagram) (*fakeStore, string) {
		st := newFakeStore()
		st.pages[pageKey(store.ScopeRepository, "PLAT", "files")] = page
		m := &fakeMerger{result: okResult(&inference.Description{Summary: page.Summary, Statements: []inference.Statement{
			{ID: str(s.ID), Kind: s.Kind, Name: s.Name, Text: "Nimmt Dateien an, neu.", Files: s.Files, Diagram: d},
		}}, nil)}
		w := newWorker(st, m)
		var logs strings.Builder
		w.log = slog.New(slog.NewTextHandler(&logs, nil))
		w.Fold(context.Background(), queued("PLAT/files#42"))
		return st, logs.String()
	}
	st, logs := fold(nil)
	if len(st.revisions) != 1 || st.revisions[0].Statements[0].Diagram != nil || st.revisions[0].Statements[0].Text != "Nimmt Dateien an, neu." {
		t.Fatalf("null: revisions = %+v, want the text changed and the stale diagram dropped", st.revisions)
	}
	if !strings.Contains(logs, "stored diagram dropped with a changed name or text") {
		t.Errorf("null: log lacks the dropped diagram:\n%s", logs)
	}
	if st, _ := fold(seq("schreibt")); len(st.revisions) != 1 || !diagram.Equal(st.revisions[0].Statements[0].Diagram, seq("schreibt")) {
		t.Fatalf("returned again: revisions = %+v, want the text changed and the diagram kept", st.revisions)
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
	if st := run([]inference.Statement{echo(s1, &changed), echo(s2, nil)}); len(st.revisions) != 1 ||
		!diagram.Equal(st.revisions[0].Statements[0].Diagram, big) {
		t.Errorf("changed over-cap diagram: revisions = %+v, want it left out and the stored one kept", st.revisions)
	}
	third := inference.Statement{Kind: "rule", Name: "R3", Text: "T", Diagram: seq("y")}
	st := run([]inference.Statement{echo(s1, nil), echo(s2, nil), third})
	if len(st.revisions) != 1 {
		t.Fatalf("a third diagram: revisions = %+v, want the rewrite taken", st.revisions)
	}
	if got := st.revisions[0].Statements; got[0].Diagram == nil || got[1].Diagram == nil || got[2].Diagram != nil {
		t.Errorf("statements = %+v, want the page's two kept and the third left out", got)
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
		"describe": renderDescribe(r, existingRepoPage(), limits, 1, 2, 1, budget{4321, 3}, "the overview", "", "_None._", "_None._", nil),
		"project":  renderProject(r, existingRepoPage(), store.Page{Map: drawnMap("files")}, "_None._", []string{"files", "portal"}, limits),
		"merge":    render(prompts.Merge, queued("PLAT/files#42"), existingRepoPage(), store.Page{}, limits),
	} {
		if ph := left.FindAllString(out, -1); len(ph) > 0 {
			t.Errorf("%s prompt: placeholders left: %v", name, ph)
		}
		if name == "describe" && (!strings.Contains(out, "at most about 4321 characters of prose and add at most 3 new statements") ||
			!strings.Contains(out, "name, text, files or diagram (one added, replaced, or removed with")) {
			t.Error("describe prompt lacks the step's budget or the diagram among a statement's changes")
		}
		// The shared rules, once, their caps filled in; the field offered.
		if !strings.Contains(out, "At most 4 nodes") || strings.Count(out, "## Diagrams") != 1 || !strings.Contains(out, `"diagram": <diagram>`) {
			t.Errorf("%s prompt lacks the diagram rules or the field, or has them twice", name)
		}
	}
	// With ALMANACH_MAX_DIAGRAMS=0 the prompts offer no diagram either, the
	// project's map still asked for.
	off := limits
	off.MaxDiagrams = 0
	for name, out := range map[string]string{
		"describe": renderDescribe(r, existingRepoPage(), off, 1, 2, 1, budget{4321, 3}, "the overview", "", "_None._", "_None._", nil),
		"project":  renderProject(r, existingRepoPage(), store.Page{}, "_None._", []string{"files"}, off),
		"merge":    render(prompts.Merge, queued("PLAT/files#42"), existingRepoPage(), store.Page{}, off),
	} {
		if ph := left.FindAllString(out, -1); len(ph) > 0 || strings.Contains(out, `"diagram"`) || !strings.Contains(out, "Statements carry no diagram here") {
			t.Errorf("%s prompt with diagrams off: placeholders %v, or a diagram offered", name, ph)
		}
		if name == "describe" && !strings.Contains(out, "change to its kind, name, text or files; one") {
			t.Error("describe prompt with diagrams off still lists a diagram among a statement's changes")
		}
	}
	if out := renderProject(r, existingRepoPage(), store.Page{}, "_None._", []string{"files"}, off); !strings.Contains(out, "Every rewrite carries `map`, never null") {
		t.Error("project prompt with diagrams off lost the map")
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

// On the baseline paths a changed text takes a stored diagram with it when
// the answer says null (the step's edit), and keeps it when the answer
// returns it again (the project step's rewrite).
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
	again := stmt(ref("s1"), "entity", "Werkzeug", "Neu.")
	again.Diagram = seq("liest")
	x.models.project = inference.DescriptionResult{Call: called, Description: &inference.Description{Summary: "Werkzeuge.",
		Statements: []inference.Statement{again}, Map: drawnMap("svc")}}
	x.w.drain(context.Background())
	repo := x.st.pages[pageKey(store.ScopeRepository, "tools", "svc")].Statements[0]
	if repo.Text != "Neu." || repo.Diagram != nil {
		t.Errorf("repository statement = %+v, want the text changed and the stale diagram dropped", repo)
	}
	project := x.st.pages[pageKey(store.ScopeProject, "tools", "")].Statements[0]
	if project.Text != "Neu." || !diagram.Equal(project.Diagram, seq("liest")) {
		t.Errorf("project statement = %+v, want the text changed and the diagram returned again kept", project)
	}
}

// On the baseline paths a diagram that breaks a rule costs that diagram:
// the step's statements land and the part is not skipped. A stored diagram
// goes with a changed text (the subject step's edit) and stays beside an
// unchanged one, where on the project step the statement also keeps its
// sources and last check.
func TestRun_InvalidDiagramCostsOnlyItself(t *testing.T) {
	x := newWorld(t, 16)
	var logs strings.Builder
	x.w.log = slog.New(slog.NewTextHandler(&logs, nil))
	src := []store.Source{{Kind: store.FromBaseline, Commit: older40, Repo: "svc"}}
	x.st.pages[pageKey(store.ScopeRepository, "tools", "svc")] = store.Page{RevisionID: 1, Revision: 1, Summary: "Ein Dienst.",
		Statements: []store.Statement{{ID: "s1", Kind: "component", Name: "Dienst", Text: "Alt.", Files: []string{"README.md"}, Sources: src, Diagram: seq("ruft")}}}
	x.st.pages[pageKey(store.ScopeProject, "tools", "")] = store.Page{RevisionID: 1, Revision: 1, Summary: "Werkzeuge.", Map: drawnMap("svc"),
		Statements: []store.Statement{{ID: "s1", Kind: "entity", Name: "Werkzeug", Text: "Alt.", Files: []string{}, Sources: src, Checked: older40, Diagram: seq("liest")}}}
	x.st.others = []store.RepoSummary{{Slug: "svc"}}
	x.queue(store.RunFull, "")
	x.models.catalogue = []inference.CatalogueResult{{Call: called, Catalogue: inference.Catalogue{Subjects: []inference.Subject{}}}}
	edit := stmt(ref("s1"), "component", "Dienst", "Neu.", "README.md")
	edit.Diagram = dangling()
	added := stmt(nil, "rule", "Regel", "Gilt.", "README.md")
	added.Diagram = dangling()
	x.models.patches = []inference.PatchResult{{Call: called, Patch: inference.Patch{Statements: []inference.Statement{edit, added}}}}
	kept := stmt(ref("s1"), "entity", "Werkzeug", "Alt.")
	kept.Diagram = dangling()
	x.models.project = inference.DescriptionResult{Call: called, Description: &inference.Description{Summary: "Werkzeuge, neu.",
		Statements: []inference.Statement{kept}, Map: drawnMap("svc")}}
	x.w.drain(context.Background())
	if p := x.plan(t); len(p.Skipped) != 0 {
		t.Fatalf("skipped = %v, want every part applied", p.Skipped)
	}
	repo := x.st.pages[pageKey(store.ScopeRepository, "tools", "svc")].Statements
	if len(repo) != 2 || repo[0].Text != "Neu." || repo[0].Diagram != nil || repo[1].Diagram != nil {
		t.Errorf("repository statements = %+v, want both, the stale stored diagram dropped and none added", repo)
	}
	project := x.st.pages[pageKey(store.ScopeProject, "tools", "")]
	if s := project.Statements[0]; project.Summary != "Werkzeuge, neu." || !diagram.Equal(s.Diagram, seq("liest")) ||
		len(s.Sources) != 1 || s.Checked != older40 {
		t.Errorf("project statement = %+v, want its diagram, sources and check as stored", s)
	}
	if n := strings.Count(logs.String(), "diagram breaking a rule left out"); n != 2 {
		t.Errorf("logged %d struck diagrams, want the step's and the project's:\n%s", n, logs.String())
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

// The map never sinks the description: a map that breaks a rule is not
// drawn, the stored one stays, and the text is applied all the same. A
// repository no longer in the project is taken out of a returned map
// before the check. A changed map alone is a revision, the same map with
// the same text none. The map is held to the drawing's limits, not the
// statement diagrams' caps.
func TestRun_ProjectMap(t *testing.T) {
	projectAs := func(summary string, m *diagram.Diagram) world {
		x := newWorld(t, 16)
		x.st.others = []store.RepoSummary{{Slug: "svc"}, {Slug: "portal"}}
		x.st.pages[pageKey(store.ScopeProject, "tools", "")] = store.Page{RevisionID: 1, Revision: 1, Summary: "Werkzeuge.",
			Statements: []store.Statement{}, Map: drawnMap("svc")}
		x.queue(store.RunFull, "")
		x.models.catalogue = []inference.CatalogueResult{{Call: called, Catalogue: inference.Catalogue{Subjects: []inference.Subject{}}}}
		x.models.patches = []inference.PatchResult{{Call: called, Patch: inference.Patch{Summary: ref("Ein Dienst."), Statements: []inference.Statement{}}}}
		x.models.project = inference.DescriptionResult{Call: called, Description: &inference.Description{Summary: summary,
			Statements: []inference.Statement{}, Map: m}}
		x.w.drain(context.Background())
		return x
	}
	project := func(m *diagram.Diagram) world { return projectAs("Werkzeuge.", m) }
	projectRevisions := func(x world) []store.NewRevision {
		var out []store.NewRevision
		for _, r := range x.st.revisions {
			if r.Scope == store.ScopeProject {
				out = append(out, r)
			}
		}
		return out
	}

	// A bad map with new text: the text lands, the stored map stays.
	dangling := drawnMap("svc")
	dangling.Edges = append(dangling.Edges, diagram.Edge{From: "svc", To: "nowhere", Label: "HTTP", Head: "end"})
	x := projectAs("Werkzeuge, neu.", dangling)
	revs := projectRevisions(x)
	if len(revs) != 1 || revs[0].Summary != "Werkzeuge, neu." || !diagram.Equal(revs[0].Map, drawnMap("svc")) || x.run().Skipped != 0 {
		t.Errorf("dangling edge: revisions %+v, skipped %d; want the text with the stored map", revs, x.run().Skipped)
	}
	// A bad map with the same text: nothing changes, nothing is skipped.
	if x := project(dangling); len(projectRevisions(x)) != 0 || x.run().Skipped != 0 {
		t.Errorf("dangling edge, same text: revisions %+v, skipped %d", projectRevisions(x), x.run().Skipped)
	}

	// A repository that left the project is taken out with its edges; the
	// rest of the map is what it was.
	departed := drawnMap("svc", "billing")
	departed.Edges = append(departed.Edges, diagram.Edge{From: "billing", To: "pg", Label: "DB", Head: "end"})
	if x := project(departed); len(projectRevisions(x)) != 0 {
		t.Errorf("departed slug: revisions = %+v, want the map as stored, no revision", projectRevisions(x))
	}

	if revs := projectRevisions(project(drawnMap("svc"))); len(revs) != 0 {
		t.Errorf("same map, same text: revisions = %+v, want none", revs)
	}
	revs = projectRevisions(project(drawnMap("svc", "portal")))
	if len(revs) != 1 || len(revs[0].Map.Nodes) != 4 {
		t.Fatalf("changed map: revisions = %+v, want one with the new map", revs)
	}

	// 13 nodes: over the statement diagrams' cap (12), within the drawing's.
	wide := drawnMap("svc", "portal")
	for i := range 9 {
		wide.Nodes = append(wide.Nodes, diagram.Node{ID: fmt.Sprintf("x%d", i), Label: "Extern", Shape: "step", Zone: "external"})
	}
	if revs := projectRevisions(project(wide)); len(revs) != 1 || len(revs[0].Map.Nodes) != 13 {
		t.Errorf("13-node map: revisions = %+v, want it drawn", revs)
	}
}

// A map that was due and not accepted is stored as the sentinel: asked
// for, not drawn. A quiet reconcile then leaves the project alone instead
// of folding it forever; a run that folds it anyway asks again.
func TestRun_MapSentinel(t *testing.T) {
	fold := func(page store.Page, answer *inference.Description) world {
		x := newWorld(t, 16)
		x.st.pages[pageKey(store.ScopeProject, "tools", "")] = page
		x.queue(store.RunFull, "")
		x.models.catalogue = []inference.CatalogueResult{{Call: called, Catalogue: inference.Catalogue{Subjects: []inference.Subject{}}}}
		x.models.patches = []inference.PatchResult{{Call: called, Patch: inference.Patch{Summary: ref("Ein Dienst."), Statements: []inference.Statement{}}}}
		x.models.project = inference.DescriptionResult{Call: called, Description: answer}
		x.w.drain(context.Background())
		return x
	}
	unmapped := store.Page{RevisionID: 1, Revision: 1, Summary: "Werkzeuge.", Statements: []store.Statement{}}
	projectMapOf := func(x world) *diagram.Diagram { return x.st.pages[pageKey(store.ScopeProject, "tools", "")].Map }

	for name, answer := range map[string]*inference.Description{
		"null map":    {Summary: "Werkzeuge, neu.", Statements: []inference.Statement{}},
		"refused map": {Summary: "Werkzeuge, neu.", Statements: []inference.Statement{}, Map: &diagram.Diagram{Kind: diagram.KindFlow, Direction: "LR"}},
		"no change":   nil,
	} {
		if m := projectMapOf(fold(unmapped, answer)); !diagram.IsSentinel(m) {
			t.Errorf("%s: map = %+v, want the sentinel", name, m)
		}
	}

	// With the sentinel stored a quiet reconcile makes no call.
	plan, _ := json.Marshal(runPlan{Plan: baseline.Plan{Subjects: []baseline.Subject{{Name: "A", Purpose: "p",
		Files: []baseline.File{{Path: "auth/login.go", Size: 30}}}}}})
	y := newWorld(t, 16)
	asked := unmapped
	asked.Map = diagram.Sentinel()
	y.st.pages[pageKey(store.ScopeProject, "tools", "")] = asked
	y.st.documented = &store.BaselineRun{Commit: older40, Plan: plan, State: store.RunDone}
	y.code.diff = []gitrepo.Entry{{Path: "go.sum", Mode: "100644", Size: 6}}
	y.queue(store.RunReconcile, older40)
	y.w.drain(context.Background())
	if y.run().State != store.RunDone || len(y.st.calls) != 0 {
		t.Errorf("quiet reconcile over the sentinel: run %+v, %d calls; want none", y.run(), len(y.st.calls))
	}
	// A full run asks again, and a good map replaces the sentinel.
	if m := projectMapOf(fold(asked, &inference.Description{Summary: "Werkzeuge.", Statements: []inference.Statement{}, Map: drawnMap("svc")})); !diagram.Equal(m, drawnMap("svc")) {
		t.Errorf("full run over the sentinel: map = %+v, want the new one", m)
	}
	if got := projectMap(asked); !strings.Contains(got, "None yet") {
		t.Errorf("the prompt shows the sentinel as a map: %q", got)
	}
}

// A carried map never names a repository that left the project: a null or
// refused map, and a "no change" answer, carry the stored one without it.
func TestRun_CarriedMapDropsDeparted(t *testing.T) {
	stored := drawnMap("svc", "gone")
	stored.Edges = append(stored.Edges, diagram.Edge{From: "gone", To: "pg", Label: "DB", Head: "end"})
	want := drawnMap("svc")
	for name, answer := range map[string]*inference.Description{
		"null map":    {Summary: "Werkzeuge, neu.", Statements: []inference.Statement{}},
		"refused map": {Summary: "Werkzeuge, neu.", Statements: []inference.Statement{}, Map: &diagram.Diagram{Kind: diagram.KindFlow, Direction: "LR"}},
		"no change":   nil,
	} {
		x := newWorld(t, 16)
		x.st.pages[pageKey(store.ScopeProject, "tools", "")] = store.Page{RevisionID: 1, Revision: 1, Summary: "Werkzeuge.",
			Statements: []store.Statement{}, Map: stored}
		x.queue(store.RunFull, "")
		x.models.catalogue = []inference.CatalogueResult{{Call: called, Catalogue: inference.Catalogue{Subjects: []inference.Subject{}}}}
		x.models.patches = []inference.PatchResult{{Call: called, Patch: inference.Patch{Summary: ref("Ein Dienst."), Statements: []inference.Statement{}}}}
		x.models.project = inference.DescriptionResult{Call: called, Description: answer}
		x.w.drain(context.Background())
		if m := x.st.pages[pageKey(store.ScopeProject, "tools", "")].Map; !diagram.Equal(m, want) {
			t.Errorf("%s: map = %+v, want the stored one without the departed repository", name, m)
		}
	}
}

// A project with more repositories than a map holds gets none: the step
// asks for null, a returned map is not drawn, and a quiet reconcile does
// not run the step again for the missing map.
func TestRun_ProjectTooLargeForAMap(t *testing.T) {
	many := func() []store.RepoSummary {
		var out []store.RepoSummary
		for i := range diagram.DrawMaxNodes {
			out = append(out, store.RepoSummary{Slug: fmt.Sprintf("r%02d", i)})
		}
		return append(out, store.RepoSummary{Slug: "svc"})
	}
	x := newWorld(t, 16)
	x.st.others = many()
	x.queue(store.RunFull, "")
	x.models.catalogue = []inference.CatalogueResult{{Call: called, Catalogue: inference.Catalogue{Subjects: []inference.Subject{}}}}
	x.models.patches = []inference.PatchResult{{Call: called, Patch: inference.Patch{Summary: ref("Ein Dienst."), Statements: []inference.Statement{}}}}
	x.models.project = inference.DescriptionResult{Call: called, Description: &inference.Description{Summary: "Werkzeuge.",
		Statements: []inference.Statement{}, Map: drawnMap("svc")}}
	x.w.drain(context.Background())
	p := x.st.pages[pageKey(store.ScopeProject, "tools", "")]
	if p.Summary != "Werkzeuge." || p.Map != nil {
		t.Errorf("project page = %+v, want the text and no map", p)
	}
	prompt := x.models.prompts[len(x.models.prompts)-1]
	if !strings.Contains(prompt, "This project has 17 repositories, more than one map holds: `map` is null") {
		t.Errorf("project prompt does not say the map is null")
	}

	plan, _ := json.Marshal(runPlan{Plan: baseline.Plan{Subjects: []baseline.Subject{{Name: "A", Purpose: "p",
		Files: []baseline.File{{Path: "auth/login.go", Size: 30}}}}}})
	y := newWorld(t, 16)
	y.st.others = many()
	y.st.pages[pageKey(store.ScopeProject, "tools", "")] = store.Page{RevisionID: 1, Revision: 1, Summary: "Werkzeuge.", Statements: []store.Statement{}}
	y.st.documented = &store.BaselineRun{Commit: older40, Plan: plan, State: store.RunDone}
	y.code.diff = []gitrepo.Entry{{Path: "go.sum", Mode: "100644", Size: 6}}
	y.queue(store.RunReconcile, older40)
	y.w.drain(context.Background())
	if y.run().State != store.RunDone || len(y.st.calls) != 0 {
		t.Errorf("quiet reconcile of a too-large project: run %+v, %d calls; want done without one", y.run(), len(y.st.calls))
	}
}
