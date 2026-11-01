package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// The sources: what Almanach clones itself. Separate from the noergler-fed
// projects and repos; phase 2 joins the two on (project_key, slug).

// ProjectSource kinds.
const (
	SourceBitbucket = "bitbucket"
	SourceStatic    = "static"
)

// Token modes: how the token travels to the remote.
const (
	TokenBearer = "bearer"
	TokenBasic  = "basic"
)

// Repo origins and states.
const (
	OriginListed = "listed"
	OriginManual = "manual"

	RepoPending = "pending"
	RepoSyncing = "syncing"
	RepoReady   = "ready"
	RepoFailed  = "failed"
	RepoMissing = "missing"
)

// ErrExists is a create of a key that is already there.
var ErrExists = errors.New("already exists")

// ProjectSource is one configured project source.
type ProjectSource struct {
	ProjectKey string
	Kind       string
	// Host is the Bitbucket base URL; "" for a static source.
	Host      string
	TokenAuth string
	TokenUser string
	// TokenEnc is the encrypted token, nil when the source has none.
	TokenEnc     []byte
	CreatedAt    time.Time
	UpdatedAt    time.Time
	LastListedAt *time.Time
	LastError    string
}

// HasToken reports whether a credential is stored.
func (s ProjectSource) HasToken() bool { return len(s.TokenEnc) > 0 }

// SourceRow is a source with its repositories counted by state.
type SourceRow struct {
	ProjectSource
	Repos   int
	Ready   int
	Active  int // pending or syncing
	Failed  int
	Missing int
}

// SourceRepo is one clone.
type SourceRepo struct {
	ProjectKey string
	Slug       string
	CloneURL   string
	Origin     string
	// Branch is "" until resolved from the remote.
	Branch       string
	HeadCommit   string
	State        string
	LastSyncedAt *time.Time
	LastError    string
	CreatedAt    time.Time
}

// sourceColumns are a source's columns in the order sourceDest scans them.
const sourceColumns = `s.project_key, s.kind, COALESCE(s.host, ''), s.token_auth, s.token_user, s.token_enc,
	s.created_at, s.updated_at, s.last_listed_at, s.last_error`

func sourceDest(src *ProjectSource) []any {
	return []any{&src.ProjectKey, &src.Kind, &src.Host, &src.TokenAuth, &src.TokenUser, &src.TokenEnc,
		timeAt(&src.CreatedAt), timeAt(&src.UpdatedAt), timeOrNil(&src.LastListedAt), &src.LastError}
}

// Sources lists every source with its repo counts, by key.
func (s *Store) Sources(ctx context.Context) ([]SourceRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+sourceColumns+`,
		       COUNT(r.slug),
		       COUNT(r.slug) FILTER (WHERE r.state = 'ready'),
		       COUNT(r.slug) FILTER (WHERE r.state IN ('pending', 'syncing')),
		       COUNT(r.slug) FILTER (WHERE r.state = 'failed'),
		       COUNT(r.slug) FILTER (WHERE r.state = 'missing')
		  FROM sources s LEFT JOIN source_repos r ON r.project_key = s.project_key
		 GROUP BY s.project_key ORDER BY s.project_key`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []SourceRow{}
	for rows.Next() {
		var r SourceRow
		if err := rows.Scan(append(sourceDest(&r.ProjectSource), &r.Repos, &r.Ready, &r.Active, &r.Failed, &r.Missing)...); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SourceByKey is one source, found or not.
func (s *Store) SourceByKey(ctx context.Context, key string) (ProjectSource, bool, error) {
	var src ProjectSource
	err := s.db.QueryRowContext(ctx, `SELECT `+sourceColumns+` FROM sources s WHERE s.project_key = ?`, key).Scan(sourceDest(&src)...)
	if errors.Is(err, sql.ErrNoRows) {
		return ProjectSource{}, false, nil
	}
	if err != nil {
		return ProjectSource{}, false, err
	}
	return src, true, nil
}

// CreateSource inserts a source in one write; ErrExists when the key is taken.
func (s *Store) CreateSource(ctx context.Context, src ProjectSource) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sources (project_key, kind, host, token_auth, token_user, token_enc, created_at, updated_at)
		VALUES (?1, ?2, NULLIF(?3, ''), ?4, ?5, ?6, ?7, ?7)`,
		src.ProjectKey, src.Kind, src.Host, src.TokenAuth, src.TokenUser, nilIfEmpty(src.TokenEnc), nowStamp())
	return uniqueAsExists(err)
}

// ProjectSpelledOtherwise is the spelling noergler delivers a project key
// in when it differs from key only by case, ok false when it does not: a
// source must match noergler's key exactly, or every note of the project
// is ignored.
//
// The case is folded in Go: SQLite's lower() folds ASCII only, and a key
// "ÄRZTE" must find "ärzte".
func (s *Store) ProjectSpelledOtherwise(ctx context.Context, key string) (string, bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key FROM projects WHERE key <> ? ORDER BY key`, key)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var known string
		if err := rows.Scan(&known); err != nil {
			return "", false, err
		}
		if strings.EqualFold(known, key) {
			return known, true, nil
		}
	}
	return "", false, rows.Err()
}

