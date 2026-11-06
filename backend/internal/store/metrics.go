package store

import (
	"context"
	"time"
)

// Metrics is what /metrics reads of the store besides the ledger: the
// queue, the last success of a merge and of a sync, the projects' current
// keys. From the rows, so a restart keeps it.
type Metrics struct {
	// Notes is the notes not yet folded, Runs the open baseline runs.
	Notes, Runs int
	// LastMerge is the newest merge call that answered ok: the call, not
	// whether its patch then applied. Nil before the first.
	LastMerge *time.Time
	// LastSync is the newest successful sync of any source repository.
	LastSync *time.Time
	// Projects is every project's current key by its id: the counters are
	// labelled by id, which a rename never changes.
	Projects map[int64]string
}

// Metrics reads the store's side of /metrics but the ledger.
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
	rows, err := s.db.QueryContext(ctx, `SELECT id, key FROM projects`)
	if err != nil {
		return Metrics{}, err
	}
	defer func() { _ = rows.Close() }()
	m.Projects = map[int64]string{}
	for rows.Next() {
		var id int64
		var key string
		if err := rows.Scan(&id, &key); err != nil {
			return Metrics{}, err
		}
		m.Projects[id] = key
	}
	return m, rows.Err()
}

// LedgerRow is one purpose's calls for one project, and what the priced
// ones cost.
type LedgerRow struct {
	Purpose   string
	ProjectID int64
	Calls     int64
	NanoUSD   int64
}

// Ledger is Almanach's own calls past the row afterID, by purpose and
// project, and the newest row's id (afterID when there is none): /metrics
// adds them to its totals, so a scrape reads only what came since the last
// one, never the whole ledger. llm_calls is append-only and its ids grow
// (rowid, nothing deleted), so no row is read twice or skipped.
// note_calls, noergler's ledger, is never in it.
func (s *Store) Ledger(ctx context.Context, afterID int64) ([]LedgerRow, int64, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.purpose, r.project_id, COUNT(*), COALESCE(SUM(c.cost_nano_usd), 0), MAX(c.id)
		  FROM llm_calls c JOIN repos r ON r.id = c.repo_id
		 WHERE c.id > ?
		 GROUP BY c.purpose, r.project_id`, afterID)
	if err != nil {
		return nil, afterID, err
	}
	defer func() { _ = rows.Close() }()
	out, last := []LedgerRow{}, afterID
	for rows.Next() {
		var r LedgerRow
		var maxID int64
		if err := rows.Scan(&r.Purpose, &r.ProjectID, &r.Calls, &r.NanoUSD, &maxID); err != nil {
			return nil, afterID, err
		}
		last = max(last, maxID)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, afterID, err
	}
	return out, last, nil
}
