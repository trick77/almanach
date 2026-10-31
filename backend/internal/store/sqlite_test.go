package store

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ncruces/go-sqlite3"
)

// A second writer waits for the first one's transaction instead of failing,
// and both land: every write transaction takes the lock at BEGIN, and the
// busy timeout covers the wait.
func TestWriters_SecondWaitsAndBothLand(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	first := noteFor("PRJ", "svc", 1)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writeDelivery(ctx, tx, deliveryFor(first), first, callFor(first), nil); err != nil {
		t.Fatal(err)
	}

	second := noteFor("PRJ", "svc", 2)
	done := make(chan error, 1)
	go func() {
		ok, err := s.Ingest(ctx, deliveryFor(second), second, callFor(second), nil)
		if err == nil && !ok {
			err = errors.New("not accepted")
		}
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("second writer finished while the first held the lock: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("second writer: %v", err)
	}
	if n := count(t, s, "notes", ""); n != 2 {
		t.Errorf("notes = %d, want both", n)
	}
}

// A writer whose busy timeout runs out gets an error (the webhook's 500)
// and leaves nothing behind.
func TestWriters_BusyTimeoutIsAnErrorAndWritesNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "almanach.db")
	holder := storeAt(t, path)
	if err := holder.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	impatient := storeAt(t, path, "_pragma=busy_timeout(50)")
	ctx := context.Background()

	tx, err := holder.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()

	n := noteFor("PRJ", "svc", 1)
	start := time.Now()
	ok, err := impatient.Ingest(ctx, deliveryFor(n), n, callFor(n), []Claim{{ProjectKey: "PRJ"}})
	if !errors.Is(err, sqlite3.BUSY) || ok {
		t.Fatalf("Ingest under a held lock = %v, %v; want a busy error", ok, err)
	}
	if waited := time.Since(start); waited < 40*time.Millisecond {
		t.Errorf("gave up after %s, want the busy timeout waited out", waited)
	}
	if _, err := impatient.InsertRevision(ctx, NewRevision{Scope: ScopeProject, ProjectKey: "PRJ", Summary: "p", SourcePR: "PRJ/svc#1"}); !errors.Is(err, sqlite3.BUSY) {
		t.Fatalf("InsertRevision under a held lock: err = %v, want busy", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	for tb, got := range counts(t, impatient) {
		if got != 0 {
			t.Errorf("%s: %d rows after the busy failure, want none", tb, got)
		}
	}
	if got := count(t, impatient, "revisions", ""); got != 0 {
		t.Errorf("revisions = %d, want none", got)
	}
}

// Stored times are fixed width, so text order is time order, to the
// nanosecond and across zones; they read back as the same instant.
func TestStamps_FixedWidthSortAsTime(t *testing.T) {
	base := time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC)
	times := []time.Time{
		base,
		base.Add(time.Nanosecond),
		base.Add(999_999_999 * time.Nanosecond),
		base.Add(time.Second),
		base.Add(-time.Hour).In(time.FixedZone("CEST", 2*3600)),
		time.Date(2027, 1, 1, 0, 0, 0, 100, time.FixedZone("west", -5*3600)),
		time.Date(1000, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	stamps := make([]string, len(times))
	for i, tm := range times {
		stamps[i] = stamp(tm)
		if len(stamps[i]) != len(stampLayout) {
			t.Errorf("stamp(%v) = %q, width %d, want %d", tm, stamps[i], len(stamps[i]), len(stampLayout))
		}
		back, err := parseStamp(stamps[i])
		if err != nil || !back.Equal(tm) {
			t.Errorf("parseStamp(%q) = %v, %v; want %v", stamps[i], back, err, tm)
		}
	}
	byTime := make([]int, len(times))
	byText := make([]int, len(times))
	for i := range times {
		byTime[i], byText[i] = i, i
	}
	sort.Slice(byTime, func(a, b int) bool { return times[byTime[a]].Before(times[byTime[b]]) })
	sort.Slice(byText, func(a, b int) bool { return stamps[byText[a]] < stamps[byText[b]] })
	for i := range byTime {
		if byTime[i] != byText[i] {
			t.Fatalf("text order %v, time order %v", byText, byTime)
		}
	}

	// The database sorts them the same way: two merges a nanosecond apart
	// list newest first.
	s := testStore(t)
	ctx := context.Background()
	for i, tm := range times {
		n := noteFor("PRJ", "svc", i+1)
		n.MergedAt = tm
		ingest(t, s, n)
	}
	notes, err := s.Notes(ctx, "PRJ", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	for i, n := range notes {
		if want := byTime[len(byTime)-1-i] + 1; n.PRID != want || !n.MergedAt.Equal(times[want-1]) {
			t.Fatalf("Notes[%d] = PR %d at %v, want PR %d", i, n.PRID, n.MergedAt, want)
		}
	}
}

// A stored time in any other shape is an error, never a zero time; a NULL
// is nil where the column may be NULL and an error where it may not.
func TestStamps_ReadStrictly(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	var tm time.Time
	var p *time.Time
	for _, bad := range []string{`'2026-10-01T12:00:00Z'`, `'2026-10-01 12:00:00'`, `42`, `NULL`} {
		if err := s.db.QueryRowContext(ctx, `SELECT `+bad).Scan(timeAt(&tm)); err == nil {
			t.Errorf("%s read as a time: %v", bad, tm)
		}
	}
	if err := s.db.QueryRowContext(ctx, `SELECT CAST(? AS BLOB)`, stamp(mergedBase)).Scan(timeAt(&tm)); err != nil || !tm.Equal(mergedBase) {
		t.Errorf("blob stamp = %v, %v", tm, err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT NULL`).Scan(timeOrNil(&p)); err != nil || p != nil {
		t.Errorf("NULL = %v, %v; want nil", p, err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT 'yesterday'`).Scan(timeOrNil(&p)); err == nil {
		t.Errorf("'yesterday' read as %v", p)
	}
}

// The JSON columns take text json_valid accepts and nothing else; a note
// refused for its JSON takes the whole delivery with it.
func TestJSON_InvalidRefused(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	n := noteFor("PRJ", "svc", 1)
	n.Body = []byte(`{"summary":`)
	if _, err := s.Ingest(ctx, deliveryFor(n), n, callFor(n), nil); !errors.Is(err, sqlite3.CONSTRAINT_CHECK) {
		t.Fatalf("Ingest with a broken note: err = %v, want a CHECK failure", err)
	}
	for tb, got := range counts(t, s) {
		if got != 0 {
			t.Errorf("%s: %d rows after the refused note, want none", tb, got)
		}
	}

	ingest(t, s, noteFor("PRJ", "svc", 2))
	sourceRepo(t, s, "tools", "svc")
	r := enqueue(t, s, "tools", "svc")
	r.Plan = []byte(`{"subjects":[]}`)
	if ok, err := s.SaveRun(ctx, r); err != nil || !ok {
		t.Fatalf("SaveRun: %v %v", ok, err)
	}
	if _, err := s.InsertRevision(ctx, NewRevision{Scope: ScopeProject, ProjectKey: "PRJ", Summary: "p", SourcePR: "PRJ/svc#2",
		Statements: fixtureStatements()}); err != nil {
		t.Fatal(err)
	}
	// Bound as text: a blob would pass no json_valid and no JSON function.
	for _, col := range []string{"deliveries.payload", "notes.note", "note_calls.warnings", "revisions.statements", "baseline_runs.plan"} {
		table, column, _ := strings.Cut(col, ".")
		if n := count(t, s, table, "typeof("+column+") <> 'text'"); n != 0 {
			t.Errorf("%s: %d rows not stored as text", col, n)
		}
	}

	r.Plan = []byte(`not json`)
	if _, err := s.SaveRun(ctx, r); !errors.Is(err, sqlite3.CONSTRAINT_CHECK) {
		t.Errorf("SaveRun with a broken plan: err = %v, want a CHECK failure", err)
	}
	for _, q := range []string{
		`UPDATE revisions SET statements = '[{'`,
		`UPDATE revisions SET map = 'nope'`,
		`UPDATE notes SET bugfix = '{"cause":'`,
		`UPDATE note_calls SET warnings = 'w1'`,
		`UPDATE deliveries SET payload = ''`,
	} {
		if _, err := s.db.ExecContext(ctx, q); !errors.Is(err, sqlite3.CONSTRAINT_CHECK) {
			t.Errorf("%s: err = %v, want a CHECK failure", q, err)
		}
	}
}
