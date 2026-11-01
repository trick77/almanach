package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/trick77/almanach/internal/redact"
)

// The read side: what the API serves. Structs, never maps.

// noteOut is a stored note's free text as it may leave the store, for a
// reader or a model: the row keeps what noergler sent (deliveries and notes
// are verbatim), every read of it passes redact.
func noteOut(title string, bugfix, body []byte) (string, json.RawMessage, json.RawMessage) {
	return redact.Text(title), redact.JSON(bugfix), redact.JSON(body)
}

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
// sources off, the wiki is noergler's projects and repositories, as before:
// the rows noergler delivered for (first_seen set), not the ones only a
// claim or a source registered.
func projectsFrom(sourced bool) string {
	if sourced {
		return `(SELECT p.id, p.key, COALESCE(p.team, '') AS team FROM sources s JOIN projects p ON p.id = s.project_id)`
	}
	return `(SELECT id, key, team FROM projects WHERE first_seen IS NOT NULL)`
}

func reposFrom(sourced bool) string {
	if sourced {
		return `(SELECT r.id, r.project_id, r.slug FROM source_repos sr JOIN repos r ON r.id = sr.repo_id)`
	}
	return `(SELECT id, project_id, slug FROM repos WHERE first_seen IS NOT NULL)`
}

// Projects lists every project with its repo and note counts and its
// current project-revision number (0 when none).
func (s *Store) Projects(ctx context.Context, sourced bool) ([]ProjectRow, error) {
	//nolint:gosec // G202: constant fragments of this file, no value spliced in
	rows, err := s.db.QueryContext(ctx, `
		SELECT p.key, p.team,
		       (SELECT COUNT(*) FROM `+reposFrom(sourced)+` r WHERE r.project_id = p.id),
		       (SELECT COUNT(*) FROM notes n JOIN repos nr ON nr.id = n.repo_id WHERE nr.project_id = p.id),
		       COALESCE((SELECT rv.revision FROM pages pg JOIN revisions rv ON rv.id = pg.current_revision_id
		                  WHERE pg.scope = 'project' AND pg.project_id = p.id AND pg.repo_id = 0), 0),
		       (SELECT MAX(n.merged_at) FROM notes n JOIN repos nr ON nr.id = n.repo_id WHERE nr.project_id = p.id)
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
	pid, ok, err := projectID(ctx, s.db, project)
	if err != nil || !ok {
		return []RepoSummary{}, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT r.slug, COALESCE(rv.summary, '')
		  FROM source_repos sr JOIN repos r ON r.id = sr.repo_id
		  LEFT JOIN pages pg ON pg.scope = 'repository' AND pg.project_id = r.project_id AND pg.repo_id = r.id
		  LEFT JOIN revisions rv ON rv.id = pg.current_revision_id
		 WHERE sr.project_id = ? ORDER BY r.slug`, pid)
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
		r.Summary = relaid(r.Summary)
		out = append(out, r)
	}
	return out, rows.Err()
}

// Repos lists a project's repositories with the summary of their current
// description, whole.
func (s *Store) Repos(ctx context.Context, project string, sourced bool) ([]RepoRow, error) {
	pid, ok, err := projectID(ctx, s.db, project)
	if err != nil || !ok {
		return []RepoRow{}, err
	}
	//nolint:gosec // G202: constant fragments of this file, no value spliced in
	rows, err := s.db.QueryContext(ctx, `
		SELECT r.slug,
		       COALESCE(rv.revision, 0), COALESCE(rv.summary, ''),
		       (SELECT COUNT(*) FROM notes n WHERE n.repo_id = r.id),
		       (SELECT MAX(n.merged_at) FROM notes n WHERE n.repo_id = r.id)
		  FROM `+reposFrom(sourced)+` r
		  LEFT JOIN pages pg ON pg.scope = 'repository' AND pg.project_id = r.project_id AND pg.repo_id = r.id
		  LEFT JOIN revisions rv ON rv.id = pg.current_revision_id
		 WHERE r.project_id = ? ORDER BY r.slug`, pid)
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
		r.Summary = relaid(r.Summary)
		out = append(out, r)
	}
	return out, rows.Err()
}

// SingleRepos is the repository of every project of one (a source with one
// repository), by the project's current key, in one query: what the
// project list shows for such a project, its repository's page being the
// project's.
func (s *Store) SingleRepos(ctx context.Context) (map[string]RepoRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT p.key, r.slug,
		       COALESCE(rv.revision, 0), COALESCE(rv.summary, ''),
		       (SELECT COUNT(*) FROM notes n WHERE n.repo_id = r.id),
		       (SELECT MAX(n.merged_at) FROM notes n WHERE n.repo_id = r.id),
		       (SELECT COUNT(*) FROM source_repos x WHERE x.project_id = s.project_id)
		  FROM sources s
		  JOIN projects p ON p.id = s.project_id
		  JOIN source_repos sr ON sr.project_id = s.project_id
		  JOIN repos r ON r.id = sr.repo_id
		  LEFT JOIN pages pg ON pg.scope = 'repository' AND pg.project_id = r.project_id AND pg.repo_id = r.id
		  LEFT JOIN revisions rv ON rv.id = pg.current_revision_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]RepoRow{}
	for rows.Next() {
		var key string
		var r RepoRow
		var n int
		if err := rows.Scan(&key, &r.Slug, &r.Revision, &r.Summary, &r.Notes, timeOrNil(&r.LastMerge), &n); err != nil {
			return nil, err
		}
		r.Summary = relaid(r.Summary)
		// The one rule, not a copy of it in SQL.
		if Single(n) {
			out[key] = r
		}
	}
	return out, rows.Err()
}

