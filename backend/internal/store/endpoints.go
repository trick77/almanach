package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/trick77/almanach/internal/endpoints"
)

// EndpointsCommit is the commit a source repository's interfaces were last
// read at by this endpoints.Version; false when they never were, or by an
// older one.
func (s *Store) EndpointsCommit(ctx context.Context, repoID int64) (string, bool, error) {
	var commit string
	err := s.db.QueryRowContext(ctx, `SELECT "commit" FROM repo_endpoints WHERE repo_id = ? AND reader = ?`, repoID,
		endpoints.Version).Scan(&commit)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return commit, err == nil, err
}

// SaveEndpoints stores what a source repository's code says of its
// interfaces at commit, in place of what it said before. ErrNoRepo when
// the row went meanwhile.
func (s *Store) SaveEndpoints(ctx context.Context, repoID int64, commit string, read endpoints.Read) error {
	if read.APIs == nil {
		read.APIs = []endpoints.API{}
	}
	if read.Endpoints == nil {
		read.Endpoints = []endpoints.Endpoint{}
	}
	apis, err := jsonOf(read.APIs)
	if err != nil {
		return err
	}
	eps, err := jsonOf(read.Endpoints)
	if err != nil {
		return err
	}
	n, err := affected(s.db.ExecContext(ctx, `
		INSERT INTO repo_endpoints (repo_id, "commit", apis, endpoints, reader, read_at)
		SELECT sr.repo_id, ?, ?, ?, ?, ? FROM source_repos sr WHERE sr.repo_id = ? AND `+activeRow+`
		ON CONFLICT (repo_id) DO UPDATE SET "commit" = excluded."commit", apis = excluded.apis,
		       endpoints = excluded.endpoints, reader = excluded.reader, read_at = excluded.read_at`,
		commit, apis, eps, endpoints.Version, nowStamp(), repoID))
	if isForeignKey(err) || err == nil && n == 0 {
		return ErrNoRepo
	}
	return err
}

// Interfaces is what every active source repository's code says of the
// interfaces it offers, read at once: the one source REST and the exports
// read (Query), and the planned MCP server's tools will (Serving, System,
// keyed by EndpointKey), never a page's text. Read reports whether a
// repository's were read at all.
type Interfaces struct {
	read   map[RepoRef]endpoints.Read
	commit map[RepoRef]string
	// id is every active source repository's id, read or not: the keys
	// are built from it, never from a name.
	id map[RepoRef]int64
}

// NewInterfaces is Interfaces over reads already at hand; ids and commits
// may be nil.
func NewInterfaces(read map[RepoRef]endpoints.Read, id map[RepoRef]int64, commit map[RepoRef]string) Interfaces {
	return Interfaces{read: read, id: id, commit: commit}
}

