package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// The baseline runs: one repository documented at one commit, one model call
// per step. The worker drains them after the notes; the poller and the API
// queue them.

// Run modes and triggers.
const (
	RunFull      = "full"
	RunReconcile = "reconcile"

	TriggerManual     = "manual"
	TriggerFirstReady = "first_ready"
	TriggerMonthly    = "monthly"
	TriggerRetry      = "retry"
)

// Run states: queued until the worker takes it, then one state per kind of
// step, done or failed at the end. A run that is neither done nor failed is
// open; one repository has at most one.
const (
	RunQueued      = "queued"
	RunCataloguing = "cataloguing"
	RunDescribing  = "describing"
	RunProject     = "project"
	RunDone        = "done"
	RunFailed      = "failed"
)

// ErrNoRepo is a run asked for a repository that is no source repository.
var ErrNoRepo = errors.New("no such source repository")

// BaselineRun is one run as stored. Plan is the catalogue, the worker's to
// read; the store keeps it verbatim.
type BaselineRun struct {
	ID         int64
	ProjectKey string
	Slug       string
	Mode       string
	Trigger    string
	BaseCommit string
	Commit     string
	State      string
	Step       int
	Steps      int
	Plan       json.RawMessage
	Attempts   int
	Calls      int
	Bytes      int64
	Skipped    int
	Error      string
	QueuedAt   time.Time
	StartedAt  *time.Time
	FinishedAt *time.Time
}

// Open reports whether the run is neither done nor failed.
func (r BaselineRun) Open() bool { return r.State != RunDone && r.State != RunFailed }

// runColumns quotes "commit": a keyword in SQLite, a column name here.
const runColumns = `id, project_key, slug, mode, trigger, base_commit, "commit", state, step, steps, plan,
	attempts, calls, bytes, skipped, error, queued_at, started_at, finished_at`

// scanner is *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

// scanRun reads runColumns, then extra destinations for whatever a query
// selects after them.
func scanRun(row scanner, extra ...any) (BaselineRun, error) {
	var r BaselineRun
	var plan []byte
	dest := append([]any{&r.ID, &r.ProjectKey, &r.Slug, &r.Mode, &r.Trigger, &r.BaseCommit, &r.Commit, &r.State, &r.Step, &r.Steps, &plan,
		&r.Attempts, &r.Calls, &r.Bytes, &r.Skipped, &r.Error, timeAt(&r.QueuedAt), timeOrNil(&r.StartedAt), timeOrNil(&r.FinishedAt)}, extra...)
	err := row.Scan(dest...)
	if len(plan) > 0 {
		r.Plan = json.RawMessage(plan)
	}
	return r, err
}

// oneRun is a single-row run query, ok false when there is none.
func (s *Store) oneRun(ctx context.Context, query string, args ...any) (BaselineRun, bool, error) {
	r, err := scanRun(s.db.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return BaselineRun{}, false, nil
	}
	if err != nil {
		return BaselineRun{}, false, err
	}
	return r, true, nil
}

