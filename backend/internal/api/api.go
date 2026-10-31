// Package api is the HTTP surface: the noergler webhook and the ping (bearer
// = team, never a session), and the read API the UI consumes (the signed-in
// reader in oidc mode, anonymous in none). Structs, never maps:
// encoding/json sorts map keys.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/trick77/almanach/internal/auth"
	"github.com/trick77/almanach/internal/config"
	"github.com/trick77/almanach/internal/diagram"
	"github.com/trick77/almanach/internal/httpapi"
	"github.com/trick77/almanach/internal/ingest"
	"github.com/trick77/almanach/internal/store"
	"github.com/trick77/almanach/internal/teamkeys"
	"github.com/trick77/almanach/web"
	kitauth "github.com/trick77/ship/kit/auth"
	"github.com/trick77/ship/kit/httpx"
	"github.com/trick77/ship/kit/logging"
)

// maxBody bounds a webhook body: a note with everything beside it is a few
// kilobytes; a megabyte is somebody else's problem.
const maxBody = 1 << 20

// Store is what the handlers need of the store.
type Store interface {
	Ingest(ctx context.Context, d store.Delivery, n store.Note, call *store.NoteCall, claims []store.Claim) (bool, error)
	IsSourceRepo(ctx context.Context, key, slug string) (bool, error)
	Projects(ctx context.Context, sourced bool) ([]store.ProjectRow, error)
	ProjectExists(ctx context.Context, key string, sourced bool) (string, bool, error)
	Repos(ctx context.Context, project string, sourced bool) ([]store.RepoRow, error)
	RepoExists(ctx context.Context, project, slug string, sourced bool) (bool, error)
	CurrentPage(ctx context.Context, scope, project, repo string) (store.Page, bool, error)
	Revisions(ctx context.Context, scope, project, repo string, limit int) ([]store.RevisionRow, error)
	Notes(ctx context.Context, project, repo string, limit int) ([]store.NoteRow, error)
	NoteByKey(ctx context.Context, project, repo string, prID int) (store.NoteDetail, bool, error)
	Problems(ctx context.Context, project string, days int) ([]store.ProblemArea, error)
}

// Waker is poked when a note was queued.
type Waker interface{ Wake() }

// Deps is what the routes need.
type Deps struct {
	Store         Store
	Keys          *teamkeys.Keys
	Worker        Waker
	AuthMode      config.AuthMode
	ProblemWindow int
	// Sources is the clone feature; nil when ALMANACH_REPOS_DIR is unset,
	// and the routes then say so.
	Sources *SourcesDeps
	// OIDC, Sessions and RedirectURL are the sign-in, set in oidc mode
	// only; RedirectURL's origin is trusted for a change beside the
	// request's own host.
	OIDC        OIDCService
	Sessions    *kitauth.Sessions
	RedirectURL string
	Log         *slog.Logger
}

// Register mounts every route.
func Register(s *httpapi.Server, d Deps) {
	// The wiki SPA is the GET catch-all: Go's mux prefers the most specific
	// pattern, so every explicit route below still wins, the SPA's own
	// allowlist keeps /api, /auth and /webhooks typos a 404, and a wrong
	// method on a real route stays the mux's 405 rather than falling in.
	s.Handle("GET /", web.Handler())
	s.HandleFunc("GET /auth/ping", d.ping)
	s.Handle("POST /webhooks/noergler", httpx.MaxBody(maxBody)(http.HandlerFunc(d.webhook)))
	s.HandleFunc("GET /api/projects", d.projects)
	s.HandleFunc("GET /api/projects/{key}", d.project)
	s.HandleFunc("GET /api/projects/{key}/problems", d.problems)
	s.HandleFunc("GET /api/projects/{key}/repos/{slug}", d.repo)
	s.HandleFunc("GET /api/projects/{key}/repos/{slug}/notes/{pr}", d.note)
	d.registerSources(s)
	d.registerAuth(s)
}

