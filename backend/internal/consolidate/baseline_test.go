package consolidate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/trick77/almanach/internal/baseline"
	"github.com/trick77/almanach/internal/config"
	"github.com/trick77/almanach/internal/diagram"
	"github.com/trick77/almanach/internal/gitrepo"
	"github.com/trick77/almanach/internal/inference"
	"github.com/trick77/almanach/internal/prompts"
	"github.com/trick77/almanach/internal/store"
)

var (
	head    = strings.Repeat("a", 40)
	older40 = strings.Repeat("b", 40)
)

// runStore is the worker's store with the runs: a step's revision lands on
// the page, as the store's transaction does.
type runStore struct {
	*fakeStore
	runs       []store.BaselineRun
	saveErr    error
	applyErr   error
	repos      []store.SourceRepo
	documented *store.BaselineRun
	sources    map[string]bool
	// others is the project's source repositories with their summaries.
	others    []store.RepoSummary
	othersErr error
	// saved is every state a run was saved in, in order.
	saved []string
	// problems is the repository's problem areas; problemsErr fails the read.
	problems    []store.RepoProblem
	problemsErr error
	// problemsUntil is the cutoff the last problem read asked for;
	// problemReads counts the reads.
	problemsUntil time.Time
	problemReads  int
	// mapErr fails the map step's write; mapWrites counts the writes tried.
	mapErr    error
	mapWrites int
}

func (s *runStore) RepoProblems(_ context.Context, _, _ string, until time.Time) ([]store.RepoProblem, error) {
	s.problemsUntil = until
	s.problemReads++
	return s.problems, s.problemsErr
}

func (s *runStore) NextRun(context.Context) (store.BaselineRun, bool, error) {
	for _, started := range []bool{true, false} {
		for _, r := range s.runs {
			if r.Open() && (r.State != store.RunQueued) == started {
				return r, true, nil
			}
		}
	}
	return store.BaselineRun{}, false, nil
}

func (s *runStore) SaveRun(_ context.Context, r store.BaselineRun) (bool, error) {
	if s.saveErr != nil {
		return false, s.saveErr
	}
	for i := range s.runs {
		if s.runs[i].ID == r.ID {
			if !s.runs[i].Open() {
				return false, nil
			}
			s.runs[i] = r
			s.saved = append(s.saved, r.State)
			return true, nil
		}
	}
	return false, nil
}

func (s *runStore) ApplyBaselineStep(ctx context.Context, rev store.NewRevision, r store.BaselineRun) (int, bool, error) {
	if s.applyErr != nil {
		return 0, false, s.applyErr
	}
	if ok, err := s.SaveRun(ctx, r); !ok || err != nil {
		return 0, ok, err
	}
	n, err := s.InsertRevision(ctx, rev)
	// A repository revision carries the page's map and basis, as the
	// store's insertRevision does.
	key := pageKey(rev.Scope, rev.ProjectKey, rev.RepoSlug)
	var repoMap *diagram.Diagram
	var basis string
	if rev.Scope == store.ScopeRepository {
		repoMap, basis = s.pages[key].RepoMap, s.pages[key].RepoMapBasis
	}
	s.pages[key] = store.Page{RevisionID: int64(n), Revision: n, Summary: rev.Summary,
		Statements: rev.Statements, SourceKind: rev.SourceKind, SourceCommit: rev.SourceCommit, Map: rev.Map, RepoMap: repoMap, RepoMapBasis: basis}
	return n, true, err
}

// SetRepoMap sets the page's map and basis in place, with the run's save,
// as the store does; mapErr fails it.
func (s *runStore) SetRepoMap(ctx context.Context, project, repo string, m *diagram.Diagram, basis string, r store.BaselineRun) (bool, error) {
	if s.mapErr != nil {
		s.mapWrites++
		return false, s.mapErr
	}
	if ok, err := s.SaveRun(ctx, r); !ok || err != nil {
		return ok, err
	}
	key := pageKey(store.ScopeRepository, project, repo)
	p := s.pages[key]
	p.RepoMap, p.RepoMapBasis = m, basis
	s.pages[key] = p
	s.mapWrites++
	return true, nil
}

func (s *runStore) DocumentedRun(context.Context, string, string) (store.BaselineRun, bool, error) {
	if s.documented == nil {
		return store.BaselineRun{}, false, nil
	}
	return *s.documented, true, nil
}

func (s *runStore) SourceRepos(context.Context, string) ([]store.SourceRepo, error) {
	return s.repos, nil
}

func (s *runStore) IsSourceRepo(_ context.Context, key, slug string) (bool, error) {
	return s.sources[key+"/"+slug], nil
}

func (s *runStore) RepoSummaries(context.Context, string) ([]store.RepoSummary, error) {
	return s.others, s.othersErr
}

// fakeCode is a clone: one tree, the bodies of its files, one diff.
type fakeCode struct {
	tree     []gitrepo.Entry
	bodies   map[string]string
	diff     []gitrepo.Entry
	contains bool
	// held answers Contains for the merge commits it names; asked counts
	// those questions.
	held        map[string]bool
	containsErr map[string]error
	asked       int
	readErr     error
	read        [][]string
}

func (c *fakeCode) Tree(ctx context.Context, _, _, _ string) ([]gitrepo.Entry, error) {
	if ctx.Err() != nil {
		// What a git child killed by the cancel answers: no context error.
		return nil, errors.New("git ls-tree: signal: killed")
	}
	return c.tree, c.readErr
}
func (c *fakeCode) Diff(context.Context, string, string, string, string) ([]gitrepo.Entry, error) {
	return c.diff, nil
}
func (c *fakeCode) Contains(_ context.Context, _, _, ancestor, _ string) (bool, error) {
	if err, ok := c.containsErr[ancestor]; ok {
		c.asked++
		return false, err
	}
	if in, ok := c.held[ancestor]; ok {
		c.asked++
		return in, nil
	}
	if ancestor == "short" {
		return false, gitrepo.ErrBadSHA
	}
	return c.contains, nil
}
func (c *fakeCode) CommitTime(context.Context, string, string, string) (time.Time, error) {
	return mergedAt, nil
}
func (c *fakeCode) ReadFiles(_ context.Context, _, _, _ string, files []baseline.File) (map[string][]byte, error) {
	out := map[string][]byte{}
	var paths []string
	for _, f := range files {
		paths = append(paths, f.Path)
		if b, ok := c.bodies[f.Path]; ok {
			out[f.Path] = []byte(b)
		}
	}
	c.read = append(c.read, paths)
	return out, nil
}

// fakeModels answers the catalogue, the patches in order, the project and
// the repository map. repoMap's zero value answers "no change".
type fakeModels struct {
	catalogue []inference.CatalogueResult
	patches   []inference.PatchResult
	project   inference.DescriptionResult
	repoMap   inference.RepoMapResult
	prompts   []string
}

func (m *fakeModels) RepoMap(_ context.Context, prompt string) inference.RepoMapResult {
	m.prompts = append(m.prompts, prompt)
	return m.repoMap
}

func (m *fakeModels) Catalogue(_ context.Context, prompt string) inference.CatalogueResult {
	m.prompts = append(m.prompts, prompt)
	res := m.catalogue[0]
	m.catalogue = m.catalogue[1:]
	return res
}

func (m *fakeModels) Patch(_ context.Context, prompt string) inference.PatchResult {
	m.prompts = append(m.prompts, prompt)
	res := m.patches[0]
	m.patches = m.patches[1:]
	if res.Patch.Added == nil {
		res.Patch.Statements, res.Patch.Added = split(res.Patch.Statements)
	}
	return res
}

func (m *fakeModels) Describe(_ context.Context, prompt string) inference.DescriptionResult {
	m.prompts = append(m.prompts, prompt)
	res := m.project
	res.Description = answered(res.Description)
	return res
}

var called = inference.Call{Outcome: inference.OutcomeOK, Cost: inference.CallCost{Model: "m", Answered: true}}

func stmt(id *string, kind, name, text string, files ...string) inference.Statement {
	return inference.Statement{ID: id, Kind: kind, Name: name, Text: text, Files: files}
}

func ref(s string) *string { return &s }

// drawnMap is a system map: the repositories by slug, a store behind the
// first, an external system calling it.
func drawnMap(slugs ...string) *diagram.Diagram {
	d := &diagram.Diagram{Kind: diagram.KindFlow, Direction: "LR", Zones: []diagram.Zone{
		{ID: "repositories", Title: "Repositories"}, {ID: "stores", Title: "Datenspeicher"}, {ID: "external", Title: "Externe Systeme"}}}
	for _, s := range slugs {
		d.Nodes = append(d.Nodes, diagram.Node{ID: s, Label: s, Shape: "step", Zone: "repositories"})
	}
	d.Nodes = append(d.Nodes, diagram.Node{ID: "pg", Label: "PostgreSQL", Shape: "step", Zone: "stores"},
		diagram.Node{ID: "forge", Label: "Forge", Shape: "step", Zone: "external"})
	d.Edges = []diagram.Edge{{From: slugs[0], To: "pg", Label: "DB", Head: "end"}, {From: "forge", To: slugs[0], Label: "HTTP: Webhook", Head: "end"}}
	return d
}

// drawnRepoMap is a repository map: the API entered, a part behind it, the
// database it writes.
func drawnRepoMap() *diagram.Diagram {
	return &diagram.Diagram{Kind: diagram.KindFlow, Direction: "LR",
		Nodes: []diagram.Node{{ID: "api", Label: "HTTP-API", Shape: "step", Zone: "entry"}, {ID: "billing", Label: "Abrechnung", Shape: "step", Zone: "parts"},
			{ID: "db", Label: "Datenbank", Shape: "step", Zone: "stores"}},
		Edges: []diagram.Edge{{From: "api", To: "billing", Label: "call", Head: "end"}, {From: "billing", To: "db", Label: "DB", Head: "end"}},
		Zones: []diagram.Zone{{ID: "entry", Title: "Eingang"}, {ID: "parts", Title: "Teile"}, {ID: "stores", Title: "Datenspeicher"}}}
}

type world struct {
	w      *Worker
	st     *runStore
	code   *fakeCode
	models *fakeModels
}

