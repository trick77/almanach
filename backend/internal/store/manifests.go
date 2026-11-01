package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"

	"github.com/trick77/almanach/internal/manifest"
)

// ManifestCommit is the commit a source repository's manifests were last
// read at; false when they never were.
func (s *Store) ManifestCommit(ctx context.Context, repoID int64) (string, bool, error) {
	var commit string
	err := s.db.QueryRowContext(ctx, `SELECT "commit" FROM repo_manifests WHERE repo_id = ?`, repoID).Scan(&commit)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return commit, err == nil, err
}

// SaveManifests stores what a source repository's manifests say at commit,
// in place of what they said before. ErrNoRepo when the row went meanwhile
// (removed while its manifests were read).
func (s *Store) SaveManifests(ctx context.Context, repoID int64, commit string, read manifest.Read) error {
	if read.Provides == nil {
		read.Provides = []string{}
	}
	if read.Requires == nil {
		read.Requires = []manifest.Dep{}
	}
	provides, err := jsonOf(read.Provides)
	if err != nil {
		return err
	}
	requires, err := jsonOf(read.Requires)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO repo_manifests (repo_id, "commit", provides, requires, read_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (repo_id) DO UPDATE SET "commit" = excluded."commit", provides = excluded.provides,
		       requires = excluded.requires, read_at = excluded.read_at`,
		repoID, commit, provides, requires, nowStamp())
	if isForeignKey(err) {
		return ErrNoRepo
	}
	return err
}

// RepoRef names a source repository in the dependency graph.
type RepoRef struct {
	ProjectKey string
	Slug       string
}

// Edge is one repository using another: From requires coordinates To
// provides. Coords are those coordinates, sorted; Version is what From
// names for the first of them, as written ("" when none).
type Edge struct {
	From    RepoRef
	To      RepoRef
	Coords  []string
	Version string
}

// Ambiguous is a coordinate more than one source repository provides (a
// fork, a copy): no edge points at any of them, since code cannot tell
// which one a user means.
type Ambiguous struct {
	Coord     string
	Providers []RepoRef
}

// Graph is the dependency graph over the source repositories. Repos are
// those with manifests read, sorted; Edges sorted by From, then To.
type Graph struct {
	Repos     []RepoRef
	Edges     []Edge
	Ambiguous []Ambiguous
}

// Dependencies joins what every source repository requires against what
// the others provide, at read time: nothing of it is stored. A repository
// gone missing from its listing counts for nothing; a repository's use of
// itself (a monorepo's modules) is no edge; a coordinate two repositories
// provide gives no edge and is listed as ambiguous.
func (s *Store) Dependencies(ctx context.Context) (Graph, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT p.key, r.slug, m.provides, m.requires
		  FROM repo_manifests m
		  JOIN source_repos sr ON sr.repo_id = m.repo_id
		  JOIN repos r ON r.id = m.repo_id
		  JOIN projects p ON p.id = r.project_id
		 WHERE sr.state <> 'missing'
		 ORDER BY p.key, r.slug`)
	if err != nil {
		return Graph{}, err
	}
	type repo struct {
		ref      RepoRef
		requires []manifest.Dep
	}
	var repos []repo
	providers := map[string][]RepoRef{}
	for rows.Next() {
		var r repo
		var provides, requires string
		if err := rows.Scan(&r.ref.ProjectKey, &r.ref.Slug, &provides, &requires); err != nil {
			_ = rows.Close()
			return Graph{}, err
		}
		var coords []string
		if err := json.Unmarshal([]byte(provides), &coords); err != nil {
			_ = rows.Close()
			return Graph{}, err
		}
		if err := json.Unmarshal([]byte(requires), &r.requires); err != nil {
			_ = rows.Close()
			return Graph{}, err
		}
		for _, c := range coords {
			providers[c] = append(providers[c], r.ref)
		}
		repos = append(repos, r)
	}
	if err := rows.Close(); err != nil {
		return Graph{}, err
	}
	if err := rows.Err(); err != nil {
		return Graph{}, err
	}
	g := Graph{Repos: []RepoRef{}, Edges: []Edge{}, Ambiguous: []Ambiguous{}}
	for _, r := range repos {
		g.Repos = append(g.Repos, r.ref)
		// requires is sorted by coordinate (manifest.Parse), so the first
		// coordinate of an edge, and its version, is the same every read.
		byTo := map[RepoRef]*Edge{}
		var order []RepoRef
		for _, d := range r.requires {
			p := providers[d.Coord]
			if len(p) != 1 || p[0] == r.ref {
				continue
			}
			e, ok := byTo[p[0]]
			if !ok {
				e = &Edge{From: r.ref, To: p[0], Version: d.Version}
				byTo[p[0]] = e
				order = append(order, p[0])
			}
			e.Coords = append(e.Coords, d.Coord)
		}
		sort.Slice(order, func(i, j int) bool { return less(order[i], order[j]) })
		for _, to := range order {
			g.Edges = append(g.Edges, *byTo[to])
		}
	}
	for c, p := range providers {
		if len(p) > 1 {
			g.Ambiguous = append(g.Ambiguous, Ambiguous{Coord: c, Providers: p})
		}
	}
	sort.Slice(g.Ambiguous, func(i, j int) bool { return g.Ambiguous[i].Coord < g.Ambiguous[j].Coord })
	return g, nil
}

func less(a, b RepoRef) bool {
	if a.ProjectKey != b.ProjectKey {
		return a.ProjectKey < b.ProjectKey
	}
	return a.Slug < b.Slug
}