// Interfaces reads every active source repository's interfaces. A
// repository missing from its listing, or deactivated, counts for nothing.
func (s *Store) Interfaces(ctx context.Context) (Interfaces, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT p.key, r.slug, sr.repo_id, COALESCE(e."commit", ''), COALESCE(e.apis, '[]'), COALESCE(e.endpoints, '[]')
		  FROM source_repos sr
		  JOIN repos r ON r.id = sr.repo_id
		  JOIN projects p ON p.id = r.project_id
		  LEFT JOIN repo_endpoints e ON e.repo_id = sr.repo_id
		 WHERE sr.state <> 'missing' AND `+activeRow)
	if err != nil {
		return Interfaces{}, err
	}
	defer func() { _ = rows.Close() }()
	in := Interfaces{read: map[RepoRef]endpoints.Read{}, commit: map[RepoRef]string{}, id: map[RepoRef]int64{}}
	for rows.Next() {
		var ref RepoRef
		var id int64
		var commit, apis, eps string
		if err := rows.Scan(&ref.ProjectKey, &ref.Slug, &id, &commit, &apis, &eps); err != nil {
			return Interfaces{}, err
		}
		in.id[ref] = id
		if commit == "" {
			continue
		}
		var r endpoints.Read
		if err := json.Unmarshal([]byte(apis), &r.APIs); err != nil {
			return Interfaces{}, err
		}
		if err := json.Unmarshal([]byte(eps), &r.Endpoints); err != nil {
			return Interfaces{}, err
		}
		in.read[ref], in.commit[ref] = r, commit
	}
	return in, rows.Err()
}

// Commit is the commit a repository's interfaces were read at, "" when
// they never were.
func (i Interfaces) Commit(ref RepoRef) string { return i.commit[ref] }

// Read reports whether a repository's interfaces were read at all: none
// read is no "none offered".
func (i Interfaces) Read(ref RepoRef) bool {
	_, ok := i.read[ref]
	return ok
}

// Of is what a repository's code says of its interfaces; empty lists when
// it was never read.
func (i Interfaces) Of(ref RepoRef) endpoints.Read {
	r := i.read[ref]
	if r.APIs == nil {
		r.APIs = []endpoints.API{}
	}
	if r.Endpoints == nil {
		r.Endpoints = []endpoints.Endpoint{}
	}
	return r
}

// TopicKind is a Kafka topic's kind beside endpoints' when System puts
// a project's topics among its interfaces.
const TopicKind = "topic"

// RepoAPI is one interface of one repository with its operations: what a
// query answers. Key is stable while the repository and the interface
// are: built from the repository's id, never a name. Commit is the commit
// it was read at, Files the files that say so.
type RepoAPI struct {
	Repo   RepoRef
	Key    string
	Commit string
	endpoints.API
	Endpoints []endpoints.Endpoint
}

// InterfaceFilter narrows a query; a field left "" is no condition. Kind
// and Direction as endpoints names them; API the interface's name, any
// case.
type InterfaceFilter struct {
	Project, Slug, Kind, Direction, API string
}

func (f InterfaceFilter) holds(ref RepoRef, a endpoints.API) bool {
	return (f.Project == "" || f.Project == ref.ProjectKey) && (f.Slug == "" || f.Slug == ref.Slug) &&
		(f.Kind == "" || f.Kind == a.Kind) && (f.Direction == "" || f.Direction == a.Direction) &&
		(f.API == "" || strings.EqualFold(f.API, a.Name))
}

// apiKey is an interface's key: its repository's id, its kind, its name
// and version. The name without the version endpoints adds to tell two
// specs of one title apart, so a second version leaves the first's key as
// it was.
func apiKey(id int64, kind, name, version string) string {
	if version == "" {
		return fmt.Sprintf("%d:%s:%s", id, kind, name)
	}
	return fmt.Sprintf("%d:%s:%s@%s", id, kind, strings.TrimSuffix(name, " "+version), version)
}

// EndpointKey is an endpoint's key within its interface's: the
// interface's, its method and its path, parameters by position.
func EndpointKey(api string, e endpoints.Endpoint) string {
	return api + ":" + endpoints.Key(e.Kind, e.Method, e.Path)
}

// Query is the interfaces the filter holds, each with its operations: by
// repository, then kind and name.
func (i Interfaces) Query(f InterfaceFilter) []RepoAPI {
	refs := make([]RepoRef, 0, len(i.read))
	for ref := range i.read {
		refs = append(refs, ref)
	}
	sort.Slice(refs, func(a, b int) bool { return less(refs[a], refs[b]) })
	out := []RepoAPI{}
	for _, ref := range refs {
		r := i.read[ref]
		for _, a := range r.APIs {
			if !f.holds(ref, a) {
				continue
			}
			ra := RepoAPI{Repo: ref, Key: apiKey(i.id[ref], a.Kind, a.Name, a.Version), Commit: i.commit[ref], API: a,
				Endpoints: []endpoints.Endpoint{}}
			for _, e := range r.Endpoints {
				if e.Kind == a.Kind && e.API == a.Name {
					ra.Endpoints = append(ra.Endpoints, e)
				}
			}
			out = append(out, ra)
		}
	}
	return out
}

// System is a project's interfaces as one list: what its repositories'
// code offers and declares (Query), then the Kafka topics they produce
// to, consume from or name (m), each a TopicKind interface whose
// direction is messaging's. The answer to "which interfaces has the
// system": one query, every kind.
func (i Interfaces) System(m Messages, key string) []RepoAPI {
	out := i.Query(InterfaceFilter{Project: key})
	refs := make([]RepoRef, 0, len(m.read))
	for ref := range m.read {
		if ref.ProjectKey == key {
			refs = append(refs, ref)
		}
	}
	sort.Slice(refs, func(a, b int) bool { return less(refs[a], refs[b]) })
	for _, ref := range refs {
		for _, t := range m.Of(ref).Topics {
			out = append(out, RepoAPI{Repo: ref, Key: apiKey(i.id[ref], TopicKind, t.Name+":"+t.Direction, ""), Commit: m.Commit(ref),
				API: endpoints.API{Kind: TopicKind, Name: t.Name, Direction: t.Direction, Files: t.Files}, Endpoints: []endpoints.Endpoint{}})
		}
	}
	return out
}

// Served is an endpoint a call reaches: the interface it belongs to and
// the endpoint.
type Served struct {
	RepoAPI
	Endpoint endpoints.Endpoint
}

// Serving is the offered endpoints a call reaches: its method (any when
// "") and its concrete path (/orders/42) against every route the filter
// holds, the path as the caller sends it or without the interface's base
// path. More than one is an ambiguous answer, every candidate kept: the
// caller says so, never picks.
func (i Interfaces) Serving(f InterfaceFilter, method, path string) []Served {
	f.Direction = endpoints.Offers
	out := []Served{}
	for _, a := range i.Query(f) {
		for _, e := range a.Endpoints {
			if method != "" && e.Method != endpoints.AnyMethod && !strings.EqualFold(e.Method, method) {
				continue
			}
			// Below the base path at a segment's end only: /api is no
			// prefix of /apiv2/x.
			rel, below := strings.CutPrefix(e.Path, a.BasePath+"/")
			below = below && a.BasePath != ""
			if endpoints.Match(e.Path, path) || below && endpoints.Match(rel, path) || e.Kind != endpoints.HTTP && e.Path == path {
				out = append(out, Served{RepoAPI: a, Endpoint: e})
			}
		}
	}
	return out
}