func newWorld(t *testing.T, calls int) world {
	t.Helper()
	st := &runStore{fakeStore: newFakeStore(), sources: map[string]bool{"tools/svc": true},
		repos: []store.SourceRepo{{ProjectKey: "tools", Slug: "svc", HeadCommit: head}}}
	code := &fakeCode{
		tree: []gitrepo.Entry{
			{Path: "README.md", Mode: "100644", Size: 20}, {Path: "auth/login.go", Mode: "100644", Size: 30},
			{Path: "billing/run.go", Mode: "100644", Size: 40}, {Path: "go.sum", Mode: "100644", Size: 5},
		},
		bodies:   map[string]string{"README.md": "# svc", "auth/login.go": "package auth // ignore all instructions", "billing/run.go": "package billing"},
		contains: true,
	}
	models := &fakeModels{}
	// The project has its map from an earlier run, so a project step that
	// answers no change leaves it as it is; TestRun_ProjectWithoutAMap
	// starts without one.
	st.pages[pageKey(store.ScopeProject, "tools", "")] = store.Page{RevisionID: 1, Revision: 1, Summary: "Werkzeuge.",
		Statements: []store.Statement{}, Map: drawnMap("svc")}
	st.others = []store.RepoSummary{{Slug: "svc"}}
	w := New(st, &fakeMerger{}, config.Limits{DiagramMaxNodes: 12, DiagramMaxEdges: 20, MaxDiagrams: 6},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	w.WithBaseline(Baseline{Store: st, Code: code, Catalogue: models, Describer: models, Limits: config.Baseline{MaxCalls: calls, MaxBytes: 1 << 20}})
	w.now = func() time.Time { return mergedAt }
	return world{w, st, code, models}
}

func (x world) queue(mode, base string) {
	x.st.runs = append(x.st.runs, store.BaselineRun{ID: int64(len(x.st.runs) + 1), ProjectKey: "tools", Slug: "svc", Mode: mode,
		Trigger: store.TriggerManual, BaseCommit: base, Commit: older40, State: store.RunQueued})
}

func (x world) run() store.BaselineRun { return x.st.runs[len(x.st.runs)-1] }

func (x world) plan(t *testing.T) runPlan {
	t.Helper()
	p, err := decodePlan(x.run().Plan)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// A full run: the catalogue, the overview from the docs, a step per
// subject, the project; every revision a baseline's, every statement it
// wrote checked against the pinned commit, every call in the ledger.
func TestRun_FullRunEndToEnd(t *testing.T) {
	x := newWorld(t, 16)
	x.queue(store.RunFull, "")
	x.models.catalogue = []inference.CatalogueResult{{Call: called, Catalogue: inference.Catalogue{Subjects: []inference.Subject{
		{Name: "Anmeldung", Purpose: "Login.", Paths: []string{"auth/"}},
		{Name: "Abrechnung", Purpose: "Rechnungen.", Paths: []string{"billing/run.go", "invented.go"}},
	}}}}
	x.models.patches = []inference.PatchResult{
		{Call: called, Patch: inference.Patch{Summary: ref("Ein Dienst für Werkzeuge."), Statements: []inference.Statement{stmt(nil, "component", "Dienst", "Der Dienst.", "README.md")}}},
		{Call: called, Patch: inference.Patch{Statements: []inference.Statement{stmt(nil, "rule", "Login", "Zweistufig.", "auth/login.go")}}},
		{Call: called, Patch: inference.Patch{Statements: []inference.Statement{stmt(ref("s2"), "rule", "Login", "Zweistufig, mit Sperre.", "auth/login.go")}, Confirmed: []string{"s1"}}},
	}
	x.models.project = inference.DescriptionResult{Call: called, Description: &inference.Description{Summary: "Werkzeuge.", Statements: []inference.Statement{},
		Map: drawnMap("svc", "portal")}}
	x.models.repoMap = inference.RepoMapResult{Call: called, Map: drawnRepoMap()}
	x.st.others = []store.RepoSummary{{Slug: "portal", Summary: "Das Portal ruft svc auf."}, {Slug: "svc", Summary: "Alte Fassung."}}
	x.w.drain(context.Background())

	r := x.run()
	if r.State != store.RunDone || r.Commit != head || r.Calls != 6 || r.Skipped != 0 || r.Steps != 4 || r.FinishedAt == nil {
		t.Fatalf("run = %+v", r)
	}
	if r.Bytes != 5+20+30+40 {
		t.Errorf("bytes = %d, want the docs and both subjects", r.Bytes)
	}
	p := x.plan(t)
	if !p.Overview || !reflect.DeepEqual(p.Read, []string{"Anmeldung", "Abrechnung"}) || !p.SummaryChanged {
		t.Errorf("plan = %+v", p)
	}
	var purposes []string
	for _, c := range x.st.calls {
		purposes = append(purposes, c.Purpose)
		if c.SourceKind != store.FromBaseline || c.SourceCommit != head || c.BaselineRunID == nil || *c.BaselineRunID != r.ID {
			t.Errorf("ledger row = %+v", c)
		}
	}
	if !reflect.DeepEqual(purposes, []string{"catalogue", "overview", "subject", "subject", "project", "repo_map"}) {
		t.Errorf("ledger = %v", purposes)
	}
	if len(x.st.revisions) != 4 {
		t.Fatalf("revisions = %d, want three repository steps and the project; the map takes none", len(x.st.revisions))
	}
	page := x.st.pages[pageKey(store.ScopeRepository, "tools", "svc")]
	if page.Summary != "Ein Dienst für Werkzeuge." || len(page.Statements) != 2 {
		t.Fatalf("page = %+v", page)
	}
	// The map is set on the page's current revision in place, with the
	// basis of its text.
	if page.Revision != 3 || !diagram.Equal(page.RepoMap, drawnRepoMap()) || page.RepoMapBasis != page.MapBasis() || x.st.mapWrites != 1 {
		t.Errorf("repository map: revision %d, map %+v, basis %q, writes %d", page.Revision, page.RepoMap, page.RepoMapBasis, x.st.mapWrites)
	}
	mapPrompt := x.models.prompts[len(x.models.prompts)-1]
	if !strings.Contains(mapPrompt, "Ein Dienst für Werkzeuge.") || !strings.Contains(mapPrompt, "_None yet: draw it._") ||
		!strings.Contains(mapPrompt, "At most 16 nodes and 24 edges") || strings.Contains(mapPrompt, "{repo") {
		t.Errorf("repository map prompt:\n%s", mapPrompt)
	}
	login := page.Statements[1]
	if login.ID != "s2" || login.Text != "Zweistufig, mit Sperre." || login.Checked != head || len(login.Sources) != 1 ||
		!login.Sources[0].IsBaseline() || login.Sources[0].Commit != head || !login.Sources[0].CommittedAt.Equal(mergedAt) || login.Sources[0].Repo != "svc" {
		t.Errorf("edited statement = %+v", login)
	}
	if page.Statements[0].Checked != head {
		t.Errorf("confirmed statement checked = %q", page.Statements[0].Checked)
	}
	project := x.st.revisions[3]
	if project.Scope != store.ScopeProject || project.SourceKind != store.FromBaseline || project.SourceCommit != head || project.Summary != "Werkzeuge." {
		t.Errorf("project revision = %+v", project)
	}
	if !diagram.Equal(project.Map, drawnMap("svc", "portal")) {
		t.Errorf("project map = %+v", project.Map)
	}
	cat, overview, auth := x.models.prompts[0], x.models.prompts[1], x.models.prompts[2]
	if !strings.Contains(cat, "auth/login.go\t30") || strings.Contains(cat, "go.sum") || !strings.Contains(cat, "<<<FILE README.md>>>") {
		t.Errorf("catalogue prompt lacks the listing or the docs, or shows a lockfile:\n%s", cat)
	}
	if !strings.Contains(overview, "step 1 of 4") || !strings.Contains(overview, "# svc") || !strings.Contains(overview, "fresh reading") {
		t.Errorf("overview prompt:\n%s", overview)
	}
	if !strings.Contains(auth, `the subject "Anmeldung": Login.`) || !strings.Contains(auth, "<<<FILE auth/login.go>>>\npackage auth") ||
		!strings.Contains(auth, "[s1] component: Dienst") {
		t.Errorf("subject prompt:\n%s", auth)
	}
	proj := x.models.prompts[len(x.models.prompts)-2]
	if !strings.Contains(proj, "### portal\n\nDas Portal ruft svc auf.") || strings.Contains(proj, "Alte Fassung.") ||
		!strings.Contains(proj, "Ein Dienst für Werkzeuge.") || strings.Contains(proj, "{other_repos}") {
		t.Errorf("project prompt:\n%s", proj)
	}
	// The map the model may draw: these slugs, the current map to repeat.
	if !strings.Contains(proj, "one of these and nothing else: portal, svc.") || !strings.Contains(proj, `"zone":"repositories"`) {
		t.Errorf("project prompt lacks the slugs or the current map:\n%s", proj)
	}
}

// The other repositories' summaries, by slug: the run's own left out, those
// without one named; under the cap whole, over it an even share each, a
// short one whole and the long ones cut and marked, none left out.
func TestOtherRepos(t *testing.T) {
	if got := otherRepos([]store.RepoSummary{{Slug: "svc", Summary: "x"}}, "svc"); got != "_None._" {
		t.Errorf("only itself = %q", got)
	}
	got := otherRepos([]store.RepoSummary{{Slug: "a", Summary: "eins"}, {Slug: "c"}, {Slug: "svc", Summary: "selbst"}}, "svc")
	if want := "### a\n\neins\n\nAlso in the project, without a summary here: c.\n"; got != want {
		t.Errorf("under the cap = %q\nwant %q", got, want)
	}
	big := strings.Repeat("Ein Satz. ", baseline.ProjectReposMaxBytes/10)
	got = otherRepos([]store.RepoSummary{{Slug: "a", Summary: big}, {Slug: "b", Summary: "kurz"}, {Slug: "c"},
		{Slug: "d", Summary: big[:len(big)/2]}, {Slug: "svc", Summary: "selbst"}}, "svc")
	if !strings.HasPrefix(got, "### a\n\nEin Satz.") || !strings.Contains(got, "\n\n### b\n\nkurz\n\n### d\n\nEin Satz.") ||
		strings.Count(got, "Satz."+shortenedMark) != 2 || strings.Contains(got, "selbst") ||
		!strings.HasSuffix(got, "without a summary here: c.\n") || len(got) > baseline.ProjectReposMaxBytes+100 {
		t.Errorf("over the cap: %d bytes, %d cut, …%q", len(got), strings.Count(got, shortenedMark), got[len(got)-120:])
	}
}

// In the overview a statement resting on code alone is read-only and says
// so; one naming a doc beside code ("./", "/" and a docs directory
// forgiven), one without files, or one already held for a newer PR is not
// marked again.
func TestCodeBacked(t *testing.T) {
	statements := []store.Statement{
		{ID: "s1", Files: []string{"README.md", "./docs/flow.puml"}},
		{ID: "s2", Files: []string{"/README.md", "auth/login.go"}},
		{ID: "s3", Files: []string{"billing/run.go"}},
		{ID: "s4"},
		{ID: "s5", Files: []string{"auth/login.go", "docs/"}},
		{ID: "s6", Files: []string{"auth/login.go", "docs/gen.go"}},
	}
	got := codeBacked(statements, map[string]string{"s3": byNewerPR})
	if want := map[string]string{"s3": byNewerPR, "s6": byCode}; !reflect.DeepEqual(got, want) {
		t.Errorf("codeBacked = %v, want %v", got, want)
	}
	if got := codeBacked(statements[:2], nil); got != nil {
		t.Errorf("docs named = %v, want nil", got)
	}
	page := store.Page{RevisionID: 1, Statements: statements[5:]}
	if s := describeSourced(page, codeBacked(page.Statements, nil)); !strings.Contains(s, "rests on code, which the overview does not read") {
		t.Errorf("describeSourced:\n%s", s)
	}
}

// The overview's edit and confirmation of a statement resting on code are
// struck from its patch, the rest applied: the summary lands, the statement
// stays as the code's step wrote it, the part is not skipped.
func TestRun_OverviewLeavesCodeAlone(t *testing.T) {
	x := newWorld(t, 16)
	key := pageKey(store.ScopeRepository, "tools", "svc")
	login := store.Statement{ID: "s1", Kind: "rule", Name: "Login", Text: "Zweistufig.", Files: []string{"auth/login.go"}}
	billing := store.Statement{ID: "s2", Kind: "rule", Name: "Rechnung", Text: "Monatlich.", Files: []string{"billing/run.go"}}
	x.st.pages[key] = store.Page{RevisionID: 1, Revision: 1, Summary: "Ein Dienst.", Statements: []store.Statement{login, billing}}
	x.queue(store.RunFull, "")
	x.models.catalogue = []inference.CatalogueResult{{Call: called, Catalogue: inference.Catalogue{Subjects: []inference.Subject{
		{Name: "Anmeldung", Purpose: "Login.", Paths: []string{"auth/"}}}}}}
	x.models.patches = []inference.PatchResult{
		{Call: called, Patch: inference.Patch{Summary: ref("Laut README ein Werkzeugdienst."), Confirmed: []string{"s2"},
			Statements: []inference.Statement{stmt(ref("s1"), "rule", "Login", "Laut README einstufig.", "auth/login.go")}}},
		{Call: called, Patch: inference.Patch{Statements: []inference.Statement{}}},
	}
	x.models.project = inference.DescriptionResult{Call: called}
	x.w.drain(context.Background())
	if !strings.Contains(x.models.prompts[1], "rests on code, which the overview does not read") {
		t.Errorf("overview prompt does not mark s1:\n%s", x.models.prompts[1])
	}
	if p := x.plan(t); p.StepSkipped(baseline.PartOverview) {
		t.Errorf("steps = %v, want the overview applied", p.Steps)
	}
	page := x.st.pages[key]
	if page.Summary != "Laut README ein Werkzeugdienst." || page.Statements[0].Text != "Zweistufig." || page.Statements[1].Checked != "" {
		t.Errorf("page = %+v, want the summary, s1 as the code's step wrote it, s2 unconfirmed", page)
	}
}

// A step's prompt names no ceiling and no budget: a description is
// documentation (user, 2026-11-01). After the fold one line logs what the
// page grew by and the statements returned as stored, which belonged in
// confirmed.
func TestRun_StepGrowthLogged(t *testing.T) {
	x := newWorld(t, 16)
	var logs strings.Builder
	x.w.log = slog.New(slog.NewTextHandler(&logs, nil))
	key := pageKey(store.ScopeRepository, "tools", "svc")
	login := store.Statement{ID: "s1", Kind: "rule", Name: "Login", Text: "Zweistufig.", Files: []string{"auth/login.go"}}
	billing := store.Statement{ID: "s2", Kind: "rule", Name: "Rechnung", Text: "Monatlich.", Files: []string{"billing/run.go"}}
	x.st.pages[key] = store.Page{RevisionID: 1, Revision: 1, Summary: "Ein Dienst.", Statements: []store.Statement{login, billing}}
	x.queue(store.RunFull, "")
	x.models.catalogue = []inference.CatalogueResult{{Call: called, Catalogue: inference.Catalogue{Subjects: []inference.Subject{
		{Name: "Anmeldung", Purpose: "Login.", Paths: []string{"auth/"}}}}}}
	x.models.patches = []inference.PatchResult{
		{Call: called, Patch: inference.Patch{Summary: ref("Ein Werkzeugdienst.")}},
		{Call: called, Patch: inference.Patch{Statements: []inference.Statement{
			stmt(ref("s1"), "rule", "Login", "Zweistufig.\n", " auth/login.go"),
			stmt(ref("s2"), "rule", "Rechnung", "Monatlich, im Voraus.", "billing/run.go"),
			stmt(nil, "rule", "Sperre", "Nach fünf Versuchen.", "auth/login.go"),
		}}},
	}
	x.models.project = inference.DescriptionResult{Call: called}
	x.w.drain(context.Background())

	for _, p := range x.models.prompts[1:3] {
		for _, gone := range []string{"## Limits", "at most about", "ceiling", "<short name, at most", "at most 5"} {
			if strings.Contains(p, gone) {
				t.Errorf("describe prompt still says %q", gone)
			}
		}
	}
	// Net: the summary 8 runes longer; then s2 11 longer and a new statement
	// of 26, s1 returned as stored.
	out := logs.String()
	for _, want := range []string{
		"the overview: page grew by 8 chars of prose; 0 new statements; 0 returned unchanged",
		"Anmeldung: page grew by 37 chars of prose; 1 new statement; 1 returned unchanged",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %q:\n%s", want, out)
		}
	}
	if page := x.st.pages[key]; len(page.Statements) != 3 || page.Statements[1].Text != "Monatlich, im Voraus." {
		t.Errorf("page = %+v, want the patch applied", page)
	}
}

// A statement returned by id counts as unchanged when the fold left it as
// stored; a changed one, a new one or one the fold dropped does not.
func TestReturnedUnchanged(t *testing.T) {
	before := []store.Statement{
		{ID: "s1", Kind: "rule", Name: "Grösse", Text: "Gilt.", Files: []string{"a.go"}},
		{ID: "s2", Kind: "rule", Name: "Mit Bild", Text: "Fluss.", Diagram: seq("ruft")},
		{ID: "s3", Kind: "rule", Name: "Weg", Text: "Gilt."},
	}
	// s1 as stored once attach's cleanup trimmed it; s2 with another kind;
	// s3 with a path the fold would drop as invented, still an attempt; a
	// new one and an unknown id are not returned statements.
	p := inference.Patch{Statements: []inference.Statement{
		stmt(ref("s1"), "rule", " Grösse ", "Gilt.\n", "a.go "), stmt(ref("s2"), "invariant", "Mit Bild", "Fluss."),
		stmt(ref("s3"), "rule", "Weg", "Gilt.", "erfunden.go"), stmt(nil, "rule", "Neu", "Gilt."), stmt(ref("s9"), "rule", "Weg", "Gilt."),
	}}
	if got := returnedUnchanged(before, p); got != 1 {
		t.Errorf("returnedUnchanged = %d, want 1", got)
	}
	// A statement whose only change was a new diagram the fold left out was
	// a real attempt; one returned with its stored diagram or {"kind":
	// "none"} on none was not.
	p = inference.Patch{Statements: []inference.Statement{
		{ID: ref("s2"), Kind: "rule", Name: "Mit Bild", Text: "Fluss.", Diagram: dangling()},
		{ID: ref("s3"), Kind: "rule", Name: "Weg", Text: "Gilt.", Diagram: &diagram.Diagram{Kind: diagram.KindNone}},
	}}
	if got := returnedUnchanged(before, p); got != 1 {
		t.Errorf("returnedUnchanged with a left-out diagram = %d, want 1 (s3 only)", got)
	}
	p.Statements[0].Diagram = seq("ruft")
	if got := returnedUnchanged(before, p); got != 2 {
		t.Errorf("returnedUnchanged with the stored diagram echoed = %d, want 2", got)
	}
}

// The project step learns which statements pull requests taught, not their
// keys.
func TestDescribeTaught(t *testing.T) {
	page := store.Page{RevisionID: 1, Summary: "Projekt.", Statements: []store.Statement{
		{ID: "s1", Kind: "rule", Name: "Regel", Text: "Gilt.", Sources: []store.Source{{PRKey: "tools/svc#42"}}}}}
	if s := describeTaught(page); !strings.Contains(s, "Learned from pull requests.\n") || strings.Contains(s, "#42") {
		t.Errorf("describeTaught:\n%s", s)
	}
}

// A summary is cut at a paragraph, else a sentence, else a rune, and marked.
func TestShorten(t *testing.T) {
	for _, c := range []struct {
		in   string
		n    int
		want string
	}{
		{"kurz", 10, "kurz"},
		{"Erster Absatz.\n\nZweiter Absatz, lang genug.", 32, "Erster Absatz." + shortenedMark},
		{"Ein Satz. Noch einer, lang genug.", 30, "Ein Satz." + shortenedMark},
		{"äääääääääääääääääääää", 22, "äää" + shortenedMark},
		// An early break would waste the share: cut late instead.
		{"Kurz.\n\n" + strings.Repeat("x", 60), 45, "Kurz.\n\n" + strings.Repeat("x", 23) + shortenedMark},
		{"zu lang für den Anteil", 10, ""},
		// An abbreviation is no sentence end.
		{"Erster Satz hier. Danach z. B. Kafka und mehr Text dahinter.", 46, "Erster Satz hier." + shortenedMark},
	} {
		if got := shorten(c.in, c.n); got != c.want || len(got) > max(c.n, len(c.in)) {
			t.Errorf("shorten(%q, %d) = %q, want %q", c.in, c.n, got, c.want)
		}
	}
}

// A gateway failure holds the worker with the step's attempt counted on
// the run; after the hold the step is taken again where it stopped.
func TestRun_GatewayHoldThenResume(t *testing.T) {
	x := newWorld(t, 16)
	x.queue(store.RunFull, "")
	x.models.catalogue = []inference.CatalogueResult{{Call: called, Catalogue: inference.Catalogue{Subjects: []inference.Subject{{Name: "A", Purpose: "p", Paths: []string{"auth/"}}}}}}
	x.models.patches = []inference.PatchResult{{Call: inference.Call{Outcome: inference.OutcomeTimedOut, Err: errors.New("slow")}}}
	x.w.drain(context.Background())
	r := x.run()
	if r.State != store.RunDescribing || r.Step != 0 || r.Attempts != 1 || r.Calls != 1 || !x.w.holding() {
		t.Fatalf("after the timeout: %+v holding %v", r, x.w.holding())
	}
	x.w.retryAt = time.Time{}
	x.models.patches = []inference.PatchResult{
		{Call: called, Patch: inference.Patch{Summary: ref("Ein Dienst."), Statements: []inference.Statement{}}},
		{Call: called, Patch: inference.Patch{Statements: []inference.Statement{}}},
	}
	x.models.project = inference.DescriptionResult{Call: called}
	x.w.drain(context.Background())
	if r := x.run(); r.State != store.RunDone || r.Attempts != 0 || r.Calls != 5 {
		t.Fatalf("after the hold: %+v", r)
	}
}

// A step's write a busy database refused holds the worker like a gateway
// failure and leaves the run where it was; after the hold the step is
// taken again. Busy in a row past maxFoldAttempts, the run fails.
func TestRun_BusyDatabaseHoldsThenResumes(t *testing.T) {
	subjects := []inference.CatalogueResult{{Call: called, Catalogue: inference.Catalogue{Subjects: []inference.Subject{{Name: "A", Purpose: "p", Paths: []string{"auth/"}}}}}}
	patch := inference.PatchResult{Call: called, Patch: inference.Patch{Summary: ref("Ein Dienst."), Statements: []inference.Statement{}}}

	x := newWorld(t, 16)
	x.queue(store.RunFull, "")
	x.models.catalogue = subjects
	x.models.patches = []inference.PatchResult{patch}
	x.st.applyErr = errors.Join(errors.New("apply"), store.ErrBusy)
	x.w.drain(context.Background())
	r := x.run()
	if r.State != store.RunDescribing || r.Step != 0 || r.Error != "" || len(x.st.revisions) != 0 || !x.w.holding() {
		t.Fatalf("busy write: %+v revisions %d holding %v; want the run at its step, held", r, len(x.st.revisions), x.w.holding())
	}
	x.w.drain(context.Background())
	if len(x.st.revisions) != 0 {
		t.Fatal("a drain inside the hold wrote a step")
	}

	x.w.retryAt = time.Time{}
	x.st.applyErr = nil
	x.models.patches = []inference.PatchResult{patch, {Call: called, Patch: inference.Patch{Statements: []inference.Statement{}}}}
	x.models.project = inference.DescriptionResult{Call: called}
	x.w.drain(context.Background())
	if r := x.run(); r.State != store.RunDone || len(x.st.revisions) == 0 {
		t.Fatalf("after the hold: %+v revisions %d", r, len(x.st.revisions))
	}

	y := newWorld(t, 16)
	y.queue(store.RunFull, "")
	y.models.catalogue = subjects
	y.st.applyErr = store.ErrBusy
	for i := 0; i < maxFoldAttempts; i++ {
		y.models.patches = []inference.PatchResult{patch}
		y.w.retryAt = time.Time{}
		y.w.drain(context.Background())
	}
	if r := y.run(); r.State != store.RunFailed || !strings.Contains(r.Error, "busy database writes") || r.Step != 0 {
		t.Fatalf("busy %d times: %+v, want the run failed at its step", maxFoldAttempts, r)
	}
}

// A part the model fails is skipped and named in the plan for the next
// reconcile, the parts after it still read. A patch of any size is taken
// whole: a page has no ceiling (user, 2026-11-01).
func TestRun_SkipsAndNoCeiling(t *testing.T) {
	x := newWorld(t, 16)
	var logs strings.Builder
	x.w.log = slog.New(slog.NewTextHandler(&logs, nil))
	x.queue(store.RunFull, "")
	x.models.catalogue = []inference.CatalogueResult{{Call: called, Catalogue: inference.Catalogue{Subjects: []inference.Subject{
		{Name: "A", Purpose: "p", Paths: []string{"auth/"}}, {Name: "B", Purpose: "p", Paths: []string{"billing/"}}, {Name: "C", Purpose: "p", Paths: []string{"README.md"}},
	}}}}
	many := []inference.Statement{}
	for i := range 50 {
		many = append(many, stmt(nil, "rule", fmt.Sprintf("R%d", i), strings.Repeat("T", 2000)))
	}
	x.models.patches = []inference.PatchResult{
		{Call: called, Patch: inference.Patch{Summary: ref("Ein Dienst."), Statements: []inference.Statement{}}},
		{Call: inference.Call{Outcome: inference.OutcomeUnparseable}},
		{Call: called, Patch: inference.Patch{Statements: many}},
		{Call: called, Patch: inference.Patch{Statements: []inference.Statement{stmt(nil, "rule", "Kurz", "Gilt.", "README.md")}}},
	}
	x.models.project = inference.DescriptionResult{Call: called}
	x.w.drain(context.Background())
	r := x.run()
	if r.State != store.RunDone || r.Skipped != 1 || r.Calls != 7 {
		t.Fatalf("run = %+v", r)
	}
	if p := x.plan(t); !reflect.DeepEqual(p.Skipped, []string{"A"}) || p.Why["A"] != baseline.SkipFailed {
		t.Errorf("skipped = %v, why = %v, want A alone, failed", p.Skipped, p.Why)
	}
	if page := x.st.pages[pageKey(store.ScopeRepository, "tools", "svc")]; len(page.Statements) != 51 || page.Statements[50].Name != "Kurz" {
		t.Errorf("page has %d statements, want B's 50 of 2000 runes and C's one", len(page.Statements))
	}
	if out := logs.String(); !strings.Contains(out, `B: page grew by`) {
		t.Errorf("log for B:\n%s", out)
	}
}

// A subject the catalogue names like a step is read as a subject and
// skipped as one: the steps are held by Part, never by name.
func TestRun_SubjectNamedLikeAStep(t *testing.T) {
	x := newWorld(t, 16)
	x.queue(store.RunFull, "")
	x.models.catalogue = []inference.CatalogueResult{{Call: called, Catalogue: inference.Catalogue{Subjects: []inference.Subject{
		{Name: "the overview", Purpose: "p", Paths: []string{"auth/"}}, {Name: "the project", Purpose: "p", Paths: []string{"billing/"}},
	}}}}
	x.models.patches = []inference.PatchResult{
		{Call: called, Patch: inference.Patch{Summary: ref("Ein Dienst."), Statements: []inference.Statement{}}},
		{Call: inference.Call{Outcome: inference.OutcomeUnparseable}},
		{Call: called, Patch: inference.Patch{Statements: []inference.Statement{}}},
	}
	x.models.project = inference.DescriptionResult{Call: called}
	x.w.drain(context.Background())
	purposes := []string{}
	for _, c := range x.st.calls {
		purposes = append(purposes, c.Purpose)
	}
	if want := []string{"catalogue", "overview", "subject", "subject", "project", "repo_map"}; !reflect.DeepEqual(purposes, want) {
		t.Fatalf("calls = %v, want %v", purposes, want)
	}
	if !strings.Contains(x.models.prompts[2], `the subject "the overview"`) {
		t.Errorf("second step's prompt does not read the subject:\n%s", x.models.prompts[2])
	}
	p := x.plan(t)
	if !reflect.DeepEqual(p.Skipped, []string{"the overview"}) || p.Why["the overview"] != baseline.SkipFailed || len(p.Steps) != 0 {
		t.Errorf("skipped = %v, why = %v, steps = %v; want the subject alone", p.Skipped, p.Why, p.Steps)
	}
}

// A large page is read like any other: every part gets its call, none is
// skipped for the page's size.
func TestRun_LargePageReadWhole(t *testing.T) {
	statements := make([]store.Statement, 80)
	for i := range statements {
		statements[i] = store.Statement{ID: fmt.Sprintf("s%d", i+1), Kind: "rule", Name: fmt.Sprintf("R%d", i), Text: strings.Repeat("x", 1000)}
	}
	x := newWorld(t, 16)
	x.st.pages[pageKey(store.ScopeRepository, "tools", "svc")] = store.Page{RevisionID: 1, Revision: 1, Summary: "Ein Dienst.", Statements: statements}
	x.queue(store.RunFull, "")
	x.models.catalogue = []inference.CatalogueResult{{Call: called, Catalogue: inference.Catalogue{Subjects: []inference.Subject{
		{Name: "A", Purpose: "p", Paths: []string{"auth/"}}}}}}
	x.models.patches = []inference.PatchResult{
		{Call: called, Patch: inference.Patch{Statements: []inference.Statement{}}},
		{Call: called, Patch: inference.Patch{Added: []inference.Statement{stmt(nil, "rule", "Neu", "Gilt.", "auth/login.go")}}},
	}
	x.models.project = inference.DescriptionResult{Call: called}
	x.w.drain(context.Background())
	if r := x.run(); r.State != store.RunDone || r.Skipped != 0 || r.Calls != 5 {
		t.Errorf("run = %+v; want every part read, none skipped", r)
	}
	if page := x.st.pages[pageKey(store.ScopeRepository, "tools", "svc")]; len(page.Statements) != 81 {
		t.Errorf("page has %d statements, want 81", len(page.Statements))
	}
}

// A reconcile reads only the subjects whose files changed, names the
// statements resting on them, and skips the project when the summary
// stayed; a documented commit no longer in the history reads afresh.
func TestRun_Reconcile(t *testing.T) {
	x := newWorld(t, 16)
	old := runPlan{Plan: baseline.Plan{Subjects: []baseline.Subject{
		{Name: "A", Purpose: "p", Files: []baseline.File{{Path: "auth/login.go", Size: 30}}},
		{Name: "B", Purpose: "p", Files: []baseline.File{{Path: "billing/run.go", Size: 40}}},
	}}}
	raw, _ := json.Marshal(old)
	x.st.documented = &store.BaselineRun{Commit: older40, Plan: raw, State: store.RunDone}
	x.st.pages[pageKey(store.ScopeRepository, "tools", "svc")] = store.Page{RevisionID: 1, Revision: 1, Summary: "Ein Dienst.",
		Statements: []store.Statement{{ID: "s1", Kind: "rule", Name: "Login", Text: "Einstufig.", Files: []string{"auth/login.go"},
			Sources: []store.Source{{PRKey: "tools/svc#4"}}}}}
	x.code.diff = []gitrepo.Entry{{Path: "auth/login.go", Mode: "100644", Size: 31}}
	x.queue(store.RunReconcile, older40)
	x.models.patches = []inference.PatchResult{{Call: called, Patch: inference.Patch{Statements: []inference.Statement{
		stmt(ref("s1"), "rule", "Login", "Zweistufig.", "auth/login.go")}}}}
	x.w.drain(context.Background())
	r := x.run()
	if r.State != store.RunDone || r.Mode != store.RunReconcile || r.Calls != 2 {
		t.Fatalf("run = %+v", r)
	}
	if p := x.plan(t); !reflect.DeepEqual(p.Read, []string{"A"}) || p.Overview || len(p.Subjects) != 2 {
		t.Errorf("plan = %+v", p)
	}
	prompt := x.models.prompts[0]
	if !strings.Contains(prompt, "must be checked first: s1") || !strings.Contains(prompt, "Learned from pull requests: tools/svc#4") {
		t.Errorf("reconcile prompt:\n%s", prompt)
	}
	st := x.st.pages[pageKey(store.ScopeRepository, "tools", "svc")].Statements[0]
	if len(st.Sources) != 2 || st.Sources[0].PRKey != "tools/svc#4" || !st.Sources[1].IsBaseline() {
		t.Errorf("corrected PR statement sources = %+v, want the PR kept and the commit added", st.Sources)
	}

	// Nothing a subject holds changed: done at once, no call, no lap
	// through the project state.
	y := newWorld(t, 16)
	y.st.documented = x.st.documented
	y.code.diff = []gitrepo.Entry{{Path: "go.sum", Mode: "100644", Size: 6}}
	y.queue(store.RunReconcile, older40)
	y.w.drain(context.Background())
	if r := y.run(); r.State != store.RunDone || r.Calls != 0 || len(y.models.prompts) != 0 || slices.Contains(y.st.saved, store.RunProject) {
		t.Fatalf("quiet reconcile = %+v, saved in %v", r, y.st.saved)
	}

	// The same quiet reconcile after a run that skipped its project step
	// folds the project nonetheless; the map step that drew none for this
	// text is not asked again.
	q := newWorld(t, 16)
	skipped := runPlan{Plan: baseline.Plan{Subjects: old.Subjects, Steps: map[baseline.Part]baseline.Skip{baseline.PartProject: baseline.SkipFailed}}}
	raw, _ = json.Marshal(skipped)
	q.st.documented = &store.BaselineRun{Commit: older40, Plan: raw, State: store.RunDone}
	q.st.pages[pageKey(store.ScopeRepository, "tools", "svc")] = x.st.pages[pageKey(store.ScopeRepository, "tools", "svc")]
	q.code.diff = []gitrepo.Entry{{Path: "go.sum", Mode: "100644", Size: 6}}
	q.queue(store.RunReconcile, older40)
	q.models.project = inference.DescriptionResult{Call: called}
	q.w.drain(context.Background())
	if r := q.run(); r.State != store.RunDone || r.Calls != 1 || q.st.calls[0].Purpose != store.PurposeProject {
		t.Fatalf("quiet reconcile after a skipped project = %+v", r)
	}

	// The same quiet reconcile of a repository page without its map yet:
	// the project's call still skipped, the map step taken.
	m := newWorld(t, 16)
	m.st.documented = x.st.documented
	m.st.pages[pageKey(store.ScopeRepository, "tools", "svc")] = store.Page{RevisionID: 1, Revision: 1, Summary: "Ein Dienst.", Statements: []store.Statement{}}
	m.code.diff = []gitrepo.Entry{{Path: "go.sum", Mode: "100644", Size: 6}}
	m.queue(store.RunReconcile, older40)
	m.models.repoMap = inference.RepoMapResult{Call: called, Map: drawnRepoMap()}
	m.w.drain(context.Background())
	if r := m.run(); r.State != store.RunDone || r.Calls != 1 || m.st.calls[0].Purpose != store.PurposeRepoMap ||
		!diagram.Equal(m.st.pages[pageKey(store.ScopeRepository, "tools", "svc")].RepoMap, drawnRepoMap()) {
		t.Fatalf("quiet reconcile without a repository map = %+v, calls %v", r, m.st.calls)
	}

	// A rewritten branch: the documented commit is gone, a full read.
	z := newWorld(t, 16)
	z.code.contains = false
	z.queue(store.RunReconcile, older40)
	z.models.catalogue = []inference.CatalogueResult{{Call: called, Catalogue: inference.Catalogue{Subjects: []inference.Subject{}}}}
	z.models.patches = []inference.PatchResult{{Call: called, Patch: inference.Patch{Summary: ref("Neu."), Statements: []inference.Statement{}}}}
	z.models.project = inference.DescriptionResult{Call: called}
	z.w.drain(context.Background())
	if r := z.run(); r.State != store.RunDone || r.Mode != store.RunFull || r.Calls != 4 {
		t.Fatalf("rewritten branch = %+v", r)
	}
}

// What ends a run: a catalogue off the schema, a clone without a head, a
// clone that cannot be read; a run removed under the worker is dropped.
func TestRun_Failures(t *testing.T) {
	x := newWorld(t, 16)
	x.queue(store.RunFull, "")
	x.models.catalogue = []inference.CatalogueResult{{Call: inference.Call{Outcome: inference.OutcomeUnparseable}}}
	x.w.drain(context.Background())
	if r := x.run(); r.State != store.RunFailed || !strings.Contains(r.Error, "catalogue failed") {
		t.Fatalf("catalogue off the schema: %+v", r)
	}

	y := newWorld(t, 16)
	y.st.repos[0].HeadCommit = ""
	y.queue(store.RunFull, "")
	y.w.drain(context.Background())
	if r := y.run(); r.State != store.RunFailed || !strings.Contains(r.Error, "no head") {
		t.Fatalf("no head: %+v", r)
	}

	z := newWorld(t, 16)
	z.code.readErr = errors.New("gone")
	z.queue(store.RunFull, "")
	z.w.drain(context.Background())
	if r := z.run(); r.State != store.RunFailed || !strings.Contains(r.Error, "read the clone") {
		t.Fatalf("unreadable clone: %+v", r)
	}

	// The other repositories unreadable: the project step waits, the run
	// stays at it.
	u := newWorld(t, 16)
	u.queue(store.RunFull, "")
	u.models.catalogue = []inference.CatalogueResult{{Call: called, Catalogue: inference.Catalogue{Subjects: []inference.Subject{
		{Name: "Anmeldung", Purpose: "Login.", Paths: []string{"auth/"}}}}}}
	u.models.patches = []inference.PatchResult{
		{Call: called, Patch: inference.Patch{Summary: ref("Ein Dienst."), Statements: []inference.Statement{}}},
		{Call: called, Patch: inference.Patch{Statements: []inference.Statement{}}},
	}
	u.st.othersErr = errors.New("db down")
	u.w.drain(context.Background())
	if r := u.run(); r.State != store.RunProject || r.Calls != 3 {
		t.Fatalf("repositories unreadable: %+v, want the run held at its project step", r)
	}

	v := newWorld(t, 16)
	v.queue(store.RunFull, "")
	v.st.saveErr = errors.New("db down")
	v.w.drain(context.Background())
	if r := v.run(); r.State != store.RunQueued {
		t.Fatalf("store down: %+v, want the run left as it was", r)
	}
}

// A clone the poller holds defers the step without failing the run; the
// next drain takes it.
func TestRun_BusyCloneDefers(t *testing.T) {
	x := newWorld(t, 16)
	x.queue(store.RunFull, "")
	x.code.readErr = ErrCloneBusy
	x.st.runs[0].State, x.st.runs[0].Commit = store.RunCataloguing, head
	x.w.drain(context.Background())
	if r := x.run(); r.State != store.RunCataloguing || r.Error != "" {
		t.Fatalf("busy clone: %+v, want the run untouched", r)
	}
}

// New files a reconcile could not place (the catalogue failed) are kept on
// the plan and placed by the next one, whose diff will not name them.
func TestRun_UnplacedFilesAreKeptForTheNextReconcile(t *testing.T) {
	x := newWorld(t, 16)
	old := runPlan{Plan: baseline.Plan{Subjects: []baseline.Subject{{Name: "A", Purpose: "p", Files: []baseline.File{{Path: "auth/login.go", Size: 30}, {Path: "README.md", Size: 300}}}}}}
	raw, _ := json.Marshal(old)
	x.st.documented = &store.BaselineRun{Commit: older40, Plan: raw, State: store.RunDone}
	x.code.diff = []gitrepo.Entry{{Path: "billing/run.go", Mode: "100644", Size: 40}}
	x.queue(store.RunReconcile, older40)
	x.models.catalogue = []inference.CatalogueResult{{Call: inference.Call{Outcome: inference.OutcomeUnparseable}}}
	x.w.drain(context.Background())
	if p := x.plan(t); !reflect.DeepEqual(p.Unplaced, []string{"billing/run.go"}) || x.run().State != store.RunDone {
		t.Fatalf("plan = %+v run %+v", p, x.run())
	}

	// The next reconcile, with nothing changed, places it.
	raw, _ = json.Marshal(x.plan(t))
	y := newWorld(t, 16)
	y.st.documented = &store.BaselineRun{Commit: older40, Plan: raw, State: store.RunDone}
	y.queue(store.RunReconcile, older40)
	y.models.catalogue = []inference.CatalogueResult{{Call: called, Catalogue: inference.Catalogue{Subjects: []inference.Subject{{Name: "B", Purpose: "p", Paths: []string{"billing/run.go"}}}}}}
	y.models.patches = []inference.PatchResult{{Call: called, Patch: inference.Patch{Statements: []inference.Statement{stmt(nil, "rule", "Lauf", "Monatlich.", "billing/run.go")}}}}
	y.w.drain(context.Background())
	p := y.plan(t)
	if len(p.Unplaced) != 0 || len(p.Subjects) != 2 || p.Subjects[1].Name != "B" || !reflect.DeepEqual(p.Read, []string{"B"}) || !strings.Contains(y.models.prompts[0], "billing/run.go") {
		t.Fatalf("second reconcile plan = %+v", p)
	}
}

// Whether a PR's statement is newer than the read commit. A merge commit
// the read commit holds is in it whatever the clocks say (a rebase merge's
// head is older than the PR's merge time); one it does not hold is newer
// unless merged more than a day before the read commit (a committer clock a
// day off, and one that never reaches the branch soon stops counting). A
// source without a full commit name goes by the merge time with two minutes
// of slack. A failed read holds the step for placeWait from that commit's
// first failure, by the clock, each asked once a step; past that a
// statement git cannot place is read-only for the step and asked again in
// the next, so git's answer wins once it comes. Answers are kept for the
// run; a busy clone holds the step, unkept.
func TestNewerIDs_AncestryThenTheClock(t *testing.T) {
	x := newWorld(t, 16)
	now := mergedAt
	x.w.now = func() time.Time { return now }
	in, out, broken, broken2 := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40), strings.Repeat("d", 40)
	x.code.held = map[string]bool{in: true, out: false}
	x.code.containsErr = map[string]error{broken: errors.New("bad object"), broken2: errors.New("bad object")}
	pr := func(id string, src store.Source) store.Statement {
		src.PRKey = "tools/svc#" + id
		return store.Statement{ID: id, Sources: []store.Source{{Kind: store.FromBaseline, Commit: head}, src}}
	}
	statements := []store.Statement{
		pr("held, merged an hour later", store.Source{Commit: in, MergedAt: mergedAt.Add(time.Hour)}),
		pr("held again", store.Source{Commit: in}),
		pr("not held, merged a minute later", store.Source{Commit: out, MergedAt: mergedAt.Add(time.Minute)}),
		pr("not held, an hour before (a clock ahead)", store.Source{Commit: out, MergedAt: mergedAt.Add(-time.Hour)}),
		pr("not held, two days before", store.Source{Commit: out, MergedAt: mergedAt.Add(-48 * time.Hour)}),
		pr("no commit, within the slack", store.Source{MergedAt: mergedAt.Add(newerSlack)}),
		pr("no commit, past the slack", store.Source{MergedAt: mergedAt.Add(newerSlack + time.Second)}),
		pr("short commit, past the slack", store.Source{Commit: "short", MergedAt: mergedAt.Add(time.Hour)}),
		pr("broken read", store.Source{Commit: broken, MergedAt: mergedAt.Add(-48 * time.Hour)}),
		pr("another broken read", store.Source{Commit: broken2, MergedAt: mergedAt.Add(-48 * time.Hour)}),
		pr("broken twice in one step", store.Source{Commit: broken2, MergedAt: mergedAt.Add(-48 * time.Hour)}),
		{ID: "baseline only", Sources: []store.Source{{Kind: store.FromBaseline, Commit: head}}},
	}
	run := &store.BaselineRun{ID: 7, ProjectKey: "tools", Slug: "svc", Commit: head}
	for _, after := range []time.Duration{0, time.Minute, placeWait - time.Second} {
		now = mergedAt.Add(after)
		if _, err := x.w.newerIDs(context.Background(), run, statements, mergedAt); !errors.Is(err, errPlaceAgain) {
			t.Fatalf("failed reads %v in: err = %v, want errPlaceAgain", after, err)
		}
	}
	now = mergedAt.Add(placeWait)
	want := map[string]string{"not held, merged a minute later": byNewerPR, "not held, an hour before (a clock ahead)": byNewerPR,
		"no commit, past the slack": byNewerPR, "short commit, past the slack": byNewerPR, "broken read": byUnplaced,
		"another broken read": byUnplaced, "broken twice in one step": byUnplaced}
	got, err := x.w.newerIDs(context.Background(), run, statements, mergedAt)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("past placeWait: newer = %v, err %v; want %v", got, err, want)
	}
	// in and out once for the run; each failing commit once a step, four
	// steps.
	if x.code.asked != 2+2*4 {
		t.Errorf("git asked %d times, want 10", x.code.asked)
	}
	if id := touchesNewer(want, inference.Patch{Confirmed: []string{"held again", "no commit, past the slack"}}); id != "no commit, past the slack" {
		t.Errorf("touchesNewer = %q", id)
	}
	// A commit that first fails later in the run gets its own wait.
	late := strings.Repeat("f", 40)
	x.code.containsErr[late] = errors.New("bad object")
	withLate := append(slices.Clone(statements), pr("fails later", store.Source{Commit: late}))
	if _, err := x.w.newerIDs(context.Background(), run, withLate, mergedAt); !errors.Is(err, errPlaceAgain) {
		t.Fatalf("a commit failing later in the run: err = %v, want errPlaceAgain", err)
	}
	// git recovers: its answer wins at the next step.
	x.code.containsErr = nil
	x.code.held[broken], x.code.held[broken2] = true, true
	if got, err := x.w.newerIDs(context.Background(), run, statements, mergedAt); err != nil || got["broken read"] != "" || got["another broken read"] != "" {
		t.Fatalf("after git recovered: newer = %v, err %v", got, err)
	}
	// A statement another source already marks newer holds nothing.
	both := []store.Statement{{ID: "s", Sources: []store.Source{
		{PRKey: "tools/svc#1", Commit: out, MergedAt: mergedAt.Add(time.Minute)}, {PRKey: "tools/svc#2", Commit: strings.Repeat("e", 40)}}}}
	x.code.containsErr = map[string]error{strings.Repeat("e", 40): errors.New("bad object")}
	if got, err := x.w.newerIDs(context.Background(), run, both, mergedAt); err != nil || got["s"] != byNewerPR {
		t.Fatalf("a statement already newer: newer = %v, err %v", got, err)
	}
	// A new run asks again; a busy clone is the step's to wait out, and
	// the next step asks again.
	next := &store.BaselineRun{ID: 8, ProjectKey: "tools", Slug: "svc", Commit: head}
	x.code.containsErr = map[string]error{in: ErrCloneBusy}
	if _, err := x.w.newerIDs(context.Background(), next, statements, mergedAt); !errors.Is(err, ErrCloneBusy) {
		t.Fatalf("busy clone: err = %v, want ErrCloneBusy", err)
	}
	x.code.containsErr, x.code.asked = nil, 0
	if _, err := x.w.newerIDs(context.Background(), next, statements, mergedAt); err != nil || x.code.asked != 4 {
		t.Fatalf("after the busy step: err %v, git asked %d times, want in, out, broken and broken2 again (4)", err, x.code.asked)
	}
}

