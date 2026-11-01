package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// The sources: what Almanach clones itself. A source hangs off its project,
// a clone off its repository, by id; the names are the projects' and the
// repositories' (names.go).

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
	// ProjectID is the project's id, ProjectKey its current key.
	ProjectID  int64
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

// SourceRepo is one clone. ProjectID and RepoID are its ids, ProjectKey and
// Slug its current names.
type SourceRepo struct {
	ProjectID  int64
	RepoID     int64
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

// sourcesFrom is a source (s) with its project (p).
const sourcesFrom = `sources s JOIN projects p ON p.id = s.project_id`

// sourceColumns are a source's columns over sourcesFrom in the order
// sourceDest scans them.
const sourceColumns = `s.project_id, p.key, s.kind, COALESCE(s.host, ''), s.token_auth, s.token_user, s.token_enc,
	s.created_at, s.updated_at, s.last_listed_at, s.last_error, s.auto_describe`

func sourceDest(src *ProjectSource) []any {
	return []any{&src.ProjectID, &src.ProjectKey, &src.Kind, &src.Host, &src.TokenAuth, &src.TokenUser, &src.TokenEnc,
		timeAt(&src.CreatedAt), timeAt(&src.UpdatedAt), timeOrNil(&src.LastListedAt), &src.LastError, &src.AutoDescribe}
}

// projectByName and repoByName are a project and a repository by any name
// they ever had, as a subquery over one or two parameters.
const (
	projectByName = `(SELECT project_id FROM project_names WHERE key = ?)`
	repoByName    = `(SELECT rn.repo_id FROM repo_names rn JOIN project_names pn ON pn.project_id = rn.project_id
	                   WHERE pn.key = ? AND rn.slug = ?)`
)

// Sources lists every source with its repo counts, by key.
func (s *Store) Sources(ctx context.Context) ([]SourceRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+sourceColumns+`,
		       COUNT(r.repo_id),
		       COUNT(r.repo_id) FILTER (WHERE r.state = 'ready'),
		       COUNT(r.repo_id) FILTER (WHERE r.state IN ('pending', 'syncing')),
		       COUNT(r.repo_id) FILTER (WHERE r.state = 'failed'),
		       COUNT(r.repo_id) FILTER (WHERE r.state = 'missing')
		  FROM `+sourcesFrom+` LEFT JOIN source_repos r ON r.project_id = s.project_id
		 GROUP BY s.project_id ORDER BY p.key`)
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

// SourceByKey is one source, found or not, by any key its project ever
// had; it carries the current one.
func (s *Store) SourceByKey(ctx context.Context, key string) (ProjectSource, bool, error) {
	var src ProjectSource
	err := s.db.QueryRowContext(ctx, `SELECT `+sourceColumns+` FROM `+sourcesFrom+` WHERE s.project_id = `+projectByName,
		key).Scan(sourceDest(&src)...)
	if errors.Is(err, sql.ErrNoRows) {
		return ProjectSource{}, false, nil
	}
	if err != nil {
		return ProjectSource{}, false, err
	}
	return src, true, nil
}

// CreateSource inserts a source with its auto_describe as given, in one
// transaction. A key a project had before finds that project again (its
// pages with it), else a project is registered. ErrExists when the project
// has a source, ErrNameTaken when another project had the key in another
// case.
func (s *Store) CreateSource(ctx context.Context, src ProjectSource) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		self, _, err := projectID(ctx, tx, src.ProjectKey)
		if err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT key, project_id FROM project_names`)
		if err != nil {
			return err
		}
		if taken, err := takenByOther(rows, src.ProjectKey, self); err != nil {
			return err
		} else if taken {
			return ErrNameTaken
		}
		pid, err := ensureProject(ctx, tx, src.ProjectKey)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO sources (project_id, kind, host, token_auth, token_user, token_enc, auto_describe, created_at, updated_at)
			VALUES (?1, ?2, NULLIF(?3, ''), ?4, ?5, ?6, ?7, ?8, ?8)`,
			pid, src.Kind, src.Host, src.TokenAuth, src.TokenUser, nilIfEmpty(src.TokenEnc), src.AutoDescribe, nowStamp())
		return uniqueAsExists(err)
	})
}

