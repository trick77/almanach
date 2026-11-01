package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/trick77/almanach/internal/clone"
	"github.com/trick77/almanach/internal/config"
	"github.com/trick77/almanach/internal/consolidate"
	"github.com/trick77/almanach/internal/httpapi"
	"github.com/trick77/almanach/internal/store"
)

var queueNow = time.Date(2026, 11, 1, 14, 0, 0, 0, time.UTC)

type fakeQueueStore struct {
	notes      []store.PendingNote
	open       []store.BaselineRun
	finished   []store.FinishedRun
	folded     []store.FoldedNote
	calls      map[int64][]store.RunCall
	openRead   bool
	callsAsked []int64
	countRuns  bool
}

func (f *fakeQueueStore) PendingNotes(context.Context) ([]store.PendingNote, error) {
	return f.notes, nil
}
func (f *fakeQueueStore) OpenRuns(context.Context) ([]store.BaselineRun, error) {
	f.openRead = true
	return f.open, nil
}
func (f *fakeQueueStore) FinishedRuns(context.Context, time.Time, int) ([]store.FinishedRun, error) {
	return f.finished, nil
}
func (f *fakeQueueStore) FoldedNotes(context.Context, time.Time, int) ([]store.FoldedNote, error) {
	return f.folded, nil
}
func (f *fakeQueueStore) QueueCount(_ context.Context, runs bool) (int, error) {
	f.countRuns = runs
	if !runs {
		return len(f.notes), nil
	}
	return len(f.notes) + len(f.open), nil
}
func (f *fakeQueueStore) RunCalls(_ context.Context, id int64) ([]store.RunCall, error) {
	f.callsAsked = append(f.callsAsked, id)
	return f.calls[id], nil
}
func (f *fakeQueueStore) RunCosts(_ context.Context, ids []int64) (map[int64]int64, error) {
	out := map[int64]int64{}
	for _, id := range ids {
		for _, c := range f.calls[id] {
			if c.CostNanoUSD != nil {
				out[id] += *c.CostNanoUSD
			}
		}
	}
	return out, nil
}
func (f *fakeQueueStore) Spent(context.Context, time.Time) (*int64, int, error) {
	n := int64(1_284_000_000)
	return &n, 23, nil
}

type fakeWorkerStatus struct{ s consolidate.Status }

func (f fakeWorkerStatus) Status() consolidate.Status { return f.s }

type fakeSync struct{ c clone.CycleStatus }

func (f fakeSync) Cycle() clone.CycleStatus { return f.c }
func (f fakeSync) RetryAt(finished time.Time, failed int) (time.Time, bool) {
	return finished.Add(24 * time.Hour), failed <= 3
}

func getQueue(t *testing.T, q *QueueDeps) queueBody {
	t.Helper()
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httpapi.New(func(context.Context) error { return nil }, discard)
	q.Now = func() time.Time { return queueNow }
	Register(srv, Deps{Store: newFakeStore(), Keys: keysFile(t), Worker: &fakeWaker{}, AuthMode: config.AuthNone, Queue: q, Log: discard})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/queue", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/queue = %d %s", rec.Code, rec.Body.String())
	}
	var out queueBody
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func at(m int) time.Time { return queueNow.Add(time.Duration(m) * time.Minute) }

func startedRun() store.BaselineRun {
	started := at(-9)
	return store.BaselineRun{ID: 41, ProjectKey: "pay", Slug: "billing", State: store.RunCataloguing, Mode: store.RunFull,
		Trigger: store.TriggerFirstReady, Commit: "a1b2c3d", QueuedAt: at(-20), StartedAt: &started}
}

func queuedRun(id int64, slug string, m int) store.BaselineRun {
	return store.BaselineRun{ID: id, ProjectKey: "shop", Slug: slug, State: store.RunQueued, Mode: store.RunFull,
		Trigger: store.TriggerMonthly, QueuedAt: at(m)}
}

func pending(slug, pr string, m int, heldBy int64) store.PendingNote {
	return store.PendingNote{ProjectKey: "pay", RepoSlug: slug, PRKey: pr, PRTitle: "t " + pr, ReceivedAt: at(m), HeldBy: heldBy}
}

