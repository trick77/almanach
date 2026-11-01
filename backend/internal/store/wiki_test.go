package store

import (
	"context"
	"encoding/json"
	"io/fs"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trick77/almanach/internal/diagram"
	"github.com/trick77/ship/kit/migrate"
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
	if n := count(t, s, "revisions", "repo_id = (SELECT id FROM repos WHERE slug = 'svc') AND scope = 'repository'"); n != 3 {
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

// A project-scope revision stores NULL repo_id and the page keys on repo_id 0.
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
	if n := count(t, s, "revisions", "scope = 'project' AND repo_id IS NULL"); n != 2 {
		t.Errorf("project revisions with NULL repo_id = %d, want 2", n)
	}
	if n := count(t, s, "pages", "scope = 'project' AND project_id = (SELECT id FROM projects WHERE key = 'PRJ') AND repo_id = 0"); n != 1 {
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

// The repository map: set by the map step on the current revision in
// place (no revision of its own), with its basis and the run's end; carried
// with its basis by every repository revision after it (a describe step, a
// merge); replaced only by the map step again. A project revision never
// holds one, and the carry never reaches it.
func TestSetRepoMap_InPlaceAndCarried(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	sourceRepo(t, s, "PRJ", "svc")
	r := enqueue(t, s, "PRJ", "svc")
	m1 := &diagram.Diagram{Kind: diagram.KindFlow, Direction: "LR",
		Nodes: []diagram.Node{{ID: "api", Label: "HTTP-API", Shape: "step", Zone: "entry"}, {ID: "db", Label: "Datenbank", Shape: "step", Zone: "stores"}},
		Edges: []diagram.Edge{{From: "api", To: "db", Label: "DB", Head: "end"}},
		Zones: []diagram.Zone{{ID: "entry", Title: "Eingang"}, {ID: "stores", Title: "Datenspeicher"}}}
	m2 := &diagram.Diagram{Kind: diagram.KindFlow, Direction: "LR", Nodes: []diagram.Node{{ID: "cli", Label: "Befehle", Shape: "step", Zone: "entry"}},
		Zones: []diagram.Zone{{ID: "entry", Title: "Eingang"}}}
	repoRev := NewRevision{Scope: ScopeRepository, ProjectKey: "PRJ", RepoSlug: "svc", Summary: "r", SourceKind: FromBaseline, SourceCommit: "abc"}
	if _, err := s.InsertRevision(ctx, repoRev); err != nil {
		t.Fatal(err)
	}
	if p, _, _ := s.CurrentPage(ctx, ScopeRepository, "PRJ", "svc"); p.RepoMap != nil || p.RepoMapBasis != "" {
		t.Fatalf("map before any was drawn = %+v, basis %q", p.RepoMap, p.RepoMapBasis)
	}
	page := func() Page {
		t.Helper()
		p, ok, err := s.CurrentPage(ctx, ScopeRepository, "PRJ", "svc")
		if err != nil || !ok {
			t.Fatalf("CurrentPage: ok=%v err=%v", ok, err)
		}
		return p
	}
	// In place: the revision stays 1, the run ends with it.
	r.State = RunDone
	if ok, err := s.SetRepoMap(ctx, "PRJ", "svc", m1, "b1", r); err != nil || !ok {
		t.Fatalf("SetRepoMap: ok=%v err=%v", ok, err)
	}
	if p := page(); p.Revision != 1 || !diagram.Equal(p.RepoMap, m1) || p.RepoMapBasis != "b1" {
		t.Errorf("after SetRepoMap: revision %d, map %+v, basis %q", p.Revision, p.RepoMap, p.RepoMapBasis)
	}
	if got, _, _ := s.RunByID(ctx, r.ID); got.State != RunDone {
		t.Errorf("run state = %q, want done with the map", got.State)
	}
	// A describe step and a merge carry map and basis.
	for i, rev := range []NewRevision{repoRev, {Scope: ScopeRepository, ProjectKey: "PRJ", RepoSlug: "svc", Summary: "merge", SourcePR: "PRJ/svc#9"}} {
		if _, err := s.InsertRevision(ctx, rev); err != nil {
			t.Fatalf("revision %d: %v", i, err)
		}
		if p := page(); !diagram.Equal(p.RepoMap, m1) || p.RepoMapBasis != "b1" {
			t.Errorf("after revision %d: map %+v, basis %q", i, p.RepoMap, p.RepoMapBasis)
		}
	}
	// Replaced in place on the current revision, the earlier ones as they were.
	r2 := enqueue(t, s, "PRJ", "svc")
	r2.State = RunDone
	if ok, err := s.SetRepoMap(ctx, "PRJ", "svc", m2, "b2", r2); err != nil || !ok {
		t.Fatalf("SetRepoMap again: ok=%v err=%v", ok, err)
	}
	if p := page(); p.Revision != 3 || !diagram.Equal(p.RepoMap, m2) || p.RepoMapBasis != "b2" {
		t.Errorf("after the second map: revision %d, map %+v, basis %q", p.Revision, p.RepoMap, p.RepoMapBasis)
	}
	var older int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM revisions WHERE scope = 'repository' AND repo_map_basis = 'b1'`).Scan(&older); err != nil || older != 2 {
		t.Errorf("revisions keeping the first map = %d, err %v; want revisions 1 and 2", older, err)
	}
	// A run over or gone writes nothing.
	if ok, err := s.SetRepoMap(ctx, "PRJ", "svc", m1, "b3", r2); err != nil || ok {
		t.Errorf("SetRepoMap of a done run: ok=%v err=%v", ok, err)
	}
	if p := page(); p.RepoMapBasis != "b2" {
		t.Errorf("basis after a refused write = %q", p.RepoMapBasis)
	}
	// None drawn: the basis set, the map SQL NULL, read back as no map (the
	// JSON text null read as an empty map once).
	r3 := enqueue(t, s, "PRJ", "svc")
	r3.State = RunDone
	if ok, err := s.SetRepoMap(ctx, "PRJ", "svc", nil, "b4", r3); err != nil || !ok {
		t.Fatalf("SetRepoMap of none: ok=%v err=%v", ok, err)
	}
	if p := page(); p.RepoMap != nil || p.RepoMapBasis != "b4" {
		t.Errorf("after none drawn: map %+v, basis %q; want no map, basis b4", p.RepoMap, p.RepoMapBasis)
	}
	var nulls int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM revisions WHERE repo_map_basis = 'b4' AND repo_map IS NULL`).Scan(&nulls); err != nil || nulls != 1 {
		t.Errorf("none drawn stored as SQL NULL: %d rows, err %v", nulls, err)
	}
	// Another repository of the project carries nothing of svc's.
	if _, err := s.InsertRevision(ctx, NewRevision{Scope: ScopeRepository, ProjectKey: "PRJ", RepoSlug: "web", Summary: "w", SourcePR: "PRJ/web#1"}); err != nil {
		t.Fatal(err)
	}
	if p, _, _ := s.CurrentPage(ctx, ScopeRepository, "PRJ", "web"); p.RepoMap != nil || p.RepoMapBasis != "" {
		t.Errorf("web took svc's map: %+v", p.RepoMap)
	}
	if _, err := s.InsertRevision(ctx, NewRevision{Scope: ScopeProject, ProjectKey: "PRJ", Summary: "p", SourcePR: "PRJ/svc#1"}); err != nil {
		t.Fatal(err)
	}
	if p, _, _ := s.CurrentPage(ctx, ScopeProject, "PRJ", ""); p.RepoMap != nil {
		t.Errorf("project page map of a repository = %+v", p.RepoMap)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE revisions SET repo_map = '{}' WHERE scope = 'project'`); err == nil {
		t.Error("a project revision took a repository map")
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE revisions SET repo_map_basis = 'x' WHERE scope = 'project'`); err == nil {
		t.Error("a project revision took a repository map basis")
	}
}

// 0004 over a database holding 0001's rows (0002, 0003 between): revisions gain repo_map NULL,
// the ledger keeps its rows and ids and takes the map step's purpose.
func TestMigrate_RepoMapOverExistingRows(t *testing.T) {
	s := emptyStore(t)
	ctx := context.Background()
	first, err := fs.ReadFile(migrations(), "0001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrate.Up(ctx, s.db, fstest.MapFS{"0001_init.sql": {Data: first}}, s.migrateOptions()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO revisions (scope, project_key, repo_slug, revision, summary, statements, chars, source_pr_key, created_at)
		VALUES ('repository', 'PRJ', 'svc', 1, 'r', '[]', 1, 'PRJ/svc#1', '2026-10-01T12:00:00.000000000Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO pages (scope, project_key, repo_slug, current_revision_id, updated_at)
		VALUES ('repository', 'PRJ', 'svc', 1, '2026-10-01T12:00:00.000000000Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO llm_calls (id, project_key, repo_slug, source_kind, source_commit, purpose, outcome, created_at)
		VALUES (41, 'PRJ', 'svc', 'baseline', 'abc', 'project', 'ok', '2026-10-01T12:00:00.000000000Z')`); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("0004 over 0001's rows: %v", err)
	}
	p, ok, err := s.CurrentPage(ctx, ScopeRepository, "PRJ", "svc")
	if err != nil || !ok || p.RepoMap != nil || p.RepoMapBasis != "" || p.Summary != "r" {
		t.Fatalf("page after 0004 = %+v, ok=%v, err=%v", p, ok, err)
	}
	if n := count(t, s, "llm_calls", "id = 41 AND purpose = 'project'"); n != 1 {
		t.Errorf("ledger row kept = %d, want 1", n)
	}
	id, err := s.InsertLLMCall(ctx, LLMCall{ProjectKey: "PRJ", RepoSlug: "svc", SourceKind: FromBaseline, SourceCommit: "abc", Purpose: PurposeRepoMap, Outcome: "ok"})
	if err != nil || id != 42 {
		t.Errorf("map step's ledger row: id %d, err %v, want 42", id, err)
	}
	if n := count(t, s, "sqlite_master", "type = 'index' AND name = 'llm_calls_run_idx'"); n != 1 {
		t.Errorf("ledger index after the rebuild = %d, want 1", n)
	}
}

func itoa(v int64) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// A basis hashed from text stored before the layout stands when a read
// only laid that text out anew; a real change still makes the map due.
// TRANSITIONAL(layout): Remove with Page.Stored.
func TestMapStandsOverRelaidText(t *testing.T) {
	stored := Page{Summary: "Ein Dienst.\n\n\n  Mehr.", Statements: []Statement{{ID: "s1", Kind: "rule", Name: "R", Text: "* eins"}}}
	read := stored
	read.RepoMapBasis = stored.MapBasis()
	read.Summary, read.Statements = "Ein Dienst.\n\nMehr.", []Statement{{ID: "s1", Kind: "rule", Name: "R", Text: "- eins"}}
	if read.MapStands() {
		t.Fatal("without what was stored, a relaid page's old basis cannot stand")
	}
	read.Stored = &StoredText{Summary: stored.Summary, Texts: []string{"* eins"}}
	if !read.MapStands() {
		t.Error("a map over text only laid out anew is due again")
	}
	read.Statements[0].Name = "S"
	if read.MapStands() {
		t.Error("a renamed statement left the map standing")
	}
}
