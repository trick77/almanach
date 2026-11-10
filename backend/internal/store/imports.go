package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/trick77/almanach/internal/imports"
	"github.com/trick77/almanach/internal/manifest"
)

// ImportsCommit is the commit a source repository's imports were last read
// at by this imports.Version; false when they never were, or by an older
// one.
func (s *Store) ImportsCommit(ctx context.Context, repoID int64) (string, bool, error) {
	var commit string
	err := s.db.QueryRowContext(ctx, `SELECT "commit" FROM repo_imports WHERE repo_id = ? AND reader = ?`, repoID,
		imports.Version).Scan(&commit)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return commit, err == nil, err
}

// SaveImports stores which packages inside a source repository import
// which at commit, in place of what it said before. ErrNoRepo when the row
// went meanwhile.
func (s *Store) SaveImports(ctx context.Context, repoID int64, commit string, read imports.Read) error {
	if read.Units == nil {
		read.Units = []imports.Unit{}
	}
	if read.Edges == nil {
		read.Edges = []imports.Edge{}
	}
	units, err := jsonOf(read.Units)
	if err != nil {
		return err
	}
	edges, err := jsonOf(read.Edges)
	if err != nil {
		return err
	}
	n, err := affected(s.db.ExecContext(ctx, `
		INSERT INTO repo_imports (repo_id, "commit", units, edges, reader, read_at)
		SELECT sr.repo_id, ?, ?, ?, ?, ? FROM source_repos sr WHERE sr.repo_id = ? AND `+activeRow+`
		ON CONFLICT (repo_id) DO UPDATE SET "commit" = excluded."commit", units = excluded.units,
		       edges = excluded.edges, reader = excluded.reader, read_at = excluded.read_at`,
		commit, units, edges, imports.Version, nowStamp(), repoID))
	if isForeignKey(err) || err == nil && n == 0 {
		return ErrNoRepo
	}
	return err
}

// RepoImports is which packages inside a source repository import which,
// as last read; none for a repository never read.
func (s *Store) RepoImports(ctx context.Context, key, slug string) (imports.Read, error) {
	_, imp, err := s.RepoParts(ctx, key, slug)
	return imp, err
}

func (s *Store) importsOf(ctx context.Context, repoID int64) (imports.Read, error) {
	out := imports.Read{Units: []imports.Unit{}, Edges: []imports.Edge{}}
	var units, edges string
	err := s.db.QueryRowContext(ctx, `SELECT units, edges FROM repo_imports WHERE repo_id = ?`, repoID).Scan(&units, &edges)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal([]byte(units), &out.Units); err != nil {
		return out, err
	}
	if err := json.Unmarshal([]byte(edges), &out.Edges); err != nil {
		return out, err
	}
	return out, nil
}

// RepoParts is RepoUnits and RepoImports at once, the repository resolved
// once: what its page shows of the parts inside it.
func (s *Store) RepoParts(ctx context.Context, key, slug string) (manifest.Inner, imports.Read, error) {
	in := manifest.Inner{Units: []manifest.Unit{}, Edges: []manifest.InnerEdge{}}
	imp := imports.Read{Units: []imports.Unit{}, Edges: []imports.Edge{}}
	_, repoID, ok, err := s.RepoIDs(ctx, key, slug)
	if err != nil || !ok {
		return in, imp, err
	}
	if in, err = s.unitsOf(ctx, repoID); err != nil {
		return in, imp, err
	}
	imp, err = s.importsOf(ctx, repoID)
	return in, imp, err
}
