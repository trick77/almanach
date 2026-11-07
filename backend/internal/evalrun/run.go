package evalrun

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/trick77/almanach/internal/baseline"
	"github.com/trick77/almanach/internal/clone"
	"github.com/trick77/almanach/internal/config"
	"github.com/trick77/almanach/internal/consolidate"
	"github.com/trick77/almanach/internal/gitrepo"
	"github.com/trick77/almanach/internal/inference"
	"github.com/trick77/almanach/internal/redact"
	"github.com/trick77/almanach/internal/store"
)

// Options is one eval run.
type Options struct {
	// Dir is the folder (projects/, labels/); Out where the run's
	// databases, clones and report go.
	Dir, Out string
	// Model writes the pages, Judge scores them: llmwire profile ids.
	Model, Judge string
	// Lookup is the environment: the model keys and any BACKEND_* the
	// pipeline reads (BACKEND_BASELINE_MAX_CALLS, ...), defaults else.
	Lookup func(string) (string, bool)
	// Deadline ends the pipeline; what is written by then is scored.
	Deadline time.Duration
	// Split runs every repository in a database of its own, all side by
	// side: one worker drains one run at a time, so a project's runs take
	// the sum of their calls. A split repository is a project of one, its
	// project step left out and its siblings out of its prompts.
	Split bool
	// Repeat runs everything this many times side by side, each in its own
	// databases; the report gives every run and the means. 0 is once.
	Repeat int
	Log    *slog.Logger
}

// Run runs every project's repositories through a full baseline run, the
// projects side by side, each in its own database, then scores the pages.
func Run(ctx context.Context, opt Options) (*Report, error) {
	projects, err := Load(opt.Dir)
	if err != nil {
		return nil, err
	}
	gitBin, err := gitrepo.Resolve()
	if err != nil {
		return nil, err
	}
	app, err := config.Load(func(k string) string {
		switch k {
		case "BACKEND_DB_PATH", "BACKEND_TEAM_KEYS_PATH", "BACKEND_REPOS_DIR":
			return opt.Out
		case "BACKEND_MODEL":
			return opt.Model
		}
		v, _ := opt.Lookup(k)
		return v
	})
	if err != nil {
		return nil, err
	}
	llm, err := inference.New(inference.Options{Model: opt.Model, ReasoningEffort: app.ReasoningEffort, Logger: opt.Log, Lookup: opt.Lookup,
		NoDiagrams: app.Limits.MaxDiagrams == 0})
	if err != nil {
		return nil, err
	}
	if warning, err := llm.Startup(ctx); err != nil {
		return nil, err
	} else if warning != "" {
		opt.Log.Warn("eval: " + warning)
	}
	judge, err := newJudge(opt.Judge, opt.Lookup, opt.Log)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	pctx, cancel := context.WithTimeout(ctx, opt.Deadline)
	defer cancel()
	// A unit is one database and its worker: a project, or with Split one
	// repository of it, once per repeat (a single run's page swings ±20
	// points of coverage: one run tells little).
	repeat := max(opt.Repeat, 1)
	type unit struct {
		run, project int
		p            Project
		dir          string
	}
	var units []unit
	for k := range repeat {
		base := opt.Out
		if repeat > 1 {
			base = filepath.Join(opt.Out, fmt.Sprintf("run-%d", k+1))
		}
		for i, p := range projects {
			if !opt.Split {
				units = append(units, unit{k, i, p, filepath.Join(base, p.Key)})
				continue
			}
			for _, r := range p.Repos {
				units = append(units, unit{k, i, Project{Key: p.Key, Repos: []Repo{r}}, filepath.Join(base, p.Key, r.Slug)})
			}
		}
	}
	results := make([][]ProjectResult, repeat)
	for k := range repeat {
		results[k] = make([]ProjectResult, len(projects))
		for i, p := range projects {
			results[k][i] = ProjectResult{Key: p.Key, Repos: map[string]RepoResult{}}
		}
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, u := range units {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := runProject(pctx, u.p, u.dir, gitBin, app, llm, opt.Log)
			if err != nil {
				opt.Log.Error("eval: run failed", "project", u.p.Key, "err", err.Error())
			}
			mu.Lock()
			defer mu.Unlock()
			agg := &results[u.run][u.project]
			for slug, rr := range res.Repos {
				agg.Repos[slug] = rr
			}
			agg.TimedOut = agg.TimedOut || res.TimedOut
			if err != nil {
				agg.Error = strings.TrimPrefix(agg.Error+"; "+err.Error(), "; ")
			}
			if res.ProjectPage != nil {
				agg.ProjectPage = res.ProjectPage
			}
		}()
	}
	wg.Wait()
	rep := &Report{Model: opt.Model, Judge: opt.Judge, Started: start, PipelineTook: time.Since(start).Round(time.Second).String(), Repeat: repeat}
	for k := range repeat {
		var run []ProjectReport
		for i, p := range projects {
			run = append(run, score(ctx, p, results[k][i], judge, opt.Log))
		}
		rep.Runs = append(rep.Runs, run)
	}
	rep.Projects = rep.Runs[0]
	rep.Means = means(rep.Runs)
	rep.Took = time.Since(start).Round(time.Second).String()
	rep.total()
	return rep, nil
}

