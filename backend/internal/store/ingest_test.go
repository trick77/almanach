package store

import (
	"context"
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
	if n := count(t, s, "claims", "team = 'alpha' AND project_key = 'PRJ' AND repo_slug IS NULL"); n != 1 {
		t.Errorf("project claim rows = %d, want 1", n)
	}
	if n := count(t, s, "claims", "team = 'alpha' AND project_key = 'PRJ' AND repo_slug = 'svc'"); n != 1 {
		t.Errorf("repo claim rows = %d, want 1", n)
	}
	if n := count(t, s, "projects", "key = 'PRJ' AND team = 'alpha'"); n != 1 {
		t.Errorf("project row = %d, want 1", n)
	}
	if n := count(t, s, "repos", "project_key = 'PRJ' AND slug = 'svc' AND team = 'alpha'"); n != 1 {
		t.Errorf("repo row = %d, want 1", n)
	}
	if n := count(t, s, "notes", "project_key = 'PRJ' AND repo_slug = 'svc' AND pr_id = 1 AND delivery_id = 'd-PRJ-svc-1' AND consolidated_at IS NULL"); n != 1 {
		t.Errorf("note row = %d, want 1", n)
	}
	if n := count(t, s, "note_calls", "delivery_id = 'd-PRJ-svc-1' AND pr_id = 1 AND rounds = 2 AND warnings = '{w1}'"); n != 1 {
		t.Errorf("note_calls row = %d, want 1", n)
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
	if err := s.pool.QueryRow(ctx, `SELECT pr_title FROM notes WHERE pr_id = 1`).Scan(&title); err != nil {
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
	if _, err := s.DeferFold(ctx, "PRJ", "svc", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkConsolidated(ctx, "PRJ", "svc", 1); err != nil {
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