// team is the caller's team by its bearer, or "" with the 401 written.
// The bearer is the whole identity: nothing in a body names a team.
func (d Deps) team(w http.ResponseWriter, r *http.Request) string {
	token, ok := httpx.Bearer(r)
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "bearer token required")
		return ""
	}
	team, reloadErr := d.Keys.Lookup(token)
	if reloadErr != nil {
		d.Log.WarnContext(r.Context(), "team keys reload failed, last good table in use", logging.Err(reloadErr))
	}
	if team == "" {
		httpx.WriteError(w, http.StatusUnauthorized, "unknown token")
		return ""
	}
	return team
}

type pingBody struct {
	Status string `json:"status"`
	Team   string `json:"team"`
}

// ping answers a sender's startup check with the team its token names.
func (d Deps) ping(w http.ResponseWriter, r *http.Request) {
	team := d.team(w, r)
	if team == "" {
		return
	}
	httpx.WriteJSON(w, http.StatusOK, pingBody{Status: "ok", Team: team})
}

type acceptedBody struct {
	Status string `json:"status"`
}

// webhook takes one noergler note. Strict: an unknown field or a missing
// required one is a 422 naming it; our own contract, so an invalid body is
// a bug on one side and must not be swallowed. Exactly one
// webhook_processed line per request, outcome accepted, deduped, ignored
// or invalid, always with the delivery id when one could be built.
func (d Deps) webhook(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	team := d.team(w, r)
	if team == "" {
		return
	}
	// The body is read whole and handed to Parse: a body that is not JSON
	// is as invalid as one with a wrong field, a 422 with its line, never
	// a 400 without one. The route's MaxBody bounds the read.
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			d.Log.InfoContext(ctx, "webhook_processed", "webhook_source", "noergler", "outcome", "invalid", "team", team,
				"delivery_id", "", "reason", "body over the limit")
			httpx.WriteError(w, http.StatusRequestEntityTooLarge, "body over the limit")
			return
		}
		httpx.ServerError(w, r, err, "read body")
		return
	}
	in, err := ingest.Parse(raw)
	if err != nil {
		d.Log.InfoContext(ctx, "webhook_processed", "webhook_source", "noergler", "outcome", "invalid", "team", team,
			"delivery_id", "", "reason", err.Error())
		httpx.WriteError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	in.Note.Team = team
	// With the sources on, a repository exists for Almanach once it is a
	// source repository: the baseline is its starting point and only the
	// deltas after it are folded. A note for any other is answered and
	// logged, never stored, so noergler marks it sent and does not resend.
	if d.Sources != nil {
		known, err := d.Store.IsSourceRepo(ctx, in.Note.ProjectKey, in.Note.RepoSlug)
		if err != nil {
			// A read, not a write: its own line, the same 500, so noergler
			// keeps the note pending and sends it again.
			d.Log.ErrorContext(ctx, "webhook_source_check_failed", "webhook_source", "noergler", "team", team,
				"delivery_id", in.DeliveryID, logging.Err(err))
			httpx.WriteError(w, http.StatusInternalServerError, "source check failed")
			return
		}
		if !known {
			d.Log.InfoContext(ctx, "webhook_processed", "webhook_source", "noergler", "outcome", "ignored", "team", team,
				"delivery_id", in.DeliveryID, "pr_key", in.Note.PRKey, "project", in.Note.ProjectKey, "repo", in.Note.RepoSlug,
				"reason", "no source repository")
			httpx.WriteJSON(w, http.StatusAccepted, acceptedBody{Status: "ignored"})
			return
		}
	}
	delivery := store.Delivery{ID: in.DeliveryID, Team: team, EventType: in.EventType, Payload: raw}
	accepted, err := d.Store.Ingest(ctx, delivery, in.Note, in.Call, in.Claims)
	if err != nil {
		// Never swallowed: the sender keeps the note pending and sends it
		// again on its next merge.
		d.Log.ErrorContext(ctx, "webhook_persist_failed", "webhook_source", "noergler", "team", team,
			"delivery_id", in.DeliveryID, logging.Err(err))
		httpx.WriteError(w, http.StatusInternalServerError, "persist failed")
		return
	}
	outcome := "accepted"
	if !accepted {
		outcome = "deduped"
	}
	d.Log.InfoContext(ctx, "webhook_processed", "webhook_source", "noergler", "outcome", outcome, "team", team,
		"delivery_id", in.DeliveryID, "pr_key", in.Note.PRKey, "change_kind", in.Note.ChangeKind)
	if accepted && d.Worker != nil {
		d.Worker.Wake()
	}
	httpx.WriteJSON(w, http.StatusAccepted, acceptedBody{Status: outcome})
}

