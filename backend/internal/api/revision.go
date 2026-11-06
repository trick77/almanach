package api

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/trick77/almanach/internal/diagram"
	"github.com/trick77/almanach/internal/store"
	"github.com/trick77/ship/kit/httpx"
)

// A revision's view: one revision of a page as it stood, and what it
// changed against the one before, statement by statement, by id. The
// diff is the API's, computed here from the two stored revisions; the
// sources on each statement say which PR or run did it.

// revisionDetailBody is one revision with its diff against the previous.
type revisionDetailBody struct {
	Scope      string `json:"scope"`
	ProjectKey string `json:"project_key"`
	RepoSlug   string `json:"repo_slug,omitempty"`
	// Revision is this one's number, Current the page's current, Previous
	// the one diffed against (0 for the first).
	Revision    int             `json:"revision"`
	Current     int             `json:"current"`
	Previous    int             `json:"previous"`
	CreatedAt   time.Time       `json:"created_at"`
	Description descriptionBody `json:"description"`
	// Added, Changed and Dropped are the statements this revision added,
	// changed (name, text, files, kind or diagram) or dropped against
	// Previous, in the revision's order (Dropped in the previous one's);
	// all of them Added on the first revision. Rechecked are the ones a
	// baseline run confirmed against the code and changed nothing else of.
	Added     []statementRef `json:"added"`
	Changed   []statementRef `json:"changed"`
	Dropped   []statementRef `json:"dropped"`
	Rechecked []statementRef `json:"rechecked"`
	// SummaryChanged says the summary differs from Previous's.
	SummaryChanged bool `json:"summary_changed"`
	// MapChanged and ShapeChanged say a project page's map, or what the
	// project was judged to be, differ from Previous's: a run writes a
	// revision for either alone. Never on a repository page, whose map
	// is set on its current revision in place.
	MapChanged   bool `json:"map_changed"`
	ShapeChanged bool `json:"shape_changed"`
}

// statementRef names a statement in a diff.
type statementRef struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// projectRevision answers GET /api/projects/{key}/revisions/{n}. A
// project of one has no project page on the wire, no revisions either.
func (d Deps) projectRevision(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	d.reader(r)
	key := r.PathValue("key")
	ok, err := d.projectPaged(ctx, key)
	if err != nil {
		httpx.ServerError(w, r, err, "project")
		return
	}
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "unknown project")
		return
	}
	d.revision(w, r, store.ScopeProject, key, "")
}

// projectPaged reports whether the project has a page on the wire: one
// the routes serve (listed, a source with the sources on) and not one of
// one, whose repository's page is the project's. The same two checks the
// project route makes (ProjectExists, single), which reads the team and
// the Markdown export's fallback between them and so keeps its own copy:
// a change to either gate belongs in both.
func (d Deps) projectPaged(ctx context.Context, key string) (bool, error) {
	_, ok, err := d.Store.ProjectExists(ctx, key, d.Sources != nil)
	if err != nil || !ok {
		return false, err
	}
	alone, err := d.single(ctx, key)
	return !alone, err
}

// repoRevision answers GET /api/projects/{key}/repos/{slug}/revisions/{n}.
func (d Deps) repoRevision(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	d.reader(r)
	key, slug := r.PathValue("key"), r.PathValue("slug")
	ok, err := d.Store.RepoExists(ctx, key, slug, d.Sources != nil)
	if err != nil {
		httpx.ServerError(w, r, err, "repo")
		return
	}
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "unknown repository")
		return
	}
	d.revision(w, r, store.ScopeRepository, key, slug)
}

// revision answers one revision of the page the names find, with its
// diff against the one before; an unknown number is a 404.
func (d Deps) revision(w http.ResponseWriter, r *http.Request, scope, key, slug string) {
	ctx := r.Context()
	// The number as the client routes write it: no sign, no leading zero,
	// so one revision has one URL.
	raw := r.PathValue("n")
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || strconv.Itoa(n) != raw {
		httpx.WriteError(w, http.StatusNotFound, "unknown revision")
		return
	}
	page, ok, err := d.Store.Revision(ctx, scope, key, slug, n)
	if err != nil {
		httpx.ServerError(w, r, err, "revision")
		return
	}
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "unknown revision")
		return
	}
	// The page's current number is the newest revision's: the list's
	// first row, no page read (its maps would be decoded for nothing).
	newest, err := d.Store.Revisions(ctx, scope, key, slug, 1)
	if err != nil {
		httpx.ServerError(w, r, err, "revisions")
		return
	}
	if len(newest) == 0 {
		httpx.WriteError(w, http.StatusNotFound, "unknown revision")
		return
	}
	// The page's current names, as the page routes answer them.
	out := revisionDetailBody{Scope: scope, ProjectKey: page.ProjectKey, RepoSlug: page.RepoSlug, Revision: page.Revision,
		Current: newest[0].Revision, CreatedAt: page.UpdatedAt, Description: *describe(page, true)}
	// Revisions are numbered one past the last and never deleted, so n-1
	// is there; were it not, the view reads as a first one.
	var prev store.Page
	if n > 1 {
		if prev, ok, err = d.Store.Revision(ctx, scope, key, slug, n-1); err != nil {
			httpx.ServerError(w, r, err, "previous revision")
			return
		}
		if ok {
			out.Previous = n - 1
		}
	}
	diff := diffStatements(prev.Statements, page.Statements)
	out.Added, out.Changed, out.Dropped, out.Rechecked = diff.added, diff.changed, diff.dropped, diff.rechecked
	out.SummaryChanged = out.Previous == 0 || prev.Summary != page.Summary
	if scope == store.ScopeProject {
		// The first revision's map is new when it has one; its shape is
		// the first judgment, no change.
		out.MapChanged = !diagram.Equal(prev.Map, page.Map)
		out.ShapeChanged = out.Previous > 0 && store.ShapeMoves(prev.Shape, page.Shape)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// statementDiff is what one revision changed in the statements, by id.
type statementDiff struct {
	added, changed, dropped, rechecked []statementRef
}

// diffStatements is what cur changed against prev, by id: added (in cur
// but not prev), changed (both, with another name, text, files, kind or
// diagram: what the fold writes a revision for), dropped (in prev but not
// cur), rechecked (both, nothing changed but the baseline's check stamp).
// Added, changed and rechecked in cur's order, dropped in prev's. Never
// nil: the wire shows lists.
func diffStatements(prev, cur []store.Statement) statementDiff {
	d := statementDiff{added: []statementRef{}, changed: []statementRef{}, dropped: []statementRef{}, rechecked: []statementRef{}}
	before := make(map[string]store.Statement, len(prev))
	for _, s := range prev {
		before[s.ID] = s
	}
	after := make(map[string]bool, len(cur))
	for _, s := range cur {
		after[s.ID] = true
		ref := statementRef{ID: s.ID, Kind: s.Kind, Name: s.Name}
		old, ok := before[s.ID]
		switch {
		case !ok:
			d.added = append(d.added, ref)
		case old.Name != s.Name || old.Text != s.Text || old.Kind != s.Kind || !slices.Equal(old.Files, s.Files) ||
			!diagram.Equal(old.Diagram, s.Diagram):
			d.changed = append(d.changed, ref)
		case old.Checked != s.Checked:
			d.rechecked = append(d.rechecked, ref)
		}
	}
	for _, s := range prev {
		if !after[s.ID] {
			d.dropped = append(d.dropped, statementRef{ID: s.ID, Kind: s.Kind, Name: s.Name})
		}
	}
	return d
}
