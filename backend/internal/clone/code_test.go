package clone

import (
	"context"
	"encoding/json"
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
	"github.com/trick77/almanach/internal/diagram"
	"github.com/trick77/almanach/internal/gitrepo"
	"github.com/trick77/almanach/internal/store"
)

// After a sync the poller queues the runs that are due: the first on a
// clone's first ready, a reconcile once the interval passed and the head
// left the documented commit; nothing with the baseline off, and an open
// run is no failure.
func TestDescribe_QueuesWhatIsDue(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	month := 30 * 24 * time.Hour
	long, recent := now.Add(-month-time.Hour), now.Add(-time.Hour)
	src := store.ProjectSource{Kind: store.SourceStatic, TokenAuth: store.TokenBasic}
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
		"first ready":         {src, "", "h1", "", nil, month, true, "tools cli full first_ready  h1"},
		"baseline off":        {src, "", "h1", "", nil, month, false, ""},
		"due":                 {src, "h0", "h1", "d0", &long, month, true, "tools cli reconcile monthly d0 h1"},
		"not yet":             {src, "h0", "h1", "d0", &recent, month, true, ""},
		"head is documented":  {src, "h0", "d0", "d0", &long, month, true, ""},
		"never described":     {src, "h0", "h1", "", nil, month, true, ""},
		"buttons only":        {src, "h0", "h1", "d0", &long, 0, true, ""},
		"last run still open": {src, "h0", "h1", "d0", nil, month, true, ""},
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
			s.jobs = []store.SyncJob{job("tools", "cli", "https://x/cli.git", "main", "h0", store.ProjectSource{Kind: store.SourceStatic})}
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

// A done run at the head that skipped parts (a subject gone aside), or left
// the repository page without a map drawn from its text (described before
// maps existed), is retried a day after it ended: a reconcile at the same
// commit, while fewer than three retries ran in a row, a retry of a failed
// run included. A head that moved waits for the interval; with the
// interval off (buttons only) nothing is queued.
func TestDescribe_RetriesWhatADoneRunLeft(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	month := 30 * 24 * time.Hour
	described := store.Page{Scope: store.ScopeRepository, Summary: "Ein Dienst."}
	drawn := described
	drawn.RepoMap = &diagram.Diagram{Kind: diagram.KindFlow}
	drawn.RepoMapBasis = drawn.MapBasis()
	stale := drawn
	stale.Summary = "Ein Dienst, neu beschrieben."
	failed := json.RawMessage(`{"format":2,"skipped":["A"],"why":{"A":"failed"}}`)
	gone := json.RawMessage(`{"format":2,"skipped":["A"],"why":{"A":"gone"}}`)
	noPage := json.RawMessage(`{"format":2,"steps":{"project":"no_page"}}`)
	for name, c := range map[string]struct {
		ago       time.Duration
		skipped   int
		plan      json.RawMessage
		retried   int
		page      store.Page
		head      string
		reconcile time.Duration
		want      string
	}{
		"skipped parts":              {25 * time.Hour, 1, failed, 0, drawn, "d0", month, "tools cli reconcile retry d0 d0"},
		"a plan from before reasons": {25 * time.Hour, 2, nil, 0, drawn, "d0", month, "tools cli reconcile retry d0 d0"},
		"no map yet":                 {25 * time.Hour, 0, nil, 0, described, "d0", month, "tools cli reconcile retry d0 d0"},
		"map from older text":        {25 * time.Hour, 0, nil, 0, stale, "d0", month, "tools cli reconcile retry d0 d0"},
		"after a failed run's retry": {25 * time.Hour, 1, failed, 1, drawn, "d0", month, "tools cli reconcile retry d0 d0"},
		"third retry":                {25 * time.Hour, 1, failed, 2, drawn, "d0", month, "tools cli reconcile retry d0 d0"},
		"past the retries":           {25 * time.Hour, 1, failed, 3, drawn, "d0", month, ""},
		"only a subject gone":        {25 * time.Hour, 1, gone, 0, drawn, "d0", month, ""},
		"only a project left as is":  {25 * time.Hour, 1, noPage, 0, drawn, "d0", month, ""},
		"head moved":                 {25 * time.Hour, 1, failed, 0, described, "h1", month, ""},
		"buttons only":               {25 * time.Hour, 1, failed, 0, described, "d0", 0, ""},
		"an hour ago":                {time.Hour, 1, failed, 0, described, "d0", month, ""},
		"finished, map stands":       {25 * time.Hour, 0, nil, 0, drawn, "d0", month, ""},
		"no page, nothing skipped":   {25 * time.Hour, 0, nil, 0, store.Page{}, "d0", month, ""},
	} {
		t.Run(name, func(t *testing.T) {
			s, g := newFakeStore(), newFakeGit()
			s.last = store.BaselineRun{ID: 2, Mode: store.RunFull, Trigger: store.TriggerManual, Commit: "d0", State: store.RunDone,
				Skipped: c.skipped, Plan: c.plan, FinishedAt: ptr(now.Add(-c.ago))}
			s.retried, s.page = c.retried, c.page
			s.jobs = []store.SyncJob{job("tools", "cli", "https://x/cli.git", "main", "h0", store.ProjectSource{Kind: store.SourceStatic})}
			g.origins[dirOf("tools", "cli")] = "https://x/cli.git"
			g.heads[dirOf("tools", "cli")+"@main"] = c.head
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

// A done run's page is read once, not every cycle: the verdict is kept for
// that run and asked again only of a newer one.
func TestDescribe_ReadsTheMapOncePerRun(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	drawn := store.Page{Scope: store.ScopeRepository, Summary: "Ein Dienst.", RepoMap: &diagram.Diagram{Kind: diagram.KindFlow}}
	drawn.RepoMapBasis = drawn.MapBasis()
	s, g := newFakeStore(), newFakeGit()
	s.last = store.BaselineRun{ID: 2, Trigger: store.TriggerManual, Commit: "d0", State: store.RunDone, FinishedAt: ptr(now.Add(-48 * time.Hour))}
	s.page = drawn
	s.jobs = []store.SyncJob{job("tools", "cli", "https://x/cli.git", "main", "h0", store.ProjectSource{Kind: store.SourceStatic})}
	g.origins[dirOf("tools", "cli")] = "https://x/cli.git"
	g.heads[dirOf("tools", "cli")+"@main"] = "d0"
	p := New(Deps{Store: s, Git: g, Lister: &fakeLister{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Describe: Describe{Reconcile: 30 * 24 * time.Hour, Wake: func() {}}, Now: func() time.Time { return now }})
	for range 3 {
		if err := p.CycleOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	s.last.ID = 3
	if err := p.CycleOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.pageAsked != 2 || len(s.enqueued) != 0 {
		t.Fatalf("page read %d times over two runs, queued %v; want twice, nothing", s.pageAsked, s.enqueued)
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
		s.jobs = []store.SyncJob{job("tools", "cli", "https://x/cli.git", "main", "", store.ProjectSource{Kind: store.SourceStatic})}
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
	s.jobs = []store.SyncJob{job("tools", "cli", "https://x/cli.git", "main", "h0", store.ProjectSource{Kind: store.SourceStatic})}
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
	// Credentials, all invented: a config value, a Secret manifest, a token
	// committed into source.
	write("application.yaml", "db:\n  host: db\n  password: s3cr3t-invented\n")
	write("secret.yaml", "apiVersion: v1\nkind: Secret\ndata:\n  x: eQ==\n")
	write("leak.go", "package main\n\nconst t = \"ghp_A1b2C3d4E5f6G7h8I9j0K1l2M3n4O5p6Q7r8\"\n") // ship:allow-secret: invented fixture
	third := commit()
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
	secrets, err := code.ReadFiles(ctx, "tools", "cli", third, []baseline.File{{Path: "application.yaml"}, {Path: "secret.yaml"}, {Path: "leak.go"}, {Path: "main.go"}})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]byte{
		"application.yaml": []byte("db:\n  host: db\n  password: <redacted>\n"),
		"leak.go":          []byte("package main\n\nconst t = \"<redacted>\"\n"),
		"main.go":          []byte("package main // two\n"),
	}
	if !reflect.DeepEqual(secrets, want) {
		t.Fatalf("ReadFiles with credentials = %q, want %q", secrets, want)
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
