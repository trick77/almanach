package store

import (
	"context"
	"errors"
	"testing"
)

var tables = []string{"deliveries", "teams", "projects", "repos", "claims", "notes", "note_calls"}

func counts(t *testing.T, s *Store) map[string]int {
	t.Helper()
	out := map[string]int{}
	for _, tb := range tables {
		out[tb] = count(t, s, tb, "")
	}
	return out
}

// One Ingest lands the whole delivery: raw body, structure, both claim
// shapes, the note and its call.
func TestIngest_WritesEverything(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	n := noteFor("PRJ", "svc", 1)
	claims := []Claim{{ProjectKey: "PRJ"}, {ProjectKey: "PRJ", RepoSlug: "svc"}}
	ok, err := s.Ingest(ctx, deliveryFor(n), n, callFor(n), claims)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("first delivery not accepted")
	}
	want := map[string]int{"deliveries": 1, "teams": 1, "projects": 1, "repos": 1, "claims": 2, "notes": 1, "note_calls": 1}
	got := counts(t, s)
	for tb, w := range want {
		if got[tb] != w {
			t.Errorf("%s: %d rows, want %d", tb, got[tb], w)
		}
	}
	const prj, svc = "(SELECT id FROM projects WHERE key = 'PRJ')", "(SELECT id FROM repos WHERE slug = 'svc')"
	if n := count(t, s, "claims", "team = 'alpha' AND project_key = 'PRJ' AND repo_slug IS NULL"); n != 1 {
		t.Errorf("project claim rows = %d, want 1", n)
	}
	if n := count(t, s, "claims", "team = 'alpha' AND project_key = 'PRJ' AND repo_slug = 'svc'"); n != 1 {
		t.Errorf("repo claim rows = %d, want 1", n)
	}
	if n := count(t, s, "projects", "key = 'PRJ' AND team = 'alpha'"); n != 1 {
		t.Errorf("project row = %d, want 1", n)
	}
	if n := count(t, s, "repos", "project_id = "+prj+" AND slug = 'svc' AND team = 'alpha'"); n != 1 {
		t.Errorf("repo row = %d, want 1", n)
	}
	if n := count(t, s, "notes", "repo_id = "+svc+" AND pr_id = 1 AND delivery_id = 'd-PRJ-svc-1' AND consolidated_at IS NULL"); n != 1 {
		t.Errorf("note row = %d, want 1", n)
	}
	if n := count(t, s, "note_calls", "delivery_id = 'd-PRJ-svc-1' AND pr_id = 1 AND rounds = 2 AND warnings = '[\"w1\"]'"); n != 1 {
		t.Errorf("note_calls row = %d, want 1", n)
	}
}

// A project belongs to the first team whose note named it, in any case
// of the key: another team's note is refused inside the transaction with
// nothing written, unless the delivery is shared, which stores it and
// leaves the owner as it was; a re-delivery dedups before the owner is
// looked at.
func TestIngest_ProjectOwnedByFirstTeam(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	first := noteFor("PRJ", "svc", 1)
	ingest(t, s, first)
	if team, err := s.ProjectTeam(ctx, "prj"); err != nil || team != "alpha" {
		t.Fatalf("ProjectTeam(prj) = %q, %v; want alpha in any case", team, err)
	}
	other := noteFor("prj", "svc", 2)
	other.Team = "beta"
	var owned *OwnedError
	if ok, err := s.Ingest(ctx, deliveryFor(other), other, callFor(other), nil); ok || !errors.As(err, &owned) || owned.Owner != "alpha" {
		t.Fatalf("another team's note = %v, %v; want *OwnedError{alpha}", ok, err)
	}
	if n := count(t, s, "deliveries", ""); n != 1 {
		t.Errorf("deliveries after the refusal = %d, want 1: nothing written", n)
	}
	if n := count(t, s, "teams", "slug = 'beta'"); n != 0 {
		t.Errorf("the refused team was registered")
	}
	d := deliveryFor(other)
	d.Shared = true
	if ok, err := s.Ingest(ctx, d, other, callFor(other), nil); err != nil || !ok {
		t.Fatalf("shared note = %v, %v; want stored", ok, err)
	}
	if team, err := s.ProjectTeam(ctx, "PRJ"); err != nil || team != "alpha" {
		t.Errorf("owner after a shared note = %q, %v; want still alpha", team, err)
	}
	// Re-delivered later by its own team, the project's owner meanwhile
	// no concern: the delivery id dedups first.
	d.Shared = false
	if ok, err := s.Ingest(ctx, d, other, callFor(other), nil); err != nil || ok {
		t.Errorf("re-delivery = %v, %v; want deduped, no refusal", ok, err)
	}
	// A project a source registered, no note yet, is nobody's: the first
	// note's team takes it.
	if err := s.CreateSource(ctx, staticSource("NEW")); err != nil {
		t.Fatal(err)
	}
	if team, err := s.ProjectTeam(ctx, "NEW"); err != nil || team != "" {
		t.Errorf("ProjectTeam of a sourced project = %q, %v; want none", team, err)
	}
	fresh := noteFor("NEW", "lib", 1)
	fresh.Team = "beta"
	ingest(t, s, fresh)
	if team, err := s.ProjectTeam(ctx, "NEW"); err != nil || team != "beta" {
		t.Errorf("ProjectTeam after the first note = %q, %v; want beta", team, err)
	}
}

