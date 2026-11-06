package api

import (
	"net/http"
	"slices"
	"strconv"
	"time"

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
	// changed (name, text, files or kind) or dropped against Previous, in
	// the revision's order (Dropped in the previous one's); all of them
	// Added on the first revision.
	Added   []statementRef `json:"added"`
	Changed []statementRef `json:"changed"`
	Dropped []statementRef `json:"dropped"`
	// SummaryChanged says the summary differs from Previous's.
	SummaryChanged bool `json:"summary_changed"`
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
	_, ok, err := d.Store.ProjectExists(ctx, key, d.Sources != nil)
	if err != nil {
		httpx.ServerError(w, r, err, "project")
		return
	}
	if ok {
		var alone bool
		if alone, err = d.single(ctx, key); err != nil {
			httpx.ServerError(w, r, err, "project source")
			return
		}
		ok = !alone
	}
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "unknown project")
		return
	}
	d.revision(w, r, store.ScopeProject, key, "")
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
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil || n < 1 {
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
	current, _, err := d.Store.CurrentPage(ctx, scope, key, slug)
	if err != nil {
		httpx.ServerError(w, r, err, "page")
		return
	}
	var prev store.Page
	if n > 1 {
		if prev, ok, err = d.Store.Revision(ctx, scope, key, slug, n-1); err != nil {
			httpx.ServerError(w, r, err, "previous revision")
			return
		}
		if !ok {
			// A gap in the numbering: nothing to diff against, every
			// statement reads as added.
			prev = store.Page{}
		}
	}
	// The page's current names, as the page routes answer them.
	out := revisionDetailBody{Scope: scope, ProjectKey: page.ProjectKey, RepoSlug: page.RepoSlug, Revision: page.Revision,
		Current: current.Revision, CreatedAt: page.UpdatedAt, Description: *describe(page, true)}
	if n > 1 {
		out.Previous = n - 1
	}
	out.Added, out.Changed, out.Dropped = diffStatements(prev.Statements, page.Statements)
	out.SummaryChanged = n == 1 || prev.Summary != page.Summary
	httpx.WriteJSON(w, http.StatusOK, out)
}

// diffStatements is what cur changed against prev, by id: added (in cur
// but not prev), changed (both, with another name, text, files or kind),
// dropped (in prev but not cur). Added and changed in cur's order,
// dropped in prev's. Never nil: the wire shows lists.
func diffStatements(prev, cur []store.Statement) (added, changed, dropped []statementRef) {
	added, changed, dropped = []statementRef{}, []statementRef{}, []statementRef{}
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
			added = append(added, ref)
		case old.Name != s.Name || old.Text != s.Text || old.Kind != s.Kind || !slices.Equal(old.Files, s.Files):
			changed = append(changed, ref)
		}
	}
	for _, s := range prev {
		if !after[s.ID] {
			dropped = append(dropped, statementRef{ID: s.ID, Kind: s.Kind, Name: s.Name})
		}
	}
	return added, changed, dropped
}
