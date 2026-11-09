package clone

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/trick77/almanach/internal/baseline"
	"github.com/trick77/almanach/internal/endpoints"
	"github.com/trick77/almanach/internal/gitrepo"
	"github.com/trick77/almanach/internal/manifest"
	"github.com/trick77/almanach/internal/messaging"
	"github.com/trick77/almanach/internal/redact"
	"github.com/trick77/almanach/internal/store"
)

// After a sync the poller reads the clone's build manifests at its head,
// under the clone's lock, when they were last read at another commit (or
// never): what the repository provides and requires, for the dependency
// graph the store joins at read time. Code, no model call, so the graph is
// current after every sync, not after the next run.

// ManifestStore is what reading the manifests, and the messages beside
// them, needs of the store.
type ManifestStore interface {
	ManifestCommit(ctx context.Context, repoID int64) (string, bool, error)
	SaveManifests(ctx context.Context, repoID int64, commit string, read manifest.Read) error
	TopicsCommit(ctx context.Context, repoID int64) (string, bool, error)
	SaveTopics(ctx context.Context, repoID int64, commit string, read messaging.Read) error
	EndpointsCommit(ctx context.Context, repoID int64) (string, bool, error)
	SaveEndpoints(ctx context.Context, repoID int64, commit string, read endpoints.Read) error
}