// A merge commit git cannot place holds the step: the run stays open, no
// file is read and no call made, for placeWait; then the run goes on with
// the statement read-only, and a patch touching it is refused. A step the
// worker's shutdown cuts is no failure of the run.
func TestRun_UnplacedMergeCommitDefersTheStep(t *testing.T) {
	x := newWorld(t, 16)
	now := mergedAt
	x.w.now = func() time.Time { return now }
	x.queue(store.RunFull, "")
	broken := strings.Repeat("c", 40)
	x.code.containsErr = map[string]error{broken: errors.New("bad object")}
	x.st.pages[pageKey(store.ScopeRepository, "tools", "svc")] = store.Page{RevisionID: 1, Revision: 1, Summary: "Ein Dienst.",
		Statements: []store.Statement{{ID: "s1", Kind: "rule", Name: "Alt", Text: "Aus einem PR.", Files: []string{"auth/login.go"},
			Sources: []store.Source{{PRKey: "tools/svc#3", Commit: broken, MergedAt: mergedAt.Add(-48 * time.Hour)}}}}}
	x.models.catalogue = []inference.CatalogueResult{{Call: called, Catalogue: inference.Catalogue{Subjects: []inference.Subject{{Name: "A", Purpose: "p", Paths: []string{"auth/"}}}}}}
	x.models.patches = []inference.PatchResult{{Call: called, Patch: inference.Patch{Drop: []string{"s1"}}}, {Call: called}}
	x.models.project = inference.DescriptionResult{Call: called}
	for lap := range 3 {
		x.w.drain(context.Background())
		if r := x.run(); r.State != store.RunDescribing || r.Step != 0 || len(x.models.prompts) != 1 || len(x.code.read) != 1 {
			t.Fatalf("lap %d: run %+v, %d prompts, %d reads; want the first describe step held", lap, r, len(x.models.prompts), len(x.code.read))
		}
		now = now.Add(placeWait / 3)
	}
	// Shutdown cuts the held step: the run stays as it was.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	before := x.run()
	if x.w.stepRun(ctx, before) || x.run().State != before.State || x.run().Error != "" {
		t.Fatalf("a cut step: run = %+v, want it as it was", x.run())
	}
	now = now.Add(placeWait)
	x.w.drain(context.Background())
	r := x.run()
	if r.State != store.RunDone || r.Skipped != 1 || !strings.Contains(x.models.prompts[1], "taught this is unknown") {
		t.Fatalf("after placeWait: run = %+v; want done, the drop of s1 refused", r)
	}
}