// UpdateSourceAccess changes a source's host and token settings. The token
// is replaced only when setToken is true (an empty tokenEnc then clears it);
// otherwise the stored one stays. Reports whether the source exists.
func (s *Store) UpdateSourceAccess(ctx context.Context, key, host, tokenAuth, tokenUser string, tokenEnc []byte, setToken bool) (bool, error) {
	n, err := affected(s.db.ExecContext(ctx, `
		UPDATE sources SET host = NULLIF(?, ''), token_auth = ?, token_user = ?,
		       token_enc = CASE WHEN ? THEN ? ELSE token_enc END, updated_at = ?
		 WHERE project_key = ?`, host, tokenAuth, tokenUser, setToken, nilIfEmpty(tokenEnc), nowStamp(), key))
	return n == 1, err
}

// IsSourceRepo reports whether (key, slug) is a source repository: the
// starting point of a page, before which noergler's notes are ignored.
func (s *Store) IsSourceRepo(ctx context.Context, key, slug string) (bool, error) {
	var ok bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM source_repos WHERE project_key = ? AND slug = ?)`, key, slug).Scan(&ok)
	return ok, err
}

// DeleteSource removes a source and, by cascade, its repos. Reports whether
// there was one.
func (s *Store) DeleteSource(ctx context.Context, key string) (bool, error) {
	n, err := affected(s.db.ExecContext(ctx, `DELETE FROM sources WHERE project_key = ?`, key))
	return n == 1, err
}

// MarkSourceListed records a listing's outcome: the time on success, the
// error text on failure (the time then stays as it was).
func (s *Store) MarkSourceListed(ctx context.Context, key, errText string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE sources SET last_error = ?2,
		       last_listed_at = CASE WHEN ?2 = '' THEN ?3 ELSE last_listed_at END
		 WHERE project_key = ?1`, key, errText, nowStamp())
	return err
}

// SourceRepos lists a source's repos by slug.
func (s *Store) SourceRepos(ctx context.Context, key string) ([]SourceRepo, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT project_key, slug, clone_url, origin, COALESCE(branch, ''), head_commit, state,
		       last_synced_at, last_error, created_at
		  FROM source_repos WHERE project_key = ? ORDER BY slug`, key)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []SourceRepo{}
	for rows.Next() {
		var r SourceRepo
		if err := rows.Scan(&r.ProjectKey, &r.Slug, &r.CloneURL, &r.Origin, &r.Branch, &r.HeadCommit, &r.State,
			timeOrNil(&r.LastSyncedAt), &r.LastError, timeAt(&r.CreatedAt)); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// AddSourceRepo inserts a hand-entered repo as pending; ErrExists when the
// slug is taken.
func (s *Store) AddSourceRepo(ctx context.Context, r SourceRepo) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO source_repos (project_key, slug, clone_url, origin, branch, state, created_at)
		VALUES (?, ?, ?, 'manual', NULLIF(?, ''), 'pending', ?)`,
		r.ProjectKey, r.Slug, r.CloneURL, r.Branch, nowStamp())
	return uniqueAsExists(err)
}

// DeleteSourceRepo removes one repo row. Reports whether there was one.
func (s *Store) DeleteSourceRepo(ctx context.Context, key, slug string) (bool, error) {
	n, err := affected(s.db.ExecContext(ctx, `DELETE FROM source_repos WHERE project_key = ? AND slug = ?`, key, slug))
	return n == 1, err
}

// ListedRepo is one repository as the Bitbucket listing names it.
type ListedRepo struct {
	Slug     string
	CloneURL string
}

// ListingResult counts what a listing changed.
type ListingResult struct {
	Added   int
	Missing int
}

