package api

import (
	"context"
	"net/http"
	"sort"
	"time"

	"github.com/trick77/almanach/internal/clone"
	"github.com/trick77/almanach/internal/consolidate"
	"github.com/trick77/almanach/internal/store"
	"github.com/trick77/ship/kit/httpx"
)

// The queue page: what the worker does now, what it takes next in the
// order it will take it, and what it finished lately. Read only.

// QueueStore is what the queue page reads of the store.
type QueueStore interface {
	PendingNotes(ctx context.Context, limit int) ([]store.PendingNote, error)
	OpenRuns(ctx context.Context) ([]store.BaselineRun, error)
	RunByID(ctx context.Context, id int64) (store.BaselineRun, bool, error)
	FinishedRuns(ctx context.Context, since time.Time, limit int) ([]store.FinishedRun, error)
	FoldedNotes(ctx context.Context, since time.Time, limit int) ([]store.FoldedNote, error)
	QueueCount(ctx context.Context, runs bool) (int, error)
	RunCalls(ctx context.Context, runID int64) ([]store.RunCall, error)
	RunCosts(ctx context.Context, ids []int64) (map[int64]int64, error)
	Spent(ctx context.Context, since time.Time) (*int64, int, error)
}

// WorkerStatus is the worker as the queue page sees it.
type WorkerStatus interface{ Status() consolidate.Status }

// SyncStatus is the poller as the queue page sees it.
type SyncStatus interface {
	Cycle() clone.CycleStatus
	RetryAt(finished time.Time, failedInRow int) (time.Time, bool)
}

// QueueDeps is what the queue page needs.
type QueueDeps struct {
	Store  QueueStore
	Worker WorkerStatus
	// Sync is nil with the sources off: no runs, no sync.
	Sync SyncStatus
	Now  func() time.Time
}

// recentWindow is how far back the page lists finished work and spend.
const recentWindow = 24 * time.Hour

// recentMax bounds the finished list.
const recentMax = 50

// notesMax bounds the notes the page lists; the rest are counted (More).
const notesMax = 100

type queueBody struct {
	// Current is the work in flight, null when the worker waits.
	Current *queueItemBody  `json:"current"`
	Next    []queueItemBody `json:"next"`
	// Sync is the poller's cycle, null with the sources off.
	Sync   *syncBody    `json:"sync"`
	Spent  spentBody    `json:"spent"`
	Recent []recentBody `json:"recent"`
	// PollSeconds is how often the worker looks on its own.
	PollSeconds int `json:"poll_seconds"`
	// MaxAttempts is the attempt an item is given up at.
	MaxAttempts int `json:"max_attempts"`
	// More is the items queued past the listed ones.
	More int `json:"more"`
}

type queueCountBody struct {
	Count int `json:"count"`
}

// queueCount is the queue's size for the nav: two counts, polled from
// every page, so none of the page's reads.
func (d Deps) queueCount(w http.ResponseWriter, r *http.Request) {
	// Runs count only where the page lists them: with the sources on.
	n, err := d.Queue.Store.QueueCount(r.Context(), d.Queue.Sync != nil)
	if err != nil {
		httpx.ServerError(w, r, err, "queue count")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, queueCountBody{Count: n})
}

// queueItemBody is a note or a run, with what it waits for.
type queueItemBody struct {
	Kind    string `json:"kind"`
	Project string `json:"project"`
	Repo    string `json:"repo"`
	// A note's.
	PRKey      string     `json:"pr_key,omitempty"`
	Title      string     `json:"title,omitempty"`
	ChangeKind string     `json:"change_kind,omitempty"`
	ReceivedAt *time.Time `json:"received_at,omitempty"`
	// HeldBy is the run of its repository the note waits for.
	HeldBy int64 `json:"held_by,omitempty"`
	// A run's.
	Run *queueRunBody `json:"run,omitempty"`
	// Attempts is the gateway failures counted against it so far.
	Attempts int `json:"attempts"`
	// Since is when the worker took it, on the current item only.
	Since *time.Time `json:"since,omitempty"`
	// Wait is why the worker does not take it yet, on the first item and
	// on a run step that waits.
	Wait *waitBody `json:"wait,omitempty"`
}

