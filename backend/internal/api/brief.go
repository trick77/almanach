package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/trick77/almanach/internal/consolidate"
	"github.com/trick77/almanach/internal/store"
	"github.com/trick77/ship/kit/httpx"
)

// A repository's brief, its .agents/PROJECT.md: asked for on the source
// page (a write, so a sources route: the wiki stays read-only), written and
// pushed by the worker. The source page shows its state on the repository's
// row; the text is read on its own route, for the copy a refused push
// leaves the reader.

// Brief states as the page shows them, one per row.
const (
	briefRequested  = "requested"
	briefPushing    = "pushing"
	briefPushed     = "pushed"
	briefUnchanged  = "unchanged"
	briefPushFailed = "push_failed"
	briefFailed     = "failed"
)

// briefBody is a repository's brief on its row. Error is the request's
// failure (state failed) or the push's (push_failed).
type briefBody struct {
	State string `json:"state"`
	// Waiting is why a request is not taken yet: "budget", the day's
	// spent (the worker takes it the next UTC day); "" otherwise.
	Waiting      string     `json:"waiting"`
	Error        string     `json:"error"`
	HasText      bool       `json:"has_text"`
	PageRevision int        `json:"page_revision"`
	WrittenAt    *time.Time `json:"written_at"`
	Branch       string     `json:"branch"`
	PushedCommit string     `json:"pushed_commit"`
	PushAt       *time.Time `json:"push_at"`
}

// waitBudget is a request the day's budget holds.
const waitBudget = "budget"

// briefsOverBudget reports whether a waiting request among briefs is held
// by the day's budget, the worker's rule (store.OverBudget, over the same
// UTC day): spend read only when a request waits and a budget is set.
func (d Deps) briefsOverBudget(ctx context.Context, briefs map[int64]store.Brief) (bool, error) {
	budget := d.DailyBudgetNanoUSD
	if budget <= 0 {
		return false, nil
	}
	waiting := false
	for _, b := range briefs {
		waiting = waiting || b.RequestedAt != nil
	}
	if !waiting {
		return false, nil
	}
	spent, err := d.Store.Spent(ctx, store.DayStart(d.now()))
	if err != nil {
		return false, err
	}
	return store.OverBudget(spent.Budgeted(), budget), nil
}

// briefOf is the row's brief, its state the newest thing that happened: a
// waiting request, a failed one, then the push.
func briefOf(b store.Brief) *briefBody {
	out := &briefBody{HasText: b.HasMarkdown, PageRevision: b.PageRevision, WrittenAt: b.WrittenAt, Branch: consolidate.BriefBranch,
		PushedCommit: b.PushedCommit, PushAt: b.PushAt}
	switch {
	case b.RequestedAt != nil:
		out.State = briefRequested
	case b.FailedAt != nil:
		out.State, out.Error = briefFailed, b.Failed
	case b.PushState == store.PushDue:
		out.State = briefPushing
	case b.PushState == store.PushPushed:
		out.State = briefPushed
	case b.PushState == store.PushUnchanged:
		out.State = briefUnchanged
	case b.PushState == store.PushFailed:
		out.State, out.Error = briefPushFailed, b.PushError
	}
	return out
}

// briefTextBody is the brief with its file.
type briefTextBody struct {
	briefBody
	Markdown string `json:"markdown"`
	Path     string `json:"path"`
}

// repoBrief serves GET /api/sources/{key}/repos/{slug}/brief: the brief
// with its file, 404 before one was written.
func (d Deps) repoBrief(w http.ResponseWriter, r *http.Request) {
	if !d.enabled(w) {
		return
	}
	src, ok := d.lookupSource(w, r)
	if !ok {
		return
	}
	rp, ok := d.lookupRepo(w, r, src)
	if !ok {
		return
	}
	b, ok, err := d.Sources.Store.RepoBrief(r.Context(), rp.RepoID)
	if err != nil {
		httpx.ServerError(w, r, err, "brief")
		return
	}
	if !ok || !b.HasMarkdown {
		httpx.WriteError(w, http.StatusNotFound, "no brief written yet")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, briefTextBody{briefBody: *briefOf(b), Markdown: b.Markdown, Path: consolidate.BriefPath})
}

// requestBrief serves POST /api/sources/{key}/repos/{slug}/brief: the
// brief queued for the worker, which writes it (one call) and pushes it.
// A request already waiting keeps its place.
func (d Deps) requestBrief(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	d.reader(r)
	if !d.enabled(w) {
		return
	}
	src, ok := d.lookupSource(w, r)
	if !ok {
		return
	}
	rp, ok := d.lookupRepo(w, r, src)
	if !ok {
		return
	}
	if !rp.Active() {
		httpx.WriteError(w, http.StatusUnprocessableEntity, "the repository is deactivated")
		return
	}
	err := d.Sources.Store.RequestBrief(ctx, rp.RepoID)
	if errors.Is(err, store.ErrNoRepo) {
		httpx.WriteError(w, http.StatusNotFound, "unknown repository")
		return
	}
	if err != nil {
		httpx.ServerError(w, r, err, "request brief")
		return
	}
	d.Log.InfoContext(ctx, "brief_queued", "project", src.ProjectKey, "repo", rp.Slug, "user", d.user(r))
	if d.Worker != nil {
		d.Worker.Wake()
	}
	httpx.WriteJSON(w, http.StatusAccepted, queuedBody{Status: "queued", Repos: 1})
}
