package store

import (
	"context"
	"testing"
	"time"
)

func repoPage(t *testing.T, s *Store, key, slug string) {
	t.Helper()
	if _, err := s.InsertRevision(context.Background(), NewRevision{Scope: ScopeRepository, ProjectKey: key, RepoSlug: slug,
		Summary: "Ein Dienst.", Statements: []Statement{}, SourceKind: FromBaseline, SourceCommit: "c0ffee"}); err != nil {
		t.Fatal(err)
	}
}

// A written repository page below the epoch is stale, with its restyles and
// its place in the poller's order (key, slug), every stale repository
// before it counted; one at the epoch, unwritten or no source is not.
func TestRepoStale(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	at := Epochs{Repo: 3, Project: 3}
	for _, slug := range []string{"a", "b", "c"} {
		sourceRepo(t, s, "tools", slug)
	}
	repoPage(t, s, "tools", "a")
	repoPage(t, s, "tools", "c")
	if _, ok, err := s.RepoStale(ctx, "tools", "b", at); err != nil || ok {
		t.Fatalf("unwritten page: ok=%v err=%v, want not stale", ok, err)
	}
	st, ok, err := s.RepoStale(ctx, "tools", "c", at)
	if err != nil || !ok || st.Epoch != 0 || st.Failed != 0 || st.Run != "" || st.Rank != 2 {
		t.Fatalf("RepoStale(c) = %+v ok=%v err=%v, want epoch 0, rank 2 (a before it, b unwritten)", st, ok, err)
	}
	// Restyles: failed ones counted at the scope's epoch, an open one's state.
	finish(t, s, restyleRun(t, s, "tools", "c", 3), RunFailed)
	finish(t, s, restyleRun(t, s, "tools", "c", 2), RunFailed)
	restyleRun(t, s, "tools", "c", 3)
	if st, _, _ := s.RepoStale(ctx, "tools", "c", at); st.Failed != 1 || st.Run != RunQueued {
		t.Fatalf("RepoStale(c) = %+v, want 1 failure at 3, a queued run", st)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE source_repos SET restyle_epoch = 3 WHERE repo_id = `+repoByName, "tools", "a"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.RepoStale(ctx, "tools", "a", at); ok {
		t.Fatal("a page at the epoch is stale")
	}
	if st, _, _ := s.RepoStale(ctx, "tools", "c", at); st.Rank != 1 {
		t.Fatalf("rank = %d, want 1 once a is restyled", st.Rank)
	}
	if _, ok, err := s.RepoStale(ctx, "tools", "nope", at); ok || err != nil {
		t.Fatalf("no repository: ok=%v err=%v", ok, err)
	}
}

// A project page is stale only written and in a project of more than one;
// its failures are its step's over the project; its rank is its first
// repository's, which the poller comes to first; a project of another key
// sorts by key.
func TestProjectStale(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	at := Epochs{Repo: 3, Project: 3}
	sourceRepo(t, s, "alpha", "x")
	repoPage(t, s, "alpha", "x")
	sourceRepo(t, s, "tools", "b")
	if _, ok, _ := s.ProjectStale(ctx, "tools", at); ok {
		t.Fatal("unwritten project page is stale")
	}
	projectPage(t, s, "tools")
	if _, ok, _ := s.ProjectStale(ctx, "tools", at); ok {
		t.Fatal("a project of one is stale")
	}
	sourceRepo(t, s, "tools", "a")
	st, ok, err := s.ProjectStale(ctx, "tools", at)
	if err != nil || !ok || st.Rank != 2 {
		t.Fatalf("ProjectStale = %+v ok=%v err=%v, want rank 2 (alpha/x first)", st, ok, err)
	}
	r := restyleRun(t, s, "tools", "b", 3)
	r.Step = RestyleProjectStep
	finish(t, s, r, RunFailed)
	finish(t, s, restyleRun(t, s, "tools", "a", 3), RunFailed)
	restyleRun(t, s, "tools", "a", 3)
	if st, _, _ := s.ProjectStale(ctx, "tools", at); st.Failed != 1 || st.Run != RunQueued {
		t.Fatalf("ProjectStale = %+v, want the project step's 1 failure, a's queued run", st)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE projects SET restyle_epoch = 3`); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.ProjectStale(ctx, "tools", at); ok {
		t.Fatal("a project page at the epoch is stale")
	}
}

// A restyle queued by hand reads what is due past the tries a page spent.
func TestRestyleDue_PastTheTries(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	sourceRepo(t, s, "tools", "svc")
	for range RestyleTries {
		finish(t, s, restyleRun(t, s, "tools", "svc", 1), RunFailed)
	}
	at := Epochs{Repo: 1, Project: 1}
	if st, _ := s.RestyleState(ctx, "tools", "svc", at, time.Now().Add(time.Hour)); st.RepoDue {
		t.Fatal("due past the tries")
	}
	if st, err := s.RestyleDue(ctx, "tools", "svc", at); err != nil || !st.RepoDue {
		t.Fatalf("RestyleDue = %+v err=%v, want the page due", st, err)
	}
}
