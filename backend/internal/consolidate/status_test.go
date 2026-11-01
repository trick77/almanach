package consolidate

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/trick77/almanach/internal/baseline"
	"github.com/trick77/almanach/internal/inference"
	"github.com/trick77/almanach/internal/store"
)

// peekMerger answers like fakeMerger and records what Status said while
// its call ran.
type peekMerger struct {
	fakeMerger
	w    *Worker
	seen Status
}

func (m *peekMerger) Merge(ctx context.Context, req inference.MergeRequest) inference.MergeResult {
	m.seen = m.w.Status()
	return m.fakeMerger.Merge(ctx, req)
}

// While a note folds, Status names it; once the drain is over, nothing.
func TestStatus_NamesTheNoteInFlight(t *testing.T) {
	st := newFakeStore()
	st.queue = []store.QueuedNote{queued("PLAT/files#42")}
	m := &peekMerger{fakeMerger: fakeMerger{result: okResult(nil, nil)}}
	w := newWorker(st, m)
	m.w = w
	w.drain(context.Background())
	b := m.seen.Busy
	if b == nil || b.Kind != KindNote || b.PRKey != "PLAT/files#42" || b.ProjectKey != "PLAT" || b.RepoSlug != "files" || b.Since.IsZero() {
		t.Fatalf("Status during the fold = %+v", m.seen)
	}
	if s := w.Status(); s.Busy != nil || s.Poll != 30*time.Second {
		t.Errorf("Status after the drain = %+v, want idle, polling every 30s", s)
	}
}

// The queue as drain takes it: free notes, the started run, the notes it
// holds, the queued runs; a second started run (none should exist) is
// left out rather than shown first.
func TestQueueOrder(t *testing.T) {
	notes := []store.PendingNote{{PRKey: "a", HeldBy: 7}, {PRKey: "b"}, {PRKey: "c", HeldBy: 7}}
	runs := []store.BaselineRun{{ID: 7, State: store.RunDescribing}, {ID: 8, State: store.RunProject}, {ID: 9, State: store.RunQueued}}
	o := QueueOrder(notes, runs)
	if len(o.Free) != 1 || o.Free[0].PRKey != "b" || len(o.Held) != 2 || o.Held[0].PRKey != "a" || o.Started == nil ||
		o.Started.ID != 7 || len(o.Queued) != 1 || o.Queued[0].ID != 9 {
		t.Errorf("order = %+v", o)
	}
}

// The loop sleeps a poll, or until a hold ends when that is sooner.
func TestNextLook(t *testing.T) {
	w := newWorker(newFakeStore(), &fakeMerger{})
	clock := mergedAt
	w.now = func() time.Time { return clock }
	if d := w.nextLook(); d != w.poll {
		t.Errorf("no hold: %s, want the poll", d)
	}
	w.hold(context.Background(), HoldTimeout, 0)
	if d := w.nextLook(); d != backoffMin {
		t.Errorf("hold of %s: sleeps %s", backoffMin, d)
	}
	w.hold(context.Background(), HoldRateLimit, time.Hour)
	if d := w.nextLook(); d != w.poll {
		t.Errorf("hold past the poll: sleeps %s, want the poll", d)
	}
}

// A wait names its run and keeps its first time; another reason restarts
// it; a step that went on clears it.
func TestStatus_Wait(t *testing.T) {
	w := newWorker(newFakeStore(), &fakeMerger{})
	clock := mergedAt
	w.now = func() time.Time { return clock }
	w.setWait(7, WaitClone)
	clock = clock.Add(time.Minute)
	w.setWait(7, WaitClone)
	if s := w.Status(); s.Wait == nil || s.Wait.RunID != 7 || s.Wait.Reason != WaitClone || !s.Wait.Since.Equal(mergedAt) {
		t.Fatalf("Status = %+v, want run 7 waiting for its clone since the first time", s.Wait)
	}
	w.setWait(7, WaitPlace)
	if s := w.Status(); !s.Wait.Since.Equal(clock) || s.Wait.Reason != WaitPlace {
		t.Errorf("another reason: %+v", s.Wait)
	}
	w.clearWait()
	if s := w.Status(); s.Wait != nil {
		t.Errorf("cleared: %+v", s.Wait)
	}
}

