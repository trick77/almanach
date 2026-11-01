package consolidate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/trick77/almanach/internal/config"
	"github.com/trick77/almanach/internal/diagram"
	"github.com/trick77/almanach/internal/inference"
	"github.com/trick77/almanach/internal/lang"
	"github.com/trick77/almanach/internal/prompts"
	"github.com/trick77/almanach/internal/store"
	"github.com/trick77/llmwire"
)

var (
	limits = config.Limits{RepoMaxStatements: 3, ProjectMaxStatements: 2, RepoMaxChars: 400, ProjectMaxChars: 200,
		DiagramMaxNodes: 4, DiagramMaxEdges: 3, MaxDiagrams: 1}
	mergedAt = time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	older    = time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
)

// fakeStore is the worker's store in memory: pages by scope|project|repo,
// a queue popped oldest-first, every write recorded.
type fakeStore struct {
	queue        []store.QueuedNote
	queueErr     error
	markErr      error
	deferErr     error
	attempts     map[string]int
	pages        map[string]store.Page
	marked       []string
	revisions    []store.NewRevision
	calls        []store.LLMCall
	nextQueuedN  int
	revisionErr  error
	pageErr      error
	insertCallID int64
	// history is every statement id a page ever used beyond the current
	// page and the revisions written in the test: what NextStatementID
	// reads off the revisions table.
	history map[string][]string
}

func (f *fakeStore) NextStatementID(_ context.Context, scope, project, repo string) (int, error) {
	n := 1
	bump := func(id string) {
		var v int
		if _, err := fmt.Sscanf(id, "s%d", &v); err == nil && v >= n {
			n = v + 1
		}
	}
	for _, id := range f.history[pageKey(scope, project, repo)] {
		bump(id)
	}
	for _, s := range f.pages[pageKey(scope, project, repo)].Statements {
		bump(s.ID)
	}
	for _, r := range f.revisions {
		if r.Scope == scope && r.ProjectKey == project && r.RepoSlug == repo {
			for _, s := range r.Statements {
				bump(s.ID)
			}
		}
	}
	return n, nil
}

func newFakeStore() *fakeStore {
	return &fakeStore{pages: map[string]store.Page{}, insertCallID: 77}
}

func pageKey(scope, project, repo string) string { return scope + "|" + project + "|" + repo }

func (f *fakeStore) NextQueued(context.Context) (store.QueuedNote, bool, error) {
	f.nextQueuedN++
	if f.queueErr != nil {
		return store.QueuedNote{}, false, f.queueErr
	}
	if len(f.queue) == 0 {
		return store.QueuedNote{}, false, nil
	}
	q := f.queue[0]
	f.queue = f.queue[1:]
	return q, true, nil
}

func (f *fakeStore) MarkConsolidated(_ context.Context, project, repo string, prID int) error {
	if f.markErr != nil {
		return f.markErr
	}
	f.marked = append(f.marked, pageKey(project, repo, strconv.Itoa(prID)))
	return nil
}

func (f *fakeStore) DeferFold(_ context.Context, project, repo string, prID int) (int, error) {
	if f.deferErr != nil {
		return 0, f.deferErr
	}
	if f.attempts == nil {
		f.attempts = map[string]int{}
	}
	k := pageKey(project, repo, strconv.Itoa(prID))
	f.attempts[k]++
	return f.attempts[k], nil
}

func (f *fakeStore) CurrentPage(_ context.Context, scope, project, repo string) (store.Page, bool, error) {
	if f.pageErr != nil {
		return store.Page{}, false, f.pageErr
	}
	p, ok := f.pages[pageKey(scope, project, repo)]
	// The store's read relays text, as store.CurrentPage does.
	p.Summary = lang.Relaid(p.Summary, false)
	p.Statements = slices.Clone(p.Statements)
	for i := range p.Statements {
		p.Statements[i].Text = lang.Relaid(p.Statements[i].Text, true)
	}
	return p, ok, nil
}

func (f *fakeStore) InsertRevision(_ context.Context, rev store.NewRevision) (int, error) {
	if f.revisionErr != nil {
		return 0, f.revisionErr
	}
	f.revisions = append(f.revisions, rev)
	return len(f.revisions), nil
}

func (f *fakeStore) InsertLLMCall(_ context.Context, c store.LLMCall) (int64, error) {
	f.calls = append(f.calls, c)
	return f.insertCallID, nil
}

// fakeMerger answers every call with one canned result and records the
// prompts it was given.
type fakeMerger struct {
	result  inference.MergeResult
	prompts []string
}

func (m *fakeMerger) Merge(_ context.Context, req inference.MergeRequest) inference.MergeResult {
	m.prompts = append(m.prompts, req.Prompt)
	res := m.result
	res.Merge.Repository, res.Merge.Project = answered(res.Merge.Repository), answered(res.Merge.Project)
	return res
}

// A fixture writes a new statement as one without an id among the rest;
// the fakes answer in the model's shape, the new ones split off into Added,
// where the schema offers no id.
func split(list []inference.Statement) (edits, added []inference.Statement) {
	for _, s := range list {
		if s.ID == nil {
			added = append(added, s)
		} else {
			edits = append(edits, s)
		}
	}
	if edits == nil && list != nil {
		edits = []inference.Statement{}
	}
	return edits, added
}

func answered(d *inference.Description) *inference.Description {
	if d == nil || d.Added != nil {
		return d
	}
	c := *d
	c.Statements, c.Added = split(d.Statements)
	return &c
}

func okResult(repo, project *inference.Description) inference.MergeResult {
	nano := int64(2_000_000)
	return inference.MergeResult{Merge: inference.Merge{Repository: repo, Project: project}, Call: inference.Call{Outcome: inference.OutcomeOK,
		Cost: inference.CallCost{Model: "gpt-x", CallID: "call-9", InputTokens: 100, CachedTokens: 20, OutputTokens: 10, NanoUSD: &nano, Answered: true}}}
}

func queued(prKey string) store.QueuedNote {
	return store.QueuedNote{Note: store.Note{
		ProjectKey: "PLAT", RepoSlug: "files", PRID: 42, Team: "platform", PRKey: prKey, PRTitle: "t", TicketKey: "PLAT-7",
		ChangeKind: "feature", MergedAt: mergedAt, Body: json.RawMessage(`{"components":[]}`),
	}, ReceivedAt: mergedAt}
}

func str(s string) *string { return &s }

func newWorker(st *fakeStore, m Merger) *Worker {
	return New(st, m, limits, slog.New(slog.DiscardHandler))
}

// existingRepoPage is a repository page with one statement sourced from an
// older PR, as the fold reads it.
func existingRepoPage() store.Page {
	return store.Page{
		Scope: store.ScopeRepository, ProjectKey: "PLAT", RepoSlug: "files", RevisionID: 5, Revision: 2,
		Summary: "Der Dienst speichert Dateien.",
		Statements: []store.Statement{{
			ID: "s1", Kind: "component", Name: "Uploader", Text: "Nimmt Dateien an.", Files: []string{"internal/files/upload.go"},
			Sources: []store.Source{{PRKey: "PLAT/files#1", MergedAt: older}},
		}},
		SourcePR: "PLAT/files#1", UpdatedAt: older,
	}
}

