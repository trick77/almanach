package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/trick77/almanach/internal/diagram"
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
	// TriggerDeparture is the run a deactivation queues: no reading, only
	// the project step taking the repository out of the project page. It
	// documents nothing: DocumentedRun never finds it.
	TriggerDeparture = "departure"

	// RunRestyle is a run that reads no code: the current prompts' layout,
	// kind and area rules over the stored page alone, the repository's and,
	// once per project, the project's, for a prompts.Epoch the page is not
	// at. Always trigger TriggerEpoch, its retries too, so it never counts
	// in the poller's retry streak. It documents nothing: LastRun,
	// DocumentedRun and the source page never find it.
	RunRestyle   = "restyle"
	TriggerEpoch = "epoch"
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
	// ReadableBytes is what a full run could have read, the tree's readable
	// files at its commit; ReadBytes the distinct files its folded parts
	// read. Bytes is the budget's count, every call's input. A reconcile
	// carries the documented run's pair.
	ReadableBytes int64
	ReadBytes     int64
	Skipped       int
	Error         string
	QueuedAt      time.Time
	StartedAt     *time.Time
	FinishedAt    *time.Time
	// Epoch is the prompts.Epoch a full or restyle run ran under, the
	// worker's to set when it starts one; 0 on any other run. A full run
	// done with one restyles its repository's page (RestyleDue).
	Epoch int
	// RepoID and ProjectID are the repository's ids; ProjectKey and Slug
	// its current names.
	RepoID    int64
	ProjectID int64
}

// Open reports whether the run is neither done nor failed.
func (r BaselineRun) Open() bool { return r.State != RunDone && r.State != RunFailed }

// runsFrom is a run (b) with its repository (r) and project (p), whose
// current names it carries.
const runsFrom = `baseline_runs b JOIN repos r ON r.id = b.repo_id JOIN projects p ON p.id = r.project_id`

// runColumns are a run's columns over runsFrom, named as scanRun reads
// them. "commit" is quoted: a keyword in SQLite, a column name here.
const runColumns = `b.id AS id, p.key AS project_key, r.slug AS slug, b.mode AS mode, b.trigger AS trigger,
	b.base_commit AS base_commit, b."commit" AS "commit", b.state AS state, b.step AS step, b.steps AS steps,
	b.plan AS plan, b.attempts AS attempts, b.calls AS calls, b.bytes AS bytes, b.readable_bytes AS readable_bytes,
	b.read_bytes AS read_bytes, b.skipped AS skipped, b.error AS error, b.queued_at AS queued_at, b.started_at AS started_at,
	b.finished_at AS finished_at, b.epoch AS epoch, b.repo_id AS repo_id, r.project_id AS project_id`

// lColumns is runColumns of a subquery aliased l, in the same order.
const lColumns = `l.id, l.project_key, l.slug, l.mode, l.trigger, l.base_commit, l."commit", l.state, l.step, l.steps, l.plan,
	l.attempts, l.calls, l.bytes, l.readable_bytes, l.read_bytes, l.skipped, l.error, l.queued_at, l.started_at, l.finished_at, l.epoch,
	l.repo_id, l.project_id`

// readsRepo holds a run row b to the runs that read its repository: never a
// departure (no reading) nor a restyle (the page alone). The poller's
// reconcile chain, its retries and the commit documented go by these.
const readsRepo = `b.trigger <> 'departure' AND b.mode <> 'restyle'`

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
		&r.Attempts, &r.Calls, &r.Bytes, &r.ReadableBytes, &r.ReadBytes, &r.Skipped, &r.Error, timeAt(&r.QueuedAt), timeOrNil(&r.StartedAt), timeOrNil(&r.FinishedAt),
		&r.Epoch, &r.RepoID, &r.ProjectID}, extra...)
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
// is no source repository (a deactivated one included).
func (s *Store) EnqueueRun(ctx context.Context, key, slug, mode, trigger, baseCommit, commit string) (int64, error) {
	rid, ok, err := repoID(ctx, s.db, key, slug)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, ErrNoRepo
	}
	var id int64
	err = s.db.QueryRowContext(ctx, `
		INSERT INTO baseline_runs (repo_id, mode, trigger, base_commit, "commit", state, queued_at)
		SELECT sr.repo_id, ?, ?, ?, ?, 'queued', ? FROM source_repos sr WHERE sr.repo_id = ? AND `+activeRow+`
		RETURNING id`,
		mode, trigger, baseCommit, commit, nowStamp(), rid).Scan(&id)
	if isForeignKey(err) || errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNoRepo
	}
	return id, uniqueAsExists(err)
}

