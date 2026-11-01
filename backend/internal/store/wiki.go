package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/trick77/almanach/internal/diagram"
	"github.com/trick77/almanach/internal/lang"
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
	Kind      string    `json:"kind,omitempty"`
	PRKey     string    `json:"pr_key,omitempty"`
	TicketKey string    `json:"ticket_key,omitempty"`
	MergedAt  time.Time `json:"merged_at,omitzero"`
	// Commit is a baseline run's read commit, or a PR's merge commit: a run
	// asks git whether that is in what it reads. Empty on PR sources from
	// before it was kept, which the merge time decides.
	Commit      string    `json:"commit,omitempty"`
	CommittedAt time.Time `json:"committed_at,omitzero"`
	// Repo is the slug whose commit a baseline source is: on the project
	// page a commit alone would not say which repository's history it is.
	Repo string `json:"repo,omitempty"`
}

// IsBaseline reports whether the source is a baseline run's commit.
func (s Source) IsBaseline() bool { return s.Kind == FromBaseline }

// Key names the source once: a statement gains a PR, or a commit, at most
// once however often it is touched by it.
func (s Source) Key() string {
	if s.IsBaseline() {
		return FromBaseline + ":" + s.Repo + "@" + s.Commit
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
	// Diagram is a picture beside the text, validated like the text; nil
	// for none.
	Diagram *diagram.Diagram `json:"diagram,omitempty"`
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
	// Map is a project page's system map, nil on a repository page and on
	// a project page no baseline project step has drawn yet.
	Map *diagram.Diagram
	// RepoMap is a repository page's map of itself, nil on a project page
	// and on a repository page no run's map step has drawn yet.
	RepoMap *diagram.Diagram
	// RepoMapBasis is the description the map step last answered for, as
	// it hashed it: RepoMap drawn from it, or none when the model drew none;
	// "" before any answer. A basis that no longer matches the page's text
	// is a map due again.
	RepoMapBasis string
	// Stored is the summary and texts as the revision holds them, set only
	// when the read laid them out anew (lang.Relaid): a map basis hashed
	// from them before the layout still matches.
	// TRANSITIONAL(layout): Remove when every page's current revision was
	// written after the layout.
	Stored *StoredText
}

// StoredText is a page's summary and statement texts as stored, before a
// read laid them out.
// TRANSITIONAL(layout): Remove with Page.Stored.
type StoredText struct {
	Summary string
	Texts   []string
}

// MapStands reports whether the map step answered for a repository page's
// description as it stands, a map drawn or none (a model that drew none
// for this text is not asked again until it changes): then a run has
// nothing new to draw from.
func (p Page) MapStands() bool {
	if p.RepoMapBasis == "" {
		return false
	}
	if p.RepoMapBasis == p.MapBasis() {
		return true
	}
	// A basis hashed before the layout is of the text as stored; a read
	// that only laid it out anew is no reason for a paid redraw.
	// TRANSITIONAL(layout): Remove with Page.Stored.
	if p.Stored == nil {
		return false
	}
	raw := p
	raw.Summary = p.Stored.Summary
	raw.Statements = slices.Clone(p.Statements)
	for i := range raw.Statements {
		raw.Statements[i].Text = p.Stored.Texts[i]
	}
	return p.RepoMapBasis == raw.MapBasis()
}

// MapBasis is the description a repository map is drawn from, as a hash:
// the summary and each statement's kind, name, text and files, never its
// sources or last check, which change without the text. The shape hashed
// is what every stored basis was: change it and every map is due again.
func (p Page) MapBasis() string {
	type part struct {
		Kind, Name, Text string
		Files            []string
	}
	parts := make([]part, 0, len(p.Statements))
	for _, s := range p.Statements {
		parts = append(parts, part{s.Kind, s.Name, s.Text, s.Files})
	}
	raw, _ := json.Marshal(struct {
		Summary    string
		Statements []part
	}{p.Summary, parts})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// QueuedNote is the next note the worker folds in.
type QueuedNote struct {
	Note
	ReceivedAt time.Time
}

// NextQueued is the oldest note not yet consolidated, ok false when none. A
// note of a repository whose baseline run has started waits for the run:
// the run pins its commit when it starts, and no step may read a page
// holding facts newer than that commit. A run still queued holds nothing; a
// long queue of runs must not hold the notes for hours.
func (s *Store) NextQueued(ctx context.Context) (QueuedNote, bool, error) {
	var q QueuedNote
	var bugfix, note []byte
	var ticket, issueType, kind, area, comp, reviewed, merge, model *string
	var runID *int64
	// The names are the current ones: a note delivered under a name the
	// repository has since left behind folds into the same page.
	err := s.db.QueryRowContext(ctx, `
		SELECT p.key, r.slug, n.pr_id, n.team, n.pr_key, n.pr_title, n.ticket_key, n.jira_issue_type, n.change_kind,
		       n.bugfix, n.bugfix_area, n.bugfix_component, n.note, n.reviewed_commit, n.source_commit, n.merge_commit,
		       n.merged_at, n.run_id, n.model_label, n.issues_posted, n.suggestions_posted, n.received_at
		  FROM notes n JOIN repos r ON r.id = n.repo_id JOIN projects p ON p.id = r.project_id
		 WHERE n.consolidated_at IS NULL
		   AND NOT EXISTS (`+startedRunOfNote+`)
		 ORDER BY n.received_at, n.pr_id LIMIT 1`).Scan(
		&q.ProjectKey, &q.RepoSlug, &q.PRID, &q.Team, &q.PRKey, &q.PRTitle, &ticket, &issueType, &kind,
		&bugfix, &area, &comp, &note, &reviewed, &q.SourceCommit, &merge, timeAt(&q.MergedAt),
		&runID, &model, &q.IssuesPosted, &q.Suggestions, timeAt(&q.ReceivedAt))
	if errors.Is(err, sql.ErrNoRows) {
		return QueuedNote{}, false, nil
	}
	if err != nil {
		return QueuedNote{}, false, err
	}
	q.TicketKey, q.IssueType, q.ChangeKind = deref(ticket), deref(issueType), deref(kind)
	q.BugfixArea, q.BugfixComponent, q.ReviewedCommit, q.MergeCommit, q.ModelLabel = deref(area), deref(comp), deref(reviewed), deref(merge), deref(model)
	q.PRTitle, q.Bugfix, q.Body = noteOut(q.PRTitle, bugfix, note)
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
	pid, rid, ok, err := scopeIDs(ctx, s.db, project, repo)
	if err != nil || !ok {
		return 1, err
	}
	var n int
	// An id is s and one to nine digits; json_each walks every revision's
	// statements.
	err = s.db.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(CAST(substr(id, 2) AS INTEGER)), 0) + 1
		  FROM (SELECT st.value ->> '$.id' AS id
		          FROM revisions r, json_each(r.statements) st
		         WHERE r.scope = ? AND r.project_id = ? AND COALESCE(r.repo_id, 0) = ?)
		 WHERE substr(id, 1, 1) = 's' AND length(id) BETWEEN 2 AND 10 AND substr(id, 2) NOT GLOB '*[^0-9]*'`,
		scope, pid, rid).Scan(&n)
	return n, err
}

// DeferFold counts a failed gateway call against the note and leaves it
// queued; returns the attempts so far. The worker gives up past its bound.
func (s *Store) DeferFold(ctx context.Context, project, repo string, prID int) (int, error) {
	rid, ok, err := repoID(ctx, s.db, project, repo)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, sql.ErrNoRows
	}
	var n int
	err = s.db.QueryRowContext(ctx, `UPDATE notes SET fold_attempts = fold_attempts + 1
		WHERE repo_id = ? AND pr_id = ? RETURNING fold_attempts`, rid, prID).Scan(&n)
	return n, err
}

// MarkConsolidated takes the note off the queue, folded or refused.
func (s *Store) MarkConsolidated(ctx context.Context, project, repo string, prID int) error {
	rid, ok, err := repoID(ctx, s.db, project, repo)
	if err != nil || !ok {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE notes SET consolidated_at = ? WHERE repo_id = ? AND pr_id = ?`,
		nowStamp(), rid, prID)
	return err
}