// A statement whose umlauts are only written in, where it had ae, oe, ue,
// is stored so but keeps its sources, its last check and its diagram: the
// fold taught nothing about it.
func TestFoldRespellingIsNoChange(t *testing.T) {
	st := newFakeStore()
	page := existingRepoPage()
	flow := &diagram.Diagram{Kind: diagram.KindFlow, Direction: "LR", Nodes: []diagram.Node{{ID: "a", Label: "A", Shape: "step"}}}
	page.Statements[0].Name, page.Statements[0].Text, page.Statements[0].Checked, page.Statements[0].Diagram = "Gebuehren", "Fuer jede Datei.", "c0ffee", flow
	st.pages[pageKey(store.ScopeRepository, "PLAT", "files")] = page
	newWorker(st, &fakeMerger{result: okResult(&inference.Description{Summary: "Der Dienst.",
		Statements: []inference.Statement{{ID: str("s1"), Kind: "component", Name: "Gebühren", Text: "Für jede Datei.", Files: []string{"internal/files/upload.go"}}},
		Added:      []inference.Statement{}}, nil)}).Fold(context.Background(), queued("PLAT/files#42"))
	if len(st.revisions) != 1 {
		t.Fatalf("revisions = %+v", st.revisions)
	}
	s := st.revisions[0].Statements[0]
	if s.Name != "Gebühren" || len(s.Sources) != 1 || s.Checked != "c0ffee" || s.Diagram == nil {
		t.Errorf("respelled statement = %+v, want the umlauts stored, one source, the check and the diagram kept", s)
	}
}

// A statement only laid out anew, its sentence turned into a list with a
// bold term, is stored so but keeps its sources: layout is not something
// the fold learned. What the model wrote outside the stored layout (a *
// bullet, a ## heading, a heading in the summary) is stored in it.
func TestFoldLayoutIsNoChange(t *testing.T) {
	st := newFakeStore()
	page := existingRepoPage()
	page.Statements[0].Text = "Nimmt Dateien an: Bilder, Texte und Archive."
	st.pages[pageKey(store.ScopeRepository, "PLAT", "files")] = page
	newWorker(st, &fakeMerger{result: okResult(&inference.Description{Summary: "## Zweck\nDer Dienst.",
		Statements: []inference.Statement{{ID: str("s1"), Kind: "component", Name: "Uploader",
			Text: "Nimmt **Dateien** an:\n* Bilder\n* Texte\n  und Archive", Files: []string{"internal/files/upload.go"}}},
		Added: []inference.Statement{}}, nil)}).Fold(context.Background(), queued("PLAT/files#42"))
	if len(st.revisions) != 1 {
		t.Fatalf("revisions = %+v", st.revisions)
	}
	r := st.revisions[0]
	if want := "Der Dienst."; r.Summary != want {
		t.Errorf("summary = %q, want %q", r.Summary, want)
	}
	s := r.Statements[0]
	if want := "Nimmt **Dateien** an:\n\n- Bilder\n- Texte und Archive"; s.Text != want {
		t.Errorf("text = %q, want %q", s.Text, want)
	}
	if len(s.Sources) != 1 {
		t.Errorf("sources = %+v, want the old one alone: a new layout teaches nothing", s.Sources)
	}
}

// A statement stored before the layout, echoed as it stood while another
// changes, keeps its sources, check and diagram: its text is only laid out
// anew on the way in.
func TestFoldLegacyStatementEchoedKeepsSources(t *testing.T) {
	st := newFakeStore()
	page := existingRepoPage()
	flow := &diagram.Diagram{Kind: diagram.KindFlow, Direction: "LR", Nodes: []diagram.Node{{ID: "a", Label: "A", Shape: "step"}}}
	page.Statements[0].Text, page.Statements[0].Checked, page.Statements[0].Diagram = "Nimmt an:\n* Bilder\n## Regeln\nNur PNG.", "c0ffee", flow
	st.pages[pageKey(store.ScopeRepository, "PLAT", "files")] = page
	newWorker(st, &fakeMerger{result: okResult(&inference.Description{Summary: "Der Dienst, neu.",
		Statements: []inference.Statement{{ID: str("s1"), Kind: "component", Name: "Uploader", Text: page.Statements[0].Text, Files: []string{"internal/files/upload.go"}}},
		Added:      []inference.Statement{}}, nil)}).Fold(context.Background(), queued("PLAT/files#42"))
	if len(st.revisions) != 1 {
		t.Fatalf("revisions = %+v", st.revisions)
	}
	s := st.revisions[0].Statements[0]
	if len(s.Sources) != 1 || s.Checked != "c0ffee" || s.Diagram == nil {
		t.Errorf("echoed legacy statement = %+v, want one source, the check and the diagram kept", s)
	}
}

// manyFiles is n distinct paths.
func manyFiles(n int) []string {
	files := make([]string, n)
	for i := range files {
		files[i] = fmt.Sprintf("f%d.go", i)
	}
	return files
}

// Every prompt that asks for statements gives the name and file caps
// fieldCap holds them to: one over a cap costs the statement (an eval's
// first project page went whole, the file cap never stated).
func TestPromptsStateTheFieldCaps(t *testing.T) {
	for name, p := range map[string]string{"merge": prompts.Merge, "describe": prompts.Describe, "project": prompts.Project} {
		for _, want := range []string{fmt.Sprintf("at most %d characters>", nameMaxChars), fmt.Sprintf(`"<path, no spaces, at most %d characters>", ..., at most %d paths]`, pathMaxChars, filesMax)} {
			if !strings.Contains(p, want) {
				t.Errorf("%s prompt lacks %q", name, want)
			}
		}
	}
}

func TestFoldNoChange(t *testing.T) {
	st := newFakeStore()
	m := &fakeMerger{result: okResult(nil, nil)}
	newWorker(st, m).Fold(context.Background(), queued("PLAT/files#42"))
	if len(st.revisions) != 0 {
		t.Errorf("revisions = %+v, want none", st.revisions)
	}
	if len(st.marked) != 1 || st.marked[0] != "PLAT|files|42" {
		t.Errorf("marked = %v", st.marked)
	}
	if len(st.calls) != 1 || st.calls[0].Outcome != "ok" || st.calls[0].SourcePR != "PLAT/files#42" || st.calls[0].Model != "gpt-x" ||
		st.calls[0].CostNanoUSD == nil || *st.calls[0].CostNanoUSD != 2_000_000 || st.calls[0].CallID != "call-9" {
		t.Errorf("calls = %+v", st.calls)
	}
	if len(m.prompts) != 1 {
		t.Fatalf("prompts = %d", len(m.prompts))
	}
}

