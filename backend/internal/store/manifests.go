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
// read at by this manifest.Version; false when they never were, or by an
// older one.
func (s *Store) ManifestCommit(ctx context.Context, repoID int64) (string, bool, error) {
	var commit string
	err := s.db.QueryRowContext(ctx, `SELECT "commit" FROM repo_manifests WHERE repo_id = ? AND reader = ?`, repoID,
		manifest.Version).Scan(&commit)
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
		INSERT INTO repo_manifests (repo_id, "commit", provides, requires, reader, read_at) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (repo_id) DO UPDATE SET "commit" = excluded."commit", provides = excluded.provides,
		       requires = excluded.requires, reader = excluded.reader, read_at = excluded.read_at`,
		repoID, commit, provides, requires, manifest.Version, nowStamp())
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
// names for the first of them that names one, as written ("" when none
// does).
type Edge struct {
	From    RepoRef
	To      RepoRef
	Coords  []string
	Version string
}

// Ambiguous is a coordinate more than one source repository provides (a
// fork, a copy): no edge points at any of them, since code cannot tell
// which one a user means. Twice: the providers are one repository cloned
// by two sources (rows from before one clone URL was one row's), which a
// removal of all but one resolves.
type Ambiguous struct {
	Coord     string
	Providers []RepoRef
	Twice     bool
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
// provide gives no edge, and is listed as ambiguous when something
// requires it.
func (s *Store) Dependencies(ctx context.Context) (Graph, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT p.key, r.slug, sr.clone_url, m.provides, m.requires
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
	clones := map[RepoRef]string{}
	for rows.Next() {
		var r repo
		var cloneURL, provides, requires string
		if err := rows.Scan(&r.ref.ProjectKey, &r.ref.Slug, &cloneURL, &provides, &requires); err != nil {
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
		clones[r.ref] = CloneKey(cloneURL)
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
			if e.Version == "" {
				e.Version = d.Version
			}
		}
		sort.Slice(order, func(i, j int) bool { return less(order[i], order[j]) })
		for _, to := range order {
			g.Edges = append(g.Edges, *byTo[to])
		}
	}
	required := map[string]bool{}
	for _, r := range repos {
		for _, d := range r.requires {
			required[d.Coord] = true
		}
	}
	for c, p := range providers {
		// A name two repositories carry that nothing uses (two web parts
		// both called "web") costs no edge: not worth a line.
		if len(p) > 1 && required[c] {
			twice := clones[p[0]] != ""
			for _, o := range p[1:] {
				twice = twice && clones[o] == clones[p[0]]
			}
			g.Ambiguous = append(g.Ambiguous, Ambiguous{Coord: c, Providers: p, Twice: twice})
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

// Library is a source repository others use, with the summary of its
// current description ("" before the first) and the users the question
// was asked for.
type Library struct {
	RepoRef
	Summary string
	UsedBy  []RepoRef
}

// Libraries is what a repository uses (slug set), or what a project's
// repositories use of other projects' (slug ""): the context a step reads
// beside its own code. In the graph's order, each once.
func (s *Store) Libraries(ctx context.Context, key, slug string) ([]Library, error) {
	g, err := s.Dependencies(ctx)
	if err != nil {
		return nil, err
	}
	out := []Library{}
	at := map[RepoRef]int{}
	for _, e := range g.Edges {
		if e.From.ProjectKey != key {
			continue
		}
		if slug != "" && e.From.Slug != slug {
			continue
		}
		if slug == "" && e.To.ProjectKey == key {
			// A project's own repository is no library of it.
			continue
		}
		i, ok := at[e.To]
		if !ok {
			i = len(out)
			at[e.To] = i
			out = append(out, Library{RepoRef: e.To})
		}
		out[i].UsedBy = append(out[i].UsedBy, e.From)
	}
	if len(out) == 0 {
		return out, nil
	}
	refs := make([]RepoRef, len(out))
	for i, l := range out {
		refs[i] = l.RepoRef
	}
	sums, err := s.sourceSummaries(ctx, refs)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Summary = sums[out[i].RepoRef]
	}
	return out, nil
}

// sourceSummaries is the current summaries of the source repositories
// named, in one query, "" before a first description. By the names the
// graph read a moment before: a rename between the two reads leaves one
// summary out of one prompt, the next step reads it again.
func (s *Store) sourceSummaries(ctx context.Context, refs []RepoRef) (map[RepoRef]string, error) {
	pairs := make([][2]string, len(refs))
	for i, r := range refs {
		pairs[i] = [2]string{r.ProjectKey, r.Slug}
	}
	named, err := jsonOf(pairs)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT p.key, r.slug, COALESCE(rv.summary, '')
		  FROM json_each(?) j
		  JOIN projects p ON p.key = j.value ->> 0
		  JOIN repos r ON r.project_id = p.id AND r.slug = j.value ->> 1
		  JOIN source_repos sr ON sr.repo_id = r.id
		  LEFT JOIN pages pg ON pg.scope = 'repository' AND pg.project_id = r.project_id AND pg.repo_id = r.id
		  LEFT JOIN revisions rv ON rv.id = pg.current_revision_id`, named)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[RepoRef]string{}
	for rows.Next() {
		var r RepoRef
		var sum string
		if err := rows.Scan(&r.ProjectKey, &r.Slug, &sum); err != nil {
			return nil, err
		}
		out[r] = sum
	}
	return out, rows.Err()
}
