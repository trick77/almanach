//go:build prompteval

package consolidate

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/trick77/almanach/internal/baseline"
	"github.com/trick77/almanach/internal/config"
	"github.com/trick77/almanach/internal/gitrepo"
	"github.com/trick77/almanach/internal/inference"
	"github.com/trick77/almanach/internal/store"
)

// The prompt eval: full baseline runs of real repositories through a real
// model, every repository of one project, then the pages dumped for a
// reader. Asserts are structural only, never prose. `make prompt-eval`; never in
// check or CI.

const evalProject = "EVAL"

// evalStore is the test store whose repositories' summaries are the pages
// the runs wrote, so each project step sees the ones before it.
type evalStore struct{ *runStore }

func (s evalStore) Repos(_ context.Context, project string, _ bool) ([]store.RepoRow, error) {
	var out []store.RepoRow
	for _, r := range s.repos {
		out = append(out, store.RepoRow{Slug: r.Slug, Summary: s.pages[pageKey(store.ScopeRepository, project, r.Slug)].Summary})
	}
	return out, nil
}

// evalCode reads bare clones without a poller: nothing removes them.
type evalCode struct{ git *gitrepo.Client }

func (c evalCode) Tree(ctx context.Context, key, slug, sha string) ([]gitrepo.Entry, error) {
	dir, err := c.git.Dir(key, slug)
	if err != nil {
		return nil, err
	}
	return c.git.ListEntries(ctx, dir, sha)
}

func (c evalCode) Diff(ctx context.Context, key, slug, from, to string) ([]gitrepo.Entry, error) {
	dir, err := c.git.Dir(key, slug)
	if err != nil {
		return nil, err
	}
	return c.git.ChangedEntries(ctx, dir, from, to)
}

func (c evalCode) Contains(ctx context.Context, key, slug, ancestor, sha string) (bool, error) {
	dir, err := c.git.Dir(key, slug)
	if err != nil {
		return false, err
	}
	if has, err := c.git.HasCommit(ctx, dir, ancestor); err != nil || !has {
		return false, err
	}
	return c.git.IsAncestor(ctx, dir, ancestor, sha)
}

func (c evalCode) CommitTime(ctx context.Context, key, slug, sha string) (time.Time, error) {
	dir, err := c.git.Dir(key, slug)
	if err != nil {
		return time.Time{}, err
	}
	return c.git.CommitTime(ctx, dir, sha)
}

