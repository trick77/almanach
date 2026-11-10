package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/trick77/almanach/internal/clone"
	"github.com/trick77/almanach/internal/store"
)

// A stale page's hint says what happens next: its run first, then a pause
// or a spent budget (nothing moves), then why nothing goes on its own, else
// the poller's turn with about when.
func TestStale_States(t *testing.T) {
	next := queueNow.Add(10 * time.Minute)
	deps := func(q *fakeQueueStore, budget int64, reconcile, interval time.Duration) Deps {
		return Deps{
			Sources: &SourcesDeps{Reconcile: reconcile, SyncInterval: interval},
			Queue: &QueueDeps{Store: q, Budget: budget, Sync: fakeSync{c: clone.CycleStatus{NextAt: &next}},
				Now: func() time.Time { return queueNow }},
		}
	}
	on, every := 720*time.Hour, 30*time.Minute
	for name, c := range map[string]struct {
		d     Deps
		st    store.Stale
		state string
		why   string
		rank  int
		eta   int64
	}{
		"running":           {deps(&fakeQueueStore{pause: &store.Pause{}}, 0, on, every), store.Stale{Run: store.RunProject}, staleRunning, "", 0, 0},
		"queued":            {deps(&fakeQueueStore{}, 0, on, every), store.Stale{Run: store.RunQueued}, staleQueued, "", 0, 0},
		"queued, paused":    {deps(&fakeQueueStore{pause: &store.Pause{}}, 0, on, every), store.Stale{Run: store.RunQueued}, stalePaused, "", 0, 0},
		"queued, budget":    {deps(&fakeQueueStore{}, 1_000_000_000, on, every), store.Stale{Run: store.RunQueued}, staleBudget, "", 0, 0},
		"under the budget":  {deps(&fakeQueueStore{}, 2_000_000_000, on, every), store.Stale{Run: store.RunQueued}, staleQueued, "", 0, 0},
		"reconcile off":     {deps(&fakeQueueStore{}, 0, 0, every), store.Stale{Rank: 1}, staleManual, staleOff, 0, 0},
		"cycle off":         {deps(&fakeQueueStore{}, 0, on, 0), store.Stale{Rank: 1}, staleManual, staleOff, 0, 0},
		"tries spent":       {deps(&fakeQueueStore{}, 0, on, every), store.Stale{Failed: store.RestyleTries, Rank: 1}, staleManual, staleFailed, 0, 0},
		"next":              {deps(&fakeQueueStore{}, 0, on, every), store.Stale{Failed: 1, Rank: 1}, staleWaiting, "", 1, 600},
		"three pages ahead": {deps(&fakeQueueStore{}, 0, on, every), store.Stale{Rank: 4}, staleWaiting, "", 4, 600 + 3*1800},
	} {
		got, err := c.d.stale(context.Background(), c.st, 3)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got.At != 3 || got.State != c.state || got.Why != c.why || got.Rank != c.rank || got.EtaSeconds != c.eta {
			t.Errorf("%s: stale = %+v, want %s/%s rank %d eta %d", name, *got, c.state, c.why, c.rank, c.eta)
		}
	}
	// No cycle reported yet: a whole interval to the first.
	d := Deps{Sources: &SourcesDeps{Reconcile: on, SyncInterval: every}}
	if got, _ := d.stale(context.Background(), store.Stale{Rank: 2}, 3); got.EtaSeconds != 2*1800 {
		t.Errorf("eta without a cycle = %d, want two intervals", got.EtaSeconds)
	}
	// A read that fails is the request's 500.
	bad := deps(&fakeQueueStore{pauseErr: errors.New("db")}, 0, on, every)
	if _, err := bad.stale(context.Background(), store.Stale{}, 3); err == nil {
		t.Error("a failed pause read is no error")
	}
	bad = deps(&fakeQueueStore{spentErr: errors.New("db")}, 1, on, every)
	if _, err := bad.stale(context.Background(), store.Stale{}, 3); err == nil {
		t.Error("a failed spend read is no error")
	}
}

// The pages carry their hint, the project's only when it has a page of its
// own; with the sources off nothing restyles and none is told.
func TestStale_OnThePages(t *testing.T) {
	st := &fakeStore{stale: map[string]store.Stale{"TOOLS/svc": {Epoch: 1, Rank: 2}}}
	d := Deps{Store: st, Sources: &SourcesDeps{Reconcile: time.Hour, SyncInterval: time.Minute}}
	got, err := d.repoStale(context.Background(), "TOOLS", "svc")
	if err != nil || got == nil || got.Epoch != 1 || got.State != staleWaiting || got.Rank != 2 {
		t.Fatalf("repoStale = %+v err=%v", got, err)
	}
	if got, err := d.repoStale(context.Background(), "TOOLS", "web"); got != nil || err != nil {
		t.Fatalf("repoStale of a page at the epoch = %+v err=%v", got, err)
	}
	if got, err := d.projectStale(context.Background(), "TOOLS"); got != nil || err != nil {
		t.Fatalf("projectStale of a page at the epoch = %+v err=%v", got, err)
	}
	st.stale["TOOLS"] = store.Stale{Epoch: 2}
	if got, _ := d.projectStale(context.Background(), "TOOLS"); got == nil || got.Epoch != 2 {
		t.Fatalf("projectStale = %+v", got)
	}
	st.staleErr = errors.New("db")
	if _, err := d.repoStale(context.Background(), "TOOLS", "svc"); err == nil {
		t.Error("a failed read is no error")
	}
	if _, err := d.projectStale(context.Background(), "TOOLS"); err == nil {
		t.Error("a failed read is no error")
	}
	d.Sources = nil
	if got, err := d.repoStale(context.Background(), "TOOLS", "svc"); got != nil || err != nil {
		t.Fatalf("sources off: %+v %v", got, err)
	}
	if got, err := d.projectStale(context.Background(), "TOOLS"); got != nil || err != nil {
		t.Fatalf("sources off: %+v %v", got, err)
	}
}
