package clone

import (
	"context"
	"encoding/base64"
	"errors"
	"hash/crc32"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/trick77/almanach/internal/gitrepo"
	"github.com/trick77/almanach/internal/secret"
	"github.com/trick77/almanach/internal/store"
)

type fakeStore struct {
	mu      sync.Mutex
	sources []store.SourceRow
	jobs    []store.SyncJob
	listed  map[string][]store.ListedRepo
	listErr map[string]string
	states  map[string]string
	heads   map[string]string
	branch  map[string]string
	errs    map[string]string
	// The baseline runs: what was queued, the newest and the documented.
	enqueued   []string
	enqueueErr error
	last       store.BaselineRun
	documented store.BaselineRun
	runErr     error
	failed     int
	docAsked   int
	// conflicts is what ApplyListing reports left out.
	conflicts []string
}

func newFakeStore() *fakeStore {
	return &fakeStore{listed: map[string][]store.ListedRepo{}, listErr: map[string]string{}, states: map[string]string{},
		heads: map[string]string{}, branch: map[string]string{}, errs: map[string]string{}}
}

func (f *fakeStore) Sources(context.Context) ([]store.SourceRow, error) { return f.sources, nil }
func (f *fakeStore) ApplyListing(_ context.Context, key string, l []store.ListedRepo) (store.ListingResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listed[key] = l
	return store.ListingResult{Added: len(l) - len(f.conflicts), Conflicts: f.conflicts}, nil
}
func (f *fakeStore) MarkSourceListed(_ context.Context, key, e string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listErr[key] = e
	return nil
}
func (f *fakeStore) SyncJobs(context.Context) ([]store.SyncJob, error) { return f.jobs, nil }
func (f *fakeStore) MarkRepoSyncing(_ context.Context, _, slug string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.states[slug] = store.RepoSyncing
	return nil
}
func (f *fakeStore) MarkRepoReady(_ context.Context, _, slug, branch, head string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.states[slug], f.branch[slug], f.heads[slug], f.errs[slug] = store.RepoReady, branch, head, ""
	return nil
}
func (f *fakeStore) MarkRepoFailed(_ context.Context, _, slug, e string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.states[slug], f.errs[slug] = store.RepoFailed, e
	return nil
}

func (f *fakeStore) EnqueueRun(_ context.Context, key, slug, mode, trigger, base, commit string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.enqueueErr != nil {
		return 0, f.enqueueErr
	}
	f.enqueued = append(f.enqueued, strings.Join([]string{key, slug, mode, trigger, base, commit}, " "))
	return int64(len(f.enqueued)), nil
}
func (f *fakeStore) RepoIDs(_ context.Context, key, slug string) (int64, int64, bool, error) {
	return idOf(key), idOf(key + "/" + slug), true, nil
}

// idOf is the fake's id for a name: stable, so a test can say where a
// clone lives without a store.
func idOf(name string) int64 { return int64(crc32.ChecksumIEEE([]byte(name)) % 100000) }

// dirOf is where the poller keeps key/slug's clone: by ids, under the
// fake's root.
func dirOf(key, slug string) string {
	return "/base/" + projectSeg(idOf(key)) + "/" + repoSeg(idOf(key+"/"+slug))
}

func (f *fakeStore) LastRun(context.Context, string, string) (store.BaselineRun, int, bool, error) {
	return f.last, f.failed, f.last.ID != 0, f.runErr
}
func (f *fakeStore) DocumentedRun(context.Context, string, string) (store.BaselineRun, bool, error) {
	f.mu.Lock()
	f.docAsked++
	f.mu.Unlock()
	return f.documented, f.documented.ID != 0, f.runErr
}

// state reads a repo's state under the lock, for a test that polls while
// the poller runs.
func (f *fakeStore) state(slug string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.states[slug]
}

