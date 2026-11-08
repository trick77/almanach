package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/trick77/almanach/internal/redact"
)

// A repository's brief (repo_briefs): its .agents/PROJECT.md, written on
// request from the stored page and pushed to a branch of the repository.
// The request and the push are the worker's, oldest first, each read from
// the rows on every lap.

// Push states of a brief.
const (
	// PushDue: written, the push waits for the worker.
	PushDue = "due"
	// PushPushed: the branch holds the brief.
	PushPushed = "pushed"
	// PushUnchanged: the default branch already holds the brief, nothing
	// was pushed.
	PushUnchanged = "unchanged"
	// PushFailed: the push was refused (no write access, a token expired):
	// the reader copies the text instead.
	PushFailed = "failed"
)

const briefColumns = `b.page_revision, b.written_at, b.requested_at, b.attempts, b.failed_at, b.failed, b.push_state,
	b.push_error, b.pushed_commit, b.push_at`

func briefDest(b *Brief, rev *sql.NullInt64, failed, state, pushErr, commit *sql.NullString) []any {
	return []any{rev, timeOrNil(&b.WrittenAt), timeOrNil(&b.RequestedAt), &b.Attempts, timeOrNil(&b.FailedAt), failed,
		state, pushErr, commit, timeOrNil(&b.PushAt)}
}

// scanBrief reads briefColumns, then dest, into a Brief: the reasons
// redacted on the way out, as a failed note's.
func scanBrief(scan func(...any) error, dest ...any) (Brief, error) {
	var b Brief
	var rev sql.NullInt64
	var failed, state, pushErr, commit sql.NullString
	if err := scan(append(briefDest(&b, &rev, &failed, &state, &pushErr, &commit), dest...)...); err != nil {
		return Brief{}, err
	}
	b.PageRevision, b.HasMarkdown = int(rev.Int64), rev.Valid
	b.Failed, b.PushState = redact.Text(failed.String), state.String
	b.PushError, b.PushedCommit = redact.Text(pushErr.String), commit.String
	return b, nil
}

