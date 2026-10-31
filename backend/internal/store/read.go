package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// The read side: what the API serves. Structs, never maps.

// ProjectRow is one project in the list.
type ProjectRow struct {
	Key       string
	Team      string
	Repos     int
	Notes     int
	Revision  int
	LastMerge *time.Time
}

// What the wiki lists. With the sources on (sourced), a project is a source
// and a repository a source repository: the source is the starting point,
// and a project noergler named without one is no page anyone reaches. The
// team is noergler's, shown when it has delivered for the key. With the
// sources off, the wiki is noergler's projects and repositories, as before.
func projectsFrom(sourced bool) string {
	if sourced {
		return `(SELECT s.project_key AS key, COALESCE(p.team, '') AS team FROM sources s LEFT JOIN projects p ON p.key = s.project_key)`
	}
	return `(SELECT key, team FROM projects)`
}

func reposFrom(sourced bool) string {
	if sourced {
		return `(SELECT project_key, slug FROM source_repos)`
	}
	return `(SELECT project_key, slug FROM repos)`
}

// Projects lists every project with its repo and note counts and its
// current project-revision number (0 when none).
func (s *Store) Projects(ctx context.Context, sourced bool) ([]ProjectRow, error) {
	//nolint:gosec // G202: constant fragments of this file, no value spliced in
	rows, err := s.db.QueryContext(ctx, `
		SELECT p.key, p.team,
		       (SELECT COUNT(*) FROM `+reposFrom(sourced)+` r WHERE r.project_key = p.key),
		       (SELECT COUNT(*) FROM notes n WHERE n.project_key = p.key),
		       COALESCE((SELECT rv.revision FROM pages pg JOIN revisions rv ON rv.id = pg.current_revision_id
		                  WHERE pg.scope = 'project' AND pg.project_key = p.key AND pg.repo_slug = ''), 0),
		       (SELECT MAX(n.merged_at) FROM notes n WHERE n.project_key = p.key)
		  FROM `+projectsFrom(sourced)+` p ORDER BY p.key`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []ProjectRow{}
	for rows.Next() {
		var r ProjectRow
		if err := rows.Scan(&r.Key, &r.Team, &r.Repos, &r.Notes, &r.Revision, timeOrNil(&r.LastMerge)); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RepoRow is one repository of a project.
type RepoRow struct {
	Slug      string
	Revision  int
	Summary   string
	Notes     int
	LastMerge *time.Time
}

// RepoSummary is a source repository and its current summary, whole.
type RepoSummary struct {
	Slug    string
	Summary string
}

// RepoSummaries lists a project's source repositories by slug with the
// summary of their current description, "" before the first: what the
// project step reads of the others, without the note counts of Repos.
func (s *Store) RepoSummaries(ctx context.Context, project string) ([]RepoSummary, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT r.slug, COALESCE(rv.summary, '')
		  FROM source_repos r
		  LEFT JOIN pages pg ON pg.scope = 'repository' AND pg.project_key = r.project_key AND pg.repo_slug = r.slug
		  LEFT JOIN revisions rv ON rv.id = pg.current_revision_id
		 WHERE r.project_key = ? ORDER BY r.slug`, project)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []RepoSummary{}
	for rows.Next() {
		var r RepoSummary
		if err := rows.Scan(&r.Slug, &r.Summary); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Repos lists a project's repositories with the summary of their current
// description, whole.
func (s *Store) Repos(ctx context.Context, project string, sourced bool) ([]RepoRow, error) {
	//nolint:gosec // G202: constant fragments of this file, no value spliced in
	rows, err := s.db.QueryContext(ctx, `
		SELECT r.slug,
		       COALESCE(rv.revision, 0), COALESCE(rv.summary, ''),
		       (SELECT COUNT(*) FROM notes n WHERE n.project_key = r.project_key AND n.repo_slug = r.slug),
		       (SELECT MAX(n.merged_at) FROM notes n WHERE n.project_key = r.project_key AND n.repo_slug = r.slug)
		  FROM `+reposFrom(sourced)+` r
		  LEFT JOIN pages pg ON pg.scope = 'repository' AND pg.project_key = r.project_key AND pg.repo_slug = r.slug
		  LEFT JOIN revisions rv ON rv.id = pg.current_revision_id
		 WHERE r.project_key = ? ORDER BY r.slug`, project)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []RepoRow{}
	for rows.Next() {
		var r RepoRow
		if err := rows.Scan(&r.Slug, &r.Revision, &r.Summary, &r.Notes, timeOrNil(&r.LastMerge)); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ProjectExists reports whether the project is listed, and its team.
func (s *Store) ProjectExists(ctx context.Context, key string, sourced bool) (string, bool, error) {
	var team string
	err := s.db.QueryRowContext(ctx, `SELECT team FROM `+projectsFrom(sourced)+` p WHERE key = ?`, key).Scan(&team)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return team, err == nil, err
}

// RepoExists reports whether the repo is listed.
func (s *Store) RepoExists(ctx context.Context, project, slug string, sourced bool) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM `+reposFrom(sourced)+` r WHERE project_key = ? AND slug = ?`, project, slug).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// RevisionRow is one entry of a page's history.
type RevisionRow struct {
	Revision int
	// SourceKind says which of SourcePR and SourceCommit made the revision.
	SourceKind   string
	SourcePR     string
	SourceCommit string
	Statements   int
	Chars        int
	CreatedAt    time.Time
}

// Revisions is a page's history, newest first.
func (s *Store) Revisions(ctx context.Context, scope, project, repo string, limit int) ([]RevisionRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT revision, source_kind, COALESCE(source_pr_key, ''), COALESCE(source_commit, ''),
		       json_array_length(statements), chars, created_at
		  FROM revisions WHERE scope = ? AND project_key = ? AND COALESCE(repo_slug, '') = ?
		 ORDER BY revision DESC LIMIT ?`, scope, project, repo, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []RevisionRow{}
	for rows.Next() {
		var r RevisionRow
		if err := rows.Scan(&r.Revision, &r.SourceKind, &r.SourcePR, &r.SourceCommit, &r.Statements, &r.Chars, timeAt(&r.CreatedAt)); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// NoteRow is one note as listed.
type NoteRow struct {
	PRID        int
	PRKey       string
	PRTitle     string
	RepoSlug    string
	TicketKey   string
	ChangeKind  string
	MergedAt    time.Time
	Items       int
	Issues      int
	Suggestions int
}

// itemsSQL counts a note's components, rules, invariants and entities. Only
// an array is counted: a missing key, a JSON null or a scalar is zero,
// never an error, and one such note never breaks a list it appears in.
var itemsSQL = `(` + arrayLen("components") + ` + ` + arrayLen("rules") + ` + ` + arrayLen("invariants") + ` + ` + arrayLen("entities") + `)`

func arrayLen(key string) string {
	return `CASE WHEN json_type(note, '$.` + key + `') = 'array' THEN json_array_length(note, '$.` + key + `') ELSE 0 END`
}

// Notes lists the notes of a repo (repo != "") or a project, newest first.
func (s *Store) Notes(ctx context.Context, project, repo string, limit int) ([]NoteRow, error) {
	//nolint:gosec // G202: constant fragments of this file, no value spliced in
	rows, err := s.db.QueryContext(ctx, `
		SELECT pr_id, pr_key, pr_title, repo_slug, COALESCE(ticket_key, ''), COALESCE(change_kind, ''), merged_at,
		       `+itemsSQL+`,
		       issues_posted, suggestions_posted
		  FROM notes WHERE project_key = ?1 AND (?2 = '' OR repo_slug = ?2)
		 ORDER BY merged_at DESC LIMIT ?3`, project, repo, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []NoteRow{}
	for rows.Next() {
		var r NoteRow
		if err := rows.Scan(&r.PRID, &r.PRKey, &r.PRTitle, &r.RepoSlug, &r.TicketKey, &r.ChangeKind, timeAt(&r.MergedAt), &r.Items, &r.Issues, &r.Suggestions); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// NoteDetail is one note in full.
type NoteDetail struct {
	Note
	// Items is the count the list shows: components, rules, invariants and
	// entities.
	Items          int
	ReceivedAt     time.Time
	ConsolidatedAt *time.Time
	Call           *NoteCallRow
}

// NoteCallRow is the recorded note call, the lanes a reader wants.
type NoteCallRow struct {
	Outcome         string
	NoteStatus      string
	Model           string
	InputTokens     *int64
	CacheReadTokens *int64
	OutputTokens    *int64
	CostNanoUSD     *int64
	// Rounds is every note call the PR made, summed by the sender.
	Rounds             int
	RoundsInputTokens  int64
	RoundsOutputTokens int64
	RoundsCost         *int64
}

// NoteByKey reads one note, ok false when none.
func (s *Store) NoteByKey(ctx context.Context, project, repo string, prID int) (NoteDetail, bool, error) {
	var d NoteDetail
	var bugfix, note []byte
	var ticket, issueType, kind, area, comp, reviewed, merge, model *string
	var runID *int64
	err := s.db.QueryRowContext(ctx, `
		SELECT project_key, repo_slug, pr_id, team, pr_key, pr_title, ticket_key, jira_issue_type, change_kind,
		       bugfix, bugfix_area, bugfix_component, note, reviewed_commit, source_commit, merge_commit, merged_at,
		       run_id, model_label, issues_posted, suggestions_posted, received_at, consolidated_at, `+itemsSQL+`
		  FROM notes WHERE project_key = ? AND repo_slug = ? AND pr_id = ?`, project, repo, prID).Scan(
		&d.ProjectKey, &d.RepoSlug, &d.PRID, &d.Team, &d.PRKey, &d.PRTitle, &ticket, &issueType, &kind,
		&bugfix, &area, &comp, &note, &reviewed, &d.SourceCommit, &merge, timeAt(&d.MergedAt),
		&runID, &model, &d.IssuesPosted, &d.Suggestions, timeAt(&d.ReceivedAt), timeOrNil(&d.ConsolidatedAt), &d.Items)
	if errors.Is(err, sql.ErrNoRows) {
		return NoteDetail{}, false, nil
	}
	if err != nil {
		return NoteDetail{}, false, err
	}
	d.TicketKey, d.IssueType, d.ChangeKind = deref(ticket), deref(issueType), deref(kind)
	d.BugfixArea, d.BugfixComponent, d.ReviewedCommit, d.MergeCommit, d.ModelLabel = deref(area), deref(comp), deref(reviewed), deref(merge), deref(model)
	d.Bugfix, d.Body = json.RawMessage(bugfix), json.RawMessage(note)
	if runID != nil {
		d.RunID = *runID
	}
	var c NoteCallRow
	var cm *string
	err = s.db.QueryRowContext(ctx, `
		SELECT COALESCE(outcome, ''), COALESCE(note_status, ''), model, input_tokens, cache_read_tokens, output_tokens,
		       cost_nano_usd, rounds, rounds_input_tokens, rounds_output_tokens, rounds_cost_nano_usd
		  FROM note_calls WHERE project_key = ? AND repo_slug = ? AND pr_id = ? ORDER BY id DESC LIMIT 1`,
		project, repo, prID).Scan(&c.Outcome, &c.NoteStatus, &cm, &c.InputTokens, &c.CacheReadTokens, &c.OutputTokens,
		&c.CostNanoUSD, &c.Rounds, &c.RoundsInputTokens, &c.RoundsOutputTokens, &c.RoundsCost)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return NoteDetail{}, false, err
	default:
		c.Model = deref(cm)
		d.Call = &c
	}
	return d, true, nil
}

// ProblemArea is one cluster of bugfixes: a repo and the directory the
// fixes' components share, with the cause class seen most and the posted
// findings on those PRs.
type ProblemArea struct {
	RepoSlug    string
	Area        string
	Component   string
	Bugfixes    int
	TopCause    string
	TopCauseN   int
	Issues      int
	Suggestions int
	LastFixPR   string
	LastFixAt   time.Time
}

// Problems lists the project's problem areas within the window, most
// bugfixes first. Grouped by the files' shared directory, never by the
// model's component name, which varies in spelling; the name shown is the
// most recent one.
func (s *Store) Problems(ctx context.Context, project string, days int) ([]ProblemArea, error) {
	since := stamp(time.Now().Add(-time.Duration(days) * 24 * time.Hour))
	rows, err := s.db.QueryContext(ctx, `
		WITH fixes AS (
		    SELECT repo_slug, COALESCE(bugfix_area, '') AS area, COALESCE(bugfix_component, '') AS component,
		           COALESCE(bugfix ->> '$.cause_kind', 'other') AS cause, merged_at, pr_key, issues_posted, suggestions_posted
		      FROM notes
		     WHERE project_key = ? AND change_kind = 'bugfix' AND merged_at >= ?
		),
		causes AS (
		    SELECT repo_slug, area, cause, COUNT(*) AS n,
		           ROW_NUMBER() OVER (PARTITION BY repo_slug, area ORDER BY COUNT(*) DESC, cause) AS rn
		      FROM fixes GROUP BY repo_slug, area, cause
		),
		latest AS (
		    SELECT repo_slug, area, component, pr_key, merged_at,
		           ROW_NUMBER() OVER (PARTITION BY repo_slug, area ORDER BY merged_at DESC) AS rn
		      FROM fixes
		)
		SELECT f.repo_slug, f.area, l.component, COUNT(*), c.cause, c.n, SUM(f.issues_posted), SUM(f.suggestions_posted), l.pr_key, l.merged_at
		  FROM fixes f
		  JOIN causes c ON c.repo_slug = f.repo_slug AND c.area = f.area AND c.rn = 1
		  JOIN latest l ON l.repo_slug = f.repo_slug AND l.area = f.area AND l.rn = 1
		 GROUP BY f.repo_slug, f.area, l.component, c.cause, c.n, l.pr_key, l.merged_at
		 ORDER BY COUNT(*) DESC, l.merged_at DESC`, project, since)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []ProblemArea{}
	for rows.Next() {
		var p ProblemArea
		if err := rows.Scan(&p.RepoSlug, &p.Area, &p.Component, &p.Bugfixes, &p.TopCause, &p.TopCauseN, &p.Issues, &p.Suggestions, &p.LastFixPR, timeAt(&p.LastFixAt)); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