// The worker's order: the notes it may fold, the started run's next step,
// the notes that wait for that run, the queued runs. The run in flight is
// current, with its steps and its cost so far.
func TestQueue_TheWorkersOrder(t *testing.T) {
	cost := int64(31_000_000)
	st := &fakeQueueStore{
		notes: []store.PendingNote{pending("billing", "pay/billing#317", -5, 41), pending("ledger", "pay/ledger#812", -3, 0)},
		open:  []store.BaselineRun{startedRun(), queuedRun(42, "checkout", -30)},
		calls: map[int64][]store.RunCall{41: {{Purpose: store.PurposeCatalogue, Outcome: "ok", CostNanoUSD: &cost, At: at(-8)}}},
	}
	busy := consolidate.Busy{Kind: consolidate.KindRun, ProjectKey: "pay", RepoSlug: "billing", RunID: 41, Since: at(-1)}
	q := &QueueDeps{Store: st, Worker: fakeWorkerStatus{consolidate.Status{Busy: &busy, Poll: 30 * time.Second}},
		Sync: fakeSync{clone.CycleStatus{Running: true, ProjectKey: "shop", RepoSlug: "web", RepoSince: at(-2), Done: 2, Total: 14}}}
	out := getQueue(t, q)

	if out.Current == nil || out.Current.Run == nil || out.Current.Run.ID != 41 || !out.Current.Since.Equal(at(-1)) {
		t.Fatalf("current = %+v, want run 41 since a minute", out.Current)
	}
	if out.Current.Run.CostUSD != "0.031" || out.Current.Run.Steps[0].State != consolidate.StepCurrent {
		t.Errorf("current run = %+v", out.Current.Run)
	}
	var order []string
	for _, it := range out.Next {
		if it.Run != nil {
			order = append(order, it.Repo)
		} else {
			order = append(order, it.PRKey)
		}
	}
	want := []string{"pay/ledger#812", "pay/billing#317", "checkout"}
	if len(order) != len(want) {
		t.Fatalf("next = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("next = %v, want %v", order, want)
		}
	}
	if out.Next[1].HeldBy != 41 || out.Next[0].Wait != nil {
		t.Errorf("held note = %+v, first = %+v", out.Next[1], out.Next[0])
	}
	if s := out.Sync; s == nil || !s.Running || s.Repo != "web" || s.Done != 2 || s.Total != 14 {
		t.Errorf("sync = %+v", out.Sync)
	}
	if out.Spent.CostUSD != "1.284" || out.Spent.Calls != 23 || out.PollSeconds != 30 || out.MaxAttempts != 8 {
		t.Errorf("spent = %+v poll %d max %d", out.Spent, out.PollSeconds, out.MaxAttempts)
	}
	// The queued run has made no call: its calls are never read.
	if len(st.callsAsked) != 1 || st.callsAsked[0] != 41 {
		t.Errorf("calls read for runs %v, want 41 alone", st.callsAsked)
	}
}

// Nothing in flight: a hold names its cause and end on the first item; a
// run step waiting for its clone says so on that run, the started run
// coming after the notes it lets through.
func TestQueue_WaitsOnTheNextItem(t *testing.T) {
	until := at(2)
	st := &fakeQueueStore{notes: []store.PendingNote{pending("ledger", "pay/ledger#812", -3, 0)}, open: []store.BaselineRun{startedRun()}}
	q := &QueueDeps{Store: st, Sync: fakeSync{},
		Worker: fakeWorkerStatus{consolidate.Status{HoldUntil: &until, HoldCause: consolidate.HoldTimeout,
			Wait: &consolidate.Wait{RunID: 41, Reason: consolidate.WaitClone, Since: at(-1)}}}}
	out := getQueue(t, q)
	if out.Current != nil || len(out.Next) != 2 {
		t.Fatalf("current %+v next %+v", out.Current, out.Next)
	}
	if w := out.Next[0].Wait; w == nil || w.Reason != consolidate.HoldTimeout || !w.Until.Equal(until) {
		t.Errorf("first wait = %+v, want the timeout hold until %v", w, until)
	}
	if w := out.Next[1].Wait; w == nil || w.Reason != consolidate.WaitClone || !w.Since.Equal(at(-1)) {
		t.Errorf("run wait = %+v, want its clone since a minute", w)
	}
}