func (c evalCode) ReadFiles(ctx context.Context, key, slug, sha string, files []baseline.File) (map[string][]byte, error) {
	out := map[string][]byte{}
	dir, err := c.git.Dir(key, slug)
	if err != nil || len(files) == 0 {
		return out, err
	}
	r, err := c.git.NewReader(ctx, dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	for _, f := range files {
		body, err := r.ReadFile(sha, f.Path, baseline.FileMaxBytes)
		if errors.Is(err, gitrepo.ErrReaderBroken) || errors.Is(err, gitrepo.ErrBadSHA) {
			return nil, err
		}
		if err == nil && !baseline.Binary(body) {
			out[f.Path] = body
		}
	}
	return out, nil
}

// TestPromptEval runs EVAL_REPOS (slug=url pairs) through EVAL_MODEL and writes
// the pages to EVAL_OUT.
func TestPromptEval(t *testing.T) {
	repos, model, out := os.Getenv("EVAL_REPOS"), os.Getenv("EVAL_MODEL"), os.Getenv("EVAL_OUT")
	if repos == "" || model == "" || out == "" {
		t.Skip("EVAL_REPOS, EVAL_MODEL and EVAL_OUT unset: make prompt-eval")
	}
	ctx := context.Background()
	gitBin, err := gitrepo.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	git, err := gitrepo.New(gitBin, root, "")
	if err != nil {
		t.Fatal(err)
	}
	st := evalStore{&runStore{fakeStore: newFakeStore(), sources: map[string]bool{}}}
	for _, pair := range strings.Split(repos, ",") {
		slug, url, ok := strings.Cut(pair, "=")
		if !ok {
			t.Fatalf("EVAL_REPOS entry %q: want slug=url", pair)
		}
		dir := filepath.Join(root, evalProject, slug)
		if b, err := exec.CommandContext(ctx, gitBin, "clone", "--bare", "--quiet", url, dir).CombinedOutput(); err != nil {
			t.Fatalf("clone %s: %v\n%s", url, err, b)
		}
		b, err := exec.CommandContext(ctx, gitBin, "-C", dir, "rev-parse", "HEAD").Output()
		if err != nil {
			t.Fatalf("head of %s: %v", slug, err)
		}
		st.repos = append(st.repos, store.SourceRepo{ProjectKey: evalProject, Slug: slug, HeadCommit: strings.TrimSpace(string(b))})
		st.sources[evalProject+"/"+slug] = true
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	llm, err := inference.New(inference.Options{Model: model, Logger: log, Lookup: os.LookupEnv})
	if err != nil {
		t.Fatal(err)
	}
	limits := config.Limits{RepoMaxStatements: 40, ProjectMaxStatements: 24, RepoMaxChars: 60000, ProjectMaxChars: 40000}
	w := New(st, &fakeMerger{}, limits, log)
	w.WithBaseline(Baseline{Store: st, Code: evalCode{git}, Catalogue: llm, Describer: llm,
		Limits: config.Baseline{MaxCalls: 16, MaxBytes: 2_000_000}})

	for _, repo := range st.repos {
		st.runs = append(st.runs, store.BaselineRun{ID: int64(len(st.runs) + 1), ProjectKey: evalProject, Slug: repo.Slug,
			Mode: store.RunFull, Trigger: store.TriggerManual, Commit: repo.HeadCommit, State: store.RunQueued})
		run := func() store.BaselineRun { return st.runs[len(st.runs)-1] }
		// A gateway hold ends a drain; the next one resumes after it.
		for lap := 0; run().Open() && lap < 40; lap++ {
			w.drain(ctx)
			if run().Open() && w.holding() {
				time.Sleep(30 * time.Second)
			}
		}
		r := run()
		if r.State != store.RunDone {
			t.Errorf("%s: run ended %s: %s", repo.Slug, r.State, r.Error)
		}
		tree, err := evalCode{git}.Tree(ctx, evalProject, repo.Slug, repo.HeadCommit)
		if err != nil {
			t.Fatal(err)
		}
		page := st.pages[pageKey(store.ScopeRepository, evalProject, repo.Slug)]
		checkPage(t, repo.Slug, page, baseline.Exists(tree))
		t.Logf("%s: %d calls, %d skipped, %d statements, %d chars", repo.Slug, r.Calls, r.Skipped, len(page.Statements), prose(page.Summary, page.Statements))
		dump(t, out, repo.Slug, page)
	}
	project := st.pages[pageKey(store.ScopeProject, evalProject, "")]
	if project.Summary == "" {
		t.Error("project: no summary")
	}
	for _, repo := range st.repos {
		if !strings.Contains(project.Summary, repo.Slug) {
			t.Errorf("project summary never names %s", repo.Slug)
		}
	}
	dump(t, out, "_project", project)
}

// checkPage holds a repository page to the structure a reader relies on:
// a summary, statements of a known kind, each with text and files in the
// tree.
func checkPage(t *testing.T, slug string, p store.Page, exists func(string) bool) {
	t.Helper()
	if p.Summary == "" || len(p.Statements) == 0 {
		t.Errorf("%s: summary %d chars, %d statements", slug, len(p.Summary), len(p.Statements))
	}
	for _, s := range p.Statements {
		switch {
		case s.Text == "" || s.Name == "":
			t.Errorf("%s %s: empty name or text", slug, s.ID)
		case len(s.Files) == 0:
			t.Errorf("%s %s %q: no files", slug, s.ID, s.Name)
		}
		for _, f := range s.Files {
			if !exists(f) {
				t.Errorf("%s %s: file %s not in the tree", slug, s.ID, f)
			}
		}
	}
}

func dump(t *testing.T, dir, name string, p store.Page) {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n%s\n", name, p.Summary)
	for _, s := range p.Statements {
		fmt.Fprintf(&b, "\n## [%s] %s: %s\n\n%s\n\nFiles: %s\n", s.ID, s.Kind, s.Name, s.Text, strings.Join(s.Files, ", "))
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}
