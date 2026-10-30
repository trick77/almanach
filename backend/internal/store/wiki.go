package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Scopes of a description.
const (
	ScopeRepository = "repository"
	ScopeProject    = "project"
)

// Where a revision, a call or a statement's source comes from: a PR's note
// or a baseline run that read the code at a commit.
const (
	FromPR       = "pr"
	FromBaseline = "baseline"
)

// Source is one thing a statement rests on, attached by code when a fold
// keeps or touches the statement: a PR, or a baseline run's commit. Kind ""
// is a PR: every statement stored before baselines existed reads as one.
type Source struct {
	Kind        string    `json:"kind,omitempty"`
	PRKey       string    `json:"pr_key,omitempty"`
	TicketKey   string    `json:"ticket_key,omitempty"`
	MergedAt    time.Time `json:"merged_at,omitzero"`
	Commit      string    `json:"commit,omitempty"`
	CommittedAt time.Time `json:"committed_at,omitzero"`
}

// IsBaseline reports whether the source is a baseline run's commit.
func (s Source) IsBaseline() bool { return s.Kind == FromBaseline }

// Key names the source once: a statement gains a PR, or a commit, at most
// once however often it is touched by it.
func (s Source) Key() string {
	if s.IsBaseline() {
		return FromBaseline + ":" + s.Commit
	}
	return FromPR + ":" + s.PRKey
}

// Statement is one fact a description holds: a component, rule, invariant
// or entity, with the files it rests on and its sources. ID is stable
// across revisions so the merge can say which statement it keeps or edits.
// Checked is the commit a baseline run last confirmed it against, "" when
// none has.
type Statement struct {
	ID      string   `json:"id"`
	Kind    string   `json:"kind"`
	Name    string   `json:"name"`
	Text    string   `json:"text"`
	Files   []string `json:"files"`
	Sources []Source `json:"sources"`
	Checked string   `json:"checked,omitempty"`
}

// Page is a description as currently published.
type Page struct {
	Scope      string
	ProjectKey string
	RepoSlug   string
	RevisionID int64
	Revision   int
	Summary    string
	Statements []Statement
	// SourceKind says which of SourcePR and SourceCommit made the revision.
	SourceKind   string
	SourcePR     string
	SourceCommit string
	UpdatedAt    time.Time
}

// QueuedNote is the next note the worker folds in.
type QueuedNote struct {
	Note
	ReceivedAt time.Time
}

// NextQueued is the oldest note not yet consolidated, ok false when none. A
// note of a repository with an open baseline run waits for the run: no step
// may read a page holding facts newer than the commit it is shown.
func (s *Store) NextQueued(ctx context.Context) (QueuedNote, bool, error) {
	var q QueuedNote
	var bugfix, note []byte
	var ticket, issueType, kind, area, comp, reviewed, merge, model *string
	var runID *int64
	err := s.pool.QueryRow(ctx, `
		SELECT project_key, repo_slug, pr_id, team, pr_key, pr_title, ticket_key, jira_issue_type, change_kind,
		       bugfix, bugfix_area, bugfix_component, note, reviewed_commit, source_commit, merge_commit, merged_at,
		       run_id, model_label, issues_posted, suggestions_posted, received_at
		  FROM notes n WHERE consolidated_at IS NULL
		   AND NOT EXISTS (SELECT 1 FROM baseline_runs b WHERE b.project_key = n.project_key AND b.slug = n.repo_slug
		                      AND b.state NOT IN ('done', 'failed'))
		 ORDER BY received_at, pr_id LIMIT 1`).Scan(
		&q.ProjectKey, &q.RepoSlug, &q.PRID, &q.Team, &q.PRKey, &q.PRTitle, &ticket, &issueType, &kind,
		&bugfix, &area, &comp, &note, &reviewed, &q.SourceCommit, &merge, &q.MergedAt,
		&runID, &model, &q.IssuesPosted, &q.Suggestions, &q.ReceivedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return QueuedNote{}, false, nil
	}
	if err != nil {
		return QueuedNote{}, false, err
	}
	q.TicketKey, q.IssueType, q.ChangeKind = deref(ticket), deref(issueType), deref(kind)
	q.BugfixArea, q.BugfixComponent, q.ReviewedCommit, q.MergeCommit, q.ModelLabel = deref(area), deref(comp), deref(reviewed), deref(merge), deref(model)
	q.Bugfix, q.Body = json.RawMessage(bugfix), json.RawMessage(note)
	if runID != nil {
		q.RunID = *runID
	}
	return q, true, nil
}