type queueRunBody struct {
	ID         int64      `json:"id"`
	State      string     `json:"state"`
	Mode       string     `json:"mode"`
	Trigger    string     `json:"trigger"`
	Commit     string     `json:"commit"`
	BaseCommit string     `json:"base_commit"`
	Calls      int        `json:"calls"`
	QueuedAt   time.Time  `json:"queued_at"`
	StartedAt  *time.Time `json:"started_at"`
	CostUSD    string     `json:"cost_usd,omitempty"`
	Steps      []stepBody `json:"steps"`
}

type stepBody struct {
	Kind  string `json:"kind"`
	Name  string `json:"name"`
	Files int    `json:"files"`
	Bytes int64  `json:"bytes"`
	State string `json:"state"`
	// Skip is the reason of a skipped step, a skip code.
	Skip    string     `json:"skip"`
	Calls   int        `json:"calls"`
	Failed  int        `json:"failed"`
	At      *time.Time `json:"at"`
	CostUSD string     `json:"cost_usd,omitempty"`
}

// waitBody is why an item is not taken yet: a hold of the whole worker
// (until set), a run step that waits for its clone or git (since set).
type waitBody struct {
	Reason string     `json:"reason"`
	Until  *time.Time `json:"until,omitempty"`
	Since  *time.Time `json:"since,omitempty"`
}

type syncBody struct {
	Running    bool       `json:"running"`
	Project    string     `json:"project,omitempty"`
	Repo       string     `json:"repo,omitempty"`
	RepoSince  *time.Time `json:"repo_since,omitempty"`
	Done       int        `json:"done"`
	Total      int        `json:"total"`
	FinishedAt *time.Time `json:"finished_at"`
	NextAt     *time.Time `json:"next_at"`
}

type spentBody struct {
	CostUSD string `json:"cost_usd,omitempty"`
	Calls   int    `json:"calls"`
}

// recentBody is a run or a note the worker finished.
type recentBody struct {
	Kind    string    `json:"kind"`
	At      time.Time `json:"at"`
	Project string    `json:"project"`
	Repo    string    `json:"repo"`
	// A note's: its PR, and the merge call's outcome ("" without a call).
	PRKey   string `json:"pr_key,omitempty"`
	Title   string `json:"title,omitempty"`
	Outcome string `json:"outcome,omitempty"`
	// A run's.
	RunID   int64  `json:"run_id,omitempty"`
	State   string `json:"state,omitempty"`
	Skipped int    `json:"skipped,omitempty"`
	Error   string `json:"error,omitempty"`
	// RetryAt is when the poller queues a failed run again at the
	// earliest, on the repository's last run only.
	RetryAt *time.Time `json:"retry_at,omitempty"`
	CostUSD string     `json:"cost_usd,omitempty"`
}