func TestFoldNewStatement(t *testing.T) {
	st := newFakeStore()
	m := &fakeMerger{result: okResult(&inference.Description{
		Summary:    "  Der Dienst speichert Dateien auf der Straße. ",
		Statements: []inference.Statement{{ID: nil, Kind: "rule", Name: "Größe", Text: "Höchstens 10 MB, außer PDFs.", Files: []string{" a.go ", ""}}},
	}, nil)}
	newWorker(st, m).Fold(context.Background(), queued("PLAT/files#42"))
	if len(st.revisions) != 1 {
		t.Fatalf("revisions = %+v, want one", st.revisions)
	}
	rev := st.revisions[0]
	if rev.Scope != store.ScopeRepository || rev.ProjectKey != "PLAT" || rev.RepoSlug != "files" || rev.SourcePR != "PLAT/files#42" {
		t.Errorf("revision identity = %+v", rev)
	}
	if rev.Summary != "Der Dienst speichert Dateien auf der Strasse." {
		t.Errorf("Summary = %q", rev.Summary)
	}
	if rev.LLMCallID == nil || *rev.LLMCallID != 77 {
		t.Errorf("LLMCallID = %v, want 77", rev.LLMCallID)
	}
	if rev.Chars == 0 {
		t.Error("Chars = 0")
	}
	if len(rev.Statements) != 1 {
		t.Fatalf("Statements = %+v", rev.Statements)
	}
	s := rev.Statements[0]
	if s.ID != "s1" || s.Kind != "rule" || s.Name != "Grösse" || s.Text != "Höchstens 10 MB, ausser PDFs." {
		t.Errorf("statement = %+v", s)
	}
	if len(s.Files) != 1 || s.Files[0] != "a.go" {
		t.Errorf("Files = %q", s.Files)
	}
	if len(s.Sources) != 1 || s.Sources[0].PRKey != "PLAT/files#42" || s.Sources[0].TicketKey != "PLAT-7" || !s.Sources[0].MergedAt.Equal(mergedAt) {
		t.Errorf("Sources = %+v", s.Sources)
	}
}

// The three kinds for agents land on the pages like any other: decision,
// extension and pitfall on the repository's, decision and pitfall on the
// project's, a new pitfall only from a bugfix note.
func TestFoldAgentKinds(t *testing.T) {
	repo := &inference.Description{Summary: "Der Dienst speichert Dateien.", Statements: []inference.Statement{
		{Kind: "decision", Name: "Ein Worker", Text: "Seriell, weil zwei Schreiber eine Seite verlieren.", Files: []string{"w.go"}},
		{Kind: "extension", Name: "Neue Route", Text: "api.go, dann openapi.yaml.", Files: []string{"api.go", "openapi.yaml"}},
		{Kind: "pitfall", Name: "Retry ohne Backoff", Text: "Brach den Upload ab.", Files: []string{"upload.go"}},
	}}
	project := &inference.Description{Summary: "Das Projekt.", Statements: []inference.Statement{
		{Kind: "decision", Name: "Eine Datenbank", Text: "Weil.", Files: []string{"files/db.go"}},
		{Kind: "pitfall", Name: "Doppelte Zustellung", Text: "Brach.", Files: []string{"files/hook.go"}},
	}}
	kinds := func(note string) []string {
		st := newFakeStore()
		q := queued("PLAT/files#42")
		q.ChangeKind = note
		newWorker(st, &fakeMerger{result: okResult(repo, project)}).Fold(context.Background(), q)
		if len(st.revisions) != 2 {
			t.Fatalf("%s: revisions = %+v, want both pages", note, st.revisions)
		}
		var got []string
		for _, rev := range st.revisions {
			for _, s := range rev.Statements {
				got = append(got, rev.Scope+":"+s.Kind)
			}
		}
		return got
	}
	if got, want := kinds("bugfix"), []string{"repository:decision", "repository:extension", "repository:pitfall", "project:decision", "project:pitfall"}; !slices.Equal(got, want) {
		t.Errorf("bugfix note: kinds = %v, want %v", got, want)
	}
	// Any other note is no pitfall's evidence: a new one is left out, the
	// rest of the answer stands.
	if got, want := kinds("feature"), []string{"repository:decision", "repository:extension", "project:decision"}; !slices.Equal(got, want) {
		t.Errorf("feature note: kinds = %v, want %v", got, want)
	}
}

// New statements come in `added` and the code numbers them, whatever id an
// answer slipped in; an edit must name an id, and one the page lacks is
// invented: the rewrite is rejected, as before the split. A merge is the
// whole page, so leaving such an edit out would delete what it meant.
func TestFoldEditsAndAdded(t *testing.T) {
	st := newFakeStore()
	st.pages[pageKey(store.ScopeRepository, "PLAT", "files")] = existingRepoPage()
	m := &fakeMerger{result: okResult(&inference.Description{Summary: "Der Dienst.",
		Statements: []inference.Statement{{ID: str("s1"), Kind: "component", Name: "Uploader", Text: "Nimmt Dateien an.", Files: []string{"internal/files/upload.go"}}},
		Added:      []inference.Statement{{ID: str("s36"), Kind: "rule", Name: "Grenze", Text: "10 MB.", Files: []string{"a.go"}}},
	}, nil)}
	newWorker(st, m).Fold(context.Background(), queued("PLAT/files#42"))
	if len(st.revisions) != 1 {
		t.Fatalf("revisions = %+v", st.revisions)
	}
	if got := st.revisions[0].Statements; len(got) != 2 || got[0].ID != "s1" || got[1].ID != "s2" || got[1].Name != "Grenze" {
		t.Errorf("statements = %+v, want s1 kept and the new one numbered s2", got)
	}

	// An invented edit beside a valid one, or an id edited twice, still
	// rejects the merge: the page stays.
	st = newFakeStore()
	st.pages[pageKey(store.ScopeRepository, "PLAT", "files")] = existingRepoPage()
	newWorker(st, &fakeMerger{result: okResult(&inference.Description{Summary: "Der Dienst.",
		Statements: []inference.Statement{
			{ID: str("s9"), Kind: "rule", Name: "Erfunden", Text: "Gibt es nicht."},
			{ID: str("s1"), Kind: "component", Name: "Uploader", Text: "Nimmt Dateien an, bis 10 MB.", Files: []string{"internal/files/upload.go"}},
			{ID: str("s1"), Kind: "component", Name: "Uploader", Text: "Zweite Fassung.", Files: []string{"internal/files/upload.go"}},
		},
		Added: []inference.Statement{},
	}, nil)}).Fold(context.Background(), queued("PLAT/files#42"))
	if len(st.revisions) != 0 {
		t.Errorf("bad edits: revision written %+v", st.revisions)
	}

	for name, d := range map[string]*inference.Description{
		"edit without an id": {Summary: "s", Statements: []inference.Statement{{Kind: "rule", Name: "n", Text: "t"}}, Added: []inference.Statement{}},
		"invented id":        {Summary: "s", Statements: []inference.Statement{{ID: str("s9"), Kind: "rule", Name: "n", Text: "t"}}, Added: []inference.Statement{}},
	} {
		st := newFakeStore()
		st.pages[pageKey(store.ScopeRepository, "PLAT", "files")] = existingRepoPage()
		newWorker(st, &fakeMerger{result: okResult(d, nil)}).Fold(context.Background(), queued("PLAT/files#42"))
		if len(st.revisions) != 0 {
			t.Errorf("%s: revision written: %+v", name, st.revisions)
		}
	}

	// On a page without statements nothing can be edited: an edit, its id
	// invented or missing, is a new statement, numbered by the code.
	st = newFakeStore()
	newWorker(st, &fakeMerger{result: okResult(&inference.Description{Summary: "Der Dienst.",
		Statements: []inference.Statement{{ID: str("s9"), Kind: "rule", Name: "Grenze", Text: "10 MB.", Files: []string{"a.go"}}, {Kind: "rule", Name: "Takt", Text: "Jede Minute.", Files: []string{"b.go"}}},
		Added:      []inference.Statement{{Kind: "component", Name: "Uploader", Text: "Nimmt an.", Files: []string{"c.go"}}},
	}, nil)}).Fold(context.Background(), queued("PLAT/files#42"))
	if len(st.revisions) != 1 {
		t.Fatalf("empty page: revisions = %+v", st.revisions)
	}
	var got []string
	for _, s := range st.revisions[0].Statements {
		got = append(got, s.ID+":"+s.Name)
	}
	if want := []string{"s1:Grenze", "s2:Takt", "s3:Uploader"}; !slices.Equal(got, want) {
		t.Errorf("empty page: statements = %v, want %v", got, want)
	}
	// An edit repeating a new statement, or an earlier edit (an id used
	// twice), is that statement once.
	st = newFakeStore()
	newWorker(st, &fakeMerger{result: okResult(&inference.Description{Summary: "Der Dienst.",
		Statements: []inference.Statement{{ID: str("s1"), Kind: "rule", Name: "Grenze", Text: "10 MB.", Files: []string{"a.go"}}},
		Added:      []inference.Statement{{Kind: "rule", Name: "Grenze", Text: "Zehn MB.", Files: []string{"a.go"}}},
	}, nil)}).Fold(context.Background(), queued("PLAT/files#42"))
	if len(st.revisions) != 1 || len(st.revisions[0].Statements) != 1 || st.revisions[0].Statements[0].Text != "Zehn MB." {
		t.Errorf("empty page, one statement twice: %+v", st.revisions)
	}
	st = newFakeStore()
	twice := inference.Statement{ID: str("s1"), Kind: "rule", Name: "Grenze", Text: "10 MB.", Files: []string{"a.go"}}
	newWorker(st, &fakeMerger{result: okResult(&inference.Description{Summary: "Der Dienst.",
		Statements: []inference.Statement{twice, twice}, Added: []inference.Statement{}}, nil)}).Fold(context.Background(), queued("PLAT/files#42"))
	if len(st.revisions) != 1 || len(st.revisions[0].Statements) != 1 || st.revisions[0].Statements[0].ID != "s1" {
		t.Errorf("empty page, id used twice: %+v, want the statement once", st.revisions)
	}
}

