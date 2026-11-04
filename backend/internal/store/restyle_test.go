package store

import (
	"context"
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trick77/ship/kit/migrate"
)

// restyleRun queues and returns a restyle run of key/slug for epoch.
func restyleRun(t *testing.T, s *Store, key, slug string, epoch int) BaselineRun {
	t.Helper()
	id, err := s.EnqueueRestyle(context.Background(), key, slug, "d0c", epoch)
	if err != nil {
		t.Fatalf("EnqueueRestyle %s/%s: %v", key, slug, err)
	}
	r, ok, err := s.RunByID(context.Background(), id)
	if err != nil || !ok {
		t.Fatalf("RunByID %d: ok=%v err=%v", id, ok, err)
	}
	return r
}

func restyleDue(t *testing.T, s *Store, slug string, epoch int, before time.Time) bool {
	t.Helper()
	due, err := s.RestyleDue(context.Background(), "tools", slug, epoch, 2, before)
	if err != nil {
		t.Fatal(err)
	}
	return due
}

// A restyle is queued at the page's commit with its epoch and the plan
// that says what it is; one open run per repository holds for it too.
func TestEnqueueRestyle(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	sourceRepo(t, s, "tools", "svc")
	r := restyleRun(t, s, "tools", "svc", 3)
	if r.Mode != RunRestyle || r.Trigger != TriggerEpoch || r.Commit != "d0c" || r.BaseCommit != "d0c" || r.Epoch != 3 ||
		string(r.Plan) != `{"restyle":{}}` || r.State != RunQueued {
		t.Fatalf("restyle run = %+v", r)
	}
	if _, err := s.EnqueueRestyle(ctx, "tools", "svc", "d0c", 3); !errors.Is(err, ErrExists) {
		t.Fatalf("second open run: err = %v, want ErrExists", err)
	}
	if _, err := s.EnqueueRestyle(ctx, "tools", "nope", "d0c", 3); !errors.Is(err, ErrNoRepo) {
		t.Fatalf("unknown repository: err = %v, want ErrNoRepo", err)
	}
}

// A restyle reads nothing of the repository: the poller's last run, its
// streaks, the documented run and the source page never see it, so the
// next reconcile starts from the plan of the run that read the code.
func TestRestyle_InvisibleToTheRunHistory(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	sourceRepo(t, s, "tools", "svc")
	read := finish(t, s, enqueue(t, s, "tools", "svc"), RunDone)
	finish(t, s, restyleRun(t, s, "tools", "svc", 1), RunFailed)
	finish(t, s, restyleRun(t, s, "tools", "svc", 1), RunDone)
	last, streak, ok, err := s.LastRun(ctx, "tools", "svc")
	if err != nil || !ok || last.ID != read.ID || streak.Failed != 0 {
		t.Fatalf("LastRun = %d %+v ok=%v err=%v, want the full run %d, no failure", last.ID, streak, ok, err, read.ID)
	}
	doc, ok, err := s.DocumentedRun(ctx, "tools", "svc")
	if err != nil || !ok || doc.ID != read.ID {
		t.Fatalf("DocumentedRun = %d ok=%v err=%v, want %d", doc.ID, ok, err, read.ID)
	}
	runs, err := s.SourceRuns(ctx, "tools")
	if err != nil || runs["svc"].Last.ID != read.ID {
		t.Fatalf("SourceRuns last = %d err=%v, want %d", runs["svc"].Last.ID, err, read.ID)
	}
}

// Due while the repository's page is below the epoch, or its project's
// page where one is written and the project has more than one repository;
// held while a restyle is open anywhere; a failed one waits for the
// retry's time and stops at the cap, per epoch.
func TestRestyleDue(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	sourceRepo(t, s, "tools", "svc")
	now := time.Now()
	if !restyleDue(t, s, "svc", 1, now) {
		t.Fatal("a page at epoch 0 is not due for epoch 1")
	}
	if restyleDue(t, s, "svc", 0, now) {
		t.Fatal("a page at the epoch is due")
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE source_repos SET restyle_epoch = 1`); err != nil {
		t.Fatal(err)
	}
	if restyleDue(t, s, "svc", 1, now) {
		t.Fatal("restyled page due again")
	}
	// The project's page: due once written and with a second repository.
	projectPage(t, s, "tools")
	if restyleDue(t, s, "svc", 1, now) {
		t.Fatal("a project of one is due its project page")
	}
	sourceRepo(t, s, "tools", "web")
	if !restyleDue(t, s, "svc", 1, now) {
		t.Fatal("the project page below the epoch is not due")
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE projects SET restyle_epoch = 1`); err != nil {
		t.Fatal(err)
	}
	if restyleDue(t, s, "svc", 1, now) {
		t.Fatal("restyled project due again")
	}
	// One open restyle anywhere holds every other.
	open := restyleRun(t, s, "tools", "web", 2)
	if restyleDue(t, s, "svc", 2, now) {
		t.Fatal("due while another restyle is open")
	}
	// A failure waits for the retry's time; the cap (2 here) ends it.
	finish(t, s, open, RunFailed)
	if restyleDue(t, s, "web", 2, now.Add(-time.Hour)) {
		t.Fatal("a fresh failure is due at once")
	}
	if !restyleDue(t, s, "web", 2, now.Add(time.Hour)) {
		t.Fatal("a failure past its wait is not due")
	}
	finish(t, s, restyleRun(t, s, "tools", "web", 2), RunFailed)
	if restyleDue(t, s, "web", 2, now.Add(time.Hour)) {
		t.Fatal("due past the cap")
	}
	if !restyleDue(t, s, "web", 3, now.Add(time.Hour)) {
		t.Fatal("the next epoch's restyle is held by the last epoch's failures")
	}
}

