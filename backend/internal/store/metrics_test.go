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
	if m.Notes != 0 || m.Runs != 0 || m.LastSync != nil || len(m.Projects) != 0 {
		t.Fatalf("empty store: %+v", m)
	}
	if p, err := s.Ledger(ctx, 0); err != nil || len(p.Rows) != 0 || p.LastID != 0 || p.LastMerge != nil {
		t.Fatalf("empty ledger: %+v %v", p, err)
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
	ids := map[string]int64{}
	for id, key := range m.Projects {
		ids[key] = id
	}
	if len(ids) != 2 || ids["PRJ"] == 0 || ids["OPS"] == 0 {
		t.Fatalf("Projects = %v", m.Projects)
	}

	p, err := s.Ledger(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(p.Rows, func(i, j int) bool { return p.Rows[i].Purpose < p.Rows[j].Purpose })
	want := []LedgerRow{
		{Purpose: PurposeCatalogue, ProjectID: ids["OPS"], Calls: 1, NanoUSD: 7},
		{Purpose: PurposeMerge, ProjectID: ids["PRJ"], Calls: 3, NanoUSD: 350},
	}
	if !reflect.DeepEqual(p.Rows, want) || p.LastID != mid || p.LastMerge == nil {
		t.Errorf("Ledger = %+v, want %+v up to %d with a last merge", p, want, mid)
	}

	// Past the last row: only what came since. The catalogue call is no
	// merge, the failed merge no ok one.
	insert(LLMCall{ProjectKey: "OPS", RepoSlug: "infra", SourceKind: FromBaseline, SourceCommit: "c0ffee",
		Purpose: PurposeCatalogue, BaselineRunID: &run, Outcome: "ok", CostNanoUSD: nano(1)})
	newest := insert(LLMCall{ProjectKey: "PRJ", RepoSlug: "svc", SourcePR: "PRJ/svc#3", Outcome: "error"})
	p, err = s.Ledger(ctx, mid)
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(p.Rows, func(i, j int) bool { return p.Rows[i].Purpose < p.Rows[j].Purpose })
	want = []LedgerRow{
		{Purpose: PurposeCatalogue, ProjectID: ids["OPS"], Calls: 1, NanoUSD: 1},
		{Purpose: PurposeMerge, ProjectID: ids["PRJ"], Calls: 1, NanoUSD: 0},
	}
	if !reflect.DeepEqual(p.Rows, want) || p.LastID != newest || p.LastMerge != nil {
		t.Errorf("Ledger past %d = %+v, want %+v up to %d, no ok merge", mid, p, want, newest)
	}
	if p, err := s.Ledger(ctx, newest); err != nil || len(p.Rows) != 0 || p.LastID != newest {
		t.Errorf("Ledger past the newest = %+v, %v", p, err)
	}
}
