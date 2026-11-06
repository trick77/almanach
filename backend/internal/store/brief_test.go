package store

import (
	"context"
	"errors"
	"testing"
)

// A brief's life: requested, written with its push due, pushed; a second
// click while one waits keeps its place; a failure keeps the brief before.
func TestBrief_RequestWritePush(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	sourceRepo(t, s, "tools", "svc")
	sourceRepo(t, s, "tools", "web")
	svc, web := row(t, s, "svc").RepoID, row(t, s, "web").RepoID

	if _, ok, err := s.NextBrief(ctx); err != nil || ok {
		t.Fatalf("NextBrief on nothing = %v, %v", ok, err)
	}
	if err := s.RequestBrief(ctx, 999); !errors.Is(err, ErrNoRepo) {
		t.Fatalf("RequestBrief(unknown) = %v, want ErrNoRepo", err)
	}
	if err := s.RequestBrief(ctx, svc); err != nil {
		t.Fatal(err)
	}
	if err := s.RequestBrief(ctx, web); err != nil {
		t.Fatal(err)
	}
	if n, err := s.DeferBrief(ctx, svc); err != nil || n != 1 {
		t.Fatalf("DeferBrief = %d, %v", n, err)
	}
	// Clicked again while waiting: the place in line and the count stay.
	if err := s.RequestBrief(ctx, svc); err != nil {
		t.Fatal(err)
	}
	j, ok, err := s.NextBrief(ctx)
	if err != nil || !ok || j.RepoID != svc || !j.Write || j.ProjectKey != "tools" || j.Slug != "svc" {
		t.Fatalf("NextBrief = %+v, %v, %v; want svc's request", j, ok, err)
	}
	if n, _ := s.DeferBrief(ctx, svc); n != 2 {
		t.Fatalf("attempts after a second click and a defer = %d, want 2: the click keeps the count", n)
	}

	if j.RequestSeq != 2 {
		t.Fatalf("request seq = %d, want 2 after two clicks", j.RequestSeq)
	}
	if err := s.WriteBrief(ctx, svc, j.RequestSeq, "## Projekt\n\nText.\n", 4); err != nil {
		t.Fatal(err)
	}
	b, ok, err := s.RepoBrief(ctx, svc)
	if err != nil || !ok || b.Markdown != "## Projekt\n\nText.\n" || b.PageRevision != 4 || b.PushState != PushDue ||
		b.RequestedAt != nil || b.Attempts != 0 || b.WrittenAt == nil {
		t.Fatalf("after WriteBrief = %+v, %v, %v", b, ok, err)
	}
	// svc's push first, though web's request is older: it costs no call.
	j, ok, _ = s.NextBrief(ctx)
	if !ok || j.RepoID != svc || j.Write || j.Markdown == "" {
		t.Fatalf("NextBrief = %+v, want svc's push", j)
	}
	if err := s.PushedBrief(ctx, svc, PushFailed, "", "git push: exit status 128: 403"); err != nil {
		t.Fatal(err)
	}
	j, _, _ = s.NextBrief(ctx)
	if j.RepoID != web || !j.Write {
		t.Fatalf("NextBrief = %+v, want web's request", j)
	}
	if err := s.FailBrief(ctx, web, j.RequestSeq, "answer off the schema"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.NextBrief(ctx); ok {
		t.Fatal("work left after the push ended")
	}

	all, err := s.Briefs(ctx, "tools")
	if err != nil || len(all) != 2 {
		t.Fatalf("Briefs = %v, %v", all, err)
	}
	if b := all[svc]; b.Markdown != "" || !b.HasMarkdown || b.PushState != PushFailed || b.PushError == "" {
		t.Errorf("svc in the listing = %+v", b)
	}
	if b := all[web]; b.HasMarkdown || b.Failed != "answer off the schema" || b.FailedAt == nil {
		t.Errorf("web in the listing = %+v", b)
	}

	// Asked again: the failure forgotten, the brief before kept.
	if err := s.RequestBrief(ctx, svc); err != nil {
		t.Fatal(err)
	}
	if err := s.FailBrief(ctx, svc, 3, "x"); err != nil {
		t.Fatal(err)
	}
	if b, _, _ := s.RepoBrief(ctx, svc); b.Markdown == "" || b.PushState != PushFailed || b.Failed != "x" {
		t.Errorf("a failed request lost the brief before: %+v", b)
	}
	if err := s.PushedBrief(ctx, svc, PushPushed, "abc", "ignored"); err != nil {
		t.Fatal(err)
	}
	if b, _, _ := s.RepoBrief(ctx, svc); b.PushState != PushPushed || b.PushedCommit != "abc" || b.PushError != "" {
		t.Errorf("after a push = %+v", b)
	}
	// Written anew and refused: the commit pushed last stays, the lease of
	// the next push.
	if err := s.WriteBrief(ctx, svc, 3, "# x\n", 5); err != nil {
		t.Fatal(err)
	}
	if j, _, _ := s.NextBrief(ctx); j.PushedCommit != "abc" {
		t.Errorf("due push = %+v, want the commit pushed last", j)
	}
	if err := s.PushedBrief(ctx, svc, PushFailed, "", "refused"); err != nil {
		t.Fatal(err)
	}
	if b, _, _ := s.RepoBrief(ctx, svc); b.PushedCommit != "abc" {
		t.Errorf("after a refused push = %+v, want the commit pushed last kept", b)
	}
	// A branch reset to the default branch is recorded as the commit last
	// pushed, with the state unchanged.
	if err := s.PushedBrief(ctx, svc, PushUnchanged, "def", ""); err != nil {
		t.Fatal(err)
	}
	if b, _, _ := s.RepoBrief(ctx, svc); b.PushState != PushUnchanged || b.PushedCommit != "def" {
		t.Errorf("after a reset = %+v", b)
	}
}

// A request that comes while the worker writes or fails the one it took
// stays, to be written anew; the one taken never ends it.
func TestBrief_RequestDuringTheCall(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	sourceRepo(t, s, "tools", "svc")
	svc := row(t, s, "svc").RepoID
	if err := s.RequestBrief(ctx, svc); err != nil {
		t.Fatal(err)
	}
	taken, _, _ := s.NextBrief(ctx)
	if err := s.RequestBrief(ctx, svc); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteBrief(ctx, svc, taken.RequestSeq, "# x\n", 1); err != nil {
		t.Fatal(err)
	}
	if b, _, _ := s.RepoBrief(ctx, svc); b.RequestedAt == nil || b.Markdown != "# x\n" {
		t.Fatalf("written while asked again = %+v, want the new request waiting", b)
	}
	if err := s.FailBrief(ctx, svc, taken.RequestSeq, "late failure"); err != nil {
		t.Fatal(err)
	}
	if b, _, _ := s.RepoBrief(ctx, svc); b.RequestedAt == nil || b.FailedAt != nil {
		t.Errorf("failed while asked again = %+v, want the new request waiting, unfailed", b)
	}
	if _, err := s.DeferBrief(ctx, 999); !errors.Is(err, ErrNoRepo) {
		t.Errorf("DeferBrief of a row that went = %v, want ErrNoRepo", err)
	}
}

// A deactivated repository's brief waits; a removed one's goes with it.
func TestBrief_SourceOnly(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	sourceRepo(t, s, "tools", "svc")
	svc := row(t, s, "svc").RepoID
	if err := s.RequestBrief(ctx, svc); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeactivateSourceRepo(ctx, "tools", "svc"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.NextBrief(ctx); ok {
		t.Error("a deactivated repository's brief is the worker's")
	}
	if ok, err := s.DeleteSourceRepo(ctx, "tools", "svc"); err != nil || !ok {
		t.Fatalf("DeleteSourceRepo = %v, %v", ok, err)
	}
	if n := count(t, s, "repo_briefs", "1=1"); n != 0 {
		t.Errorf("repo_briefs rows after the removal = %d", n)
	}
}

// A brief's call is on the ledger with its own purpose and source.
func TestBrief_LedgerPurpose(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if _, err := s.InsertLLMCall(ctx, LLMCall{ProjectKey: "tools", RepoSlug: "svc", SourceKind: FromPage, Purpose: PurposeBrief, Outcome: "ok"}); err != nil {
		t.Fatal(err)
	}
	// A page source is a brief's alone.
	if _, err := s.InsertLLMCall(ctx, LLMCall{ProjectKey: "tools", RepoSlug: "svc", SourceKind: FromPage, Purpose: PurposeMerge, Outcome: "ok"}); err == nil {
		t.Error("a merge call with a page source was stored")
	}
}
