package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/trick77/almanach/internal/diagram"
	"github.com/trick77/almanach/internal/redact"
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
	// Area is the part of the system the statement is about, the page's
	// sub-chunk inside every kind (baseline.Arrange); "" before one is
	// given.
	Area string `json:"area,omitempty"`
}

// A project's shape: one system its repositories make together, or a
// holder of repositories that each stand on their own, whose page is a
// short summary alone: no statements, no map.
const (
	ShapeSystem     = "system"
	ShapeCollection = "collection"
)

// ShapeMoves reports whether judging a page shaped before as after changes
// what the page shows: another shape, but a system judged on a page
// written before shapes ("", its NULL), which changes nothing but the
// label. The worker writes a revision for a move alone; the revision view
// names one.
func ShapeMoves(before, after string) bool {
	return after != before && (before != "" || after != ShapeSystem)
}

// queuedColumns are a queued note's columns as scanQueued reads them. The
// names are the current ones: a note delivered under a name the repository
// has since left behind folds into the same page.
const queuedColumns = `p.key, r.slug, n.pr_id, n.team, n.pr_key, n.pr_title, n.ticket_key, n.jira_issue_type, n.change_kind,
	n.bugfix, n.bugfix_area, n.bugfix_component, n.note, n.reviewed_commit, n.source_commit, n.merge_commit,
	n.merged_at, n.run_id, n.model_label, n.issues_posted, n.suggestions_posted, n.received_at, n.delivery_id`

// queuedFrom is the queue: the notes neither consolidated nor failed, with
// their names. A note of a repository whose baseline run has started waits
// for the run: the run pins its commit when it starts, and no step may read
// a page holding facts newer than that commit. A run still queued holds
// nothing; a long queue of runs must not hold the notes for hours.
const queuedFrom = `notes n JOIN repos r ON r.id = n.repo_id JOIN projects p ON p.id = r.project_id
	WHERE n.consolidated_at IS NULL AND n.fold_failed_at IS NULL
	  AND NOT EXISTS (` + startedRunOfNote + `)`

func scanQueued(row interface{ Scan(...any) error }) (QueuedNote, error) {
	var q QueuedNote
	var bugfix, note []byte
	var ticket, issueType, kind, area, comp, reviewed, merge, model *string
	var runID *int64
	if err := row.Scan(&q.ProjectKey, &q.RepoSlug, &q.PRID, &q.Team, &q.PRKey, &q.PRTitle, &ticket, &issueType, &kind,
		&bugfix, &area, &comp, &note, &reviewed, &q.SourceCommit, &merge, timeAt(&q.MergedAt),
		&runID, &model, &q.IssuesPosted, &q.Suggestions, timeAt(&q.ReceivedAt), &q.DeliveryID); err != nil {
		return QueuedNote{}, err
	}
	q.TicketKey, q.IssueType, q.ChangeKind = deref(ticket), deref(issueType), deref(kind)
	q.BugfixArea, q.BugfixComponent, q.ReviewedCommit, q.MergeCommit, q.ModelLabel = deref(area), deref(comp), deref(reviewed), deref(merge), deref(model)
	q.PRTitle, q.Bugfix, q.Body = noteOut(q.PRTitle, bugfix, note)
	if runID != nil {
		q.RunID = *runID
	}
	return q, nil
}

// NextQueued is the oldest note on the queue received at or before
// dueBefore, ok false when none. The oldest note of all is its
// repository's oldest too, so a repository is due once its oldest note
// is: the worker passes now less BACKEND_FOLD_DELAY, and a repository's
// notes wait for the delay from the first of them, not from each.
func (s *Store) NextQueued(ctx context.Context, dueBefore time.Time) (QueuedNote, bool, error) {
	q, err := scanQueued(s.db.QueryRowContext(ctx, `SELECT `+queuedColumns+` FROM `+queuedFrom+`
		   AND n.received_at <= ?
		 ORDER BY n.received_at, n.pr_id LIMIT 1`, stamp(dueBefore)))
	if errors.Is(err, sql.ErrNoRows) {
		return QueuedNote{}, false, nil
	}
	if err != nil {
		return QueuedNote{}, false, err
	}
	return q, true, nil
}