type fakeGit struct {
	origins  map[string]string // dir → origin ("" = no checkout)
	heads    map[string]string
	fetchErr map[string]error
	calls    []string
	access   []gitrepo.Access
	onClone  func(dir string)
	// legacy are clones in folders named by names ("/base/key/slug"), as
	// before the ids; MoveLegacy moves them and records the move in moved.
	legacy map[string]string
	moved  []string
}

// MoveLegacy is gitrepo's: TRANSITIONAL(ids), goes with it.
func (g *fakeGit) MoveLegacy(fromKey, fromSlug, toKey, toSlug string) (bool, error) {
	from, to := "/base/"+fromKey+"/"+fromSlug, "/base/"+toKey+"/"+toSlug
	origin, ok := g.legacy[from]
	if !ok || g.origins[to] != "" {
		return false, nil
	}
	delete(g.legacy, from)
	g.origins[to] = origin
	g.moved = append(g.moved, from+" → "+to)
	return true, nil
}

func newFakeGit() *fakeGit {
	return &fakeGit{origins: map[string]string{}, heads: map[string]string{}, fetchErr: map[string]error{}, legacy: map[string]string{}}
}

func (g *fakeGit) Dir(key, slug string) (string, error) {
	if !gitrepo.ValidSegment(key) || !gitrepo.ValidSegment(slug) {
		return "", gitrepo.ErrBadSegment
	}
	return "/base/" + key + "/" + slug, nil
}
func (g *fakeGit) OriginURL(_ context.Context, dir string) (string, error) {
	return g.origins[dir], nil
}
func (g *fakeGit) EnsureCloned(_ context.Context, dir, url string, a gitrepo.Access) error {
	g.access = append(g.access, a)
	if g.origins[dir] == "" {
		g.calls = append(g.calls, "clone "+dir)
		g.origins[dir] = url
	}
	if g.onClone != nil {
		g.onClone(dir)
	}
	return nil
}
func (g *fakeGit) DefaultBranch(context.Context, string, gitrepo.Access) (string, error) {
	g.calls = append(g.calls, "default-branch")
	return "main", nil
}
func (g *fakeGit) Fetch(_ context.Context, dir, _ string, _ gitrepo.Access) error {
	g.calls = append(g.calls, "fetch "+dir)
	return g.fetchErr[dir]
}
func (g *fakeGit) HeadSHA(_ context.Context, dir, branch string) (string, error) {
	if h, ok := g.heads[dir+"@"+branch]; ok {
		return h, nil
	}
	return "", gitrepo.ErrBranchGone
}
func (g *fakeGit) ProjectDir(key string) (string, error) { return "/base/" + key, nil }
func (g *fakeGit) Remove(key, slug string) error {
	dir := "/base/" + key + "/" + slug
	g.calls = append(g.calls, "remove "+dir)
	delete(g.origins, dir)
	return nil
}
func (g *fakeGit) RemoveProject(key string) error {
	g.calls = append(g.calls, "remove-project /base/"+key)
	return nil
}

type fakeLister struct {
	repos []store.ListedRepo
	err   error
	seen  []gitrepo.Access
}

func (l *fakeLister) ListRepos(_ context.Context, _, _ string, a gitrepo.Access) ([]store.ListedRepo, error) {
	l.seen = append(l.seen, a)
	return l.repos, l.err
}

