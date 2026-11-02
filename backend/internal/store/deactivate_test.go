package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/trick77/almanach/internal/manifest"
)

// row is the source row of slug in tools.
func row(t *testing.T, s *Store, slug string) SourceRepo {
	t.Helper()
	r, ok, err := s.SourceRepoByName(context.Background(), "tools", slug)
	if err != nil || !ok {
		t.Fatalf("row %s: ok=%v err=%v", slug, ok, err)
	}
	return r
}

// projectPage gives the project a page, so a deactivation has one to take
// the repository out of.
func projectPage(t *testing.T, s *Store, key string) {
	t.Helper()
	if _, err := s.InsertRevision(context.Background(), NewRevision{Scope: ScopeProject, ProjectKey: key, Summary: "Werkzeuge.",
		Statements: []Statement{}, SourceKind: FromBaseline, SourceCommit: "c0ffee"}); err != nil {
		t.Fatal(err)
	}
}

// A deactivated repository is no source anywhere: not for the notes, the
// wiki, the project step, the sync, a run; its row stays on the source.
// Activated, it is all of them again.
func TestDeactivateSourceRepo_NoSourceAnywhere(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	sourceRepo(t, s, "tools", "svc")
	sourceRepo(t, s, "tools", "web")
	sourceRepo(t, s, "tools", "cli")
	if err := s.MarkRepoReady(ctx, "tools", "svc", "main", "aaa"); err != nil {
		t.Fatal(err)
	}
	queuedRun := enqueue(t, s, "tools", "svc")

	queued, err := s.DeactivateSourceRepo(ctx, "tools", "svc")
	if err != nil || queued {
		t.Fatalf("deactivate without a project page: queued=%v err=%v, want no departure run", queued, err)
	}
	if _, ok, _ := s.RunByID(ctx, queuedRun.ID); ok {
		t.Error("the queued run of a deactivated repository is still there")
	}
	if ok, _ := s.IsSourceRepo(ctx, "tools", "svc"); ok {
		t.Error("a deactivated repository is a source repository for the notes")
	}
	if n, _ := s.SourceRepoCount(ctx, "tools"); n != 2 {
		t.Errorf("SourceRepoCount = %d, want 2", n)
	}
	if ok, _ := s.RepoExists(ctx, "tools", "svc", true); ok {
		t.Error("a deactivated repository's page is in the wiki")
	}
	repos, _ := s.Repos(ctx, "tools", true)
	sums, _ := s.RepoSummaries(ctx, "tools")
	if len(repos) != 2 || len(sums) != 2 {
		t.Errorf("Repos = %+v, RepoSummaries = %+v, want web and cli", repos, sums)
	}
	jobs, _ := s.SyncJobs(ctx)
	for _, j := range jobs {
		if j.Repo.Slug == "svc" {
			t.Error("a deactivated repository is a sync job")
		}
	}
	if n, _ := s.RequestSync(ctx, "tools", "svc"); n != 0 {
		t.Errorf("RequestSync moved %d rows of a deactivated repository", n)
	}
	if _, err := s.EnqueueRun(ctx, "tools", "svc", RunFull, TriggerManual, "", "aaa"); !errors.Is(err, ErrNoRepo) {
		t.Errorf("EnqueueRun of a deactivated repository: %v, want ErrNoRepo", err)
	}
	row, ok, _ := s.SourceRepoByName(ctx, "tools", "svc")
	if !ok || row.Active() || row.HeadCommit != "" || row.State != RepoPending {
		t.Fatalf("the row: %+v, want deactivated, its head forgotten, pending", row)
	}
	rows, _ := s.Sources(ctx)
	if rows[0].Repos != 3 || rows[0].Inactive != 1 || rows[0].Active != 2 {
		t.Errorf("counts = %+v, want 3 repos, 1 inactive, the 2 others pending", rows[0])
	}
	// Twice is once.
	if queued, err := s.DeactivateSourceRepo(ctx, "tools", "svc"); err != nil || queued {
		t.Errorf("deactivate again: queued=%v err=%v", queued, err)
	}

	if err := s.ActivateSourceRepo(ctx, "tools", "svc"); err != nil {
		t.Fatal(err)
	}
	row, _, _ = s.SourceRepoByName(ctx, "tools", "svc")
	if !row.Active() || row.ReactivatedAt == nil || row.State != RepoPending {
		t.Fatalf("after activation: %+v", row)
	}
	if ok, _ := s.IsSourceRepo(ctx, "tools", "svc"); !ok {
		t.Error("an activated repository is no source repository")
	}
	if n, _ := s.SourceRepoCount(ctx, "tools"); n != 3 {
		t.Errorf("SourceRepoCount after activation = %d, want 3", n)
	}
	if _, err := s.DeactivateSourceRepo(ctx, "tools", "nope"); !errors.Is(err, ErrNoRepo) {
		t.Errorf("unknown repository: %v, want ErrNoRepo", err)
	}
}