// ProjectSpelledOtherwise is the spelling noergler delivers a project key
// in when it differs from key only by case, ok false when it does not: a
// source must match noergler's key exactly, or every note of the project
// is ignored. Only keys noergler delivered for count (first_seen set).
//
// The case is folded in Go: SQLite's lower() folds ASCII only, and a key
// "ÄRZTE" must find "ärzte".
func (s *Store) ProjectSpelledOtherwise(ctx context.Context, key string) (string, bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key FROM projects WHERE key <> ? AND first_seen IS NOT NULL ORDER BY key`, key)
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
		 WHERE project_id = `+projectByName, host, tokenAuth, tokenUser, setToken, nilIfEmpty(tokenEnc), nowStamp(), key))
	return n == 1, err
}

// SetAutoDescribe turns a source's automatic runs on or off. Reports
// whether the source exists.
func (s *Store) SetAutoDescribe(ctx context.Context, key string, on bool) (bool, error) {
	n, err := affected(s.db.ExecContext(ctx, `UPDATE sources SET auto_describe = ?, updated_at = ? WHERE project_id = `+projectByName,
		on, nowStamp(), key))
	return n == 1, err
}

// IsSourceRepo reports whether (key, slug) is a source repository: the
// starting point of a page, before which noergler's notes are ignored. Any
// name the project and the repository ever had counts.
func (s *Store) IsSourceRepo(ctx context.Context, key, slug string) (bool, error) {
	var ok bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM source_repos WHERE repo_id = `+repoByName+`)`, key, slug).Scan(&ok)
	return ok, err
}

// DeleteSource removes a source and, by cascade, its repos. The project and
// its pages stay. Reports whether there was one.
func (s *Store) DeleteSource(ctx context.Context, key string) (bool, error) {
	n, err := affected(s.db.ExecContext(ctx, `DELETE FROM sources WHERE project_id = `+projectByName, key))
	return n == 1, err
}

// MarkSourceListed records a listing's outcome: the time on success, the
// error text on failure (the time then stays as it was).
func (s *Store) MarkSourceListed(ctx context.Context, key, errText string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE sources SET last_error = ?2,
		       last_listed_at = CASE WHEN ?2 = '' THEN ?3 ELSE last_listed_at END
		 WHERE project_id = (SELECT project_id FROM project_names WHERE key = ?1)`, key, errText, nowStamp())
	return err
}

// sourceRepoColumns are a clone's columns over source_repos sr, repos r and
// projects p, in the order sourceRepoDest scans them.
const sourceRepoColumns = `sr.project_id, sr.repo_id, p.key, r.slug, sr.clone_url, sr.origin, COALESCE(sr.branch, ''),
	sr.head_commit, sr.state, sr.last_synced_at, sr.last_error, sr.created_at`

const sourceReposFrom = `source_repos sr JOIN repos r ON r.id = sr.repo_id JOIN projects p ON p.id = sr.project_id`

func sourceRepoDest(r *SourceRepo) []any {
	return []any{&r.ProjectID, &r.RepoID, &r.ProjectKey, &r.Slug, &r.CloneURL, &r.Origin, &r.Branch, &r.HeadCommit, &r.State,
		timeOrNil(&r.LastSyncedAt), &r.LastError, timeAt(&r.CreatedAt)}
}

// SourceRepos lists a source's repos by slug.
func (s *Store) SourceRepos(ctx context.Context, key string) ([]SourceRepo, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+sourceRepoColumns+` FROM `+sourceReposFrom+`
		 WHERE sr.project_id = `+projectByName+` ORDER BY r.slug`, key)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []SourceRepo{}
	for rows.Next() {
		var r SourceRepo
		if err := rows.Scan(sourceRepoDest(&r)...); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SourceRepoByName is one clone by any name its project and repository
// ever had, ok false when the source has none such.
func (s *Store) SourceRepoByName(ctx context.Context, key, slug string) (SourceRepo, bool, error) {
	var r SourceRepo
	err := s.db.QueryRowContext(ctx, `SELECT `+sourceRepoColumns+` FROM `+sourceReposFrom+` WHERE sr.repo_id = `+repoByName,
		key, slug).Scan(sourceRepoDest(&r)...)
	if errors.Is(err, sql.ErrNoRows) {
		return SourceRepo{}, false, nil
	}
	return r, err == nil, err
}

// AddSourceRepo inserts a hand-entered repo as pending. A slug the project
// had before finds that repository again (its pages with it); ErrExists
// when it is a source repository already, ErrNameTaken when another
// repository of the project had the slug in another case.
func (s *Store) AddSourceRepo(ctx context.Context, r SourceRepo) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		pid, ok, err := projectID(ctx, tx, r.ProjectKey)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNoProject
		}
		rid, err := claimSlug(ctx, tx, pid, r.Slug)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO source_repos (repo_id, project_id, clone_url, origin, branch, state, created_at)
			VALUES (?, ?, ?, 'manual', NULLIF(?, ''), 'pending', ?)`,
			rid, pid, r.CloneURL, r.Branch, nowStamp())
		return uniqueAsExists(err)
	})
}

// claimSlug is the repository a new source repository's slug names: the one
// that had it exactly, else a new one. ErrNameTaken when another repository
// of the project had it in another case.
func claimSlug(ctx context.Context, tx *sql.Tx, pid int64, slug string) (int64, error) {
	var self int64
	err := tx.QueryRowContext(ctx, `SELECT repo_id FROM repo_names WHERE project_id = ? AND slug = ?`, pid, slug).Scan(&self)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT slug, repo_id FROM repo_names WHERE project_id = ?`, pid)
	if err != nil {
		return 0, err
	}
	if taken, err := takenByOther(rows, slug, self); err != nil {
		return 0, err
	} else if taken {
		return 0, ErrNameTaken
	}
	if self != 0 {
		return self, nil
	}
	return ensureRepo(ctx, tx, pid, slug)
}

