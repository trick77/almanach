package store

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"time"
)

// A restyle brings the prompts of today to a stored page without reading
// code (RunRestyle). restyle_epoch on source_repos and projects is the
// epoch each page was last restyled at; a page below its scope's epoch
// (Epochs) is due once, a full run done under it counting for its
// repository's page.

// EnqueueRestyle queues a restyle run of one repository for epoch at its
// documented commit, the page's: the run reads no code, so no head is
// pinned. byHand is a button's, which the worker runs past RestyleTries
// (RestyleDue): its plan says so, the trigger being epoch whoever queued it
// (the table's check, 0004). ErrExists when the repository has an open run,
// ErrNoRepo when it is no source repository (a deactivated one included).
func (s *Store) EnqueueRestyle(ctx context.Context, key, slug, commit string, epoch int, byHand bool) (int64, error) {
	plan := `{"restyle":{}}`
	if byHand {
		plan = `{"restyle":{"manual":true}}`
	}
	rid, ok, err := repoID(ctx, s.db, key, slug)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, ErrNoRepo
	}
	var id int64
	err = s.db.QueryRowContext(ctx, `
		INSERT INTO baseline_runs (repo_id, mode, trigger, base_commit, "commit", state, plan, queued_at, epoch)
		SELECT sr.repo_id, 'restyle', 'epoch', ?, ?, 'queued', ?, ?, ? FROM source_repos sr
		 WHERE sr.repo_id = ? AND `+activeRow+`
		RETURNING id`,
		commit, commit, plan, nowStamp(), epoch, rid).Scan(&id)
	if isForeignKey(err) || errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNoRepo
	}
	return id, uniqueAsExists(err)
}

// RestyleTries is how many restyles of one page may fail at one epoch: the
// first and three retries, as the poller's reading runs. The project page's
// are counted over the project, whichever repository's run made them, so a
// page that keeps failing costs RestyleTries calls, not that many per
// repository.
const RestyleTries = 4

// The steps of a restyle run as its row counts them (BaselineRun.Step): the
// repository's page, the project's, the map. A run that failed at
// RestyleProjectStep failed on the project page; one that failed on the
// map spends neither page's tries but its repository's.
const (
	RestyleRepoStep    = 0
	RestyleProjectStep = 1
	RestyleMapStep     = 2
)

// Epochs is the prompts' generation per scope (prompts.RepoEpoch,
// prompts.ProjectEpoch): a page restyled below its scope's is due, and is
// marked restyled at its scope's own. Run is the newer, what a restyle run
// is stamped with (BaselineRun.Epoch).
type Epochs struct {
	Repo, Project int
}

// Run is the epoch a restyle run runs under: the newer of the two.
func (e Epochs) Run() int { return max(e.Repo, e.Project) }

// RestyleState reports what of a repository is due a restyle at e. Its page
// while below e.Repo; its project's page while below e.Project, written,
// and the project has more than one active source repository (a project of
// one has none: its repository's page is the project's). Either only while
// fewer than RestyleTries of its restyles failed under the scope's rules
// (a run stamped at or past the scope's epoch: the epochs only grow), the
// newest of them before failedBefore (the retry's wait); so a bump of one
// scope gives the other's pages no new tries. Nothing is due of a
// repository that is no active source.
func (s *Store) RestyleState(ctx context.Context, key, slug string, e Epochs, failedBefore time.Time) (RestyleState, error) {
	return s.restyleState(ctx, key, slug, e, failedBefore, RestyleTries)
}

// RestyleDue is RestyleState for a restyle queued by hand: due while below
// the scope's epoch, whatever the restyles that failed (a button is the
// way past RestyleTries).
func (s *Store) RestyleDue(ctx context.Context, key, slug string, e Epochs) (RestyleState, error) {
	return s.restyleState(ctx, key, slug, e, time.Now().AddDate(100, 0, 0), math.MaxInt32)
}

