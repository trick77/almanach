package store

import (
	"context"
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/trick77/ship/kit/migrate"
)

// migratedFrom0001 is a store at 0001 filled by fill, then migrated to the
// newest schema.
func migratedFrom0001(t *testing.T, fill string) *Store {
	t.Helper()
	ctx := context.Background()
	s := emptyStore(t)
	first, err := fs.ReadFile(migrations(), "0001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrate.Up(ctx, s.db, fstest.MapFS{"0001_init.sql": {Data: first}}, s.migrateOptions()); err != nil {
		t.Fatalf("0001: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, fill); err != nil {
		t.Fatalf("fill 0001: %v", err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate to ids: %v", err)
	}
	return s
}

const t0 = "2026-10-01T12:00:00.000000000Z"

// Rows of every table keyed by names in 0001: a project noergler delivered
// for (PRJ) with a source, a source-only project (SRC) with a listed clone
// and runs, and a removed clone only the ledger knows (GONE/old).
const fill0001 = `
INSERT INTO teams (slug, first_seen, last_seen) VALUES ('alpha', '` + t0 + `', '` + t0 + `');
INSERT INTO projects (key, team, first_seen, last_seen) VALUES ('PRJ', 'alpha', '` + t0 + `', '` + t0 + `');
INSERT INTO repos (project_key, slug, team, first_seen, last_seen) VALUES ('PRJ', 'svc', 'alpha', '` + t0 + `', '` + t0 + `');
INSERT INTO claims (team, project_key, repo_slug, seen_at) VALUES ('alpha', 'PRJ', NULL, '` + t0 + `'), ('alpha', 'PRJ', 'svc', '` + t0 + `');
INSERT INTO deliveries (delivery_id, team, event_type, received_at, payload) VALUES ('d1', 'alpha', 'pr.merged', '` + t0 + `', '{}');
INSERT INTO notes (project_key, repo_slug, pr_id, team, pr_key, pr_title, note, source_commit, merged_at, delivery_id, received_at, consolidated_at)
VALUES ('PRJ', 'svc', 1, 'alpha', 'PRJ/svc#1', 'first', '{"summary":"x"}', 'abc', '` + t0 + `', 'd1', '` + t0 + `', '` + t0 + `');
INSERT INTO note_calls (delivery_id, team, project_key, repo_slug, pr_id, outcome, cost_nano_usd, created_at)
VALUES ('d1', 'alpha', 'PRJ', 'svc', 1, 'ok', 1500000, '` + t0 + `');
INSERT INTO sources (project_key, kind, host, token_auth, created_at, updated_at) VALUES
  ('PRJ', 'static', NULL, 'basic', '` + t0 + `', '` + t0 + `'),
  ('SRC', 'bitbucket', 'https://forge.example.invalid', 'bearer', '` + t0 + `', '` + t0 + `');
INSERT INTO source_repos (project_key, slug, clone_url, origin, head_commit, state, created_at) VALUES
  ('PRJ', 'svc', 'https://forge.example.invalid/svc.git', 'manual', 'abc', 'ready', '` + t0 + `'),
  ('SRC', 'api', 'https://forge.example.invalid/api.git', 'listed', 'c0ffee', 'ready', '` + t0 + `');
INSERT INTO baseline_runs (id, project_key, slug, mode, trigger, "commit", state, queued_at, finished_at) VALUES
  (5, 'SRC', 'api', 'full', 'manual', 'c0ffee', 'done', '` + t0 + `', '` + t0 + `'),
  (7, 'SRC', 'api', 'full', 'manual', 'c0ffee', 'queued', '` + t0 + `', NULL);
UPDATE sqlite_sequence SET seq = 9 WHERE name = 'baseline_runs';
INSERT INTO llm_calls (id, project_key, repo_slug, source_kind, source_pr_key, outcome, cost_nano_usd, created_at) VALUES
  (1, 'PRJ', 'svc', 'pr', 'PRJ/svc#1', 'ok', 100, '` + t0 + `'),
  (3, 'GONE', 'old', 'pr', 'GONE/old#4', 'ok', 300, '` + t0 + `');
INSERT INTO llm_calls (id, project_key, repo_slug, source_kind, source_commit, purpose, baseline_run_id, outcome, cost_nano_usd, created_at)
VALUES (2, 'SRC', 'api', 'baseline', 'c0ffee', 'overview', 5, 'ok', 200, '` + t0 + `');
INSERT INTO revisions (id, scope, project_key, repo_slug, revision, summary, statements, chars, source_kind, source_pr_key, llm_call_id, created_at) VALUES
  (1, 'repository', 'PRJ', 'svc', 1, 'svc page', '[{"id":"s1","kind":"rule","name":"n","text":"t","files":[],"sources":[{"kind":"pr","pr_key":"PRJ/svc#1"}]}]', 1, 'pr', 'PRJ/svc#1', 1, '` + t0 + `'),
  (2, 'project', 'PRJ', NULL, 1, 'prj page', '[]', 1, 'pr', 'PRJ/svc#1', 1, '` + t0 + `');
INSERT INTO revisions (id, scope, project_key, repo_slug, revision, summary, statements, chars, source_kind, source_commit, baseline_run_id, created_at)
VALUES (3, 'repository', 'SRC', 'api', 1, 'api page', '[]', 1, 'baseline', 'c0ffee', 5, '` + t0 + `');
INSERT INTO pages (scope, project_key, repo_slug, current_revision_id, updated_at) VALUES
  ('repository', 'PRJ', 'svc', 1, '` + t0 + `'), ('project', 'PRJ', '', 2, '` + t0 + `'), ('repository', 'SRC', 'api', 3, '` + t0 + `');
`

// 0002 carries every row over onto ids: pages, notes, calls, costs, runs
// and clones read the same through the store, the run counter keeps
// counting, and nothing dangles.
func TestMigrate0002_CarriesEverythingOver(t *testing.T) {
	s := migratedFrom0001(t, fill0001)
	ctx := context.Background()

	for _, c := range []struct{ scope, key, slug, summary string }{
		{ScopeRepository, "PRJ", "svc", "svc page"}, {ScopeProject, "PRJ", "", "prj page"}, {ScopeRepository, "SRC", "api", "api page"},
	} {
		p, ok, err := s.CurrentPage(ctx, c.scope, c.key, c.slug)
		if err != nil || !ok || p.Summary != c.summary || p.ProjectKey != c.key || p.RepoSlug != c.slug {
			t.Errorf("page %s %s/%s: %+v ok=%v err=%v", c.scope, c.key, c.slug, p, ok, err)
		}
	}
	p, _, _ := s.CurrentPage(ctx, ScopeRepository, "PRJ", "svc")
	if len(p.Statements) != 1 || len(p.Statements[0].Sources) != 1 || p.Statements[0].Sources[0].PRKey != "PRJ/svc#1" {
		t.Errorf("statements not carried byte for byte: %+v", p.Statements)
	}
	d, ok, err := s.NoteByKey(ctx, "PRJ", "svc", 1)
	if err != nil || !ok || d.PRKey != "PRJ/svc#1" || d.Call == nil || *d.Call.CostNanoUSD != 1_500_000 || d.ConsolidatedAt == nil {
		t.Errorf("note: %+v ok=%v err=%v", d, ok, err)
	}

	noergler, err := s.Projects(ctx, false)
	if err != nil || len(noergler) != 1 || noergler[0].Key != "PRJ" || noergler[0].Repos != 1 || noergler[0].Notes != 1 {
		t.Errorf("noergler's projects: %+v %v", noergler, err)
	}
	sourced, err := s.Projects(ctx, true)
	if err != nil || len(sourced) != 2 || sourced[0].Key != "PRJ" || sourced[1].Key != "SRC" || sourced[1].Team != "" {
		t.Errorf("sourced projects: %+v %v", sourced, err)
	}

	repos, err := s.SourceRepos(ctx, "SRC")
	if err != nil || len(repos) != 1 || repos[0].Slug != "api" || repos[0].State != RepoReady || repos[0].HeadCommit != "c0ffee" {
		t.Errorf("source repos: %+v %v", repos, err)
	}
	last, _, ok, err := s.LastRun(ctx, "SRC", "api")
	if err != nil || !ok || last.ID != 7 || !last.Open() {
		t.Errorf("last run: %+v ok=%v err=%v", last, ok, err)
	}
	runs, err := s.SourceRuns(ctx, "SRC")
	if err != nil || runs["api"].Documented != "c0ffee" || runs["api"].Last.ID != 7 {
		t.Errorf("source runs: %+v %v", runs, err)
	}
	if _, err := s.EnqueueRun(ctx, "SRC", "api", RunFull, TriggerManual, "", "c0ffee"); !errors.Is(err, ErrExists) {
		t.Errorf("second open run: %v, want ErrExists", err)
	}
	id, err := s.EnqueueRun(ctx, "PRJ", "svc", RunFull, TriggerManual, "", "abc")
	if err != nil || id != 10 {
		t.Errorf("next run id = %d, %v; want 10 (never reused past the old counter)", id, err)
	}

	if n := count(t, s, "llm_calls", ""); n != 3 {
		t.Errorf("llm_calls = %d, want 3 (the removed clone's too)", n)
	}
	if n := count(t, s, "repos", "slug = 'old'"); n != 1 {
		t.Errorf("the removed clone's repository = %d, want 1", n)
	}
	if n := count(t, s, "claims", ""); n != 2 {
		t.Errorf("claims = %d, want 2", n)
	}
	var problems int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&problems); err != nil || problems != 0 {
		t.Errorf("foreign key check: %d rows, %v", problems, err)
	}
}

// A change of case on a project's own key is a rename like any other:
// afterwards a note delivered under the new spelling lands on the source
// repository and its pages, and the old key still finds the project.
func TestRenameProject_CaseOnly(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	sourceRepo(t, s, "ACME", "svc")
	if _, err := s.InsertRevision(ctx, NewRevision{Scope: ScopeRepository, ProjectKey: "ACME", RepoSlug: "svc", Summary: "kept", SourcePR: "ACME/svc#1"}); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.IsSourceRepo(ctx, "acme", "svc"); ok {
		t.Fatal("acme is a source repository before the rename")
	}
	if _, err := s.RenameProject(ctx, "ACME", "acme"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if ok, err := s.IsSourceRepo(ctx, "acme", "svc"); err != nil || !ok {
		t.Fatalf("acme/svc after the rename: %v %v", ok, err)
	}
	ingest(t, s, noteFor("acme", "svc", 2))
	notes, err := s.Notes(ctx, "ACME", "svc", 10)
	if err != nil || len(notes) != 1 || notes[0].PRKey != "acme/svc#2" {
		t.Errorf("notes under the old key: %+v %v", notes, err)
	}
	p, ok, err := s.CurrentPage(ctx, ScopeRepository, "acme", "svc")
	if err != nil || !ok || p.Summary != "kept" || p.ProjectKey != "acme" {
		t.Errorf("page under the new key: %+v ok=%v err=%v", p, ok, err)
	}
	if key, ok, _ := s.ProjectKeyOf(ctx, "ACME"); !ok || key != "acme" {
		t.Errorf("ProjectKeyOf(ACME) = %q %v", key, ok)
	}
	src, ok, _ := s.SourceByKey(ctx, "ACME")
	if !ok || src.ProjectKey != "acme" {
		t.Errorf("source by the old key: %+v %v", src, ok)
	}
	// And back: its own names, in any case.
	if _, err := s.RenameProject(ctx, "acme", "ACME"); err != nil {
		t.Errorf("rename back: %v", err)
	}
}

// A key another project ever had, in any case, is never given away.
func TestRenameProject_NameNeverReused(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	sourceRepo(t, s, "ONE", "a")
	sourceRepo(t, s, "TWO", "b")
	if _, err := s.RenameProject(ctx, "ONE", "FIRST"); err != nil {
		t.Fatal(err)
	}
	for _, to := range []string{"ONE", "one", "first"} {
		if _, err := s.RenameProject(ctx, "TWO", to); !errors.Is(err, ErrNameTaken) {
			t.Errorf("TWO → %s: %v, want ErrNameTaken", to, err)
		}
	}
	if err := s.CreateSource(ctx, staticSource("one")); !errors.Is(err, ErrNameTaken) {
		t.Errorf("a new source under a former key in another case: %v, want ErrNameTaken", err)
	}
	if _, err := s.RenameProject(ctx, "NOPE", "x"); !errors.Is(err, ErrNoProject) {
		t.Errorf("unknown key: %v, want ErrNoProject", err)
	}
}

// A hand-entered repository renamed keeps its pages, notes and runs; the
// old slug still finds it; a new clone URL queues a sync; an open run or a
// slug another repository had refuses the rename.
func TestRenameRepo(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	sourceRepo(t, s, "PRJ", "svc")
	sourceRepo(t, s, "PRJ", "other")
	if err := s.MarkRepoReady(ctx, "PRJ", "svc", "main", "abc"); err != nil {
		t.Fatal(err)
	}
	ingest(t, s, noteFor("PRJ", "svc", 1))
	if _, err := s.InsertRevision(ctx, NewRevision{Scope: ScopeRepository, ProjectKey: "PRJ", RepoSlug: "svc", Summary: "kept", SourcePR: "PRJ/svc#1"}); err != nil {
		t.Fatal(err)
	}
	run := enqueue(t, s, "PRJ", "svc")
	if err := s.RenameRepo(ctx, "PRJ", "svc", "core", ""); !errors.Is(err, ErrRunOpen) {
		t.Fatalf("rename with an open run: %v, want ErrRunOpen", err)
	}
	finish(t, s, run, RunDone)

	if err := s.RenameRepo(ctx, "PRJ", "svc", "core", "https://forge.example.invalid/core.git"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	r, ok, err := s.SourceRepoByName(ctx, "PRJ", "svc")
	if err != nil || !ok || r.Slug != "core" || r.CloneURL != "https://forge.example.invalid/core.git" || r.State != RepoPending {
		t.Errorf("clone by the old slug: %+v ok=%v err=%v", r, ok, err)
	}
	p, ok, _ := s.CurrentPage(ctx, ScopeRepository, "PRJ", "core")
	if !ok || p.Summary != "kept" || p.RepoSlug != "core" {
		t.Errorf("page under the new slug: %+v %v", p, ok)
	}
	if n, _ := s.Notes(ctx, "PRJ", "core", 10); len(n) != 1 || n[0].RepoSlug != "core" || n[0].PRKey != "PRJ/svc#1" {
		t.Errorf("notes under the new slug, PR key as merged: %+v", n)
	}
	if doc, ok, _ := s.DocumentedRun(ctx, "PRJ", "core"); !ok || doc.ID != run.ID || doc.Slug != "core" {
		t.Errorf("documented run under the new slug: %+v %v", doc, ok)
	}
	if key, slug, ok, _ := s.RepoNamesOf(ctx, "PRJ", "svc"); !ok || key != "PRJ" || slug != "core" {
		t.Errorf("RepoNamesOf(svc) = %s/%s %v", key, slug, ok)
	}
	for _, to := range []string{"other", "OTHER"} {
		if err := s.RenameRepo(ctx, "PRJ", "core", to, ""); !errors.Is(err, ErrNameTaken) {
			t.Errorf("core → %s: %v, want ErrNameTaken", to, err)
		}
	}
	if err := s.RenameRepo(ctx, "PRJ", "other", "SVC", ""); !errors.Is(err, ErrNameTaken) {
		t.Errorf("other → SVC (core's former slug): %v, want ErrNameTaken", err)
	}
	if err := s.AddSourceRepo(ctx, SourceRepo{ProjectKey: "PRJ", Slug: "Svc", CloneURL: "https://forge.example.invalid/x.git"}); !errors.Is(err, ErrNameTaken) {
		t.Errorf("a new repository under a former slug in another case: %v, want ErrNameTaken", err)
	}
	if err := s.RenameRepo(ctx, "PRJ", "core", "Core", ""); err != nil {
		t.Errorf("a change of case of its own slug: %v", err)
	}
	if err := s.RenameRepo(ctx, "PRJ", "nope", "x", ""); !errors.Is(err, ErrNoRepo) {
		t.Errorf("unknown slug: %v, want ErrNoRepo", err)
	}
}

// A repository renamed in Bitbucket is the same repository by its forge id:
// slug changed, nothing added, nothing missing, the old slug a name of it.
// A new repository under a former slug of another is left out.
func TestApplyListing_FollowsRenameByForgeID(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if err := s.CreateSource(ctx, bitbucketSource("SHOP")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyListing(ctx, "SHOP", []ListedRepo{{Slug: "ui", CloneURL: "https://forge.example.invalid/ui.git", ForgeID: 11}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertRevision(ctx, NewRevision{Scope: ScopeRepository, ProjectKey: "SHOP", RepoSlug: "ui", Summary: "kept", SourcePR: "SHOP/ui#1"}); err != nil {
		t.Fatal(err)
	}
	res, err := s.ApplyListing(ctx, "SHOP", []ListedRepo{{Slug: "web", CloneURL: "https://forge.example.invalid/web.git", ForgeID: 11}})
	if err != nil || res.Added != 0 || res.Missing != 0 || res.Renamed != 1 || len(res.Conflicts) != 0 {
		t.Fatalf("listing after the rename: %+v %v", res, err)
	}
	repos, _ := s.SourceRepos(ctx, "SHOP")
	if len(repos) != 1 || repos[0].Slug != "web" || repos[0].CloneURL != "https://forge.example.invalid/web.git" {
		t.Errorf("repos: %+v", repos)
	}
	if p, ok, _ := s.CurrentPage(ctx, ScopeRepository, "SHOP", "ui"); !ok || p.Summary != "kept" || p.RepoSlug != "web" {
		t.Errorf("page by the old slug: %+v %v", p, ok)
	}

	res, err = s.ApplyListing(ctx, "SHOP", []ListedRepo{
		{Slug: "web", CloneURL: "https://forge.example.invalid/web.git", ForgeID: 11},
		{Slug: "UI", CloneURL: "https://forge.example.invalid/ui-new.git", ForgeID: 12},
	})
	if err != nil || res.Added != 0 || len(res.Conflicts) != 1 || res.Conflicts[0] != "UI" {
		t.Fatalf("a new repository under a former slug: %+v %v", res, err)
	}
	if repos, _ := s.SourceRepos(ctx, "SHOP"); len(repos) != 1 {
		t.Errorf("the conflicting repository was added: %+v", repos)
	}
}

// TRANSITIONAL(ids): a repository listed before 0002 has no forge id; the
// first listing that names it by its slug records it. Remove with the
// backfill in listedRepo.
func TestApplyListing_BackfillsForgeIDBySlug(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if err := s.CreateSource(ctx, bitbucketSource("SHOP")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyListing(ctx, "SHOP", []ListedRepo{{Slug: "ui", CloneURL: "u"}}); err != nil {
		t.Fatal(err)
	}
	if n := count(t, s, "repos", "slug = 'ui' AND forge_id IS NULL"); n != 1 {
		t.Fatalf("ui without a forge id = %d, want 1", n)
	}
	res, err := s.ApplyListing(ctx, "SHOP", []ListedRepo{{Slug: "ui", CloneURL: "u", ForgeID: 11}})
	if err != nil || res.Added != 0 || res.Renamed != 0 {
		t.Fatalf("listing with a forge id: %+v %v", res, err)
	}
	if n := count(t, s, "repos", "slug = 'ui' AND forge_id = 11"); n != 1 {
		t.Errorf("ui's forge id not recorded")
	}
}