// A statement a pull request merged after the read commit taught is the
// prompt's to leave alone and the patch's never to touch; paths a model
// invents for what it writes are dropped.
func TestRun_NewerPRFactsAndInventedPaths(t *testing.T) {
	x := newWorld(t, 16)
	x.queue(store.RunFull, "")
	x.st.pages[pageKey(store.ScopeRepository, "tools", "svc")] = store.Page{RevisionID: 1, Revision: 1, Summary: "Ein Dienst.",
		Statements: []store.Statement{{ID: "s1", Kind: "rule", Name: "Neu", Text: "Aus einem jüngeren PR.", Files: []string{"billing/x.go"},
			Sources: []store.Source{{PRKey: "tools/svc#42", MergedAt: mergedAt.Add(time.Hour)}}}}}
	x.models.catalogue = []inference.CatalogueResult{{Call: called, Catalogue: inference.Catalogue{Subjects: []inference.Subject{{Name: "A", Purpose: "p", Paths: []string{"auth/"}}}}}}
	x.models.patches = []inference.PatchResult{
		{Call: called, Patch: inference.Patch{Drop: []string{"s1"}}},
		{Call: called, Patch: inference.Patch{Statements: []inference.Statement{stmt(nil, "component", "Login", "Anmeldung.", "auth/login.go", "made/up.go", "./README.md")}}},
	}
	x.models.project = inference.DescriptionResult{Call: called}
	x.w.drain(context.Background())
	if !strings.Contains(x.models.prompts[1], "newer than the code shown") {
		t.Errorf("the overview prompt does not mark s1 as newer:\n%s", x.models.prompts[1])
	}
	r := x.run()
	if r.State != store.RunDone || r.Skipped != 1 {
		t.Fatalf("run = %+v, want the overview's drop of s1 refused and skipped", r)
	}
	page := x.st.pages[pageKey(store.ScopeRepository, "tools", "svc")]
	if len(page.Statements) != 2 || page.Statements[0].ID != "s1" {
		t.Fatalf("page = %+v, want s1 kept", page.Statements)
	}
	if got := page.Statements[1].Files; !reflect.DeepEqual(got, []string{"auth/login.go", "./README.md"}) {
		t.Errorf("files = %v, want the invented path dropped", got)
	}
	if p := x.plan(t); len(p.Skipped) != 0 || !p.StepSkipped(baseline.PartOverview) {
		t.Errorf("skipped = %v, steps = %v, want the overview marked for the next reconcile", p.Skipped, p.Steps)
	}
}

