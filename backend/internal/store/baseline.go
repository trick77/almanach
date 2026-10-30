package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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

const runColumns = `id, project_key, slug, mode, trigger, base_commit, commit, state, step, steps, plan,
	attempts, calls, bytes, skipped, error, queued_at, started_at, finished_at`

func scanRun(row pgx.Row) (BaselineRun, error) {
	var r BaselineRun
	var plan []byte
	err := row.Scan(&r.ID, &r.ProjectKey, &r.Slug, &r.Mode, &r.Trigger, &r.BaseCommit, &r.Commit, &r.State, &r.Step, &r.Steps, &plan,
		&r.Attempts, &r.Calls, &r.Bytes, &r.Skipped, &r.Error, &r.QueuedAt, &r.StartedAt, &r.FinishedAt)
	if len(plan) > 0 {
		r.Plan = json.RawMessage(plan)
	}
	return r, err
}

// oneRun is a single-row run query, ok false when there is none.
func (s *Store) oneRun(ctx context.Context, sql string, args ...any) (BaselineRun, bool, error) {
	r, err := scanRun(s.pool.QueryRow(ctx, sql, args...))
	if errors.Is(err, pgx.ErrNoRows) {
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
	err := s.pool.QueryRow(ctx, `
		INSERT INTO baseline_runs (project_key, slug, mode, trigger, base_commit, commit, state)
		VALUES ($1, $2, $3, $4, $5, $6, 'queued') RETURNING id`,
		key, slug, mode, trigger, baseCommit, commit).Scan(&id)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" {
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
	return s.oneRun(ctx, `SELECT `+runColumns+` FROM baseline_runs WHERE id = $1`, id)
}

// LastRun is a repository's newest run, open or not.
func (s *Store) LastRun(ctx context.Context, key, slug string) (BaselineRun, bool, error) {
	return s.oneRun(ctx, `SELECT `+runColumns+` FROM baseline_runs
		WHERE project_key = $1 AND slug = $2 ORDER BY id DESC LIMIT 1`, key, slug)
}

// DocumentedRun is a repository's newest done run: its commit is the one
// the page was last read against.
func (s *Store) DocumentedRun(ctx context.Context, key, slug string) (BaselineRun, bool, error) {
	return s.oneRun(ctx, `SELECT `+runColumns+` FROM baseline_runs
		WHERE project_key = $1 AND slug = $2 AND state = 'done' ORDER BY id DESC LIMIT 1`, key, slug)
}

// SaveRun writes a run's progress: state, step, plan, counters, error and
// times. Reports false when the run is gone, its repository removed.
func (s *Store) SaveRun(ctx context.Context, r BaselineRun) (bool, error) {
	return saveRun(ctx, s.pool, r)
}

type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func saveRun(ctx context.Context, db execer, r BaselineRun) (bool, error) {
	var plan any
	if len(r.Plan) > 0 {
		plan = []byte(r.Plan)
	}
	tag, err := db.Exec(ctx, `
		UPDATE baseline_runs SET state = $2, step = $3, steps = $4, plan = $5, attempts = $6, calls = $7, bytes = $8,
		       skipped = $9, error = $10, started_at = $11, finished_at = $12
		 WHERE id = $1`,
		r.ID, r.State, r.Step, r.Steps, plan, r.Attempts, r.Calls, r.Bytes, r.Skipped, r.Error, r.StartedAt, r.FinishedAt)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// ApplyBaselineStep writes a step's revision and the run's progress in one
// transaction: a crash between the two would fold the same subject twice
// after the restart. Returns the revision number; ok false, and nothing
// written, when the run is gone.
func (s *Store) ApplyBaselineStep(ctx context.Context, rev NewRevision, r BaselineRun) (int, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	ok, err := saveRun(ctx, tx, r)
	if err != nil || !ok {
		return 0, false, err
	}
	n, err := insertRevision(ctx, tx, rev)
	if err != nil {
		return 0, false, err
	}
	return n, true, tx.Commit(ctx)
}

// RunCost sums what the gateway priced of a run's calls; nil when it priced
// none of them.
func (s *Store) RunCost(ctx context.Context, runID int64) (*int64, error) {
	var cost *int64
	err := s.pool.QueryRow(ctx, `SELECT SUM(cost_nano_usd) FROM llm_calls WHERE baseline_run_id = $1`, runID).Scan(&cost)
	return cost, err
}

// RepoRuns is what the source page shows of one repository's runs: the
// newest run and the commit last documented.
type RepoRuns struct {
	Last         BaselineRun
	LastCost     *int64
	Documented   string
	DocumentedAt *time.Time
}

// SourceRuns is RepoRuns for every repository of a source that has a run,
// by slug.
func (s *Store) SourceRuns(ctx context.Context, key string) (map[string]RepoRuns, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+runColumns+`,
		       (SELECT SUM(c.cost_nano_usd) FROM llm_calls c WHERE c.baseline_run_id = l.id),
		       d.doc_commit, d.doc_at
		  FROM (SELECT DISTINCT ON (slug) * FROM baseline_runs WHERE project_key = $1 ORDER BY slug, id DESC) l
		  LEFT JOIN LATERAL (SELECT commit AS doc_commit, finished_at AS doc_at FROM baseline_runs
		                      WHERE project_key = l.project_key AND slug = l.slug AND state = 'done'
		                      ORDER BY id DESC LIMIT 1) d ON true`, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]RepoRuns{}
	for rows.Next() {
		var rr RepoRuns
		var plan []byte
		var documented *string
		r := &rr.Last
		if err := rows.Scan(&r.ID, &r.ProjectKey, &r.Slug, &r.Mode, &r.Trigger, &r.BaseCommit, &r.Commit, &r.State, &r.Step, &r.Steps, &plan,
			&r.Attempts, &r.Calls, &r.Bytes, &r.Skipped, &r.Error, &r.QueuedAt, &r.StartedAt, &r.FinishedAt,
			&rr.LastCost, &documented, &rr.DocumentedAt); err != nil {
			return nil, err
		}
		if len(plan) > 0 {
			r.Plan = json.RawMessage(plan)
		}
		rr.Documented = deref(documented)
		out[r.Slug] = rr
	}
	return out, rows.Err()
}