// An extension is a repository's change recipe: on the project page it is
// refused like an unknown kind, the project rewrite rejected whole and the
// repository's applied.
func TestFoldProjectExtensionRefused(t *testing.T) {
	st := newFakeStore()
	repo := &inference.Description{Summary: "Der Dienst.", Statements: []inference.Statement{
		{Kind: "extension", Name: "Neue Route", Text: "api.go.", Files: []string{"api.go"}},
	}}
	project := &inference.Description{Summary: "Das Projekt.", Statements: []inference.Statement{
		{Kind: "extension", Name: "Neuer Dienst", Text: "Überall.", Files: []string{"x.go"}},
	}}
	for _, kind := range []string{"extension", "tutorial"} {
		project.Statements[0].Kind = kind
		st.revisions = nil
		newWorker(st, &fakeMerger{result: okResult(repo, project)}).Fold(context.Background(), queued("PLAT/files#42"))
		for _, rev := range st.revisions {
			if rev.Scope == store.ScopeProject {
				t.Errorf("%s: project revision written: %+v", kind, rev)
			}
		}
	}
	if _, _, reason := attach(store.ScopeProject, nil, project.Statements, store.Source{}, 1, limitsOf(limits)); reason == "" {
		t.Error("attach took an unknown kind on the project page")
	}
	project.Statements[0].Kind = "extension"
	if _, _, reason := attach(store.ScopeProject, nil, project.Statements, store.Source{}, 1, limitsOf(limits)); !strings.Contains(reason, "extension not on a project page") {
		t.Errorf("reason = %q", reason)
	}
	if _, _, reason := attach(store.ScopeRepository, nil, project.Statements, store.Source{}, 1, limitsOf(limits)); reason != "" {
		t.Errorf("repository extension refused: %q", reason)
	}
}

func TestFoldKeptIDUnchangedKeepsSources(t *testing.T) {
	st := newFakeStore()
	st.pages[pageKey(store.ScopeRepository, "PLAT", "files")] = existingRepoPage()
	m := &fakeMerger{result: okResult(&inference.Description{
		Summary:    "Der Dienst speichert Dateien, neu formuliert.",
		Statements: []inference.Statement{{ID: str("s1"), Kind: "component", Name: "Uploader", Text: "Nimmt Dateien an.", Files: []string{"internal/files/upload.go"}}},
	}, nil)}
	newWorker(st, m).Fold(context.Background(), queued("PLAT/files#42"))
	if len(st.revisions) != 1 {
		t.Fatalf("revisions = %+v, want one (the summary changed)", st.revisions)
	}
	s := st.revisions[0].Statements[0]
	if s.ID != "s1" || len(s.Sources) != 1 || s.Sources[0].PRKey != "PLAT/files#1" {
		t.Errorf("statement = %+v, want the old source only", s)
	}
}

func TestFoldKeptIDChangedAppendsPROnce(t *testing.T) {
	st := newFakeStore()
	st.pages[pageKey(store.ScopeRepository, "PLAT", "files")] = existingRepoPage()
	m := &fakeMerger{result: okResult(&inference.Description{
		Summary:    "Der Dienst speichert Dateien.",
		Statements: []inference.Statement{{ID: str("s1"), Kind: "component", Name: "Uploader", Text: "Nimmt Dateien bis 10 MB an.", Files: []string{"internal/files/upload.go"}}},
	}, nil)}
	newWorker(st, m).Fold(context.Background(), queued("PLAT/files#42"))
	if len(st.revisions) != 1 {
		t.Fatalf("revisions = %+v, want one", st.revisions)
	}
	s := st.revisions[0].Statements[0]
	if s.ID != "s1" || len(s.Sources) != 2 || s.Sources[0].PRKey != "PLAT/files#1" || s.Sources[1].PRKey != "PLAT/files#42" {
		t.Errorf("Sources = %+v, want old then this PR", s.Sources)
	}

	// The same PR folded again (a re-sent note) does not add itself twice.
	page := existingRepoPage()
	page.Statements[0].Sources = append(page.Statements[0].Sources, store.Source{PRKey: "PLAT/files#42", MergedAt: mergedAt})
	st2 := newFakeStore()
	st2.pages[pageKey(store.ScopeRepository, "PLAT", "files")] = page
	newWorker(st2, m).Fold(context.Background(), queued("PLAT/files#42"))
	if len(st2.revisions) != 1 {
		t.Fatalf("revisions = %+v, want one", st2.revisions)
	}
	if got := st2.revisions[0].Statements[0].Sources; len(got) != 2 {
		t.Errorf("Sources = %+v, want the PR once", got)
	}
}