func (s *Store) restyleState(ctx context.Context, key, slug string, e Epochs, failedBefore time.Time, tries int) (RestyleState, error) {
	var st RestyleState
	rid, ok, err := repoID(ctx, s.db, key, slug)
	if err != nil || !ok {
		return st, err
	}
	// f is the failed restyles under the scope's rules: the repository's
	// own, and the project's that failed on the project page.
	err = s.db.QueryRowContext(ctx, `
		SELECT sr.restyle_epoch < ?2
		       AND (SELECT COUNT(*) FROM baseline_runs f
		             WHERE f.repo_id = sr.repo_id AND f.mode = 'restyle' AND f.state = 'failed' AND f.epoch >= ?2) < ?3
		       AND COALESCE((SELECT MAX(f.finished_at) FROM baseline_runs f
		                      WHERE f.repo_id = sr.repo_id AND f.mode = 'restyle' AND f.state = 'failed' AND f.epoch >= ?2), '') < ?4,
		       p.restyle_epoch < ?6
		       AND EXISTS (SELECT 1 FROM pages pg WHERE pg.scope = 'project' AND pg.project_id = sr.project_id)
		       AND (SELECT COUNT(*) FROM source_repos o WHERE o.project_id = sr.project_id AND o.deactivated_at IS NULL) > 1
		       AND (SELECT COUNT(*) FROM baseline_runs f JOIN source_repos o ON o.repo_id = f.repo_id
		             WHERE o.project_id = sr.project_id AND f.mode = 'restyle' AND f.state = 'failed' AND f.epoch >= ?6 AND f.step = ?5) < ?3
		       AND COALESCE((SELECT MAX(f.finished_at) FROM baseline_runs f JOIN source_repos o ON o.repo_id = f.repo_id
		                      WHERE o.project_id = sr.project_id AND f.mode = 'restyle' AND f.state = 'failed' AND f.epoch >= ?6
		                        AND f.step = ?5), '') < ?4,
		       EXISTS (SELECT 1 FROM baseline_runs o WHERE o.mode = 'restyle' AND o.state NOT IN ('done', 'failed'))
		  FROM source_repos sr JOIN projects p ON p.id = sr.project_id
		 WHERE sr.repo_id = ?1 AND `+activeRow,
		rid, e.Repo, tries, stamp(failedBefore), RestyleProjectStep, e.Project).Scan(&st.RepoDue, &st.ProjectDue, &st.Open)
	if errors.Is(err, sql.ErrNoRows) {
		return RestyleState{}, nil
	}
	return st, err
}

// staleRepo is a repository the poller would restyle: its page below
// ?1, or, the project's first active repository by slug (the one the
// poller comes to first), its project's page below ?2 in a project of more
// than one.
const staleRepo = `(x.restyle_epoch < ?1
	   AND EXISTS (SELECT 1 FROM pages pg WHERE pg.scope = 'repository' AND pg.project_id = x.project_id AND pg.repo_id = x.repo_id)
	  OR xp.restyle_epoch < ?2
	   AND EXISTS (SELECT 1 FROM pages pg WHERE pg.scope = 'project' AND pg.project_id = x.project_id)
	   AND (SELECT COUNT(*) FROM source_repos o WHERE o.project_id = x.project_id AND o.deactivated_at IS NULL) > 1
	   AND xr.slug = (SELECT MIN(r2.slug) FROM source_repos s2 JOIN repos r2 ON r2.id = s2.repo_id
	                   WHERE s2.project_id = x.project_id AND s2.deactivated_at IS NULL))`

