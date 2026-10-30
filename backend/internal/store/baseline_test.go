package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// sourceRepo creates a static source key (if needed) with one repository.
func sourceRepo(t *testing.T, s *Store, key, slug string) {
	t.Helper()
	ctx := context.Background()
	if err := s.CreateSource(ctx, staticSource(key)); err != nil && !errors.Is(err, ErrExists) {
		t.Fatal(err)
	}
	if err := s.AddSourceRepo(ctx, SourceRepo{ProjectKey: key, Slug: slug, CloneURL: "https://forge.example.invalid/" + slug + ".git"}); err != nil {
		t.Fatal(err)
	}
}

func enqueue(t *testing.T, s *Store, key, slug string) BaselineRun {
	t.Helper()
	id, err := s.EnqueueRun(context.Background(), key, slug, RunFull, TriggerManual, "", "c0ffee")
	if err != nil {
		t.Fatalf("enqueue %s/%s: %v", key, slug, err)
	}
	r, ok, err := s.RunByID(context.Background(), id)
	if err != nil || !ok {
		t.Fatalf("RunByID %d: ok=%v err=%v", id, ok, err)
	}
	return r
}

func finish(t *testing.T, s *Store, r BaselineRun, state string) BaselineRun {
	t.Helper()
	now := time.Now().UTC()
	r.State, r.FinishedAt = state, &now
	if ok, err := s.SaveRun(context.Background(), r); err != nil || !ok {
		t.Fatalf("SaveRun: ok=%v err=%v", ok, err)
	}
	return r
}

// One open run per repository; a repository that is no source has none;
// once the run is over the next may start.
func TestEnqueueRun_OneOpenPerRepository(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	sourceRepo(t, s, "tools", "svc")
	r := enqueue(t, s, "tools", "svc")
	if r.State != RunQueued || r.Mode != RunFull || r.Trigger != TriggerManual || r.Commit != "c0ffee" || !r.Open() || r.Plan != nil {
		t.Fatalf("queued run = %+v", r)
	}
	if _, err := s.EnqueueRun(ctx, "tools", "svc", RunReconcile, TriggerMonthly, "c0ffee", "beef"); !errors.Is(err, ErrExists) {
		t.Fatalf("second open run: err = %v, want ErrExists", err)
	}
	if _, err := s.EnqueueRun(ctx, "tools", "nope", RunFull, TriggerManual, "", "beef"); !errors.Is(err, ErrNoRepo) {
		t.Fatalf("unknown repository: err = %v, want ErrNoRepo", err)
	}
	finish(t, s, r, RunFailed)
	if _, err := s.EnqueueRun(ctx, "tools", "svc", RunFull, TriggerManual, "", "beef"); err != nil {
		t.Fatalf("after the run failed: %v", err)
	}
}

// A started run goes to its end before a queued one begins, even an older
// one; done and failed runs are never handed out.
func TestNextRun_StartedBeforeQueued(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if _, ok, err := s.NextRun(ctx); err != nil || ok {
		t.Fatalf("no runs: ok=%v err=%v", ok, err)
	}
	sourceRepo(t, s, "tools", "a")
	sourceRepo(t, s, "tools", "b")
	a := enqueue(t, s, "tools", "a")
	b := enqueue(t, s, "tools", "b")
	if next, ok, err := s.NextRun(ctx); err != nil || !ok || next.ID != a.ID {
		t.Fatalf("both queued: next=%d ok=%v err=%v, want the older %d", next.ID, ok, err, a.ID)
	}
	now := time.Now().UTC()
	b.State, b.StartedAt, b.Step, b.Steps = RunDescribing, &now, 2, 5
	b.Plan = json.RawMessage(`{"subjects":[]}`)
	if ok, err := s.SaveRun(ctx, b); err != nil || !ok {
		t.Fatalf("SaveRun: ok=%v err=%v", ok, err)
	}
	next, ok, err := s.NextRun(ctx)
	if err != nil || !ok || next.ID != b.ID {
		t.Fatalf("b started: next=%d ok=%v err=%v, want %d", next.ID, ok, err, b.ID)
	}
	if next.Step != 2 || next.Steps != 5 || next.StartedAt == nil || string(next.Plan) != `{"subjects": []}` {
		t.Fatalf("saved progress = step %d/%d started %v plan %s", next.Step, next.Steps, next.StartedAt, next.Plan)
	}
	finish(t, s, next, RunDone)
	finish(t, s, a, RunDone)
	if _, ok, err := s.NextRun(ctx); err != nil || ok {
		t.Fatalf("all done: ok=%v err=%v", ok, err)
	}
}

