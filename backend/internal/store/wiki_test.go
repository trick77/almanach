package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/trick77/almanach/internal/diagram"
)

// The queue hands out the oldest unconsolidated note; marking it takes it
// off; empty is ok=false.
func TestNextQueued_OldestFirstUntilMarked(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if _, ok, err := s.NextQueued(ctx); err != nil || ok {
		t.Fatalf("empty queue: ok=%v err=%v", ok, err)
	}
	ingest(t, s, noteFor("PRJ", "svc", 1))
	ingest(t, s, noteFor("PRJ", "svc", 2))
	// Order is received_at; pin it so the test never depends on the clock.
	if _, err := s.db.ExecContext(ctx, `UPDATE notes SET received_at = ? WHERE pr_id = 1`, stamp(mergedBase)); err != nil {
		t.Fatal(err)
	}

	q, ok, err := s.NextQueued(ctx)
	if err != nil || !ok {
		t.Fatalf("NextQueued: ok=%v err=%v", ok, err)
	}
	if q.PRID != 1 || q.ProjectKey != "PRJ" || q.RepoSlug != "svc" || q.Team != "alpha" {
		t.Fatalf("NextQueued = %s/%s#%d team %s, want PRJ/svc#1 alpha", q.ProjectKey, q.RepoSlug, q.PRID, q.Team)
	}
	if q.TicketKey != "ABC-1" || q.ChangeKind != "feature" || q.RunID != 101 || q.ModelLabel != "m" {
		t.Errorf("lifted columns = ticket %q kind %q run %d model %q", q.TicketKey, q.ChangeKind, q.RunID, q.ModelLabel)
	}
	if string(q.Body) == "" || q.ReceivedAt.IsZero() || !q.MergedAt.Equal(mergedBase.Add(time.Minute)) {
		t.Errorf("body/received/merged = %q %v %v", q.Body, q.ReceivedAt, q.MergedAt)
	}
	if len(q.Bugfix) != 0 {
		t.Errorf("Bugfix = %q, want empty on a feature note", q.Bugfix)
	}

	// A deferred fold counts its attempt and leaves the note where it is.
	for want := 1; want <= 2; want++ {
		if n, err := s.DeferFold(ctx, "PRJ", "svc", 1); err != nil || n != want {
			t.Fatalf("DeferFold = %d, %v; want %d", n, err, want)
		}
	}
	if q, ok, err := s.NextQueued(ctx); err != nil || !ok || q.PRID != 1 {
		t.Fatalf("after defer: ok=%v pr=%d err=%v, want pr 1 still queued", ok, q.PRID, err)
	}

	if err := s.MarkConsolidated(ctx, "PRJ", "svc", 1); err != nil {
		t.Fatal(err)
	}
	q, ok, err = s.NextQueued(ctx)
	if err != nil || !ok || q.PRID != 2 {
		t.Fatalf("after mark: ok=%v pr=%d err=%v, want pr 2", ok, q.PRID, err)
	}
	if err := s.MarkConsolidated(ctx, "PRJ", "svc", 2); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.NextQueued(ctx); err != nil || ok {
		t.Fatalf("drained queue: ok=%v err=%v", ok, err)
	}
}

func fixtureStatements() []Statement {
	return []Statement{{
		ID: "s1", Kind: "component", Name: "Files", Text: "Serves files.",
		Files:   []string{"internal/files/files.go"},
		Sources: []Source{{PRKey: "PRJ/svc#1", TicketKey: "ABC-1", MergedAt: mergedBase}},
	}, {
		ID: "s2", Kind: "rule", Name: "No redirects", Text: "Never follows.",
		Files:   []string{},
		Sources: []Source{{PRKey: "PRJ/svc#2", MergedAt: mergedBase.Add(time.Hour)}},
	}}
}