// rank is the place of the repository key/slug among the stale ones in the
// poller's order, itself counted.
func (s *Store) rank(ctx context.Context, key, slug string, e Epochs) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM source_repos x JOIN repos xr ON xr.id = x.repo_id JOIN projects xp ON xp.id = x.project_id
		 WHERE x.deactivated_at IS NULL AND (xp.key < ?3 OR xp.key = ?3 AND xr.slug <= ?4) AND `+staleRepo,
		e.Repo, e.Project, key, slug).Scan(&n)
	return max(n, 1), err
}

// RepoStale reports a repository's page below e.Repo, with its restyles
// and its rank; ok false when it is at the epoch, unwritten, or no active
// source.
func (s *Store) RepoStale(ctx context.Context, key, slug string, e Epochs) (Stale, bool, error) {
	var st Stale
	rid, ok, err := repoID(ctx, s.db, key, slug)
	if err != nil || !ok {
		return st, false, err
	}
	var pkey, rslug string
	err = s.db.QueryRowContext(ctx, `
		SELECT sr.restyle_epoch,
		       (SELECT COUNT(*) FROM baseline_runs f
		         WHERE f.repo_id = sr.repo_id AND f.mode = 'restyle' AND f.state = 'failed' AND f.epoch >= ?2),
		       COALESCE((SELECT o.state FROM baseline_runs o
		                  WHERE o.repo_id = sr.repo_id AND o.mode = 'restyle' AND o.state NOT IN ('done', 'failed')), ''),
		       p.key, r.slug
		  FROM `+sourceReposFrom+`
		 WHERE sr.repo_id = ?1 AND `+activeRow+` AND sr.restyle_epoch < ?2
		   AND EXISTS (SELECT 1 FROM pages pg WHERE pg.scope = 'repository' AND pg.project_id = sr.project_id AND pg.repo_id = sr.repo_id)`,
		rid, e.Repo).Scan(&st.Epoch, &st.Failed, &st.Run, &pkey, &rslug)
	if errors.Is(err, sql.ErrNoRows) {
		return Stale{}, false, nil
	}
	if err != nil {
		return st, false, err
	}
	st.Rank, err = s.rank(ctx, pkey, rslug, e)
	return st, err == nil, err
}

// ProjectStale reports a project's page below e.Project, as RepoStale: only
// a written page of a project of more than one active repository, a project
// of one having none of its own.
func (s *Store) ProjectStale(ctx context.Context, key string, e Epochs) (Stale, bool, error) {
	var st Stale
	var pkey, first string
	err := s.db.QueryRowContext(ctx, `
		SELECT p.restyle_epoch,
		       (SELECT COUNT(*) FROM baseline_runs f JOIN source_repos o ON o.repo_id = f.repo_id
		         WHERE o.project_id = p.id AND f.mode = 'restyle' AND f.state = 'failed' AND f.epoch >= ?2 AND f.step = ?3),
		       COALESCE((SELECT f.state FROM baseline_runs f JOIN source_repos o ON o.repo_id = f.repo_id
		                  WHERE o.project_id = p.id AND f.mode = 'restyle' AND f.state NOT IN ('done', 'failed')), ''),
		       p.key,
		       (SELECT MIN(r2.slug) FROM source_repos s2 JOIN repos r2 ON r2.id = s2.repo_id
		         WHERE s2.project_id = p.id AND s2.deactivated_at IS NULL)
		  FROM projects p
		 WHERE p.id = (SELECT project_id FROM project_names WHERE key = ?1) AND p.restyle_epoch < ?2
		   AND EXISTS (SELECT 1 FROM pages pg WHERE pg.scope = 'project' AND pg.project_id = p.id)
		   AND (SELECT COUNT(*) FROM source_repos o WHERE o.project_id = p.id AND o.deactivated_at IS NULL) > 1`,
		key, e.Project, RestyleProjectStep).Scan(&st.Epoch, &st.Failed, &st.Run, &pkey, &first)
	if errors.Is(err, sql.ErrNoRows) {
		return Stale{}, false, nil
	}
	if err != nil {
		return st, false, err
	}
	st.Rank, err = s.rank(ctx, pkey, first, e)
	return st, err == nil, err
}

// MarkProjectRestyled puts the project page of run runID's repository at
// epoch: the run's project step answered under today's prompts, the page
// written, or found as it should be, with the whole of it in the prompt.
// Only while the run stands and did not fail: a run gone with its
// repository, or over another way, wrote nothing to mark. Outside the
// step's transaction: a failed write costs one restyle call later, never
// the page.
func (s *Store) MarkProjectRestyled(ctx context.Context, runID int64, epoch int) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE projects SET restyle_epoch = MAX(restyle_epoch, ?)
		 WHERE id = (SELECT r.project_id FROM baseline_runs b JOIN repos r ON r.id = b.repo_id
		              WHERE b.id = ? AND b.state <> 'failed')`, epoch, runID)
	return err
}

// Restyled is what one restyle step writes beside the run's progress: a
// revision when the text changed, else the page laid out anew in place on
// its current revision (ArrangeID, 0 for nothing to lay out).
type Restyled struct {
	Revision  *NewRevision
	ArrangeID int64
	Arranged  []Statement
	// JudgeID, when no revision is written, is the project page's current
	// revision to set Shape on in place (JudgeShape): a first judgment that
	// changes nothing else.
	JudgeID int64
	Shape   string
}

// ApplyRestyle writes a restyle step in one transaction: the run's
// progress, what the step changed of the page, and the page's
// restyle_epoch, so a restart never pays the step's call twice and a later
// failure of the run never makes the page due again. Scope says whose
// epoch: the repository's or its project's. Returns the revision number, 0
// without one; ok false, and nothing written, when the run is over or gone.
// A layout whose revision is no longer the page's current one is not
// written, the epoch all the same: the newer revision's writer laid it out.
func (s *Store) ApplyRestyle(ctx context.Context, r BaselineRun, scope string, epoch int, w Restyled) (int, bool, error) {
	var n int
	var ok bool
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var err error
		if ok, err = saveRun(ctx, tx, r); err != nil || !ok {
			return err
		}
		switch {
		case w.Revision != nil:
			if n, err = insertRevision(ctx, tx, *w.Revision); err != nil {
				return err
			}
		case w.ArrangeID != 0:
			if _, err := arrangeRevision(ctx, tx, w.ArrangeID, w.Arranged); err != nil {
				return err
			}
		}
		if w.Revision == nil && w.JudgeID != 0 {
			if _, err := judgeShape(ctx, tx, w.JudgeID, w.Shape); err != nil {
				return err
			}
		}
		if scope == ScopeProject {
			_, err = tx.ExecContext(ctx, `
				UPDATE projects SET restyle_epoch = MAX(restyle_epoch, ?)
				 WHERE id = (SELECT project_id FROM source_repos WHERE repo_id = ?)`, epoch, r.RepoID)
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE source_repos SET restyle_epoch = MAX(restyle_epoch, ?) WHERE repo_id = ?`, epoch, r.RepoID)
		return err
	})
	if err != nil || !ok {
		return 0, false, err
	}
	return n, true, nil
}
