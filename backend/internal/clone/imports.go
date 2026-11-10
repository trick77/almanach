package clone

import (
	"context"
	"errors"

	"github.com/trick77/almanach/internal/baseline"
	"github.com/trick77/almanach/internal/gitrepo"
	"github.com/trick77/almanach/internal/imports"
	"github.com/trick77/almanach/internal/store"
)

// Beside the manifests the poller reads which packages inside the clone
// import which (internal/imports): Go and Python sources, each read whole,
// none a test. Code, no model call.

// importsDue reports whether the clone's imports are to be read at head:
// never read, read at another commit, or by an older imports.Version.
func (p *Poller) importsDue(ctx context.Context, j store.SyncJob, head string) bool {
	m := p.Manifests
	if m.Store == nil || m.Reads == nil || head == "" {
		return false
	}
	at, ok, err := m.Store.ImportsCommit(ctx, j.Repo.RepoID)
	if err != nil {
		p.Log.Warn("imports_check_failed", "project", j.Source.ProjectKey, "repo", j.Repo.Slug, "err", err.Error())
		return false
	}
	return !ok || at != head
}

// syncedImports is which packages of the clone import which at head, read
// when due; false when not due or not read (logged: the next cycle tries
// again). The clone's lock is the caller's.
func (p *Poller) syncedImports(ctx context.Context, j store.SyncJob, head string) (imports.Read, bool) {
	if !p.importsDue(ctx, j, head) {
		return imports.Read{}, false
	}
	dir, err := p.Dir(j.Repo.ProjectID, j.Repo.RepoID)
	var read imports.Read
	if err == nil {
		read, err = p.readImports(ctx, dir, head)
	}
	if err != nil {
		if ctx.Err() == nil {
			p.Log.Warn("imports_read_failed", "project", j.Source.ProjectKey, "repo", j.Repo.Slug, "err", err.Error())
		}
		return imports.Read{}, false
	}
	return read, true
}

// readImports reads what imports.Parse needs at head: the Go and Python
// sources and their go.mod and pyproject.toml files, each readable whole,
// none a test (imports.Wanted, baseline.TestFile). A generator's output
// counts: its imports are the code's. Bodies pass redact.Text, as every
// read's do; only folders and names leave Parse.
func (p *Poller) readImports(ctx context.Context, dir, head string) (imports.Read, error) {
	entries, err := p.Manifests.Reads.ListEntries(ctx, dir, head)
	if err != nil {
		return imports.Read{}, err
	}
	var want []gitrepo.Entry
	for _, e := range entries {
		if baseline.Readable(e) && e.Size <= baseline.FileMaxBytes && !baseline.TestFile(e.Path) && imports.Wanted(e.Path) {
			want = append(want, e)
		}
	}
	if len(want) == 0 {
		return imports.Parse(nil), nil
	}
	r, err := p.Manifests.Reads.NewReader(ctx, dir)
	if err != nil {
		return imports.Read{}, err
	}
	defer func() { _ = r.Close() }()
	files := map[string][]byte{}
	if err := readBodies(ctx, r, head, want, baseline.FileMaxBytes, true, files); err != nil {
		return imports.Read{}, err
	}
	return imports.Parse(files), nil
}

// saveImports stores what was read; a failure is logged, never the
// repository's.
func (p *Poller) saveImports(ctx context.Context, j store.SyncJob, head string, read imports.Read) {
	err := p.Manifests.Store.SaveImports(ctx, j.Repo.RepoID, head, read)
	switch {
	case errors.Is(err, store.ErrNoRepo):
	case err != nil:
		p.Log.Warn("imports_save_failed", "project", j.Source.ProjectKey, "repo", j.Repo.Slug, "err", err.Error())
	default:
		p.Log.Info("imports_read", "project", j.Source.ProjectKey, "repo", j.Repo.Slug, "head", gitrepo.ShortSHA(head),
			"units", len(read.Units), "edges", len(read.Edges))
	}
}
