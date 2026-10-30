package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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
	// AutoDescribe: runs start on their own (a clone's first ready, the
	// monthly reconcile); off, only the buttons start one.
	AutoDescribe bool
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

// Sources lists every source with its repo counts, by key.
func (s *Store) Sources(ctx context.Context) ([]SourceRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT s.project_key, s.kind, COALESCE(s.host, ''), s.token_auth, s.token_user, s.token_enc,
		       s.created_at, s.updated_at, s.last_listed_at, s.last_error, s.auto_describe,
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
	defer rows.Close()
	out := []SourceRow{}
	for rows.Next() {
		var r SourceRow
		if err := rows.Scan(&r.ProjectKey, &r.Kind, &r.Host, &r.TokenAuth, &r.TokenUser, &r.TokenEnc,
			&r.CreatedAt, &r.UpdatedAt, &r.LastListedAt, &r.LastError, &r.AutoDescribe,
			&r.Repos, &r.Ready, &r.Active, &r.Failed, &r.Missing); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SourceByKey is one source, found or not.
func (s *Store) SourceByKey(ctx context.Context, key string) (ProjectSource, bool, error) {
	var src ProjectSource
	err := s.pool.QueryRow(ctx, `
		SELECT project_key, kind, COALESCE(host, ''), token_auth, token_user, token_enc,
		       created_at, updated_at, last_listed_at, last_error, auto_describe
		  FROM sources WHERE project_key = $1`, key).
		Scan(&src.ProjectKey, &src.Kind, &src.Host, &src.TokenAuth, &src.TokenUser, &src.TokenEnc,
			&src.CreatedAt, &src.UpdatedAt, &src.LastListedAt, &src.LastError, &src.AutoDescribe)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProjectSource{}, false, nil
	}
	if err != nil {
		return ProjectSource{}, false, err
	}
	return src, true, nil
}

// CreateSource inserts a source with its auto_describe as given, in one
// write; ErrExists when the key is taken.
func (s *Store) CreateSource(ctx context.Context, src ProjectSource) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO sources (project_key, kind, host, token_auth, token_user, token_enc, auto_describe)
		VALUES ($1, $2, NULLIF($3, ''), $4, $5, $6, $7)`,
		src.ProjectKey, src.Kind, src.Host, src.TokenAuth, src.TokenUser, src.TokenEnc, src.AutoDescribe)
	return uniqueAsExists(err)
}

// ProjectSpelledOtherwise is the spelling noergler delivers a project key
// in when it differs from key only by case, ok false when it does not: a
// source must match noergler's key exactly, or every note of the project
// is ignored.
func (s *Store) ProjectSpelledOtherwise(ctx context.Context, key string) (string, bool, error) {
	var known string
	err := s.pool.QueryRow(ctx, `SELECT key FROM projects WHERE lower(key) = lower($1) AND key <> $1 ORDER BY key LIMIT 1`, key).Scan(&known)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	return known, err == nil, err
}

// UpdateSourceAccess changes a source's host and token settings. The token
// is replaced only when setToken is true (an empty tokenEnc then clears it);
// otherwise the stored one stays. Reports whether the source exists.
func (s *Store) UpdateSourceAccess(ctx context.Context, key, host, tokenAuth, tokenUser string, tokenEnc []byte, setToken bool) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE sources SET host = NULLIF($2, ''), token_auth = $3, token_user = $4,
		       token_enc = CASE WHEN $6 THEN $5 ELSE token_enc END, updated_at = now()
		 WHERE project_key = $1`, key, host, tokenAuth, tokenUser, nilIfEmpty(tokenEnc), setToken)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// SetAutoDescribe turns a source's automatic runs on or off. Reports
// whether the source exists.
func (s *Store) SetAutoDescribe(ctx context.Context, key string, on bool) (bool, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE sources SET auto_describe = $2, updated_at = now() WHERE project_key = $1`, key, on)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// IsSourceRepo reports whether (key, slug) is a source repository: the
// starting point of a page, before which noergler's notes are ignored.
func (s *Store) IsSourceRepo(ctx context.Context, key, slug string) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM source_repos WHERE project_key = $1 AND slug = $2)`, key, slug).Scan(&ok)
	return ok, err
}

// DeleteSource removes a source and, by cascade, its repos. Reports whether
// there was one.
func (s *Store) DeleteSource(ctx context.Context, key string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM sources WHERE project_key = $1`, key)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// MarkSourceListed records a listing's outcome: the time on success, the
// error text on failure (the time then stays as it was).
func (s *Store) MarkSourceListed(ctx context.Context, key, errText string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE sources SET last_error = $2,
		       last_listed_at = CASE WHEN $2 = '' THEN now() ELSE last_listed_at END
		 WHERE project_key = $1`, key, errText)
	return err
}

