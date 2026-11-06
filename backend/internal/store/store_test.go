package store

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testStore opens a migrated database in a file of the test's own temp
// dir: no server, no skip, every store test runs in a plain go test.
func testStore(t *testing.T) *Store {
	t.Helper()
	s := emptyStore(t)
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return s
}

// emptyStore is testStore before any migration ran.
func emptyStore(t *testing.T) *Store {
	t.Helper()
	return storeAt(t, filepath.Join(t.TempDir(), "almanach.db"))
}

// storeAt opens the database at path, closed when the test ends; extra are
// DSN parameters after the store's own.
func storeAt(t *testing.T, path string, extra ...string) *Store {
	t.Helper()
	s, err := open(context.Background(), path, slog.New(slog.NewTextHandler(io.Discard, nil)), extra...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

// Backup writes a consistent, compacted copy that opens as a database of
// its own with every row, while the original stays open and a writer
// holds its lock; it refuses the database itself and a file that exists,
// and leaves no part file behind.
func TestBackup(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	ingest(t, s, noteFor("PRJ", "svc", 1))
	ingest(t, s, noteFor("PRJ", "svc", 2))
	dir := t.TempDir()
	dest := filepath.Join(dir, "copy.db")
	// A writer mid-transaction on the same file: the copy is a reader's
	// view and waits on nothing.
	writer := storeAt(t, s.path, "_pragma=busy_timeout(50)")
	tx, err := writer.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE notes SET pr_title = 'uncommitted'`); err != nil {
		t.Fatal(err)
	}
	size, err := s.Backup(ctx, dest)
	if err != nil || size <= 0 {
		t.Fatalf("Backup under a writer = %d, %v", size, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if parts, _ := filepath.Glob(filepath.Join(dir, "*.part")); len(parts) != 0 {
		t.Errorf("part files left behind: %v", parts)
	}
	// No sidecars beside the copy (checked before anything opens it: an
	// open in WAL mode makes them).
	for _, p := range []string{dest + "-wal", dest + "-shm"} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s written beside the copy", p)
		}
	}
	// The copy stands on its own: migrated, every committed note there,
	// nothing uncommitted, writable.
	c := storeAt(t, dest)
	if n := count(t, c, "notes", "pr_title = 'uncommitted'"); n != 0 {
		t.Errorf("the copy holds %d uncommitted rows", n)
	}
	if err := c.SchemaCurrent(ctx); err != nil {
		t.Errorf("copy's schema: %v", err)
	}
	if n := count(t, c, "notes", ""); n != 2 {
		t.Errorf("copy's notes = %d, want 2", n)
	}
	ingest(t, c, noteFor("PRJ", "svc", 3))
	if n := count(t, s, "notes", ""); n != 2 {
		t.Errorf("original's notes after a write to the copy = %d, want 2", n)
	}
	// Refusals: the file exists; the database itself, by any spelling, and
	// its sidecars, there or not.
	if _, err := s.Backup(ctx, dest); err == nil || !strings.Contains(err.Error(), "exists") {
		t.Errorf("Backup onto an existing file = %v, want refused", err)
	}
	for _, same := range []string{s.path, filepath.Join(filepath.Dir(s.path), ".", filepath.Base(s.path)), s.path + "-wal", s.path + "-shm", s.path + "-journal"} {
		if _, err := s.Backup(ctx, same); err == nil || !strings.Contains(err.Error(), "database itself") {
			t.Errorf("Backup onto %s = %v, want refused", same, err)
		}
	}
	// A folder that does not exist fails cleanly, nothing left.
	if _, err := s.Backup(ctx, filepath.Join(dir, "nope", "copy.db")); err == nil {
		t.Error("Backup into a missing folder succeeded")
	}
	// A part file that is not empty is refused by SQLite itself: the copy
	// fails, the placeholder is taken away, nothing under the name.
	full := filepath.Join(dir, "full.db.part")
	if err := os.WriteFile(full, []byte("not empty"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.backupInto(ctx, full, filepath.Join(dir, "full.db")); err == nil {
		t.Error("backupInto filled a part file that was not empty")
	}
	if _, err := os.Stat(filepath.Join(dir, "full.db")); err == nil {
		t.Error("full.db there after a failed copy")
	}
	// The database through a symlinked folder is still the database.
	link := filepath.Join(dir, "link")
	if err := os.Symlink(filepath.Dir(s.path), link); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Backup(ctx, filepath.Join(link, filepath.Base(s.path)+"-wal")); err == nil || !strings.Contains(err.Error(), "database itself") {
		t.Errorf("Backup onto a sidecar through a symlink = %v, want refused", err)
	}
	// A sidecar that is not there, spelled in another case: on a
	// case-insensitive file system it is the sidecar.
	if _, err := s.Backup(ctx, strings.ToUpper(s.path)+"-JOURNAL"); err == nil || !strings.Contains(err.Error(), "database itself") {
		t.Errorf("Backup onto the journal in another case = %v, want refused", err)
	}
	// A cancelled context (Ctrl-C, SIGTERM) fails the copy and takes the
	// placeholder and the part file away.
	done, cancel := context.WithCancel(ctx)
	cancel()
	gone := filepath.Join(dir, "cancelled.db")
	if _, err := s.Backup(done, gone); err == nil {
		t.Error("Backup with a cancelled context succeeded")
	}
	if _, err := os.Stat(gone); err == nil {
		t.Error("cancelled.db there after a cancelled copy")
	}
	if parts, _ := filepath.Glob(filepath.Join(dir, "cancelled.db.*.part")); len(parts) != 0 {
		t.Errorf("part files left after a cancelled copy: %v", parts)
	}
}

// OpenExisting opens a migrated database and refuses a path that names no
// file (never creating it) and an empty database, as one a mistyped path
// once made.
func TestOpenExisting(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	dir := t.TempDir()
	if _, err := OpenExisting(ctx, filepath.Join(dir, "nope.db"), s.log); err == nil {
		t.Error("OpenExisting opened a file that is not there")
	}
	if _, err := os.Stat(filepath.Join(dir, "nope.db")); err == nil {
		t.Error("OpenExisting created the file")
	}
	empty := emptyStore(t)
	if _, err := empty.db.ExecContext(ctx, `CREATE TABLE t (x)`); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenExisting(ctx, empty.path, s.log); err == nil || !strings.Contains(err.Error(), "no migration applied") {
		t.Errorf("OpenExisting of an unmigrated database = %v, want refused", err)
	}
	again, err := OpenExisting(ctx, s.path, s.log)
	if err != nil {
		t.Fatal(err)
	}
	again.Close()
}

// Optimize runs on every idle connection of a pool with a history and
// says so; with a context already done it fails as one log line.
func TestOptimize(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	ingest(t, s, noteFor("PRJ", "svc", 1))
	var buf strings.Builder
	s.log = slog.New(slog.NewTextHandler(&buf, nil))
	if !s.Optimize(ctx) {
		t.Errorf("Optimize on a live pool failed: %s", buf.String())
	}
	done, cancel := context.WithCancel(ctx)
	cancel()
	if s.Optimize(done) {
		t.Error("Optimize with a cancelled context reported success")
	}
	if n := strings.Count(buf.String(), "SQLite optimize failed"); n != 1 {
		t.Errorf("optimize warnings = %d, want 1: %s", n, buf.String())
	}
}

// The startup gate: an unmigrated database names what is pending, a
// migrated one passes, and a second migrate applies nothing.
func TestSchemaCurrent_FailsUntilMigrated(t *testing.T) {
	s := emptyStore(t)
	ctx := context.Background()
	err := s.SchemaCurrent(ctx)
	if err == nil || !strings.Contains(err.Error(), "0001_init.sql") {
		t.Fatalf("unmigrated: err = %v, want the pending file named", err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.SchemaCurrent(ctx); err != nil {
		t.Fatalf("migrated: err = %v, want nil", err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if err := s.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
}

// A folder that does not exist is an error at open, naming the path.
func TestOpen_MissingFolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent", "almanach.db")
	s, err := Open(context.Background(), path, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err == nil {
		s.Close()
		t.Fatal("opened a database in a folder that does not exist")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("err = %v, want the path named", err)
	}
}

// Fixtures shared by the store tests.

// mergedBase is the fixtures' merge time; stored times keep every
// nanosecond, so they compare with Equal after a round-trip.
var mergedBase = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

// noteFor is a plain feature note of one PR; the body carries three items
// (two components, one rule), which Notes counts.
func noteFor(project, repo string, prID int) Note {
	return Note{
		ProjectKey:   project,
		RepoSlug:     repo,
		PRID:         prID,
		Team:         "alpha",
		PRKey:        fmt.Sprintf("%s/%s#%d", project, repo, prID),
		PRTitle:      fmt.Sprintf("PR %d", prID),
		TicketKey:    "ABC-1",
		IssueType:    "Story",
		ChangeKind:   "feature",
		Body:         json.RawMessage(`{"summary":"x","components":[{"name":"a"},{"name":"b"}],"rules":[{"name":"r"}],"invariants":[],"entities":[]}`),
		SourceCommit: "abc123",
		MergeCommit:  "def456",
		MergedAt:     mergedBase.Add(time.Duration(prID) * time.Minute),
		RunID:        int64(100 + prID),
		ModelLabel:   "m",
		IssuesPosted: 1,
		Suggestions:  2,
	}
}

// bugfixFor is noteFor as a bugfix in one area with the given cause kind,
// merged at the given time.
func bugfixFor(project, repo string, prID int, area, cause string, mergedAt time.Time) Note {
	n := noteFor(project, repo, prID)
	n.ChangeKind = "bugfix"
	n.Bugfix = json.RawMessage(fmt.Sprintf(`{"symptom":"it broke","cause":"because","cause_kind":%q}`, cause))
	n.BugfixArea = area
	n.BugfixComponent = "files"
	n.MergedAt = mergedAt
	return n
}

// deliveryFor is the delivery a note arrived in, keyed by the PR.
func deliveryFor(n Note) Delivery {
	return Delivery{
		ID:        fmt.Sprintf("d-%s-%s-%d", n.ProjectKey, n.RepoSlug, n.PRID),
		Team:      n.Team,
		EventType: "pr.merged",
		Payload:   json.RawMessage(`{"pr":` + fmt.Sprint(n.PRID) + `}`),
	}
}

// callFor is a note call with the lanes NoteByKey reads.
func callFor(n Note) *NoteCall {
	runID := n.RunID
	return &NoteCall{
		RunID:        &runID,
		Outcome:      "ok",
		NoteStatus:   "written",
		Profile:      "notes",
		Model:        "test-model",
		InputTokens:  i64(1000),
		CacheRead:    i64(400),
		OutputTokens: i64(50),
		Cost:         i64(1_500_000),
		Warnings:     []string{"w1"},
		Rounds:       2,
		RoundsCost:   i64(3_000_000),
	}
}

func i64(v int64) *int64 { return &v }

// ingest writes a note with its delivery and call under one repo claim.
func ingest(t *testing.T, s *Store, n Note) {
	t.Helper()
	ok, err := s.Ingest(context.Background(), deliveryFor(n), n, callFor(n), []Claim{{ProjectKey: n.ProjectKey, RepoSlug: n.RepoSlug}})
	if err != nil {
		t.Fatalf("ingest %s: %v", n.PRKey, err)
	}
	if !ok {
		t.Fatalf("ingest %s: not accepted", n.PRKey)
	}
}

// count is the row count of one table, with an optional WHERE clause.
func count(t *testing.T, s *Store, table, where string) int {
	t.Helper()
	q := "SELECT COUNT(*) FROM " + table
	if where != "" {
		q += " WHERE " + where
	}
	var n int
	if err := s.db.QueryRowContext(context.Background(), q).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}