// CurrentPage is the published description of one scope, ok false when
// there is none yet. Any name the project or repository ever had finds the
// page; the page carries the current ones.
func (s *Store) CurrentPage(ctx context.Context, scope, project, repo string) (Page, bool, error) {
	pid, rid, ok, err := scopeIDs(ctx, s.db, project, repo)
	if err != nil || !ok {
		return Page{}, false, err
	}
	p := Page{Scope: scope}
	var statements, drawn, repoDrawn []byte
	err = s.db.QueryRowContext(ctx, `
		SELECT pj.key, COALESCE(rp.slug, ''), r.id, r.revision, r.summary, r.statements, r.source_kind,
		       COALESCE(r.source_pr_key, ''), COALESCE(r.source_commit, ''), p.updated_at, r.map,
		       r.repo_map, COALESCE(r.repo_map_basis, '')
		  FROM pages p JOIN revisions r ON r.id = p.current_revision_id
		  JOIN projects pj ON pj.id = p.project_id LEFT JOIN repos rp ON rp.id = p.repo_id
		 WHERE p.scope = ? AND p.project_id = ? AND p.repo_id = ?`, scope, pid, rid).Scan(
		&p.ProjectKey, &p.RepoSlug, &p.RevisionID, &p.Revision, &p.Summary, &statements, &p.SourceKind, &p.SourcePR,
		&p.SourceCommit, timeAt(&p.UpdatedAt), &drawn, &repoDrawn, &p.RepoMapBasis)
	if errors.Is(err, sql.ErrNoRows) {
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
	// Read laid out (lang.Relaid): a revision written before the layout
	// reads as one written now, wherever the page goes; what it holds is
	// kept beside it when that differs (Page.Stored).
	// TRANSITIONAL(layout): Remove the relaying when every page's current
	// revision was written after the layout.
	stored := StoredText{Summary: p.Summary, Texts: make([]string, len(p.Statements))}
	changed := false
	p.Summary = lang.Relaid(p.Summary, false)
	changed = p.Summary != stored.Summary
	for i := range p.Statements {
		stored.Texts[i] = p.Statements[i].Text
		p.Statements[i].Text = lang.Relaid(p.Statements[i].Text, true)
		changed = changed || p.Statements[i].Text != stored.Texts[i]
	}
	if changed {
		p.Stored = &stored
	}
	if p.Map, err = diagramOf(drawn); err != nil {
		return Page{}, false, err
	}
	if p.RepoMap, err = diagramOf(repoDrawn); err != nil {
		return Page{}, false, err
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
	// Map is a project revision's system map: the project step's, or the
	// current page's carried forward; nil on a repository revision.
	//
	// A repository revision names no map: insertRevision carries the page's
	// repository map and its basis into it, and only SetRepoMap changes
	// them. Carried in the store, unlike Map: nothing removes or repairs a
	// repository map on the way, while a project revision's caller drops
	// departed repositories from Map and writes its sentinel, so only the
	// caller knows the map to carry.
	Map *diagram.Diagram
}

// InsertRevision writes the next revision of a page and repoints the page
// to it, in one transaction. The transaction holds the database's one
// write lock from its BEGIN, so two folds of the same page number
// themselves correctly. Returns the revision number.
func (s *Store) InsertRevision(ctx context.Context, rev NewRevision) (int, error) {
	var n int
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var err error
		n, err = insertRevision(ctx, tx, rev)
		return err
	})
	if err != nil {
		return 0, err
	}
	return n, nil
}