// A retry of the same delivery id is refused and writes nothing, not even
// the changed title.
func TestIngest_RetryIsNoop(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	n := noteFor("PRJ", "svc", 1)
	d := deliveryFor(n)
	claims := []Claim{{ProjectKey: "PRJ"}, {ProjectKey: "PRJ", RepoSlug: "svc"}}
	if ok, err := s.Ingest(ctx, d, n, callFor(n), claims); err != nil || !ok {
		t.Fatalf("first ingest: ok=%v err=%v", ok, err)
	}
	before := counts(t, s)

	retry := n
	retry.PRTitle = "changed on retry"
	retry.PRID = 2
	ok, err := s.Ingest(ctx, d, retry, callFor(retry), []Claim{{ProjectKey: "OTHER"}})
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("retry accepted, want refused")
	}
	after := counts(t, s)
	for _, tb := range tables {
		if before[tb] != after[tb] {
			t.Errorf("%s: %d rows after retry, want %d", tb, after[tb], before[tb])
		}
	}
	var title string
	if err := s.db.QueryRowContext(ctx, `SELECT pr_title FROM notes WHERE pr_id = 1`).Scan(&title); err != nil {
		t.Fatal(err)
	}
	if title != "PR 1" {
		t.Errorf("title after retry = %q, want the first delivery's", title)
	}
}

// Without a call there is no note_calls row; the note still lands.
func TestIngest_NilCallWritesNoCallRow(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	n := noteFor("PRJ", "svc", 1)
	ok, err := s.Ingest(ctx, deliveryFor(n), n, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("not accepted")
	}
	if got := count(t, s, "notes", ""); got != 1 {
		t.Errorf("notes = %d, want 1", got)
	}
	if got := count(t, s, "note_calls", ""); got != 0 {
		t.Errorf("note_calls = %d, want 0", got)
	}
	if got := count(t, s, "claims", ""); got != 0 {
		t.Errorf("claims = %d, want 0", got)
	}
}

// A second note for the same PR replaces the first and queues it again.
func TestIngest_SamePRReplacesAndRequeues(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	n := noteFor("PRJ", "svc", 1)
	ingest(t, s, n)
	if _, err := s.DeferFold(ctx, "PRJ", "svc", 1, deliveryFor(n).ID); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkConsolidated(ctx, "PRJ", "svc", 1, deliveryFor(n).ID); err != nil {
		t.Fatal(err)
	}
	again := n
	again.PRTitle = "merged again"
	d := deliveryFor(n)
	d.ID = "d-second"
	if ok, err := s.Ingest(ctx, d, again, callFor(again), nil); err != nil || !ok {
		t.Fatalf("second ingest: ok=%v err=%v", ok, err)
	}
	if got := count(t, s, "notes", ""); got != 1 {
		t.Errorf("notes = %d, want 1", got)
	}
	if got := count(t, s, "notes", "pr_title = 'merged again' AND delivery_id = 'd-second' AND consolidated_at IS NULL AND fold_attempts = 0"); got != 1 {
		t.Errorf("replaced, requeued note rows with the attempts reset = %d, want 1", got)
	}
	if got := count(t, s, "note_calls", ""); got != 2 {
		t.Errorf("note_calls = %d, want 2", got)
	}
}