func (d Deps) queue(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	d.reader(r)
	q := d.Queue
	out, err := q.build(ctx)
	if err != nil {
		httpx.ServerError(w, r, err, "queue")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (q *QueueDeps) now() time.Time {
	if q.Now != nil {
		return q.Now()
	}
	return time.Now()
}

func (q *QueueDeps) build(ctx context.Context) (queueBody, error) {
	st := q.Worker.Status()
	out := queueBody{Next: []queueItemBody{}, Recent: []recentBody{}, PollSeconds: int(st.Poll / time.Second),
		MaxAttempts: consolidate.MaxAttempts}
	notes, err := q.Store.PendingNotes(ctx, notesMax)
	if err != nil {
		return out, err
	}
	var runs []store.BaselineRun
	if q.Sync != nil {
		if runs, err = q.Store.OpenRuns(ctx); err != nil {
			return out, err
		}
	}

	// The worker's order, its own (consolidate.QueueOrder).
	order := consolidate.QueueOrder(notes, runs)
	queued := order.Queued
	var free, held []queueItemBody
	for _, n := range order.Free {
		free = append(free, noteItem(n))
	}
	for _, n := range order.Held {
		held = append(held, noteItem(n))
	}
	var startedItem *queueItemBody
	if order.Started != nil {
		it, err := q.runItem(ctx, *order.Started)
		if err != nil {
			return out, err
		}
		startedItem = &it
	}

	// The current item: the note or run step the worker is on.
	if b := st.Busy; b != nil {
		switch b.Kind {
		case consolidate.KindNote:
			for i, it := range free {
				if it.PRKey == b.PRKey && it.Project == b.ProjectKey && it.Repo == b.RepoSlug {
					cur := it
					out.Current = &cur
					free = append(free[:i], free[i+1:]...)
					break
				}
			}
			// Folded between the status and the read of the notes: still
			// the work in flight a moment ago, named by what the status
			// knows of it.
			if out.Current == nil {
				out.Current = &queueItemBody{Kind: consolidate.KindNote, Project: b.ProjectKey, Repo: b.RepoSlug, PRKey: b.PRKey}
			}
		case consolidate.KindRun:
			if startedItem != nil && startedItem.Run.ID == b.RunID {
				out.Current, startedItem = startedItem, nil
			} else {
				// A queued run the worker just took: its first step pins it.
				for i := range queued {
					if queued[i].ID == b.RunID {
						it, err := q.runItem(ctx, queued[i])
						if err != nil {
							return out, err
						}
						out.Current = &it
						queued = append(queued[:i], queued[i+1:]...)
						break
					}
				}
			}
			// Over between the status and the read of the runs (its last
			// step): still the work in flight a moment ago.
			if out.Current == nil {
				r, ok, err := q.Store.RunByID(ctx, b.RunID)
				if err != nil {
					return out, err
				}
				if ok {
					it, err := q.runItem(ctx, r)
					if err != nil {
						return out, err
					}
					out.Current = &it
				}
			}
		}
		if out.Current != nil {
			since := b.Since
			out.Current.Since = &since
		}
	}

	out.Next = append(out.Next, free...)
	if startedItem != nil {
		out.Next = append(out.Next, *startedItem)
	}
	out.Next = append(out.Next, held...)
	for _, r := range queued {
		it, err := q.runItem(ctx, r)
		if err != nil {
			return out, err
		}
		out.Next = append(out.Next, it)
	}

	// Why the next item waits: a hold stops everything; a run step that
	// waits for its clone or git says so on that run.
	if st.Wait != nil {
		for i := range out.Next {
			if run := out.Next[i].Run; run != nil && run.ID == st.Wait.RunID {
				since := st.Wait.Since
				out.Next[i].Wait = &waitBody{Reason: st.Wait.Reason, Since: &since}
			}
		}
	}
	if st.HoldUntil != nil && st.Busy == nil && out.Current == nil && len(out.Next) > 0 {
		out.Next[0].Wait = &waitBody{Reason: st.HoldCause, Until: st.HoldUntil}
	}
	// The notes past notesMax are counted, not listed: a backlog of a long
	// hold is not read and sent every few seconds.
	if len(notes) == notesMax {
		total, err := q.Store.QueueCount(ctx, q.Sync != nil)
		if err != nil {
			return out, err
		}
		shown := len(out.Next)
		if out.Current != nil {
			shown++
		}
		out.More = max(total-shown, 0)
	}

	if q.Sync != nil {
		c := q.Sync.Cycle()
		sb := &syncBody{Running: c.Running, Done: c.Done, Total: c.Total, FinishedAt: c.FinishedAt, NextAt: c.NextAt}
		if c.Running && c.RepoSlug != "" {
			since := c.RepoSince
			sb.Project, sb.Repo, sb.RepoSince = c.ProjectKey, c.RepoSlug, &since
		}
		out.Sync = sb
	}

	since := q.now().Add(-recentWindow)
	cost, calls, err := q.Store.Spent(ctx, since)
	if err != nil {
		return out, err
	}
	out.Spent = spentBody{CostUSD: usd(cost), Calls: calls}
	if out.Recent, err = q.recent(ctx, since); err != nil {
		return out, err
	}
	return out, nil
}

func noteItem(n store.PendingNote) queueItemBody {
	at := n.ReceivedAt
	return queueItemBody{Kind: consolidate.KindNote, Project: n.ProjectKey, Repo: n.RepoSlug, PRKey: n.PRKey, Title: n.PRTitle,
		ChangeKind: n.ChangeKind, ReceivedAt: &at, HeldBy: n.HeldBy, Attempts: n.Attempts}
}

func (q *QueueDeps) runItem(ctx context.Context, r store.BaselineRun) (queueItemBody, error) {
	// A queued run has made no call: no read for it.
	var calls []store.RunCall
	if r.State != store.RunQueued {
		var err error
		if calls, err = q.Store.RunCalls(ctx, r.ID); err != nil {
			return queueItemBody{}, err
		}
	}
	steps, err := consolidate.RunSteps(r, calls)
	if err != nil {
		return queueItemBody{}, err
	}
	body := &queueRunBody{ID: r.ID, State: r.State, Mode: r.Mode, Trigger: r.Trigger, Commit: r.Commit, BaseCommit: r.BaseCommit,
		Calls: r.Calls, QueuedAt: r.QueuedAt, StartedAt: r.StartedAt, Steps: make([]stepBody, 0, len(steps))}
	for _, s := range steps {
		body.Steps = append(body.Steps, stepBody{Kind: s.Kind, Name: s.Name, Files: s.Files, Bytes: s.Bytes, State: s.State,
			Skip: string(s.Skip), Calls: s.Calls, Failed: s.Failed, At: s.At, CostUSD: usd(s.CostNanoUSD)})
	}
	// The run's cost is every call's, as RunCosts sums a finished run,
	// not the steps' (a call no step holds still cost).
	var total int64
	priced := false
	for _, c := range calls {
		if c.CostNanoUSD != nil {
			total, priced = total+*c.CostNanoUSD, true
		}
	}
	if priced {
		body.CostUSD = usd(&total)
	}
	return queueItemBody{Kind: consolidate.KindRun, Project: r.ProjectKey, Repo: r.Slug, Run: body, Attempts: r.Attempts}, nil
}

// recent is the finished runs and notes since, newest first, at most
// recentMax.
func (q *QueueDeps) recent(ctx context.Context, since time.Time) ([]recentBody, error) {
	out := []recentBody{}
	if q.Sync != nil {
		// Each list at most recentMax, the merged one cut to it again: the
		// newest recentMax of both are among them.
		runs, err := q.Store.FinishedRuns(ctx, since, recentMax)
		if err != nil {
			return nil, err
		}
		ids := make([]int64, 0, len(runs))
		for _, r := range runs {
			ids = append(ids, r.ID)
		}
		costs, err := q.Store.RunCosts(ctx, ids)
		if err != nil {
			return nil, err
		}
		for _, r := range runs {
			rb := recentBody{Kind: consolidate.KindRun, At: *r.FinishedAt, Project: r.ProjectKey, Repo: r.Slug, RunID: r.ID,
				State: r.State, Skipped: r.Skipped, Error: r.Error}
			if c, ok := costs[r.ID]; ok {
				rb.CostUSD = usd(&c)
			}
			if r.State == store.RunFailed && r.Latest {
				if at, ok := q.Sync.RetryAt(*r.FinishedAt, r.FailedInRow); ok {
					rb.RetryAt = &at
				}
			}
			out = append(out, rb)
		}
	}
	notes, err := q.Store.FoldedNotes(ctx, since, recentMax)
	if err != nil {
		return nil, err
	}
	for _, n := range notes {
		out = append(out, recentBody{Kind: consolidate.KindNote, At: n.ConsolidatedAt, Project: n.ProjectKey, Repo: n.RepoSlug,
			PRKey: n.PRKey, Title: n.PRTitle, Outcome: n.Outcome, CostUSD: usd(n.CostNanoUSD)})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	if len(out) > recentMax {
		out = out[:recentMax]
	}
	return out, nil
}
