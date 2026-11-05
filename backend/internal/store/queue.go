package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"time"

	"github.com/trick77/almanach/internal/redact"
)

// The queue as the worker drains it: the notes it has not folded, the runs
// that are open, and what the worker spent and finished lately. The order
// is the worker's (NextQueued, NextRun); which of the two goes first is
// the worker's rule, the API's to lay out.

// PendingNote is a note the worker has not folded yet.
type PendingNote struct {
	ProjectKey string
	RepoSlug   string
	PRKey      string
	PRTitle    string
	ChangeKind string
	ReceivedAt time.Time
	// Attempts is the gateway failures counted against it so far.
	Attempts int
	// HeldBy is the started run of its repository it waits for, 0 when
	// none: NextQueued skips it until that run is over.
	HeldBy int64
}

// startedRunOfNote is the started run of note n's repository, the run its
// note waits for: NextQueued skips a note that has one, PendingNotes names
// it. One rule, so the page shows the hold the worker keeps.
const startedRunOfNote = `SELECT b.id FROM baseline_runs b WHERE b.repo_id = n.repo_id
	AND b.state NOT IN ('queued', 'done', 'failed')`

// PendingNotes is the unfolded notes, oldest first, as NextQueued takes
// them, at most limit.
func (s *Store) PendingNotes(ctx context.Context, limit int) ([]PendingNote, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT p.key, r.slug, n.pr_key, n.pr_title, COALESCE(n.change_kind, ''), n.received_at, n.fold_attempts,
		       COALESCE((`+startedRunOfNote+` LIMIT 1), 0)
		  FROM notes n JOIN repos r ON r.id = n.repo_id JOIN projects p ON p.id = r.project_id
		 WHERE n.consolidated_at IS NULL AND n.fold_failed_at IS NULL
		 ORDER BY n.received_at, n.pr_id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []PendingNote
	for rows.Next() {
		var n PendingNote
		if err := rows.Scan(&n.ProjectKey, &n.RepoSlug, &n.PRKey, &n.PRTitle, &n.ChangeKind, timeAt(&n.ReceivedAt),
			&n.Attempts, &n.HeldBy); err != nil {
			return nil, err
		}
		n.PRTitle, _, _ = noteOut(n.PRTitle, nil, nil)
		out = append(out, n)
	}
	return out, rows.Err()
}

// OpenRuns is every run neither done nor failed, in NextRun's order: a
// started one first, then the queued, oldest first.
func (s *Store) OpenRuns(ctx context.Context) ([]BaselineRun, error) {
	return s.runs(ctx, `SELECT `+runColumns+` FROM `+runsFrom+`
		WHERE b.state NOT IN ('done', 'failed')
		ORDER BY b.state = 'queued', b.queued_at, b.id`)
}

// FinishedRun is a run that ended, with what the poller's retry reads of
// its repository: whether it is the newest run of its kind, and how many
// runs failed since the newest done one (LastRun's count). A restyle is a
// kind of its own: the newest restyle, its failures its page's at its
// epoch as RestyleState counts them (one that failed on the project page,
// the project's over the project); a reading run's never count a restyle,
// as LastRun's.
type FinishedRun struct {
	BaselineRun
	Latest      bool
	FailedInRow int
}

