package consolidate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/trick77/almanach/internal/baseline"
	"github.com/trick77/almanach/internal/config"
	"github.com/trick77/almanach/internal/diagram"
	"github.com/trick77/almanach/internal/gitrepo"
	"github.com/trick77/almanach/internal/inference"
	"github.com/trick77/almanach/internal/store"
)

// The prompt eval: full baseline runs of real repositories through a real
// model, every repository of one project, then the pages dumped for a
// reader. Asserts are structural only, never prose. `make prompt-eval`; the
// gate compiles it and, without its EVAL_* variables, skips it.

const evalProject = "EVAL"

// evalStore is the test store whose repositories' summaries are the pages
// the runs wrote, so each project step sees the ones before it.
type evalStore struct{ *runStore }

func (s evalStore) RepoSummaries(_ context.Context, project string) ([]store.RepoSummary, error) {
	var out []store.RepoSummary
	for _, r := range s.repos {
		out = append(out, store.RepoSummary{Slug: r.Slug, Summary: s.pages[pageKey(store.ScopeRepository, project, r.Slug)].Summary})
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
		// As clone.Code: a cut step or a broken reader is no file left out.
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(err, gitrepo.ErrReaderBroken) || errors.Is(err, gitrepo.ErrBadSHA) {
			return nil, err
		}
		if err == nil && !baseline.Binary(body) {
			out[f.Path] = body
		}
	}
	return out, nil
}

// evalModels writes every prompt to dir before it goes out, numbered, so a
// stalled or odd call can be read and replayed.
type evalModels struct {
	llm *inference.Client
	dir string
	n   *int
}

func (m evalModels) keep(kind, prompt string) {
	*m.n++
	_ = os.MkdirAll(m.dir, 0o755)
	_ = os.WriteFile(filepath.Join(m.dir, fmt.Sprintf("%02d-%s.txt", *m.n, kind)), []byte(prompt), 0o644)
}

func (m evalModels) Catalogue(ctx context.Context, prompt string) inference.CatalogueResult {
	m.keep("catalogue", prompt)
	return m.llm.Catalogue(ctx, prompt)
}

func (m evalModels) Patch(ctx context.Context, prompt string) inference.PatchResult {
	m.keep("patch", prompt)
	return m.llm.Patch(ctx, prompt)
}

func (m evalModels) Describe(ctx context.Context, prompt string) inference.DescriptionResult {
	m.keep("project", prompt)
	return m.llm.Describe(ctx, prompt)
}

// evalClient is EVAL_MODEL at EVAL_EFFORT.
func evalClient(t *testing.T, log *slog.Logger) *inference.Client {
	t.Helper()
	llm, err := inference.New(inference.Options{Model: os.Getenv("EVAL_MODEL"), ReasoningEffort: os.Getenv("EVAL_EFFORT"),
		Logger: log, Lookup: os.LookupEnv})
	if err != nil {
		t.Fatal(err)
	}
	return llm
}

