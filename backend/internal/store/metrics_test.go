package store

import (
	"context"
	"reflect"
	"sort"
	"testing"
)

// The queue, the last ok merge, the projects by id; the ledger by purpose
// and project id past a row, an unpriced call counted at no cost.
func TestMetricsAndLedger(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	m, err := s.Metrics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if m.Notes != 0 || m.Runs != 0 || m.LastMerge != nil || m.LastSync != nil || len(m.Projects) != 0 {
		t.Fatalf("empty store: %+v", m)
	}
	if rows, last, err := s.Ledger(ctx, 0); err != nil || len(rows) != 0 || last != 0 {
		t.Fatalf("empty ledger: %v %d %v", rows, last, err)
	}

	nano := func(n int64) *int64 { return &n }
	run := int64(1)
	insert := func(c LLMCall) int64 {
		t.Helper()
		id, err := s.InsertLLMCall(ctx, c)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	insert(LLMCall{ProjectKey: "PRJ", RepoSlug: "svc", SourcePR: "PRJ/svc#1", Outcome: "error", CostNanoUSD: nano(100)})
	insert(LLMCall{ProjectKey: "PRJ", RepoSlug: "svc", SourcePR: "PRJ/svc#2", Outcome: "ok", CostNanoUSD: nano(250)})
	insert(LLMCall{ProjectKey: "PRJ", RepoSlug: "web", SourcePR: "PRJ/web#1", Outcome: "ok"})
	mid := insert(LLMCall{ProjectKey: "OPS", RepoSlug: "infra", SourceKind: FromBaseline, SourceCommit: "c0ffee", Purpose: PurposeCatalogue,
		BaselineRunID: &run, Outcome: "ok", CostNanoUSD: nano(7)})

	m, err = s.Metrics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if m.LastMerge == nil || m.LastMerge.IsZero() {
		t.Errorf("LastMerge = %v, want the ok merge's time", m.LastMerge)
	}
	ids := map[string]int64{}
	for id, key := range m.Projects {
		ids[key] = id
	}
	if len(ids) != 2 || ids["PRJ"] == 0 || ids["OPS"] == 0 {
		t.Fatalf("Projects = %v", m.Projects)
	}

	rows, last, err := s.Ledger(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Purpose < rows[j].Purpose })
	want := []LedgerRow{
		{Purpose: PurposeCatalogue, ProjectID: ids["OPS"], Calls: 1, NanoUSD: 7},
		{Purpose: PurposeMerge, ProjectID: ids["PRJ"], Calls: 3, NanoUSD: 350},
	}
	if !reflect.DeepEqual(rows, want) || last != mid {
		t.Errorf("Ledger = %+v, %d, want %+v, %d", rows, last, want, mid)
	}

	// Past the last row: only what came since.
	newest := insert(LLMCall{ProjectKey: "PRJ", RepoSlug: "svc", SourcePR: "PRJ/svc#3", Outcome: "ok", CostNanoUSD: nano(1)})
	rows, last, err = s.Ledger(ctx, mid)
	if err != nil {
		t.Fatal(err)
	}
	if want := []LedgerRow{{Purpose: PurposeMerge, ProjectID: ids["PRJ"], Calls: 1, NanoUSD: 1}}; !reflect.DeepEqual(rows, want) || last != newest {
		t.Errorf("Ledger past %d = %+v, %d", mid, rows, last)
	}
	if rows, last, err := s.Ledger(ctx, newest); err != nil || len(rows) != 0 || last != newest {
		t.Errorf("Ledger past the newest = %v, %d, %v", rows, last, err)
	}
}
