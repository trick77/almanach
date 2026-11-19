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

// FoldedNotes is the notes taken off the queue at or after since, newest
// first, at most limit.
func (s *Store) FoldedNotes(ctx context.Context, since time.Time, limit int) ([]FoldedNote, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT p.key, r.slug, n.pr_key, n.pr_title, n.consolidated_at,
		       COALESCE(c.outcome, ''), c.cost_nano_usd, COALESCE(json_array_length(c.batch_pr_keys), 1)
		  FROM notes n JOIN repos r ON r.id = n.repo_id JOIN projects p ON p.id = r.project_id
		  LEFT JOIN llm_calls c ON c.id = NULLIF(max(
		      COALESCE((SELECT MAX(l.id) FROM llm_calls l
		                 WHERE l.repo_id = n.repo_id AND l.source_pr_key = n.pr_key AND l.purpose = 'merge'
		                   AND l.created_at BETWEEN n.received_at AND n.consolidated_at), 0),
		      -- A later note of a fold: only the batch calls of its own window
		      -- (llm_calls_created_idx), their few keys expanded.
		      COALESCE((SELECT MAX(l.id) FROM llm_calls l
		                 WHERE l.created_at BETWEEN n.received_at AND n.consolidated_at
		                   AND l.batch_pr_keys IS NOT NULL AND l.repo_id = n.repo_id AND l.purpose = 'merge'
		                   AND EXISTS (SELECT 1 FROM json_each(l.batch_pr_keys) k WHERE k.value = n.pr_key)), 0)), 0)
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
			&n.Outcome, &n.CostNanoUSD, &n.Batch); err != nil {
			return nil, err
		}
		n.PRTitle, _, _ = noteOut(n.PRTitle, nil, nil)
		out = append(out, n)
	}
	return out, rows.Err()
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
// note's fold) apart from the runs (every baseline call) and the briefs
// (a repository's PROJECT.md, asked by a button): what they cost, how many
// there were, and how many of them no one priced. Never summed with
// note_calls, noergler's ledger.
type CostRow struct {
	// Month is YYYY-MM, UTC.
	Month      string
	ProjectKey string
	Merges     CostSum
	Runs       CostSum
	Briefs     CostSum
}

// CostsMonths is how far back Costs looks: the ledger only grows, and the
// page is read on every visit.
const CostsMonths = 12

// CostLedger is the Kosten page's calls twice: by month and project, and
// by month and purpose. Both of one set of rows.
type CostLedger struct {
	Projects []CostRow
	Purposes []PurposeCost
}