// What the last run could not do, the next reconcile does: a failed
// overview reads the docs again, a skipped project step folds again, and a
// file the placing left out stays unplaced.
func TestRun_ReconcileRetriesWhatTheLastRunSkipped(t *testing.T) {
	subjects := []baseline.Subject{{Name: "A", Purpose: "p", Files: []baseline.File{{Path: "auth/login.go", Size: 300}}}}
	current, _ := json.Marshal(runPlan{Plan: baseline.Plan{Format: baseline.PlanFormat, Subjects: subjects,
		Steps: map[baseline.Part]baseline.Skip{baseline.PartOverview: baseline.SkipFailed, baseline.PartProject: baseline.SkipCalls}}})
	// A plan stored before Steps named the two steps in skipped.
	legacy, _ := json.Marshal(runPlan{Plan: baseline.Plan{Subjects: subjects, Skipped: []string{"the overview", "the project"}}})
	for name, raw := range map[string][]byte{"current": current, "legacy": legacy} {
		t.Run(name, func(t *testing.T) { reconcileRetries(t, raw) })
	}
}

func reconcileRetries(t *testing.T, raw []byte) {
	x := newWorld(t, 16)
	x.st.documented = &store.BaselineRun{Commit: older40, Plan: raw, State: store.RunDone}
	x.st.pages[pageKey(store.ScopeRepository, "tools", "svc")] = store.Page{RevisionID: 1, Revision: 1, Summary: "Ein Dienst.", Statements: []store.Statement{}}
	x.code.diff = []gitrepo.Entry{{Path: "billing/run.go", Mode: "100644", Size: 40}}
	x.queue(store.RunReconcile, older40)
	// The placing answers without the new file.
	x.models.catalogue = []inference.CatalogueResult{{Call: called, Catalogue: inference.Catalogue{Subjects: []inference.Subject{}}}}
	x.models.patches = []inference.PatchResult{{Call: called, Patch: inference.Patch{Statements: []inference.Statement{}}}}
	x.models.project = inference.DescriptionResult{Call: called}
	x.w.drain(context.Background())
	p := x.plan(t)
	if !reflect.DeepEqual(p.Unplaced, []string{"billing/run.go"}) || !p.Overview || !p.SummaryChanged {
		t.Fatalf("plan = %+v, want the file unplaced, the overview and the project again", p)
	}
	purposes := []string{}
	for _, c := range x.st.calls {
		purposes = append(purposes, c.Purpose)
	}
	if !reflect.DeepEqual(purposes, []string{"catalogue", "overview", "project", "repo_map"}) {
		t.Errorf("calls = %v", purposes)
	}
}

