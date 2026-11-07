package store

import (
	"context"
	"database/sql"
	"errors"
)

// Deactivation: a source repository the operator takes out without removing
// its row, so a Bitbucket project's listing never adds it back. Instant
// where it is the store's (no source any more, its queued runs and
// manifests gone); the project page is the worker's, by a departure run,
// and departure_due says until when the page may still name it.

// DropQueuedRuns is a deactivation's first step, before its clone goes:
// the repository's queued runs are dropped, so none starts on the clone
// being removed (a worker that read one already finds it gone when it
// saves). ErrRunOpen while a run of it is past queued: that run reads the
// clone, nothing is dropped.
func (s *Store) DropQueuedRuns(ctx context.Context, key, slug string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		rid, ok, err := repoID(ctx, tx, key, slug)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNoRepo
		}
		return dropQueued(ctx, tx, rid)
	})
}

// dropQueued is ErrRunOpen when a run of the repository is past queued,
// else its queued runs deleted.
func dropQueued(ctx context.Context, tx *sql.Tx, rid int64) error {
	var started bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM baseline_runs WHERE repo_id = ? AND state NOT IN ('queued', 'done', 'failed'))`, rid).Scan(&started); err != nil {
		return err
	}
	if started {
		return ErrRunOpen
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM baseline_runs WHERE repo_id = ? AND state = 'queued'`, rid)
	return err
}

// DeactivateSourceRepo takes a source repository out: deactivated now, its
// head forgotten (the caller removes the clone), pending again for when it
// comes back (missing stays missing), its queued runs and its manifests
// gone. When the project has a page, the departure is due, and a departure
// run is queued at the head it had (else the commit last documented; with
// neither, none: the next project step takes it out). ErrNoRepo when the
// source has no such repository, ErrRunOpen while a run of it is past
// queued: that run reads the clone. Deactivating a deactivated one changes
// nothing. Reports whether a departure run was queued.
func (s *Store) DeactivateSourceRepo(ctx context.Context, key, slug string) (bool, error) {
	var queued bool
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		rid, ok, err := repoID(ctx, tx, key, slug)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNoRepo
		}
		var pid int64
		var head string
		var off bool
		err = tx.QueryRowContext(ctx, `SELECT project_id, head_commit, deactivated_at IS NOT NULL FROM source_repos WHERE repo_id = ?`,
			rid).Scan(&pid, &head, &off)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNoRepo
		}
		if err != nil || off {
			return err
		}
		if err := dropQueued(ctx, tx, rid); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM repo_manifests WHERE repo_id = ?`, rid); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM repo_topics WHERE repo_id = ?`, rid); err != nil {
			return err
		}
		var page bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pages WHERE scope = 'project' AND project_id = ? AND repo_id = 0)`,
			pid).Scan(&page); err != nil {
			return err
		}
		now := nowStamp()
		if _, err := tx.ExecContext(ctx, `
			UPDATE source_repos SET deactivated_at = ?, head_commit = '', last_error = '', departure_due = ?,
			       state = CASE WHEN state = 'missing' THEN state ELSE 'pending' END
			 WHERE repo_id = ?`, now, page, rid); err != nil {
			return err
		}
		if !page {
			return nil
		}
		if head == "" {
			// Never cloned, or deactivated while pending: the commit last
			// documented names the repository's history.
			if err := tx.QueryRowContext(ctx, `
				SELECT COALESCE((SELECT "commit" FROM baseline_runs WHERE repo_id = ? AND state = 'done' AND trigger <> 'departure'
				                  ORDER BY id DESC LIMIT 1), '')`, rid).Scan(&head); err != nil || head == "" {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO baseline_runs (repo_id, mode, trigger, base_commit, "commit", state, plan, queued_at, reason)
			VALUES (?, 'full', 'departure', '', ?, 'queued', '{"departure":true}', ?, '{"kind":"`+ReasonDeparture+`"}')`, rid, head, now); err != nil {
			return err
		}
		queued = true
		return nil
	})
	return queued, err
}

// ActivateSourceRepo brings a deactivated source repository back: a source
// again, pending (missing stays missing) so the next cycle clones it, no
// departure due, its queued departure run dropped (the repository stays on
// the project page). ErrNoRepo when the source has no such repository,
// ErrRunOpen while its departure run is past queued. Activating an active
// one changes nothing.
func (s *Store) ActivateSourceRepo(ctx context.Context, key, slug string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		rid, ok, err := repoID(ctx, tx, key, slug)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNoRepo
		}
		var off bool
		err = tx.QueryRowContext(ctx, `SELECT deactivated_at IS NOT NULL FROM source_repos WHERE repo_id = ?`, rid).Scan(&off)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNoRepo
		}
		if err != nil || !off {
			return err
		}
		if err := dropQueued(ctx, tx, rid); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			UPDATE source_repos SET deactivated_at = NULL, reactivated_at = ?, departure_due = 0,
			       state = CASE WHEN state = 'missing' THEN state ELSE 'pending' END
			 WHERE repo_id = ?`, nowStamp(), rid)
		return err
	})
}

// DepartedRepos is a project's deactivated source repositories, by current
// slug: what the project page must no longer hold.
func (s *Store) DepartedRepos(ctx context.Context, key string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT r.slug FROM source_repos sr JOIN repos r ON r.id = sr.repo_id
		 WHERE sr.deactivated_at IS NOT NULL AND sr.project_id = `+projectByName+` ORDER BY r.slug`, key)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []string{}
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			return nil, err
		}
		out = append(out, slug)
	}
	return out, rows.Err()
}

// DepartureDue reports whether the project page may still name a
// deactivated repository: no call that had it in its prompt answered yet
// (its departure run's failed, or is still to come). The project step then
// folds the page whatever a reconcile would skip.
func (s *Store) DepartureDue(ctx context.Context, key string) (bool, error) {
	var due bool
	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM source_repos sr
		 WHERE sr.project_id = `+projectByName+` AND sr.deactivated_at IS NOT NULL AND sr.departure_due = 1)`, key).Scan(&due)
	return due, err
}

// SettleDepartures records that a call with these deactivated repositories
// in its prompt answered: the page is taken to name them no more. Only the
// ones named: one deactivated during the call is still due.
func (s *Store) SettleDepartures(ctx context.Context, key string, slugs []string) error {
	named, err := jsonOf(slugs)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		UPDATE source_repos SET departure_due = 0
		 WHERE deactivated_at IS NOT NULL AND project_id = `+projectByName+`
		   AND repo_id IN (SELECT r.id FROM repos r WHERE r.slug IN (SELECT value FROM json_each(?)))`, key, named)
	return err
}