// DeleteSourceRepo removes one repo row; the repository and its pages stay.
// Reports whether there was one.
func (s *Store) DeleteSourceRepo(ctx context.Context, key, slug string) (bool, error) {
	n, err := affected(s.db.ExecContext(ctx, `DELETE FROM source_repos WHERE repo_id = `+repoByName, key, slug))
	return n == 1, err
}

// ListedRepo is one repository as the Bitbucket listing names it. ForgeID
// is Bitbucket's repository id, 0 when the listing gave none.
type ListedRepo struct {
	Slug     string
	CloneURL string
	ForgeID  int64
}

// ListingResult counts what a listing changed. Conflicts are the listed
// slugs left out because another repository of the project had them.
type ListingResult struct {
	Added     int
	Renamed   int
	Missing   int
	Conflicts []string
}

// ApplyListing reconciles a Bitbucket source's repos with what the API
// listed, in one transaction. A listed repository is found by its forge id
// first, so a repository renamed in Bitbucket keeps its id, its pages and
// its runs: its slug changes, the old one stays a name of it. Otherwise by
// its slug. A new one is inserted pending; a known one takes the listed
// clone URL and, when it was missing, is pending again; a listed row the
// API no longer names becomes missing. A slug another repository of the
// project had (in any case) is left out and reported as a conflict: a name
// is never given to another. Hand-entered rows are not touched (a Bitbucket
// source has none, but the rule costs nothing).
func (s *Store) ApplyListing(ctx context.Context, key string, listed []ListedRepo) (ListingResult, error) {
	res := ListingResult{Conflicts: []string{}}
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		pid, ok, err := projectID(ctx, tx, key)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNoProject
		}
		now := nowStamp()
		seen := make([]int64, 0, len(listed))
		for _, l := range listed {
			rid, renamed, err := listedRepo(ctx, tx, pid, l)
			if errors.Is(err, ErrNameTaken) {
				res.Conflicts = append(res.Conflicts, l.Slug)
				continue
			}
			if err != nil {
				return err
			}
			if renamed {
				res.Renamed++
			}
			seen = append(seen, rid)
			// A new clone is an insert; a known one is left to the update
			// below, which a hand-entered row does not match.
			n, err := affected(tx.ExecContext(ctx, `
				INSERT INTO source_repos (repo_id, project_id, clone_url, origin, state, created_at)
				VALUES (?, ?, ?, 'listed', 'pending', ?)
				ON CONFLICT (repo_id) DO NOTHING`, rid, pid, l.CloneURL, now))
			if err != nil {
				return err
			}
			if n == 1 {
				res.Added++
				continue
			}
			if _, err := tx.ExecContext(ctx, `
				UPDATE source_repos
				   SET clone_url = ?2, state = CASE WHEN state = 'missing' THEN 'pending' ELSE state END
				 WHERE repo_id = ?1 AND origin = 'listed'`, rid, l.CloneURL); err != nil {
				return err
			}
		}
		ids, err := jsonOf(seen)
		if err != nil {
			return err
		}
		n, err := affected(tx.ExecContext(ctx, `
			UPDATE source_repos SET state = 'missing'
			 WHERE project_id = ? AND origin = 'listed' AND state <> 'missing'
			   AND repo_id NOT IN (SELECT value FROM json_each(?))`, pid, ids))
		if err != nil {
			return err
		}
		res.Missing = int(n)
		return nil
	})
	return res, err
}