// Removing the repository removes its runs: progress on a gone run reports
// false and a step writes nothing.
func TestBaselineRun_GoneWithItsRepository(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	sourceRepo(t, s, "tools", "svc")
	r := enqueue(t, s, "tools", "svc")
	if _, err := s.DeleteSourceRepo(ctx, "tools", "svc"); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.SaveRun(ctx, r); err != nil || ok {
		t.Fatalf("SaveRun of a gone run: ok=%v err=%v", ok, err)
	}
	if _, ok, err := s.RunByID(ctx, r.ID); err != nil || ok {
		t.Fatalf("RunByID of a gone run: ok=%v err=%v", ok, err)
	}
	_, ok, err := s.ApplyBaselineStep(ctx, baselineRevision("tools", "svc", r.ID), r)
	if err != nil || ok {
		t.Fatalf("ApplyBaselineStep of a gone run: ok=%v err=%v", ok, err)
	}
	if n := count(t, s, "revisions", ""); n != 0 {
		t.Fatalf("revisions = %d, want none written for a gone run", n)
	}
}

func baselineRevision(key, slug string, runID int64) NewRevision {
	return NewRevision{
		Scope: ScopeRepository, ProjectKey: key, RepoSlug: slug, Summary: "Read from the code.",
		Statements: []Statement{{ID: "s1", Kind: "component", Name: "Core", Text: "The core.", Files: []string{"core.go"},
			Sources: []Source{{Kind: FromBaseline, Commit: "c0ffee", CommittedAt: mergedBase}}, Checked: "c0ffee"}},
		Chars: 20, SourceKind: FromBaseline, SourceCommit: "c0ffee", BaselineRunID: &runID,
	}
}

// A step's revision and the run's progress land together; the page then
// says it came from a commit, not a PR.
func TestApplyBaselineStep_RevisionAndProgressTogether(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	sourceRepo(t, s, "tools", "svc")
	r := enqueue(t, s, "tools", "svc")
	r.State, r.Step, r.Steps, r.Calls, r.Bytes = RunDescribing, 1, 3, 2, 4096
	n, ok, err := s.ApplyBaselineStep(ctx, baselineRevision("tools", "svc", r.ID), r)
	if err != nil || !ok || n != 1 {
		t.Fatalf("ApplyBaselineStep = %d ok=%v err=%v, want revision 1", n, ok, err)
	}
	got, _, _ := s.RunByID(ctx, r.ID)
	if got.State != RunDescribing || got.Step != 1 || got.Calls != 2 || got.Bytes != 4096 {
		t.Fatalf("run after the step = %+v", got)
	}
	p, ok, err := s.CurrentPage(ctx, ScopeRepository, "tools", "svc")
	if err != nil || !ok {
		t.Fatalf("CurrentPage: ok=%v err=%v", ok, err)
	}
	if p.SourceKind != FromBaseline || p.SourceCommit != "c0ffee" || p.SourcePR != "" {
		t.Fatalf("page source = %q %q %q, want baseline at c0ffee", p.SourceKind, p.SourceCommit, p.SourcePR)
	}
	st := p.Statements[0]
	if st.Checked != "c0ffee" || len(st.Sources) != 1 || !st.Sources[0].IsBaseline() || st.Sources[0].Commit != "c0ffee" {
		t.Fatalf("statement = %+v", st)
	}
	var runID *int64
	if err := s.pool.QueryRow(ctx, `SELECT baseline_run_id FROM revisions`).Scan(&runID); err != nil || runID == nil || *runID != r.ID {
		t.Fatalf("baseline_run_id = %v err=%v, want %d", runID, err, r.ID)
	}
}

// Exactly one of the PR key and the commit names a revision's source.
func TestInsertRevision_SourceKindChecks(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	noCommit := baselineRevision("tools", "svc", 1)
	noCommit.SourceCommit = ""
	if _, err := s.InsertRevision(ctx, noCommit); err == nil || !strings.Contains(err.Error(), "revisions_source_commit") {
		t.Fatalf("baseline without a commit: err = %v", err)
	}
	noPR := NewRevision{Scope: ScopeProject, ProjectKey: "PRJ", Summary: "x", Chars: 1}
	if _, err := s.InsertRevision(ctx, noPR); err == nil || !strings.Contains(err.Error(), "revisions_source_pr") {
		t.Fatalf("PR revision without a PR key: err = %v", err)
	}
	pr := NewRevision{Scope: ScopeProject, ProjectKey: "PRJ", Summary: "x", Chars: 1, SourcePR: "PRJ/svc#1"}
	if _, err := s.InsertRevision(ctx, pr); err != nil {
		t.Fatalf("PR revision: %v", err)
	}
	p, _, _ := s.CurrentPage(ctx, ScopeProject, "PRJ", "")
	if p.SourceKind != FromPR || p.SourcePR != "PRJ/svc#1" || p.SourceCommit != "" {
		t.Fatalf("page source = %q %q %q, want the PR", p.SourceKind, p.SourcePR, p.SourceCommit)
	}
}