// A run's steps from its plan and calls: the catalogue, the overview, the
// subjects in reading order with their files, the project and the map,
// each done, skipped, current or to do, with its calls' time and cost.
func TestRunSteps(t *testing.T) {
	plan := runPlan{
		Plan: baseline.Plan{
			Format: baseline.PlanFormat,
			Subjects: []baseline.Subject{
				{Name: "Abgleich", Files: []baseline.File{{Path: "a.go", Size: 100}, {Path: "b.go", Size: 50}}},
				{Name: "Export", Files: []baseline.File{{Path: "c.go", Size: 10}}},
				{Name: "Webhooks", Files: []baseline.File{{Path: "d.go", Size: 1}}},
			},
		},
		Overview: true,
		Read:     []string{"Abgleich", "Export", "Webhooks"},
	}
	plan.SkipSubject("Export", baseline.SkipBytes)
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	r := store.BaselineRun{ID: 1, State: store.RunDescribing, Step: 3, Plan: raw}
	at := mergedAt
	one, two := 1, 3
	calls := []store.RunCall{
		{Purpose: store.PurposeCatalogue, Outcome: "ok", CostNanoUSD: i64(1_000_000), At: at},
		{Purpose: store.PurposeSubject, Step: &one, Outcome: "timed_out", At: at},
		{Purpose: store.PurposeSubject, Step: &one, Outcome: "ok", CostNanoUSD: i64(2_000_000), At: at.Add(time.Minute)},
		{Purpose: store.PurposeSubject, Step: &two, Outcome: "ok", At: at},
	}
	steps, err := RunSteps(r, calls)
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		kind, name, state string
		skip              baseline.Skip
		files, calls      int
	}
	want := []row{
		{StepCatalogue, "", StepDone, "", 0, 1},
		{StepOverview, "", StepDone, "", 0, 0},
		{StepSubject, "Abgleich", StepDone, "", 2, 2},
		{StepSubject, "Export", StepSkipped, baseline.SkipBytes, 1, 0},
		{StepSubject, "Webhooks", StepCurrent, "", 1, 1},
		{StepProject, "", StepTodo, "", 0, 0},
		{StepRepoMap, "", StepTodo, "", 0, 0},
	}
	if len(steps) != len(want) {
		t.Fatalf("steps = %+v", steps)
	}
	for i, w := range want {
		s := steps[i]
		if (row{s.Kind, s.Name, s.State, s.Skip, s.Files, s.Calls}) != w {
			t.Errorf("step %d = %+v, want %+v", i, s, w)
		}
	}
	if s := steps[2]; s.Failed != 1 || s.Bytes != 150 || s.CostNanoUSD == nil || *s.CostNanoUSD != 2_000_000 || !s.At.Equal(at.Add(time.Minute)) {
		t.Errorf("Abgleich = %+v, want one failed of two calls, 150 bytes, 2 milli, the last call's time", s)
	}

	// Later states: the project step, then the map; a queued run has no
	// plan, only the steps every run has.
	r.State = store.RunProject
	if steps, _ := RunSteps(r, nil); steps[5].State != StepCurrent || steps[6].State != StepTodo || steps[4].State != StepDone {
		t.Errorf("project: %+v", steps)
	}
	plan.ProjectDone = true
	plan.SkipStep(baseline.PartProject, baseline.SkipNoPage)
	r.Plan, _ = json.Marshal(plan)
	if steps, _ := RunSteps(r, nil); steps[5].State != StepSkipped || steps[5].Skip != baseline.SkipNoPage || steps[6].State != StepCurrent {
		t.Errorf("map: %+v", steps)
	}
	queuedRun := store.BaselineRun{State: store.RunQueued}
	if steps, err := RunSteps(queuedRun, nil); err != nil || len(steps) != 3 || steps[0].State != StepTodo {
		t.Errorf("queued: %+v %v", steps, err)
	}
	r.State, r.Step = store.RunCataloguing, 0
	if steps, _ := RunSteps(r, nil); steps[0].State != StepCurrent {
		t.Errorf("cataloguing: %+v", steps)
	}
	if _, err := RunSteps(store.BaselineRun{Plan: json.RawMessage(`{`)}, nil); err == nil {
		t.Error("a broken plan read without an error")
	}
}

func i64(v int64) *int64 { return &v }
