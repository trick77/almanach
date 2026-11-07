package store

import (
	"context"
	"testing"
	"time"
)

// A run's reason is read back with it; one never set, or one that does not
// read, is none.
func TestRunReason(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	sourceRepo(t, s, "PRJ", "svc")
	r := enqueue(t, s, "PRJ", "svc")
	if r.Reason != nil {
		t.Fatalf("before any = %+v, want none", r.Reason)
	}
	want := RunReason{Kind: ReasonRetry, Attempt: 2, Of: 3, LastError: "the call failed"}
	if err := s.SetRunReason(ctx, r.ID, want); err != nil {
		t.Fatal(err)
	}
	got, _, err := s.RunByID(ctx, r.ID)
	if err != nil || got.Reason == nil || *got.Reason != want {
		t.Fatalf("reason = %+v %v, want %+v", got.Reason, err, want)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE baseline_runs SET reason = '{"kind": 7}' WHERE id = ?`, r.ID); err != nil {
		t.Fatal(err)
	}
	if got, _, err := s.RunByID(ctx, r.ID); err != nil || got.Reason != nil {
		t.Errorf("unreadable reason = %+v %v, want none", got.Reason, err)
	}
}

// A pause is a row: on until lifted, the first's time kept by a second,
// none after the resume.
func TestWorkerPause(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if p, err := s.WorkerPause(ctx); err != nil || p != nil {
		t.Fatalf("before any = %+v %v, want none", p, err)
	}
	before := time.Now().Add(-time.Second)
	if err := s.PauseWorker(ctx); err != nil {
		t.Fatal(err)
	}
	first, err := s.WorkerPause(ctx)
	if err != nil || first == nil || first.At.Before(before) {
		t.Fatalf("paused = %+v %v", first, err)
	}
	if err := s.PauseWorker(ctx); err != nil {
		t.Fatal(err)
	}
	p, err := s.WorkerPause(ctx)
	if err != nil || p == nil || !p.At.Equal(first.At) {
		t.Fatalf("paused again = %+v %v, want the first's time", p, err)
	}
	if err := s.ResumeWorker(ctx); err != nil {
		t.Fatal(err)
	}
	if p, err := s.WorkerPause(ctx); err != nil || p != nil {
		t.Errorf("after the resume = %+v %v, want none", p, err)
	}
	if err := s.ResumeWorker(ctx); err != nil {
		t.Errorf("resume with none on: %v", err)
	}
}
