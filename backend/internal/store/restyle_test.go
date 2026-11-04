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

// epochs is the epochs tools/slug's page and its project's page stand at.
func epochs(s *Store, slug string) (repo, project int, err error) {
	err = s.db.QueryRowContext(context.Background(), `
		SELECT sr.restyle_epoch, p.restyle_epoch FROM source_repos sr JOIN projects p ON p.id = sr.project_id
		 WHERE sr.repo_id = `+repoByName, "tools", slug).Scan(&repo, &project)
	return repo, project, err
}

func restyleDue(t *testing.T, s *Store, slug string, epoch int, before time.Time) bool {
	t.Helper()
	due, err := s.RestyleDue(context.Background(), "tools", slug, Epochs{Repo: epoch, Project: epoch}, before)
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
// streaks and the documented run never see it, so the next reconcile
// starts from the plan of the run that read the code. The source page
// shows one while it is open, holding the repository's run; over, the
// reading's run again. The queue's recent list counts a restyle apart.
func TestRestyle_InvisibleToTheRunHistory(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	sourceRepo(t, s, "tools", "svc")
	read := finish(t, s, enqueue(t, s, "tools", "svc"), RunDone)
	failed := finish(t, s, restyleRun(t, s, "tools", "svc", 1), RunFailed)
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
	if err != nil || runs["svc"].Last.ID != read.ID || runs["svc"].Documented != read.Commit {
		t.Fatalf("SourceRuns = %+v err=%v, want the reading run %d", runs["svc"], err, read.ID)
	}
	recent, err := s.FinishedRuns(ctx, time.Now().Add(-time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range recent {
		switch f.ID {
		case read.ID:
			if !f.Latest || f.FailedInRow != 0 {
				t.Errorf("reading run in recent = latest %v, failed %d: a restyle after it counts", f.Latest, f.FailedInRow)
			}
		case failed.ID:
			if f.Latest || f.FailedInRow != 1 {
				t.Errorf("failed restyle in recent = latest %v, failed %d", f.Latest, f.FailedInRow)
			}
		}
	}
	open := restyleRun(t, s, "tools", "svc", 2)
	if runs, _ := s.SourceRuns(ctx, "tools"); runs["svc"].Last.ID != open.ID || runs["svc"].Documented != read.Commit {
		t.Fatalf("SourceRuns while a restyle is open = %+v, want it, the documented commit the reading's", runs["svc"])
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
	// A failure waits for the retry's time; RestyleTries end it. The
	// project's page at 2, the repository's page alone is due.
	if _, err := s.db.ExecContext(ctx, `UPDATE projects SET restyle_epoch = 2`); err != nil {
		t.Fatal(err)
	}
	finish(t, s, open, RunFailed)
	if restyleDue(t, s, "web", 2, now.Add(-time.Hour)) {
		t.Fatal("a fresh failure is due at once")
	}
	if !restyleDue(t, s, "web", 2, now.Add(time.Hour)) {
		t.Fatal("a failure past its wait is not due")
	}
	for range RestyleTries - 1 {
		finish(t, s, restyleRun(t, s, "tools", "web", 2), RunFailed)
	}
	if restyleDue(t, s, "web", 2, now.Add(time.Hour)) {
		t.Fatal("due past the cap")
	}
	if !restyleDue(t, s, "web", 3, now.Add(time.Hour)) {
		t.Fatal("the next epoch's restyle is held by the last epoch's failures")
	}
}

// The project page's failures count over the project, whichever
// repository's run made them, and only those that failed on its step: a
// page that keeps failing costs RestyleTries calls, never that many per
// repository, and a repository page's failure never spends the project's.
func TestRestyleState_ProjectFailuresOverTheProject(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	for _, slug := range []string{"a", "b", "c", "d", "e"} {
		sourceRepo(t, s, "tools", slug)
	}
	projectPage(t, s, "tools")
	if _, err := s.db.ExecContext(ctx, `UPDATE source_repos SET restyle_epoch = 1`); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Hour)
	state := func(slug string) RestyleState {
		t.Helper()
		st, err := s.RestyleState(ctx, "tools", slug, Epochs{Repo: 1, Project: 1}, later)
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	// A failure on a repository page spends nothing of the project's.
	r := restyleRun(t, s, "tools", "e", 1)
	finish(t, s, r, RunFailed)
	if !state("a").ProjectDue {
		t.Fatal("a repository page's failure spent the project page's tries")
	}
	for i, slug := range []string{"a", "b", "c", "d"}[:RestyleTries] {
		if !state(slug).ProjectDue {
			t.Fatalf("project page not due before failure %d", i+1)
		}
		r := restyleRun(t, s, "tools", slug, 1)
		r.Step = RestyleProjectStep
		finish(t, s, r, RunFailed)
	}
	if st := state("e"); st.ProjectDue || st.RepoDue {
		t.Fatalf("state after %d project failures over the project = %+v, want nothing due", RestyleTries, st)
	}
}

// A reading run's project step that had the whole page under today's rules
// puts the project page at the epoch, by the run: never back, never by a
// run that failed or is gone.
func TestMarkProjectRestyled(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	sourceRepo(t, s, "tools", "svc")
	sourceRepo(t, s, "tools", "web")
	projectPage(t, s, "tools")
	failed := finish(t, s, enqueue(t, s, "tools", "web"), RunFailed)
	for _, id := range []int64{failed.ID, 999} {
		if err := s.MarkProjectRestyled(ctx, id, 2); err != nil {
			t.Fatal(err)
		}
	}
	at2 := Epochs{Repo: 2, Project: 2}
	if st, _ := s.RestyleState(ctx, "tools", "svc", at2, time.Now()); !st.ProjectDue {
		t.Fatalf("state = %+v: marked by a failed or a missing run", st)
	}
	r := enqueue(t, s, "tools", "svc")
	for _, epoch := range []int{2, 1} {
		if err := s.MarkProjectRestyled(ctx, r.ID, epoch); err != nil {
			t.Fatal(err)
		}
	}
	st, err := s.RestyleState(ctx, "tools", "svc", at2, time.Now())
	if err != nil || st.ProjectDue {
		t.Fatalf("state = %+v err=%v, want the project page at 2", st, err)
	}
	if st, _ := s.RestyleState(ctx, "tools", "nope", at2, time.Now()); st != (RestyleState{}) {
		t.Fatalf("state of no repository = %+v", st)
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
	// One that skipped a part left that part's statements as they were.
	skipped := enqueue(t, s, "tools", "svc")
	skipped.Epoch, skipped.Skipped = 2, 1
	finish(t, s, skipped, RunDone)
	if repo, _, _ := epochs(s, "svc"); repo != 0 {
		t.Fatalf("epoch after a full run that skipped a part = %d, want 0", repo)
	}
	full := enqueue(t, s, "tools", "svc")
	full.Epoch = 2
	finish(t, s, full, RunDone)
	if repo, _, err := epochs(s, "svc"); err != nil || repo != 2 {
		t.Fatalf("epoch after the full run = %d err=%v, want 2", repo, err)
	}
	r := restyleRun(t, s, "tools", "web", 3)
	r.State = RunProject
	n, ok, err := s.ApplyRestyle(ctx, r, ScopeRepository, 3, Restyled{Revision: ptrRevision(baselineRevision("tools", "web", r.ID))})
	if err != nil || !ok || n != 1 {
		t.Fatalf("ApplyRestyle = %d ok=%v err=%v, want revision 1", n, ok, err)
	}
	if repo, project, _ := epochs(s, "web"); repo != 3 || project != 0 {
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
	if _, project, _ := epochs(s, "web"); project != 3 {
		t.Fatalf("project epoch = %d, want 3", project)
	}
	// Over, the run takes nothing more, the epoch included.
	finish(t, s, r, RunDone)
	if _, ok, err := s.ApplyRestyle(ctx, r, ScopeRepository, 9, Restyled{}); err != nil || ok {
		t.Fatalf("ApplyRestyle on a run over: ok=%v err=%v, want nothing written", ok, err)
	}
	if repo, _, _ := epochs(s, "web"); repo != 3 {
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
	if repo, project, err := epochs(s, "svc"); err != nil || repo != 0 || project != 0 {
		t.Fatalf("epochs after 0004 = %d %d err=%v, want 0 0", repo, project, err)
	}
}