// Costs is the last CostsMonths' calls by month and project (newest month
// first, then by key) and by month and purpose (the dearest purpose first
// within a month): the Kosten page. Two queries over the ledger's tail
// (llm_calls_created_idx), grouped in SQL, both held to the last id read
// before them: a call the worker writes between the two is in neither,
// never in one table and missing from the other.
func (s *Store) Costs(ctx context.Context, now time.Time) (CostLedger, error) {
	from := DayStart(now)
	from = time.Date(from.Year(), from.Month()-CostsMonths+1, 1, 0, 0, 0, 0, time.UTC)
	out := CostLedger{Projects: []CostRow{}, Purposes: []PurposeCost{}}
	var upTo sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT MAX(id) FROM llm_calls`).Scan(&upTo); err != nil || !upTo.Valid {
		return out, err
	}
	var err error
	if out.Projects, err = s.costsByProject(ctx, from, upTo.Int64); err != nil {
		return CostLedger{}, err
	}
	if out.Purposes, err = s.costsByPurpose(ctx, from, upTo.Int64); err != nil {
		return CostLedger{}, err
	}
	return out, nil
}

// costsByPurpose is Costs by month and purpose. The reasoning share's
// figures count only the calls that reported reasoning within their
// output: one with reasoning but no output total (stored as 0) would put
// the share past 100 %.
func (s *Store) costsByPurpose(ctx context.Context, from time.Time, upTo int64) ([]PurposeCost, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT substr(created_at, 1, 7), purpose,
		       COUNT(*), COUNT(*) - COUNT(cost_nano_usd), SUM(cost_nano_usd),
		       COALESCE(SUM(input_tokens), 0), COALESCE(SUM(cached_tokens), 0), COALESCE(SUM(output_tokens), 0),
		       SUM(reasoning_tokens) FILTER (WHERE output_tokens >= reasoning_tokens),
		       COALESCE(SUM(output_tokens) FILTER (WHERE output_tokens >= reasoning_tokens), 0)
		  FROM llm_calls
		 WHERE created_at >= ? AND id <= ?
		 GROUP BY 1, 2
		 ORDER BY 1 DESC, COALESCE(SUM(cost_nano_usd), 0) DESC, 2`, stamp(from), upTo)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []PurposeCost{}
	for rows.Next() {
		var p PurposeCost
		var nano, reasoning sql.NullInt64
		if err := rows.Scan(&p.Month, &p.Purpose, &p.Calls, &p.Unpriced, &nano,
			&p.InputTokens, &p.CachedTokens, &p.OutputTokens, &reasoning, &p.ReasonedOutputTokens); err != nil {
			return nil, err
		}
		if nano.Valid {
			p.NanoUSD = &nano.Int64
		}
		if reasoning.Valid {
			p.ReasoningTokens = &reasoning.Int64
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// costsByProject is Costs by month and project, merges, runs and briefs
// apart.
func (s *Store) costsByProject(ctx context.Context, from time.Time, upTo int64) ([]CostRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT substr(c.created_at, 1, 7), p.key,
		       CASE c.purpose WHEN 'merge' THEN 'merge' WHEN 'brief' THEN 'brief' ELSE 'run' END,
		       COUNT(*), COUNT(*) - COUNT(c.cost_nano_usd), SUM(c.cost_nano_usd)
		  FROM llm_calls c JOIN repos r ON r.id = c.repo_id JOIN projects p ON p.id = r.project_id
		 WHERE c.created_at >= ? AND c.id <= ?
		 GROUP BY 1, 2, 3
		 ORDER BY 1 DESC, 2, 3`, stamp(from), upTo)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []CostRow{}
	for rows.Next() {
		var month, key, kind string
		var sum CostSum
		var nano sql.NullInt64
		if err := rows.Scan(&month, &key, &kind, &sum.Calls, &sum.Unpriced, &nano); err != nil {
			return nil, err
		}
		if nano.Valid {
			sum.NanoUSD = &nano.Int64
		}
		if n := len(out); n == 0 || out[n-1].Month != month || out[n-1].ProjectKey != key {
			out = append(out, CostRow{Month: month, ProjectKey: key})
		}
		row := &out[len(out)-1]
		switch kind {
		case PurposeMerge:
			row.Merges = sum
		case PurposeBrief:
			row.Briefs = sum
		default:
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

// OverBudget reports whether spent (nil: none priced) has reached a daily
// budget; never with none (budget 0 or less). The worker's rule and the
// source page's, one.
func OverBudget(spent *int64, budget int64) bool {
	return budget > 0 && spent != nil && *spent >= budget
}

// dearestWindow is how far back Spend.Dearest looks, from the start of
// the spend's window (the UTC day). "30 Tage" in strings.ts' unpricedHint
// and openapi.yaml's unpriced say it: they follow a change.
const dearestWindow = 30 * 24 * time.Hour

// billed is a ledger row the gateway billed: streamed, or from before the
// column with an outcome that answered (inference.Outcome's strings).
const billed = `(streamed = 1 OR outcome IN ('ok', 'unparseable', 'too_long'))`

// Spent is what Almanach's own model calls since a time cost: one read, so
// the sum and the counts agree.
func (s *Store) Spent(ctx context.Context, since time.Time) (Spend, error) {
	var out Spend
	var cost, dearest sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT SUM(cost_nano_usd), COUNT(*),
		COALESCE(SUM(cost_nano_usd IS NULL AND `+billed+`), 0),
		(SELECT MAX(cost_nano_usd) FROM llm_calls WHERE created_at >= ?2)
		FROM llm_calls WHERE created_at >= ?1`, stamp(since), stamp(since.Add(-dearestWindow))).Scan(&cost, &out.Calls, &out.Unpriced, &dearest)
	if err != nil {
		return Spend{}, err
	}
	if cost.Valid {
		out.NanoUSD = &cost.Int64
	}
	out.Dearest = dearest.Int64
	return out, nil
}
