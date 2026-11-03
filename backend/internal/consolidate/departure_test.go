package consolidate

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/trick77/almanach/internal/diagram"
	"github.com/trick77/almanach/internal/inference"
	"github.com/trick77/almanach/internal/store"
)

// departureWorld is a project of svc, web and old, old just deactivated and
// its departure due: its page has a statement old's run wrote, one a pull
// request taught, one old and svc taught together, one without a source,
// and a map with old on it.
func departureWorld(t *testing.T, calls int) world {
	t.Helper()
	x := newWorld(t, calls)
	oldCommit := store.Source{Kind: store.FromBaseline, Commit: older40, Repo: "old"}
	svcCommit := store.Source{Kind: store.FromBaseline, Commit: head, Repo: "svc"}
	x.st.pages[pageKey(store.ScopeProject, "tools", "")] = store.Page{RevisionID: 1, Revision: 1, Summary: "Werkzeuge aus svc, web und old.",
		Statements: []store.Statement{
			{ID: "s1", Kind: "component", Name: "Altlast", Text: "Nur old.", Sources: []store.Source{oldCommit}},
			{ID: "s2", Kind: "rule", Name: "Altregel", Text: "Aus einem PR von old.", Sources: []store.Source{{Kind: "pr", PRKey: "tools/old#1"}}},
			{ID: "s3", Kind: "interface", Name: "API", Text: "svc bietet sie, old nutzt sie.", Sources: []store.Source{oldCommit, svcCommit}},
			{ID: "s4", Kind: "decision", Name: "Ohne Quelle", Text: "old bleibt aussen vor."},
		},
		Map: drawnMap("svc", "web", "old")}
	x.st.others = []store.RepoSummary{{Slug: "svc", Summary: "Der Dienst."}, {Slug: "web", Summary: "Die Oberfläche."}}
	x.st.departed = []string{"old"}
	x.st.departureDue = true
	x.st.runs = append(x.st.runs, store.BaselineRun{ID: 1, ProjectKey: "tools", Slug: "old", Mode: store.RunFull,
		Trigger: store.TriggerDeparture, Commit: older40, State: store.RunQueued, Plan: []byte(`{"departure":true}`)})
	return x
}

func ids(statements []store.Statement) []string {
	out := []string{}
	for _, s := range statements {
		out = append(out, s.ID)
	}
	return out
}

func mapSlugs(m *diagram.Diagram) []string {
	out := []string{}
	if m == nil {
		return out
	}
	for _, n := range m.Nodes {
		out = append(out, n.ID)
	}
	return out
}

func (x world) projectPage() store.Page { return x.st.pages[pageKey(store.ScopeProject, "tools", "")] }

// A departure run reads nothing: the code takes old off the map, one call
// rewrites the statements and the summary (the patch rules holding: a
// PR-taught statement is never dropped), the departure settled. One
// revision, the run done, one project call in the ledger.
func TestDeparture_MapThenCall(t *testing.T) {
	x := departureWorld(t, 16)
	x.models.project = inference.DescriptionResult{Call: called, Description: &inference.Description{Summary: "Werkzeuge aus svc und web.",
		Statements: []inference.Statement{stmt(ref("s3"), "interface", "API", "svc bietet sie.")}, Drop: []string{"s1", "s2", "s4"},
		Map: drawnMap("svc", "web", "old")}}
	x.w.drain(context.Background())

	r := x.run()
	if r.State != store.RunDone || r.Calls != 1 || r.FinishedAt == nil {
		t.Fatalf("run = %+v", r)
	}
	if len(x.st.calls) != 1 || x.st.calls[0].Purpose != store.PurposeProject || x.st.calls[0].RepoSlug != "old" {
		t.Fatalf("ledger = %+v", x.st.calls)
	}
	prompt := x.models.prompts[0]
	for _, want := range []string{"taken out of project `tools`: `old`", "`svc`, `web`", "Nur old.", "svc bietet sie, old nutzt sie.", "Der Dienst."} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
	if len(x.st.revisions) != 1 {
		t.Fatalf("revisions = %d, want one", len(x.st.revisions))
	}
	page := x.projectPage()
	if got := ids(page.Statements); !slices.Equal(got, []string{"s2", "s3"}) {
		t.Errorf("statements = %v, want s2 (PR-taught, never dropped) and s3", got)
	}
	if page.Summary != "Werkzeuge aus svc und web." || page.Statements[1].Text != "svc bietet sie." {
		t.Errorf("page = %+v", page)
	}
	// Taking old out teaches nothing: s3 keeps the sources it had, never
	// credited to the departed repository's run.
	if got := page.Statements[1].Sources; len(got) != 2 || got[0].Repo != "old" || got[1].Repo != "svc" {
		t.Errorf("s3 sources = %+v, want old's and svc's as before", got)
	}
	// The map is the code's, whatever the call returned: old is off.
	if slices.Contains(mapSlugs(page.Map), "old") || !slices.Contains(mapSlugs(page.Map), "svc") {
		t.Errorf("map nodes = %v", mapSlugs(page.Map))
	}
	if !slices.EqualFunc(x.st.settled, [][]string{{"old"}}, slices.Equal) {
		t.Errorf("settled = %v, want old", x.st.settled)
	}
}