// A note of a repository with an open run waits for the run; notes of
// other repositories do not.
func TestNextQueued_WaitsForTheRepositorysOpenRun(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	ingest(t, s, noteFor("PRJ", "svc", 1))
	sourceRepo(t, s, "PRJ", "svc")
	r := enqueue(t, s, "PRJ", "svc")
	if _, ok, err := s.NextQueued(ctx); err != nil || ok {
		t.Fatalf("open run: ok=%v err=%v, want the note held", ok, err)
	}
	ingest(t, s, noteFor("PRJ", "other", 2))
	if q, ok, err := s.NextQueued(ctx); err != nil || !ok || q.PRID != 2 {
		t.Fatalf("other repository: pr=%d ok=%v err=%v, want 2", q.PRID, ok, err)
	}
	if err := s.MarkConsolidated(ctx, "PRJ", "other", 2); err != nil {
		t.Fatal(err)
	}
	finish(t, s, r, RunDone)
	if q, ok, err := s.NextQueued(ctx); err != nil || !ok || q.PRID != 1 {
		t.Fatalf("run done: pr=%d ok=%v err=%v, want 1", q.PRID, ok, err)
	}
}

// The newest run, the commit last documented and the run's cost: what the
// source page shows per repository.
func TestSourceRuns_LastDocumentedAndCost(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	sourceRepo(t, s, "tools", "a")
	sourceRepo(t, s, "tools", "b")
	if runs, err := s.SourceRuns(ctx, "tools"); err != nil || len(runs) != 0 {
		t.Fatalf("no runs: %v %v", runs, err)
	}
	done := finish(t, s, enqueue(t, s, "tools", "a"), RunDone)
	failed := finish(t, s, enqueue(t, s, "tools", "a"), RunFailed)
	for _, cost := range []*int64{i64(1_000_000), nil, i64(2_500_000)} {
		if _, err := s.InsertLLMCall(ctx, LLMCall{ProjectKey: "tools", RepoSlug: "a", SourceKind: FromBaseline, SourceCommit: "c0ffee",
			Purpose: PurposeSubject, BaselineRunID: &failed.ID, Outcome: "ok", CostNanoUSD: cost}); err != nil {
			t.Fatal(err)
		}
	}
	open := enqueue(t, s, "tools", "b")

	runs, err := s.SourceRuns(ctx, "tools")
	if err != nil {
		t.Fatal(err)
	}
	a, b := runs["a"], runs["b"]
	if a.Last.ID != failed.ID || a.Documented != "c0ffee" || a.DocumentedAt == nil || a.LastCost == nil || *a.LastCost != 3_500_000 {
		t.Fatalf("a = last %d documented %q at %v cost %v", a.Last.ID, a.Documented, a.DocumentedAt, a.LastCost)
	}
	if b.Last.ID != open.ID || b.Documented != "" || b.DocumentedAt != nil || b.LastCost != nil {
		t.Fatalf("b = last %d documented %q cost %v", b.Last.ID, b.Documented, b.LastCost)
	}
	if cost, err := s.RunCost(ctx, failed.ID); err != nil || cost == nil || *cost != 3_500_000 {
		t.Fatalf("RunCost = %v %v", cost, err)
	}
	if cost, err := s.RunCost(ctx, done.ID); err != nil || cost != nil {
		t.Fatalf("RunCost of an unpriced run = %v %v, want nil", cost, err)
	}
	if last, ok, err := s.LastRun(ctx, "tools", "a"); err != nil || !ok || last.ID != failed.ID {
		t.Fatalf("LastRun = %d %v %v", last.ID, ok, err)
	}
	if doc, ok, err := s.DocumentedRun(ctx, "tools", "a"); err != nil || !ok || doc.ID != done.ID {
		t.Fatalf("DocumentedRun = %d %v %v", doc.ID, ok, err)
	}
	if _, ok, err := s.DocumentedRun(ctx, "tools", "b"); err != nil || ok {
		t.Fatalf("DocumentedRun of b: ok=%v err=%v", ok, err)
	}
}

