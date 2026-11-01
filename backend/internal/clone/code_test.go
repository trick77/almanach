package clone

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/trick77/almanach/internal/baseline"
	"github.com/trick77/almanach/internal/consolidate"
	"github.com/trick77/almanach/internal/gitrepo"
	"github.com/trick77/almanach/internal/store"
)

// After a sync the poller queues the runs that are due: the first on a
// clone's first ready, a reconcile once the interval passed and the head
// left the documented commit; nothing with auto_describe off or the
// baseline off, and an open run is no failure.
func TestDescribe_QueuesWhatIsDue(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	month := 30 * 24 * time.Hour
	long, recent := now.Add(-month-time.Hour), now.Add(-time.Hour)
	on := store.ProjectSource{Kind: store.SourceStatic, TokenAuth: store.TokenBasic, AutoDescribe: true}
	off := on
	off.AutoDescribe = false
	for name, c := range map[string]struct {
		src        store.ProjectSource
		before     string // the row's head before the sync
		head       string
		documented string
		finished   *time.Time
		reconcile  time.Duration
		baseline   bool
		want       string
	}{
		"first ready":         {on, "", "h1", "", nil, month, true, "tools cli full first_ready  h1"},
		"first ready, off":    {off, "", "h1", "", nil, month, true, ""},
		"baseline off":        {on, "", "h1", "", nil, month, false, ""},
		"due":                 {on, "h0", "h1", "d0", &long, month, true, "tools cli reconcile monthly d0 h1"},
		"not yet":             {on, "h0", "h1", "d0", &recent, month, true, ""},
		"head is documented":  {on, "h0", "d0", "d0", &long, month, true, ""},
		"never described":     {on, "h0", "h1", "", nil, month, true, ""},
		"buttons only":        {on, "h0", "h1", "d0", &long, 0, true, ""},
		"last run still open": {on, "h0", "h1", "d0", nil, month, true, ""},
	} {
		t.Run(name, func(t *testing.T) {
			s, g := newFakeStore(), newFakeGit()
			s.jobs = []store.SyncJob{job("tools", "cli", "https://x/cli.git", "main", c.before, c.src)}
			g.origins[dirOf("tools", "cli")] = "https://x/cli.git"
			g.heads[dirOf("tools", "cli")+"@main"] = c.head
			if c.documented != "" {
				// The last run is the documented one, or one still open.
				s.last = store.BaselineRun{ID: 2, Mode: store.RunFull, Commit: c.documented, State: store.RunDone, FinishedAt: c.finished}
				if c.finished == nil {
					s.last.State = store.RunDescribing
				}
			}
			wakes := 0
			d := Describe{Reconcile: c.reconcile}
			if c.baseline {
				d.Wake = func() { wakes++ }
			}
			p := New(Deps{Store: s, Git: g, Lister: &fakeLister{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Describe: d,
				Now: func() time.Time { return now }})
			if err := p.CycleOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			got := strings.Join(s.enqueued, "|")
			if got != c.want || (c.want != "") != (wakes == 1) {
				t.Fatalf("queued %q, wakes %d; want %q", got, wakes, c.want)
			}
			// A done last run is the documented one: one query a cycle.
			if s.docAsked != 0 {
				t.Errorf("DocumentedRun asked %d times after a done run", s.docAsked)
			}
		})
	}
}

// A failed run is queued again in its own mode a day after it ended (or
// after the interval, when that is shorter), three times in a row, whatever
// the head and even with reconciles off; past that the repository is back
// on the reconcile interval. A fresh failure and a repository never
// described (cloned before the baseline) wait.
func TestDescribe_RetriesAFailedRun(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	month := 30 * 24 * time.Hour
	failedAt := func(mode string, ago time.Duration) store.BaselineRun {
		return store.BaselineRun{ID: 2, Mode: mode, State: store.RunFailed, FinishedAt: ptr(now.Add(-ago))}
	}
	for name, c := range map[string]struct {
		last       store.BaselineRun
		documented string
		failed     int
		reconcile  time.Duration
		want       string
	}{
		"failed a day ago":           {failedAt(store.RunFull, 25*time.Hour), "", 1, month, "tools cli full retry  h1"},
		"failed an hour ago":         {failedAt(store.RunFull, time.Hour), "", 1, month, ""},
		"third retry":                {failedAt(store.RunFull, 25*time.Hour), "", 3, month, "tools cli full retry  h1"},
		"past the retries":           {failedAt(store.RunFull, 25*time.Hour), "", 4, month, ""},
		"past the retries, a month":  {failedAt(store.RunFull, month+time.Hour), "", 4, month, "tools cli full monthly  h1"},
		"past the retries, buttons":  {failedAt(store.RunFull, month+time.Hour), "", 4, 0, ""},
		"reconcile failed":           {failedAt(store.RunReconcile, 25*time.Hour), "d0", 1, month, "tools cli reconcile retry d0 h1"},
		"full read failed, doc'd":    {failedAt(store.RunFull, 25*time.Hour), "d0", 1, month, "tools cli full retry  h1"},
		"reconcile past the retries": {failedAt(store.RunReconcile, month+time.Hour), "d0", 4, month, "tools cli reconcile monthly d0 h1"},
		"shorter interval":           {failedAt(store.RunReconcile, 3*time.Hour), "d0", 1, 2 * time.Hour, "tools cli reconcile retry d0 h1"},
		"retry inside the interval":  {failedAt(store.RunReconcile, time.Hour), "d0", 1, 2 * time.Hour, ""},
		"full read failed at head":   {failedAt(store.RunFull, 25*time.Hour), "h1", 1, month, "tools cli full retry  h1"},
		"reconcile failed, off":      {failedAt(store.RunReconcile, 25*time.Hour), "d0", 1, 0, "tools cli reconcile retry d0 h1"},
		"reconcile failed, no doc":   {failedAt(store.RunReconcile, 25*time.Hour), "", 1, month, "tools cli full retry  h1"},
		"never ran":                  {store.BaselineRun{}, "", 0, month, ""},
	} {
		t.Run(name, func(t *testing.T) {
			s, g := newFakeStore(), newFakeGit()
			s.last, s.failed = c.last, c.failed
			if c.documented != "" {
				s.documented = store.BaselineRun{ID: 1, Commit: c.documented, State: store.RunDone}
			}
			s.jobs = []store.SyncJob{job("tools", "cli", "https://x/cli.git", "main", "h0", store.ProjectSource{Kind: store.SourceStatic, AutoDescribe: true})}
			g.origins[dirOf("tools", "cli")] = "https://x/cli.git"
			g.heads[dirOf("tools", "cli")+"@main"] = "h1"
			p := New(Deps{Store: s, Git: g, Lister: &fakeLister{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
				Describe: Describe{Reconcile: c.reconcile, Wake: func() {}}, Now: func() time.Time { return now }})
			if err := p.CycleOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(s.enqueued, "|"); got != c.want {
				t.Fatalf("queued %q, want %q", got, c.want)
			}
		})
	}
}

func ptr(t time.Time) *time.Time { return &t }

// While the poller holds the disk, a read waits briefly and then answers
// busy rather than standing behind a long clone.
func TestCode_BusyWhileThePollerHoldsTheDisk(t *testing.T) {
	old := lockWait
	lockWait = 20 * time.Millisecond
	t.Cleanup(func() { lockWait = old })
	p := New(Deps{Store: newFakeStore(), Git: newFakeGit(), Lister: &fakeLister{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	code := NewCode(p, nil)
	p.disk.Lock()
	_, err := code.Tree(context.Background(), "tools", "cli", strings.Repeat("a", 40))
	if !errors.Is(err, consolidate.ErrCloneBusy) {
		t.Fatalf("Tree while the disk is held: err = %v, want ErrCloneBusy", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	lockWait = time.Hour
	if _, err := code.Tree(ctx, "tools", "cli", strings.Repeat("a", 40)); !errors.Is(err, context.Canceled) {
		t.Fatalf("Tree on a cancelled step: err = %v", err)
	}
	p.disk.Unlock()
}

func TestDescribe_OpenRunAndStoreFailures(t *testing.T) {
	for _, err := range []error{store.ErrExists, errors.New("db down")} {
		s, g := newFakeStore(), newFakeGit()
		s.enqueueErr = err
		s.jobs = []store.SyncJob{job("tools", "cli", "https://x/cli.git", "main", "", store.ProjectSource{Kind: store.SourceStatic, AutoDescribe: true})}
		g.heads[dirOf("tools", "cli")+"@main"] = "h1"
		wakes := 0
		p := New(Deps{Store: s, Git: g, Lister: &fakeLister{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
			Describe: Describe{Reconcile: time.Hour, Wake: func() { wakes++ }}})
		if cerr := p.CycleOnce(context.Background()); cerr != nil || wakes != 0 || s.state("cli") != store.RepoReady {
			t.Fatalf("enqueue %v: cycle err %v, wakes %d, state %s; want the sync unaffected", err, cerr, wakes, s.state("cli"))
		}
	}
	s, g := newFakeStore(), newFakeGit()
	s.runErr = errors.New("db down")
	s.jobs = []store.SyncJob{job("tools", "cli", "https://x/cli.git", "main", "h0", store.ProjectSource{Kind: store.SourceStatic, AutoDescribe: true})}
	g.origins[dirOf("tools", "cli")] = "https://x/cli.git"
	g.heads[dirOf("tools", "cli")+"@main"] = "h1"
	p := New(Deps{Store: s, Git: g, Lister: &fakeLister{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Describe: Describe{Reconcile: time.Hour, Wake: func() {}}})
	if err := p.CycleOnce(context.Background()); err != nil || len(s.enqueued) != 0 {
		t.Fatalf("run check failed: err %v, queued %v", err, s.enqueued)
	}
}

// pathGit is a fakeGit whose clones live in real folders, for the reads.
type pathGit struct {
	*fakeGit
	root string
}

func (g pathGit) Dir(key, slug string) (string, error) { return filepath.Join(g.root, key, slug), nil }

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// The clones are read through gitrepo under the poller's lock: the tree,
// a diff, ancestry, a commit's time, the files (a binary or a missing one
// left out).
func TestCode_ReadsAClone(t *testing.T) {
	gitBin, err := gitrepo.Resolve()
	if err != nil {
		t.Skip(err)
	}
	root := t.TempDir()
	work := filepath.Join(t.TempDir(), "work")
	run(t, filepath.Dir(work), "init", "-q", "-b", "main", work)
	write := func(p, body string) {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(work, p)), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(work, p), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	commit := func() string {
		run(t, work, "add", ".")
		run(t, work, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-q", "-m", "c")
		return run(t, work, "rev-parse", "HEAD")
	}
	write("main.go", "package main\n")
	write("blob.bin", "a\x00b")
	first := commit()
	write("main.go", "package main // two\n")
	second := commit()
	// Where the poller keeps tools/cli: its folder named by ids.
	run(t, root, "clone", "-q", "--bare", work, filepath.Join(root, projectSeg(idOf("tools")), repoSeg(idOf("tools/cli"))))

	client, err := gitrepo.New(gitBin, root, "")
	if err != nil {
		t.Fatal(err)
	}
	p := New(Deps{Store: newFakeStore(), Git: pathGit{newFakeGit(), root}, Lister: &fakeLister{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	code := NewCode(p, client)
	ctx := context.Background()

	tree, err := code.Tree(ctx, "tools", "cli", second)
	if err != nil || len(tree) != 2 {
		t.Fatalf("Tree = %+v %v", tree, err)
	}
	diff, err := code.Diff(ctx, "tools", "cli", first, second)
	if err != nil || len(diff) != 1 || diff[0].Path != "main.go" {
		t.Fatalf("Diff = %+v %v", diff, err)
	}
	if ok, err := code.Contains(ctx, "tools", "cli", first, second); err != nil || !ok {
		t.Errorf("Contains(first, second) = %v %v", ok, err)
	}
	if ok, err := code.Contains(ctx, "tools", "cli", strings.Repeat("0", 40), second); err != nil || ok {
		t.Errorf("Contains(missing) = %v %v", ok, err)
	}
	if at, err := code.CommitTime(ctx, "tools", "cli", second); err != nil || at.IsZero() {
		t.Errorf("CommitTime = %v %v", at, err)
	}
	bodies, err := code.ReadFiles(ctx, "tools", "cli", second, []baseline.File{{Path: "main.go"}, {Path: "blob.bin"}, {Path: "gone.go"}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(bodies, map[string][]byte{"main.go": []byte("package main // two\n")}) {
		t.Fatalf("ReadFiles = %q", bodies)
	}
	if none, err := code.ReadFiles(ctx, "tools", "cli", second, nil); err != nil || len(none) != 0 {
		t.Errorf("ReadFiles of nothing = %v %v", none, err)
	}
	if _, err := code.ReadFiles(ctx, "tools", "cli", "HEAD", []baseline.File{{Path: "main.go"}}); !errors.Is(err, gitrepo.ErrBadSHA) {
		t.Errorf("ReadFiles at a ref: err = %v, want ErrBadSHA", err)
	}
	if _, err := code.Tree(ctx, "tools", "nope", second); err == nil {
		t.Error("Tree of a clone that is not there succeeded")
	}
}
