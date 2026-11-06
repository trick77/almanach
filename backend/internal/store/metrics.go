package store

import (
	"context"
	"time"
)

// Metrics is what /metrics reads of the store besides the ledger: the
// queue, the last successful sync, the projects' current keys. From the
// rows, so a restart keeps it.
type Metrics struct {
	// Notes is the notes not yet folded, Runs the open baseline runs: the
	// nav's count (QueueCount), split.
	Notes, Runs int
	// LastSync is the newest successful sync of any source repository.
	LastSync *time.Time
	// Projects is every project's current key by its id: the counters are
	// labelled by id, which a rename never changes.
	Projects map[int64]string
}

// Metrics reads the store's side of /metrics but the ledger.
func (s *Store) Metrics(ctx context.Context) (Metrics, error) {
	var m Metrics
	notes, err := s.QueueCount(ctx, false)
	if err != nil {
		return Metrics{}, err
	}
	all, err := s.QueueCount(ctx, true)
	if err != nil {
		return Metrics{}, err
	}
	m.Notes, m.Runs = notes, all-notes
	if err := s.db.QueryRowContext(ctx, `SELECT MAX(last_synced_at) FROM source_repos`).Scan(timeOrNil(&m.LastSync)); err != nil {
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

// LedgerPart is the ledger's rows past one id: summed by purpose and
// project, the newest id among them, and the newest merge call among them
// that answered ok (the call, not whether its patch then applied).
type LedgerPart struct {
	Rows []LedgerRow
	// LastID is the newest row's id, the asked one when there is none.
	LastID int64
	// LastMerge is nil when none of the rows is an ok merge.
	LastMerge *time.Time
}

// Ledger is Almanach's own calls past the row afterID: /metrics adds them
// to its totals, so a scrape reads only what came since the last one, by
// rowid range (the primary key), never the whole ledger. llm_calls is append-only and its
// ids grow (rowid, nothing deleted), so no row is read twice or skipped.
// note_calls, noergler's ledger, is never in it.
func (s *Store) Ledger(ctx context.Context, afterID int64) (LedgerPart, error) {
	part := LedgerPart{Rows: []LedgerRow{}, LastID: afterID}
	// One statement, so the sums, the last id and the last merge are of
	// one snapshot: a row landing between two reads would be counted and
	// its merge time lost.
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.purpose, r.project_id, COUNT(*), COALESCE(SUM(c.cost_nano_usd), 0), MAX(c.id),
		       MAX(CASE WHEN c.purpose = 'merge' AND c.outcome = 'ok' THEN c.created_at END)
		  FROM llm_calls c JOIN repos r ON r.id = c.repo_id
		 WHERE c.id > ?
		 GROUP BY c.purpose, r.project_id`, afterID)
	if err != nil {
		return LedgerPart{}, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var r LedgerRow
		var maxID int64
		var merge *time.Time
		if err := rows.Scan(&r.Purpose, &r.ProjectID, &r.Calls, &r.NanoUSD, &maxID, timeOrNil(&merge)); err != nil {
			return LedgerPart{}, err
		}
		part.LastID = max(part.LastID, maxID)
		if merge != nil && (part.LastMerge == nil || merge.After(*part.LastMerge)) {
			part.LastMerge = merge
		}
		part.Rows = append(part.Rows, r)
	}
	if err := rows.Err(); err != nil {
		return LedgerPart{}, err
	}
	return part, nil
}
