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
	ingested, err := s.Ingest(ctx, deliveryFor(n), n, callFor(n), claims)
	if err != nil {
		t.Fatal(err)
	}
	if !ingested.Accepted {
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

// ownerOf is the team projects.team holds for the project key names, by
// any name in any case; "" for none.
func ownerOf(t *testing.T, s *Store, key string) string {
	t.Helper()
	ctx := context.Background()
	pid, ok, err := projectIDAnyCase(ctx, s.db, key)
	if err != nil || !ok {
		t.Fatalf("project %s: %v, %v", key, ok, err)
	}
	var team string
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(team, '') FROM projects WHERE id = ?`, pid).Scan(&team); err != nil {
		t.Fatal(err)
	}
	return team
}

// A project belongs to the first team whose note named it, in any case
// of the key: another team's note is refused inside the transaction with
// nothing written; under Grace it is stored, its owner reported and kept;
// a re-delivery dedups before the owner is looked at.
func TestIngest_ProjectOwnedByFirstTeam(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	first := noteFor("PRJ", "svc", 1)
	ingest(t, s, first)
	if team := ownerOf(t, s, "prj"); team != "alpha" {
		t.Fatalf("owner of prj = %q, want alpha in any case", team)
	}
	other := noteFor("prj", "svc", 2)
	other.Team = "beta"
	var owned *OwnedError
	if got, err := s.Ingest(ctx, deliveryFor(other), other, callFor(other), nil); got.Accepted || !errors.As(err, &owned) || owned.Owner != "alpha" {
		t.Fatalf("another team's note = %+v, %v; want *OwnedError{alpha}", got, err)
	}
	if n := count(t, s, "deliveries", ""); n != 1 {
		t.Errorf("deliveries after the refusal = %d, want 1: nothing written", n)
	}
	if n := count(t, s, "teams", "slug = 'beta'"); n != 0 {
		t.Errorf("the refused team was registered")
	}
	d := deliveryFor(other)
	d.Grace = true
	if got, err := s.Ingest(ctx, d, other, callFor(other), nil); err != nil || !got.Accepted || got.Owner != "alpha" {
		t.Fatalf("note under grace = %+v, %v; want stored, owner alpha reported", got, err)
	}
	if team := ownerOf(t, s, "PRJ"); team != "alpha" {
		t.Errorf("owner after a note under grace = %q, want still alpha", team)
	}
	// The owner's own note reports none.
	own := noteFor("PRJ", "svc", 3)
	if got, err := s.Ingest(ctx, deliveryFor(own), own, callFor(own), nil); err != nil || !got.Accepted || got.Owner != "" {
		t.Errorf("owner's note = %+v, %v; want accepted, no owner reported", got, err)
	}
	// Re-delivered later without grace: the delivery id dedups first.
	d.Grace = false
	if got, err := s.Ingest(ctx, d, other, callFor(other), nil); err != nil || got.Accepted {
		t.Errorf("re-delivery = %+v, %v; want deduped, no refusal", got, err)
	}
	// A project a source registered, no note yet, is nobody's: the first
	// note's team takes it.
	if err := s.CreateSource(ctx, staticSource("NEW")); err != nil {
		t.Fatal(err)
	}
	if team := ownerOf(t, s, "NEW"); team != "" {
		t.Errorf("owner of a sourced project = %q, want none", team)
	}
	fresh := noteFor("NEW", "lib", 1)
	fresh.Team = "beta"
	if got, err := s.Ingest(ctx, deliveryFor(fresh), fresh, callFor(fresh), nil); err != nil || !got.Accepted || got.Owner != "" {
		t.Errorf("first note of a sourced project = %+v, %v; want accepted, no owner reported", got, err)
	}
	if team := ownerOf(t, s, "NEW"); team != "beta" {
		t.Errorf("owner after the first note = %q, want beta", team)
	}
}

// A listing lets a team into another team's project by any name the
// project had, in any case, so a rename keeps it either way; it never
// claims a project no note has claimed yet. A key no project had lists
// nothing.
func TestIngest_ListedSharesNeverClaims(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if err := s.CreateSource(ctx, staticSource("OLD")); err != nil {
		t.Fatal(err)
	}
	listed := noteFor("old", "svc", 1)
	listed.Team = "beta"
	d := deliveryFor(listed)
	d.Listed = []string{"nowhere", "OLD"}
	if got, err := s.Ingest(ctx, d, listed, callFor(listed), nil); err != nil || !got.Accepted || got.Owner != "" {
		t.Fatalf("listed note = %+v, %v; want accepted", got, err)
	}
	if team := ownerOf(t, s, "OLD"); team != "" {
		t.Errorf("owner after a listed note = %q, want none: a listing never claims", team)
	}
	ingest(t, s, noteFor("OLD", "svc", 2))
	if team := ownerOf(t, s, "OLD"); team != "alpha" {
		t.Fatalf("owner after alpha's note = %q, want alpha", team)
	}
	if _, err := s.RenameProject(ctx, "OLD", "NEW"); err != nil {
		t.Fatal(err)
	}
	// Listed under the old name, the note names the new one.
	renamed := noteFor("NEW", "svc", 3)
	renamed.Team = "beta"
	d = deliveryFor(renamed)
	d.Listed = []string{"old"}
	if got, err := s.Ingest(ctx, d, renamed, callFor(renamed), nil); err != nil || !got.Accepted || got.Owner != "" {
		t.Errorf("listed under the old name = %+v, %v; want accepted", got, err)
	}
	// Listed under the new name, the note names the old one.
	stale := noteFor("OLD", "svc", 4)
	stale.Team = "beta"
	d = deliveryFor(stale)
	d.Listed = []string{"new"}
	if got, err := s.Ingest(ctx, d, stale, callFor(stale), nil); err != nil || !got.Accepted {
		t.Errorf("listed under the new name = %+v, %v; want accepted", got, err)
	}
	unlisted := noteFor("NEW", "svc", 5)
	unlisted.Team = "beta"
	d = deliveryFor(unlisted)
	d.Listed = []string{"nowhere"}
	var owned *OwnedError
	if _, err := s.Ingest(ctx, d, unlisted, callFor(unlisted), nil); !errors.As(err, &owned) || owned.Owner != "alpha" {
		t.Errorf("listed elsewhere = %v; want *OwnedError{alpha}", err)
	}
	if team := ownerOf(t, s, "NEW"); team != "alpha" {
		t.Errorf("owner at the end = %q, want alpha", team)
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
	if got, err := s.Ingest(ctx, d, n, callFor(n), claims); err != nil || !got.Accepted {
		t.Fatalf("first ingest: %+v err=%v", got, err)
	}
	before := counts(t, s)

	retry := n
	retry.PRTitle = "changed on retry"
	retry.PRID = 2
	got, err := s.Ingest(ctx, d, retry, callFor(retry), []Claim{{ProjectKey: "OTHER"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Accepted {
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
	ingested, err := s.Ingest(ctx, deliveryFor(n), n, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !ingested.Accepted {
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
	if got, err := s.Ingest(ctx, d, again, callFor(again), nil); err != nil || !got.Accepted {
		t.Fatalf("second ingest: %+v err=%v", got, err)
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