// The finished list: runs and notes newest first, a failed run's next
// automatic try when it is its repository's last run.
func TestQueue_Recent(t *testing.T) {
	done, failedAt := at(-60), at(-30)
	cost := int64(733_000_000)
	st := &fakeQueueStore{
		finished: []store.FinishedRun{
			{BaselineRun: store.BaselineRun{ID: 39, ProjectKey: "shop", Slug: "search", State: store.RunFailed, Error: "given up", FinishedAt: &failedAt},
				Latest: true, FailedInRow: 1},
			{BaselineRun: store.BaselineRun{ID: 40, ProjectKey: "shop", Slug: "web", State: store.RunDone, FinishedAt: &done}, Latest: true},
		},
		folded: []store.FoldedNote{{ProjectKey: "pay", RepoSlug: "ledger", PRKey: "pay/ledger#808", ConsolidatedAt: at(-45), Outcome: "ok"}},
		calls:  map[int64][]store.RunCall{40: {{Purpose: store.PurposeCatalogue, CostNanoUSD: &cost}}},
	}
	out := getQueue(t, &QueueDeps{Store: st, Worker: fakeWorkerStatus{}, Sync: fakeSync{}})
	if len(out.Recent) != 3 || out.Recent[0].RunID != 39 || out.Recent[1].PRKey != "pay/ledger#808" || out.Recent[2].RunID != 40 {
		t.Fatalf("recent = %+v", out.Recent)
	}
	if r := out.Recent[0]; r.RetryAt == nil || !r.RetryAt.Equal(failedAt.Add(24*time.Hour)) || r.Error != "given up" {
		t.Errorf("failed run = %+v, want a retry a day after", r)
	}
	if out.Recent[2].CostUSD != "0.733" || out.Recent[2].RetryAt != nil {
		t.Errorf("done run = %+v", out.Recent[2])
	}

	// Not the repository's last run: no retry promised.
	st.finished[0].Latest = false
	if out := getQueue(t, &QueueDeps{Store: st, Worker: fakeWorkerStatus{}, Sync: fakeSync{}}); out.Recent[0].RetryAt != nil {
		t.Errorf("superseded failed run promises a retry: %+v", out.Recent[0])
	}
	// Past the retries: none promised either.
	st.finished[0].Latest, st.finished[0].FailedInRow = true, 4
	if out := getQueue(t, &QueueDeps{Store: st, Worker: fakeWorkerStatus{}, Sync: fakeSync{}}); out.Recent[0].RetryAt != nil {
		t.Errorf("retries spent, a retry promised: %+v", out.Recent[0])
	}
}

// A note the worker folded between its status and the read of the notes
// is still the work in flight, named by the status.
func TestQueue_NoteFoldedMeanwhile(t *testing.T) {
	st := &fakeQueueStore{notes: []store.PendingNote{pending("ledger", "pay/ledger#813", -2, 0)}}
	busy := consolidate.Busy{Kind: consolidate.KindNote, ProjectKey: "pay", RepoSlug: "ledger", PRKey: "pay/ledger#812", Since: at(0)}
	out := getQueue(t, &QueueDeps{Store: st, Worker: fakeWorkerStatus{consolidate.Status{Busy: &busy}}})
	if out.Current == nil || out.Current.PRKey != "pay/ledger#812" || out.Current.Since == nil || len(out.Next) != 1 {
		t.Errorf("current %+v next %+v", out.Current, out.Next)
	}
}

// With the sources off there are no runs and no sync: the runs are never
// read, the sync is null; a note in flight is current.
func TestQueue_SourcesOff(t *testing.T) {
	st := &fakeQueueStore{notes: []store.PendingNote{pending("ledger", "pay/ledger#812", -3, 0), pending("ledger", "pay/ledger#813", -2, 0)}}
	busy := consolidate.Busy{Kind: consolidate.KindNote, ProjectKey: "pay", RepoSlug: "ledger", PRKey: "pay/ledger#812", Since: at(0)}
	out := getQueue(t, &QueueDeps{Store: st, Worker: fakeWorkerStatus{consolidate.Status{Busy: &busy}}})
	if st.openRead || out.Sync != nil {
		t.Errorf("sources off: runs read %v, sync %+v", st.openRead, out.Sync)
	}
	if out.Current == nil || out.Current.PRKey != "pay/ledger#812" || len(out.Next) != 1 || out.Next[0].PRKey != "pay/ledger#813" {
		t.Errorf("current %+v next %+v", out.Current, out.Next)
	}
}

// The nav's count: notes and open runs, nothing else read.
func TestQueueCount(t *testing.T) {
	st := &fakeQueueStore{notes: []store.PendingNote{pending("ledger", "pay/ledger#812", -3, 0)}, open: []store.BaselineRun{startedRun()}}
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httpapi.New(func(context.Context) error { return nil }, discard)
	Register(srv, Deps{Store: newFakeStore(), Keys: keysFile(t), Worker: &fakeWaker{}, AuthMode: config.AuthNone,
		Queue: &QueueDeps{Store: st, Worker: fakeWorkerStatus{}}, Log: discard})
	// Sources off: the runs are not counted, as the page does not list them.
	code, body := do(t, srv.Handler(), "GET", "/api/queue/count", "", "", nil)
	if code != 200 || field(t, body, "count") != "1" || st.openRead || st.countRuns {
		t.Errorf("count = %d %v, runs read %v counted %v", code, body, st.openRead, st.countRuns)
	}
	srv = httpapi.New(func(context.Context) error { return nil }, discard)
	Register(srv, Deps{Store: newFakeStore(), Keys: keysFile(t), Worker: &fakeWaker{}, AuthMode: config.AuthNone,
		Queue: &QueueDeps{Store: st, Worker: fakeWorkerStatus{}, Sync: fakeSync{}}, Log: discard})
	if code, body := do(t, srv.Handler(), "GET", "/api/queue/count", "", "", nil); code != 200 || field(t, body, "count") != "2" {
		t.Errorf("sources on: count = %d %v", code, body)
	}
}