// ProjectExists reports whether the project is listed, and its team.
// Any key the project ever had finds it.
func (s *Store) ProjectExists(ctx context.Context, key string, sourced bool) (string, bool, error) {
	pid, ok, err := projectID(ctx, s.db, key)
	if err != nil || !ok {
		return "", false, err
	}
	var team string
	err = s.db.QueryRowContext(ctx, `SELECT team FROM `+projectsFrom(sourced)+` p WHERE id = ?`, pid).Scan(&team)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return team, err == nil, err
}

// RepoExists reports whether the repo is listed. Any name it ever had
// finds it.
func (s *Store) RepoExists(ctx context.Context, project, slug string, sourced bool) (bool, error) {
	rid, ok, err := repoID(ctx, s.db, project, slug)
	if err != nil || !ok {
		return false, err
	}
	var one int
	err = s.db.QueryRowContext(ctx, `SELECT 1 FROM `+reposFrom(sourced)+` r WHERE id = ?`, rid).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// scopeIDs resolves a page's names: the project, and the repository unless
// repo is "" (the project scope, repository id 0). ok false when a name
// was never used.
func scopeIDs(ctx context.Context, q rowQuerier, project, repo string) (pid, rid int64, ok bool, err error) {
	if repo == "" {
		pid, ok, err = projectID(ctx, q, project)
		return pid, 0, ok, err
	}
	err = q.QueryRowContext(ctx, `
		SELECT rn.project_id, rn.repo_id FROM repo_names rn JOIN project_names pn ON pn.project_id = rn.project_id
		 WHERE pn.key = ? AND rn.slug = ?`, project, repo).Scan(&pid, &rid)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, false, nil
	}
	return pid, rid, err == nil, err
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
	pid, rid, ok, err := scopeIDs(ctx, s.db, project, repo)
	if err != nil || !ok {
		return []RevisionRow{}, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT revision, source_kind, COALESCE(source_pr_key, ''), COALESCE(source_commit, ''),
		       json_array_length(statements), chars, created_at
		  FROM revisions WHERE scope = ? AND project_id = ? AND COALESCE(repo_id, 0) = ?
		 ORDER BY revision DESC LIMIT ?`, scope, pid, rid, limit)
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
	pid, rid, ok, err := scopeIDs(ctx, s.db, project, repo)
	if err != nil || !ok {
		return []NoteRow{}, err
	}
	//nolint:gosec // G202: constant fragments of this file, no value spliced in
	rows, err := s.db.QueryContext(ctx, `
		SELECT n.pr_id, n.pr_key, n.pr_title, r.slug, COALESCE(n.ticket_key, ''), COALESCE(n.change_kind, ''), n.merged_at,
		       `+itemsSQL+`,
		       n.issues_posted, n.suggestions_posted
		  FROM notes n JOIN repos r ON r.id = n.repo_id
		 WHERE r.project_id = ?1 AND (?2 = 0 OR n.repo_id = ?2)
		 ORDER BY n.merged_at DESC LIMIT ?3`, pid, rid, limit)
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
		r.PRTitle = redact.Text(r.PRTitle)
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
	rid, ok, err := repoID(ctx, s.db, project, repo)
	if err != nil || !ok {
		return NoteDetail{}, false, err
	}
	err = s.db.QueryRowContext(ctx, `
		SELECT p.key, r.slug, n.pr_id, n.team, n.pr_key, n.pr_title, n.ticket_key, n.jira_issue_type, n.change_kind,
		       n.bugfix, n.bugfix_area, n.bugfix_component, n.note, n.reviewed_commit, n.source_commit, n.merge_commit,
		       n.merged_at, n.run_id, n.model_label, n.issues_posted, n.suggestions_posted, n.received_at,
		       n.consolidated_at, `+itemsSQL+`
		  FROM notes n JOIN repos r ON r.id = n.repo_id JOIN projects p ON p.id = r.project_id
		 WHERE n.repo_id = ? AND n.pr_id = ?`, rid, prID).Scan(
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
	d.PRTitle, d.Bugfix, d.Body = noteOut(d.PRTitle, bugfix, note)
	if runID != nil {
		d.RunID = *runID
	}
	var c NoteCallRow
	var cm *string
	err = s.db.QueryRowContext(ctx, `
		SELECT COALESCE(outcome, ''), COALESCE(note_status, ''), model, input_tokens, cache_read_tokens, output_tokens,
		       cost_nano_usd, rounds, rounds_input_tokens, rounds_output_tokens, rounds_cost_nano_usd
		  FROM note_calls WHERE repo_id = ? AND pr_id = ? ORDER BY id DESC LIMIT 1`,
		rid, prID).Scan(&c.Outcome, &c.NoteStatus, &cm, &c.InputTokens, &c.CacheReadTokens, &c.OutputTokens,
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
	pid, ok, err := projectID(ctx, s.db, project)
	if err != nil || !ok {
		return []ProblemArea{}, err
	}
	rows, err := s.db.QueryContext(ctx, `
		WITH fixes AS (
		    SELECT n.repo_id, COALESCE(n.bugfix_area, '') AS area, COALESCE(n.bugfix_component, '') AS component,
		           COALESCE(n.bugfix ->> '$.cause_kind', 'other') AS cause, n.merged_at, n.pr_key, n.issues_posted,
		           n.suggestions_posted
		      FROM notes n JOIN repos r ON r.id = n.repo_id
		     WHERE r.project_id = ? AND n.change_kind = 'bugfix' AND n.merged_at >= ?
		),
		causes AS (
		    SELECT repo_id, area, cause, COUNT(*) AS n,
		           ROW_NUMBER() OVER (PARTITION BY repo_id, area ORDER BY COUNT(*) DESC, cause) AS rn
		      FROM fixes GROUP BY repo_id, area, cause
		),
		latest AS (
		    SELECT repo_id, area, component, pr_key, merged_at,
		           ROW_NUMBER() OVER (PARTITION BY repo_id, area ORDER BY merged_at DESC) AS rn
		      FROM fixes
		)
		SELECT r.slug, f.area, l.component, COUNT(*), c.cause, c.n, SUM(f.issues_posted), SUM(f.suggestions_posted), l.pr_key, l.merged_at
		  FROM fixes f
		  JOIN causes c ON c.repo_id = f.repo_id AND c.area = f.area AND c.rn = 1
		  JOIN latest l ON l.repo_id = f.repo_id AND l.area = f.area AND l.rn = 1
		  JOIN repos r ON r.id = f.repo_id
		 GROUP BY f.repo_id, f.area, l.component, c.cause, c.n, l.pr_key, l.merged_at
		 ORDER BY COUNT(*) DESC, l.merged_at DESC`, pid, since)
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
		// The component is a name from noergler's note: note text like any.
		p.Component = redact.Text(p.Component)
		out = append(out, p)
	}
	return out, rows.Err()
}

// RepoProblem is one problem area of a repository as a baseline step reads
// it: the directory, how many bugfixes touched it, the cause class seen
// most, and the newest fixes' PR keys, newest first.
type RepoProblem struct {
	Area     string
	Bugfixes int
	TopCause string
	PRKeys   []string
}

// repoProblemPRs is how many of an area's newest fixes a step is told of.
const repoProblemPRs = 3

// RepoProblems lists a repository's problem areas over its whole history up
// to until, most bugfixes first: what broke before does not expire like the
// dashboard's window, and a fix merged after the commit a run reads is no
// evidence about that code. Grouped by bugfix_area like Problems.
func (s *Store) RepoProblems(ctx context.Context, project, repo string, until time.Time) ([]RepoProblem, error) {
	rid, ok, err := repoID(ctx, s.db, project, repo)
	if err != nil || !ok {
		return []RepoProblem{}, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT COALESCE(bugfix_area, ''), COALESCE(bugfix ->> '$.cause_kind', 'other'), pr_key
		  FROM notes
		 WHERE repo_id = ? AND change_kind = 'bugfix' AND merged_at <= ?
		 ORDER BY merged_at DESC, pr_key`, rid, stamp(until))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	byArea := map[string]*RepoProblem{}
	causes := map[string]map[string]int{}
	var order []string
	for rows.Next() {
		var area, cause, key string
		if err := rows.Scan(&area, &cause, &key); err != nil {
			return nil, err
		}
		p := byArea[area]
		if p == nil {
			p = &RepoProblem{Area: area}
			byArea[area], causes[area] = p, map[string]int{}
			order = append(order, area)
		}
		p.Bugfixes++
		causes[area][cause]++
		if len(p.PRKeys) < repoProblemPRs {
			p.PRKeys = append(p.PRKeys, key)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]RepoProblem, 0, len(order))
	for _, area := range order {
		p := byArea[area]
		// The cause seen most, ties to the first in name order, as Problems.
		for cause, n := range causes[area] {
			if best := causes[area][p.TopCause]; p.TopCause == "" || n > best || (n == best && cause < p.TopCause) {
				p.TopCause = cause
			}
		}
		out = append(out, *p)
	}
	// Most bugfixes first; among equals the area fixed most recently, the
	// order the rows met them in.
	slices.SortStableFunc(out, func(a, b RepoProblem) int { return b.Bugfixes - a.Bugfixes })
	return out, nil
}
