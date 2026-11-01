package store

import (
	"context"
	"testing"
	"time"
)

// The pending notes in NextQueued's order, each with the started run it
// waits for; a folded note leaves them, with its merge call's outcome.
func TestPendingAndFoldedNotes(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	before := time.Now().Add(-time.Minute)
	ingest(t, s, noteFor("PRJ", "svc", 1))
	ingest(t, s, noteFor("PRJ", "other", 2))
	ingest(t, s, noteFor("PRJ", "gone", 3))
	sourceRepo(t, s, "PRJ", "svc")
	r := enqueue(t, s, "PRJ", "svc")
	r.State = RunDescribing
	if ok, err := s.SaveRun(ctx, r); err != nil || !ok {
		t.Fatalf("SaveRun: ok=%v err=%v", ok, err)
	}
	if _, err := s.DeferFold(ctx, "PRJ", "other", 2); err != nil {
		t.Fatal(err)
	}
	if one, err := s.PendingNotes(ctx, 1); err != nil || len(one) != 1 || one[0].RepoSlug != "svc" {
		t.Fatalf("PendingNotes limited to 1 = %+v, %v", one, err)
	}
	notes, err := s.PendingNotes(ctx, 50)
	if err != nil || len(notes) != 3 {
		t.Fatalf("PendingNotes = %+v, %v", notes, err)
	}
	if notes[0].RepoSlug != "svc" || notes[0].HeldBy != r.ID || notes[0].PRTitle != "PR 1" || notes[0].ChangeKind != "feature" {
		t.Errorf("first = %+v, want svc held by run %d", notes[0], r.ID)
	}
	if notes[1].RepoSlug != "other" || notes[1].HeldBy != 0 || notes[1].Attempts != 1 {
		t.Errorf("second = %+v, want other, free, 1 attempt", notes[1])
	}

	if _, err := s.InsertLLMCall(ctx, LLMCall{ProjectKey: "PRJ", RepoSlug: "other", SourcePR: "PRJ/other#2", Outcome: "error"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertLLMCall(ctx, LLMCall{ProjectKey: "PRJ", RepoSlug: "other", SourcePR: "PRJ/other#2", Outcome: "ok", CostNanoUSD: i64(12_000_000)}); err != nil {
		t.Fatal(err)
	}
	for _, repo := range []string{"other", "gone"} {
		pr := map[string]int{"other": 2, "gone": 3}[repo]
		if err := s.MarkConsolidated(ctx, "PRJ", repo, pr); err != nil {
			t.Fatal(err)
		}
	}
	if one, err := s.FoldedNotes(ctx, before, 1); err != nil || len(one) != 1 {
		t.Fatalf("FoldedNotes limited to 1 = %+v, %v", one, err)
	}
	folded, err := s.FoldedNotes(ctx, before, 50)
	if err != nil || len(folded) != 2 {
		t.Fatalf("FoldedNotes = %+v, %v", folded, err)
	}
	byRepo := map[string]FoldedNote{}
	for _, f := range folded {
		byRepo[f.RepoSlug] = f
	}
	if f := byRepo["other"]; f.Outcome != "ok" || f.CostNanoUSD == nil || *f.CostNanoUSD != 12_000_000 {
		t.Errorf("other = %+v, want its last call: ok, 12 milli", f)
	}
	if f := byRepo["gone"]; f.Outcome != "" || f.CostNanoUSD != nil {
		t.Errorf("gone = %+v, want no call", f)
	}
	if later, err := s.FoldedNotes(ctx, time.Now().Add(time.Minute), 50); err != nil || len(later) != 0 {
		t.Errorf("FoldedNotes after now = %+v, %v", later, err)
	}
}

// Open runs in NextRun's order; finished ones since a time; a run's calls
// with their step; the spend of a window.
func TestRunsCallsAndSpend(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	before := time.Now().Add(-time.Minute)
	for _, slug := range []string{"a", "b", "c"} {
		sourceRepo(t, s, "PRJ", slug)
	}
	a, b, c := enqueue(t, s, "PRJ", "a"), enqueue(t, s, "PRJ", "b"), enqueue(t, s, "PRJ", "c")
	b.State = RunDescribing
	if ok, err := s.SaveRun(ctx, b); err != nil || !ok {
		t.Fatalf("SaveRun: ok=%v err=%v", ok, err)
	}
	finish(t, s, c, RunFailed)
	open, err := s.OpenRuns(ctx)
	if err != nil || len(open) != 2 || open[0].ID != b.ID || open[1].ID != a.ID {
		t.Fatalf("OpenRuns = %+v, %v; want b (started), then a", open, err)
	}
	if n, err := s.QueueCount(ctx, true); err != nil || n != 2 {
		t.Errorf("QueueCount = %d, %v; want the 2 open runs", n, err)
	}
	if n, err := s.QueueCount(ctx, false); err != nil || n != 0 {
		t.Errorf("QueueCount without runs = %d, %v", n, err)
	}
	if none, err := s.FinishedRuns(ctx, before, 0); err != nil || len(none) != 0 {
		t.Errorf("FinishedRuns limited to 0 = %+v, %v", none, err)
	}
	done, err := s.FinishedRuns(ctx, before, 50)
	if err != nil || len(done) != 1 || done[0].ID != c.ID || !done[0].Latest || done[0].FailedInRow != 1 {
		t.Fatalf("FinishedRuns = %+v, %v; want c, the latest, 1 failed in a row", done, err)
	}
	// A newer run of c's repository: c is no longer the latest.
	enqueue(t, s, "PRJ", "c")
	if done, err := s.FinishedRuns(ctx, before, 50); err != nil || len(done) != 1 || done[0].Latest {
		t.Errorf("superseded: %+v, %v", done, err)
	}

	step := 2
	for _, call := range []LLMCall{
		{Purpose: PurposeCatalogue, Outcome: "ok", CostNanoUSD: i64(1_000_000)},
		{Purpose: PurposeSubject, Step: &step, Outcome: "timed_out"},
		{Purpose: PurposeSubject, Step: &step, Outcome: "ok", CostNanoUSD: i64(2_000_000)},
	} {
		id := b.ID
		call.ProjectKey, call.RepoSlug, call.SourceKind, call.SourceCommit, call.BaselineRunID = "PRJ", "b", FromBaseline, "c0ffee", &id
		if _, err := s.InsertLLMCall(ctx, call); err != nil {
			t.Fatal(err)
		}
	}
	calls, err := s.RunCalls(ctx, b.ID)
	if err != nil || len(calls) != 3 {
		t.Fatalf("RunCalls = %+v, %v", calls, err)
	}
	if calls[0].Step != nil || calls[1].Step == nil || *calls[1].Step != 2 || calls[1].Outcome != "timed_out" {
		t.Errorf("calls = %+v", calls)
	}
	costs, err := s.RunCosts(ctx, []int64{a.ID, b.ID})
	if err != nil || len(costs) != 1 || costs[b.ID] != 3_000_000 {
		t.Errorf("RunCosts = %v, %v; want b alone at 3 milli", costs, err)
	}
	if costs, err := s.RunCosts(ctx, nil); err != nil || len(costs) != 0 {
		t.Errorf("RunCosts of none = %v, %v", costs, err)
	}
	cost, n, err := s.Spent(ctx, before)
	if err != nil || n != 3 || cost == nil || *cost != 3_000_000 {
		t.Errorf("Spent = %v %d %v", cost, n, err)
	}
	if cost, n, err := s.Spent(ctx, time.Now().Add(time.Minute)); err != nil || n != 0 || cost != nil {
		t.Errorf("Spent after now = %v %d %v", cost, n, err)
	}
}
