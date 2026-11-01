package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/trick77/almanach/internal/store"
	"github.com/trick77/ship/kit/httpx"
)

// The dependency graph: which source repository uses which, joined by the
// store from the manifests the poller read (store.Dependencies), never
// stored and never a model's. With the sources off there are no source
// repositories, so no graph: every list is empty.

// depBody is a repository on one end of an edge; Version is what the
// user names, as written ("" when its manifest names none).
type depBody struct {
	Project string `json:"project"`
	Slug    string `json:"slug"`
	Version string `json:"version"`
}

// useBody is a repository another one depends on: the version it names,
// and the other users with theirs.
type useBody struct {
	depBody
	Also []depBody `json:"also"`
}

type repoRefBody struct {
	Project string `json:"project"`
	Slug    string `json:"slug"`
}

type edgeBody struct {
	From    repoRefBody `json:"from"`
	To      repoRefBody `json:"to"`
	Version string      `json:"version"`
	Coords  []string    `json:"coords"`
}

type ambiguousBody struct {
	Coord     string        `json:"coord"`
	Providers []repoRefBody `json:"providers"`
	// ClonedTwice: the providers are one repository two sources clone.
	ClonedTwice bool `json:"cloned_twice"`
}

type dependenciesBody struct {
	Repos     []repoRefBody   `json:"repos"`
	Edges     []edgeBody      `json:"edges"`
	Ambiguous []ambiguousBody `json:"ambiguous"`
}

// graph is the dependency graph, empty with the sources off.
func (d Deps) graph(ctx context.Context) (store.Graph, error) {
	if d.Sources == nil {
		return store.Graph{Repos: []store.RepoRef{}, Edges: []store.Edge{}, Ambiguous: []store.Ambiguous{}}, nil
	}
	return d.Store.Dependencies(ctx)
}

func refBody(r store.RepoRef) repoRefBody { return repoRefBody{Project: r.ProjectKey, Slug: r.Slug} }

// dependencies serves GET /api/dependencies.
func (d Deps) dependencies(w http.ResponseWriter, r *http.Request) {
	d.reader(r)
	g, err := d.graph(r.Context())
	if err != nil {
		httpx.ServerError(w, r, err, "dependencies")
		return
	}
	out := dependenciesBody{Repos: make([]repoRefBody, 0, len(g.Repos)), Edges: make([]edgeBody, 0, len(g.Edges)),
		Ambiguous: make([]ambiguousBody, 0, len(g.Ambiguous))}
	for _, rp := range g.Repos {
		out.Repos = append(out.Repos, refBody(rp))
	}
	for _, e := range g.Edges {
		out.Edges = append(out.Edges, edgeBody{From: refBody(e.From), To: refBody(e.To), Version: e.Version, Coords: e.Coords})
	}
	for _, a := range g.Ambiguous {
		p := make([]repoRefBody, 0, len(a.Providers))
		for _, rp := range a.Providers {
			p = append(p, refBody(rp))
		}
		out.Ambiguous = append(out.Ambiguous, ambiguousBody{Coord: a.Coord, Providers: p, ClonedTwice: a.Twice})
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// usesOf is what repo uses, each with its other users, and what
// uses repo.
func usesOf(g store.Graph, repo store.RepoRef) (uses []useBody, usedBy []depBody) {
	uses, usedBy = []useBody{}, []depBody{}
	for _, e := range g.Edges {
		switch {
		case e.From == repo:
			u := useBody{depBody: depBody{Project: e.To.ProjectKey, Slug: e.To.Slug, Version: e.Version}, Also: []depBody{}}
			for _, o := range g.Edges {
				if o.To == e.To && o.From != repo {
					u.Also = append(u.Also, depBody{Project: o.From.ProjectKey, Slug: o.From.Slug, Version: o.Version})
				}
			}
			uses = append(uses, u)
		case e.To == repo:
			usedBy = append(usedBy, depBody{Project: e.From.ProjectKey, Slug: e.From.Slug, Version: e.Version})
		}
	}
	return uses, usedBy
}

// markdownDeps is the repository's edges as the export's last section, ""
// when it has none.
func markdownDeps(uses []useBody, usedBy []depBody) string {
	if len(uses) == 0 && len(usedBy) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n## Abhängigkeiten\n\n")
	line := func(d depBody) {
		fmt.Fprintf(&b, "- `%s/%s`", d.Project, d.Slug)
		// A version is a manifest's text: one line, in a code span with no
		// backtick to close it, so nothing in it renders as Markdown.
		if v := strings.Join(strings.Fields(strings.ReplaceAll(d.Version, "`", "'")), " "); v != "" {
			fmt.Fprintf(&b, " (`%s`)", v)
		}
		b.WriteString("\n")
	}
	if len(uses) > 0 {
		b.WriteString("Verwendet:\n\n")
		for _, u := range uses {
			line(u.depBody)
		}
		b.WriteString("\n")
	}
	if len(usedBy) > 0 {
		b.WriteString("Verwendet von:\n\n")
		for _, u := range usedBy {
			line(u)
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

// sharedBody is a repository of another project the project's
// repositories use, and which of them do.
type sharedBody struct {
	Project string        `json:"project"`
	Slug    string        `json:"slug"`
	UsedBy  []repoRefBody `json:"used_by"`
}

// shared is the project's shared repositories for its map, none with the sources
// off. The caller asks only with a map drawn (a standalone project has
// none).
func (d Deps) shared(ctx context.Context, key string) ([]sharedBody, error) {
	out := []sharedBody{}
	if d.Sources == nil {
		return out, nil
	}
	libs, err := d.Store.Shared(ctx, key, "")
	if err != nil {
		return nil, err
	}
	for _, l := range libs {
		b := sharedBody{Project: l.ProjectKey, Slug: l.Slug, UsedBy: make([]repoRefBody, 0, len(l.UsedBy))}
		for _, u := range l.UsedBy {
			b.UsedBy = append(b.UsedBy, refBody(u))
		}
		out = append(out, b)
	}
	return out, nil
}