// ProjectResult is what a project's runs left: per repository its run and
// page, and the project's page.
type ProjectResult struct {
	Key         string
	Repos       map[string]RepoResult
	ProjectPage *store.Page
	TimedOut    bool
	Error       string
}

// RepoResult is one repository's run and page.
type RepoResult struct {
	Run   store.BaselineRun
	Calls []store.RunCall
	Page  *store.Page
	// Picked is every file the run's read parts held, docs included, by
	// path; Sizes their bytes.
	Picked map[string]int64
}

// cloneURL is the address a repository is registered under: never
// reached, the clone is laid down from the folder.
func cloneURL(key, slug string) string {
	return "https://eval.invalid/" + key + "/" + slug + ".git"
}

func runProject(ctx context.Context, p Project, out, gitBin string, app *config.App, llm *inference.Client, log *slog.Logger) (ProjectResult, error) {
	res := ProjectResult{Key: p.Key, Repos: map[string]RepoResult{}}
	if _, err := os.Stat(out); err == nil {
		return res, fmt.Errorf("%s holds a run already: give -out a new folder", out)
	}
	clones := filepath.Join(out, "clones")
	if err := os.MkdirAll(clones, 0o750); err != nil {
		return res, err
	}
	db, err := store.Open(ctx, filepath.Join(out, "almanach.db"), log)
	if err != nil {
		return res, err
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		return res, err
	}
	git, err := gitrepo.New(gitBin, clones, "")
	if err != nil {
		return res, err
	}
	poller := clone.New(clone.Deps{Store: db, Git: git, Log: log, Manifests: clone.Manifests{Store: db, Reads: git}})
	for i, r := range p.Repos {
		sr := store.SourceRepo{ProjectKey: p.Key, Slug: r.Slug, CloneURL: cloneURL(p.Key, r.Slug)}
		if i == 0 {
			err = db.CreateSourceWith(ctx, store.ProjectSource{ProjectKey: p.Key, Kind: store.SourceStatic, TokenAuth: "bearer"}, sr)
		} else {
			err = db.AddSourceRepo(ctx, sr)
		}
		if err != nil {
			return res, fmt.Errorf("register %s: %w", r.Slug, err)
		}
	}
	for _, r := range p.Repos {
		head, err := layDown(ctx, db, poller, git, gitBin, p.Key, r)
		if err != nil {
			return res, fmt.Errorf("clone %s: %w", r.Slug, err)
		}
		if err := poller.ReadManifestsNow(ctx, p.Key, r.Slug, head); err != nil {
			log.Warn("eval: manifests", "project", p.Key, "repo", r.Slug, "err", err.Error())
		}
		if _, err := db.EnqueueRun(ctx, p.Key, r.Slug, store.RunFull, store.TriggerFirstReady, "", head); err != nil {
			return res, fmt.Errorf("queue %s: %w", r.Slug, err)
		}
	}
	worker := consolidate.New(db, llm, app.Limits, log).WithBaseline(consolidate.Baseline{Store: db, Code: clone.NewCode(poller, git),
		Catalogue: llm, Describer: llm, Limits: app.Baseline})
	worker.Start(context.WithoutCancel(ctx))
	res.TimedOut = waitRuns(ctx, db)
	// Past the deadline the step in flight is cut off: it is not scored.
	worker.Stop(5 * time.Second)
	for _, r := range p.Repos {
		rr, err := collect(context.WithoutCancel(ctx), db, p.Key, r.Slug)
		if err != nil {
			return res, err
		}
		unread(context.WithoutCancel(ctx), r.Dir, rr.Run.Commit, rr.Picked)
		res.Repos[r.Slug] = rr
	}
	if page, ok, err := db.CurrentPage(context.WithoutCancel(ctx), store.ScopeProject, p.Key, ""); err != nil {
		return res, err
	} else if ok {
		res.ProjectPage = &page
	}
	return res, nil
}