// listedRepo is the repository a listed entry is: by forge id (renamed when
// its slug moved on), else by slug, else new. ErrNameTaken when the slug is
// another repository's, in any case.
func listedRepo(ctx context.Context, tx *sql.Tx, pid int64, l ListedRepo) (int64, bool, error) {
	if l.ForgeID != 0 {
		var rid int64
		var slug string
		err := tx.QueryRowContext(ctx, `SELECT id, slug FROM repos WHERE project_id = ? AND forge_id = ?`, pid, l.ForgeID).Scan(&rid, &slug)
		switch {
		case err == nil && slug == l.Slug:
			return rid, false, nil
		case err == nil:
			return rid, true, renameRepoTo(ctx, tx, pid, rid, l.Slug, "")
		case !errors.Is(err, sql.ErrNoRows):
			return 0, false, err
		}
	}
	rid, err := claimSlug(ctx, tx, pid, l.Slug)
	if err != nil {
		return 0, false, err
	}
	if l.ForgeID == 0 {
		return rid, false, nil
	}
	var forge sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT forge_id FROM repos WHERE id = ?`, rid).Scan(&forge); err != nil {
		return 0, false, err
	}
	switch {
	case !forge.Valid:
		// TRANSITIONAL(ids): a repository from before 0002 has no forge id
		// until a listing names it; its slug matches, so it takes the listed
		// one. Remove when every listed repository on every instance has a
		// forge_id (SELECT COUNT(*) FROM source_repos sr JOIN repos r ON
		// r.id = sr.repo_id WHERE sr.origin = 'listed' AND r.forge_id IS NULL
		// is 0); a new repository still takes its forge id below.
		_, err := tx.ExecContext(ctx, `UPDATE repos SET forge_id = ? WHERE id = ?`, l.ForgeID, rid)
		return rid, false, err
	case forge.Int64 != l.ForgeID:
		// The slug is a name of another repository Bitbucket knows by
		// another id: never given to a second one.
		return 0, false, ErrNameTaken
	}
	return rid, false, nil
}

// RequestSync marks a source's repos (or one repo, when slug is set) pending
// so the next cycle syncs them; a missing one stays missing. Reports how
// many rows moved.
func (s *Store) RequestSync(ctx context.Context, key, slug string) (int, error) {
	pid, rid, ok, err := scopeIDs(ctx, s.db, key, slug)
	if err != nil || !ok {
		return 0, err
	}
	n, err := affected(s.db.ExecContext(ctx, `
		UPDATE source_repos SET state = 'pending'
		 WHERE project_id = ?1 AND (?2 = 0 OR repo_id = ?2) AND state <> 'missing'`, pid, rid))
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
		SELECT `+sourceColumns+`, `+sourceRepoColumns+`
		  FROM `+sourceReposFrom+` JOIN sources s ON s.project_id = sr.project_id
		 WHERE sr.state <> 'missing'
		 ORDER BY p.key, r.slug`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []SyncJob{}
	for rows.Next() {
		var j SyncJob
		if err := rows.Scan(append(sourceDest(&j.Source), sourceRepoDest(&j.Repo)...)...); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// MarkRepoSyncing is the cycle taking a repo on.
func (s *Store) MarkRepoSyncing(ctx context.Context, key, slug string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE source_repos SET state = 'syncing' WHERE repo_id = `+repoByName, key, slug)
	return err
}

// MarkRepoReady records a successful sync: the branch (resolved or as
// configured), the head and the time; a left-over error goes.
func (s *Store) MarkRepoReady(ctx context.Context, key, slug, branch, head string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE source_repos SET state = 'ready', branch = NULLIF(?, ''), head_commit = ?,
		       last_synced_at = ?, last_error = ''
		 WHERE repo_id = `+repoByName, branch, head, nowStamp(), key, slug)
	return err
}

// MarkRepoFailed records why a sync failed; the head stays what it was.
func (s *Store) MarkRepoFailed(ctx context.Context, key, slug, errText string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE source_repos SET state = 'failed', last_error = ? WHERE repo_id = `+repoByName, errText, key, slug)
	return err
}

// nilIfEmpty is a token as bound: none is NULL, never an empty blob.
func nilIfEmpty(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}
