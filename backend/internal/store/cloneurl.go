package store

import (
	"context"
	"database/sql"
	"net/url"
	"strings"

	"github.com/trick77/almanach/internal/urlorigin"
)

// One repository is cloned once: a clone URL is one source row's, across
// every source. Two rows of one repository would be two clones, two pages
// described at twice the cost, and a coordinate two repositories provide,
// which gives the dependency graph no edge to either.

// CloneKey is a clone URL as the repository it names: the origin as
// urlorigin has it (scheme and host lower case, the default port dropped),
// the path lower case without a trailing slash or `.git`. Lower case
// because the forges in use (Bitbucket, GitHub, GitLab) name a repository
// in any case; a server that tells two repositories apart by case alone
// would have one refused, never one cloned twice. "" for a URL with no
// origin.
func CloneKey(cloneURL string) string {
	origin := urlorigin.Of(cloneURL)
	if origin == "" {
		return ""
	}
	u, err := url.Parse(cloneURL)
	if err != nil {
		return ""
	}
	p := strings.ToLower(strings.TrimRight(u.Path, "/"))
	p = strings.TrimRight(strings.TrimSuffix(p, ".git"), "/")
	return origin + p
}

// ClonedElsewhereError is a clone URL refused because another source row
// clones that repository already: the row's project and slug.
type ClonedElsewhereError struct {
	ProjectKey string
	Slug       string
}

func (e *ClonedElsewhereError) Error() string {
	return "the repository is cloned as " + e.ProjectKey + "/" + e.Slug + " already"
}

// heldRow is a source row holding a clone URL.
type heldRow struct {
	repoID     int64
	projectID  int64
	projectKey string
	slug       string
}

// clonesHeld is every source row by CloneKey. A row gone missing from its
// listing holds nothing: the repository moved (to a folder, another
// project) must not wait for its old row's removal, and the old project's
// next listing that names it again reports it instead.
func clonesHeld(ctx context.Context, tx *sql.Tx) (map[string][]heldRow, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT sr.repo_id, sr.project_id, p.key, r.slug, sr.clone_url
		  FROM source_repos sr
		  JOIN repos r ON r.id = sr.repo_id
		  JOIN projects p ON p.id = sr.project_id
		 WHERE sr.state <> 'missing'`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string][]heldRow{}
	for rows.Next() {
		var h heldRow
		var u string
		if err := rows.Scan(&h.repoID, &h.projectID, &h.projectKey, &h.slug, &u); err != nil {
			return nil, err
		}
		if k := CloneKey(u); k != "" {
			out[k] = append(out[k], h)
		}
	}
	return out, rows.Err()
}

// clonedElsewhere is nil when no source row but repoID's clones cloneURL,
// else a *ClonedElsewhereError naming the first other one.
func clonedElsewhere(ctx context.Context, tx *sql.Tx, repoID int64, cloneURL string) error {
	held, err := clonesHeld(ctx, tx)
	if err != nil {
		return err
	}
	for _, h := range held[CloneKey(cloneURL)] {
		if h.repoID != repoID {
			return &ClonedElsewhereError{ProjectKey: h.projectKey, Slug: h.slug}
		}
	}
	return nil
}