// ManifestReads is what reading the manifests and messages needs of
// gitrepo.
type ManifestReads interface {
	ListEntries(ctx context.Context, dir, sha string) ([]gitrepo.Entry, error)
	NewReader(ctx context.Context, dir string) (*gitrepo.Reader, error)
	GrepFiles(ctx context.Context, dir, sha, pattern string) ([]string, error)
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
	// A manifest is parsed whole: one over FileMaxBytes, which a step would
	// read as its head, is not streamed out of git only to be refused.
	whole := func(e gitrepo.Entry) bool { return baseline.Readable(e) && e.Size <= baseline.FileMaxBytes }
	for _, e := range entries {
		if whole(e) && manifest.Is(e.Path) {
			if err := read(e); err != nil {
				return manifest.Read{}, err
			}
		}
	}
	// Then the folders the builds declare (manifest.Modules), at any depth,
	// until a pass declares none new: a Maven module may list modules of
	// its own. Each module is visited once, so the passes end. A
	// workspace's patterns are matched against the tree's folders.
	byDir := map[string][]gitrepo.Entry{}
	var dirs []string
	for _, e := range entries {
		if whole(e) {
			d := path.Dir(e.Path)
			if _, ok := byDir[d]; !ok && d != "." {
				dirs = append(dirs, d)
			}
			byDir[d] = append(byDir[d], e)
		}
	}
	visited := map[manifest.Module]bool{}
	for {
		var next []gitrepo.Entry
		for _, m := range manifest.Modules(files, dirs) {
			if visited[m] {
				continue
			}
			visited[m] = true
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

// ReadManifestsNow reads and stores the manifests of a ready clone at
// head outside a cycle, under the clone's lock: a clone this poller did
// not sync itself (laid down by its caller) gets its dependencies too.
func (p *Poller) ReadManifestsNow(ctx context.Context, key, slug, head string) error {
	pid, rid, ok, err := p.Store.RepoIDs(ctx, key, slug)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("repository %s/%s: %w", key, slug, store.ErrNoRepo)
	}
	unlock, ok := p.tryLockRepo(rid)
	if !ok {
		return fmt.Errorf("repository %s/%s: the clone is busy", key, slug)
	}
	defer unlock()
	j := store.SyncJob{Source: store.ProjectSource{ProjectID: pid, ProjectKey: key}, Repo: store.SourceRepo{ProjectID: pid, RepoID: rid, ProjectKey: key, Slug: slug}}
	// Each when due, on its own: manifests current at head leave topics a
	// newer reader wants still to read.
	if topics, ok := p.syncedTopics(ctx, j, head); ok {
		p.saveTopics(ctx, j, head, topics)
	}
	if eps, ok := p.syncedEndpoints(ctx, j, head); ok {
		p.saveEndpoints(ctx, j, head, eps)
	}
	read, ok := p.syncedManifests(ctx, j, head)
	if !ok {
		return fmt.Errorf("repository %s/%s: manifests not read", key, slug)
	}
	p.saveManifests(ctx, j, head, read)
	return nil
}

// topicsDue reports whether the clone's messages are to be read at head:
// never read, read at another commit, or by an older messaging.Version.
func (p *Poller) topicsDue(ctx context.Context, j store.SyncJob, head string) bool {
	m := p.Manifests
	if m.Store == nil || m.Reads == nil || head == "" {
		return false
	}
	at, ok, err := m.Store.TopicsCommit(ctx, j.Repo.RepoID)
	if err != nil {
		p.Log.Warn("topics_check_failed", "project", j.Source.ProjectKey, "repo", j.Repo.Slug, "err", err.Error())
		return false
	}
	return !ok || at != head
}

// syncedTopics is what the clone's code says of its messages at head, read
// when due; false when not due or not read (logged: the next cycle tries
// again). The clone's lock is the caller's.
func (p *Poller) syncedTopics(ctx context.Context, j store.SyncJob, head string) (messaging.Read, bool) {
	if !p.topicsDue(ctx, j, head) {
		return messaging.Read{}, false
	}
	dir, err := p.Dir(j.Repo.ProjectID, j.Repo.RepoID)
	var read messaging.Read
	if err == nil {
		read, err = p.readTopics(ctx, dir, head)
	}
	if err != nil {
		if ctx.Err() == nil {
			p.Log.Warn("topics_read_failed", "project", j.Source.ProjectKey, "repo", j.Repo.Slug, "err", err.Error())
		}
		return messaging.Read{}, false
	}
	return read, true
}

// readTopics reads what messaging.Parse needs at head: the files git finds
// a topic or Kafka in, the schemas and the application properties, each
// readable whole, none a test or a generator's output. Bodies pass
// redact.Text, as the manifests' do; only names leave Parse.
func (p *Poller) readTopics(ctx context.Context, dir, head string) (messaging.Read, error) {
	entries, err := p.Manifests.Reads.ListEntries(ctx, dir, head)
	if err != nil {
		return messaging.Read{}, err
	}
	hits, err := p.Manifests.Reads.GrepFiles(ctx, dir, head, messaging.GrepPattern)
	if err != nil {
		return messaging.Read{}, err
	}
	hit := make(map[string]bool, len(hits))
	for _, h := range hits {
		hit[h] = true
	}
	var want []gitrepo.Entry
	for _, e := range entries {
		if !baseline.Readable(e) || e.Size > baseline.FileMaxBytes || baseline.TestFile(e.Path) {
			continue
		}
		if messaging.SchemaFile(e.Path) || messaging.Config(e.Path) || hit[e.Path] && messaging.Source(e.Path) {
			want = append(want, e)
		}
	}
	files := map[string][]byte{}
	if len(want) > 0 {
		r, err := p.Manifests.Reads.NewReader(ctx, dir)
		if err != nil {
			return messaging.Read{}, err
		}
		defer func() { _ = r.Close() }()
		if err := readBodies(ctx, r, head, want, baseline.FileMaxBytes, false, files); err != nil {
			return messaging.Read{}, err
		}
	}
	return messaging.Parse(files), nil
}

// readBodies reads entries at head into files, each redacted: up to limit
// bytes, none binary, a generator's output only with generated. A file
// that cannot be read is left out; a broken reader or an ended context
// ends the read.
func readBodies(ctx context.Context, r *gitrepo.Reader, head string, es []gitrepo.Entry, limit int64, generated bool,
	files map[string][]byte) error {
	for _, e := range es {
		body, err := r.ReadFile(head, e.Path, limit)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, gitrepo.ErrReaderBroken) || errors.Is(err, gitrepo.ErrBadSHA) {
			return err
		}
		if err == nil && !baseline.Binary(body) && (generated || !baseline.Generated(body)) {
			files[e.Path] = []byte(redact.Text(string(body)))
		}
	}
	return nil
}

// saveTopics stores what was read; a failure is logged, never the
// repository's.
func (p *Poller) saveTopics(ctx context.Context, j store.SyncJob, head string, read messaging.Read) {
	err := p.Manifests.Store.SaveTopics(ctx, j.Repo.RepoID, head, read)
	switch {
	case errors.Is(err, store.ErrNoRepo):
	case err != nil:
		p.Log.Warn("topics_save_failed", "project", j.Source.ProjectKey, "repo", j.Repo.Slug, "err", err.Error())
	default:
		p.Log.Info("topics_read", "project", j.Source.ProjectKey, "repo", j.Repo.Slug, "head", gitrepo.ShortSHA(head),
			"topics", len(read.Topics), "schemas", len(read.Schemas))
	}
}

// endpointsDue reports whether the clone's interfaces are to be read at
// head: never read, read at another commit, or by an older
// endpoints.Version.
func (p *Poller) endpointsDue(ctx context.Context, j store.SyncJob, head string) bool {
	m := p.Manifests
	if m.Store == nil || m.Reads == nil || head == "" {
		return false
	}
	at, ok, err := m.Store.EndpointsCommit(ctx, j.Repo.RepoID)
	if err != nil {
		p.Log.Warn("endpoints_check_failed", "project", j.Source.ProjectKey, "repo", j.Repo.Slug, "err", err.Error())
		return false
	}
	return !ok || at != head
}

// syncedEndpoints is what the clone's code says of its interfaces at head,
// read when due; false when not due or not read (logged: the next cycle
// tries again). The clone's lock is the caller's.
func (p *Poller) syncedEndpoints(ctx context.Context, j store.SyncJob, head string) (endpoints.Read, bool) {
	if !p.endpointsDue(ctx, j, head) {
		return endpoints.Read{}, false
	}
	dir, err := p.Dir(j.Repo.ProjectID, j.Repo.RepoID)
	var read endpoints.Read
	if err == nil {
		read, err = p.readEndpoints(ctx, dir, head)
	}
	if err != nil {
		if ctx.Err() == nil {
			p.Log.Warn("endpoints_read_failed", "project", j.Source.ProjectKey, "repo", j.Repo.Slug, "err", err.Error())
		}
		return endpoints.Read{}, false
	}
	return read, true
}

// readEndpoints reads what endpoints.Parse needs at head, none a test:
//   - sources git finds a route or service in, builds and application
//     properties: readable whole (FileMaxBytes), none a generator's output;
//   - contracts (specs git finds, protos, the WSDLs a build or source
//     names, the files a spec's path items point to): up to CutMaxBytes, a
//     generator's output too. A committed spec export is large and marked
//     generated, and is the one that says what the service offers; only
//     names leave Parse, no model reads the bytes.
//
// Bodies pass redact.Text, as the manifests' do.
func (p *Poller) readEndpoints(ctx context.Context, dir, head string) (endpoints.Read, error) {
	entries, err := p.Manifests.Reads.ListEntries(ctx, dir, head)
	if err != nil {
		return endpoints.Read{}, err
	}
	hits, err := p.Manifests.Reads.GrepFiles(ctx, dir, head, endpoints.GrepPattern)
	if err != nil {
		return endpoints.Read{}, err
	}
	hit := make(map[string]bool, len(hits))
	for _, h := range hits {
		hit[h] = true
	}
	specHits, err := p.Manifests.Reads.GrepFiles(ctx, dir, head, endpoints.SpecPattern)
	if err != nil {
		return endpoints.Read{}, err
	}
	specHit := make(map[string]bool, len(specHits))
	for _, h := range specHits {
		specHit[h] = true
	}
	byPath := map[string]gitrepo.Entry{}
	var code, whole, wsdls []gitrepo.Entry
	for _, e := range entries {
		if !baseline.Readable(e) || baseline.TestFile(e.Path) {
			continue
		}
		byPath[e.Path] = e
		ext := strings.ToLower(path.Ext(e.Path))
		switch {
		case ext == ".wsdl":
			wsdls = append(wsdls, e)
		case ext == ".proto":
			whole = append(whole, e)
		case endpoints.Build(e.Path) || endpoints.Config(e.Path):
			code = append(code, e)
		case endpoints.SpecCandidate(e.Path) && specHit[e.Path]:
			whole = append(whole, e)
		case endpoints.Source(e.Path) && hit[e.Path]:
			code = append(code, e)
		}
	}
	if len(code)+len(whole)+len(wsdls) == 0 {
		return endpoints.Parse(nil), nil
	}
	r, err := p.Manifests.Reads.NewReader(ctx, dir)
	if err != nil {
		return endpoints.Read{}, err
	}
	defer func() { _ = r.Close() }()
	files := map[string][]byte{}
	read := func(es []gitrepo.Entry, contract bool) error {
		if contract {
			return readBodies(ctx, r, head, es, baseline.CutMaxBytes, true, files)
		}
		var fit []gitrepo.Entry
		for _, e := range es {
			if e.Size <= baseline.FileMaxBytes {
				fit = append(fit, e)
			}
		}
		return readBodies(ctx, r, head, fit, baseline.FileMaxBytes, false, files)
	}
	if err := read(code, false); err != nil {
		return endpoints.Read{}, err
	}
	if err := read(whole, true); err != nil {
		return endpoints.Read{}, err
	}
	if len(wsdls) > 0 {
		names := make([]string, len(wsdls))
		for i, e := range wsdls {
			names[i] = e.Path
		}
		var named []gitrepo.Entry
		for _, p := range endpoints.WSDLNamed(names, files) {
			named = append(named, byPath[p])
		}
		if err := read(named, true); err != nil {
			return endpoints.Read{}, err
		}
	}
	// A path item's $ref chain, file by file: what one read names next.
	for range 4 {
		var refs []gitrepo.Entry
		for _, p := range endpoints.Refs(files) {
			if e, ok := byPath[p]; ok {
				refs = append(refs, e)
			}
		}
		if len(refs) == 0 {
			break
		}
		if err := read(refs, true); err != nil {
			return endpoints.Read{}, err
		}
	}
	return endpoints.Parse(files), nil
}

// saveEndpoints stores what was read; a failure is logged, never the
// repository's.
func (p *Poller) saveEndpoints(ctx context.Context, j store.SyncJob, head string, read endpoints.Read) {
	err := p.Manifests.Store.SaveEndpoints(ctx, j.Repo.RepoID, head, read)
	switch {
	case errors.Is(err, store.ErrNoRepo):
	case err != nil:
		p.Log.Warn("endpoints_save_failed", "project", j.Source.ProjectKey, "repo", j.Repo.Slug, "err", err.Error())
	default:
		p.Log.Info("endpoints_read", "project", j.Source.ProjectKey, "repo", j.Repo.Slug, "head", gitrepo.ShortSHA(head),
			"apis", len(read.APIs), "endpoints", len(read.Endpoints))
	}
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
