package api

import (
	"net/http"
	"time"

	"github.com/trick77/almanach/internal/store"
	"github.com/trick77/ship/kit/httpx"
)

// The Kosten page: what Almanach's own model calls cost, by month and
// project, the merges (a note's fold) apart from the runs. Almanach's
// ledger alone (llm_calls), never noergler's figures (note_calls). Read
// only.

type costsBody struct {
	// Months is newest first; a month with no call is absent.
	Months []costMonthBody `json:"months"`
	// Budget is the day's ceiling (BACKEND_DAILY_BUDGET_USD), omitted
	// when none; Today what the calls of the UTC day cost so far.
	BudgetUSD string    `json:"budget_usd,omitempty"`
	Today     spentBody `json:"today"`
}

type costMonthBody struct {
	// Month is YYYY-MM, UTC.
	Month    string            `json:"month"`
	CostUSD  string            `json:"cost_usd,omitempty"`
	Calls    int               `json:"calls"`
	Projects []costProjectBody `json:"projects"`
}

type costProjectBody struct {
	Project string      `json:"project"`
	CostUSD string      `json:"cost_usd,omitempty"`
	Calls   int         `json:"calls"`
	Merges  costSumBody `json:"merges"`
	Runs    costSumBody `json:"runs"`
}

type costSumBody struct {
	Calls int `json:"calls"`
	// Unpriced is the calls no one priced; their cost is unknown, not 0.
	Unpriced int    `json:"unpriced"`
	CostUSD  string `json:"cost_usd,omitempty"`
}

func (d Deps) costs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	d.reader(r)
	rows, err := d.Store.Costs(ctx, d.now())
	if err != nil {
		httpx.ServerError(w, r, err, "costs")
		return
	}
	out := costsBody{Months: []costMonthBody{}}
	var monthTotal *int64
	for _, row := range rows {
		if n := len(out.Months); n == 0 || out.Months[n-1].Month != row.Month {
			if n > 0 {
				out.Months[n-1].CostUSD = usd(monthTotal)
			}
			monthTotal = nil
			out.Months = append(out.Months, costMonthBody{Month: row.Month, Projects: []costProjectBody{}})
		}
		m := &out.Months[len(out.Months)-1]
		total := addNano(row.Merges.NanoUSD, row.Runs.NanoUSD)
		p := costProjectBody{Project: row.ProjectKey, Merges: sumBody(row.Merges), Runs: sumBody(row.Runs),
			Calls: row.Merges.Calls + row.Runs.Calls, CostUSD: usd(total)}
		m.Projects = append(m.Projects, p)
		m.Calls += p.Calls
		monthTotal = addNano(monthTotal, total)
	}
	if n := len(out.Months); n > 0 {
		out.Months[n-1].CostUSD = usd(monthTotal)
	}
	cost, calls, err := d.Store.Spent(ctx, store.DayStart(d.now()))
	if err != nil {
		httpx.ServerError(w, r, err, "costs today")
		return
	}
	out.Today = spentBody{CostUSD: usd(cost), Calls: calls}
	if b := d.DailyBudgetNanoUSD; b > 0 {
		out.BudgetUSD = usd(&b)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// now is the clock the costs page counts the day by: the queue's, which a
// test sets, else the wall's.
func (d Deps) now() time.Time {
	if d.Queue != nil {
		return d.Queue.now()
	}
	return time.Now()
}

func sumBody(s store.CostSum) costSumBody {
	return costSumBody{Calls: s.Calls, Unpriced: s.Unpriced, CostUSD: usd(s.NanoUSD)}
}

// addNano sums two optional amounts: nil when neither is priced.
func addNano(a, b *int64) *int64 {
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	}
	n := *a + *b
	return &n
}