// user is who reads, off the session's claims in oidc mode (preferred
// username, else email, else subject); anonymous in none. The read API serves the same to everyone;
// the name is for the access log and a later audit, never a gate.
func (d Deps) user(r *http.Request) string {
	return auth.ReaderFrom(r.Context())
}

// reader puts the reader on the access log line of every read route.
func (d Deps) reader(r *http.Request) {
	if u := d.user(r); u != "" {
		httpx.LogAttrs(r.Context(), slog.String("user", u))
	}
}

// --- read API ----------------------------------------------------------------

type projectBody struct {
	Key       string     `json:"key"`
	Team      string     `json:"team"`
	Repos     int        `json:"repos"`
	Notes     int        `json:"notes"`
	Revision  int        `json:"revision"`
	LastMerge *time.Time `json:"last_merge"`
}

type projectsBody struct {
	Projects []projectBody `json:"projects"`
}

func (d Deps) projects(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	d.reader(r)
	rows, err := d.Store.Projects(ctx, d.Sources != nil)
	if err != nil {
		httpx.ServerError(w, r, err, "projects")
		return
	}
	out := projectsBody{Projects: make([]projectBody, 0, len(rows))}
	for _, p := range rows {
		out.Projects = append(out.Projects, projectBody{Key: p.Key, Team: p.Team, Repos: p.Repos, Notes: p.Notes, Revision: p.Revision, LastMerge: p.LastMerge})
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// sourceBody is one source of a statement: a PR with its key, ticket and
// merge time, or a baseline run's commit with the commit's time. kind is
// always set; a source stored before baselines existed is a PR.
type sourceBody struct {
	Kind        string    `json:"kind"`
	PRKey       string    `json:"pr_key,omitempty"`
	TicketKey   string    `json:"ticket_key,omitempty"`
	MergedAt    time.Time `json:"merged_at,omitzero"`
	Commit      string    `json:"commit,omitempty"`
	CommittedAt time.Time `json:"committed_at,omitzero"`
	Repo        string    `json:"repo,omitempty"`
}

type statementBody struct {
	ID      string       `json:"id"`
	Kind    string       `json:"kind"`
	Name    string       `json:"name"`
	Text    string       `json:"text"`
	Files   []string     `json:"files"`
	Sources []sourceBody `json:"sources"`
	// Checked is the commit a baseline run last confirmed the statement
	// against, "" when none has.
	Checked string `json:"checked"`
	// Diagram is rongo's IR as validated on the way in, absent for none.
	Diagram *diagram.Diagram `json:"diagram,omitempty"`
}

type descriptionBody struct {
	Revision     int             `json:"revision"`
	Summary      string          `json:"summary"`
	Statements   []statementBody `json:"statements"`
	SourceKind   string          `json:"source_kind"`
	SourcePR     string          `json:"source_pr_key"`
	SourceCommit string          `json:"source_commit"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

// sourceKind is a kind as the wire says it: "" stored is a PR.
func sourceKind(kind string) string {
	if kind == "" {
		return store.FromPR
	}
	return kind
}

func describe(p store.Page, ok bool) *descriptionBody {
	if !ok {
		return nil
	}
	out := &descriptionBody{Revision: p.Revision, Summary: p.Summary, SourceKind: sourceKind(p.SourceKind), SourcePR: p.SourcePR,
		SourceCommit: p.SourceCommit, UpdatedAt: p.UpdatedAt, Statements: make([]statementBody, 0, len(p.Statements))}
	for _, s := range p.Statements {
		sb := statementBody{ID: s.ID, Kind: s.Kind, Name: s.Name, Text: s.Text, Files: s.Files, Checked: s.Checked,
			Diagram: s.Diagram, Sources: make([]sourceBody, 0, len(s.Sources))}
		if sb.Files == nil {
			sb.Files = []string{}
		}
		for _, src := range s.Sources {
			sb.Sources = append(sb.Sources, sourceBody{Kind: sourceKind(src.Kind), PRKey: src.PRKey, TicketKey: src.TicketKey,
				MergedAt: src.MergedAt, Commit: src.Commit, CommittedAt: src.CommittedAt, Repo: src.Repo})
		}
		out.Statements = append(out.Statements, sb)
	}
	return out
}

type repoBody struct {
	Slug      string     `json:"slug"`
	Revision  int        `json:"revision"`
	Summary   string     `json:"summary"`
	Notes     int        `json:"notes"`
	LastMerge *time.Time `json:"last_merge"`
}

type noteBody struct {
	PRID        int       `json:"pr_id"`
	PRKey       string    `json:"pr_key"`
	PRTitle     string    `json:"pr_title"`
	RepoSlug    string    `json:"repo_slug"`
	TicketKey   string    `json:"ticket_key,omitempty"`
	ChangeKind  string    `json:"change_kind,omitempty"`
	MergedAt    time.Time `json:"merged_at"`
	Items       int       `json:"items"`
	Issues      int       `json:"issues_posted"`
	Suggestions int       `json:"suggestions_posted"`
}

type problemBody struct {
	RepoSlug    string    `json:"repo_slug"`
	Area        string    `json:"area"`
	Component   string    `json:"component"`
	Bugfixes    int       `json:"bugfixes"`
	TopCause    string    `json:"top_cause"`
	TopCauseN   int       `json:"top_cause_count"`
	Issues      int       `json:"issues_posted"`
	Suggestions int       `json:"suggestions_posted"`
	LastFixPR   string    `json:"last_fix_pr_key"`
	LastFixAt   time.Time `json:"last_fix_at"`
}

type projectDetailBody struct {
	Key         string           `json:"key"`
	Team        string           `json:"team"`
	Description *descriptionBody `json:"description"`
	// Map is the system map of the current project revision, null before
	// a baseline project step drew one.
	Map *diagram.Diagram `json:"map"`
	// MapExpected says a baseline run will draw a map: the sources are on,
	// the project has a source repository, and not more than one map holds.
	MapExpected bool `json:"map_expected"`
	// MapDeclined says a project step asked for the map and none was
	// accepted: no map, and none promised for the next run either.
	MapDeclined bool          `json:"map_declined"`
	Repos       []repoBody    `json:"repos"`
	Problems    []problemBody `json:"problems"`
	ProblemDays int           `json:"problem_window_days"`
	Notes       []noteBody    `json:"recent_notes"`
}

func (d Deps) project(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	d.reader(r)
	key := r.PathValue("key")
	team, ok, err := d.Store.ProjectExists(ctx, key, d.Sources != nil)
	if err != nil {
		httpx.ServerError(w, r, err, "project")
		return
	}
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "unknown project")
		return
	}
	page, found, err := d.Store.CurrentPage(ctx, store.ScopeProject, key, "")
	if err != nil {
		httpx.ServerError(w, r, err, "project page")
		return
	}
	repos, err := d.Store.Repos(ctx, key, d.Sources != nil)
	if err != nil {
		httpx.ServerError(w, r, err, "repos")
		return
	}
	problems, err := d.Store.Problems(ctx, key, d.ProblemWindow)
	if err != nil {
		httpx.ServerError(w, r, err, "problems")
		return
	}
	notes, err := d.Store.Notes(ctx, key, "", 20)
	if err != nil {
		httpx.ServerError(w, r, err, "notes")
		return
	}
	// With the sources on, repos is the source repositories alone.
	mapExpected := d.Sources != nil && len(repos) > 0 && diagram.MapFits(len(repos))
	// The sentinel is no map: asked for, not drawn.
	drawn, declined := page.Map, diagram.IsSentinel(page.Map)
	if declined {
		drawn = nil
	}
	out := projectDetailBody{Key: key, Team: team, Description: describe(page, found), Map: drawn, MapExpected: mapExpected,
		MapDeclined: declined, ProblemDays: d.ProblemWindow,
		Repos: make([]repoBody, 0, len(repos)), Problems: problemBodies(problems), Notes: noteBodies(notes)}
	for _, rp := range repos {
		out.Repos = append(out.Repos, repoBody{Slug: rp.Slug, Revision: rp.Revision, Summary: firstSentence(rp.Summary), Notes: rp.Notes, LastMerge: rp.LastMerge})
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

type problemsBody struct {
	Problems    []problemBody `json:"problems"`
	ProblemDays int           `json:"problem_window_days"`
}

func (d Deps) problems(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	d.reader(r)
	key := r.PathValue("key")
	days := d.ProblemWindow
	if v := r.URL.Query().Get("days"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 3650 {
			httpx.WriteError(w, http.StatusBadRequest, "days: 1..3650")
			return
		}
		days = n
	}
	if _, ok, err := d.Store.ProjectExists(ctx, key, d.Sources != nil); err != nil {
		httpx.ServerError(w, r, err, "project")
		return
	} else if !ok {
		httpx.WriteError(w, http.StatusNotFound, "unknown project")
		return
	}
	problems, err := d.Store.Problems(ctx, key, days)
	if err != nil {
		httpx.ServerError(w, r, err, "problems")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, problemsBody{Problems: problemBodies(problems), ProblemDays: days})
}

type revisionBody struct {
	Revision     int       `json:"revision"`
	SourceKind   string    `json:"source_kind"`
	SourcePR     string    `json:"source_pr_key"`
	SourceCommit string    `json:"source_commit"`
	Statements   int       `json:"statements"`
	Chars        int       `json:"chars"`
	CreatedAt    time.Time `json:"created_at"`
}

type repoDetailBody struct {
	ProjectKey  string           `json:"project_key"`
	Slug        string           `json:"slug"`
	Description *descriptionBody `json:"description"`
	Revisions   []revisionBody   `json:"revisions"`
	Notes       []noteBody       `json:"notes"`
	// NotesTotal is every note of the repository; Notes lists the newest
	// hundred.
	NotesTotal int        `json:"notes_total"`
	LastMerge  *time.Time `json:"last_merge"`
}

func (d Deps) repo(w http.ResponseWriter, r *http.Request) {
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
	page, found, err := d.Store.CurrentPage(ctx, store.ScopeRepository, key, slug)
	if err != nil {
		httpx.ServerError(w, r, err, "repo page")
		return
	}
	revisions, err := d.Store.Revisions(ctx, store.ScopeRepository, key, slug, 50)
	if err != nil {
		httpx.ServerError(w, r, err, "revisions")
		return
	}
	notes, err := d.Store.Notes(ctx, key, slug, 100)
	if err != nil {
		httpx.ServerError(w, r, err, "notes")
		return
	}
	// The repository's own row carries the whole count and the newest
	// merge; the notes list above is capped.
	repos, err := d.Store.Repos(ctx, key, d.Sources != nil)
	if err != nil {
		httpx.ServerError(w, r, err, "repos")
		return
	}
	out := repoDetailBody{ProjectKey: key, Slug: slug, Description: describe(page, found), Notes: noteBodies(notes),
		Revisions: make([]revisionBody, 0, len(revisions))}
	for _, rp := range repos {
		if rp.Slug == slug {
			out.NotesTotal, out.LastMerge = rp.Notes, rp.LastMerge
		}
	}
	for _, rv := range revisions {
		out.Revisions = append(out.Revisions, revisionBody{Revision: rv.Revision, SourceKind: sourceKind(rv.SourceKind), SourcePR: rv.SourcePR,
			SourceCommit: rv.SourceCommit, Statements: rv.Statements, Chars: rv.Chars, CreatedAt: rv.CreatedAt})
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

type noteCallBody struct {
	Outcome         string `json:"outcome"`
	NoteStatus      string `json:"note_status"`
	Model           string `json:"model,omitempty"`
	InputTokens     *int64 `json:"input_tokens"`
	CacheReadTokens *int64 `json:"cache_read_tokens"`
	OutputTokens    *int64 `json:"output_tokens"`
	CostUSD         string `json:"cost_usd,omitempty"`
	// Rounds and its tokens are every note call the PR made, summed by
	// the sender; the lanes above are this call alone.
	Rounds             int    `json:"rounds"`
	RoundsInputTokens  int64  `json:"rounds_input_tokens"`
	RoundsOutputTokens int64  `json:"rounds_output_tokens"`
	RoundsCostUSD      string `json:"rounds_cost_usd,omitempty"`
}

type noteDetailBody struct {
	noteBody
	ProjectKey     string          `json:"project_key"`
	JiraIssueType  string          `json:"jira_issue_type,omitempty"`
	ReviewedCommit string          `json:"reviewed_commit,omitempty"`
	SourceCommit   string          `json:"source_commit"`
	MergeCommit    string          `json:"merge_commit,omitempty"`
	RunID          int64           `json:"run_id,omitempty"`
	ModelLabel     string          `json:"model_label,omitempty"`
	Bugfix         json.RawMessage `json:"bugfix"`
	BugfixArea     string          `json:"bugfix_area,omitempty"`
	Note           json.RawMessage `json:"note"`
	ReceivedAt     time.Time       `json:"received_at"`
	ConsolidatedAt *time.Time      `json:"consolidated_at"`
	Call           *noteCallBody   `json:"note_call"`
}

func (d Deps) note(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	d.reader(r)
	key, slug := r.PathValue("key"), r.PathValue("slug")
	prID, err := strconv.Atoi(r.PathValue("pr"))
	if err != nil || prID < 1 {
		httpx.WriteError(w, http.StatusNotFound, "unknown note")
		return
	}
	n, ok, err := d.Store.NoteByKey(ctx, key, slug, prID)
	if err != nil {
		httpx.ServerError(w, r, err, "note")
		return
	}
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "unknown note")
		return
	}
	out := noteDetailBody{
		noteBody: noteBody{PRID: n.PRID, PRKey: n.PRKey, PRTitle: n.PRTitle, RepoSlug: n.RepoSlug, TicketKey: n.TicketKey,
			ChangeKind: n.ChangeKind, MergedAt: n.MergedAt, Items: n.Items, Issues: n.IssuesPosted, Suggestions: n.Suggestions},
		ProjectKey: n.ProjectKey, JiraIssueType: n.IssueType, ReviewedCommit: n.ReviewedCommit, SourceCommit: n.SourceCommit,
		MergeCommit: n.MergeCommit, RunID: n.RunID, ModelLabel: n.ModelLabel, Bugfix: orNull(n.Bugfix), BugfixArea: n.BugfixArea,
		Note: orNull(n.Body), ReceivedAt: n.ReceivedAt, ConsolidatedAt: n.ConsolidatedAt,
	}
	if c := n.Call; c != nil {
		out.Call = &noteCallBody{Outcome: c.Outcome, NoteStatus: c.NoteStatus, Model: c.Model, InputTokens: c.InputTokens,
			CacheReadTokens: c.CacheReadTokens, OutputTokens: c.OutputTokens, CostUSD: usd(c.CostNanoUSD), Rounds: c.Rounds,
			RoundsInputTokens: c.RoundsInputTokens, RoundsOutputTokens: c.RoundsOutputTokens, RoundsCostUSD: usd(c.RoundsCost)}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func problemBodies(rows []store.ProblemArea) []problemBody {
	out := make([]problemBody, 0, len(rows))
	for _, p := range rows {
		out = append(out, problemBody{RepoSlug: p.RepoSlug, Area: p.Area, Component: p.Component, Bugfixes: p.Bugfixes,
			TopCause: p.TopCause, TopCauseN: p.TopCauseN, Issues: p.Issues, Suggestions: p.Suggestions, LastFixPR: p.LastFixPR, LastFixAt: p.LastFixAt})
	}
	return out
}

func noteBodies(rows []store.NoteRow) []noteBody {
	out := make([]noteBody, 0, len(rows))
	for _, n := range rows {
		out = append(out, noteBody{PRID: n.PRID, PRKey: n.PRKey, PRTitle: n.PRTitle, RepoSlug: n.RepoSlug, TicketKey: n.TicketKey,
			ChangeKind: n.ChangeKind, MergedAt: n.MergedAt, Items: n.Items, Issues: n.Issues, Suggestions: n.Suggestions})
	}
	return out
}

// abbreviations end in a period without ending the sentence, the German
// ones a description of software reaches for.
var abbreviations = map[string]bool{
	"bzw": true, "ca": true, "etc": true, "evtl": true, "ggf": true, "inkl": true, "max": true, "min": true,
	"nr": true, "usw": true, "vgl": true, "zb": true, "dh": true, "ua": true, "sog": true, "bspw": true, "vs": true,
}

// firstSentence is a summary's first sentence, for a list: up to the first
// ".", "!" or "?" that is followed by a space or the end. After an
// abbreviation or a dotted name ("z.B.", "config.yaml", "2.0") the period
// ends the sentence only when a capital follows ("Liest main.go. Dann").
func firstSentence(s string) string {
	s = strings.TrimSpace(s)
	for i := 0; i < len(s); i++ {
		if !strings.ContainsRune(".!?", rune(s[i])) || (i+1 < len(s) && s[i+1] != ' ') {
			continue
		}
		if i+1 == len(s) {
			return s
		}
		start := strings.LastIndexByte(s[:i], ' ') + 1
		word := s[start:i]
		dotted := strings.Contains(word, ".") || abbreviations[strings.ToLower(strings.ReplaceAll(word, ".", ""))]
		if next, _ := utf8.DecodeRuneInString(strings.TrimLeft(s[i+1:], " ")); dotted && !unicode.IsUpper(next) {
			continue
		}
		return s[:i+1]
	}
	return s
}

func orNull(b json.RawMessage) json.RawMessage {
	if len(b) == 0 {
		return json.RawMessage("null")
	}
	return b
}

// usd renders nano-USD as a 3-decimal string, never a float; "" when
// unpriced (the field is then omitted).
func usd(nano *int64) string {
	if nano == nil {
		return ""
	}
	n := *nano
	sign := ""
	if n < 0 {
		sign, n = "-", -n
	}
	// Round half up to milli-USD.
	milli := (n + 500_000) / 1_000_000
	return sign + strconv.FormatInt(milli/1000, 10) + "." + leftPad(strconv.FormatInt(milli%1000, 10), 3)
}

func leftPad(s string, n int) string {
	for len(s) < n {
		s = "0" + s
	}
	return s
}