// A statement the call adds is left out: a departure only takes away.
func TestDeparture_NoStatementAdded(t *testing.T) {
	x := departureWorld(t, 16)
	x.models.project = inference.DescriptionResult{Call: called, Description: &inference.Description{Summary: "Werkzeuge aus svc und web.",
		Statements: []inference.Statement{stmt(nil, "rule", "Neu", "Erfunden.")}}}
	x.w.drain(context.Background())
	page := x.projectPage()
	if got := ids(page.Statements); !slices.Equal(got, []string{"s1", "s2", "s3", "s4"}) || page.Summary != "Werkzeuge aus svc und web." {
		t.Errorf("statements = %v, summary %q", got, page.Summary)
	}
}

// No call to be had, or none answered: the map alone, every statement as
// it was, one revision, the departure still due for the next project step.
// No rewrite asked: answered, settled.
func TestDeparture_MapAlone(t *testing.T) {
	for name, c := range map[string]struct {
		calls  int
		res    inference.DescriptionResult
		settle bool
	}{
		"no rewrite asked":     {16, inference.DescriptionResult{Call: called}, true},
		"no call left":         {0, inference.DescriptionResult{Call: called}, false},
		"the call failed":      {16, inference.DescriptionResult{Call: inference.Call{Outcome: inference.OutcomeUnparseable}}, false},
		"gateway attempts out": {16, inference.DescriptionResult{Call: inference.Call{Outcome: inference.OutcomeTimedOut, Err: errors.New("slow")}}, false},
	} {
		t.Run(name, func(t *testing.T) {
			x := departureWorld(t, c.calls)
			x.models.project = c.res
			if name == "gateway attempts out" {
				x.st.runs[0].Attempts = maxFoldAttempts - 1
			}
			x.w.drain(context.Background())
			if r := x.run(); r.State != store.RunDone {
				t.Fatalf("run = %+v, want done", r)
			}
			page := x.projectPage()
			if got := ids(page.Statements); !slices.Equal(got, []string{"s1", "s2", "s3", "s4"}) || len(x.st.revisions) != 1 ||
				slices.Contains(mapSlugs(page.Map), "old") {
				t.Errorf("statements = %v, revisions %d, map %v", got, len(x.st.revisions), mapSlugs(page.Map))
			}
			if x.st.departureDue == c.settle {
				t.Errorf("due after the run = %v, want %v", x.st.departureDue, !c.settle)
			}
		})
	}
}

// Left with one repository, the project shows no page: the map alone, no
// call, settled.
func TestDeparture_ProjectOfOneNoCall(t *testing.T) {
	x := departureWorld(t, 16)
	x.st.others = []store.RepoSummary{{Slug: "svc"}}
	x.w.drain(context.Background())
	if r := x.run(); r.State != store.RunDone || r.Calls != 0 || len(x.models.prompts) != 0 || x.st.departureDue {
		t.Fatalf("run = %+v, prompts %d, due %v", r, len(x.models.prompts), x.st.departureDue)
	}
}

