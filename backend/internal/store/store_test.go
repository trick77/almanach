package store

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// testStore opens a store on a fresh schema, migrates it and drops the
// schema at cleanup. SHIP_TEST_DSN comes from ship check, which starts the
// Postgres of ship.yaml; a plain go test without it skips.
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
	dsn := os.Getenv("SHIP_TEST_DSN")
	if dsn == "" {
		t.Skip("SHIP_TEST_DSN not set")
	}
	ctx := context.Background()
	schema := fmt.Sprintf("t_%d_%d", time.Now().UnixNano(), os.Getpid())
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	})
	cfg2, _ := pgxpool.ParseConfig(dsn)
	cfg2.ConnConfig.RuntimeParams["search_path"] = schema
	s, err := openConfig(ctx, cfg2, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

// The startup gate: an unmigrated schema names what is pending, a migrated
// one passes.
func TestSchemaCurrent_FailsUntilMigrated(t *testing.T) {
	s := emptyStore(t)
	ctx := context.Background()
	err := s.SchemaCurrent(ctx)
	if err == nil || !strings.Contains(err.Error(), "0001_initial.sql") {
		t.Fatalf("unmigrated: err = %v, want the pending file named", err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.SchemaCurrent(ctx); err != nil {
		t.Fatalf("migrated: err = %v, want nil", err)
	}
}

// Fixtures shared by the store tests.

// mergedBase is a merge time Postgres stores exactly (microseconds), so the
// fixtures compare with Equal after a round-trip.
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
	if err := s.pool.QueryRow(context.Background(), q).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}