// ApplyListing reconciles a Bitbucket source's repos with what the API
// listed, in one transaction: a new slug is inserted pending; a known one
// takes the listed clone URL and, when it was missing, is pending again; a
// listed row the API no longer names becomes missing. Hand-entered rows are
// not touched (a Bitbucket source has none, but the rule costs nothing).
func (s *Store) ApplyListing(ctx context.Context, key string, listed []ListedRepo) (ListingResult, error) {
	var res ListingResult
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		now := nowStamp()
		seen := make([]string, 0, len(listed))
		for _, l := range listed {
			seen = append(seen, l.Slug)
			// A new slug is an insert; a known one is left to the update
			// below, which a hand-entered row does not match.
			n, err := affected(tx.ExecContext(ctx, `
				INSERT INTO source_repos (project_key, slug, clone_url, origin, state, created_at)
				VALUES (?, ?, ?, 'listed', 'pending', ?)
				ON CONFLICT (project_key, slug) DO NOTHING`, key, l.Slug, l.CloneURL, now))
			if err != nil {
				return err
			}
			if n == 1 {
				res.Added++
				continue
			}
			if _, err := tx.ExecContext(ctx, `
				UPDATE source_repos
				   SET clone_url = ?3, state = CASE WHEN state = 'missing' THEN 'pending' ELSE state END
				 WHERE project_key = ?1 AND slug = ?2 AND origin = 'listed'`, key, l.Slug, l.CloneURL); err != nil {
				return err
			}
		}
		names, err := jsonOf(seen)
		if err != nil {
			return err
		}
		n, err := affected(tx.ExecContext(ctx, `
			UPDATE source_repos SET state = 'missing'
			 WHERE project_key = ? AND origin = 'listed' AND state <> 'missing'
			   AND slug NOT IN (SELECT value FROM json_each(?))`, key, names))
		if err != nil {
			return err
		}
		res.Missing = int(n)
		return nil
	})
	return res, err
}

// RequestSync marks a source's repos (or one repo, when slug is set) pending
// so the next cycle syncs them; a missing one stays missing. Reports how
// many rows moved.
func (s *Store) RequestSync(ctx context.Context, key, slug string) (int, error) {
	n, err := affected(s.db.ExecContext(ctx, `
		UPDATE source_repos SET state = 'pending'
		 WHERE project_key = ?1 AND (?2 = '' OR slug = ?2) AND state <> 'missing'`, key, slug))
	return int(n), err
}

// SyncJob is one repo with the source it belongs to, as the poller needs
// it: the token travels encrypted and is opened only for the git process.
type SyncJob struct {
	Source ProjectSource
	Repo   SourceRepo
}

// SyncJobs is every repo a cycle must look at (everything but missing), by
// source then slug, with its source's access settings.
func (s *Store) SyncJobs(ctx context.Context) ([]SyncJob, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+sourceColumns+`,
		       r.slug, r.clone_url, r.origin, COALESCE(r.branch, ''), r.head_commit, r.state,
		       r.last_synced_at, r.last_error, r.created_at
		  FROM source_repos r JOIN sources s ON s.project_key = r.project_key
		 WHERE r.state <> 'missing'
		 ORDER BY s.project_key, r.slug`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []SyncJob{}
	for rows.Next() {
		var j SyncJob
		if err := rows.Scan(append(sourceDest(&j.Source),
			&j.Repo.Slug, &j.Repo.CloneURL, &j.Repo.Origin, &j.Repo.Branch, &j.Repo.HeadCommit, &j.Repo.State,
			timeOrNil(&j.Repo.LastSyncedAt), &j.Repo.LastError, timeAt(&j.Repo.CreatedAt))...); err != nil {
			return nil, err
		}
		j.Repo.ProjectKey = j.Source.ProjectKey
		out = append(out, j)
	}
	return out, rows.Err()
}

// MarkRepoSyncing is the cycle taking a repo on.
func (s *Store) MarkRepoSyncing(ctx context.Context, key, slug string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE source_repos SET state = 'syncing' WHERE project_key = ? AND slug = ?`, key, slug)
	return err
}

// MarkRepoReady records a successful sync: the branch (resolved or as
// configured), the head and the time; a left-over error goes.
func (s *Store) MarkRepoReady(ctx context.Context, key, slug, branch, head string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE source_repos SET state = 'ready', branch = NULLIF(?, ''), head_commit = ?,
		       last_synced_at = ?, last_error = ''
		 WHERE project_key = ? AND slug = ?`, branch, head, nowStamp(), key, slug)
	return err
}

// MarkRepoFailed records why a sync failed; the head stays what it was.
func (s *Store) MarkRepoFailed(ctx context.Context, key, slug, errText string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE source_repos SET state = 'failed', last_error = ? WHERE project_key = ? AND slug = ?`, errText, key, slug)
	return err
}

// nilIfEmpty is a token as bound: none is NULL, never an empty blob.
func nilIfEmpty(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}