// QueuedBatch is every note on the queue of one repository, in the order
// their pull requests were merged (received, for one without a merge
// time): what one fold takes together. The worker cuts it to what one call
// may show (FoldBatchBytes); the rest stays queued for the next fold.
func (s *Store) QueuedBatch(ctx context.Context, project, repo string) ([]QueuedNote, error) {
	rid, ok, err := repoID(ctx, s.db, project, repo)
	if err != nil || !ok {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+queuedColumns+` FROM `+queuedFrom+`
		   AND n.repo_id = ?
		 ORDER BY COALESCE(n.merged_at, n.received_at), n.received_at, n.pr_id`, rid)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []QueuedNote
	for rows.Next() {
		q, err := scanQueued(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
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

// DeferFold counts a failed gateway call against the note delivered by
// delivery and leaves it queued; returns the attempts so far. The worker
// gives up past its bound. A note replaced since by a later delivery for
// the same PR is not counted against: 0 attempts, nil error, which no
// counted defer returns. No note for the PR at all, or no delivery named,
// is sql.ErrNoRows.
func (s *Store) DeferFold(ctx context.Context, project, repo string, prID int, delivery string) (int, error) {
	if delivery == "" {
		return 0, sql.ErrNoRows
	}
	rid, ok, err := repoID(ctx, s.db, project, repo)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, sql.ErrNoRows
	}
	var n int
	err = s.db.QueryRowContext(ctx, `UPDATE notes SET fold_attempts = fold_attempts + 1
		WHERE repo_id = ? AND pr_id = ? AND delivery_id = ? RETURNING fold_attempts`, rid, prID, delivery).Scan(&n)
	if !errors.Is(err, sql.ErrNoRows) {
		return n, err
	}
	var replaced bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM notes WHERE repo_id = ? AND pr_id = ?)`, rid, prID).Scan(&replaced); err != nil {
		return 0, err
	}
	if !replaced {
		return 0, sql.ErrNoRows
	}
	return 0, nil
}

// MarkConsolidated takes the note delivered by delivery off the queue,
// folded or refused. A note replaced while it was folded stays queued: the
// fold read the old one.
func (s *Store) MarkConsolidated(ctx context.Context, project, repo string, prID int, delivery string) error {
	rid, ok, err := repoID(ctx, s.db, project, repo)
	if err != nil || !ok {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE notes SET consolidated_at = ? WHERE repo_id = ? AND pr_id = ? AND delivery_id = ?`,
		nowStamp(), rid, prID, delivery)
	return err
}

// MarkFoldFailed takes the note delivered by delivery off the queue as
// failed, with its reason, with count the attempt counted (false: DeferFold
// counted it), the attempts returned: not folded, not consolidated, kept
// for RequeueFailed. A note consolidated or replaced
// meanwhile is left alone, 0 returned: the fold read the old one. The
// reason is redacted on the way in, as on the way out: an error may echo
// what the call carried.
func (s *Store) MarkFoldFailed(ctx context.Context, project, repo string, prID int, delivery, reason string, count bool) (int, error) {
	rid, ok, err := repoID(ctx, s.db, project, repo)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, sql.ErrNoRows
	}
	add := 0
	if count {
		add = 1
	}
	var n int
	err = s.db.QueryRowContext(ctx, `UPDATE notes SET fold_failed_at = ?, fold_failed = ?, fold_attempts = fold_attempts + ?
		WHERE repo_id = ? AND pr_id = ? AND delivery_id = ? AND consolidated_at IS NULL RETURNING fold_attempts`,
		nowStamp(), redact.Text(reason), add, rid, prID, delivery).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return n, err
}

// RequeueFailed puts a repository's failed notes back on the queue, those
// under maxAttempts; returns how many. The worker calls it when one of the
// repository's runs ended: the page was written anew, the note may fold now.
func (s *Store) RequeueFailed(ctx context.Context, project, repo string, maxAttempts int) (int, error) {
	rid, ok, err := repoID(ctx, s.db, project, repo)
	if err != nil || !ok {
		return 0, err
	}
	n, err := affected(s.db.ExecContext(ctx, `UPDATE notes SET fold_failed_at = NULL, fold_failed = NULL
		WHERE repo_id = ? AND fold_failed_at IS NOT NULL AND consolidated_at IS NULL AND fold_attempts < ?`, rid, maxAttempts))
	return int(n), err
}