// FinishedRuns is the runs that ended at or after since, newest first, at
// most limit, in one query.
func (s *Store) FinishedRuns(ctx context.Context, since time.Time, limit int) ([]FinishedRun, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+runColumns+`,
		       b.id = (SELECT MAX(x.id) FROM baseline_runs x WHERE x.repo_id = b.repo_id AND (x.mode = 'restyle') = (b.mode = 'restyle')),
		       CASE WHEN b.mode = 'restyle' AND b.step = ?3
		            THEN (SELECT COUNT(*) FROM baseline_runs f JOIN source_repos o ON o.repo_id = f.repo_id
		                   WHERE o.project_id = (SELECT project_id FROM source_repos WHERE repo_id = b.repo_id)
		                     AND f.mode = 'restyle' AND f.state = 'failed' AND f.epoch = b.epoch AND f.step = ?3)
		            WHEN b.mode = 'restyle'
		            THEN (SELECT COUNT(*) FROM baseline_runs f
		                   WHERE f.repo_id = b.repo_id AND f.mode = 'restyle' AND f.state = 'failed' AND f.epoch = b.epoch)
		            ELSE (SELECT COUNT(*) FROM baseline_runs f
		                   WHERE f.repo_id = b.repo_id AND f.state = 'failed' AND f.mode <> 'restyle'
		                     AND f.id > COALESCE((SELECT MAX(d.id) FROM baseline_runs d
		                                           WHERE d.repo_id = b.repo_id AND d.state = 'done' AND d.mode <> 'restyle'), 0))
		       END
		  FROM `+runsFrom+`
		 WHERE b.state IN ('done', 'failed') AND b.finished_at >= ?1
		 ORDER BY b.finished_at DESC, b.id DESC LIMIT ?2`, stamp(since), limit, RestyleProjectStep)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []FinishedRun
	for rows.Next() {
		var f FinishedRun
		if f.BaselineRun, err = scanRun(rows, &f.Latest, &f.FailedInRow); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// QueueCount is how many notes are unfolded (a failed one waits for a run,
// not the worker: not counted) and, with runs, how many runs are open: the
// queue's size, for the nav.
func (s *Store) QueueCount(ctx context.Context, runs bool) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT (SELECT COUNT(*) FROM notes WHERE consolidated_at IS NULL AND fold_failed_at IS NULL)
		     + CASE WHEN ? THEN (SELECT COUNT(*) FROM baseline_runs WHERE state NOT IN ('done', 'failed')) ELSE 0 END`,
		runs).Scan(&n)
	return n, err
}

func (s *Store) runs(ctx context.Context, query string, args ...any) ([]BaselineRun, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []BaselineRun
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RunCall is one model call of a run, for its steps on the queue page.
type RunCall struct {
	Purpose string
	// Step is the reading step, nil for the catalogue, project and map.
	Step        *int
	Outcome     string
	CostNanoUSD *int64
	At          time.Time
}

// RunCalls is a run's calls in the order they were made.
func (s *Store) RunCalls(ctx context.Context, runID int64) ([]RunCall, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT purpose, step, outcome, cost_nano_usd, created_at FROM llm_calls
		 WHERE baseline_run_id = ? ORDER BY id`, runID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []RunCall
	for rows.Next() {
		var c RunCall
		var step sql.NullInt64
		if err := rows.Scan(&c.Purpose, &step, &c.Outcome, &c.CostNanoUSD, timeAt(&c.At)); err != nil {
			return nil, err
		}
		if step.Valid {
			i := int(step.Int64)
			c.Step = &i
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// FoldedNote is a note the worker took off the queue: folded, refused or
// ignored. Outcome and cost are the last merge call of this delivery
// (made between its arrival and its fold); "" and nil when it made none
// (a note for a repository that is no source).
type FoldedNote struct {
	ProjectKey     string
	RepoSlug       string
	PRKey          string
	PRTitle        string
	ConsolidatedAt time.Time
	Outcome        string
	CostNanoUSD    *int64
}

// FoldedNotes is the notes taken off the queue at or after since, newest
// first, at most limit.
func (s *Store) FoldedNotes(ctx context.Context, since time.Time, limit int) ([]FoldedNote, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT p.key, r.slug, n.pr_key, n.pr_title, n.consolidated_at,
		       COALESCE(c.outcome, ''), c.cost_nano_usd
		  FROM notes n JOIN repos r ON r.id = n.repo_id JOIN projects p ON p.id = r.project_id
		  LEFT JOIN llm_calls c ON c.id = (SELECT MAX(l.id) FROM llm_calls l
		                                    WHERE l.repo_id = n.repo_id AND l.source_pr_key = n.pr_key
		                                      AND l.purpose = 'merge'
		                                      AND l.created_at BETWEEN n.received_at AND n.consolidated_at)
		 WHERE n.consolidated_at >= ?
		 ORDER BY n.consolidated_at DESC, n.pr_id DESC LIMIT ?`, stamp(since), limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []FoldedNote
	for rows.Next() {
		var n FoldedNote
		if err := rows.Scan(&n.ProjectKey, &n.RepoSlug, &n.PRKey, &n.PRTitle, timeAt(&n.ConsolidatedAt),
			&n.Outcome, &n.CostNanoUSD); err != nil {
			return nil, err
		}
		n.PRTitle, _, _ = noteOut(n.PRTitle, nil, nil)
		out = append(out, n)
	}
	return out, rows.Err()
}

// FailedNote is a note the merge call failed on, off the queue until the
// repository's next run (RequeueFailed) or, at the worker's bound of
// attempts, for good.
type FailedNote struct {
	ProjectKey string
	RepoSlug   string
	PRKey      string
	PRTitle    string
	FailedAt   time.Time
	Reason     string
	Attempts   int
}

// FailedNotes is the failed notes, newest failure first, at most limit.
func (s *Store) FailedNotes(ctx context.Context, limit int) ([]FailedNote, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT p.key, r.slug, n.pr_key, n.pr_title, n.fold_failed_at, COALESCE(n.fold_failed, ''), n.fold_attempts
		  FROM notes n JOIN repos r ON r.id = n.repo_id JOIN projects p ON p.id = r.project_id
		 WHERE n.fold_failed_at IS NOT NULL AND n.consolidated_at IS NULL
		 ORDER BY n.fold_failed_at DESC, n.pr_id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []FailedNote
	for rows.Next() {
		var n FailedNote
		if err := rows.Scan(&n.ProjectKey, &n.RepoSlug, &n.PRKey, &n.PRTitle, timeAt(&n.FailedAt), &n.Reason, &n.Attempts); err != nil {
			return nil, err
		}
		n.PRTitle, _, _ = noteOut(n.PRTitle, nil, nil)
		n.Reason = redact.Text(n.Reason)
		out = append(out, n)
	}
	return out, rows.Err()
}

// RunCosts is what each run's calls cost, by run id; a run none of whose
// calls was priced is absent.
func (s *Store) RunCosts(ctx context.Context, ids []int64) (map[int64]int64, error) {
	out := map[int64]int64{}
	if len(ids) == 0 {
		return out, nil
	}
	// The ids as one bound JSON array, json_each its rows: one query, no
	// SQL built from the list.
	list, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT baseline_run_id, SUM(cost_nano_usd) FROM llm_calls
		 WHERE baseline_run_id IN (SELECT value FROM json_each(?))
		 GROUP BY baseline_run_id`, string(list))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		var cost sql.NullInt64
		if err := rows.Scan(&id, &cost); err != nil {
			return nil, err
		}
		if cost.Valid {
			out[id] = cost.Int64
		}
	}
	return out, rows.Err()
}

// CostRow is one month of one project's own model calls, the merges (a
// note's fold) apart from the runs (every baseline call): what they cost,
// how many there were, and how many of them no one priced. Never summed
// with note_calls, noergler's ledger.
type CostRow struct {
	// Month is YYYY-MM, UTC.
	Month      string
	ProjectKey string
	Merges     CostSum
	Runs       CostSum
}

// CostSum is calls and their cost; NanoUSD nil when none was priced.
type CostSum struct {
	Calls    int
	Unpriced int
	NanoUSD  *int64
}

// CostsMonths is how far back Costs looks: the ledger only grows, and the
// page is read on every visit.
const CostsMonths = 12

// Costs is every project's calls by month over the last CostsMonths,
// newest month first, then by key: the Kosten page. One query over the
// ledger's tail (llm_calls_created_idx), grouped in SQL.
func (s *Store) Costs(ctx context.Context, now time.Time) ([]CostRow, error) {
	from := DayStart(now)
	from = time.Date(from.Year(), from.Month()-CostsMonths+1, 1, 0, 0, 0, 0, time.UTC)
	rows, err := s.db.QueryContext(ctx, `
		SELECT substr(c.created_at, 1, 7), p.key, c.purpose = 'merge',
		       COUNT(*), COUNT(*) - COUNT(c.cost_nano_usd), SUM(c.cost_nano_usd)
		  FROM llm_calls c JOIN repos r ON r.id = c.repo_id JOIN projects p ON p.id = r.project_id
		 WHERE c.created_at >= ?
		 GROUP BY 1, 2, 3
		 ORDER BY 1 DESC, 2, 3`, stamp(from))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []CostRow{}
	for rows.Next() {
		var month, key string
		var merge bool
		var sum CostSum
		var nano sql.NullInt64
		if err := rows.Scan(&month, &key, &merge, &sum.Calls, &sum.Unpriced, &nano); err != nil {
			return nil, err
		}
		if nano.Valid {
			sum.NanoUSD = &nano.Int64
		}
		if n := len(out); n == 0 || out[n-1].Month != month || out[n-1].ProjectKey != key {
			out = append(out, CostRow{Month: month, ProjectKey: key})
		}
		row := &out[len(out)-1]
		if merge {
			row.Merges = sum
		} else {
			row.Runs = sum
		}
	}
	return out, rows.Err()
}

// DayStart is the start of t's UTC day: the day a budget and the costs
// page's "today" count by, one rule for both.
func DayStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// USD renders nano-USD as a 3-decimal string, never through a float: the
// amount is rounded half up to milli-USD on the integer.
func USD(nano int64) string {
	sign := ""
	if nano < 0 {
		sign, nano = "-", -nano
	}
	milli := (nano + 500_000) / 1_000_000
	frac := strconv.FormatInt(milli%1000, 10)
	for len(frac) < 3 {
		frac = "0" + frac
	}
	return sign + strconv.FormatInt(milli/1000, 10) + "." + frac
}

// Spent is what Almanach's own model calls since a time cost, and how many
// there were; cost nil when none of them was priced.
func (s *Store) Spent(ctx context.Context, since time.Time) (*int64, int, error) {
	var cost sql.NullInt64
	var calls int
	err := s.db.QueryRowContext(ctx, `SELECT SUM(cost_nano_usd), COUNT(*) FROM llm_calls WHERE created_at >= ?`, stamp(since)).Scan(&cost, &calls)
	if err != nil || !cost.Valid {
		return nil, calls, err
	}
	return &cost.Int64, calls, nil
}