// layDown clones the folder's repository bare into the place the poller
// reads it from and marks it ready at its HEAD.
func layDown(ctx context.Context, db *store.Store, poller *clone.Poller, git *gitrepo.Client, gitBin, key string, r Repo) (string, error) {
	pid, rid, ok, err := db.RepoIDs(ctx, key, r.Slug)
	if err != nil || !ok {
		return "", fmt.Errorf("ids: %w", err)
	}
	dir, err := poller.Dir(pid, rid)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o750); err != nil {
		return "", err
	}
	// The poller's layout: a bare clone whose branches are read from the
	// remote-tracking refs its fetch writes (gitrepo.Fetch).
	for _, args := range [][]string{
		{"clone", "--quiet", "--bare", r.Dir, dir},
		{"-C", dir, "fetch", "--quiet", r.Dir, "+refs/heads/*:refs/remotes/origin/*"},
	} {
		if out, err := exec.CommandContext(ctx, gitBin, args...).CombinedOutput(); err != nil { //nolint:gosec // the operator's folder
			return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
		}
	}
	b, err := exec.CommandContext(ctx, gitBin, "-C", r.Dir, "symbolic-ref", "--short", "HEAD").Output() //nolint:gosec // the operator's folder
	if err != nil {
		return "", fmt.Errorf("branch: %w", err)
	}
	branch := strings.TrimSpace(string(b))
	head, err := git.HeadSHA(ctx, dir, branch)
	if err != nil {
		return "", err
	}
	return head, db.MarkRepoReady(ctx, key, r.Slug, branch, head)
}

// waitRuns waits until no run is open; true when ctx ended first.
func waitRuns(ctx context.Context, db *store.Store) bool {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		if _, open, err := db.NextRun(ctx); err == nil && !open {
			return false
		}
		select {
		case <-ctx.Done():
			return true
		case <-t.C:
		}
	}
}

// planFiles is the part of a run's stored plan that says what it read.
type planFiles struct {
	Subjects []struct {
		Name  string `json:"name"`
		Files []struct {
			Path string `json:"path"`
			Size int64  `json:"size"`
		} `json:"files"`
	} `json:"subjects"`
	Skipped  []string `json:"skipped"`
	Read     []string `json:"read"`
	Overview bool     `json:"overview"`
	Docs     []struct {
		Path string `json:"path"`
		Size int64  `json:"size"`
	} `json:"docs"`
}

func collect(ctx context.Context, db *store.Store, key, slug string) (RepoResult, error) {
	var rr RepoResult
	run, _, ok, err := db.LastRun(ctx, key, slug)
	if err != nil {
		return rr, err
	}
	if ok {
		rr.Run = run
		if rr.Calls, err = db.RunCalls(ctx, run.ID); err != nil {
			return rr, err
		}
		rr.Picked = picked(run.Plan, run.State, run.Step)
	}
	page, ok, err := db.CurrentPage(ctx, store.ScopeRepository, key, slug)
	if err != nil {
		return rr, err
	}
	if ok {
		rr.Page = &page
	}
	return rr, nil
}

// unread leaves out of picked what the run's reader drops at read time
// (clone.Code.ReadFiles): binary, a generator's output by its head, a
// Secret manifest. The plan lists them; the model never saw them.
func unread(ctx context.Context, dir, commit string, picked map[string]int64) {
	if commit == "" {
		return
	}
	for p := range picked {
		body, err := exec.CommandContext(ctx, "git", "-C", dir, "show", commit+":"+p).Output() //nolint:gosec // the corpus' own repository
		if err != nil {
			continue
		}
		if _, ok := redact.File(p, body); !ok || baseline.Binary(body) || baseline.Generated(body) {
			delete(picked, p)
		}
	}
}

// picked is the files of the subjects a run read, less those it skipped,
// and the docs its overview read. A run still reading, or failed while
// reading (cut off by the deadline), read only the steps before its own:
// the subjects after were planned, never read.
func picked(raw json.RawMessage, state string, step int) map[string]int64 {
	out := map[string]int64{}
	var p planFiles
	if len(raw) == 0 || json.Unmarshal(raw, &p) != nil {
		return out
	}
	done := len(p.Read)
	switch state {
	case store.RunQueued, store.RunCataloguing, store.RunDescribing, store.RunFailed:
		done = step
		if p.Overview {
			done--
		}
	}
	read, skipped := map[string]bool{}, map[string]bool{}
	for i, n := range p.Read {
		if i < done {
			read[n] = true
		}
	}
	for _, n := range p.Skipped {
		skipped[n] = true
	}
	for _, s := range p.Subjects {
		if !read[s.Name] || skipped[s.Name] {
			continue
		}
		for _, f := range s.Files {
			out[f.Path] = f.Size
		}
	}
	for _, d := range p.Docs {
		out[d.Path] = d.Size
	}
	return out
}