// insertRevision is InsertRevision inside the caller's transaction.
func insertRevision(ctx context.Context, tx *sql.Tx, rev NewRevision) (int, error) {
	pid, err := ensureProject(ctx, tx, rev.ProjectKey)
	if err != nil {
		return 0, err
	}
	var rid int64
	var repo *int64
	if rev.Scope == ScopeRepository {
		if rid, err = ensureRepo(ctx, tx, pid, rev.RepoSlug); err != nil {
			return 0, err
		}
		repo = &rid
	}
	statements, err := jsonOf(orEmptyStatements(rev.Statements))
	if err != nil {
		return 0, err
	}
	var drawn any
	if rev.Map != nil {
		if drawn, err = jsonOf(rev.Map); err != nil {
			return 0, err
		}
	}
	now := nowStamp()
	var id int64
	var n int
	// A repository revision takes the page's map and its basis, read in
	// this transaction: a merge or a run's describe step changes the text,
	// never the map, and the basis then says the map is due again. On a
	// project revision both stay NULL: the subqueries' scope never matches
	// a project page.
	err = tx.QueryRowContext(ctx, `
		INSERT INTO revisions (scope, project_id, repo_id, revision, summary, statements, chars,
		                       source_kind, source_pr_key, source_commit, llm_call_id, baseline_run_id, map,
		                       repo_map, repo_map_basis, created_at)
		VALUES (?1, ?2, ?3,
		    (SELECT COALESCE(MAX(revision), 0) + 1 FROM revisions
		      WHERE scope = ?1 AND project_id = ?2 AND COALESCE(repo_id, 0) = ?4),
		    ?5, ?6, ?7, ?8, NULLIF(?9, ''), NULLIF(?10, ''), ?11, ?12, ?13,
		    (SELECT r.repo_map FROM pages p JOIN revisions r ON r.id = p.current_revision_id
		      WHERE ?1 = 'repository' AND p.scope = ?1 AND p.project_id = ?2 AND p.repo_id = ?4),
		    (SELECT r.repo_map_basis FROM pages p JOIN revisions r ON r.id = p.current_revision_id
		      WHERE ?1 = 'repository' AND p.scope = ?1 AND p.project_id = ?2 AND p.repo_id = ?4),
		    ?14)
		RETURNING id, revision`,
		rev.Scope, pid, repo, rid, rev.Summary, statements, rev.Chars,
		fromKind(rev.SourceKind), rev.SourcePR, rev.SourceCommit, rev.LLMCallID, rev.BaselineRunID, drawn, now).Scan(&id, &n)
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO pages (scope, project_id, repo_id, current_revision_id, updated_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (scope, project_id, repo_id) DO UPDATE SET current_revision_id = EXCLUDED.current_revision_id, updated_at = EXCLUDED.updated_at`,
		rev.Scope, pid, rid, id, now); err != nil {
		return 0, err
	}
	return n, nil
}

// diagramOf reads a stored diagram column; NULL is nil.
func diagramOf(raw []byte) (*diagram.Diagram, error) {
	// The JSON text null is no map either: a write before SetRepoMap kept
	// a nil map as SQL NULL stored it so.
	if raw == nil || string(raw) == "null" {
		return nil, nil
	}
	d := &diagram.Diagram{}
	if err := json.Unmarshal(raw, d); err != nil {
		return nil, err
	}
	return d, nil
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
	PurposeRepoMap   = "repo_map"
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
	// Step is the run's reading step (overview or subject), nil otherwise.
	Step         *int
	Outcome      string
	Model        string
	InputTokens  int64
	CachedTokens int64
	OutputTokens int64
	CostNanoUSD  *int64
	CallID       string
}

// InsertLLMCall records one model call and returns its id.
func (s *Store) InsertLLMCall(ctx context.Context, c LLMCall) (int64, error) {
	purpose := c.Purpose
	if purpose == "" {
		purpose = PurposeMerge
	}
	var id int64
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		pid, err := ensureProject(ctx, tx, c.ProjectKey)
		if err != nil {
			return err
		}
		rid, err := ensureRepo(ctx, tx, pid, c.RepoSlug)
		if err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `
			INSERT INTO llm_calls (repo_id, source_kind, source_pr_key, source_commit, purpose, baseline_run_id, step,
			                       outcome, model, input_tokens, cached_tokens, output_tokens, cost_nano_usd, call_id, created_at)
			VALUES (?, ?, NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?, NULLIF(?, ''), ?)
			RETURNING id`,
			rid, fromKind(c.SourceKind), c.SourcePR, c.SourceCommit, purpose, c.BaselineRunID, c.Step,
			c.Outcome, c.Model, c.InputTokens, c.CachedTokens, c.OutputTokens, c.CostNanoUSD, c.CallID, nowStamp()).Scan(&id)
	})
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
