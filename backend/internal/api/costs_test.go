package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/trick77/almanach/internal/config"
	"github.com/trick77/almanach/internal/httpapi"
	"github.com/trick77/almanach/internal/store"
)

// The costs page: months newest first, each project's merges apart from
// its runs with their calls and unpriced count, the month summed; the
// day's spend and the budget beside.
func TestCosts(t *testing.T) {
	st := newFakeStore()
	st.costs = []store.CostRow{
		{Month: "2026-11", ProjectKey: "PLAT", Merges: store.CostSum{Calls: 3, NanoUSD: ptr[int64](30_000_000)}, Runs: store.CostSum{Calls: 10, Unpriced: 2, NanoUSD: ptr[int64](1_200_000_000)}},
		{Month: "2026-11", ProjectKey: "ZED", Merges: store.CostSum{Calls: 1, Unpriced: 1}},
		{Month: "2026-10", ProjectKey: "PLAT", Runs: store.CostSum{Calls: 4, NanoUSD: ptr[int64](500_000_000)}},
	}
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httpapi.New(func(context.Context) error { return nil }, discard)
	q := &QueueDeps{Store: &fakeQueueStore{}, Worker: fakeWorkerStatus{}}
	Register(srv, Deps{Store: st, Keys: keysFile(t), Worker: &fakeWaker{}, AuthMode: config.AuthNone, Queue: q,
		DailyBudgetNanoUSD: 5_000_000_000, Log: discard})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/costs", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/costs = %d %s", rec.Code, rec.Body.String())
	}
	var out costsBody
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Months) != 2 || out.Months[0].Month != "2026-11" || out.Months[1].Month != "2026-10" {
		t.Fatalf("months = %+v", out.Months)
	}
	nov := out.Months[0]
	if nov.CostUSD != "1.230" || nov.Calls != 14 || len(nov.Projects) != 2 {
		t.Errorf("november = %+v, want 1.230 over 14 calls of two projects", nov)
	}
	if p := nov.Projects[0]; p.Project != "PLAT" || p.CostUSD != "1.230" || p.Calls != 13 || p.Merges.CostUSD != "0.030" ||
		p.Runs.Calls != 10 || p.Runs.Unpriced != 2 || p.Runs.CostUSD != "1.200" {
		t.Errorf("PLAT = %+v", p)
	}
	if p := nov.Projects[1]; p.Project != "ZED" || p.CostUSD != "" || p.Merges.Unpriced != 1 || p.Calls != 1 {
		t.Errorf("ZED = %+v, want unpriced, no cost", p)
	}
	if out.Months[1].CostUSD != "0.500" || out.Months[1].Projects[0].Runs.Calls != 4 {
		t.Errorf("october = %+v", out.Months[1])
	}
	if out.BudgetUSD != "5.000" || out.Today.CostUSD != "1.284" || out.Today.Calls != 23 {
		t.Errorf("budget %q today %+v", out.BudgetUSD, out.Today)
	}

	// No budget, no ledger: an empty list, the budget omitted.
	st.costs = nil
	srv = httpapi.New(func(context.Context) error { return nil }, discard)
	Register(srv, Deps{Store: st, Keys: keysFile(t), Worker: &fakeWaker{}, AuthMode: config.AuthNone, Queue: q, Log: discard})
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/costs", nil))
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("GET /api/costs = %d %s: %v", rec.Code, rec.Body.String(), err)
	}
	if _, has := raw["budget_usd"]; string(raw["months"]) != "[]" || has {
		t.Errorf("empty = %s", rec.Body.String())
	}
}