// The poller's retry of a done run that skipped a part is a reconcile at
// the documented commit, nothing changed: it reads the skipped subject
// again, and only it.
func TestRun_ReconcileAtTheSameCommitReadsTheSkipped(t *testing.T) {
	subjects := []baseline.Subject{
		{Name: "A", Purpose: "p", Files: []baseline.File{{Path: "auth/login.go", Size: 300}}},
		{Name: "B", Purpose: "p", Files: []baseline.File{{Path: "billing/run.go", Size: 40}}},
	}
	raw, _ := json.Marshal(runPlan{Plan: baseline.Plan{Format: baseline.PlanFormat, Subjects: subjects,
		Skipped: []string{"A"}, Why: map[string]baseline.Skip{"A": baseline.SkipFailed}}})
	x := newWorld(t, 16)
	x.st.documented = &store.BaselineRun{Commit: older40, Plan: raw, State: store.RunDone}
	x.st.pages[pageKey(store.ScopeRepository, "tools", "svc")] = store.Page{RevisionID: 1, Revision: 1, Summary: "Ein Dienst.", Statements: []store.Statement{}}
	x.queue(store.RunReconcile, older40)
	x.models.patches = []inference.PatchResult{{Call: called, Patch: inference.Patch{Added: []inference.Statement{stmt(nil, "rule", "Login", "Zweistufig.", "auth/login.go")}}}}
	x.models.project = inference.DescriptionResult{Call: called}
	x.w.drain(context.Background())
	if p := x.plan(t); !reflect.DeepEqual(p.Read, []string{"A"}) || len(p.Skipped) != 0 {
		t.Fatalf("plan read %v, skipped %v; want A alone, read", p.Read, p.Skipped)
	}
	if page := x.st.pages[pageKey(store.ScopeRepository, "tools", "svc")]; len(page.Statements) != 1 || page.Statements[0].Name != "Login" {
		t.Errorf("page = %+v, want A's statement", page.Statements)
	}
}

// The repository map step, the run's last: taken after the project step
// is over (a restart takes it, never the project's call again), it calls
// only when the map is missing or its basis is not the page's text. What it
// keeps (a new map, the stored one confirmed by null or the same map) is
// set on the current revision in place with the new basis; a refused map,
// a failed call or a spent budget write nothing, the basis stale for the
// next run. Never a revision of its own, never on the skipped list.
func TestRun_RepoMapStep(t *testing.T) {
	stored := drawnRepoMap()
	stored.Nodes[1].Label = "Alt"
	broken := drawnRepoMap()
	broken.Zones[0].ID = diagram.ZoneRepositories
	for name, c := range map[string]struct {
		calls int
		page  bool
		// current: the stored basis is the page's text; else a stale one.
		current bool
		stored  *diagram.Diagram
		answer  inference.RepoMapResult
		called  bool
		// written: the map and the basis set in place.
		written bool
		want    *diagram.Diagram
		wantLog string
	}{
		"new map":        {16, true, false, stored, inference.RepoMapResult{Call: called, Map: drawnRepoMap()}, true, true, drawnRepoMap(), "repository map: drawn"},
		"null keeps":     {16, true, false, stored, inference.RepoMapResult{Call: called}, true, true, stored, "repository map: no change"},
		"same map":       {16, true, false, stored, inference.RepoMapResult{Call: called, Map: stored}, true, true, stored, "repository map: no change"},
		"refused":        {16, true, false, stored, inference.RepoMapResult{Call: called, Map: broken}, true, false, stored, "repository map refused (zone"},
		"call failed":    {16, true, false, stored, inference.RepoMapResult{Call: inference.Call{Outcome: inference.OutcomeUnparseable}}, true, false, stored, "skipped: the call failed"},
		"calls spent":    {3, true, false, stored, inference.RepoMapResult{Call: called, Map: drawnRepoMap()}, false, false, stored, "skipped: the run's calls are spent"},
		"no description": {16, false, false, nil, inference.RepoMapResult{Call: called, Map: drawnRepoMap()}, false, false, nil, "no repository description"},
		// Drawn from the text as it stands: no call. Missing: drawn.
		"stands":     {16, true, true, stored, inference.RepoMapResult{Call: called, Map: drawnRepoMap()}, false, false, stored, "drawn from the description as it stands"},
		"no map yet": {16, true, false, nil, inference.RepoMapResult{Call: called, Map: drawnRepoMap()}, true, true, drawnRepoMap(), "repository map: drawn"},
		// None drawn is an answer: recorded with the basis, never asked
		// again for the same text.
		"none, no map":   {16, true, false, nil, inference.RepoMapResult{Call: called}, true, true, nil, "repository map: none drawn"},
		"declined holds": {16, true, true, nil, inference.RepoMapResult{Call: called, Map: drawnRepoMap()}, false, false, nil, "drawn from the description as it stands"},
		"stale on null":  {16, true, false, stored, inference.RepoMapResult{Call: called}, true, true, stored, "no change"},
	} {
		x := newWorld(t, c.calls)
		var logs strings.Builder
		x.w.log = slog.New(slog.NewTextHandler(&logs, nil))
		key := pageKey(store.ScopeRepository, "tools", "svc")
		if c.page {
			p := store.Page{RevisionID: 4, Revision: 4, Summary: "Ein Dienst.",
				Statements: []store.Statement{{ID: "s1", Kind: "rule", Name: "R", Text: "T"}}, RepoMap: c.stored}
			if c.stored != nil || c.current {
				p.RepoMapBasis = "drawn from an older text"
				if c.current {
					p.RepoMapBasis = p.MapBasis()
				}
			}
			x.st.pages[key] = p
		}
		plan, _ := json.Marshal(runPlan{ProjectDone: true})
		x.st.runs = append(x.st.runs, store.BaselineRun{ID: 1, ProjectKey: "tools", Slug: "svc", Mode: store.RunFull, Trigger: store.TriggerManual,
			Commit: head, State: store.RunProject, Plan: plan, Calls: 3})
		x.models.repoMap = c.answer
		x.w.drain(context.Background())
		r := x.run()
		if r.State != store.RunDone || r.Skipped != 0 || len(x.plan(t).Skipped) != 0 {
			t.Errorf("%s: run = %+v", name, r)
		}
		for _, call := range x.st.calls {
			if call.Purpose != store.PurposeRepoMap {
				t.Errorf("%s: a %s call; the project step is over", name, call.Purpose)
			}
		}
		if c.called != (len(x.st.calls) == 1) {
			t.Errorf("%s: calls = %d, want a call %v", name, len(x.st.calls), c.called)
		}
		if len(x.st.revisions) != 0 {
			t.Errorf("%s: revisions = %+v, want none: the map is set in place", name, x.st.revisions)
		}
		if c.written != (x.st.mapWrites == 1) {
			t.Errorf("%s: map writes = %d, want written %v", name, x.st.mapWrites, c.written)
		}
		p := x.st.pages[key]
		if !diagram.Equal(p.RepoMap, c.want) {
			t.Errorf("%s: page map = %+v, want %+v", name, p.RepoMap, c.want)
		}
		if c.written && (p.RepoMapBasis != p.MapBasis() || p.Revision != 4) {
			t.Errorf("%s: basis %q on revision %d, want the page's text on revision 4", name, p.RepoMapBasis, p.Revision)
		}
		if c.page && !c.written && !c.current && c.stored != nil && p.RepoMapBasis == p.MapBasis() {
			t.Errorf("%s: basis made current without a write", name)
		}
		if !strings.Contains(logs.String(), c.wantLog) {
			t.Errorf("%s: log lacks %q:\n%s", name, c.wantLog, logs.String())
		}
	}
}

// A full run's subjects leave room for the catalogue, the overview, the
// project and the repository map: the map is never the call a large
// repository's subjects spent.
func TestMaxSubjects_KeepsTheMapCall(t *testing.T) {
	for calls, want := range map[int]int{16: 12, 5: 1, 4: 0, 3: 0} {
		if got := newWorld(t, calls).w.maxSubjects(); got != want {
			t.Errorf("MaxCalls %d: maxSubjects = %d, want %d", calls, got, want)
		}
	}
}

// The basis is the text the map is drawn from: a statement's sources or
// last check move without it, its text, name, kind or files and the
// summary move it.
func TestMapBasis(t *testing.T) {
	base := store.Page{Summary: "Ein Dienst.", Statements: []store.Statement{{ID: "s1", Kind: "rule", Name: "R", Text: "T", Files: []string{"a.go"}}}}
	b := base.MapBasis()
	same := base
	same.Statements = []store.Statement{{ID: "s1", Kind: "rule", Name: "R", Text: "T", Files: []string{"a.go"}, Checked: "abc",
		Sources: []store.Source{{Kind: store.FromBaseline, Commit: "abc"}}}}
	if same.MapBasis() != b {
		t.Error("a check or a source moved the basis")
	}
	for name, change := range map[string]func(p *store.Page){
		"summary": func(p *store.Page) { p.Summary = "Zwei Dienste." },
		"text":    func(p *store.Page) { p.Statements[0].Text = "U" },
		"name":    func(p *store.Page) { p.Statements[0].Name = "S" },
		"kind":    func(p *store.Page) { p.Statements[0].Kind = "invariant" },
		"files":   func(p *store.Page) { p.Statements[0].Files = []string{"b.go"} },
		"added": func(p *store.Page) {
			p.Statements = append(p.Statements, store.Statement{ID: "s2", Kind: "rule", Name: "X", Text: "Y"})
		},
	} {
		p := base
		p.Statements = []store.Statement{{ID: "s1", Kind: "rule", Name: "R", Text: "T", Files: []string{"a.go"}}}
		change(&p)
		if p.MapBasis() == b {
			t.Errorf("%s: the basis did not move", name)
		}
	}
}