func TestFoldRejections(t *testing.T) {
	long := strings.Repeat("x", limits.RepoMaxChars+1)
	many := make([]inference.Statement, limits.RepoMaxStatements+1)
	for i := range many {
		many[i] = inference.Statement{Kind: "rule", Name: "n", Text: "t"}
	}
	cases := []struct {
		name string
		desc *inference.Description
	}{
		{"unknown id", &inference.Description{Summary: "s", Statements: []inference.Statement{{ID: str("s9"), Kind: "rule", Name: "n", Text: "t"}}}},
		{"unknown kind", &inference.Description{Summary: "s", Statements: []inference.Statement{{Kind: "opinion", Name: "n", Text: "t"}}}},
		{"id used twice", &inference.Description{Summary: "s", Statements: []inference.Statement{
			{ID: str("s1"), Kind: "rule", Name: "n", Text: "t"}, {ID: str("s1"), Kind: "rule", Name: "m", Text: "u"}}}},
		{"empty statement", &inference.Description{Summary: "s", Statements: []inference.Statement{{Kind: "rule", Name: " ", Text: "t"}}}},
		{"over the statement cap", &inference.Description{Summary: "s", Statements: many}},
		// The ceiling is the whole description: the summary and the names
		// and texts together, never one statement by itself.
		{"description over the cap by a statement", &inference.Description{Summary: "s", Statements: []inference.Statement{{Kind: "rule", Name: "n", Text: long}}}},
		{"description over the cap by the summary", &inference.Description{Summary: strings.Repeat("y", limits.RepoMaxChars+1), Statements: nil}},
		{"empty summary", &inference.Description{Summary: "  ", Statements: nil}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newFakeStore()
			st.pages[pageKey(store.ScopeRepository, "PLAT", "files")] = existingRepoPage()
			m := &fakeMerger{result: okResult(tc.desc, nil)}
			newWorker(st, m).Fold(context.Background(), queued("PLAT/files#42"))
			if len(st.revisions) != 0 {
				t.Errorf("revisions = %+v, want the rewrite rejected", st.revisions)
			}
			if len(st.marked) != 1 {
				t.Errorf("marked = %v, want the note marked anyway", st.marked)
			}
		})
	}
}

// A statement over a field cap costs that statement, never the rewrite: a
// new one is left out, an edit leaves the statement as stored, the rest of
// the answer stands.
func TestFoldFieldCapsLeaveOneOut(t *testing.T) {
	keep := inference.Statement{Kind: "rule", Name: "Grenze", Text: "10 MB.", Files: []string{"a.go"}}
	for name, bad := range map[string]inference.Statement{
		"name over cap":        {Kind: "rule", Name: strings.Repeat("n", nameMaxChars+1), Text: "t"},
		"too many files":       {Kind: "rule", Name: "n", Text: "t", Files: []string{"a", "b", "c", "d", "e", "f"}},
		"path over cap":        {Kind: "rule", Name: "n", Text: "t", Files: []string{strings.Repeat("p", pathMaxChars+1)}},
		"path with whitespace": {Kind: "rule", Name: "n", Text: "t", Files: []string{"internal/a file.go"}},
	} {
		t.Run(name, func(t *testing.T) {
			st := newFakeStore()
			st.pages[pageKey(store.ScopeRepository, "PLAT", "files")] = existingRepoPage()
			edit := bad
			edit.ID = str("s1")
			newWorker(st, &fakeMerger{result: okResult(&inference.Description{Summary: "Der Dienst.",
				Statements: []inference.Statement{edit}, Added: []inference.Statement{bad, keep}}, nil)}).Fold(context.Background(), queued("PLAT/files#42"))
			if len(st.revisions) != 1 {
				t.Fatalf("revisions = %+v, want one", st.revisions)
			}
			var got []string
			for _, s := range st.revisions[0].Statements {
				got = append(got, s.ID+":"+s.Name)
			}
			if want := []string{"s1:Uploader", "s2:Grenze"}; !slices.Equal(got, want) {
				t.Errorf("statements = %v, want %v: the edit left as stored, the bad new one out", got, want)
			}
			if s1 := st.revisions[0].Statements[0]; len(s1.Sources) != 1 {
				t.Errorf("s1 sources = %+v, want the stored one alone", s1.Sources)
			}
		})
	}
	// Caps that leave out every statement proposed never empty a page; the
	// refusal names each statement and its cap.
	st := newFakeStore()
	w := newWorker(st, &fakeMerger{result: okResult(&inference.Description{Summary: "Der Dienst.", Statements: []inference.Statement{},
		Added: []inference.Statement{{Kind: "rule", Name: "Viele", Text: "t", Files: manyFiles(filesMax + 1)}, {Kind: "rule", Name: strings.Repeat("n", nameMaxChars+1), Text: "t"}}}, nil)})
	var logs strings.Builder
	w.log = slog.New(slog.NewTextHandler(&logs, nil))
	w.Fold(context.Background(), queued("PLAT/files#42"))
	if len(st.revisions) != 0 {
		t.Errorf("every statement over a cap: revision written %+v", st.revisions)
	}
	if want := fmt.Sprintf(`every statement over a field cap: \"Viele\" (names %d files, over %d), \"%s\" (name over %d characters)`, filesMax+1, filesMax, strings.Repeat("n", nameMaxChars+1), nameMaxChars); !strings.Contains(logs.String(), want) {
		t.Errorf("refusal lacks the statements and caps:\n%s", logs.String())
	}
	// A statement stored over a cap and returned with its name and files
	// as they are stays: only a new or changed field meets the caps.
	st = newFakeStore()
	page := existingRepoPage()
	page.Statements[0].Name = strings.Repeat("n", nameMaxChars+1)
	st.pages[pageKey(store.ScopeRepository, "PLAT", "files")] = page
	newWorker(st, &fakeMerger{result: okResult(&inference.Description{Summary: "Der Dienst.", Statements: []inference.Statement{
		{ID: str("s1"), Kind: "component", Name: page.Statements[0].Name, Text: "Nimmt Dateien an, neu.", Files: page.Statements[0].Files}},
		Added: []inference.Statement{}}, nil)}).Fold(context.Background(), queued("PLAT/files#42"))
	if len(st.revisions) != 1 || st.revisions[0].Statements[0].Text != "Nimmt Dateien an, neu." {
		t.Errorf("stored over-cap name: revisions %+v, want the text edit taken", st.revisions)
	}
}

func TestFoldIdenticalNoRevision(t *testing.T) {
	st := newFakeStore()
	st.pages[pageKey(store.ScopeRepository, "PLAT", "files")] = existingRepoPage()
	m := &fakeMerger{result: okResult(&inference.Description{
		Summary:    "Der Dienst speichert Dateien.",
		Statements: []inference.Statement{{ID: str("s1"), Kind: "component", Name: "Uploader", Text: "Nimmt Dateien an.", Files: []string{"internal/files/upload.go"}}},
	}, nil)}
	newWorker(st, m).Fold(context.Background(), queued("PLAT/files#42"))
	if len(st.revisions) != 0 {
		t.Errorf("revisions = %+v, want none for an identical description", st.revisions)
	}
	if len(st.marked) != 1 {
		t.Errorf("marked = %v", st.marked)
	}
}

// Text stored before the layout, an indent or a "* " item in it, returned
// as it stands is no new revision: only its whitespace and marks would
// change.
func TestFoldLegacyTextEchoedNoRevision(t *testing.T) {
	st := newFakeStore()
	page := existingRepoPage()
	page.Summary = "Der Dienst.\n\n\n  Speichert."
	page.Statements[0].Text = "Nimmt an:\n* Bilder\n  * Texte"
	st.pages[pageKey(store.ScopeRepository, "PLAT", "files")] = page
	m := &fakeMerger{result: okResult(&inference.Description{
		Summary:    page.Summary,
		Statements: []inference.Statement{{ID: str("s1"), Kind: "component", Name: "Uploader", Text: page.Statements[0].Text, Files: []string{"internal/files/upload.go"}}},
	}, nil)}
	newWorker(st, m).Fold(context.Background(), queued("PLAT/files#42"))
	if len(st.revisions) != 0 {
		t.Errorf("revisions = %+v, want none for legacy text echoed", st.revisions)
	}
}

