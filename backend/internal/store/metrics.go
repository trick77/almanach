package store

import (
	"context"
	"time"
)

// Metrics is what /metrics reads of the store: the queue, the last
// success of a merge and of a sync, and the ledger of Almanach's own
// calls. From the rows, so a restart keeps it; note_calls (noergler's
// ledger) is never in it.
type Metrics struct {
	// Notes is the notes not yet folded, Runs the open baseline runs.
	Notes, Runs int
	// LastMerge is the newest merge call that answered ok: the call, not
	// whether its patch then applied. Nil before the first.
	LastMerge *time.Time
	// LastSync is the newest successful sync of any source repository.
	LastSync *time.Time
	// Calls is the whole ledger by purpose and project.
	Calls []CallTotal
}

// CallTotal is one purpose's calls for one project, and what the priced
// ones cost.
type CallTotal struct {
	Purpose    string
	ProjectKey string
	Calls      int64
	NanoUSD    int64
}

// Metrics reads the store's side of /metrics. The ledger is summed whole
// on every scrape: one GROUP BY over llm_calls, small next to the pages.
func (s *Store) Metrics(ctx context.Context) (Metrics, error) {
	var m Metrics
	err := s.db.QueryRowContext(ctx, `
		SELECT (SELECT COUNT(*) FROM notes WHERE consolidated_at IS NULL),
		       (SELECT COUNT(*) FROM baseline_runs WHERE state NOT IN ('done', 'failed')),
		       (SELECT MAX(created_at) FROM llm_calls WHERE purpose = 'merge' AND outcome = 'ok'),
		       (SELECT MAX(last_synced_at) FROM source_repos)`).
		Scan(&m.Notes, &m.Runs, timeOrNil(&m.LastMerge), timeOrNil(&m.LastSync))
	if err != nil {
		return Metrics{}, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.purpose, p.key, COUNT(*), COALESCE(SUM(c.cost_nano_usd), 0)
		  FROM llm_calls c JOIN repos r ON r.id = c.repo_id JOIN projects p ON p.id = r.project_id
		 GROUP BY c.purpose, p.id
		 ORDER BY c.purpose, p.key`)
	if err != nil {
		return Metrics{}, err
	}
	defer func() { _ = rows.Close() }()
	m.Calls = []CallTotal{}
	for rows.Next() {
		var c CallTotal
		if err := rows.Scan(&c.Purpose, &c.ProjectKey, &c.Calls, &c.NanoUSD); err != nil {
			return Metrics{}, err
		}
		m.Calls = append(m.Calls, c)
	}
	return m, rows.Err()
}
