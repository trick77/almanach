package consolidate

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/trick77/almanach/internal/inference"
	"github.com/trick77/almanach/internal/prompts"
	"github.com/trick77/almanach/internal/store"
)

var (
	svcRun = store.Source{Kind: store.FromBaseline, Commit: head, Repo: "svc"}
	svcPR  = store.Source{Kind: "pr", PRKey: "tools/svc#7"}
)

// restyleWorld is svc's page written under earlier rules, its map drawn
// from it, and a restyle run queued at its commit: a component telling a
// flow, a rule a pull request taught.
func restyleWorld(t *testing.T) world {
	t.Helper()
	x := newWorld(t, 16)
	page := store.Page{RevisionID: 5, Revision: 5, Summary: "svc rechnet ab. Ablauf: Login, dann Abrechnung.",
		Statements: []store.Statement{
			{ID: "s1", Kind: "component", Name: "Kern", Text: "Der Kern. Ablauf: erst Login, dann Abrechnung.",
				Files: []string{"core.go", "billing/run.go"}, Sources: []store.Source{svcRun}, Checked: head},
			{ID: "s2", Kind: "rule", Name: "Anmeldung", Text: "Nur angemeldet.", Files: []string{"auth/login.go"},
				Sources: []store.Source{svcPR}, Area: "Zugriff"},
		}}
	page.RepoMapBasis = page.MapBasis()
	x.st.pages[pageKey(store.ScopeRepository, "tools", "svc")] = page
	x.st.repos = append(x.st.repos, store.SourceRepo{ProjectKey: "tools", Slug: "web", HeadCommit: head})
	x.st.runs = append(x.st.runs, store.BaselineRun{ID: 1, ProjectKey: "tools", Slug: "svc", Mode: store.RunRestyle,
		Trigger: store.TriggerEpoch, BaseCommit: head, Commit: head, State: store.RunQueued, Plan: []byte(`{"restyle":{}}`),
		Epoch: prompts.Epoch, RepoID: 3})
	return x
}

func (x world) repoPage() store.Page {
	return x.st.pages[pageKey(store.ScopeRepository, "tools", "svc")]
}

