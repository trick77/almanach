package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// A restyle brings the prompts of prompts.Epoch to a stored page without
// reading code (RunRestyle). restyle_epoch on source_repos and projects is
// the epoch each page was last restyled at; a page below the current one is
// due once, a full run done under it counting for its repository's page.

// EnqueueRestyle queues a restyle run of one repository for epoch at its
// documented commit, the page's: the run reads no code, so no head is
// pinned. ErrExists when the repository has an open run, ErrNoRepo when it
// is no source repository (a deactivated one included).
func (s *Store) EnqueueRestyle(ctx context.Context, key, slug, commit string, epoch int) (int64, error) {
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
		SELECT sr.repo_id, 'restyle', 'epoch', ?, ?, 'queued', '{"restyle":{}}', ?, ? FROM source_repos sr
		 WHERE sr.repo_id = ? AND `+activeRow+`
		RETURNING id`,
		commit, commit, nowStamp(), epoch, rid).Scan(&id)
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
// repository's page, then the project's. A run that failed at
// RestyleProjectStep failed on the project page.
const (
	RestyleRepoStep    = 0
	RestyleProjectStep = 1
)

// RestyleState is what is due of a repository's restyle at an epoch: its
// page, its project's. One answer for the poller's queueing and the run's
// steps, so the two never disagree and a run never ends with a page left
// due that it judged not due.
type RestyleState struct {
	RepoDue, ProjectDue bool
	// Open: a restyle is open somewhere in the wiki.
	Open bool
}

// RestyleState reports what of a repository is due a restyle at epoch. Its
// page while below it; its project's page while below it, written, and the
// project has more than one active source repository (a project of one has
// none: its repository's page is the project's). Either only while fewer
// than RestyleTries of its restyles failed at this epoch, the newest of
// them before failedBefore (the retry's wait). Nothing is due of a
// repository that is no active source.
func (s *Store) RestyleState(ctx context.Context, key, slug string, epoch int, failedBefore time.Time) (RestyleState, error) {
	var st RestyleState
	rid, ok, err := repoID(ctx, s.db, key, slug)
	if err != nil || !ok {
		return st, err
	}
	// f is the failed restyles at the epoch: the repository's own, and the
	// project's that failed on the project page.
	err = s.db.QueryRowContext(ctx, `
		SELECT sr.restyle_epoch < ?2
		       AND (SELECT COUNT(*) FROM baseline_runs f
		             WHERE f.repo_id = sr.repo_id AND f.mode = 'restyle' AND f.state = 'failed' AND f.epoch = ?2) < ?3
		       AND COALESCE((SELECT MAX(f.finished_at) FROM baseline_runs f
		                      WHERE f.repo_id = sr.repo_id AND f.mode = 'restyle' AND f.state = 'failed' AND f.epoch = ?2), '') < ?4,
		       p.restyle_epoch < ?2
		       AND EXISTS (SELECT 1 FROM pages pg WHERE pg.scope = 'project' AND pg.project_id = sr.project_id)
		       AND (SELECT COUNT(*) FROM source_repos o WHERE o.project_id = sr.project_id AND o.deactivated_at IS NULL) > 1
		       AND (SELECT COUNT(*) FROM baseline_runs f JOIN source_repos o ON o.repo_id = f.repo_id
		             WHERE o.project_id = sr.project_id AND f.mode = 'restyle' AND f.state = 'failed' AND f.epoch = ?2 AND f.step = ?5) < ?3
		       AND COALESCE((SELECT MAX(f.finished_at) FROM baseline_runs f JOIN source_repos o ON o.repo_id = f.repo_id
		                      WHERE o.project_id = sr.project_id AND f.mode = 'restyle' AND f.state = 'failed' AND f.epoch = ?2
		                        AND f.step = ?5), '') < ?4,
		       EXISTS (SELECT 1 FROM baseline_runs o WHERE o.mode = 'restyle' AND o.state NOT IN ('done', 'failed'))
		  FROM source_repos sr JOIN projects p ON p.id = sr.project_id
		 WHERE sr.repo_id = ?1 AND `+activeRow,
		rid, epoch, RestyleTries, stamp(failedBefore), RestyleProjectStep).Scan(&st.RepoDue, &st.ProjectDue, &st.Open)
	if errors.Is(err, sql.ErrNoRows) {
		return RestyleState{}, nil
	}
	return st, err
}

// RestyleDue reports whether the poller queues a restyle of a repository at
// epoch (RestyleState): its page or its project's is due, and no restyle
// is open anywhere, so a bump trickles through the wiki one page at a time
// and never crowds out the notes and the reading.
func (s *Store) RestyleDue(ctx context.Context, key, slug string, epoch int, failedBefore time.Time) (bool, error) {
	st, err := s.RestyleState(ctx, key, slug, epoch, failedBefore)
	return (st.RepoDue || st.ProjectDue) && !st.Open, err
}

// MarkProjectRestyled puts a project's page at epoch: a reading run's
// project step answered under today's prompts, the page written, or found
// as it should be, with the whole of it in the prompt. Outside the step's
// transaction: a failed write costs one restyle call later, never the page.
func (s *Store) MarkProjectRestyled(ctx context.Context, key string, epoch int) error {
	_, err := s.db.ExecContext(ctx, `UPDATE projects SET restyle_epoch = MAX(restyle_epoch, ?) WHERE id = `+projectByName, epoch, key)
	return err
}

// Restyled is what one restyle step writes beside the run's progress: a
// revision when the text changed, else the page laid out anew in place on
// its current revision (ArrangeID, 0 for nothing to lay out).
type Restyled struct {
	Revision  *NewRevision
	ArrangeID int64
	Arranged  []Statement
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
