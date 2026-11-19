package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
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
	st.purposes = []store.PurposeCost{
		{Month: "2026-11", Purpose: store.PurposeSubject, CostSum: store.CostSum{Calls: 10, Unpriced: 2, NanoUSD: ptr[int64](1_200_000_000)},
			InputTokens: 900_000, CachedTokens: 300_000, OutputTokens: 200_000, ReasoningTokens: ptr[int64](180_000), ReasonedOutputTokens: 190_000},
		{Month: "2026-11", Purpose: store.PurposeMerge, CostSum: store.CostSum{Calls: 4, Unpriced: 1, NanoUSD: ptr[int64](30_000_000)},
			InputTokens: 40_000, OutputTokens: 6_000},
		{Month: "2026-10", Purpose: store.PurposeRestyle, CostSum: store.CostSum{Calls: 4, NanoUSD: ptr[int64](500_000_000)}},
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
	// The same months by purpose, in the store's order, tokens beside.
	if ps := nov.Purposes; len(ps) != 2 || ps[0].Purpose != "subject" || ps[0].CostUSD != "1.200" || ps[0].Unpriced != 2 ||
		ps[0].CachedTokens != 300_000 || ps[0].ReasoningTokens == nil || *ps[0].ReasoningTokens != 180_000 || ps[0].ReasonedOutputTokens != 190_000 ||
		ps[1].Purpose != "merge" || ps[1].ReasoningTokens != nil {
		t.Errorf("november purposes = %+v", ps)
	}
	if ps := out.Months[1].Purposes; len(ps) != 1 || ps[0].Purpose != "restyle" || ps[0].Calls != 4 {
		t.Errorf("october purposes = %+v", ps)
	}
	if !strings.Contains(rec.Body.String(), `"purpose":"merge","calls":4,"unpriced":1,"cost_usd":"0.030","input_tokens":40000`) {
		t.Errorf("purpose row not flat on the wire: %s", rec.Body.String())
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