// Revisions number 1, 2, 3 per page, the page follows the newest, the
// statements survive the JSON column.
func TestInsertRevision_NumbersAndRepoints(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if _, ok, err := s.CurrentPage(ctx, ScopeRepository, "PRJ", "svc"); err != nil || ok {
		t.Fatalf("CurrentPage before any revision: ok=%v err=%v", ok, err)
	}
	if n, err := s.NextStatementID(ctx, ScopeRepository, "PRJ", "svc"); err != nil || n != 1 {
		t.Fatalf("NextStatementID before any revision = %d, %v; want 1", n, err)
	}
	callID, err := s.InsertLLMCall(ctx, LLMCall{ProjectKey: "PRJ", RepoSlug: "svc", SourcePR: "PRJ/svc#1", Outcome: "ok",
		Model: "test-model", InputTokens: 10, CachedTokens: 5, OutputTokens: 3, CostNanoUSD: i64(42), CallID: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	if callID == 0 {
		t.Fatal("InsertLLMCall returned id 0")
	}
	for i, want := range []int{1, 2, 3} {
		rev := NewRevision{Scope: ScopeRepository, ProjectKey: "PRJ", RepoSlug: "svc", Summary: "rev " + string(rune('0'+want)),
			Statements: fixtureStatements(), Chars: 100 + i, SourcePR: "PRJ/svc#1"}
		if i == 0 {
			rev.LLMCallID = &callID
		}
		n, err := s.InsertRevision(ctx, rev)
		if err != nil {
			t.Fatalf("revision %d: %v", want, err)
		}
		if n != want {
			t.Fatalf("revision number = %d, want %d", n, want)
		}
	}
	p, ok, err := s.CurrentPage(ctx, ScopeRepository, "PRJ", "svc")
	if err != nil || !ok {
		t.Fatalf("CurrentPage: ok=%v err=%v", ok, err)
	}
	if p.Revision != 3 || p.Summary != "rev 3" || p.SourcePR != "PRJ/svc#1" || p.RepoSlug != "svc" || p.UpdatedAt.IsZero() {
		t.Errorf("page = rev %d %q src %q repo %q", p.Revision, p.Summary, p.SourcePR, p.RepoSlug)
	}
	got, _ := json.Marshal(p.Statements)
	want, _ := json.Marshal(fixtureStatements())
	if string(got) != string(want) {
		t.Errorf("statements round-trip:\n got %s\nwant %s", got, want)
	}
	if n := count(t, s, "revisions", "repo_slug = 'svc' AND scope = 'repository'"); n != 3 {
		t.Errorf("revision rows = %d, want 3", n)
	}
	if n := count(t, s, "revisions", "revision = 1 AND llm_call_id = "+itoa(callID)); n != 1 {
		t.Errorf("revision 1 llm_call_id rows = %d, want 1", n)
	}

	// A second page numbers independently.
	if n, err := s.InsertRevision(ctx, NewRevision{Scope: ScopeRepository, ProjectKey: "PRJ", RepoSlug: "other", Summary: "o", SourcePR: "x"}); err != nil || n != 1 {
		t.Fatalf("other repo revision = %d err=%v, want 1", n, err)
	}

	// The next statement id is one past every id the page's history used,
	// a dropped one included: revision 4 keeps s1 only, s2 stays taken.
	if _, err := s.InsertRevision(ctx, NewRevision{Scope: ScopeRepository, ProjectKey: "PRJ", RepoSlug: "svc", Summary: "r4",
		Statements: fixtureStatements()[:1], SourcePR: "PRJ/svc#9"}); err != nil {
		t.Fatal(err)
	}
	if n, err := s.NextStatementID(ctx, ScopeRepository, "PRJ", "svc"); err != nil || n != 3 {
		t.Errorf("NextStatementID after dropping s2 = %d, %v; want 3", n, err)
	}
	if n, err := s.NextStatementID(ctx, ScopeRepository, "PRJ", "other"); err != nil || n != 1 {
		t.Errorf("NextStatementID of a page without statements = %d, %v; want 1", n, err)
	}
}

// A project-scope revision stores NULL repo_slug and the page keys on the empty slug.
func TestInsertRevision_ProjectScope(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	for _, want := range []int{1, 2} {
		n, err := s.InsertRevision(ctx, NewRevision{Scope: ScopeProject, ProjectKey: "PRJ", Summary: "p", SourcePR: "PRJ/svc#1", Statements: nil})
		if err != nil {
			t.Fatalf("project revision %d: %v", want, err)
		}
		if n != want {
			t.Fatalf("project revision number = %d, want %d", n, want)
		}
	}
	if n := count(t, s, "revisions", "scope = 'project' AND repo_slug IS NULL"); n != 2 {
		t.Errorf("project revisions with NULL repo_slug = %d, want 2", n)
	}
	if n := count(t, s, "pages", "scope = 'project' AND project_key = 'PRJ' AND repo_slug = ''"); n != 1 {
		t.Errorf("project page rows = %d, want 1", n)
	}
	p, ok, err := s.CurrentPage(ctx, ScopeProject, "PRJ", "")
	if err != nil || !ok {
		t.Fatalf("CurrentPage project: ok=%v err=%v", ok, err)
	}
	if p.Revision != 2 || p.Statements == nil || len(p.Statements) != 0 {
		t.Errorf("project page = rev %d statements %v, want 2 and []", p.Revision, p.Statements)
	}
	if p.Map != nil {
		t.Errorf("project page map = %+v, want none before one is drawn", p.Map)
	}
}

// The map and a statement's diagram come back as written: the map in its
// own column, the diagram inside the statements. A repository revision
// carries no map, the column's check refuses one.
func TestInsertRevision_Diagrams(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	m := &diagram.Diagram{Kind: diagram.KindFlow, Direction: "LR",
		Nodes: []diagram.Node{{ID: "svc", Label: "svc", Shape: "step", Zone: "repositories"}, {ID: "pg", Label: "PostgreSQL", Shape: "step", Zone: "stores"}},
		Edges: []diagram.Edge{{From: "svc", To: "pg", Label: "DB", Head: "end"}},
		Zones: []diagram.Zone{{ID: "repositories", Title: "Repositories"}, {ID: "stores", Title: "Datenspeicher"}}}
	seq := &diagram.Diagram{Kind: diagram.KindSequence, Actors: []diagram.Actor{{ID: "a", Label: "A"}},
		Rows: []diagram.Row{{Type: diagram.RowFrame, Keyword: "loop", Label: "jede Minute"}, {Type: diagram.RowMessage, From: "a", To: "a", Line: "solid", Head: "filled"}, {Type: diagram.RowEnd}}}
	_, err := s.InsertRevision(ctx, NewRevision{Scope: ScopeProject, ProjectKey: "PRJ", Summary: "p", SourceKind: FromBaseline, SourceCommit: "abc",
		Statements: []Statement{{ID: "s1", Kind: "rule", Name: "R", Text: "T", Files: []string{}, Diagram: seq}}, Map: m})
	if err != nil {
		t.Fatal(err)
	}
	p, ok, err := s.CurrentPage(ctx, ScopeProject, "PRJ", "")
	if err != nil || !ok {
		t.Fatalf("CurrentPage: ok=%v err=%v", ok, err)
	}
	if !diagram.Equal(p.Map, m) || !diagram.Equal(p.Statements[0].Diagram, seq) {
		t.Errorf("map = %+v, diagram = %+v", p.Map, p.Statements[0].Diagram)
	}
	if _, err := s.InsertRevision(ctx, NewRevision{Scope: ScopeRepository, ProjectKey: "PRJ", RepoSlug: "svc", Summary: "r", SourcePR: "PRJ/svc#1", Map: m}); err == nil {
		t.Error("a repository revision with a map was stored")
	}
}

func itoa(v int64) string {
	b, _ := json.Marshal(v)
	return string(b)
}