func TestFoldProjectScope(t *testing.T) {
	st := newFakeStore()
	m := &fakeMerger{result: okResult(nil, &inference.Description{
		Summary:    "Zwei Repositories teilen eine Datenbank.",
		Statements: []inference.Statement{{Kind: "entity", Name: "Datei", Text: "Gehört einem Projekt.", Files: nil}},
	})}
	newWorker(st, m).Fold(context.Background(), queued("PLAT/files#42"))
	if len(st.revisions) != 1 {
		t.Fatalf("revisions = %+v", st.revisions)
	}
	rev := st.revisions[0]
	if rev.Scope != store.ScopeProject || rev.RepoSlug != "" || rev.ProjectKey != "PLAT" {
		t.Errorf("project revision = %+v, want scope project with no repo", rev)
	}
	if rev.Statements[0].Files == nil || len(rev.Statements[0].Files) != 0 {
		t.Errorf("Files = %#v, want an empty slice", rev.Statements[0].Files)
	}
}

// withDescription is a failed result that still carries a rewrite, so a
// gate that fell through to apply() would show as a revision.
func failed(outcome inference.Outcome, err error) inference.MergeResult {
	return inference.MergeResult{Call: inference.Call{Outcome: outcome, Err: err},
		Merge: inference.Merge{Repository: &inference.Description{Summary: "s", Statements: nil}}}
}

// A failure specific to the call (the model's answer off the schema, a
// 4xx, a 2xx of the wrong shape) marks the note: no retry, no hold.
func TestFoldCallSpecificFailureMarks(t *testing.T) {
	for name, res := range map[string]inference.MergeResult{
		"unparseable":    failed(inference.OutcomeUnparseable, errors.New("boom")),
		"bad request":    failed(inference.OutcomeError, fmt.Errorf("wrapped: %w", llmwire.ErrBadRequest)),
		"response shape": failed(inference.OutcomeError, fmt.Errorf("wrapped: %w", llmwire.ErrResponseShape)),
	} {
		st := newFakeStore()
		w := newWorker(st, &fakeMerger{result: res})
		// A hold from an earlier gateway failure ends with any answer.
		w.backoff, w.retryAt = backoffMin, time.Time{}
		if next := w.Fold(context.Background(), queued("PLAT/files#42")); !next {
			t.Errorf("%s: paused the drain", name)
		}
		if len(st.revisions) != 0 || len(st.marked) != 1 || len(st.attempts) != 0 || w.holding() || w.backoff != 0 {
			t.Errorf("%s: revisions %d marked %v attempts %v holding %v; want marked, no retry, no hold",
				name, len(st.revisions), st.marked, st.attempts, w.holding())
		}
		if len(st.calls) != 1 || st.calls[0].Outcome != res.Outcome.String() {
			t.Errorf("%s: calls = %+v, want one row with the outcome", name, st.calls)
		}
	}
}

// A gateway-class failure (timeout, 5xx, transport, refused key, rate
// limit) keeps the note, counts the attempt and holds the whole worker
// with a doubling backoff; a wake during the hold folds nothing; a rate
// limit's Retry-After wins when longer; a call that gets through clears
// the hold; past maxFoldAttempts the note is given up.
func TestFoldGatewayFailureHoldsTheWorker(t *testing.T) {
	for name, res := range map[string]inference.MergeResult{
		"timed out":  failed(inference.OutcomeTimedOut, llmwire.ErrCallCap),
		"upstream":   failed(inference.OutcomeError, fmt.Errorf("wrapped: %w", llmwire.ErrUpstream)),
		"auth":       failed(inference.OutcomeError, fmt.Errorf("wrapped: %w", llmwire.ErrAuth)),
		"transport":  failed(inference.OutcomeError, errors.New("dial tcp: connection refused")),
		"proxy page": failed(inference.OutcomeError, fmt.Errorf("wrapped: %w", llmwire.ErrMalformedResponse)),
		"rate limited": failed(inference.OutcomeError, &llmwire.RateLimitError{
			APIError: &llmwire.APIError{StatusCode: 429}, RetryAfter: time.Hour}),
	} {
		st := newFakeStore()
		st.queue = []store.QueuedNote{queued("PLAT/files#42"), queued("PLAT/files#43")}
		m := &fakeMerger{result: res}
		w := newWorker(st, m)
		clock := mergedAt
		w.now = func() time.Time { return clock }
		w.drain(context.Background())
		if len(m.prompts) != 1 || len(st.marked) != 0 || st.attempts["PLAT|files|42"] != 1 || len(st.revisions) != 0 {
			t.Errorf("%s: after one drain: calls %d marked %v attempts %v revisions %d; want one call, nothing marked, one attempt",
				name, len(m.prompts), st.marked, st.attempts, len(st.revisions))
		}
		want := backoffMin
		if name == "rate limited" {
			want = time.Hour
		}
		if !w.holding() || !w.retryAt.Equal(clock.Add(want)) {
			t.Errorf("%s: hold until %v, want %v from now", name, w.retryAt, want)
		}
		// A wake inside the hold folds nothing.
		w.drain(context.Background())
		if len(m.prompts) != 1 {
			t.Errorf("%s: a drain inside the hold made %d calls", name, len(m.prompts)-1)
		}
		// Past the hold the same note is tried again and the hold doubles.
		clock = w.retryAt
		w.drain(context.Background())
		if len(m.prompts) != 2 || st.attempts["PLAT|files|42"] != 2 {
			t.Errorf("%s: after the hold: calls %d attempts %v; want a second try of the same note", name, len(m.prompts), st.attempts)
		}
		if name != "rate limited" && !w.retryAt.Equal(clock.Add(2*backoffMin)) {
			t.Errorf("%s: second hold until %v, want doubled to %s", name, w.retryAt, 2*backoffMin)
		}
	}

	// The backoff caps, and a call that gets through clears the hold.
	st := newFakeStore()
	m := &fakeMerger{result: failed(inference.OutcomeError, errors.New("down"))}
	w := newWorker(st, m)
	clock := mergedAt
	w.now = func() time.Time { return clock }
	for i := 0; i < 8; i++ {
		w.Fold(context.Background(), queued("PLAT/files#42"))
		clock = w.retryAt
	}
	if w.backoff != backoffMax {
		t.Errorf("backoff after 8 failures = %s, want the cap %s", w.backoff, backoffMax)
	}
	m.result = okResult(nil, nil)
	w.Fold(context.Background(), queued("PLAT/files#43"))
	if w.holding() || w.backoff != 0 {
		t.Errorf("a call that got through left a hold: until %v backoff %s", w.retryAt, w.backoff)
	}

	// Past maxFoldAttempts the note is given up, marked, with the rest
	// of the queue untouched by it.
	st = newFakeStore()
	m = &fakeMerger{result: failed(inference.OutcomeError, errors.New("down"))}
	w = newWorker(st, m)
	w.now = func() time.Time { return mergedAt }
	for i := 1; i <= maxFoldAttempts; i++ {
		w.clearHold()
		if next := w.Fold(context.Background(), queued("PLAT/files#42")); next != (i == maxFoldAttempts) {
			t.Errorf("attempt %d: next = %v", i, next)
		}
	}
	if len(st.marked) != 1 || st.attempts["PLAT|files|42"] != maxFoldAttempts || len(st.revisions) != 0 {
		t.Errorf("after %d attempts: marked %v attempts %v revisions %d; want given up once", maxFoldAttempts, st.marked, st.attempts, len(st.revisions))
	}

	// A defer the store refuses pauses the drain like a failed mark.
	st = newFakeStore()
	st.deferErr = errors.New("read-only")
	w = newWorker(st, &fakeMerger{result: failed(inference.OutcomeError, errors.New("down"))})
	if next := w.Fold(context.Background(), queued("PLAT/files#42")); next || len(st.marked) != 0 {
		t.Errorf("defer failed: next %v marked %v; want paused and unmarked", next, st.marked)
	}
}

