package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// A project and a repository are ids; a key and a slug are names. Every
// name ever used maps to its id (project_names, repo_names), so a caller
// holding an old name still reaches the same row. A name is never given to
// another project or repository, in any case: only the owner may change
// the case of its own name (user, 2026-11-01).

// ErrNameTaken is a rename to a name another project or repository has
// had, in any case.
var ErrNameTaken = errors.New("name used by another")

// ErrRunOpen is a new clone URL for a repository while a baseline run of it
// is open: the re-clone would pull the folder from under the run.
var ErrRunOpen = errors.New("a baseline run is open")

// ErrNoProject is a project key no project ever had.
var ErrNoProject = errors.New("no such project")

type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type querier interface {
	rowQuerier
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// projectID resolves any key the project ever had; ok false when none.
func projectID(ctx context.Context, q rowQuerier, key string) (int64, bool, error) {
	var id int64
	err := q.QueryRowContext(ctx, `SELECT project_id FROM project_names WHERE key = ?`, key).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return id, err == nil, err
}

// repoID resolves any key of the project and any slug the repository ever
// had; ok false when none (an empty slug names no repository).
func repoID(ctx context.Context, q rowQuerier, key, slug string) (int64, bool, error) {
	if slug == "" {
		return 0, false, nil
	}
	_, rid, ok, err := scopeIDs(ctx, q, key, slug)
	return rid, ok, err
}

// ensureProject is the project a key names, registered when no project
// ever had it. A key another project had in another case is that project:
// a name in any case belongs to one project, so the new spelling becomes a
// name of it instead of a second project.
func ensureProject(ctx context.Context, q querier, key string) (int64, error) {
	id, ok, err := projectID(ctx, q, key)
	if err != nil || ok {
		return id, err
	}
	rows, err := q.QueryContext(ctx, `SELECT key, project_id FROM project_names`)
	if err != nil {
		return 0, err
	}
	if id, ok, err = ownerInAnyCase(rows, key); err != nil {
		return 0, err
	}
	if !ok {
		if err := q.QueryRowContext(ctx, `INSERT INTO projects (key) VALUES (?) RETURNING id`, key).Scan(&id); err != nil {
			return 0, err
		}
	}
	_, err = q.ExecContext(ctx, `INSERT INTO project_names (key, project_id) VALUES (?, ?)`, key, id)
	return id, err
}

// ensureRepo is the repository a slug names in the project, registered
// when no repository of the project ever had it; a slug another repository
// had in another case is that repository, as for projects.
func ensureRepo(ctx context.Context, q querier, projectID int64, slug string) (int64, error) {
	var id int64
	err := q.QueryRowContext(ctx, `SELECT repo_id FROM repo_names WHERE project_id = ? AND slug = ?`, projectID, slug).Scan(&id)
	if err == nil || !errors.Is(err, sql.ErrNoRows) {
		return id, err
	}
	rows, err := q.QueryContext(ctx, `SELECT slug, repo_id FROM repo_names WHERE project_id = ?`, projectID)
	if err != nil {
		return 0, err
	}
	id, ok, err := ownerInAnyCase(rows, slug)
	if err != nil {
		return 0, err
	}
	if !ok {
		if err := q.QueryRowContext(ctx, `INSERT INTO repos (project_id, slug) VALUES (?, ?) RETURNING id`, projectID, slug).Scan(&id); err != nil {
			return 0, err
		}
	}
	_, err = q.ExecContext(ctx, `INSERT INTO repo_names (project_id, slug, repo_id) VALUES (?, ?, ?)`, projectID, slug, id)
	return id, err
}

// ownerInAnyCase is the owner of name in a (name, owner) list, matched in
// any case, folded in Go (SQLite's lower() folds ASCII only).
func ownerInAnyCase(rows *sql.Rows, name string) (int64, bool, error) {
	owners, err := ownersInAnyCase(rows, name)
	if err != nil || len(owners) == 0 {
		return 0, false, err
	}
	return owners[0], true, nil
}

// ownersInAnyCase is every owner of name in a (name, owner) list, matched
// in any case, in list order. The one case-folding scan: SQLite's lower()
// folds ASCII only, Go folds Unicode.
func ownersInAnyCase(rows *sql.Rows, name string) ([]int64, error) {
	defer func() { _ = rows.Close() }()
	var owners []int64
	for rows.Next() {
		var known string
		var owner int64
		if err := rows.Scan(&known, &owner); err != nil {
			return nil, err
		}
		if strings.EqualFold(known, name) {
			owners = append(owners, owner)
		}
	}
	return owners, rows.Err()
}

// RepoIDs resolves a repository by any of its names to its project's and
// its own id; ok false when no repository ever had the name.
func (s *Store) RepoIDs(ctx context.Context, key, slug string) (projectID, repoID int64, ok bool, err error) {
	if slug == "" {
		return 0, 0, false, nil
	}
	return scopeIDs(ctx, s.db, key, slug)
}

// ProjectIDOf resolves a project by any of its keys; ok false when none.
func (s *Store) ProjectIDOf(ctx context.Context, key string) (int64, bool, error) {
	return projectID(ctx, s.db, key)
}

// ProjectKeyOf is the current key of the project any key names; ok false
// when none.
func (s *Store) ProjectKeyOf(ctx context.Context, key string) (string, bool, error) {
	var current string
	err := s.db.QueryRowContext(ctx, `
		SELECT p.key FROM project_names n JOIN projects p ON p.id = n.project_id WHERE n.key = ?`, key).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return current, err == nil, err
}

// RepoNamesOf is the current key and slug of the repository any key and
// slug name; ok false when none.
func (s *Store) RepoNamesOf(ctx context.Context, key, slug string) (string, string, bool, error) {
	var curKey, curSlug string
	err := s.db.QueryRowContext(ctx, `
		SELECT p.key, r.slug
		  FROM repo_names rn JOIN project_names pn ON pn.project_id = rn.project_id
		  JOIN repos r ON r.id = rn.repo_id JOIN projects p ON p.id = r.project_id
		 WHERE pn.key = ? AND rn.slug = ?`, key, slug).Scan(&curKey, &curSlug)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	return curKey, curSlug, err == nil, err
}

// takenByOther reports whether name, in any case, is a name of a row other
// than self in the given name list. The case is folded in Go: SQLite's
// lower() folds ASCII only.
func takenByOther(rows *sql.Rows, name string, self int64) (bool, error) {
	owners, err := ownersInAnyCase(rows, name)
	if err != nil {
		return false, err
	}
	for _, owner := range owners {
		if owner != self {
			return true, nil
		}
	}
	return false, nil
}

// RenameProject gives the project any key names the key to. The old key
// stays a name of the project. Refused with ErrNameTaken when another
// project ever had to, in any case; ErrNoProject when no project ever had
// key. Returns the project's id.
func (s *Store) RenameProject(ctx context.Context, key, to string) (int64, error) {
	var id int64
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var ok bool
		var err error
		if id, ok, err = projectID(ctx, tx, key); err != nil {
			return err
		} else if !ok {
			return ErrNoProject
		}
		rows, err := tx.QueryContext(ctx, `SELECT key, project_id FROM project_names`)
		if err != nil {
			return err
		}
		if taken, err := takenByOther(rows, to, id); err != nil {
			return err
		} else if taken {
			return ErrNameTaken
		}
		if _, err := tx.ExecContext(ctx, `UPDATE projects SET key = ? WHERE id = ?`, to, id); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO project_names (key, project_id) VALUES (?, ?) ON CONFLICT (key) DO NOTHING`, to, id)
		return err
	})
	return id, err
}

// RenameRepo gives a repository a new slug and, when cloneURL is not
// empty, a new clone URL, which queues a sync. The old slug stays a name of
// the repository. Refused with ErrNameTaken when another repository of the
// project ever had the slug, in any case; ErrRunOpen for a new clone URL
// while a baseline run of it is open; ErrNoRepo when the source has no such
// repository.
func (s *Store) RenameRepo(ctx context.Context, key, slug, to, cloneURL string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		return renameRepo(ctx, tx, key, slug, to, cloneURL)
	})
}

func renameRepo(ctx context.Context, tx *sql.Tx, key, slug, to, cloneURL string) error {
	rid, ok, err := repoID(ctx, tx, key, slug)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNoRepo
	}
	var pid int64
	var current string
	err = tx.QueryRowContext(ctx, `SELECT project_id, clone_url FROM source_repos WHERE repo_id = ?`, rid).Scan(&pid, &current)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNoRepo
	}
	if err != nil {
		return err
	}
	// A new slug touches no run: runs, pages and the clone folder are keyed
	// by id. A new clone URL re-clones the folder a run may be reading.
	if cloneURL != "" && cloneURL != current {
		var open bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM baseline_runs WHERE repo_id = ? AND state NOT IN ('done', 'failed'))`,
			rid).Scan(&open); err != nil {
			return err
		}
		if open {
			return ErrRunOpen
		}
	}
	return renameRepoTo(ctx, tx, pid, rid, to, cloneURL)
}

// renameRepoTo is the rename itself: the name check, the slug, the name
// table and, with a new clone URL, a sync.
func renameRepoTo(ctx context.Context, tx *sql.Tx, pid, rid int64, to, cloneURL string) error {
	rows, err := tx.QueryContext(ctx, `SELECT slug, repo_id FROM repo_names WHERE project_id = ?`, pid)
	if err != nil {
		return err
	}
	if taken, err := takenByOther(rows, to, rid); err != nil {
		return err
	} else if taken {
		return ErrNameTaken
	}
	if _, err := tx.ExecContext(ctx, `UPDATE repos SET slug = ? WHERE id = ?`, to, rid); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO repo_names (project_id, slug, repo_id) VALUES (?, ?, ?) ON CONFLICT DO NOTHING`,
		pid, to, rid); err != nil {
		return err
	}
	if cloneURL == "" {
		return nil
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE source_repos SET clone_url = ?, state = CASE WHEN state = 'missing' THEN state ELSE 'pending' END
		 WHERE repo_id = ? AND clone_url <> ?`, cloneURL, rid, cloneURL)
	return err
}