// NextStatementID is one past the highest s<n> id any revision of the page
// ever used, 1 for a page with none. Over every revision, not the current
// one: an id dropped in one fold must never name a different fact later,
// the history reads ids as stable.
func (s *Store) NextStatementID(ctx context.Context, scope, project, repo string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(MAX(substr(st->>'id', 2)::int), 0) + 1
		  FROM revisions r, jsonb_array_elements(r.statements) st
		 WHERE r.scope = $1 AND r.project_key = $2 AND COALESCE(r.repo_slug, '') = $3
		   AND st->>'id' ~ '^s[0-9]{1,9}$'`, scope, project, repo).Scan(&n)
	return n, err
}

// DeferFold counts a failed gateway call against the note and leaves it
// queued; returns the attempts so far. The worker gives up past its bound.
func (s *Store) DeferFold(ctx context.Context, project, repo string, prID int) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `UPDATE notes SET fold_attempts = fold_attempts + 1
		WHERE project_key = $1 AND repo_slug = $2 AND pr_id = $3 RETURNING fold_attempts`, project, repo, prID).Scan(&n)
	return n, err
}

// MarkConsolidated takes the note off the queue, folded or refused.
func (s *Store) MarkConsolidated(ctx context.Context, project, repo string, prID int) error {
	_, err := s.pool.Exec(ctx, `UPDATE notes SET consolidated_at = now() WHERE project_key = $1 AND repo_slug = $2 AND pr_id = $3`,
		project, repo, prID)
	return err
}

// CurrentPage is the published description of one scope, ok false when
// there is none yet.
func (s *Store) CurrentPage(ctx context.Context, scope, project, repo string) (Page, bool, error) {
	p := Page{Scope: scope, ProjectKey: project, RepoSlug: repo}
	var statements []byte
	err := s.pool.QueryRow(ctx, `
		SELECT r.id, r.revision, r.summary, r.statements, r.source_kind, COALESCE(r.source_pr_key, ''),
		       COALESCE(r.source_commit, ''), p.updated_at
		  FROM pages p JOIN revisions r ON r.id = p.current_revision_id
		 WHERE p.scope = $1 AND p.project_key = $2 AND p.repo_slug = $3`, scope, project, repo).Scan(
		&p.RevisionID, &p.Revision, &p.Summary, &statements, &p.SourceKind, &p.SourcePR, &p.SourceCommit, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Page{}, false, nil
	}
	if err != nil {
		return Page{}, false, err
	}
	if err := json.Unmarshal(statements, &p.Statements); err != nil {
		return Page{}, false, err
	}
	if p.Statements == nil {
		p.Statements = []Statement{}
	}
	return p, true, nil
}

// NewRevision is what one fold produces for one scope. SourceKind "" is a
// PR's note (SourcePR); a baseline run sets FromBaseline, SourceCommit and
// BaselineRunID.
type NewRevision struct {
	Scope         string
	ProjectKey    string
	RepoSlug      string
	Summary       string
	Statements    []Statement
	Chars         int
	SourceKind    string
	SourcePR      string
	SourceCommit  string
	LLMCallID     *int64
	BaselineRunID *int64
}

// InsertRevision writes the next revision of a page and repoints the page
// to it, in one transaction under the page row's lock, so two folds of the
// same page number themselves correctly. Returns the revision number.
func (s *Store) InsertRevision(ctx context.Context, rev NewRevision) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	n, err := insertRevision(ctx, tx, rev)
	if err != nil {
		return 0, err
	}
	return n, tx.Commit(ctx)
}

// insertRevision is InsertRevision inside the caller's transaction.
func insertRevision(ctx context.Context, tx pgx.Tx, rev NewRevision) (int, error) {
	// One lock per page, whether or not the page row exists yet: the folds
	// are serial today, the lock keeps them correct if that changes.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, rev.Scope+"|"+rev.ProjectKey+"|"+rev.RepoSlug); err != nil {
		return 0, err
	}
	var repo *string
	if rev.Scope == ScopeRepository {
		repo = &rev.RepoSlug
	}
	statements, err := json.Marshal(orEmptyStatements(rev.Statements))
	if err != nil {
		return 0, err
	}
	var id int64
	var n int
	err = tx.QueryRow(ctx, `
		INSERT INTO revisions (scope, project_key, repo_slug, revision, summary, statements, chars,
		                       source_kind, source_pr_key, source_commit, llm_call_id, baseline_run_id)
		VALUES ($1, $2, $3,
		    (SELECT COALESCE(MAX(revision), 0) + 1 FROM revisions
		      WHERE scope = $1 AND project_key = $2 AND COALESCE(repo_slug, '') = $4),
		    $5, $6, $7, $8, NULLIF($9, ''), NULLIF($10, ''), $11, $12)
		RETURNING id, revision`,
		rev.Scope, rev.ProjectKey, repo, rev.RepoSlug, rev.Summary, statements, rev.Chars,
		fromKind(rev.SourceKind), rev.SourcePR, rev.SourceCommit, rev.LLMCallID, rev.BaselineRunID).Scan(&id, &n)
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO pages (scope, project_key, repo_slug, current_revision_id) VALUES ($1, $2, $3, $4)
		ON CONFLICT (scope, project_key, repo_slug) DO UPDATE SET current_revision_id = EXCLUDED.current_revision_id, updated_at = now()`,
		rev.Scope, rev.ProjectKey, rev.RepoSlug, id); err != nil {
		return 0, err
	}
	return n, nil
}

