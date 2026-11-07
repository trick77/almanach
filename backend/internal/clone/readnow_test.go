package clone

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/trick77/almanach/internal/gitrepo"
	"github.com/trick77/almanach/internal/manifest"
)

// A clone laid down by the caller, not synced by the poller, gets its
// manifests read and stored on demand; a second ask at the same head
// reads nothing.
func TestReadManifestsNow(t *testing.T) {
	gitBin, err := gitrepo.Resolve()
	if err != nil {
		t.Skip(err)
	}
	root := t.TempDir()
	work := filepath.Join(t.TempDir(), "work")
	run(t, filepath.Dir(work), "init", "-q", "-b", "main", work)
	if err := os.WriteFile(filepath.Join(work, "go.mod"), []byte("module example.invalid/kit\n\nrequire example.invalid/wire v0.1.8\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run(t, work, "add", ".")
	run(t, work, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-q", "-m", "c")
	head := run(t, work, "rev-parse", "HEAD")
	dir := filepath.Join(root, projectSeg(idOf("tools")), repoSeg(idOf("tools/kit")))
	run(t, root, "clone", "-q", "--bare", work, dir)
	client, err := gitrepo.New(gitBin, root, "")
	if err != nil {
		t.Fatal(err)
	}
	m := &fakeManifests{commit: map[int64]string{}, saved: map[int64]manifest.Read{}}
	p := New(Deps{Store: newFakeStore(), Git: pathGit{newFakeGit(), root}, Lister: &fakeLister{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Manifests: Manifests{Store: m, Reads: client}})
	ctx := context.Background()
	if err := p.ReadManifestsNow(ctx, "tools", "kit", head); err != nil {
		t.Fatal(err)
	}
	got := m.saved[idOf("tools/kit")]
	if len(got.Provides) != 1 || got.Provides[0] != "go:example.invalid/kit" || len(got.Requires) != 1 {
		t.Fatalf("saved = %+v", got)
	}
	// Read already at this head: nothing new, said so.
	if err := p.ReadManifestsNow(ctx, "tools", "kit", head); err == nil {
		t.Error("a second read at the same head: want no read")
	}
	if m.saves != 1 {
		t.Errorf("saves = %d, want 1", m.saves)
	}
}