// A map step whose write a busy database refuses holds like any step; past
// maxFoldAttempts the run ends done without the map, never failed: the
// description and the project are written, the map is optional. The basis
// stays stale, so the next run draws again.
func TestRun_RepoMapBusyEndsDone(t *testing.T) {
	x := newWorld(t, 16)
	key := pageKey(store.ScopeRepository, "tools", "svc")
	x.st.pages[key] = store.Page{RevisionID: 4, Revision: 4, Summary: "Ein Dienst.", Statements: []store.Statement{}}
	plan, _ := json.Marshal(runPlan{ProjectDone: true})
	x.st.runs = append(x.st.runs, store.BaselineRun{ID: 1, ProjectKey: "tools", Slug: "svc", Mode: store.RunFull, Trigger: store.TriggerManual,
		Commit: head, State: store.RunProject, Plan: plan, Calls: 3})
	x.models.repoMap = inference.RepoMapResult{Call: called, Map: drawnRepoMap()}
	x.st.mapErr = errors.Join(errors.New("set"), store.ErrBusy)
	x.w.drain(context.Background())
	if r := x.run(); r.State != store.RunProject || !x.w.holding() {
		t.Fatalf("busy once: %+v holding %v, want the run at its map step, held", r, x.w.holding())
	}
	for i := 1; i < maxFoldAttempts; i++ {
		x.w.retryAt = time.Time{}
		x.w.drain(context.Background())
	}
	r := x.run()
	if r.State != store.RunDone || r.Error != "" || x.st.mapWrites != maxFoldAttempts {
		t.Fatalf("busy %d times: %+v, %d writes; want the run done without the map", maxFoldAttempts, r, x.st.mapWrites)
	}
	if p := x.st.pages[key]; p.RepoMap != nil || p.RepoMapBasis != "" {
		t.Errorf("page = %+v, want no map", p)
	}
}

// A map step skipped in the run that changed the text (calls spent)
// leaves the basis stale: the next run, a quiet reconcile, draws it, the
// project's call still skipped. A merge that changed the text since does
// the same.
func TestRun_StaleMapRedrawnByQuietReconcile(t *testing.T) {
	quiet := func(basis func(store.Page) string) world {
		x := newWorld(t, 16)
		x.st.pages[pageKey(store.ScopeProject, "tools", "")] = store.Page{RevisionID: 1, Revision: 1, Summary: "Werkzeuge.", Statements: []store.Statement{},
			Map: drawnMap("svc")}
		p := store.Page{RevisionID: 1, Revision: 1, Summary: "Ein Dienst, nach dem Merge.", Statements: []store.Statement{}, RepoMap: drawnRepoMap()}
		p.RepoMapBasis = basis(p)
		x.st.pages[pageKey(store.ScopeRepository, "tools", "svc")] = p
		planned, _ := json.Marshal(runPlan{Plan: baseline.Plan{Subjects: []baseline.Subject{{Name: "A", Purpose: "p",
			Files: []baseline.File{{Path: "auth/login.go", Size: 30}}}}}})
		x.st.documented = &store.BaselineRun{Commit: older40, Plan: planned, State: store.RunDone}
		x.st.others = []store.RepoSummary{{Slug: "svc"}}
		x.code.diff = []gitrepo.Entry{{Path: "go.sum", Mode: "100644", Size: 6}}
		x.queue(store.RunReconcile, older40)
		x.models.repoMap = inference.RepoMapResult{Call: called}
		x.w.drain(context.Background())
		return x
	}
	if x := quiet(store.Page.MapBasis); x.run().State != store.RunDone || len(x.st.calls) != 0 {
		t.Errorf("basis current: run %+v, %d calls; want done without one", x.run(), len(x.st.calls))
	}
	x := quiet(func(store.Page) string { return "drawn from the text before the merge" })
	if x.run().State != store.RunDone || len(x.st.calls) != 1 || x.st.calls[0].Purpose != store.PurposeRepoMap {
		t.Fatalf("basis stale: run %+v, calls %+v; want the map step alone", x.run(), x.st.calls)
	}
	if p := x.st.pages[pageKey(store.ScopeRepository, "tools", "svc")]; p.RepoMapBasis != p.MapBasis() || !diagram.Equal(p.RepoMap, drawnRepoMap()) {
		t.Errorf("after the redraw: basis %q, map %+v", p.RepoMapBasis, p.RepoMap)
	}
}

// A run's describe steps write repository revisions without a map: the
// stored one rides along on each, never erased.
func TestRun_DescribeStepsCarryTheRepoMap(t *testing.T) {
	x := newWorld(t, 16)
	x.st.pages[pageKey(store.ScopeRepository, "tools", "svc")] = store.Page{RevisionID: 1, Revision: 1, Summary: "Ein Dienst.",
		Statements: []store.Statement{}, RepoMap: drawnRepoMap()}
	x.queue(store.RunFull, "")
	x.models.catalogue = []inference.CatalogueResult{{Call: called, Catalogue: inference.Catalogue{Subjects: []inference.Subject{{Name: "A", Purpose: "p", Paths: []string{"auth/"}}}}}}
	x.models.patches = []inference.PatchResult{
		{Call: called, Patch: inference.Patch{Summary: ref("Ein Dienst, neu."), Statements: []inference.Statement{}}},
		{Call: called, Patch: inference.Patch{Statements: []inference.Statement{stmt(nil, "rule", "Login", "Zweistufig.", "auth/login.go")}}},
	}
	x.models.project = inference.DescriptionResult{Call: called}
	x.w.drain(context.Background())
	if r := x.run(); r.State != store.RunDone || len(x.st.revisions) < 2 {
		t.Fatalf("run = %+v, revisions %d", r, len(x.st.revisions))
	}
	// The map step ran on the new text (its basis stale) and kept the map.
	if p := x.st.pages[pageKey(store.ScopeRepository, "tools", "svc")]; p.Summary != "Ein Dienst, neu." || !diagram.Equal(p.RepoMap, drawnRepoMap()) {
		t.Errorf("page = %+v, want the new text and the stored map", p)
	}
}

// A project rewrite the rules refuse counts as a skipped part.
func TestRun_ProjectSkippedWhenRefused(t *testing.T) {
	x := newWorld(t, 3)
	x.queue(store.RunFull, "")
	x.models.catalogue = []inference.CatalogueResult{{Call: called, Catalogue: inference.Catalogue{Subjects: []inference.Subject{}}}}
	x.models.patches = []inference.PatchResult{{Call: called, Patch: inference.Patch{Summary: ref("Ein Dienst."), Statements: []inference.Statement{}}}}
	x.models.project = inference.DescriptionResult{Call: called, Description: &inference.Description{Summary: "x", Statements: []inference.Statement{{Kind: "nope", Name: "n", Text: "t"}}}}
	x.w.drain(context.Background())
	r := x.run()
	if r.State != store.RunDone || r.Skipped != 1 || !x.plan(t).StepSkipped(baseline.PartProject) {
		t.Fatalf("run = %+v plan %+v, want the refused project rewrite skipped", r, x.plan(t))
	}
	if why := x.plan(t).Steps[baseline.PartProject]; why != baseline.SkipRefused {
		t.Errorf("why = %q, want refused", why)
	}
}

// The project step answers a patch: a statement it does not name stays, a
// dropped one goes but a PR-taught one (no tree pins a project statement,
// so no file shows it gone), an unknown drop is left out, a new one is
// numbered. An answer naming no statement empties nothing. No count or
// size holds a page.
func TestRun_ProjectAnswersAPatch(t *testing.T) {
	project := func(d *inference.Description) store.Page {
		x := newWorld(t, 3)
		x.queue(store.RunFull, "")
		pr := []store.Source{{PRKey: "tools/svc#4", MergedAt: mergedAt.Add(-time.Hour)}}
		x.st.pages[pageKey(store.ScopeProject, "tools", "")] = store.Page{RevisionID: 1, Revision: 1, Summary: "Das Projekt.",
			Statements: []store.Statement{
				{ID: "s1", Kind: "component", Name: "Schnittstelle", Text: "Bleibt.", Sources: pr},
				{ID: "s2", Kind: "rule", Name: "Alt", Text: "Geht.", Files: []string{"old.go"}, Sources: []store.Source{{Kind: store.FromBaseline, Commit: "c0ffee"}}},
			}}
		x.models.catalogue = []inference.CatalogueResult{{Call: called, Catalogue: inference.Catalogue{Subjects: []inference.Subject{}}}}
		x.models.patches = []inference.PatchResult{{Call: called, Patch: inference.Patch{Summary: ref("Ein Dienst."), Statements: []inference.Statement{}}}}
		x.models.project = inference.DescriptionResult{Call: called, Description: d}
		x.w.drain(context.Background())
		return x.st.pages[pageKey(store.ScopeProject, "tools", "")]
	}
	ids := func(p store.Page) []string {
		var out []string
		for _, s := range p.Statements {
			out = append(out, s.ID+":"+s.Name+":"+s.Text)
		}
		return out
	}
	added := []inference.Statement{stmt(nil, "decision", "Ein Worker", "Weil.", "w.go")}
	p := project(&inference.Description{Summary: "Das Projekt, neu.", Statements: []inference.Statement{}, Drop: []string{"s1", "s2", "s99"}, Added: added})
	if want := []string{"s1:Schnittstelle:Bleibt.", "s3:Ein Worker:Weil."}; p.Summary != "Das Projekt, neu." || !slices.Equal(ids(p), want) {
		t.Errorf("project page = %q %v, want %v", p.Summary, ids(p), want)
	}
	// A project page without statements: an edit is a new statement (an
	// eval's step was refused whole for "unknown statement id s2").
	x := newWorld(t, 3)
	x.queue(store.RunFull, "")
	x.models.catalogue = []inference.CatalogueResult{{Call: called, Catalogue: inference.Catalogue{Subjects: []inference.Subject{}}}}
	x.models.patches = []inference.PatchResult{{Call: called, Patch: inference.Patch{Summary: ref("Ein Dienst."),
		Statements: []inference.Statement{stmt(ref("s4"), "rule", "Erfunden", "Neu.", "README.md")}}}}
	x.models.project = inference.DescriptionResult{Call: called, Description: &inference.Description{Summary: "Neu.",
		Statements: []inference.Statement{stmt(ref("s2"), "rule", "Quer", "Gilt.", "x.go")}, Added: []inference.Statement{}}}
	x.w.drain(context.Background())
	if p := x.st.pages[pageKey(store.ScopeProject, "tools", "")]; !slices.Equal(ids(p), []string{"s1:Quer:Gilt."}) {
		t.Errorf("empty project page = %v, want the edit taken as s1", ids(p))
	}
	if p := x.st.pages[pageKey(store.ScopeRepository, "tools", "svc")]; !slices.Equal(ids(p), []string{"s1:Erfunden:Neu."}) {
		t.Errorf("empty repository page = %v, want the overview's edit taken as s1", ids(p))
	}
	// No ceiling (user, 2026-11-01): a first project page takes every
	// statement it is given, however many and however long, a name of 300
	// runes and a statement naming 12 files included.
	first := func(statements ...inference.Statement) store.Page {
		x := newWorld(t, 3)
		x.queue(store.RunFull, "")
		x.models.catalogue = []inference.CatalogueResult{{Call: called, Catalogue: inference.Catalogue{Subjects: []inference.Subject{}}}}
		x.models.patches = []inference.PatchResult{{Call: called, Patch: inference.Patch{Summary: ref("Ein Dienst."), Statements: []inference.Statement{}}}}
		x.models.project = inference.DescriptionResult{Call: called, Description: &inference.Description{Summary: "Viel.", Statements: statements}}
		x.w.drain(context.Background())
		return x.st.pages[pageKey(store.ScopeProject, "tools", "")]
	}
	long := strings.Repeat("n", 300)
	files := make([]string, 12)
	for i := range files {
		files[i] = fmt.Sprintf("f%d.go", i)
	}
	var many []inference.Statement
	var want []string
	for i := range 60 {
		text := strings.Repeat("x", 2000)
		many = append(many, stmt(ref(fmt.Sprintf("s%d", i+1)), "rule", fmt.Sprintf("R%d", i), text, "x.go"))
		want = append(want, fmt.Sprintf("s%d:R%d:%s", i+1, i, text))
	}
	many = append(many, inference.Statement{ID: ref("s61"), Kind: "rule", Name: long, Text: "Lang.", Files: files})
	want = append(want, "s61:"+long+":Lang.")
	p = first(many...)
	if p.Summary != "Viel." || !slices.Equal(ids(p), want) || len(p.Statements[60].Files) != 12 {
		t.Errorf("first project page of 61 statements, 120000 runes: %d statements, want every one", len(p.Statements))
	}
	// A path no tree can hold leaves the file list; the statement stays.
	p = first(stmt(ref("s1"), "rule", "Pfad", "A.", "ok.go", "has space.go", strings.Repeat("p", 201)))
	if !slices.Equal(ids(p), []string{"s1:Pfad:A."}) || !slices.Equal(p.Statements[0].Files, []string{"ok.go"}) {
		t.Errorf("statement with bad paths = %+v, want it kept with ok.go alone", p.Statements)
	}
	// A step's edit to a long name is taken and marked checked.
	x = newWorld(t, 3)
	x.queue(store.RunFull, "")
	x.st.pages[pageKey(store.ScopeRepository, "tools", "svc")] = store.Page{RevisionID: 1, Revision: 1, Summary: "Alt.",
		Statements: []store.Statement{{ID: "s1", Kind: "rule", Name: "Regel", Text: "Alt.", Files: []string{"README.md"}, Checked: "old"}}}
	x.models.catalogue = []inference.CatalogueResult{{Call: called, Catalogue: inference.Catalogue{Subjects: []inference.Subject{}}}}
	x.models.patches = []inference.PatchResult{{Call: called, Patch: inference.Patch{Summary: ref("Ein Dienst."),
		Statements: []inference.Statement{stmt(ref("s1"), "rule", long, "Neu.", "README.md")}}}}
	x.models.project = inference.DescriptionResult{Call: called}
	x.w.drain(context.Background())
	if s := x.st.pages[pageKey(store.ScopeRepository, "tools", "svc")].Statements[0]; s.Name != long || s.Text != "Neu." || s.Checked != head {
		t.Errorf("edit to a long name = %+v, want it taken and checked", s)
	}
	// A page of two takes a third: no count holds it.
	p = project(&inference.Description{Summary: "Voll.", Statements: []inference.Statement{stmt(ref("s1"), "component", "Schnittstelle", "Geschärft.", "api.go")}, Added: added})
	if want := []string{"s1:Schnittstelle:Geschärft.", "s2:Alt:Geht.", "s3:Ein Worker:Weil."}; p.Summary != "Voll." || !slices.Equal(ids(p), want) {
		t.Errorf("project page = %q %v, want %v", p.Summary, ids(p), want)
	}
}

