package clone

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/trick77/almanach/internal/gitrepo"
	"github.com/trick77/almanach/internal/manifest"
	"github.com/trick77/almanach/internal/store"
)

type fakeManifests struct {
	mu      sync.Mutex
	commit  map[int64]string
	saved   map[int64]manifest.Read
	saves   int
	failAsk error
}

func (f *fakeManifests) ManifestCommit(_ context.Context, rid int64) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failAsk != nil {
		return "", false, f.failAsk
	}
	c, ok := f.commit[rid]
	return c, ok, nil
}

func (f *fakeManifests) SaveManifests(_ context.Context, rid int64, commit string, read manifest.Read) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saves++
	f.commit[rid], f.saved[rid] = commit, read
	return nil
}

// After a sync the clone's manifests are read at its head, the root's and
// one level down, never deeper; once per head, not every cycle; and not
// after a failed sync.
func TestCycle_ReadsTheManifests(t *testing.T) {
	gitBin, err := gitrepo.Resolve()
	if err != nil {
		t.Skip(err)
	}
	root := t.TempDir()
	work := filepath.Join(t.TempDir(), "work")
	run(t, filepath.Dir(work), "init", "-q", "-b", "main", work)
	for p, body := range map[string]string{
		"go.mod":                   "module example.invalid/loom\n\nrequire example.invalid/wire v0.1.8\n",
		"web/package.json":         `{"name": "loom-ui", "dependencies": {"left-pad": "^1.3.0"}}`,
		"corpus/checkout/a/go.mod": "module example.invalid/copied\n",
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(work, p)), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(work, p), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	run(t, work, "add", ".")
	run(t, work, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-q", "-m", "c")
	head := run(t, work, "rev-parse", "HEAD")
	dir := filepath.Join(root, projectSeg(idOf("tools")), repoSeg(idOf("tools/loom")))
	run(t, root, "clone", "-q", "--bare", work, dir)

	client, err := gitrepo.New(gitBin, root, "")
	if err != nil {
		t.Fatal(err)
	}
	s, g := newFakeStore(), newFakeGit()
	s.jobs = []store.SyncJob{job("tools", "loom", "https://x/loom.git", "main", "", store.ProjectSource{Kind: store.SourceStatic})}
	g.origins[dir] = "https://x/loom.git"
	g.heads[dir+"@main"] = head
	m := &fakeManifests{commit: map[int64]string{}, saved: map[int64]manifest.Read{}}
	p := New(Deps{Store: s, Git: pathGit{g, root}, Lister: &fakeLister{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Manifests: Manifests{Store: m, Reads: client}})
	ctx := context.Background()
	if err := p.CycleOnce(ctx); err != nil {
		t.Fatal(err)
	}
	want := manifest.Read{
		Provides: []string{"go:example.invalid/loom", "npm:loom-ui"},
		Requires: []manifest.Dep{{Coord: "go:example.invalid/wire", Version: "v0.1.8"}, {Coord: "npm:left-pad", Version: "^1.3.0"}},
	}
	rid := idOf("tools/loom")
	if got := m.saved[rid]; !reflect.DeepEqual(got, want) || m.commit[rid] != head {
		t.Fatalf("saved %+v at %q, want %+v at the head", got, m.commit[rid], want)
	}
	if err := p.CycleOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if m.saves != 1 {
		t.Fatalf("saves = %d, want one read per head", m.saves)
	}

	// A store that cannot say when they were read reads nothing; the sync
	// itself still counts.
	m.failAsk = errors.New("database is locked")
	delete(m.commit, rid)
	if err := p.CycleOnce(ctx); err != nil || m.saves != 1 || s.states["loom"] != store.RepoReady {
		t.Fatalf("with the store failing: err %v, saves %d, state %s", err, m.saves, s.states["loom"])
	}

	// A failed sync reads nothing.
	m.failAsk = nil
	g.fetchErr[dir] = errors.New("unreachable")
	if err := p.CycleOnce(ctx); err != nil || m.saves != 1 {
		t.Fatalf("after a failed sync: err %v, saves %d", err, m.saves)
	}
}
