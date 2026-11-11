package api

import (
	"context"
	"time"

	"github.com/trick77/almanach/internal/prompts"
	"github.com/trick77/almanach/internal/store"
)

// The states of a page below its scope's epoch, as the wiki's hint words
// them: what happens next and what a reader can do about it (user,
// 2026-11-10: a clear call to action, and when it happens on its own,
// say so and roughly when).
const (
	staleQueued  = "queued"  // its restyle waits in the queue
	staleRunning = "running" // its restyle runs
	staleWaiting = "waiting" // the poller comes to it on its own
	stalePaused  = "paused"  // the queue is paused: nothing happens
	staleBudget  = "budget"  // the day's budget is spent: tomorrow
	staleManual  = "manual"  // nothing on its own: a button
)

// Why a stale page waits for a button.
const (
	staleOff    = "off"    // the poller restyles nothing on its own
	staleFailed = "failed" // store.RestyleTries restyles failed
)

// staleBody is a page below its scope's epoch, null on a page at it.
type staleBody struct {
	// Epoch is the page's rule level, At today's for its scope.
	Epoch int    `json:"epoch"`
	At    int    `json:"at"`
	State string `json:"state"`
	// Why says what keeps a manual one from going on its own.
	Why string `json:"why,omitempty"`
	// Rank is its place among the pages waiting, 1 next, with waiting.
	Rank int `json:"rank,omitempty"`
	// EtaSeconds is about when the poller comes to it, with waiting and a
	// cycle on: one restyle a cycle, the next cycle first. An estimate.
	EtaSeconds int64 `json:"eta_seconds,omitempty"`
}

// stale words a page the store found below its scope's epoch at.
func (d Deps) stale(ctx context.Context, st store.Stale, at int) (*staleBody, error) {
	out := &staleBody{Epoch: st.Epoch, At: at}
	if st.Run != "" && st.Run != store.RunQueued {
		out.State = staleRunning
		return out, nil
	}
	if d.Queue != nil {
		p, err := d.Queue.Store.WorkerPause(ctx)
		if err != nil {
			return nil, err
		}
		if p != nil {
			out.State = stalePaused
			return out, nil
		}
	}
	spent, err := d.budgetSpent(ctx)
	if err != nil {
		return nil, err
	}
	switch {
	case spent:
		out.State = staleBudget
	case st.Run == store.RunQueued:
		out.State = staleQueued
	case !d.restyleOn():
		out.State, out.Why = staleManual, staleOff
	case st.Failed >= store.RestyleTries:
		out.State, out.Why = staleManual, staleFailed
	default:
		out.State, out.Rank = staleWaiting, st.Rank
		out.EtaSeconds = int64(d.restyleEta(st.Rank, st.Failed, st.FailedAt) / time.Second)
	}
	return out, nil
}

// restyleEta is about how long until the poller queues the restyle of the
// page at rank, the wiki's hint's and the queue page's one sum: the time to
// its next cycle, then a cycle for each page ahead (the poller queues one
// restyle at a time, so a cycle that runs while one is open queues none),
// never before the retry wait of its newest failed restyle, failed of them
// finished, the newest at failedAt (nil for none).
func (d Deps) restyleEta(rank, failed int, failedAt *time.Time) time.Duration {
	every := d.Sources.SyncInterval
	first, now := every, time.Now()
	var sync SyncStatus
	if d.Queue != nil {
		now, sync = d.Queue.now(), d.Queue.Sync
	}
	if sync != nil {
		if next := sync.Cycle().NextAt; next != nil {
			first = max(next.Sub(now), 0)
		}
	}
	wait := first + time.Duration(rank-1)*every
	if failedAt != nil && sync != nil && every > 0 {
		// Queued by the first cycle past the retry wait.
		if at, ok := sync.RestyleRetryAt(*failedAt, failed); ok && at.Sub(now) > wait {
			cycles := (at.Sub(now) - first + every - 1) / every
			wait = first + cycles*every
		}
	}
	return wait
}

// restyleOn reports whether the poller restyles pages on its own: the
// reconcile interval and the cycle on, else only a button does.
func (d Deps) restyleOn() bool {
	return d.Sources != nil && d.Sources.Reconcile > 0 && d.Sources.SyncInterval > 0
}

// budgetSpent reports whether the day's budget is spent: the worker holds
// every run, a restyle too, until the next UTC day.
func (d Deps) budgetSpent(ctx context.Context) (bool, error) {
	if d.Queue == nil || d.Queue.Budget <= 0 {
		return false, nil
	}
	s, err := d.Queue.Store.Spent(ctx, store.DayStart(d.Queue.now()))
	if err != nil {
		return false, err
	}
	return store.OverBudget(s.Budgeted(), d.Queue.Budget), nil
}

// repoStale is a repository page's staleBody, nil at its epoch or with the
// sources off (nothing restyles then).
func (d Deps) repoStale(ctx context.Context, key, slug string) (*staleBody, error) {
	if d.Sources == nil {
		return nil, nil
	}
	e := store.Epochs{Repo: prompts.RepoEpoch, Project: prompts.ProjectEpoch}
	st, ok, err := d.Store.RepoStale(ctx, key, slug, e)
	if err != nil || !ok {
		return nil, err
	}
	return d.stale(ctx, st, e.Repo)
}

// projectStale is a project page's, as repoStale.
func (d Deps) projectStale(ctx context.Context, key string) (*staleBody, error) {
	if d.Sources == nil {
		return nil, nil
	}
	e := store.Epochs{Repo: prompts.RepoEpoch, Project: prompts.ProjectEpoch}
	st, ok, err := d.Store.ProjectStale(ctx, key, e)
	if err != nil || !ok {
		return nil, err
	}
	return d.stale(ctx, st, e.Project)
}