// With a project page, a deactivation queues the departure run at the head
// the repository had; a started run refuses it, the clone being read. The
// departure run documents nothing, and an activation drops it while queued.
func TestDeactivateSourceRepo_DepartureRun(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	sourceRepo(t, s, "tools", "svc")
	sourceRepo(t, s, "tools", "web")
	projectPage(t, s, "tools")
	if err := s.MarkRepoReady(ctx, "tools", "svc", "main", "aaa"); err != nil {
		t.Fatal(err)
	}
	documented := finish(t, s, enqueue(t, s, "tools", "svc"), RunDone)
	started := enqueue(t, s, "tools", "svc")
	started.State = RunCataloguing
	if ok, err := s.SaveRun(ctx, started); err != nil || !ok {
		t.Fatal(err)
	}
	if _, err := s.DeactivateSourceRepo(ctx, "tools", "svc"); !errors.Is(err, ErrRunOpen) {
		t.Fatalf("deactivate under a started run: %v, want ErrRunOpen", err)
	}
	if ok, _ := s.IsSourceRepo(ctx, "tools", "svc"); !ok {
		t.Fatal("a refused deactivation took the repository out")
	}
	finish(t, s, started, RunFailed)

	queued, err := s.DeactivateSourceRepo(ctx, "tools", "svc")
	if err != nil || !queued {
		t.Fatalf("deactivate: queued=%v err=%v", queued, err)
	}
	next, ok, err := s.NextRun(ctx)
	if err != nil || !ok || next.Trigger != TriggerDeparture || next.Slug != "svc" || next.Commit != "aaa" || string(next.Plan) != `{"departure":true}` {
		t.Fatalf("the departure run: %+v ok=%v err=%v", next, ok, err)
	}
	if departed, _ := s.DepartedRepos(ctx, "tools"); !reflect.DeepEqual(departed, []DepartedRepo{{Slug: "svc", Names: []string{"svc"}}}) {
		t.Errorf("DepartedRepos = %v", departed)
	}
	long := time.Now().Add(-time.Hour)
	if due, err := s.DepartureDue(ctx, "tools", long); err != nil || !due {
		t.Errorf("DepartureDue before the run: %v %v, want due", due, err)
	}
	finish(t, s, next, RunDone)
	if due, _ := s.DepartureDue(ctx, "tools", long); due {
		t.Error("DepartureDue after the done departure run")
	}
	if doc, ok, _ := s.DocumentedRun(ctx, "tools", "svc"); !ok || doc.ID != documented.ID {
		t.Errorf("DocumentedRun = %+v, want the reading run %d, never the departure", doc, documented.ID)
	}
	// The poller's last run and streak know no departure either.
	if last, streak, ok, _ := s.LastRun(ctx, "tools", "svc"); !ok || last.Trigger == TriggerDeparture || streak.Failed != 1 {
		t.Errorf("LastRun = %+v %+v, want the failed reading run, its failure counted", last, streak)
	}
	// The cycle's writes never land on the deactivated row.
	if err := s.MarkRepoReady(ctx, "tools", "svc", "main", "bbb"); err != nil {
		t.Fatal(err)
	}
	if row, _, _ := s.SourceRepoByName(ctx, "tools", "svc"); row.HeadCommit != "" || row.State != RepoPending {
		t.Errorf("a sync in flight wrote on the deactivated row: %+v", row)
	}
	if err := s.SaveManifests(ctx, row(t, s, "svc").RepoID, "bbb", manifest.Read{}); !errors.Is(err, ErrNoRepo) {
		t.Errorf("SaveManifests on a deactivated row: %v, want ErrNoRepo", err)
	}

	// Activated and deactivated again: the second departure run is dropped
	// by the activation while it waits.
	if err := s.ActivateSourceRepo(ctx, "tools", "svc"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeactivateSourceRepo(ctx, "tools", "svc"); err != nil {
		t.Fatal(err)
	}
	// Its head was forgotten and never synced back: the departure names
	// the commit last documented.
	if again, ok, _ := s.NextRun(ctx); !ok || again.Commit != documented.Commit {
		t.Errorf("second departure at %q, want the documented %q", again.Commit, documented.Commit)
	}
	if err := s.ActivateSourceRepo(ctx, "tools", "svc"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.NextRun(ctx); ok {
		t.Error("an activation left the queued departure run")
	}
}

// The listing never adds a deactivated repository back, nor takes the mark
// away; it still goes missing when Bitbucket drops it, and pending again
// when it comes back, deactivated all along.
func TestApplyListing_KeepsADeactivatedRepositoryOut(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if err := s.CreateSource(ctx, bitbucketSource("SHOP")); err != nil {
		t.Fatal(err)
	}
	both := []ListedRepo{{Slug: "ui", CloneURL: "https://forge.example.invalid/scm/shop/ui.git"},
		{Slug: "api", CloneURL: "https://forge.example.invalid/scm/shop/api.git"}}
	if _, err := s.ApplyListing(ctx, "SHOP", both); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeactivateSourceRepo(ctx, "SHOP", "api"); err != nil {
		t.Fatal(err)
	}
	if res, err := s.ApplyListing(ctx, "SHOP", both); err != nil || res.Added != 0 {
		t.Fatalf("listing again: %+v, %v", res, err)
	}
	row, _, _ := s.SourceRepoByName(ctx, "SHOP", "api")
	if row.Active() {
		t.Fatal("the listing activated a deactivated repository")
	}
	if res, _ := s.ApplyListing(ctx, "SHOP", both[:1]); res.Missing != 1 {
		t.Fatalf("dropped by Bitbucket: %+v, want it missing", res)
	}
	row, _, _ = s.SourceRepoByName(ctx, "SHOP", "api")
	if row.Active() || row.State != RepoMissing {
		t.Fatalf("after the drop: %+v", row)
	}
	if _, err := s.ApplyListing(ctx, "SHOP", both); err != nil {
		t.Fatal(err)
	}
	row, _, _ = s.SourceRepoByName(ctx, "SHOP", "api")
	if row.Active() || row.State != RepoPending {
		t.Fatalf("listed again: %+v", row)
	}
	jobs, _ := s.SyncJobs(ctx)
	if len(jobs) != 1 || jobs[0].Repo.Slug != "ui" {
		t.Fatalf("jobs = %+v, want ui alone", jobs)
	}
}

// PRRepos places each pull request by the repository its note was for.
func TestPRRepos(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	sourceRepo(t, s, "tools", "svc")
	sourceRepo(t, s, "tools", "web")
	ingest(t, s, noteFor("tools", "svc", 1))
	ingest(t, s, noteFor("tools", "web", 2))
	got, err := s.PRRepos(ctx, "tools", []string{"tools/svc#1", "tools/web#2", "tools/svc#9"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, map[string]string{"tools/svc#1": "svc", "tools/web#2": "web"}) {
		t.Fatalf("PRRepos = %v", got)
	}
}