// Nothing to take out: no page, no repository deactivated any more, an
// earlier call took them out already (not due), or a page without a map
// the call leaves as it is. The run ends done without a revision.
func TestDeparture_NothingToTakeOut(t *testing.T) {
	trimmed := func(x world) {
		p := x.projectPage()
		p.Map = drawnMap("svc", "web")
		x.st.pages[pageKey(store.ScopeProject, "tools", "")] = p
	}
	for name, set := range map[string]func(x world){
		"no page": func(x world) { delete(x.st.pages, pageKey(store.ScopeProject, "tools", "")) },
		"activated again": func(x world) {
			x.st.departed = nil
			trimmed(x)
		},
		"not due": func(x world) {
			x.st.departureDue = false
			trimmed(x)
		},
		"no map": func(x world) {
			x.models.project = inference.DescriptionResult{Call: called}
			p := x.projectPage()
			p.Map = nil
			x.st.pages[pageKey(store.ScopeProject, "tools", "")] = p
		},
	} {
		t.Run(name, func(t *testing.T) {
			x := departureWorld(t, 16)
			set(x)
			x.w.drain(context.Background())
			if r := x.run(); r.State != store.RunDone || len(x.st.revisions) != 0 {
				t.Fatalf("run = %+v, revisions %d", r, len(x.st.revisions))
			}
			if name != "no map" && len(x.models.prompts) != 0 {
				t.Errorf("a call with nothing due: %d prompts", len(x.models.prompts))
			}
		})
	}
}

// A project too large for a map loses it with the departure, as any
// revision of it would.
func TestDeparture_TooLargeForAMap(t *testing.T) {
	x := departureWorld(t, 0)
	x.st.others = nil
	for i := range diagram.MapMaxNodes {
		x.st.others = append(x.st.others, store.RepoSummary{Slug: fmt.Sprintf("r%d", i)})
	}
	x.w.drain(context.Background())
	if page := x.projectPage(); page.Map != nil {
		t.Errorf("map = %v, want none", mapSlugs(page.Map))
	}
}

// A write the database refuses as busy holds the step, nothing lost and
// nothing settled.
func TestDeparture_BusyWriteHolds(t *testing.T) {
	x := departureWorld(t, 0)
	x.st.applyErr = store.ErrBusy
	x.w.drain(context.Background())
	if r := x.run(); r.State != store.RunProject || !x.st.departureDue {
		t.Fatalf("run = %+v, due %v, want still in its project step, due", r, x.st.departureDue)
	}
}

// A departure still due makes a quiet reconcile of another repository fold
// the project; its answered call settles it.
func TestProjectStep_TakesADueDepartureOut(t *testing.T) {
	x := newWorld(t, 16)
	page := x.projectPage()
	r := store.BaselineRun{ProjectKey: "tools", Slug: "svc", Mode: store.RunReconcile}
	if skip, _, err := x.w.projectStands(context.Background(), &r, runPlan{}, page); err != nil || !skip {
		t.Fatalf("quiet reconcile: skip=%v err=%v, want it skipped", skip, err)
	}
	x.st.departureDue = true
	if skip, _, err := x.w.projectStands(context.Background(), &r, runPlan{}, page); err != nil || skip {
		t.Fatalf("with a departure due: skip=%v err=%v, want the project folded", skip, err)
	}

	x.st.departed = []string{"old"}
	x.st.pages[pageKey(store.ScopeRepository, "tools", "svc")] = store.Page{RevisionID: 2, Revision: 1, Summary: "Der Dienst.",
		Statements: []store.Statement{}}
	x.models.project = inference.DescriptionResult{Call: called}
	plan := []byte(`{"read":[]}`)
	x.st.runs = append(x.st.runs, store.BaselineRun{ID: 1, ProjectKey: "tools", Slug: "svc", Mode: store.RunReconcile, BaseCommit: older40,
		Trigger: store.TriggerMonthly, Commit: head, State: store.RunProject, Plan: plan})
	x.w.drain(context.Background())
	if len(x.models.prompts) == 0 || !strings.Contains(x.models.prompts[0], "no part of it: `old`") {
		t.Fatal("the project step did not name the departed repository")
	}
	if x.st.departureDue || !slices.EqualFunc(x.st.settled, [][]string{{"old"}}, slices.Equal) {
		t.Errorf("due %v, settled %v, want old settled", x.st.departureDue, x.st.settled)
	}
}

// Every project fold names the deactivated repositories, never one when
// there are none.
func TestRenderProject_NamesTheDeparted(t *testing.T) {
	r := store.BaselineRun{ProjectKey: "tools", Slug: "svc", Commit: head}
	with := renderProject(r, store.Page{}, store.Page{}, "_None._", "_None._", nil, []string{"svc", "web"}, []string{"old"}, limits, nil)
	if !strings.Contains(with, "taken out of the project and are no part of it: `old`") {
		t.Error("the project prompt does not name the departed repository")
	}
	without := renderProject(r, store.Page{}, store.Page{}, "_None._", "_None._", nil, []string{"svc", "web"}, nil, limits, nil)
	if strings.Contains(without, "taken out of the project") {
		t.Error("the project prompt names departed repositories when there are none")
	}
}