// EnqueueRun queues a run of one repository at a pinned commit and returns
// its id. ErrExists when the repository has an open run, ErrNoRepo when it
// is no source repository.
func (s *Store) EnqueueRun(ctx context.Context, key, slug, mode, trigger, baseCommit, commit string) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO baseline_runs (project_key, slug, mode, trigger, base_commit, "commit", state, queued_at)
		VALUES (?, ?, ?, ?, ?, ?, 'queued', ?) RETURNING id`,
		key, slug, mode, trigger, baseCommit, commit, nowStamp()).Scan(&id)
	if isForeignKey(err) {
		return 0, ErrNoRepo
	}
	return id, uniqueAsExists(err)
}

// NextRun is the run the worker steps next: one already started before any
// queued one, so a run goes to its end before the next begins; among equals
// the oldest. ok false when no run is open.
func (s *Store) NextRun(ctx context.Context) (BaselineRun, bool, error) {
	return s.oneRun(ctx, `SELECT `+runColumns+` FROM baseline_runs
		WHERE state NOT IN ('done', 'failed')
		ORDER BY state = 'queued', queued_at, id LIMIT 1`)
}

// RunByID is one run, ok false when it is gone (its repository removed).
func (s *Store) RunByID(ctx context.Context, id int64) (BaselineRun, bool, error) {
	return s.oneRun(ctx, `SELECT `+runColumns+` FROM baseline_runs WHERE id = ?`, id)
}

// LastRun is a repository's newest run, open or not, and how many runs
// failed since its newest done run (all of them before any): what bounds
// the automatic retries, read in the same query.
func (s *Store) LastRun(ctx context.Context, key, slug string) (BaselineRun, int, bool, error) {
	var failed int
	r, err := scanRun(s.db.QueryRowContext(ctx, `
		SELECT `+runColumns+`,
		       (SELECT COUNT(*) FROM baseline_runs f
		         WHERE f.project_key = l.project_key AND f.slug = l.slug AND f.state = 'failed'
		           AND f.id > COALESCE((SELECT MAX(d.id) FROM baseline_runs d
		                                 WHERE d.project_key = l.project_key AND d.slug = l.slug AND d.state = 'done'), 0))
		  FROM baseline_runs l
		 WHERE l.project_key = ? AND l.slug = ? ORDER BY l.id DESC LIMIT 1`, key, slug), &failed)
	if errors.Is(err, sql.ErrNoRows) {
		return BaselineRun{}, 0, false, nil
	}
	if err != nil {
		return BaselineRun{}, 0, false, err
	}
	return r, failed, true, nil
}

// DocumentedRun is a repository's newest done run: its commit is the one
// the page was last read against.
func (s *Store) DocumentedRun(ctx context.Context, key, slug string) (BaselineRun, bool, error) {
	return s.oneRun(ctx, `SELECT `+runColumns+` FROM baseline_runs
		WHERE project_key = ? AND slug = ? AND state = 'done' ORDER BY id DESC LIMIT 1`, key, slug)
}

// SaveRun writes a run's progress: the commit it pinned, its mode (a reconcile
// that reads afresh becomes full), state, step, plan,
// counters, error and times. A run that is over is never written again:
// reports false when the run is done, failed or gone (its repository
// removed), and writes nothing.
func (s *Store) SaveRun(ctx context.Context, r BaselineRun) (bool, error) {
	return saveRun(ctx, s.db, r)
}

// execer is *sql.DB and *sql.Tx.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func saveRun(ctx context.Context, db execer, r BaselineRun) (bool, error) {
	n, err := affected(db.ExecContext(ctx, `
		UPDATE baseline_runs SET state = ?, step = ?, steps = ?, plan = ?, attempts = ?, calls = ?, bytes = ?,
		       skipped = ?, error = ?, started_at = ?, finished_at = ?, "commit" = ?, mode = ?
		 WHERE id = ? AND state NOT IN ('done', 'failed')`,
		r.State, r.Step, r.Steps, jsonOrNull(r.Plan), r.Attempts, r.Calls, r.Bytes, r.Skipped, r.Error,
		stampOrNull(r.StartedAt), stampOrNull(r.FinishedAt), r.Commit, r.Mode, r.ID))
	return n == 1, err
}

// ApplyBaselineStep writes a step's revision and the run's progress in one
// transaction: a crash between the two would fold the same subject twice
// after the restart. Returns the revision number; ok false, and nothing
// written, when the run is over or gone.
func (s *Store) ApplyBaselineStep(ctx context.Context, rev NewRevision, r BaselineRun) (int, bool, error) {
	var n int
	var ok bool
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var err error
		if ok, err = saveRun(ctx, tx, r); err != nil || !ok {
			return err
		}
		n, err = insertRevision(ctx, tx, rev)
		return err
	})
	if err != nil || !ok {
		return 0, false, err
	}
	return n, true, nil
}

// RepoRuns is what the source page shows of one repository's runs: the
// newest run, what the gateway priced of its calls (nil when it priced
// none) and the commit last documented.
type RepoRuns struct {
	Last         BaselineRun
	LastCost     *int64
	Documented   string
	DocumentedAt *time.Time
}

// SourceRuns is RepoRuns for every repository of a source that has a run,
// by slug.
func (s *Store) SourceRuns(ctx context.Context, key string) (map[string]RepoRuns, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+runColumns+`,
		       (SELECT SUM(c.cost_nano_usd) FROM llm_calls c WHERE c.baseline_run_id = l.id),
		       (SELECT d."commit" FROM baseline_runs d
		         WHERE d.project_key = l.project_key AND d.slug = l.slug AND d.state = 'done' ORDER BY d.id DESC LIMIT 1),
		       (SELECT d.finished_at FROM baseline_runs d
		         WHERE d.project_key = l.project_key AND d.slug = l.slug AND d.state = 'done' ORDER BY d.id DESC LIMIT 1)
		  FROM (SELECT `+runColumns+`, ROW_NUMBER() OVER (PARTITION BY slug ORDER BY id DESC) AS rn
		          FROM baseline_runs WHERE project_key = ?) l
		 WHERE l.rn = 1`, key)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]RepoRuns{}
	for rows.Next() {
		var rr RepoRuns
		var documented *string
		last, err := scanRun(rows, &rr.LastCost, &documented, timeOrNil(&rr.DocumentedAt))
		if err != nil {
			return nil, err
		}
		rr.Last, rr.Documented = last, deref(documented)
		out[last.Slug] = rr
	}
	return out, rows.Err()
}