// TestPromptReplay sends the kept prompt in EVAL_PROMPT once, under the
// schema its name says, and logs what came back: a stalled step, alone.
func TestPromptReplay(t *testing.T) {
	file := os.Getenv("EVAL_PROMPT")
	if file == "" || os.Getenv("EVAL_MODEL") == "" {
		t.Skip("EVAL_PROMPT and EVAL_MODEL unset")
	}
	prompt, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	llm := evalClient(t, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))
	ctx, start := context.Background(), time.Now()
	var call inference.Call
	var got string
	switch {
	case strings.HasSuffix(file, "-catalogue.txt"):
		res := llm.Catalogue(ctx, string(prompt))
		call, got = res.Call, fmt.Sprintf("%d subjects", len(res.Catalogue.Subjects))
	case strings.HasSuffix(file, "-patch.txt"):
		res := llm.Patch(ctx, string(prompt))
		call, got = res.Call, fmt.Sprintf("%d edits, %d added, summary %v", len(res.Patch.Statements), len(res.Patch.Added), res.Patch.Summary != nil)
	case strings.HasSuffix(file, "-project.txt"):
		res := llm.Describe(ctx, string(prompt))
		call, got = res.Call, fmt.Sprintf("description %v", res.Description != nil)
	default:
		t.Fatalf("EVAL_PROMPT %s: want a kept prompt, NN-catalogue|patch|project.txt", file)
	}
	t.Logf("%s after %s: %s, err %v", call.Outcome, time.Since(start).Round(time.Second), got, call.Err)
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
	for _, repo := range evalRepos(t, repos) {
		slug, url := repo[0], repo[1]
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
	models := evalModels{llm: evalClient(t, log), dir: filepath.Join(out, "prompts"), n: new(int)}
	// The production defaults: config with only what it requires set.
	app, err := config.Load(func(k string) string {
		switch k {
		case "ALMANACH_DB_PATH", "ALMANACH_TEAM_KEYS_PATH", "ALMANACH_MODEL":
			return "unused"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	w := New(st, &fakeMerger{}, app.Limits, log)
	w.WithBaseline(Baseline{Store: st, Code: evalCode{git}, Catalogue: models, Describer: models, Limits: app.Baseline})

	for _, repo := range st.repos {
		st.runs = append(st.runs, store.BaselineRun{ID: int64(len(st.runs) + 1), ProjectKey: evalProject, Slug: repo.Slug,
			Mode: store.RunFull, Trigger: store.TriggerManual, Commit: repo.HeadCommit, State: store.RunQueued})
		run := func() store.BaselineRun { return st.runs[len(st.runs)-1] }
		// A gateway hold ends a drain; the next one resumes after it. Any
		// other pause (a store refusal, say) is the eval's fault: stop, never
		// drain again into paid steps.
		for lap := 0; run().Open() && lap < 40; lap++ {
			w.drain(ctx)
			if !run().Open() {
				break
			}
			if !w.holding() {
				t.Fatalf("%s: drain paused without a hold, run at %s step %d: %s", repo.Slug, run().State, run().Step, run().Error)
			}
			time.Sleep(time.Until(w.retryAt))
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
	// A slug opening a German sentence is capitalised ("Almanach liest"):
	// matched as a whole word in any case, so "web" is not found in
	// "Webhook".
	for _, repo := range st.repos {
		if !regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(repo.Slug) + `\b`).MatchString(project.Summary) {
			t.Errorf("project summary never names %s", repo.Slug)
		}
	}
	checkSpelling(t, "project", project)
	// Every project carries its map, every repository on it, beside what
	// they talk to; the store holds only maps the checks passed.
	if project.Map == nil {
		t.Error("project: no map")
	} else {
		onMap := map[string]bool{}
		for _, n := range project.Map.Nodes {
			onMap[n.ID] = n.Zone == diagram.ZoneRepositories
		}
		for _, repo := range st.repos {
			if !onMap[repo.Slug] {
				t.Errorf("project map lacks repository %s", repo.Slug)
			}
		}
		if len(project.Map.Nodes) <= len(st.repos) {
			t.Errorf("project map holds the repositories only: %d nodes", len(project.Map.Nodes))
		}
	}
	dump(t, out, "_project", project)
}

// evalRepos is EVAL_REPOS as slug, url pairs in order; a malformed entry
// fails the test.
func evalRepos(t *testing.T, list string) [][2]string {
	t.Helper()
	var out [][2]string
	for _, pair := range strings.Split(list, ",") {
		slug, url, ok := strings.Cut(pair, "=")
		if !ok || slug == "" || url == "" {
			t.Fatalf("EVAL_REPOS entry %q: want slug=url", pair)
		}
		out = append(out, [2]string{slug, url})
	}
	return out
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
	checkSpelling(t, slug, p)
}

// checkSpelling holds a page's prose, summary and statements, to the
// prompts' umlaut rule.
func checkSpelling(t *testing.T, slug string, p store.Page) {
	t.Helper()
	if w := spelledOutWord(p.Summary); w != "" {
		t.Errorf("%s summary: umlaut spelled out (%s)", slug, w)
	}
	for _, s := range p.Statements {
		if w := spelledOutWord(s.Name + "\n" + s.Text); w != "" {
			t.Errorf("%s %s %q: umlaut spelled out (%s)", slug, s.ID, s.Name, w)
		}
	}
}

// spelledOutWord is the first word of s whose umlauts are spelled out
// ("ergaenzen", "fuer"), the slip the prompts' spelling rule forbids; ""
// when none. A token shaped like an identifier (a backtick, a slash, a dot
// or an underscore in it, or a capital inside it) is a quote, never prose:
// an eval quoted a test's `Datenueberpruefung`.
func spelledOutWord(s string) string {
	for _, tok := range strings.Fields(s) {
		if strings.ContainsAny(tok, "`/._") || innerCapital(tok) {
			continue
		}
		if w := strings.TrimFunc(tok, func(r rune) bool { return !unicode.IsLetter(r) }); spelledOut.MatchString(w) {
			return w
		}
	}
	return ""
}

// The eval's spelling check finds a spelled-out word in prose wherever it
// stands, and passes an identifier however it is quoted.
func TestSpelledOutWord(t *testing.T) {
	for s, want := range map[string]string{
		"Gilt für jede Datei.":                        "",
		"Gilt fuer jede Datei.":                       "fuer",
		"Ein ` Zeichen fuer die Suche `x`":            "fuer",
		"Wert`x` ueber alles":                         "ueber",
		"Die Regel (`Datenueberpruefung` ist gleich)": "",
		"in internal/pruefung/handler.go":             "",
		"heisst DatenUeberpruefung im Code":           "",
		"eine neue Queue, aktuell":                    "",
		"Aenderungen, die bleiben.":                   "Aenderungen",
	} {
		if got := spelledOutWord(s); got != want {
			t.Errorf("spelledOutWord(%q) = %q, want %q", s, got, want)
		}
	}
}

func innerCapital(tok string) bool {
	for i, r := range tok {
		if i > 0 && unicode.IsUpper(r) {
			return true
		}
	}
	return false
}

// spelledOut matches one word, the common German ones a model writes with
// their umlauts spelled out.
var spelledOut = regexp.MustCompile(`(?i)^(fuer|ueber\w*|\w*aender\w*|\w*pruef\w*|\w*fueg\w*|\w*ergaenz\w*|moeglich\w*|\w*groess\w*|waehr\w*|koenn\w*|muess\w*|naechst\w*|spaet\w*)$`)

func dump(t *testing.T, dir, name string, p store.Page) {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n%s\n", name, p.Summary)
	for _, s := range p.Statements {
		fmt.Fprintf(&b, "\n## [%s] %s: %s\n\n%s\n\nFiles: %s\n%s", s.ID, s.Kind, s.Name, s.Text, strings.Join(s.Files, ", "), diagramLine(s.Diagram))
	}
	if p.Map != nil {
		fmt.Fprintf(&b, "\n## Map\n\n%s\n", projectMap(p))
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	// The page as stored, for TestEvalLoad.
	j, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".json"), j, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestEvalLoad loads the pages an eval of EVAL_REPOS wrote (EVAL_OUT, one
// <slug>.json each and _project.json) into EVAL_DB, a new SQLite file the
// app serves as ALMANACH_DB_PATH with ALMANACH_REPOS_DIR set: project EVAL
// as a static source, each repository on it ready, each page one baseline
// revision. Only EVAL_REPOS' pages, so another run's left in EVAL_OUT never
// joins in; a missing one fails, and a failed load leaves no file behind.
// The pages and their diagrams are then read in the UI, as a reader sees
// them. No model call: `make prompt-eval-db`.
func TestEvalLoad(t *testing.T) {
	out, path := os.Getenv("EVAL_OUT"), os.Getenv("EVAL_DB")
	if out == "" || path == "" {
		t.Skip("EVAL_OUT and EVAL_DB unset: make prompt-eval-db")
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatalf("%s exists: EVAL_DB names a new file", path)
	}
	read := func(name string) store.Page {
		b, err := os.ReadFile(filepath.Join(out, name+".json"))
		if err != nil {
			t.Fatalf("%v: run make prompt-eval for these EVAL_REPOS first", err)
		}
		var p store.Page
		if err := json.Unmarshal(b, &p); err != nil {
			t.Fatalf("%s.json: %v", name, err)
		}
		return p
	}
	ctx := context.Background()
	st, err := store.Open(ctx, path, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		st.Close()
		if t.Failed() {
			for _, f := range []string{path, path + "-wal", path + "-shm", path + "-migrate.lock"} {
				_ = os.Remove(f)
			}
		}
	})
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateSource(ctx, store.ProjectSource{ProjectKey: evalProject, Kind: store.SourceStatic, TokenAuth: store.TokenBasic}); err != nil {
		t.Fatal(err)
	}
	insert := func(name string, rev store.NewRevision, p store.Page) {
		rev.ProjectKey, rev.Summary, rev.Statements = evalProject, p.Summary, p.Statements
		rev.Chars, rev.SourceKind, rev.SourceCommit = prose(p.Summary, p.Statements), store.FromBaseline, evalCommit(p)
		if _, err := st.InsertRevision(ctx, rev); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		t.Logf("%s: %d statements", name, len(p.Statements))
	}
	for _, repo := range evalRepos(t, os.Getenv("EVAL_REPOS")) {
		slug, url := repo[0], repo[1]
		p := read(slug)
		if err := st.AddSourceRepo(ctx, store.SourceRepo{ProjectKey: evalProject, Slug: slug, CloneURL: url, Origin: store.OriginManual}); err != nil {
			t.Fatal(err)
		}
		if err := st.MarkRepoReady(ctx, evalProject, slug, "", evalCommit(p)); err != nil {
			t.Fatal(err)
		}
		insert(slug, store.NewRevision{Scope: store.ScopeRepository, RepoSlug: slug}, p)
	}
	p := read("_project")
	insert("_project", store.NewRevision{Scope: store.ScopeProject, Map: p.Map}, p)
}

// evalCommit is the commit a dumped page was read at, from its statements'
// baseline sources; "eval" when none names one.
func evalCommit(p store.Page) string {
	if p.SourceCommit != "" {
		return p.SourceCommit
	}
	for _, s := range p.Statements {
		for _, src := range s.Sources {
			if src.IsBaseline() && src.Commit != "" {
				return src.Commit
			}
		}
	}
	return "eval"
}