// RequestBrief asks for repository repoID's brief, written anew from its
// page and pushed. A request already waiting keeps its place in line and
// its count; one the worker is on meanwhile is followed by this one
// (request_seq). The last failure is forgotten. ErrNoRepo for a repository
// that is no source.
func (s *Store) RequestBrief(ctx context.Context, repoID int64) error {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO repo_briefs (repo_id, requested_at, request_seq)
		SELECT repo_id, ?, 1 FROM source_repos WHERE repo_id = ?
		ON CONFLICT (repo_id) DO UPDATE SET
		    attempts = CASE WHEN requested_at IS NULL THEN 0 ELSE attempts END,
		    requested_at = COALESCE(requested_at, excluded.requested_at),
		    request_seq = request_seq + 1,
		    failed_at = NULL, failed = NULL`, nowStamp(), repoID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		if err == nil {
			err = ErrNoRepo
		}
		return err
	}
	return nil
}

// RepoBrief is repository repoID's brief, the file with it; false before
// the first request.
func (s *Store) RepoBrief(ctx context.Context, repoID int64) (Brief, bool, error) {
	var md sql.NullString
	b, err := scanBrief(s.db.QueryRowContext(ctx, `SELECT `+briefColumns+`, b.markdown FROM repo_briefs b WHERE b.repo_id = ?`, repoID).Scan, &md)
	if errors.Is(err, sql.ErrNoRows) {
		return Brief{}, false, nil
	}
	if err != nil {
		return Brief{}, false, err
	}
	b.Markdown = md.String
	return b, true, nil
}

// Briefs is the briefs of a source's repositories by repository id,
// without their files: the source page's listing.
func (s *Store) Briefs(ctx context.Context, key string) (map[int64]Brief, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+briefColumns+`, b.repo_id FROM repo_briefs b JOIN source_repos sr ON sr.repo_id = b.repo_id
		 WHERE sr.project_id = `+projectByName, key)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[int64]Brief{}
	for rows.Next() {
		var id int64
		b, err := scanBrief(rows.Scan, &id)
		if err != nil {
			return nil, err
		}
		out[id] = b
	}
	return out, rows.Err()
}

// NextBrief is the brief the worker owes next, of a repository that is a
// source: a due push first, oldest first (it costs no call, and a request
// the day's budget holds must not hold it), then the oldest request. A row
// with both is pushed first and written anew after.
func (s *Store) NextBrief(ctx context.Context) (BriefJob, bool, error) {
	var j BriefJob
	err := s.db.QueryRowContext(ctx, `
		SELECT b.repo_id, p.key, r.slug, COALESCE(b.push_state, '') <> 'due', COALESCE(b.markdown, ''), COALESCE(b.pushed_commit, ''),
		       b.request_seq
		  FROM `+sourceReposFrom+` JOIN repo_briefs b ON b.repo_id = sr.repo_id
		 WHERE (b.requested_at IS NOT NULL OR b.push_state = 'due') AND `+activeRow+`
		 ORDER BY COALESCE(b.push_state, '') = 'due' DESC,
		          CASE WHEN b.push_state = 'due' THEN b.push_at ELSE b.requested_at END, b.repo_id LIMIT 1`).
		Scan(&j.RepoID, &j.ProjectKey, &j.Slug, &j.Write, &j.Markdown, &j.PushedCommit, &j.RequestSeq)
	if errors.Is(err, sql.ErrNoRows) {
		return BriefJob{}, false, nil
	}
	return j, err == nil, err
}

// WriteBrief stores the answer to request seq, the file and the page
// revision it was written from, its push due. The request is done unless
// another came while it was written: that one stays, to be written anew.
func (s *Store) WriteBrief(ctx context.Context, repoID, seq int64, markdown string, pageRevision int) error {
	now := nowStamp()
	_, err := s.db.ExecContext(ctx, `
		UPDATE repo_briefs SET markdown = ?, page_revision = ?, written_at = ?,
		       requested_at = CASE WHEN request_seq = ? THEN NULL ELSE requested_at END, attempts = 0,
		       failed_at = NULL, failed = NULL, push_state = 'due', push_error = NULL, push_at = ?
		 WHERE repo_id = ?`, markdown, pageRevision, now, seq, now, repoID)
	return err
}

// DeferBrief counts an attempt against the request before its call, which
// stays; the attempts so far returned. ErrNoRepo when the row went (its
// repository removed): the job is gone, nothing to hold for.
func (s *Store) DeferBrief(ctx context.Context, repoID int64) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `UPDATE repo_briefs SET attempts = attempts + 1 WHERE repo_id = ? RETURNING attempts`, repoID).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNoRepo
	}
	return n, err
}

// FailBrief takes request seq off with its reason; the brief before it,
// and its push, stay as they were. One that came while it failed stays,
// unfailed.
func (s *Store) FailBrief(ctx context.Context, repoID, seq int64, reason string) error {
	if reason == "" {
		reason = "failed"
	}
	_, err := s.db.ExecContext(ctx, `UPDATE repo_briefs SET requested_at = NULL, failed_at = ?, failed = ? WHERE repo_id = ? AND request_seq = ?`,
		nowStamp(), reason, repoID, seq)
	return err
}

// PushedBrief records how a due push ended: state PushPushed with its
// commit, PushUnchanged (commit, when a branch of its own went back to the
// default branch), or PushFailed with why. The commit pushed last is kept
// when none is given: it is what the next push may replace. The worker is
// serial: no request writes the brief anew between its push and this.
func (s *Store) PushedBrief(ctx context.Context, repoID int64, state, commit, pushErr string) error {
	if state == PushFailed && pushErr == "" {
		pushErr = "push failed"
	}
	if state != PushFailed {
		pushErr = ""
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE repo_briefs SET push_state = ?, pushed_commit = COALESCE(NULLIF(?, ''), pushed_commit),
		       push_error = NULLIF(?, ''), push_at = ?
		 WHERE repo_id = ? AND markdown IS NOT NULL`, state, commit, pushErr, nowStamp(), repoID)
	return err
}
