package store

import (
	"context"
	"reflect"
	"testing"
)

// The ledger by purpose and project, an unpriced call counted at no cost;
// the last merge is the newest one that answered ok.
func TestMetrics(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	m, err := s.Metrics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if m.Notes != 0 || m.Runs != 0 || m.LastMerge != nil || m.LastSync != nil || len(m.Calls) != 0 {
		t.Fatalf("empty store: %+v", m)
	}

	nano := func(n int64) *int64 { return &n }
	run := int64(1)
	for _, c := range []LLMCall{
		{ProjectKey: "PRJ", RepoSlug: "svc", SourcePR: "PRJ/svc#1", Outcome: "error", CostNanoUSD: nano(100)},
		{ProjectKey: "PRJ", RepoSlug: "svc", SourcePR: "PRJ/svc#2", Outcome: "ok", CostNanoUSD: nano(250)},
		{ProjectKey: "PRJ", RepoSlug: "web", SourcePR: "PRJ/web#1", Outcome: "ok"},
		{ProjectKey: "OPS", RepoSlug: "infra", SourceKind: FromBaseline, SourceCommit: "c0ffee", Purpose: PurposeCatalogue,
			BaselineRunID: &run, Outcome: "ok", CostNanoUSD: nano(7)},
	} {
		if _, err := s.InsertLLMCall(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	m, err = s.Metrics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []CallTotal{
		{Purpose: PurposeCatalogue, ProjectKey: "OPS", Calls: 1, NanoUSD: 7},
		{Purpose: PurposeMerge, ProjectKey: "PRJ", Calls: 3, NanoUSD: 350},
	}
	if !reflect.DeepEqual(m.Calls, want) {
		t.Errorf("Calls = %+v, want %+v", m.Calls, want)
	}
	if m.LastMerge == nil || m.LastMerge.IsZero() {
		t.Errorf("LastMerge = %v, want the ok merge's time", m.LastMerge)
	}
}
