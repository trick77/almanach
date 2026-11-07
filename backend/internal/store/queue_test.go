package store

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// The ledger by month and project, merges apart from runs, an unpriced
// call counted but costing nothing; and by project alone.
func TestCostsByMonthAndProject(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	ingest(t, s, noteFor("PRJ", "svc", 1))
	ingest(t, s, noteFor("ZED", "web", 2))
	for _, c := range []LLMCall{
		{ProjectKey: "PRJ", RepoSlug: "svc", SourcePR: "PRJ/svc#1", Outcome: "ok", CostNanoUSD: i64(30_000_000)},
		{ProjectKey: "PRJ", RepoSlug: "svc", SourceKind: FromBaseline, SourceCommit: "c0ffee", Purpose: PurposeSubject, Outcome: "ok", CostNanoUSD: i64(1_000_000_000)},
		{ProjectKey: "PRJ", RepoSlug: "svc", SourceKind: FromBaseline, SourceCommit: "c0ffee", Purpose: PurposeCatalogue, Outcome: "ok"},
		{ProjectKey: "PRJ", RepoSlug: "svc", SourceKind: FromPage, Purpose: PurposeBrief, Outcome: "ok", CostNanoUSD: i64(7_000_000)},
		{ProjectKey: "ZED", RepoSlug: "web", SourcePR: "ZED/web#2", Outcome: "error"},
	} {
		if _, err := s.InsertLLMCall(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.Costs(ctx, time.Now())
	if err != nil || len(rows) != 2 {
		t.Fatalf("Costs = %+v, %v", rows, err)
	}
	// The month the rows were written in, off the rows: a test straddling
	// midnight on the last of a month must not fail.
	var month string
	if err := s.db.QueryRowContext(ctx, `SELECT substr(created_at, 1, 7) FROM llm_calls LIMIT 1`).Scan(&month); err != nil {
		t.Fatal(err)
	}
	prj, zed := rows[0], rows[1]
	if prj.Month != month || prj.ProjectKey != "PRJ" || prj.Merges.Calls != 1 || prj.Merges.NanoUSD == nil || *prj.Merges.NanoUSD != 30_000_000 ||
		prj.Runs.Calls != 2 || prj.Runs.Unpriced != 1 || prj.Runs.NanoUSD == nil || *prj.Runs.NanoUSD != 1_000_000_000 ||
		prj.Briefs.Calls != 1 || prj.Briefs.NanoUSD == nil || *prj.Briefs.NanoUSD != 7_000_000 {
		t.Errorf("PRJ = %+v", prj)
	}
	if zed.ProjectKey != "ZED" || zed.Merges.Calls != 1 || zed.Merges.Unpriced != 1 || zed.Merges.NanoUSD != nil || zed.Runs.Calls != 0 {
		t.Errorf("ZED = %+v", zed)
	}
	// Only the last CostsMonths: a clock far ahead sees none of the rows.
	if later, err := s.Costs(ctx, time.Now().AddDate(0, CostsMonths+1, 0)); err != nil || len(later) != 0 {
		t.Errorf("Costs a year on = %+v, %v; want none", later, err)
	}
	// Money as the API prints it, on the integer; the UTC day's start.
	for nano, want := range map[int64]string{1_030_000_000: "1.030", 1_234_567_500: "1.235", -5_000: "-0.000", 999_500_000: "1.000"} {
		if got := USD(nano); got != want {
			t.Errorf("USD(%d) = %q, want %q", nano, got, want)
		}
	}
	at := time.Date(2026, 11, 5, 23, 30, 0, 0, time.FixedZone("CET", 3600))
	if got := DayStart(at); !got.Equal(time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("DayStart(%v) = %v", at, got)
	}
}

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
	if _, err := s.DeferFold(ctx, "PRJ", "other", 2, "d-PRJ-other-2"); err != nil {
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
		if err := s.MarkConsolidated(ctx, "PRJ", repo, pr, fmt.Sprintf("d-PRJ-%s-%d", repo, pr)); err != nil {
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
	// The timed-out call got no answer: no price, and nothing billed.
	sp, err := s.Spent(ctx, before)
	if err != nil || sp.Calls != 3 || sp.NanoUSD == nil || *sp.NanoUSD != 3_000_000 || sp.Unpriced != 0 || sp.Dearest != 2_000_000 {
		t.Errorf("Spent = %+v %v", sp, err)
	}
	if b := sp.Budgeted(); b == nil || *b != 3_000_000 {
		t.Errorf("Budgeted = %v, want the priced sum", b)
	}
	if sp, err := s.Spent(ctx, time.Now().Add(time.Minute)); err != nil || sp.Calls != 0 || sp.NanoUSD != nil || sp.Budgeted() != nil {
		t.Errorf("Spent after now = %+v %v", sp, err)
	}
	// A loop cut: answered in part, billed, no price came back. The budget
	// counts it as the dearest priced call.
	cut := LLMCall{Purpose: PurposeSubject, Step: &step, Outcome: "too_long", ProjectKey: "PRJ", RepoSlug: "b",
		SourceKind: FromBaseline, SourceCommit: "c0ffee"}
	if _, err := s.InsertLLMCall(ctx, cut); err != nil {
		t.Fatal(err)
	}
	sp, err = s.Spent(ctx, before)
	if err != nil || sp.Calls != 4 || sp.Unpriced != 1 {
		t.Errorf("Spent with a cut = %+v %v, want it unpriced", sp, err)
	}
	if b := sp.Budgeted(); b == nil || *b != 5_000_000 {
		t.Errorf("Budgeted = %v, want 3 milli + the dearest 2", b)
	}
	// A stall mid-answer was billed too: streamed, whatever its outcome.
	stall := cut
	stall.Outcome, stall.Streamed = "timed_out", true
	if _, err := s.InsertLLMCall(ctx, stall); err != nil {
		t.Fatal(err)
	}
	if sp, err := s.Spent(ctx, before); err != nil || sp.Unpriced != 2 {
		t.Errorf("Spent with a stall = %+v %v, want two unpriced", sp, err)
	}
	// A day of unpriced calls alone still has the dearest of the month.
	sp, err = s.Spent(ctx, time.Now().Add(time.Minute))
	if err != nil || sp.Calls != 0 || sp.Dearest != 2_000_000 {
		t.Errorf("Spent of a later day = %+v %v, want the month's dearest", sp, err)
	}
}

func TestSpend_Budgeted(t *testing.T) {
	for _, c := range []struct {
		sp   Spend
		want *int64
	}{
		{Spend{}, nil},
		{Spend{Unpriced: 2}, nil},
		{Spend{Unpriced: 2, Dearest: 5}, i64(10)},
		{Spend{NanoUSD: i64(7)}, i64(7)},
	} {
		got := c.sp.Budgeted()
		if (got == nil) != (c.want == nil) || got != nil && *got != *c.want {
			t.Errorf("%+v.Budgeted() = %v, want %v", c.sp, got, c.want)
		}
	}
}