// NextRun is the run the worker steps next: one already started before any
// queued one, so a run goes to its end before the next begins; among equals
// the oldest. ok false when no run is open.
func (s *Store) NextRun(ctx context.Context) (BaselineRun, bool, error) {
	return s.oneRun(ctx, `SELECT `+runColumns+` FROM `+runsFrom+`
		WHERE b.state NOT IN ('done', 'failed')
		ORDER BY b.state = 'queued', b.queued_at, b.id LIMIT 1`)
}

// NextDeparture is the oldest queued departure run: the one a spent daily
// budget still lets start.
func (s *Store) NextDeparture(ctx context.Context) (BaselineRun, bool, error) {
	return s.oneRun(ctx, `SELECT `+runColumns+` FROM `+runsFrom+`
		WHERE b.state = 'queued' AND b.trigger = 'departure'
		ORDER BY b.queued_at, b.id LIMIT 1`)
}

// RunByID is one run, ok false when it is gone (its repository removed).
func (s *Store) RunByID(ctx context.Context, id int64) (BaselineRun, bool, error) {
	return s.oneRun(ctx, `SELECT `+runColumns+` FROM `+runsFrom+` WHERE b.id = ?`, id)
}

// RunStreak is what bounds the poller's automatic runs: Failed is how many
// runs failed since the newest done run (all of them before any), Retried
// how many of the newest runs in a row were the poller's retries (trigger
// retry), whatever they retried.
type RunStreak struct {
	Failed, Retried int
}

// retriedB and retriedL count the newest runs in a row that are retries of
// the repository of run row b (LastRun) or l (SourceRuns). Constants, one
// per alias: SQL is never built at run time. A departure or a restyle
// between them breaks no streak: neither reads the repository.
const (
	retriedB = `(SELECT COUNT(*) FROM baseline_runs t
	  WHERE t.repo_id = b.repo_id AND t.trigger = 'retry'
	    AND t.id > COALESCE((SELECT MAX(n.id) FROM baseline_runs n
	                          WHERE n.repo_id = b.repo_id AND n.trigger NOT IN ('retry', 'departure', 'epoch')), 0))`
	retriedL = `(SELECT COUNT(*) FROM baseline_runs t
	  WHERE t.repo_id = l.repo_id AND t.trigger = 'retry'
	    AND t.id > COALESCE((SELECT MAX(n.id) FROM baseline_runs n
	                          WHERE n.repo_id = l.repo_id AND n.trigger NOT IN ('retry', 'departure', 'epoch')), 0))`
)

// LastRun is a repository's newest run that reads it (never a departure nor
// a restyle), open or not, and its streak, read in the same query: what the
// poller decides the next run by.
func (s *Store) LastRun(ctx context.Context, key, slug string) (BaselineRun, RunStreak, bool, error) {
	rid, ok, err := repoID(ctx, s.db, key, slug)
	if err != nil || !ok {
		return BaselineRun{}, RunStreak{}, false, err
	}
	var st RunStreak
	r, err := scanRun(s.db.QueryRowContext(ctx, `
		SELECT `+runColumns+`,
		       (SELECT COUNT(*) FROM baseline_runs f
		         WHERE f.repo_id = b.repo_id AND f.state = 'failed' AND f.trigger NOT IN ('departure', 'epoch')
		           AND f.id > COALESCE((SELECT MAX(d.id) FROM baseline_runs d
		                                 WHERE d.repo_id = b.repo_id AND d.state = 'done' AND d.trigger NOT IN ('departure', 'epoch')), 0)),
		       `+retriedB+`
		  FROM `+runsFrom+`
		 WHERE b.repo_id = ? AND `+readsRepo+` ORDER BY b.id DESC LIMIT 1`, rid), &st.Failed, &st.Retried)
	if errors.Is(err, sql.ErrNoRows) {
		return BaselineRun{}, RunStreak{}, false, nil
	}
	if err != nil {
		return BaselineRun{}, RunStreak{}, false, err
	}
	return r, st, true, nil
}

