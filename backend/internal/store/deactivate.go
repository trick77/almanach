package store

import (
	"context"
	"database/sql"
	"errors"
)

// Deactivation: a source repository the operator takes out without removing
// its row, so a Bitbucket project's listing never adds it back. Instant
// where it is the store's (no source any more, its queued runs and
// manifests gone); the project page is the worker's, by a departure run.

// DeactivateSourceRepo takes a source repository out: deactivated now, its
// head forgotten (the caller removes the clone), pending again for when it
// comes back (missing stays missing), its queued runs and its manifests
// gone, and, when the project has a page, a departure run queued at the
// head it had. ErrNoRepo when the source has no such repository,
// ErrRunOpen while a run of it is past queued: that run reads the clone.
// Deactivating a deactivated one changes nothing. Reports whether a
// departure run was queued.
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
		if started, err := runStarted(ctx, tx, rid); err != nil {
			return err
		} else if started {
			return ErrRunOpen
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM baseline_runs WHERE repo_id = ? AND state = 'queued'`, rid); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM repo_manifests WHERE repo_id = ?`, rid); err != nil {
			return err
		}
		now := nowStamp()
		if _, err := tx.ExecContext(ctx, `
			UPDATE source_repos SET deactivated_at = ?, head_commit = '', last_error = '',
			       state = CASE WHEN state = 'missing' THEN state ELSE 'pending' END
			 WHERE repo_id = ?`, now, rid); err != nil {
			return err
		}
		var page bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pages WHERE scope = 'project' AND project_id = ? AND repo_id = 0)`,
			pid).Scan(&page); err != nil || !page {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO baseline_runs (repo_id, mode, trigger, base_commit, "commit", state, plan, queued_at)
			VALUES (?, 'full', 'departure', '', ?, 'queued', '{"departure":true}', ?)`, rid, head, now); err != nil {
			return err
		}
		queued = true
		return nil
	})
	return queued, err
}

// ActivateSourceRepo brings a deactivated source repository back: a source
// again, pending (missing stays missing) so the next cycle clones it, its
// queued departure run dropped (the repository stays on the project page).
// ErrNoRepo when the source has no such repository, ErrRunOpen while its
// departure run is past queued. Activating an active one changes nothing.
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
		if started, err := runStarted(ctx, tx, rid); err != nil {
			return err
		} else if started {
			return ErrRunOpen
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM baseline_runs WHERE repo_id = ? AND state = 'queued'`, rid); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			UPDATE source_repos SET deactivated_at = NULL, reactivated_at = ?,
			       state = CASE WHEN state = 'missing' THEN state ELSE 'pending' END
			 WHERE repo_id = ?`, nowStamp(), rid)
		return err
	})
}

// runStarted reports whether a run of the repository is open and past
// queued.
func runStarted(ctx context.Context, tx *sql.Tx, rid int64) (bool, error) {
	var started bool
	err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM baseline_runs WHERE repo_id = ? AND state NOT IN ('queued', 'done', 'failed'))`, rid).Scan(&started)
	return started, err
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

// PRRepos is the repository, by current slug, each pull request of the
// project named was noted for; a key no note of the project has is left
// out.
func (s *Store) PRRepos(ctx context.Context, key string, prKeys []string) (map[string]string, error) {
	out := map[string]string{}
	if len(prKeys) == 0 {
		return out, nil
	}
	named, err := jsonOf(prKeys)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT n.pr_key, r.slug FROM notes n JOIN repos r ON r.id = n.repo_id
		 WHERE r.project_id = `+projectByName+` AND n.pr_key IN (SELECT value FROM json_each(?))`, key, named)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var pr, slug string
		if err := rows.Scan(&pr, &slug); err != nil {
			return nil, err
		}
		out[pr] = slug
	}
	return out, rows.Err()
}