// A database busy past its timeout is temporary, like the gateway: the
// paid fold's note stays queued with its attempt counted and the worker
// holds. Any other write failure ends the note as before: marked.
func TestFoldBusyDatabaseDefers(t *testing.T) {
	ctx := context.Background()
	st := newFakeStore()
	st.revisionErr = fmt.Errorf("insert revision: %w", store.ErrBusy)
	m := &fakeMerger{result: okResult(&inference.Description{Summary: "Der Dienst speichert Dateien.", Statements: []inference.Statement{}}, nil)}
	w := newWorker(st, m)
	w.now = func() time.Time { return mergedAt }
	if next := w.Fold(ctx, queued("PLAT/files#42")); next || len(st.marked) != 0 || st.attempts["PLAT|files|42"] != 1 || !w.holding() {
		t.Fatalf("busy revision write: next %v marked %v attempts %v holding %v; want deferred, held", next, st.marked, st.attempts, w.holding())
	}

	// The hold over, the store answering: the same note folds.
	w.clearHold()
	st.revisionErr = nil
	if next := w.Fold(ctx, queued("PLAT/files#42")); !next || len(st.marked) != 1 || len(st.revisions) != 1 {
		t.Fatalf("after the hold: next %v marked %v revisions %d", next, st.marked, len(st.revisions))
	}

	// Another write failure: logged, the note marked, no hold.
	st.revisionErr = errors.New("disk full")
	if next := w.Fold(ctx, queued("PLAT/files#43")); !next || len(st.marked) != 2 || w.holding() {
		t.Fatalf("disk full: next %v marked %v holding %v; want marked, no hold", next, st.marked, w.holding())
	}

	// A busy page read before the call defers too, and pays for nothing.
	st = newFakeStore()
	st.pageErr = store.ErrBusy
	m = &fakeMerger{result: okResult(nil, nil)}
	w = newWorker(st, m)
	w.now = func() time.Time { return mergedAt }
	if next := w.Fold(ctx, queued("PLAT/files#42")); next || len(st.marked) != 0 || st.attempts["PLAT|files|42"] != 1 || len(m.prompts) != 0 || !w.holding() {
		t.Fatalf("busy page read: next %v marked %v attempts %v calls %d", next, st.marked, st.attempts, len(m.prompts))
	}
}