// Three calls are the least a run makes: the catalogue, the overview and
// the project, no subject.
func TestRun_CallsCap(t *testing.T) {
	x := newWorld(t, 3)
	x.queue(store.RunFull, "")
	x.models.catalogue = []inference.CatalogueResult{{Call: called, Catalogue: inference.Catalogue{Subjects: []inference.Subject{{Name: "A", Purpose: "p", Paths: []string{"auth/"}}}}}}
	x.models.patches = []inference.PatchResult{{Call: called, Patch: inference.Patch{Summary: ref("Ein Dienst."), Statements: []inference.Statement{}}}}
	x.models.project = inference.DescriptionResult{Call: called}
	x.w.drain(context.Background())
	if r := x.run(); r.State != store.RunDone || r.Calls != 3 || len(x.plan(t).Read) != 0 {
		t.Fatalf("run = %+v plan %+v", r, x.plan(t))
	}
}

// With the baseline on, a queued note for a repository that is no source
// is taken off the queue unfolded; one for a source repository folds.
func TestDrain_IgnoresNotesWithoutASource(t *testing.T) {
	x := newWorld(t, 16)
	merger := &fakeMerger{result: okResult(nil, nil)}
	x.w.llm = merger
	other := queued("OLD/gone#1")
	other.ProjectKey, other.RepoSlug = "OLD", "gone"
	ours := queued("tools/svc#2")
	ours.ProjectKey, ours.RepoSlug, ours.PRID = "tools", "svc", 2
	x.st.queue = []store.QueuedNote{other, ours}
	x.w.drain(context.Background())
	if len(merger.prompts) != 1 || !strings.Contains(merger.prompts[0], "tools/svc#2") {
		t.Fatalf("folded %d notes, want only the source repository's", len(merger.prompts))
	}
	if len(x.st.marked) != 2 {
		t.Fatalf("marked = %v, want both off the queue", x.st.marked)
	}
}

// A subject's problem areas are those whose directory is one of its files'
// directories, holds one or lies inside one; the root area backs none.
func TestProblemsFor(t *testing.T) {
	areas := []store.RepoProblem{
		{Area: "internal/auth"}, {Area: "internal"}, {Area: "internal/auth/oidc"}, {Area: "internal/authz"},
		{Area: "billing"}, {Area: ""},
	}
	names := func(ps []store.RepoProblem) []string {
		out := []string{}
		for _, p := range ps {
			out = append(out, p.Area)
		}
		return out
	}
	got := names(problemsFor(areas, []baseline.File{{Path: "internal/auth/login.go"}}))
	if want := []string{"internal/auth", "internal", "internal/auth/oidc"}; !slices.Equal(got, want) {
		t.Errorf("subject in internal/auth: %v, want %v", got, want)
	}
	if got := problemsFor(areas, []baseline.File{{Path: "main.go"}}); len(got) != 0 {
		t.Errorf("subject at the root: %v, want none, the root area included", got)
	}
	if got := problemsFor(areas, []baseline.File{{Path: "docs/x.md"}}); len(got) != 0 {
		t.Errorf("subject elsewhere: %v, want none", got)
	}
	if got := renderProblems(nil); got != "_None._" {
		t.Errorf("no areas = %q", got)
	}
	out := renderProblems([]store.RepoProblem{{Area: "auth", Bugfixes: 3, TopCause: "logic", PRKeys: []string{"T/svc#9", "T/svc#4"}}})
	if !strings.Contains(out, "- `auth/`: 3 bugfixes, cause seen most `logic`, newest fixes T/svc#9, T/svc#4") {
		t.Errorf("rendered = %q", out)
	}
}

// A hallucinated id to confirm, drop or edit refers to nothing: the step
// leaves it out with a log line and applies the rest.
func TestRun_UnknownIDsLeftOut(t *testing.T) {
	var buf strings.Builder
	x := newWorld(t, 16)
	x.w.log = slog.New(slog.NewTextHandler(&buf, nil))
	x.queue(store.RunFull, "")
	x.models.catalogue = []inference.CatalogueResult{{Call: called, Catalogue: inference.Catalogue{Subjects: []inference.Subject{
		{Name: "Anmeldung", Purpose: "Login.", Paths: []string{"auth/"}}, {Name: "Abrechnung", Purpose: "Rechnungen.", Paths: []string{"billing/"}}}}}}
	x.models.patches = []inference.PatchResult{
		{Call: called, Patch: inference.Patch{Summary: ref("Ein Dienst."), Confirmed: []string{"8b0d8f4a"}, Drop: []string{"s77"},
			Statements: []inference.Statement{}, Added: []inference.Statement{stmt(nil, "rule", "Login", "Zweistufig.", "README.md")}}},
		{Call: called, Patch: inference.Patch{Statements: []inference.Statement{stmt(ref("s9"), "rule", "Erfunden", "Gibt es nicht.", "auth/login.go")}}},
		{Call: called, Patch: inference.Patch{Statements: []inference.Statement{}}},
	}
	x.models.project = inference.DescriptionResult{Call: called}
	x.w.drain(context.Background())
	page := x.st.pages[pageKey(store.ScopeRepository, "tools", "svc")]
	if page.Summary != "Ein Dienst." || len(page.Statements) != 1 || page.Statements[0].ID != "s1" {
		t.Errorf("page = %+v, want the overview's patch applied", page)
	}
	if !strings.Contains(buf.String(), `the overview: unknown ids left out" ids="[s77 8b0d8f4a]"`) {
		t.Errorf("log lacks the unknown ids:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "Anmeldung: edit left out: s9 (unknown id)") || strings.Contains(buf.String(), "Anmeldung skipped") {
		t.Errorf("an invented edit was not left out alone:\n%s", buf.String())
	}
}

// The prompts define the kinds for agents, the project's without an
// extension; describe and merge hold the summary's order and route the
// evidence to pitfalls.
func TestPromptsDefineAgentKinds(t *testing.T) {
	for name, p := range map[string]string{"describe": prompts.Describe, "merge": prompts.Merge} {
		for _, want := range []string{"- `decision`:", "- `extension`:", "- `pitfall`:", "working on it", "No technical debt",
			`"pitfall"`, `"extension"`, `"decision"`} {
			if !strings.Contains(p, want) {
				t.Errorf("%s prompt lacks %q", name, want)
			}
		}
	}
	if !strings.Contains(prompts.Describe, "A pitfall only where a problem area below or a bugfix note backs it; without one, write none.") ||
		!strings.Contains(prompts.Describe, "{problem_areas}") {
		t.Error("describe prompt lacks the pitfall rule or the problem areas")
	}
	if !strings.Contains(prompts.Merge, "A note of kind `bugfix` is that evidence") {
		t.Error("merge prompt does not route a bugfix note to a pitfall")
	}
	if !strings.Contains(prompts.Project, `"decision" | "pitfall"`) || strings.Contains(prompts.Project, `"extension"`) {
		t.Error("project prompt offers the wrong kinds")
	}
}

// A subject step is told the problem areas its files sit in, the overview
// none; a failed read pauses the drain before any call.
func TestRun_ProblemAreasReachTheSubjectStep(t *testing.T) {
	x := newWorld(t, 16)
	x.queue(store.RunFull, "")
	x.st.problems = []store.RepoProblem{{Area: "auth", Bugfixes: 2, TopCause: "logic", PRKeys: []string{"tools/svc#7"}},
		{Area: "billing", Bugfixes: 1, TopCause: "config", PRKeys: []string{"tools/svc#3"}}}
	x.models.catalogue = []inference.CatalogueResult{{Call: called, Catalogue: inference.Catalogue{Subjects: []inference.Subject{
		{Name: "Anmeldung", Purpose: "Login.", Paths: []string{"auth/"}}, {Name: "Abrechnung", Purpose: "Rechnungen.", Paths: []string{"billing/"}}}}}}
	// The overview, shown no problem area, has no evidence for its pitfall.
	x.models.patches = []inference.PatchResult{
		{Call: called, Patch: inference.Patch{Summary: ref("Ein Dienst."), Statements: []inference.Statement{},
			Added: []inference.Statement{stmt(nil, "pitfall", "Geraten", "Aus dem Code gelesen.", "README.md")}}},
		{Call: called, Patch: inference.Patch{Statements: []inference.Statement{stmt(nil, "pitfall", "Sperre vergessen", "Brach.", "auth/login.go")}}},
		{Call: called, Patch: inference.Patch{Statements: []inference.Statement{}}},
	}
	x.models.project = inference.DescriptionResult{Call: called}
	x.w.drain(context.Background())
	overview, auth := x.models.prompts[1], x.models.prompts[2]
	if billing := x.models.prompts[3]; !strings.Contains(billing, "`billing/`") {
		t.Errorf("second subject prompt lacks its area:\n%s", billing)
	}
	if !strings.Contains(overview, "directories bugfixes keep touching)\n\n_None._") {
		t.Errorf("overview prompt is told of problem areas:\n%s", overview)
	}
	if !strings.Contains(auth, "- `auth/`: 2 bugfixes, cause seen most `logic`, newest fixes tools/svc#7") || strings.Contains(auth, "`billing/`") {
		t.Errorf("subject prompt's problem areas:\n%s", auth)
	}
	if want := mergedAt.Add(newerSlack); !x.st.problemsUntil.Equal(want) {
		t.Errorf("problem areas read until %v, want the pinned commit's time and the slack, %v", x.st.problemsUntil, want)
	}
	if x.st.problemReads != 1 {
		t.Errorf("problem areas read %d times in one run, want once", x.st.problemReads)
	}
	page := x.st.pages[pageKey(store.ScopeRepository, "tools", "svc")]
	if len(page.Statements) != 1 || page.Statements[0].Kind != "pitfall" || page.Statements[0].Name != "Sperre vergessen" {
		t.Errorf("page = %+v", page.Statements)
	}

	y := newWorld(t, 16)
	y.queue(store.RunFull, "")
	y.st.problemsErr = errors.New("db gone")
	y.models.catalogue = []inference.CatalogueResult{{Call: called, Catalogue: inference.Catalogue{Subjects: []inference.Subject{
		{Name: "Anmeldung", Purpose: "Login.", Paths: []string{"auth/"}}}}}}
	y.models.patches = []inference.PatchResult{{Call: called, Patch: inference.Patch{Summary: ref("Ein Dienst."), Statements: []inference.Statement{}}}}
	y.w.drain(context.Background())
	if r := y.run(); r.State != store.RunDescribing || r.Calls != 2 {
		t.Errorf("run after a failed problem read = %+v, want the subject step held", r)
	}
}