// RequeueFailedBefore puts every failed note that failed before a time
// back on the queue, those under maxAttempts; returns how many. The worker
// calls it on every drain with a day's age: a note no run requeues (the
// sources off, a repository without runs) is tried again by time.
func (s *Store) RequeueFailedBefore(ctx context.Context, before time.Time, maxAttempts int) (int, error) {
	n, err := affected(s.db.ExecContext(ctx, `UPDATE notes SET fold_failed_at = NULL, fold_failed = NULL
		WHERE fold_failed_at < ? AND fold_failed_at IS NOT NULL AND consolidated_at IS NULL AND fold_attempts < ?`, stamp(before), maxAttempts))
	return int(n), err
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
	var statements, drawn, repoDrawn, batch []byte
	err = s.db.QueryRowContext(ctx, `
		SELECT pj.key, COALESCE(rp.slug, ''), r.id, r.revision, r.summary, r.statements, r.source_kind,
		       COALESCE(r.source_pr_key, ''), COALESCE(r.source_commit, ''), p.updated_at, r.map,
		       r.repo_map, COALESCE(r.repo_map_basis, ''), COALESCE(r.shape, ''), r.batch_pr_keys
		  FROM pages p JOIN revisions r ON r.id = p.current_revision_id
		  JOIN projects pj ON pj.id = p.project_id LEFT JOIN repos rp ON rp.id = p.repo_id
		 WHERE p.scope = ? AND p.project_id = ? AND p.repo_id = ?`, scope, pid, rid).Scan(
		&p.ProjectKey, &p.RepoSlug, &p.RevisionID, &p.Revision, &p.Summary, &statements, &p.SourceKind, &p.SourcePR,
		&p.SourceCommit, timeAt(&p.UpdatedAt), &drawn, &repoDrawn, &p.RepoMapBasis, &p.Shape, &batch)
	if errors.Is(err, sql.ErrNoRows) {
		return Page{}, false, nil
	}
	if err != nil {
		return Page{}, false, err
	}
	if err := json.Unmarshal(statements, &p.Statements); err != nil {
		return Page{}, false, err
	}
	if p.BatchPRKeys, err = keysOf(batch); err != nil {
		return Page{}, false, err
	}
	if p.Statements == nil {
		p.Statements = []Statement{}
	}
	if p.Map, err = diagramOf(drawn); err != nil {
		return Page{}, false, err
	}
	if p.RepoMap, err = diagramOf(repoDrawn); err != nil {
		return Page{}, false, err
	}
	return p, true, nil
}

