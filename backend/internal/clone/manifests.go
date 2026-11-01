package clone

import (
	"context"
	"errors"

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
// caller. Those over FileMaxBytes, binary or a Secret manifest are left
// out; a manifest that does not parse counts for nothing (manifest.Parse).
// Every body passes redact.File first: a version is shown on the pages,
// and a dependency URL may carry a token.
func (p *Poller) readManifests(ctx context.Context, dir, head string) (manifest.Read, error) {
	entries, err := p.Manifests.Reads.ListEntries(ctx, dir, head)
	if err != nil {
		return manifest.Read{}, err
	}
	files := map[string][]byte{}
	var r *gitrepo.Reader
	defer func() {
		if r != nil {
			_ = r.Close()
		}
	}()
	for _, e := range entries {
		if !baseline.Readable(e) || !manifest.Is(e.Path) {
			continue
		}
		if r == nil {
			if r, err = p.Manifests.Reads.NewReader(ctx, dir); err != nil {
				return manifest.Read{}, err
			}
		}
		body, err := r.ReadFile(head, e.Path, baseline.FileMaxBytes)
		if ctx.Err() != nil {
			return manifest.Read{}, ctx.Err()
		}
		if errors.Is(err, gitrepo.ErrReaderBroken) || errors.Is(err, gitrepo.ErrBadSHA) {
			return manifest.Read{}, err
		}
		if err != nil || baseline.Binary(body) {
			continue
		}
		if body, ok := redact.File(e.Path, body); ok {
			files[e.Path] = body
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