func testKey(t *testing.T) secret.Key {
	t.Helper()
	raw := make([]byte, secret.KeySize)
	k, err := secret.Parse(base64.StdEncoding.EncodeToString(raw))
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func poller(s Store, g Git, l Lister, k secret.Key) *Poller {
	return New(Deps{Store: s, Git: g, Lister: l, Key: k, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
}

func job(key, slug, url, branch, head string, src store.ProjectSource) store.SyncJob {
	src.ProjectKey, src.ProjectID = key, idOf(key)
	return store.SyncJob{Source: src, Repo: store.SourceRepo{ProjectID: idOf(key), RepoID: idOf(key + "/" + slug), ProjectKey: key, Slug: slug,
		CloneURL: url, Branch: branch, HeadCommit: head, State: store.RepoPending}}
}

func TestCycle_ClonesResolvesFetchesMarksReady(t *testing.T) {
	s, g, l, k := newFakeStore(), newFakeGit(), &fakeLister{}, testKey(t)
	sealed, _ := k.Seal("tok")
	src := store.ProjectSource{Kind: store.SourceStatic, TokenAuth: store.TokenBasic, TokenUser: "me", TokenEnc: sealed}
	s.jobs = []store.SyncJob{job("tools", "cli", "https://x/cli.git", "", "", src)}
	g.heads[dirOf("tools", "cli")+"@main"] = "abc"

	if err := poller(s, g, l, k).CycleOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.states["cli"] != store.RepoReady || s.branch["cli"] != "main" || s.heads["cli"] != "abc" {
		t.Fatalf("row after cycle: state=%s branch=%s head=%s", s.states["cli"], s.branch["cli"], s.heads["cli"])
	}
	want := "clone " + dirOf("tools", "cli") + ",default-branch,fetch " + dirOf("tools", "cli")
	if got := strings.Join(g.calls, ","); got != want {
		t.Fatalf("git calls = %s, want %s", got, want)
	}
	if len(g.access) != 1 || g.access[0].Token != "tok" || g.access[0].Auth != store.TokenBasic || g.access[0].User != "me" {
		t.Fatalf("access handed to git = %+v", g.access)
	}
}

// TRANSITIONAL(ids): a clone from before the ids, in a folder named by its
// names, is moved to its id folder and fetched there, not cloned again.
// Remove with MoveLegacy.
func TestCycle_MovesALegacyClone(t *testing.T) {
	s, g, l, k := newFakeStore(), newFakeGit(), &fakeLister{}, testKey(t)
	src := store.ProjectSource{Kind: store.SourceStatic, TokenAuth: store.TokenBasic}
	s.jobs = []store.SyncJob{job("tools", "cli", "https://x/cli.git", "main", "abc", src)}
	g.legacy["/base/tools/cli"] = "https://x/cli.git"
	g.heads[dirOf("tools", "cli")+"@main"] = "abc"
	if err := poller(s, g, l, k).CycleOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(g.moved) != 1 || g.moved[0] != "/base/tools/cli → "+dirOf("tools", "cli") {
		t.Fatalf("moved = %v", g.moved)
	}
	if got := strings.Join(g.calls, ","); got != "fetch "+dirOf("tools", "cli") {
		t.Fatalf("git calls = %s, want the fetch only", got)
	}
}

// TRANSITIONAL(ids): a removal also takes the clone a sync never moved out
// of its folder named by names, never an id folder. Remove with
// RemoveLegacy.
func TestRemoveLegacy(t *testing.T) {
	g := newFakeGit()
	p := poller(newFakeStore(), g, &fakeLister{}, testKey(t))
	for _, err := range []error{p.RemoveLegacy("tools", "cli"), p.RemoveLegacyProject("tools"), p.RemoveLegacy("p3", "r4"), p.RemoveLegacyProject("p3")} {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := strings.Join(g.calls, ","); got != "remove /base/tools/cli,remove-project /base/tools" {
		t.Fatalf("git calls = %s", got)
	}
}

// A listed slug another repository had is left out by the store; the
// source's error says which, so the source page shows it.
func TestList_ReportsNameConflicts(t *testing.T) {
	s, g, k := newFakeStore(), newFakeGit(), testKey(t)
	s.conflicts = []string{"ui"}
	l := &fakeLister{repos: []store.ListedRepo{{Slug: "ui", CloneURL: "https://x/ui.git", ForgeID: 12}}}
	s.sources = []store.SourceRow{{ProjectSource: store.ProjectSource{ProjectKey: "SHOP", Kind: store.SourceBitbucket, Host: "https://x"}}}
	if err := poller(s, g, l, k).CycleOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.listErr["SHOP"], "ui") {
		t.Fatalf("listing error = %q, want the conflicting slug named", s.listErr["SHOP"])
	}
}

func TestCycle_ReclonesOnOriginMismatch_AndRecordsFailures(t *testing.T) {
	s, g, l, k := newFakeStore(), newFakeGit(), &fakeLister{}, testKey(t)
	src := store.ProjectSource{Kind: store.SourceStatic, TokenAuth: store.TokenBasic}
	s.jobs = []store.SyncJob{
		job("tools", "moved", "https://x/new.git", "main", "old", src),
		job("tools", "broken", "https://x/broken.git", "main", "", src),
		job("tools", "gone", "https://x/gone.git", "develop", "", src),
		job("tools", "fine", "https://x/fine.git", "main", "abc", src),
	}
	g.origins[dirOf("tools", "moved")] = "https://x/old.git"
	g.heads[dirOf("tools", "moved")+"@main"] = "new"
	g.fetchErr[dirOf("tools", "broken")] = errors.New("git fetch: exit status 128: fatal: unable to access")
	g.origins[dirOf("tools", "gone")] = "https://x/gone.git"
	g.origins[dirOf("tools", "fine")] = "https://x/fine.git"
	g.heads[dirOf("tools", "fine")+"@main"] = "abc"

	if err := poller(s, g, l, k).CycleOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	calls := strings.Join(g.calls, ",")
	if !strings.Contains(calls, "remove "+dirOf("tools", "moved")+",clone "+dirOf("tools", "moved")) {
		t.Fatalf("mismatch did not re-clone: %s", calls)
	}
	if s.states["moved"] != store.RepoReady || s.heads["moved"] != "new" {
		t.Fatalf("moved: %s %s", s.states["moved"], s.heads["moved"])
	}
	if s.states["broken"] != store.RepoFailed || !strings.Contains(s.errs["broken"], "unable to access") {
		t.Fatalf("broken: %s %q", s.states["broken"], s.errs["broken"])
	}
	if s.states["gone"] != store.RepoFailed || !strings.Contains(s.errs["gone"], "not found on the remote") {
		t.Fatalf("gone: %s %q", s.states["gone"], s.errs["gone"])
	}
	// The cycle went on past the failures.
	if s.states["fine"] != store.RepoReady {
		t.Fatalf("fine after failures: %s", s.states["fine"])
	}
}

func TestCycle_ListsBitbucketSources(t *testing.T) {
	s, g, k := newFakeStore(), newFakeGit(), testKey(t)
	sealed, _ := k.Seal("pat")
	s.sources = []store.SourceRow{
		{ProjectSource: store.ProjectSource{ProjectKey: "SHOP", Kind: store.SourceBitbucket, Host: "https://forge.example.invalid", TokenAuth: store.TokenBearer, TokenEnc: sealed}},
		{ProjectSource: store.ProjectSource{ProjectKey: "tools", Kind: store.SourceStatic, TokenAuth: store.TokenBasic}},
	}
	l := &fakeLister{repos: []store.ListedRepo{{Slug: "ui", CloneURL: "https://forge.example.invalid/scm/shop/ui.git"}}}
	if err := poller(s, g, l, k).CycleOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(l.seen) != 1 || l.seen[0].Token != "pat" || l.seen[0].Auth != store.TokenBearer {
		t.Fatalf("lister saw %+v, want one bearer call for SHOP only", l.seen)
	}
	if len(s.listed["SHOP"]) != 1 || s.listErr["SHOP"] != "" {
		t.Fatalf("listing not applied: %+v err=%q", s.listed, s.listErr["SHOP"])
	}
	if _, ok := s.listed["tools"]; ok {
		t.Fatal("static source was listed")
	}

	l.err = errors.New("list repositories: GET /rest/api/1.0/projects/SHOP/repos: 401")
	if err := poller(s, g, l, k).CycleOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.listErr["SHOP"], "401") {
		t.Fatalf("listing failure not recorded: %q", s.listErr["SHOP"])
	}
}

func TestCycle_TokenWithoutKeyFailsTheRepo(t *testing.T) {
	s, g, l := newFakeStore(), newFakeGit(), &fakeLister{}
	src := store.ProjectSource{Kind: store.SourceStatic, TokenAuth: store.TokenBasic, TokenEnc: []byte("sealed-elsewhere")}
	s.jobs = []store.SyncJob{job("tools", "cli", "https://x/cli.git", "main", "", src)}
	if err := poller(s, g, l, secret.Key{}).CycleOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.states["cli"] != store.RepoFailed || !strings.Contains(s.errs["cli"], "BACKEND_SOURCE_TOKEN_KEY") {
		t.Fatalf("cli: %s %q", s.states["cli"], s.errs["cli"])
	}
	if len(g.calls) != 0 {
		t.Fatalf("git was called without a usable token: %v", g.calls)
	}
}

func TestRemove_SkipsTheRowInTheRunningCycle(t *testing.T) {
	s, g, l, k := newFakeStore(), newFakeGit(), &fakeLister{}, testKey(t)
	src := store.ProjectSource{Kind: store.SourceStatic, TokenAuth: store.TokenBasic}
	s.jobs = []store.SyncJob{job("tools", "a", "https://x/a.git", "main", "", src), job("tools", "b", "https://x/b.git", "main", "", src)}
	g.heads[dirOf("tools", "a")+"@main"] = "aaa"
	g.heads[dirOf("tools", "b")+"@main"] = "bbb"
	p := poller(s, g, l, k)
	// b is removed while the cycle works on a (inside the disk lock, as a
	// real Remove would be once it got the lock): the cycle, which listed
	// b before, must skip it rather than clone it back.
	g.onClone = func(dir string) {
		if dir == dirOf("tools", "a") {
			p.removed[projectSeg(idOf("tools"))+"/"+repoSeg(idOf("tools/b"))] = true
		}
	}
	if err := p.CycleOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.states["a"] != store.RepoReady {
		t.Fatalf("a: %s", s.states["a"])
	}
	if _, touched := s.states["b"]; touched || strings.Contains(strings.Join(g.calls, ","), dirOf("tools", "b")) {
		t.Fatalf("b was synced after its removal: states=%v calls=%v", s.states, g.calls)
	}
	// Remove during a sync waits for the disk lock.
	done := make(chan struct{})
	p.disk.Lock()
	go func() {
		_ = p.Remove(idOf("tools"), idOf("tools/a"))
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("Remove did not wait for the disk lock")
	case <-time.After(50 * time.Millisecond):
	}
	p.disk.Unlock()
	<-done
	if !strings.Contains(strings.Join(g.calls, ","), "remove "+dirOf("tools", "a")) {
		t.Fatalf("remove not forwarded: %v", g.calls)
	}
}

func TestStartNudgeStop(t *testing.T) {
	s, g, l, k := newFakeStore(), newFakeGit(), &fakeLister{}, testKey(t)
	src := store.ProjectSource{Kind: store.SourceStatic, TokenAuth: store.TokenBasic}
	s.jobs = []store.SyncJob{job("tools", "cli", "https://x/cli.git", "main", "", src)}
	g.heads[dirOf("tools", "cli")+"@main"] = "abc"
	p := New(Deps{Store: s, Git: g, Lister: l, Key: k, FirstDelay: time.Hour, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	p.Start(context.Background())
	p.Nudge()
	deadline := time.Now().Add(2 * time.Second)
	for s.state("cli") != store.RepoReady && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	p.Stop()
	if s.state("cli") != store.RepoReady {
		t.Fatalf("nudge did not run a cycle: %s", s.states["cli"])
	}
	p.Stop() // idempotent
}