// SourceRepos lists a source's repos by slug.
func (s *Store) SourceRepos(ctx context.Context, key string) ([]SourceRepo, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT project_key, slug, clone_url, origin, COALESCE(branch, ''), head_commit, state,
		       last_synced_at, last_error, created_at
		  FROM source_repos WHERE project_key = $1 ORDER BY slug`, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRepos(rows)
}

func scanRepos(rows pgx.Rows) ([]SourceRepo, error) {
	out := []SourceRepo{}
	for rows.Next() {
		var r SourceRepo
		if err := rows.Scan(&r.ProjectKey, &r.Slug, &r.CloneURL, &r.Origin, &r.Branch, &r.HeadCommit, &r.State,
			&r.LastSyncedAt, &r.LastError, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// AddSourceRepo inserts a hand-entered repo as pending; ErrExists when the
// slug is taken.
func (s *Store) AddSourceRepo(ctx context.Context, r SourceRepo) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO source_repos (project_key, slug, clone_url, origin, branch, state)
		VALUES ($1, $2, $3, 'manual', NULLIF($4, ''), 'pending')`,
		r.ProjectKey, r.Slug, r.CloneURL, r.Branch)
	return uniqueAsExists(err)
}

// DeleteSourceRepo removes one repo row. Reports whether there was one.
func (s *Store) DeleteSourceRepo(ctx context.Context, key, slug string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM source_repos WHERE project_key = $1 AND slug = $2`, key, slug)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
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
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		seen := make([]string, 0, len(listed))
		for _, l := range listed {
			seen = append(seen, l.Slug)
			// xmax = 0 on the returned row tells an insert from an update;
			// a conflict with a hand-entered row updates nothing and returns
			// no row at all.
			var inserted bool
			err := tx.QueryRow(ctx, `
				INSERT INTO source_repos (project_key, slug, clone_url, origin, state)
				VALUES ($1, $2, $3, 'listed', 'pending')
				ON CONFLICT (project_key, slug) DO UPDATE
				   SET clone_url = EXCLUDED.clone_url,
				       state = CASE WHEN source_repos.state = 'missing' THEN 'pending' ELSE source_repos.state END
				 WHERE source_repos.origin = 'listed'
				RETURNING (xmax = 0)`, key, l.Slug, l.CloneURL).Scan(&inserted)
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			if inserted {
				res.Added++
			}
		}
		tag, err := tx.Exec(ctx, `
			UPDATE source_repos SET state = 'missing'
			 WHERE project_key = $1 AND origin = 'listed' AND state <> 'missing' AND NOT (slug = ANY($2))`, key, seen)
		if err != nil {
			return err
		}
		res.Missing = int(tag.RowsAffected())
		return nil
	})
	return res, err
}

// RequestSync marks a source's repos (or one repo, when slug is set) pending
// so the next cycle syncs them; a missing one stays missing. Reports how
// many rows moved.
func (s *Store) RequestSync(ctx context.Context, key, slug string) (int, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE source_repos SET state = 'pending'
		 WHERE project_key = $1 AND ($2 = '' OR slug = $2) AND state <> 'missing'`, key, slug)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
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
	rows, err := s.pool.Query(ctx, `
		SELECT s.project_key, s.kind, COALESCE(s.host, ''), s.token_auth, s.token_user, s.token_enc,
		       s.created_at, s.updated_at, s.last_listed_at, s.last_error, s.auto_describe,
		       r.slug, r.clone_url, r.origin, COALESCE(r.branch, ''), r.head_commit, r.state,
		       r.last_synced_at, r.last_error, r.created_at
		  FROM source_repos r JOIN sources s ON s.project_key = r.project_key
		 WHERE r.state <> 'missing'
		 ORDER BY s.project_key, r.slug`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SyncJob{}
	for rows.Next() {
		var j SyncJob
		if err := rows.Scan(&j.Source.ProjectKey, &j.Source.Kind, &j.Source.Host, &j.Source.TokenAuth, &j.Source.TokenUser, &j.Source.TokenEnc,
			&j.Source.CreatedAt, &j.Source.UpdatedAt, &j.Source.LastListedAt, &j.Source.LastError, &j.Source.AutoDescribe,
			&j.Repo.Slug, &j.Repo.CloneURL, &j.Repo.Origin, &j.Repo.Branch, &j.Repo.HeadCommit, &j.Repo.State,
			&j.Repo.LastSyncedAt, &j.Repo.LastError, &j.Repo.CreatedAt); err != nil {
			return nil, err
		}
		j.Repo.ProjectKey = j.Source.ProjectKey
		out = append(out, j)
	}
	return out, rows.Err()
}

// MarkRepoSyncing is the cycle taking a repo on.
func (s *Store) MarkRepoSyncing(ctx context.Context, key, slug string) error {
	_, err := s.pool.Exec(ctx, `UPDATE source_repos SET state = 'syncing' WHERE project_key = $1 AND slug = $2`, key, slug)
	return err
}

// MarkRepoReady records a successful sync: the branch (resolved or as
// configured), the head and the time; a left-over error goes.
func (s *Store) MarkRepoReady(ctx context.Context, key, slug, branch, head string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE source_repos SET state = 'ready', branch = NULLIF($3, ''), head_commit = $4,
		       last_synced_at = now(), last_error = ''
		 WHERE project_key = $1 AND slug = $2`, key, slug, branch, head)
	return err
}

// MarkRepoFailed records why a sync failed; the head stays what it was.
func (s *Store) MarkRepoFailed(ctx context.Context, key, slug, errText string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE source_repos SET state = 'failed', last_error = $3 WHERE project_key = $1 AND slug = $2`, key, slug, errText)
	return err
}

func uniqueAsExists(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrExists
	}
	return err
}

func nilIfEmpty(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	return b
}
