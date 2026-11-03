package clone

import (
	"context"
	"errors"
	"path"

	"github.com/trick77/almanach/internal/baseline"
	"github.com/trick77/almanach/internal/gitrepo"
	"github.com/trick77/almanach/internal/manifest"
	"github.com/trick77/almanach/internal/redact"
	"github.com/trick77/almanach/internal/store"
)

// After a sync the poller reads the clone's build manifests at its head,
// under the clone's lock, when they were last read at another commit (or
// never): what the repository provides and requires, for the dependency
// graph the store joins at read time. Code, no model call, so the graph is
// current after every sync, not after the next run.

// ManifestStore is what reading the manifests needs of the store.
type ManifestStore interface {
	ManifestCommit(ctx context.Context, repoID int64) (string, bool, error)
	SaveManifests(ctx context.Context, repoID int64, commit string, read manifest.Read) error
}

// ManifestReads is what reading the manifests needs of gitrepo.
type ManifestReads interface {
	ListEntries(ctx context.Context, dir, sha string) ([]gitrepo.Entry, error)
	NewReader(ctx context.Context, dir string) (*gitrepo.Reader, error)
}

// Manifests is the store and the reads; either nil: no manifests are read.
type Manifests struct {
	Store ManifestStore
	Reads ManifestReads
}

// manifestsDue reports whether the clone's manifests are to be read at
// head: they never were, or at another commit. A store that does not
// answer reads nothing this cycle; the next one asks again.
func (p *Poller) manifestsDue(ctx context.Context, j store.SyncJob, head string) bool {
	m := p.Manifests
	if m.Store == nil || m.Reads == nil || head == "" {
		return false
	}
	at, ok, err := m.Store.ManifestCommit(ctx, j.Repo.RepoID)
	if err != nil {
		p.Log.Warn("manifests_check_failed", "project", j.Source.ProjectKey, "repo", j.Repo.Slug, "err", err.Error())
		return false
	}
	return !ok || at != head
}

// readManifests reads the manifests at head, the clone's lock held by the
// caller. Those over FileMaxBytes or binary are left out; a manifest that
// does not parse counts for nothing (manifest.Parse). Every body passes
// redact.Text first, each credential shape replaced: a version is shown on
// the pages, and a dependency URL may carry a token. Not redact.File: its
// key rules for .properties and .toml read a catalog alias or a property
// as a secret (`jjwt-api` holds `jsonwebtoken:`, `tokenLibVersion`) and
// would blank the coordinate itself. Only coordinates and versions leave
// Parse, never a body, and Parse resolves no `$x` from a key those rules
// call secret (redact.SecretKey).
func (p *Poller) readManifests(ctx context.Context, dir, head string) (manifest.Read, error) {
	entries, err := p.Manifests.Reads.ListEntries(ctx, dir, head)
	if err != nil {
		return manifest.Read{}, err
	}
	files := map[string][]byte{}
	tried := map[string]bool{}
	var r *gitrepo.Reader
	defer func() {
		if r != nil {
			_ = r.Close()
		}
	}()
	read := func(e gitrepo.Entry) error {
		tried[e.Path] = true
		if r == nil {
			nr, err := p.Manifests.Reads.NewReader(ctx, dir)
			if err != nil {
				return err
			}
			r = nr
		}
		body, err := r.ReadFile(head, e.Path, baseline.FileMaxBytes)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, gitrepo.ErrReaderBroken) || errors.Is(err, gitrepo.ErrBadSHA) {
			return err
		}
		if err == nil && !baseline.Binary(body) {
			files[e.Path] = []byte(redact.Text(string(body)))
		}
		return nil
	}
	for _, e := range entries {
		if baseline.Readable(e) && manifest.Is(e.Path) {
			if err := read(e); err != nil {
				return manifest.Read{}, err
			}
		}
	}
	// Then the folders the builds declare (manifest.Modules), at any depth,
	// until a pass declares none new: a Maven module may list modules of
	// its own. Each module is visited once, so the passes end.
	var byDir map[string][]gitrepo.Entry
	visited := map[manifest.Module]bool{}
	for {
		var next []gitrepo.Entry
		for _, m := range manifest.Modules(files) {
			if visited[m] {
				continue
			}
			visited[m] = true
			if byDir == nil {
				byDir = map[string][]gitrepo.Entry{}
				for _, e := range entries {
					if baseline.Readable(e) {
						byDir[path.Dir(e.Path)] = append(byDir[path.Dir(e.Path)], e)
					}
				}
			}
			for _, e := range byDir[m.Dir] {
				if !tried[e.Path] && m.Reads(path.Base(e.Path)) {
					next = append(next, e)
				}
			}
		}
		if len(next) == 0 {
			break
		}
		for _, e := range next {
			if err := read(e); err != nil {
				return manifest.Read{}, err
			}
		}
	}
	return manifest.Parse(files), nil
}

// syncedManifests is the manifests of a clone just synced to head, read
// when due; false when they are not due or could not be read (logged: the
// next cycle tries again). ctx is the sync's own, so a removal cuts it.
func (p *Poller) syncedManifests(ctx context.Context, j store.SyncJob, head string) (manifest.Read, bool) {
	if !p.manifestsDue(ctx, j, head) {
		return manifest.Read{}, false
	}
	dir, err := p.Dir(j.Repo.ProjectID, j.Repo.RepoID)
	var read manifest.Read
	if err == nil {
		read, err = p.readManifests(ctx, dir, head)
	}
	if err != nil {
		if ctx.Err() == nil {
			p.Log.Warn("manifests_read_failed", "project", j.Source.ProjectKey, "repo", j.Repo.Slug, "err", err.Error())
		}
		return manifest.Read{}, false
	}
	return read, true
}

// saveManifests stores what was read; a failure is logged, never the
// repository's: its sync went well, the next cycle reads again.
func (p *Poller) saveManifests(ctx context.Context, j store.SyncJob, head string, read manifest.Read) {
	err := p.Manifests.Store.SaveManifests(ctx, j.Repo.RepoID, head, read)
	switch {
	case errors.Is(err, store.ErrNoRepo):
		// Removed while it was read.
	case err != nil:
		p.Log.Warn("manifests_save_failed", "project", j.Source.ProjectKey, "repo", j.Repo.Slug, "err", err.Error())
	default:
		p.Log.Info("manifests_read", "project", j.Source.ProjectKey, "repo", j.Repo.Slug, "head", gitrepo.ShortSHA(head),
			"provides", len(read.Provides), "requires", len(read.Requires))
	}
}