func TestRender(t *testing.T) {
	q := queued("PLAT/files#42")
	q.TicketKey, q.ChangeKind = "", ""
	q.Body = json.RawMessage(`{"components":[{"name":"x"}]}`)
	empty := store.Page{}
	out := render(prompts.Merge, q, existingRepoPage(), empty, limits)
	for _, ph := range []string{"{repo}", "{project}", "{pr_key}", "{ticket_key}", "{change_kind}", "{repo_max_statements}",
		"{project_max_statements}", "{repo_max_chars}", "{project_max_chars}", "{repo_description}", "{project_description}", "{note}",
		"{diagram_rules}", "{diagram_max_nodes}", "{diagram_max_edges}", "{max_diagrams}"} {
		if strings.Contains(out, ph) {
			t.Errorf("placeholder %s left in the prompt", ph)
		}
	}
	for _, want := range []string{"`files`", "`PLAT`", "PR PLAT/files#42, ticket none, kind unknown", "at most 3 statements", "at most 2 statements",
		"400 characters of prose in all", "200 characters of prose in all", "Der Dienst speichert Dateien.", "### [s1] component: Uploader\n\nNimmt Dateien an.",
		"the current description has 54", "the current description has 0",
		noneYet, `{"components":[{"name":"x"}]}`, "At most 4 nodes", "and 3 edges", "At most 1 statement diagrams on a page", "null keeps the diagram"} {
		if !strings.Contains(out, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
	if n := strings.Count(out, "_None yet"); n != 1 {
		t.Errorf("_None yet appears %d times, want once (the project page only)", n)
	}
}

func TestDescribe(t *testing.T) {
	// An empty page says every statement is new, or the model numbers its
	// own and invents ids; a page only read says no more than that it is
	// empty. The baseline renderings say the same.
	if got := describe(store.Page{RevisionID: 0, Summary: "ignored"}); got != noneYet {
		t.Errorf("describe(no revision) = %q", got)
	}
	if got := describeRead(store.Page{}); got != "_None yet._" {
		t.Errorf("describeRead(no revision) = %q", got)
	}
	bare := store.Page{RevisionID: 1, Summary: "Werkzeuge."}
	for name, got := range map[string]string{"describe": describe(bare), "describeSourced": describeSourced(bare, nil), "describeTaught": describeTaught(bare)} {
		if got != "Werkzeuge.\n"+noStatementsYet {
			t.Errorf("%s(no statements) = %q", name, got)
		}
	}
	if got := describeSourced(store.Page{}, nil); got != noneYet {
		t.Errorf("describeSourced(no revision) = %q", got)
	}
	if got := describeRead(bare); got != "Werkzeuge.\n" {
		t.Errorf("describeRead(no statements) = %q", got)
	}
	p := existingRepoPage()
	p.Statements = append(p.Statements, store.Statement{ID: "s2", Kind: "rule", Name: "R", Text: "T"})
	got := describe(p)
	want := "Der Dienst speichert Dateien.\n\n### [s1] component: Uploader\n\nNimmt Dateien an.\n\nFiles: internal/files/upload.go\n\n### [s2] rule: R\n\nT\n"
	if got != want {
		t.Errorf("describe =\n%q\nwant\n%q", got, want)
	}
	// A multi-paragraph text stays under its heading, every paragraph
	// attributable to the id.
	p.Statements[1].Text = "Absatz eins.\n\nAbsatz zwei."
	if got := describe(p); !strings.Contains(got, "### [s2] rule: R\n\nAbsatz eins.\n\nAbsatz zwei.\n") {
		t.Errorf("describe with paragraphs = %q", got)
	}
	// A sub-heading and a list stay inside the statement: #### under the
	// frame's ###, never a statement of its own.
	p.Statements[1].Text = "#### Ablauf\n\n- eins\n- zwei"
	if got := describe(p); !strings.Contains(got, "### [s2] rule: R\n\n#### Ablauf\n\n- eins\n- zwei\n") {
		t.Errorf("describe with layout = %q", got)
	}
}

// A statement is as long as its subject needs: a text far over the old
// per-statement cap is accepted under the page's ceiling, and the chars
// stored are the prose the ceiling counted. The project scope has its own,
// smaller ceiling.
func TestFoldLongStatementUnderThePageCeiling(t *testing.T) {
	st := newFakeStore()
	st.pages[pageKey(store.ScopeRepository, "PLAT", "files")] = existingRepoPage()
	text := strings.Repeat("ä", 350) + "\n\n" + strings.Repeat("b", 20)
	repo := &inference.Description{Summary: "s", Statements: []inference.Statement{{Kind: "rule", Name: "n", Text: text}}}
	project := &inference.Description{Summary: strings.Repeat("p", limits.ProjectMaxChars+1), Statements: nil}
	newWorker(st, &fakeMerger{result: okResult(repo, project)}).Fold(context.Background(), queued("PLAT/files#42"))
	if len(st.revisions) != 1 || st.revisions[0].Scope != store.ScopeRepository {
		t.Fatalf("revisions = %+v, want the repository accepted and the project rejected", st.revisions)
	}
	if want := 1 + 1 + 350 + 2 + 20; st.revisions[0].Chars != want {
		t.Errorf("chars = %d, want the prose runes %d", st.revisions[0].Chars, want)
	}
}

// An id a past revision used and a later fold dropped is never reissued:
// the page holds s1, the history once held s3, the new statement is s4.
func TestNewStatementIDNeverReusesADroppedOne(t *testing.T) {
	st := newFakeStore()
	st.pages[pageKey(store.ScopeRepository, "PLAT", "files")] = existingRepoPage()
	st.history = map[string][]string{pageKey(store.ScopeRepository, "PLAT", "files"): {"s1", "s2", "s3"}}
	desc := &inference.Description{Summary: "s", Statements: []inference.Statement{
		{ID: str("s1"), Kind: "component", Name: "Uploader", Text: "Nimmt Dateien an."},
		{Kind: "rule", Name: "Neu", Text: "t"},
	}}
	newWorker(st, &fakeMerger{result: okResult(desc, nil)}).Fold(context.Background(), queued("PLAT/files#42"))
	if len(st.revisions) != 1 || len(st.revisions[0].Statements) != 2 || st.revisions[0].Statements[1].ID != "s4" {
		t.Fatalf("revisions = %+v, want the new statement as s4", st.revisions)
	}
}

// A mark that fails pauses the drain: the same note would otherwise come
// back at once, one paid call per lap.
func TestDrainPausesWhenTheMarkFails(t *testing.T) {
	st := newFakeStore()
	st.queue = []store.QueuedNote{queued("PLAT/files#1"), queued("PLAT/files#2")}
	st.markErr = errors.New("read-only transaction")
	m := &fakeMerger{result: okResult(nil, nil)}
	newWorker(st, m).drain(context.Background())
	if len(m.prompts) != 1 || st.nextQueuedN != 1 {
		t.Errorf("drain folded %d notes after %d reads, want one of each", len(m.prompts), st.nextQueuedN)
	}
}

// Stop lets the fold in flight finish; past the grace it cancels it.
func TestStopWaitsForTheFoldThenCancels(t *testing.T) {
	st := newFakeStore()
	st.queue = []store.QueuedNote{queued("PLAT/files#1")}
	m := newBlockingMerger()
	w := newWorker(st, m)
	w.Start(context.Background())
	<-m.started
	done := make(chan struct{})
	go func() { w.Stop(5 * time.Second); close(done) }()
	select {
	case <-done:
		t.Fatal("Stop returned while the fold was still running")
	case <-time.After(50 * time.Millisecond):
	}
	close(m.block)
	<-done
	if len(st.marked) != 1 {
		t.Errorf("marked = %v, want the fold in flight finished", st.marked)
	}

	st = newFakeStore()
	st.queue = []store.QueuedNote{queued("PLAT/files#1")}
	m = newBlockingMerger()
	w = newWorker(st, m)
	w.Start(context.Background())
	<-m.started
	w.Stop(20 * time.Millisecond)
	if m.ctx == nil || m.ctx.Err() == nil {
		t.Error("the fold was not cancelled past the grace")
	}
}

// blockingMerger holds its first call until block closes or the ctx ends.
type blockingMerger struct {
	block   chan struct{}
	started chan struct{}
	ctx     context.Context
	once    sync.Once
}

func newBlockingMerger() *blockingMerger {
	return &blockingMerger{block: make(chan struct{}), started: make(chan struct{})}
}

func (m *blockingMerger) Merge(ctx context.Context, _ inference.MergeRequest) inference.MergeResult {
	m.ctx = ctx
	m.once.Do(func() { close(m.started) })
	select {
	case <-m.block:
	case <-ctx.Done():
	}
	return okResult(nil, nil)
}

func TestDrainOldestFirst(t *testing.T) {
	st := newFakeStore()
	for _, pr := range []string{"PLAT/files#1", "PLAT/files#2", "PLAT/files#3"} {
		q := queued(pr)
		q.PRID = len(st.queue) + 1
		st.queue = append(st.queue, q)
	}
	m := &fakeMerger{result: okResult(nil, nil)}
	w := newWorker(st, m)
	w.drain(context.Background())
	if len(m.prompts) != 3 {
		t.Fatalf("folded %d notes, want 3", len(m.prompts))
	}
	for i, want := range []string{"PR PLAT/files#1,", "PR PLAT/files#2,", "PR PLAT/files#3,"} {
		if !strings.Contains(m.prompts[i], want) {
			t.Errorf("fold %d prompt lacks %q", i, want)
		}
	}
	if len(st.marked) != 3 || st.marked[0] != "PLAT|files|1" || st.marked[2] != "PLAT|files|3" {
		t.Errorf("marked = %v", st.marked)
	}
	if st.nextQueuedN != 4 {
		t.Errorf("NextQueued called %d times, want 4 (three notes, then none)", st.nextQueuedN)
	}
	if len(st.queue) != 0 {
		t.Errorf("queue left = %+v", st.queue)
	}
}

func TestDrainStopsOnQueueError(t *testing.T) {
	st := newFakeStore()
	st.queue = []store.QueuedNote{queued("PLAT/files#1")}
	st.queueErr = errors.New("db down")
	m := &fakeMerger{result: okResult(nil, nil)}
	newWorker(st, m).drain(context.Background())
	if len(m.prompts) != 0 || st.nextQueuedN != 1 {
		t.Errorf("drain folded %d notes after %d reads, want none", len(m.prompts), st.nextQueuedN)
	}
}

func TestDrainStopsOnContext(t *testing.T) {
	st := newFakeStore()
	st.queue = []store.QueuedNote{queued("PLAT/files#1")}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	newWorker(st, &fakeMerger{result: okResult(nil, nil)}).drain(ctx)
	if st.nextQueuedN != 0 {
		t.Errorf("drain read the queue %d times on a cancelled context", st.nextQueuedN)
	}
}

func TestWakeNeverBlocks(t *testing.T) {
	w := newWorker(newFakeStore(), &fakeMerger{result: okResult(nil, nil)})
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.Wake()
		w.Wake()
		w.Wake()
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Wake blocked")
	}
	if len(w.wake) != 1 {
		t.Errorf("pending wakes = %d, want one", len(w.wake))
	}
}