// fromKind is a source kind as stored: "" is a PR.
func fromKind(kind string) string {
	if kind == "" {
		return FromPR
	}
	return kind
}

// Purposes of a model call: a note's fold, or a baseline run's step.
const (
	PurposeMerge     = "merge"
	PurposeCatalogue = "catalogue"
	PurposeOverview  = "overview"
	PurposeSubject   = "subject"
	PurposeProject   = "project"
)

// LLMCall is one model call as the gateway described it. SourceKind "" and
// Purpose "" are a note's merge (SourcePR); a baseline step sets
// FromBaseline, SourceCommit, its Purpose and BaselineRunID.
type LLMCall struct {
	ProjectKey    string
	RepoSlug      string
	SourceKind    string
	SourcePR      string
	SourceCommit  string
	Purpose       string
	BaselineRunID *int64
	Outcome       string
	Model         string
	InputTokens   int64
	CachedTokens  int64
	OutputTokens  int64
	CostNanoUSD   *int64
	CallID        string
}

// InsertLLMCall records one model call and returns its id.
func (s *Store) InsertLLMCall(ctx context.Context, c LLMCall) (int64, error) {
	purpose := c.Purpose
	if purpose == "" {
		purpose = PurposeMerge
	}
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO llm_calls (project_key, repo_slug, source_kind, source_pr_key, source_commit, purpose, baseline_run_id,
		                       outcome, model, input_tokens, cached_tokens, output_tokens, cost_nano_usd, call_id)
		VALUES ($1, $2, $3, NULLIF($4, ''), NULLIF($5, ''), $6, $7, $8, NULLIF($9, ''), $10, $11, $12, $13, NULLIF($14, ''))
		RETURNING id`,
		c.ProjectKey, c.RepoSlug, fromKind(c.SourceKind), c.SourcePR, c.SourceCommit, purpose, c.BaselineRunID,
		c.Outcome, c.Model, c.InputTokens, c.CachedTokens, c.OutputTokens, c.CostNanoUSD, c.CallID).Scan(&id)
	return id, err
}

func orEmptyStatements(s []Statement) []Statement {
	if s == nil {
		return []Statement{}
	}
	return s
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
