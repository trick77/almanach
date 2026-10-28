package consolidate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/trick77/almanach/internal/config"
	"github.com/trick77/almanach/internal/inference"
	"github.com/trick77/almanach/internal/prompts"
	"github.com/trick77/almanach/internal/store"
)

var (
	limits   = config.Limits{RepoMaxStatements: 3, ProjectMaxStatements: 2, SummaryMaxChars: 80, StatementMaxChars: 30}
	mergedAt = time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	older    = time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
)

// fakeStore is the worker's store in memory: pages by scope|project|repo,
// a queue popped oldest-first, every write recorded.
type fakeStore struct {
	queue        []store.QueuedNote
	queueErr     error
	markErr      error
	pages        map[string]store.Page
	marked       []string
	revisions    []store.NewRevision
	calls        []store.LLMCall
	nextQueuedN  int
	revisionErr  error
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

func (f *fakeStore) CurrentPage(_ context.Context, scope, project, repo string) (store.Page, bool, error) {
	p, ok := f.pages[pageKey(scope, project, repo)]
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
	return m.result
}

func okResult(repo, project *inference.Description) inference.MergeResult {
	nano := int64(2_000_000)
	return inference.MergeResult{Outcome: inference.OutcomeOK, Merge: inference.Merge{Repository: repo, Project: project},
		Cost: inference.CallCost{Model: "gpt-x", CallID: "call-9", InputTokens: 100, CachedTokens: 20, OutputTokens: 10, NanoUSD: &nano, Answered: true}}
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
	long := strings.Repeat("x", limits.StatementMaxChars+1)
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
		{"statement text over cap", &inference.Description{Summary: "s", Statements: []inference.Statement{{Kind: "rule", Name: "n", Text: long}}}},
		{"summary over cap", &inference.Description{Summary: strings.Repeat("y", limits.SummaryMaxChars+1), Statements: nil}},
		{"empty summary", &inference.Description{Summary: "  ", Statements: nil}},
		{"name over cap", &inference.Description{Summary: "s", Statements: []inference.Statement{{Kind: "rule", Name: strings.Repeat("n", nameMaxChars+1), Text: "t"}}}},
		{"too many files", &inference.Description{Summary: "s", Statements: []inference.Statement{{Kind: "rule", Name: "n", Text: "t",
			Files: []string{"a", "b", "c", "d", "e", "f"}}}}},
		{"path over cap", &inference.Description{Summary: "s", Statements: []inference.Statement{{Kind: "rule", Name: "n", Text: "t",
			Files: []string{strings.Repeat("p", pathMaxChars+1)}}}}},
		{"path with whitespace", &inference.Description{Summary: "s", Statements: []inference.Statement{{Kind: "rule", Name: "n", Text: "t",
			Files: []string{"internal/a file.go"}}}}},
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

func TestFoldFailedCall(t *testing.T) {
	for _, outcome := range []inference.Outcome{inference.OutcomeError, inference.OutcomeTimedOut, inference.OutcomeUnparseable} {
		st := newFakeStore()
		m := &fakeMerger{result: inference.MergeResult{Outcome: outcome, Err: errors.New("boom"),
			Merge: inference.Merge{Repository: &inference.Description{Summary: "s", Statements: nil}}}}
		newWorker(st, m).Fold(context.Background(), queued("PLAT/files#42"))
		if len(st.revisions) != 0 {
			t.Errorf("%s: revisions = %+v, want none", outcome, st.revisions)
		}
		if len(st.marked) != 1 {
			t.Errorf("%s: marked = %v, want the note marked", outcome, st.marked)
		}
		if len(st.calls) != 1 || st.calls[0].Outcome != outcome.String() {
			t.Errorf("%s: calls = %+v, want one row with the outcome", outcome, st.calls)
		}
	}
}

func TestRender(t *testing.T) {
	q := queued("PLAT/files#42")
	q.TicketKey, q.ChangeKind = "", ""
	q.Body = json.RawMessage(`{"components":[{"name":"x"}]}`)
	empty := store.Page{}
	out := render(prompts.Merge, q, existingRepoPage(), empty, limits)
	for _, ph := range []string{"{repo}", "{project}", "{pr_key}", "{ticket_key}", "{change_kind}", "{repo_max_statements}",
		"{project_max_statements}", "{statement_max_chars}", "{summary_max_chars}", "{repo_description}", "{project_description}", "{note}"} {
		if strings.Contains(out, ph) {
			t.Errorf("placeholder %s left in the prompt", ph)
		}
	}
	for _, want := range []string{"`files`", "`PLAT`", "PR PLAT/files#42, ticket none, kind unknown", "at most 3 statements", "at most 2 statements",
		"at most 30 characters", "at most 80", "Der Dienst speichert Dateien.", "- [s1] component: Uploader: Nimmt Dateien an. (internal/files/upload.go)",
		"_None yet._", `{"components":[{"name":"x"}]}`} {
		if !strings.Contains(out, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
	if strings.Count(out, "_None yet._") != 1 {
		t.Errorf("_None yet._ appears %d times, want once (the project page only)", strings.Count(out, "_None yet._"))
	}
}

func TestDescribe(t *testing.T) {
	if got := describe(store.Page{RevisionID: 0, Summary: "ignored"}); got != "_None yet._" {
		t.Errorf("describe(no revision) = %q", got)
	}
	p := existingRepoPage()
	p.Statements = append(p.Statements, store.Statement{ID: "s2", Kind: "rule", Name: "R", Text: "T"})
	got := describe(p)
	if !strings.HasPrefix(got, "Der Dienst speichert Dateien.\n\n- [s1] component: Uploader: Nimmt Dateien an. (internal/files/upload.go)\n- [s2] rule: R: T\n") {
		t.Errorf("describe = %q", got)
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
