package store

import (
	"context"
	"slices"
	"strconv"
	"testing"
	"time"
)

// A note is due once received at or before the cutoff the worker passes
// (now less the fold delay); one received after waits, the queue still
// holding it.
func TestNextQueued_DueBefore(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	ingest(t, s, noteFor("PRJ", "svc", 1))
	if _, err := s.db.ExecContext(ctx, `UPDATE notes SET received_at = ?`, stamp(mergedBase)); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.NextQueued(ctx, mergedBase.Add(-time.Second)); err != nil || ok {
		t.Fatalf("before its arrival plus the delay: ok=%v err=%v, want none due", ok, err)
	}
	if q, ok, err := s.NextQueued(ctx, mergedBase); err != nil || !ok || q.PRID != 1 {
		t.Fatalf("at the cutoff: ok=%v pr=%d err=%v, want pr 1", ok, q.PRID, err)
	}
}

// A repository's batch is every note on its queue in merge order, whatever
// order they arrived in; a consolidated, a failed or another repository's
// note is not in it.
func TestQueuedBatch_MergeOrder(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	for _, pr := range []int{3, 1, 2, 4, 5} {
		ingest(t, s, noteFor("PRJ", "svc", pr))
	}
	ingest(t, s, noteFor("PRJ", "other", 9))
	if err := s.MarkConsolidated(ctx, "PRJ", "svc", 4, "d-PRJ-svc-4"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkFoldFailed(ctx, "PRJ", "svc", 5, "d-PRJ-svc-5", "too_long", true); err != nil {
		t.Fatal(err)
	}
	batch, err := s.QueuedBatch(ctx, "PRJ", "svc")
	if err != nil {
		t.Fatal(err)
	}
	var got []int
	for _, q := range batch {
		got = append(got, q.PRID)
		if q.DeliveryID == "" || len(q.Body) == 0 || q.ProjectKey != "PRJ" || q.RepoSlug != "svc" {
			t.Errorf("note %d read as %+v", q.PRID, q)
		}
	}
	if !slices.Equal(got, []int{1, 2, 3}) {
		t.Errorf("batch = %v, want 1 2 3: merge order, queued notes of svc alone", got)
	}
	if batch, err := s.QueuedBatch(ctx, "PRJ", "nope"); err != nil || len(batch) != 0 {
		t.Errorf("unknown repository: %v, %v", batch, err)
	}
}

// A fold of several notes keeps every pull request on its call row and its
// revision; each of its notes on the queue page shows that call, its cost
// the whole call's and the batch's size beside it. A fold of one stores no
// list.
func TestFoldBatch_KeysOnCallAndRevision(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	for _, pr := range []int{1, 2, 3} {
		ingest(t, s, noteFor("PRJ", "svc", pr))
	}
	keys := []string{"PRJ/svc#1", "PRJ/svc#2"}
	nano := int64(4_000_000)
	id, err := s.InsertLLMCall(ctx, LLMCall{ProjectKey: "PRJ", RepoSlug: "svc", SourcePR: keys[0], Outcome: "ok", Model: "m",
		CostNanoUSD: &nano, BatchPRKeys: keys})
	if err != nil {
		t.Fatal(err)
	}
	single, err := s.InsertLLMCall(ctx, LLMCall{ProjectKey: "PRJ", RepoSlug: "svc", SourcePR: "PRJ/svc#3", Outcome: "ok", Model: "m",
		CostNanoUSD: &nano, BatchPRKeys: []string{"PRJ/svc#3"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertRevision(ctx, NewRevision{Scope: ScopeRepository, ProjectKey: "PRJ", RepoSlug: "svc", Summary: "S.",
		SourcePR: keys[0], LLMCallID: &id, BatchPRKeys: keys}); err != nil {
		t.Fatal(err)
	}
	page, ok, err := s.CurrentPage(ctx, ScopeRepository, "PRJ", "svc")
	if err != nil || !ok || page.SourcePR != keys[0] || !slices.Equal(page.BatchPRKeys, keys) {
		t.Fatalf("page = %+v, %v, %v", page, ok, err)
	}
	if _, err := s.InsertRevision(ctx, NewRevision{Scope: ScopeRepository, ProjectKey: "PRJ", RepoSlug: "svc", Summary: "T.",
		SourcePR: "PRJ/svc#3", LLMCallID: &single}); err != nil {
		t.Fatal(err)
	}
	rev, ok, err := s.Revision(ctx, ScopeRepository, "PRJ", "svc", 1)
	if err != nil || !ok || !slices.Equal(rev.BatchPRKeys, keys) {
		t.Fatalf("revision 1 = %+v, %v, %v", rev, ok, err)
	}
	rows, err := s.Revisions(ctx, ScopeRepository, "PRJ", "svc", 10)
	if err != nil || len(rows) != 2 || rows[0].BatchPRKeys != nil || !slices.Equal(rows[1].BatchPRKeys, keys) {
		t.Fatalf("revisions = %+v, %v", rows, err)
	}
	for _, pr := range []int{1, 2, 3} {
		if err := s.MarkConsolidated(ctx, "PRJ", "svc", pr, deliveryFor(noteFor("PRJ", "svc", pr)).ID); err != nil {
			t.Fatal(err)
		}
	}
	if got := count(t, s, "llm_calls", "batch_pr_keys IS NULL AND id = "+strconv.FormatInt(single, 10)); got != 1 {
		t.Errorf("a fold of one stored a list")
	}
	folded, err := s.FoldedNotes(ctx, mergedBase.Add(-time.Hour), 10)
	if err != nil || len(folded) != 3 {
		t.Fatalf("FoldedNotes = %+v, %v", folded, err)
	}
	for _, n := range folded {
		want := 2
		if n.PRKey == "PRJ/svc#3" {
			want = 1
		}
		if n.Outcome != "ok" || n.CostNanoUSD == nil || *n.CostNanoUSD != nano || n.Batch != want {
			t.Errorf("%s: outcome %q cost %v batch %d, want ok, the call's cost, batch %d", n.PRKey, n.Outcome, n.CostNanoUSD, n.Batch, want)
		}
	}
}