// DocumentedRun is a repository's newest done run that read it (never a
// departure nor a restyle): its commit is the one the page was last read
// against, its plan the one the next reconcile starts from. A restyle's
// plan holds no subjects: found here, the next reconcile would read afresh.
func (s *Store) DocumentedRun(ctx context.Context, key, slug string) (BaselineRun, bool, error) {
	rid, ok, err := repoID(ctx, s.db, key, slug)
	if err != nil || !ok {
		return BaselineRun{}, false, err
	}
	return s.oneRun(ctx, `SELECT `+runColumns+` FROM `+runsFrom+`
		WHERE b.repo_id = ? AND b.state = 'done' AND `+readsRepo+` ORDER BY b.id DESC LIMIT 1`, rid)
}

// SaveRun writes a run's progress: the commit it pinned, its mode (a reconcile
// that reads afresh becomes full), its epoch, state, step, plan,
// counters, error and times. A run that is over is never written again:
// reports false when the run is done, failed or gone (its repository
// removed), and writes nothing.
func (s *Store) SaveRun(ctx context.Context, r BaselineRun) (bool, error) {
	var ok bool
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var err error
		ok, err = saveRun(ctx, tx, r)
		return err
	})
	return ok, err
}

// execer is *sql.DB and *sql.Tx.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// saveRun writes the run; a full run done under an epoch, no part skipped,
// restyled its repository's page with it, every step having had the page
// in its prompt under that epoch's rules: the repository is not due a
// restyle (user, 2026-11-04). One that skipped a part left that part's
// statements as they were: still due.
func saveRun(ctx context.Context, db execer, r BaselineRun) (bool, error) {
	n, err := affected(db.ExecContext(ctx, `
		UPDATE baseline_runs SET state = ?, step = ?, steps = ?, plan = ?, attempts = ?, calls = ?, bytes = ?, readable_bytes = ?,
		       read_bytes = ?, skipped = ?, error = ?, started_at = ?, finished_at = ?, "commit" = ?, mode = ?, epoch = ?
		 WHERE id = ? AND state NOT IN ('done', 'failed')`,
		r.State, r.Step, r.Steps, jsonOrNull(r.Plan), r.Attempts, r.Calls, r.Bytes, r.ReadableBytes, r.ReadBytes, r.Skipped, r.Error,
		stampOrNull(r.StartedAt), stampOrNull(r.FinishedAt), r.Commit, r.Mode, r.Epoch, r.ID))
	if err != nil || n != 1 {
		return false, err
	}
	if r.State == RunDone && r.Mode == RunFull && r.Trigger != TriggerDeparture && r.Epoch > 0 && r.Skipped == 0 {
		if _, err := db.ExecContext(ctx, `
			UPDATE source_repos SET restyle_epoch = MAX(restyle_epoch, ?)
			 WHERE repo_id = (SELECT repo_id FROM baseline_runs WHERE id = ?)`, r.Epoch, r.ID); err != nil {
			return false, err
		}
	}
	return true, nil
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

// SetRepoMap is the map step's write: the repository page's map and the
// basis it was drawn from, set on the page's current revision in place,
// and the run's end, in one transaction. No revision of its own: the text
// did not change ("identical rewrite = no revision"). ok false, and nothing
// written, when the run is over or gone. A page that vanished meanwhile
// (the repository removed) takes nothing. A nil map (the model drew none)
// is SQL NULL beside its basis, never the JSON text null: that would read
// back as an empty map.
func (s *Store) SetRepoMap(ctx context.Context, project, repo string, m *diagram.Diagram, basis string, r BaselineRun) (bool, error) {
	var drawn any
	if m != nil {
		raw, err := jsonOf(m)
		if err != nil {
			return false, err
		}
		drawn = raw
	}
	var ok bool
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var err error
		if ok, err = saveRun(ctx, tx, r); err != nil || !ok {
			return err
		}
		// Any name the repository ever had finds it, as CurrentPage does.
		rid, found, err := repoID(ctx, tx, project, repo)
		if err != nil || !found {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			UPDATE revisions SET repo_map = ?, repo_map_basis = ?
			 WHERE scope = 'repository' AND id = (SELECT current_revision_id FROM pages
			                                       WHERE scope = 'repository' AND repo_id = ?)`,
			drawn, basis, rid)
		return err
	})
	if err != nil || !ok {
		return false, err
	}
	return true, nil
}

// ArrangePage is a page's layout, the repository map step's or the project
// step's: the same statements in their areas and order
// (baseline.ApplyOutline), set on the revision in place while it is its
// page's current one. No revision of its own, as SetRepoMap: no statement's
// text changed. ok false, and nothing written, when revisionID is no
// longer its page's current revision.
func (s *Store) ArrangePage(ctx context.Context, revisionID int64, statements []Statement) (bool, error) {
	return arrangeRevision(ctx, s.db, revisionID, statements)
}

// arrangeRevision is ArrangePage's write, alone or in a restyle step's
// transaction: the statements set in place while revisionID is its page's
// current revision.
func arrangeRevision(ctx context.Context, db execer, revisionID int64, statements []Statement) (bool, error) {
	raw, err := jsonOf(orEmptyStatements(statements))
	if err != nil {
		return false, err
	}
	n, err := affected(db.ExecContext(ctx, `
		UPDATE revisions SET statements = ?
		 WHERE id = ? AND id IN (SELECT current_revision_id FROM pages)`,
		raw, revisionID))
	return n > 0, err
}

// JudgeShape sets a project page's first judgment of its shape in place on
// revisionID, when the judgment changes nothing a reader would see but the
// label (a page written as a system's, judged one): no revision of its own,
// as ArrangePage. Only on a project revision still unjudged and its page's
// current one; ok false, and nothing written, otherwise.
func (s *Store) JudgeShape(ctx context.Context, revisionID int64, shape string) (bool, error) {
	return judgeShape(ctx, s.db, revisionID, shape)
}

func judgeShape(ctx context.Context, db execer, revisionID int64, shape string) (bool, error) {
	n, err := affected(db.ExecContext(ctx, `
		UPDATE revisions SET shape = ?
		 WHERE id = ? AND scope = 'project' AND shape IS NULL AND id IN (SELECT current_revision_id FROM pages)`,
		shape, revisionID))
	return n > 0, err
}

// RepoRuns is what the source page shows of one repository's runs: the
// newest run, what its priced calls cost (nil when none was priced), the
// commit last documented and how many of the newest runs in a
// row were the poller's retries (RunStreak.Retried).
type RepoRuns struct {
	Last         BaselineRun
	LastCost     *int64
	Documented   string
	DocumentedAt *time.Time
	Retried      int
}

// SourceRuns is RepoRuns for every repository of a source that has a run,
// by slug.
func (s *Store) SourceRuns(ctx context.Context, key string) (map[string]RepoRuns, error) {
	pid, ok, err := projectID(ctx, s.db, key)
	if err != nil || !ok {
		return map[string]RepoRuns{}, err
	}
	// l is each repository's newest run, a restyle only while it is open:
	// it holds the repository's one open run, and the page must say why its
	// buttons wait; over, the reading's run shows again. d is its newest
	// done one that read it.
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+lColumns+`,
		       (SELECT SUM(c.cost_nano_usd) FROM llm_calls c WHERE c.baseline_run_id = l.id),
		       d."commit", d.finished_at, `+retriedL+`
		  FROM (SELECT `+runColumns+`, ROW_NUMBER() OVER (PARTITION BY b.repo_id ORDER BY b.id DESC) AS rn
		          FROM `+runsFrom+` WHERE r.project_id = ?1 AND (b.mode <> 'restyle' OR b.state NOT IN ('done', 'failed'))) l
		  LEFT JOIN (SELECT b.repo_id, b."commit", b.finished_at,
		                    ROW_NUMBER() OVER (PARTITION BY b.repo_id ORDER BY b.id DESC) AS rn
		               FROM baseline_runs b JOIN repos r ON r.id = b.repo_id
		              WHERE r.project_id = ?1 AND b.state = 'done' AND `+readsRepo+`) d
		    ON d.repo_id = l.repo_id AND d.rn = 1
		 WHERE l.rn = 1`, pid)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]RepoRuns{}
	for rows.Next() {
		var rr RepoRuns
		var documented *string
		last, err := scanRun(rows, &rr.LastCost, &documented, timeOrNil(&rr.DocumentedAt), &rr.Retried)
		if err != nil {
			return nil, err
		}
		rr.Last, rr.Documented = last, deref(documented)
		out[last.Slug] = rr
	}
	return out, rows.Err()
}