// Revision is one revision of a page by its number, ok false when the page
// or the number is unknown: the summary and statements as they stood, the
// source that wrote them, when, and a project page's map and shape: a
// revision written for those alone says so. No repository map: the map
// step sets that on the current revision in place, it writes none.
// Any name the project or repository ever had finds it; the page carries
// the current ones.
func (s *Store) Revision(ctx context.Context, scope, project, repo string, n int) (Page, bool, error) {
	pid, rid, ok, err := scopeIDs(ctx, s.db, project, repo)
	if err != nil || !ok {
		return Page{}, false, err
	}
	p := Page{Scope: scope}
	var statements, drawn, batch []byte
	err = s.db.QueryRowContext(ctx, `
		SELECT pj.key, COALESCE(rp.slug, ''), r.id, r.revision, r.summary, r.statements, r.source_kind,
		       COALESCE(r.source_pr_key, ''), COALESCE(r.source_commit, ''), r.created_at, r.map, COALESCE(r.shape, ''),
		       r.batch_pr_keys
		  FROM revisions r JOIN projects pj ON pj.id = r.project_id LEFT JOIN repos rp ON rp.id = r.repo_id
		 WHERE r.scope = ? AND r.project_id = ? AND COALESCE(r.repo_id, 0) = ? AND r.revision = ?`, scope, pid, rid, n).Scan(
		&p.ProjectKey, &p.RepoSlug, &p.RevisionID, &p.Revision, &p.Summary, &statements, &p.SourceKind, &p.SourcePR,
		&p.SourceCommit, timeAt(&p.UpdatedAt), &drawn, &p.Shape, &batch)
	if errors.Is(err, sql.ErrNoRows) {
		return Page{}, false, nil
	}
	if err != nil {
		return Page{}, false, err
	}
	if err := json.Unmarshal(statements, &p.Statements); err != nil {
		return Page{}, false, err
	}
	if p.BatchPRKeys, err = keysOf(batch); err != nil {
		return Page{}, false, err
	}
	if p.Statements == nil {
		p.Statements = []Statement{}
	}
	if p.Map, err = diagramOf(drawn); err != nil {
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
	// Shape is a project revision's judgment of the project (ShapeSystem,
	// ShapeCollection); "" carries the page's current one, as insertRevision
	// reads it, so a writer that judges nothing (a note's fold, a map, a
	// departure) never resets it. Ignored on a repository revision.
	Shape string
	// BatchPRKeys is every pull request a fold of several notes took, in
	// merge order, SourcePR the first; stored for two or more (batchKeys).
	BatchPRKeys []string
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
	batch, err := batchKeys(rev.BatchPRKeys)
	if err != nil {
		return 0, err
	}
	now := nowStamp()
	var id int64
	var n int
	// A repository revision takes the page's map and its basis, read in
	// this transaction: a merge or a run's describe step changes the text,
	// never the map, and the basis then says the map is due again. On a
	// project revision both stay NULL: the subqueries' scope never matches
	// a project page. A project revision's shape, when its writer judged
	// none, is the page's current one, read the same way.
	err = tx.QueryRowContext(ctx, `
		INSERT INTO revisions (scope, project_id, repo_id, revision, summary, statements, chars,
		                       source_kind, source_pr_key, source_commit, llm_call_id, baseline_run_id, map,
		                       repo_map, repo_map_basis, created_at, shape, batch_pr_keys)
		VALUES (?1, ?2, ?3,
		    (SELECT COALESCE(MAX(revision), 0) + 1 FROM revisions
		      WHERE scope = ?1 AND project_id = ?2 AND COALESCE(repo_id, 0) = ?4),
		    ?5, ?6, ?7, ?8, NULLIF(?9, ''), NULLIF(?10, ''), ?11, ?12, ?13,
		    (SELECT r.repo_map FROM pages p JOIN revisions r ON r.id = p.current_revision_id
		      WHERE ?1 = 'repository' AND p.scope = ?1 AND p.project_id = ?2 AND p.repo_id = ?4),
		    (SELECT r.repo_map_basis FROM pages p JOIN revisions r ON r.id = p.current_revision_id
		      WHERE ?1 = 'repository' AND p.scope = ?1 AND p.project_id = ?2 AND p.repo_id = ?4),
		    ?14,
		    CASE WHEN ?1 = 'project' THEN COALESCE(NULLIF(?15, ''),
		        (SELECT r.shape FROM pages p JOIN revisions r ON r.id = p.current_revision_id
		          WHERE p.scope = 'project' AND p.project_id = ?2 AND p.repo_id = 0)) END,
		    ?16)
		RETURNING id, revision`,
		rev.Scope, pid, repo, rid, rev.Summary, statements, rev.Chars,
		fromKind(rev.SourceKind), rev.SourcePR, rev.SourceCommit, rev.LLMCallID, rev.BaselineRunID, drawn, now, rev.Shape, batch).Scan(&id, &n)
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

// keysOf reads a stored batch_pr_keys column; NULL is nil.
func keysOf(raw []byte) ([]string, error) {
	if raw == nil {
		return nil, nil
	}
	var keys []string
	if err := json.Unmarshal(raw, &keys); err != nil {
		return nil, err
	}
	return keys, nil
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
	// PurposeProjectMap is the system map asked again alone, the project
	// step's description taken.
	PurposeProjectMap = "project_map"
	// PurposeRestyle is a restyle run's call, the repository's page or the
	// project's (its step says which).
	PurposeRestyle = "restyle"
	// PurposeBrief is a repository's brief written from its page, the
	// call's source FromPage.
	PurposeBrief = "brief"
)

// FromPage is a call's source when it read a stored page alone: a brief.
// A call's only, never a revision's or a statement's.
const FromPage = "page"

// LLMCall is one model call as llmwire described it. SourceKind "" and
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
	// Streamed: the answer or its reasoning had begun to arrive, so the
	// gateway billed the call whatever its outcome.
	Streamed bool
	// BatchPRKeys is every pull request a merge folded together, in merge
	// order, SourcePR the first; stored for two or more (batchKeys).
	BatchPRKeys []string
}

// batchKeys is a fold's PR keys as stored: a JSON array for two or more,
// NULL for one or none.
func batchKeys(keys []string) (any, error) {
	if len(keys) < 2 {
		return nil, nil
	}
	return jsonOf(keys)
}

// InsertLLMCall records one model call and returns its id.
func (s *Store) InsertLLMCall(ctx context.Context, c LLMCall) (int64, error) {
	purpose := c.Purpose
	if purpose == "" {
		purpose = PurposeMerge
	}
	batch, err := batchKeys(c.BatchPRKeys)
	if err != nil {
		return 0, err
	}
	var id int64
	err = s.inTx(ctx, func(tx *sql.Tx) error {
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
			                       outcome, model, input_tokens, cached_tokens, output_tokens, cost_nano_usd, call_id, created_at,
			                       batch_pr_keys, streamed)
			VALUES (?, ?, NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?, NULLIF(?, ''), ?, ?, ?)
			RETURNING id`,
			rid, fromKind(c.SourceKind), c.SourcePR, c.SourceCommit, purpose, c.BaselineRunID, c.Step,
			c.Outcome, c.Model, c.InputTokens, c.CachedTokens, c.OutputTokens, c.CostNanoUSD, c.CallID, nowStamp(), batch,
			c.Streamed).Scan(&id)
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