func statementNamed(t *testing.T, page store.Page, name string) store.Statement {
	t.Helper()
	for _, s := range page.Statements {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("no statement %q on the page: %v", name, ids(page.Statements))
	return store.Statement{}
}

// A restyle reads no code and learns nothing: the component's flow becomes
// a flow statement carrying the component's sources, one split from the
// summary those of the statements sharing its file; one from nowhere is
// left out. An edit keeps its files, sources and last check; nothing is
// dropped; a new statement is unchecked. Then the project's page, its map
// as stored, then the map step, the text having moved. Both pages at the
// epoch, three calls in the ledger, the restyle's by step.
func TestRestyle_SplitsAndCreditsNothing(t *testing.T) {
	x := restyleWorld(t)
	x.models.restyles = []inference.RestyleResult{
		{Call: called, Patch: inference.Patch{Summary: ref("svc rechnet ab, siehe «Abrechnung»."),
			Statements: []inference.Statement{{ID: ref("s1"), Kind: "component", Name: "Kern", Text: "Der Kern; sein Ablauf ist «Abrechnung».",
				Files: []string{"elsewhere.go"}, Area: "Kern"}},
			Added: []inference.Statement{
				{Kind: "flow", Name: "Abrechnung", Text: "1. Login 2. Abrechnung", Files: []string{"billing/run.go", "invented.go"}, Area: "Kern", From: []string{"s1"}},
				{Kind: "rule", Name: "Erfunden", Text: "Neu.", Files: []string{"core.go"}, From: []string{"s9"}},
				{Kind: "flow", Name: "Anmeldefluss", Text: "Erst anmelden.", Files: []string{"auth/login.go"}, Area: "Zugriff", From: []string{"summary"}},
				{Kind: "flow", Name: "Ohne Datei", Text: "Aus der Übersicht.", From: []string{"summary"}},
			}}},
		{Call: called, Patch: inference.Patch{Summary: ref("Werkzeuge, nach Teilen gruppiert."), Statements: []inference.Statement{}}},
	}
	x.w.drain(context.Background())

	r := x.run()
	if r.State != store.RunDone || r.Calls != 3 {
		t.Fatalf("run = %+v, want done after the two restyles and the map step", r)
	}
	var purposes []string
	for _, c := range x.st.calls {
		p := c.Purpose
		if c.Step != nil {
			p += string(rune('0' + *c.Step))
		}
		purposes = append(purposes, p)
	}
	if want := []string{"restyle0", "restyle1", "repo_map"}; !slices.Equal(purposes, want) {
		t.Fatalf("ledger = %v, want %v", purposes, want)
	}
	if !slices.Equal(x.models.restyledProject, []bool{false, true}) {
		t.Fatalf("restyle calls for the project = %v", x.models.restyledProject)
	}
	if p := x.models.prompts[0]; !strings.Contains(p, "Der Kern. Ablauf") || strings.Contains(p, "package billing") ||
		!strings.Contains(p, "Learned from pull requests.") || strings.Contains(p, "tools/svc#7") {
		t.Errorf("repository restyle prompt:\n%s", p)
	}
	if p := x.models.prompts[1]; !strings.Contains(p, "Werkzeuge.") || !strings.Contains(p, "`svc`, `web`") {
		t.Errorf("project restyle prompt:\n%s", p)
	}

	page := x.repoPage()
	if got := ids(page.Statements); len(got) != 4 {
		t.Fatalf("statements = %v, want s1, s2 and the two split off", got)
	}
	kern := statementNamed(t, page, "Kern")
	if kern.Text != "Der Kern; sein Ablauf ist «Abrechnung»." || !slices.Equal(kern.Files, []string{"core.go", "billing/run.go"}) ||
		!slices.Equal(kern.Sources, []store.Source{svcRun}) || kern.Checked != head || kern.Area != "Kern" {
		t.Errorf("edited component = %+v", kern)
	}
	flow := statementNamed(t, page, "Abrechnung")
	if flow.Kind != "flow" || !slices.Equal(flow.Files, []string{"billing/run.go"}) || !slices.Equal(flow.Sources, []store.Source{svcRun}) || flow.Checked != "" {
		t.Errorf("flow split from s1 = %+v", flow)
	}
	login := statementNamed(t, page, "Anmeldefluss")
	if !slices.Equal(login.Sources, []store.Source{svcPR}) || !slices.Equal(login.Files, []string{"auth/login.go"}) {
		t.Errorf("flow split from the summary = %+v, want s2's pull request", login)
	}
	if page.Summary != "svc rechnet ab, siehe «Abrechnung»." || statementNamed(t, page, "Anmeldung").Text != "Nur angemeldet." {
		t.Errorf("page = %+v", page)
	}
	if x.st.repoEpoch != prompts.Epoch || x.st.projectEpoch != prompts.Epoch {
		t.Errorf("epochs = %d %d, want %d both", x.st.repoEpoch, x.st.projectEpoch, prompts.Epoch)
	}
	project := x.projectPage()
	if project.Summary != "Werkzeuge, nach Teilen gruppiert." || project.Map == nil || mapSlugs(project.Map)[0] != "svc" {
		t.Errorf("project page = %+v, want the summary regrouped, the map as stored", project)
	}
}

// Each page is restyled only below the epoch, a project of one has no page
// of its own, and with nothing due the run ends without a call: the map
// stands, the text unchanged.
func TestRestyle_OnlyWhatIsDue(t *testing.T) {
	for name, c := range map[string]struct {
		repoEpoch, projectEpoch, repos int
		want                           []bool
	}{
		"both due":       {0, 0, 2, []bool{false, true}},
		"repository at":  {prompts.Epoch, 0, 2, []bool{true}},
		"project at":     {0, prompts.Epoch, 2, []bool{false}},
		"project of one": {0, 0, 1, []bool{false}},
		"none due":       {prompts.Epoch, prompts.Epoch, 2, nil},
	} {
		t.Run(name, func(t *testing.T) {
			x := restyleWorld(t)
			x.st.repoEpoch, x.st.projectEpoch = c.repoEpoch, c.projectEpoch
			x.st.repoCount = map[string]int{"tools": c.repos}
			x.w.drain(context.Background())
			if r := x.run(); r.State != store.RunDone || r.Calls != len(c.want) {
				t.Fatalf("run = %+v, want done after %d calls", r, len(c.want))
			}
			if !slices.Equal(x.models.restyledProject, c.want) {
				t.Fatalf("restyle calls for the project = %v, want %v", x.models.restyledProject, c.want)
			}
			if len(x.st.revisions) != 0 {
				t.Errorf("revisions = %d after answers that change nothing", len(x.st.revisions))
			}
		})
	}
}

// An answer that only lays the page out writes no revision: the layout in
// place on the current revision, the map standing, no map call.
func TestRestyle_LayoutInPlace(t *testing.T) {
	x := restyleWorld(t)
	x.st.projectEpoch = prompts.Epoch
	x.models.restyles = []inference.RestyleResult{{Call: called, Patch: inference.Patch{Statements: []inference.Statement{}},
		Outline: []inference.OutlineArea{{Area: "Zugriff", IDs: []string{"s2"}}, {Area: "Kern", IDs: []string{"s1"}}}}}
	x.w.drain(context.Background())
	if r := x.run(); r.State != store.RunDone || r.Calls != 1 {
		t.Fatalf("run = %+v, want done after one call", r)
	}
	page := x.repoPage()
	if len(x.st.revisions) != 0 || x.st.arranges != 1 || page.Revision != 5 {
		t.Fatalf("revisions %d, layouts %d, page revision %d: want the layout in place", len(x.st.revisions), x.st.arranges, page.Revision)
	}
	if got := ids(page.Statements); !slices.Equal(got, []string{"s2", "s1"}) || page.Statements[1].Area != "Kern" {
		t.Errorf("statements = %v %+v", got, page.Statements)
	}
	if x.st.repoEpoch != prompts.Epoch {
		t.Errorf("repository epoch = %d", x.st.repoEpoch)
	}
}

// A call that fails fails the run, the page below the epoch still: the
// poller's retry takes it again.
func TestRestyle_FailedCallFailsTheRun(t *testing.T) {
	x := restyleWorld(t)
	x.models.restyles = []inference.RestyleResult{{Call: inference.Call{Outcome: inference.OutcomeUnparseable}}}
	x.w.drain(context.Background())
	if r := x.run(); r.State != store.RunFailed || !strings.Contains(r.Error, "restyle call failed") {
		t.Fatalf("run = %+v, want failed", r)
	}
	if x.st.repoEpoch != 0 || len(x.st.revisions) != 0 {
		t.Errorf("epoch %d, revisions %d after a failed call", x.st.repoEpoch, len(x.st.revisions))
	}
}

// A store that does not answer pauses the drain: the run stays open, no
// page moves to the epoch, nothing is paid twice after.
func TestRestyle_HoldsOnAStoreThatDoesNotAnswer(t *testing.T) {
	down := errors.New("database down")
	for name, fail := range map[string]func(x world){
		"epochs":          func(x world) { x.st.epochErr = down },
		"page":            func(x world) { x.st.pageErr = down },
		"step write":      func(x world) { x.st.applyErr = down },
		"repository rows": func(x world) { x.st.repoEpoch, x.st.reposErr = prompts.Epoch, down },
	} {
		t.Run(name, func(t *testing.T) {
			x := restyleWorld(t)
			fail(x)
			x.w.drain(context.Background())
			if r := x.run(); !r.Open() {
				t.Fatalf("run = %+v, want it open, the drain paused", r)
			}
			if x.st.restyles != 0 {
				t.Errorf("%d restyle steps written", x.st.restyles)
			}
		})
	}
}

// A repository without a page has nothing to restyle and is at the epoch
// without a call; the project's page is restyled all the same.
func TestRestyle_NoRepositoryPage(t *testing.T) {
	x := restyleWorld(t)
	delete(x.st.pages, pageKey(store.ScopeRepository, "tools", "svc"))
	x.w.drain(context.Background())
	if r := x.run(); r.State != store.RunDone || !slices.Equal(x.models.restyledProject, []bool{true}) {
		t.Fatalf("run = %+v, calls for the project %v", r, x.models.restyledProject)
	}
	if x.st.repoEpoch != prompts.Epoch || x.st.projectEpoch != prompts.Epoch {
		t.Errorf("epochs = %d %d", x.st.repoEpoch, x.st.projectEpoch)
	}
}

// With the run's calls spent the project's page waits, still due, for the
// next restyle of one of its repositories; a failed project call fails the
// run, the repository's page restyled and kept at the epoch.
func TestRestyle_ProjectLeftDue(t *testing.T) {
	t.Run("calls spent", func(t *testing.T) {
		x := restyleWorld(t)
		x.w.base.Limits.MaxCalls = 1
		x.w.drain(context.Background())
		if r := x.run(); r.State != store.RunDone || r.Calls != 1 {
			t.Fatalf("run = %+v, want done after one call", r)
		}
		if x.st.repoEpoch != prompts.Epoch || x.st.projectEpoch != 0 {
			t.Errorf("epochs = %d %d, want the project still due", x.st.repoEpoch, x.st.projectEpoch)
		}
	})
	t.Run("call failed", func(t *testing.T) {
		x := restyleWorld(t)
		x.models.restyles = []inference.RestyleResult{{Call: called, Patch: inference.Patch{Statements: []inference.Statement{}}},
			{Call: inference.Call{Outcome: inference.OutcomeUnparseable}}}
		x.w.drain(context.Background())
		if r := x.run(); r.State != store.RunFailed || !strings.Contains(r.Error, "project restyle call failed") {
			t.Fatalf("run = %+v, want failed", r)
		}
		if x.st.repoEpoch != prompts.Epoch || x.st.projectEpoch != 0 {
			t.Errorf("epochs = %d %d", x.st.repoEpoch, x.st.projectEpoch)
		}
	})
	t.Run("no call for the repository", func(t *testing.T) {
		x := restyleWorld(t)
		x.w.base.Limits.MaxCalls = 0
		x.w.drain(context.Background())
		if r := x.run(); r.State != store.RunFailed || x.st.repoEpoch != 0 {
			t.Fatalf("run = %+v, epoch %d: want failed, the page still due", r, x.st.repoEpoch)
		}
	})
}

// A full run is stamped with the epoch it reads under when it starts, a
// reconcile that reads afresh too; a reconcile, a departure, and a run a
// deploy restarted after stamping keep what they have.
func TestStartRun_StampsAFullRunsEpoch(t *testing.T) {
	for name, c := range map[string]struct {
		mode, trigger, plan string
		contains            bool
		epoch, want         int
	}{
		"full":             {store.RunFull, store.TriggerManual, "", true, 0, prompts.Epoch},
		"reconcile":        {store.RunReconcile, store.TriggerMonthly, "", true, 0, 0},
		"reconcile afresh": {store.RunReconcile, store.TriggerMonthly, "", false, 0, prompts.Epoch},
		"departure":        {store.RunFull, store.TriggerDeparture, `{"departure":true}`, true, 0, 0},
		"stamped before":   {store.RunFull, store.TriggerManual, "", true, prompts.Epoch + 7, prompts.Epoch + 7},
	} {
		t.Run(name, func(t *testing.T) {
			x := newWorld(t, 16)
			x.code.contains = c.contains
			x.st.runs = append(x.st.runs, store.BaselineRun{ID: 1, ProjectKey: "tools", Slug: "svc", Mode: c.mode, Trigger: c.trigger,
				BaseCommit: older40, Commit: older40, State: store.RunQueued, Epoch: c.epoch, Plan: []byte(c.plan)})
			r := x.run()
			if err := x.w.startRun(context.Background(), &r); err != nil {
				t.Fatal(err)
			}
			if got := x.run().Epoch; got != c.want {
				t.Errorf("epoch = %d, want %d", got, c.want)
			}
		})
	}
}

// A project step that answered with the whole page under today's rules
// puts the project page at the epoch: a full run's stamped with it, or the
// step that wrote the page first; a reconcile's, or a full run's from
// before a bump, does not.
func TestProjectRestyled(t *testing.T) {
	for name, c := range map[string]struct {
		mode  string
		epoch int
		first bool
		want  int
	}{
		"full":       {store.RunFull, prompts.Epoch, false, prompts.Epoch},
		"full, old":  {store.RunFull, prompts.Epoch - 1, false, 0},
		"reconcile":  {store.RunReconcile, 0, false, 0},
		"first page": {store.RunReconcile, 0, true, prompts.Epoch},
	} {
		t.Run(name, func(t *testing.T) {
			x := newWorld(t, 16)
			r := store.BaselineRun{ProjectKey: "tools", Mode: c.mode, Epoch: c.epoch}
			x.w.projectRestyled(context.Background(), &r, c.first)
			if x.st.projectEpoch != c.want {
				t.Errorf("project epoch = %d, want %d", x.st.projectEpoch, c.want)
			}
		})
	}
}

// The queue lists a restyle's three steps, its calls on theirs.
func TestRunSteps_Restyle(t *testing.T) {
	zero, one := 0, 1
	r := store.BaselineRun{State: store.RunProject, Plan: []byte(`{"restyle":{"repo":true}}`)}
	steps, err := RunSteps(r, []store.RunCall{{Purpose: store.PurposeRestyle, Step: &zero, Outcome: "ok"},
		{Purpose: store.PurposeRestyle, Step: &one, Outcome: "ok"}})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, s := range steps {
		got = append(got, s.Kind+":"+s.State+":"+string(rune('0'+s.Calls)))
	}
	if want := []string{"restyle:done:1", "project_restyle:current:1", "repo_map:todo:0"}; !slices.Equal(got, want) {
		t.Errorf("steps = %v, want %v", got, want)
	}
	for state, want := range map[string]string{store.RunQueued: "todo todo todo", store.RunDone: "done done done"} {
		r := store.BaselineRun{State: state, Plan: []byte(`{"restyle":{}}`)}
		steps, err := RunSteps(r, []store.RunCall{{Purpose: store.PurposeRepoMap, Outcome: "ok"}, {Purpose: store.PurposeSubject, Outcome: "ok"}})
		if err != nil {
			t.Fatal(err)
		}
		var states []string
		for _, s := range steps {
			states = append(states, s.State)
		}
		if strings.Join(states, " ") != want || steps[2].Calls != 1 || steps[0].Calls != 0 {
			t.Errorf("%s: steps %+v, want %s", state, steps, want)
		}
	}
	r = store.BaselineRun{State: store.RunProject, Plan: []byte(`{"restyle":{},"project_done":true}`)}
	if steps, _ := RunSteps(r, nil); steps[2].State != StepCurrent || steps[0].State != StepDone {
		t.Errorf("map step: %+v", steps)
	}
	r = store.BaselineRun{State: store.RunProject, Plan: []byte(`{"restyle":{}}`)}
	if steps, _ := RunSteps(r, nil); steps[0].State != StepCurrent {
		t.Errorf("repository step: %+v", steps)
	}
}