// A note's merge call keeps its old shape; a baseline call carries its
// commit and purpose and never a PR key.
func TestInsertLLMCall_KindsAndPurposes(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if _, err := s.InsertLLMCall(ctx, LLMCall{ProjectKey: "PRJ", RepoSlug: "svc", SourcePR: "PRJ/svc#1", Outcome: "ok"}); err != nil {
		t.Fatal(err)
	}
	run := int64(7)
	if _, err := s.InsertLLMCall(ctx, LLMCall{ProjectKey: "PRJ", RepoSlug: "svc", SourceKind: FromBaseline, SourceCommit: "c0ffee",
		Purpose: PurposeCatalogue, BaselineRunID: &run, Outcome: "ok"}); err != nil {
		t.Fatal(err)
	}
	if n := count(t, s, "llm_calls", "purpose = 'merge' AND source_kind = 'pr' AND source_pr_key = 'PRJ/svc#1'"); n != 1 {
		t.Fatalf("merge rows = %d, want 1", n)
	}
	if n := count(t, s, "llm_calls", "purpose = 'catalogue' AND source_kind = 'baseline' AND source_pr_key IS NULL AND baseline_run_id = 7"); n != 1 {
		t.Fatalf("catalogue rows = %d, want 1", n)
	}
	if _, err := s.InsertLLMCall(ctx, LLMCall{ProjectKey: "PRJ", RepoSlug: "svc", SourceKind: FromBaseline, Outcome: "ok"}); err == nil {
		t.Fatal("a baseline call without a commit was stored")
	}
}

// The switch defaults on, is read with every source and turns off; a
// source repository is known as one.
func TestAutoDescribeAndIsSourceRepo(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	sourceRepo(t, s, "tools", "svc")
	src, _, _ := s.SourceByKey(ctx, "tools")
	if !src.AutoDescribe {
		t.Fatal("auto_describe defaults off, want on")
	}
	if ok, err := s.SetAutoDescribe(ctx, "tools", false); err != nil || !ok {
		t.Fatalf("SetAutoDescribe: ok=%v err=%v", ok, err)
	}
	if ok, err := s.SetAutoDescribe(ctx, "nope", false); err != nil || ok {
		t.Fatalf("SetAutoDescribe of an unknown source: ok=%v err=%v", ok, err)
	}
	rows, _ := s.Sources(ctx)
	jobs, _ := s.SyncJobs(ctx)
	src, _, _ = s.SourceByKey(ctx, "tools")
	if src.AutoDescribe || rows[0].AutoDescribe || jobs[0].Source.AutoDescribe {
		t.Fatalf("after turning off: byKey=%v list=%v job=%v", src.AutoDescribe, rows[0].AutoDescribe, jobs[0].Source.AutoDescribe)
	}
	for _, c := range []struct {
		key, slug string
		want      bool
	}{{"tools", "svc", true}, {"tools", "other", false}, {"PRJ", "svc", false}} {
		if got, err := s.IsSourceRepo(ctx, c.key, c.slug); err != nil || got != c.want {
			t.Errorf("IsSourceRepo(%s, %s) = %v %v, want %v", c.key, c.slug, got, err, c.want)
		}
	}
}

// A source stored before baselines existed reads as a PR; a baseline
// source carries its commit and none of a PR's fields.
func TestSource_JSONShapes(t *testing.T) {
	var old Source
	if err := json.Unmarshal([]byte(`{"pr_key":"PRJ/svc#1","ticket_key":"ABC-1","merged_at":"2026-10-01T12:00:00Z"}`), &old); err != nil {
		t.Fatal(err)
	}
	if old.IsBaseline() || old.Key() != "pr:PRJ/svc#1" || !old.MergedAt.Equal(mergedBase) {
		t.Fatalf("old source = %+v key %s", old, old.Key())
	}
	b := Source{Kind: FromBaseline, Commit: "c0ffee", CommittedAt: mergedBase}
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"kind":"baseline","commit":"c0ffee","committed_at":"2026-10-01T12:00:00Z"}` {
		t.Fatalf("baseline source JSON = %s", raw)
	}
	if b.Key() != "baseline:c0ffee" {
		t.Fatalf("baseline key = %s", b.Key())
	}
	pr, _ := json.Marshal(Source{PRKey: "PRJ/svc#1", MergedAt: mergedBase})
	if string(pr) != `{"pr_key":"PRJ/svc#1","merged_at":"2026-10-01T12:00:00Z"}` {
		t.Fatalf("PR source JSON = %s, want the shape stored before", pr)
	}
}
