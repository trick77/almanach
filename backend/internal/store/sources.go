package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/trick77/almanach/internal/urlorigin"
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
	// Standalone: the repositories each stand for themselves, no project
	// page, no system map. False (every source by default) is one system.
	// A Bitbucket project is always one.
	Standalone bool
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
	// LastSyncedAt is the newest sync of any of its repositories, nil
	// before the first: a static source is never listed, so this is its
	// last run.
	LastSyncedAt *time.Time
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
	s.created_at, s.updated_at, s.last_listed_at, s.last_error, s.standalone`

func sourceDest(src *ProjectSource) []any {
	return []any{&src.ProjectID, &src.ProjectKey, &src.Kind, &src.Host, &src.TokenAuth, &src.TokenUser, &src.TokenEnc,
		timeAt(&src.CreatedAt), timeAt(&src.UpdatedAt), timeOrNil(&src.LastListedAt), &src.LastError, &src.Standalone}
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
		       COUNT(r.repo_id) FILTER (WHERE r.state = 'missing'),
		       MAX(r.last_synced_at)
		  FROM `+sourcesFrom+` LEFT JOIN source_repos r ON r.project_id = s.project_id
		 GROUP BY s.project_id ORDER BY p.key`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []SourceRow{}
	for rows.Next() {
		var r SourceRow
		if err := rows.Scan(append(sourceDest(&r.ProjectSource), &r.Repos, &r.Ready, &r.Active, &r.Failed, &r.Missing,
			timeOrNil(&r.LastSyncedAt))...); err != nil {
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

// CreateSource inserts a source in one transaction. A key a project had
// before finds that project again (its
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
			INSERT INTO sources (project_id, kind, host, token_auth, token_user, token_enc, created_at, updated_at, standalone)
			VALUES (?1, ?2, NULLIF(?3, ''), ?4, ?5, ?6, ?7, ?7, ?8)`,
			pid, src.Kind, src.Host, src.TokenAuth, src.TokenUser, nilIfEmpty(src.TokenEnc), nowStamp(), src.Standalone)
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

// SetSourceStandalone sets whether a source's repositories stand alone.
// Reports whether it changed: false when the source already said so, or
// is gone. Becoming a system again marks the project due, so the next run
// folds the page the folder kept.
func (s *Store) SetSourceStandalone(ctx context.Context, key string, standalone bool) (bool, error) {
	n, err := affected(s.db.ExecContext(ctx, `
		UPDATE sources SET standalone = ?1, project_due = NOT ?1, updated_at = ?2
		 WHERE project_id = `+projectByName+` AND standalone <> ?1`, standalone, nowStamp(), key))
	return n == 1, err
}

// ProjectSystem is what a project's source says of it as a system.
type ProjectSystem struct {
	// Standalone: the repositories stand for themselves, no project page.
	Standalone bool
	// Due: the folder became a system again, the project page it kept
	// must be folded by the next run.
	Due bool
}

// ProjectSystemOf is what a project's source says of it; a project without
// a source is a system, nothing due.
func (s *Store) ProjectSystemOf(ctx context.Context, key string) (ProjectSystem, error) {
	var ps ProjectSystem
	err := s.db.QueryRowContext(ctx, `SELECT standalone, project_due FROM sources WHERE project_id = `+projectByName,
		key).Scan(&ps.Standalone, &ps.Due)
	if errors.Is(err, sql.ErrNoRows) {
		return ProjectSystem{}, nil
	}
	return ps, err
}

// ProjectFolded clears a project's due mark: a project step got its answer.
func (s *Store) ProjectFolded(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sources SET project_due = 0 WHERE project_due AND project_id = `+projectByName, key)
	return err
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

// MarkSourceListed records a listing's outcome: the time when it was
// applied (listed), the error text when there is one: a failure, or
// repositories an applied listing left out. A failed listing keeps the
// time as it was.
func (s *Store) MarkSourceListed(ctx context.Context, key, errText string, listed bool) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE sources SET last_error = ?2,
		       last_listed_at = CASE WHEN ?4 THEN ?3 ELSE last_listed_at END
		 WHERE project_id = (SELECT project_id FROM project_names WHERE key = ?1)`, key, errText, nowStamp(), listed)
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

// oneHostIn is OneHost over the project's rows but the one of except (0:
// none), read in the caller's transaction, so a check and its write cannot
// leave the folder on two hosts.
func oneHostIn(ctx context.Context, tx *sql.Tx, pid, except int64, cloneURL string) error {
	rows, err := tx.QueryContext(ctx, `SELECT clone_url FROM source_repos WHERE project_id = ? AND repo_id <> ?`, pid, except)
	if err != nil {
		return err
	}
	have := []SourceRepo{}
	for rows.Next() {
		var h SourceRepo
		if err := rows.Scan(&h.CloneURL); err != nil {
			_ = rows.Close()
			return err
		}
		have = append(have, h)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	return OneHost(have, cloneURL)
}

// Origins is the hosts a source's rows are on (urlorigin), each once,
// sorted. One definition for the API and the poller, over all the rows,
// missing ones included, so the two never derive a folder's host apart.
func Origins(repos []SourceRepo) []string {
	urls := make([]string, 0, len(repos))
	for _, r := range repos {
		urls = append(urls, r.CloneURL)
	}
	return urlorigin.Distinct(urls)
}

// OtherHostError is a repository refused because its folder is on another
// host, or already on several: Hosts are the folder's.
type OtherHostError struct{ Hosts []string }

func (e *OtherHostError) Error() string {
	return "the folder is on " + strings.Join(e.Hosts, ", ")
}

// OneHost is nil when cloneURL may join a static folder holding have: the
// folder is empty, or on one host and cloneURL on it.
func OneHost(have []SourceRepo, cloneURL string) error {
	hosts := Origins(have)
	if len(hosts) == 0 || len(hosts) == 1 && urlorigin.Same(cloneURL, hosts[0]) {
		return nil
	}
	return &OtherHostError{Hosts: hosts}
}

// AddSourceRepo inserts a hand-entered repo as pending. A slug the project
// had before finds that repository again (its pages with it); ErrExists
// when it is a source repository already, ErrNameTaken when another
// repository of the project had the slug in another case, an
// *OtherHostError when the folder's rows are on another host (OneHost), a
// *ClonedElsewhereError when another source row clones the URL already.
// Checks and insert are one IMMEDIATE transaction, so two adds at once
// cannot leave the folder on two hosts.
func (s *Store) AddSourceRepo(ctx context.Context, r SourceRepo) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		pid, ok, err := projectID(ctx, tx, r.ProjectKey)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNoProject
		}
		if err := oneHostIn(ctx, tx, pid, 0, r.CloneURL); err != nil {
			return err
		}
		rid, _, err := claimSlug(ctx, tx, pid, r.Slug)
		if err != nil {
			return err
		}
		if err := clonedElsewhere(ctx, tx, rid, r.CloneURL); err != nil {
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
// that had it exactly (taking the slug back as its current one when it had
// moved on and is no source repository now), else a new one, created true.
// ErrNameTaken when another repository of the project had it in another
// case.
func claimSlug(ctx context.Context, tx *sql.Tx, pid int64, slug string) (int64, bool, error) {
	var self int64
	err := tx.QueryRowContext(ctx, `SELECT repo_id FROM repo_names WHERE project_id = ? AND slug = ?`, pid, slug).Scan(&self)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, false, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT slug, repo_id FROM repo_names WHERE project_id = ?`, pid)
	if err != nil {
		return 0, false, err
	}
	if taken, err := takenByOther(rows, slug, self); err != nil {
		return 0, false, err
	} else if taken {
		return 0, false, ErrNameTaken
	}
	if self == 0 {
		rid, err := ensureRepo(ctx, tx, pid, slug)
		return rid, true, err
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE repos SET slug = ? WHERE id = ? AND slug <> ?
		   AND NOT EXISTS (SELECT 1 FROM source_repos WHERE repo_id = ?)`, slug, self, slug, self)
	return self, false, err
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
// slugs left out because another repository of the project had them;
// Elsewhere the ones left out because another source clones their URL.
type ListingResult struct {
	Added     int
	Renamed   int
	Missing   int
	Conflicts []string
	Elsewhere []string
}

// ApplyListing reconciles a Bitbucket source's repos with what the API
// listed, in one transaction. A listed repository is found by its forge id
// first, so a repository renamed in Bitbucket keeps its id, its pages and
// its runs: its slug changes, the old one stays a name of it. Otherwise by
// its slug. A new one is inserted pending; a known one takes the listed
// clone URL and, when it was missing, is pending again; a listed row the
// API no longer names becomes missing. A slug another repository of the
// project had (in any case) is left out and reported as a conflict: a name
// is never given to another. A clone URL only another project's rows hold
// is left out and reported as elsewhere: one repository, one clone. Hand-entered rows are not touched (a Bitbucket
// source has none, but the rule costs nothing).
func (s *Store) ApplyListing(ctx context.Context, key string, listed []ListedRepo) (ListingResult, error) {
	res := ListingResult{Conflicts: []string{}, Elsewhere: []string{}}
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		pid, ok, err := projectID(ctx, tx, key)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNoProject
		}
		held, err := clonesHeld(ctx, tx)
		if err != nil {
			return err
		}
		now := nowStamp()
		seen := make([]int64, 0, len(listed))
		forges := make(map[int64]bool, len(listed))
		for _, l := range listed {
			if l.ForgeID != 0 {
				forges[l.ForgeID] = true
			}
		}
		for _, l := range listed {
			if elsewhere(held[CloneKey(l.CloneURL)], pid) {
				// One repository, one clone: another source has it. A row of
				// this project that knew it stays listed, its URL as it was.
				h := held[CloneKey(l.CloneURL)][0]
				res.Elsewhere = append(res.Elsewhere, l.Slug+" (as "+h.projectKey+"/"+h.slug+")")
				if rid, ok, err := knownListed(ctx, tx, pid, l); err != nil {
					return err
				} else if ok {
					seen = append(seen, rid)
				}
				continue
			}
			rid, renamed, err := listedRepo(ctx, tx, pid, l, forges)
			if errors.Is(err, ErrNameTaken) {
				res.Conflicts = append(res.Conflicts, l.Slug)
				if rid != 0 {
					// Known by its forge id, only its new slug is
					// another's: still in Bitbucket, never missing.
					seen = append(seen, rid)
				}
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

// elsewhere reports whether the rows holding a clone URL are all another
// project's. A row of this project holding it too (two sources cloned it
// before the rule) keeps it: the listing goes on as before.
func elsewhere(held []heldRow, pid int64) bool {
	for _, h := range held {
		if h.projectID == pid {
			return false
		}
	}
	return len(held) > 0
}

// knownListed is the listed source row of the project a listed entry is,
// by forge id, else by its current slug; false when there is none.
func knownListed(ctx context.Context, tx *sql.Tx, pid int64, l ListedRepo) (int64, bool, error) {
	var rid int64
	err := tx.QueryRowContext(ctx, `
		SELECT sr.repo_id FROM source_repos sr JOIN repos r ON r.id = sr.repo_id
		 WHERE sr.project_id = ?1 AND sr.origin = 'listed' AND ((?2 <> 0 AND r.forge_id = ?2) OR r.slug = ?3)
		 ORDER BY (r.forge_id IS ?2) DESC LIMIT 1`, pid, l.ForgeID, l.Slug).Scan(&rid)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return rid, err == nil, err
}

// listedRepo is the repository a listed entry is: by forge id (renamed when
// its slug moved on), else by slug, else new. ErrNameTaken when the slug is
// another repository's, in any case; the rid is then set when the entry is
// a known repository whose new slug is the taken one. forges are the forge
// ids the whole listing names.
func listedRepo(ctx context.Context, tx *sql.Tx, pid int64, l ListedRepo, forges map[int64]bool) (int64, bool, error) {
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
	rid, created, err := claimSlug(ctx, tx, pid, l.Slug)
	if err != nil {
		return 0, false, err
	}
	if l.ForgeID == 0 {
		return rid, false, nil
	}
	var forge sql.NullInt64
	var slug string
	if err := tx.QueryRowContext(ctx, `SELECT forge_id, slug FROM repos WHERE id = ?`, rid).Scan(&forge, &slug); err != nil {
		return 0, false, err
	}
	switch {
	case created:
		_, err := tx.ExecContext(ctx, `UPDATE repos SET forge_id = ? WHERE id = ?`, l.ForgeID, rid)
		return rid, false, err
	case !forge.Valid:
		// TRANSITIONAL(ids): a repository from before 0002 has no forge id
		// until a listing names it; its slug matches, so it takes the listed
		// one. Remove when every listed repository on every instance has a
		// forge_id (SELECT COUNT(*) FROM source_repos sr JOIN repos r ON
		// r.id = sr.repo_id WHERE sr.origin = 'listed' AND r.forge_id IS NULL
		// is 0).
		_, err := tx.ExecContext(ctx, `UPDATE repos SET forge_id = ? WHERE id = ?`, l.ForgeID, rid)
		return rid, false, err
	case forge.Int64 != l.ForgeID && forges[forge.Int64]:
		// The slug is a name of another repository Bitbucket still lists
		// under its own id: never given to a second one.
		return 0, false, ErrNameTaken
	case forge.Int64 != l.ForgeID:
		// The repository that had the slug is gone from Bitbucket and a new
		// one took the slug there. Same name, same repository here, as a
		// hand-entered slug added again: it takes the new id and, when it
		// had moved on to another slug since, the listed one back.
		if _, err := tx.ExecContext(ctx, `UPDATE repos SET forge_id = ? WHERE id = ?`, l.ForgeID, rid); err != nil {
			return 0, false, err
		}
		if slug != l.Slug {
			return rid, true, renameRepoTo(ctx, tx, pid, rid, l.Slug, "")
		}
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