// A step writes the run, the page's change and its epoch together: a
// revision, or the layout in place on the current revision. A full run
// done under an epoch puts its repository's page there; a departure and a
// reconcile do not.
func TestApplyRestyle_AndTheFullRunsEpoch(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	sourceRepo(t, s, "tools", "svc")
	sourceRepo(t, s, "tools", "web")
	full := enqueue(t, s, "tools", "svc")
	full.Epoch = 2
	finish(t, s, full, RunDone)
	if repo, _, err := s.RestyleEpochs(ctx, "tools", "svc"); err != nil || repo != 2 {
		t.Fatalf("epoch after the full run = %d err=%v, want 2", repo, err)
	}
	r := restyleRun(t, s, "tools", "web", 3)
	r.State = RunProject
	n, ok, err := s.ApplyRestyle(ctx, r, ScopeRepository, 3, Restyled{Revision: ptrRevision(baselineRevision("tools", "web", r.ID))})
	if err != nil || !ok || n != 1 {
		t.Fatalf("ApplyRestyle = %d ok=%v err=%v, want revision 1", n, ok, err)
	}
	if repo, project, _ := s.RestyleEpochs(ctx, "tools", "web"); repo != 3 || project != 0 {
		t.Fatalf("epochs = %d %d, want the repository's 3 alone", repo, project)
	}
	page, _, _ := s.CurrentPage(ctx, ScopeRepository, "tools", "web")
	moved := page.Statements
	moved[0].Area = "Kern"
	if _, ok, err := s.ApplyRestyle(ctx, r, ScopeProject, 3, Restyled{ArrangeID: page.RevisionID, Arranged: moved}); err != nil || !ok {
		t.Fatalf("ApplyRestyle layout: ok=%v err=%v", ok, err)
	}
	if page, _, _ = s.CurrentPage(ctx, ScopeRepository, "tools", "web"); page.Revision != 1 || page.Statements[0].Area != "Kern" {
		t.Fatalf("page after the layout = revision %d, area %q", page.Revision, page.Statements[0].Area)
	}
	if _, project, _ := s.RestyleEpochs(ctx, "tools", "web"); project != 3 {
		t.Fatalf("project epoch = %d, want 3", project)
	}
	// Over, the run takes nothing more, the epoch included.
	finish(t, s, r, RunDone)
	if _, ok, err := s.ApplyRestyle(ctx, r, ScopeRepository, 9, Restyled{}); err != nil || ok {
		t.Fatalf("ApplyRestyle on a run over: ok=%v err=%v, want nothing written", ok, err)
	}
	if repo, _, _ := s.RestyleEpochs(ctx, "tools", "web"); repo != 3 {
		t.Fatalf("epoch = %d after a write refused, want 3", repo)
	}
}

func ptrRevision(r NewRevision) *NewRevision { return &r }

// 0004 rebuilds the runs and the ledger: every row comes over, a run's
// epoch 0, the run id sequence goes on where it stood, every page starts
// below the first epoch.
func TestMigration0004_CarriesRunsAndCalls(t *testing.T) {
	s := emptyStore(t)
	ctx := context.Background()
	before := fstest.MapFS{}
	for _, name := range []string{"0001_init.sql", "0002_llm_calls_project_map.sql", "0003_repo_deactivation.sql"} {
		b, err := fs.ReadFile(migrations(), name)
		if err != nil {
			t.Fatal(err)
		}
		before[name] = &fstest.MapFile{Data: b}
	}
	if _, err := migrate.Up(ctx, s.db, before, s.migrateOptions()); err != nil {
		t.Fatal(err)
	}
	sourceRepo(t, s, "tools", "svc")
	rid, _, err := repoID(ctx, s.db, "tools", "svc")
	if err != nil {
		t.Fatal(err)
	}
	// Queued as before 0004: a run's row read back needs its epoch.
	old, err := s.EnqueueRun(ctx, "tools", "svc", RunFull, TriggerManual, "", "c0ffee")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE baseline_runs SET state = 'done', finished_at = ? WHERE id = ?`, nowStamp(), old); err != nil {
		t.Fatal(err)
	}
	// The newest run removed: the sequence, not the rows, keeps its id.
	gone, err := s.EnqueueRun(ctx, "tools", "svc", RunFull, TriggerManual, "", "c0ffee")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM baseline_runs WHERE id = ?`, gone); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO llm_calls (repo_id, source_kind, source_commit, purpose, baseline_run_id, outcome, created_at)
		VALUES (?, 'baseline', 'c0ffee', 'project_map', ?, 'ok', ?)`, rid, old, nowStamp()); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate 0004: %v", err)
	}
	got, ok, err := s.RunByID(ctx, old)
	if err != nil || !ok || got.State != RunDone || got.Mode != RunFull || got.Epoch != 0 {
		t.Fatalf("run after 0004 = %+v ok=%v err=%v", got, ok, err)
	}
	if n := count(t, s, "llm_calls", "purpose = 'project_map'"); n != 1 {
		t.Fatalf("ledger rows after 0004 = %d, want 1", n)
	}
	next := enqueue(t, s, "tools", "svc")
	if next.ID <= gone {
		t.Fatalf("run id after 0004 = %d, reissues %d", next.ID, gone)
	}
	if repo, project, err := s.RestyleEpochs(ctx, "tools", "svc"); err != nil || repo != 0 || project != 0 {
		t.Fatalf("epochs after 0004 = %d %d err=%v, want 0 0", repo, project, err)
	}
}
