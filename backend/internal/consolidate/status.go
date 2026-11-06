package consolidate

import (
	"errors"
	"slices"
	"time"

	"github.com/trick77/almanach/internal/inference"
	"github.com/trick77/almanach/internal/store"
	"github.com/trick77/toolkit/llmwire"
)

// What the worker is doing right now, for the queue page: the note or run
// step in flight, a hold and why, a run step that waits. In memory only:
// the queue itself is the store's, and a restart ends any hold.

// Why the worker holds.
const (
	// HoldTimeout is a call that ran past its cap.
	HoldTimeout = "timeout"
	// HoldRateLimit is the endpoint asking to slow down.
	HoldRateLimit = "rate_limit"
	// HoldKeyRefused is the endpoint refusing the key (401/403).
	HoldKeyRefused = "key_refused"
	// HoldUnavailable is any other failure of the endpoint: a 5xx, a
	// transport error, a proxy page.
	HoldUnavailable = "unavailable"
	// HoldBusy is the database's write lock taken past its timeout.
	HoldBusy = "busy"
	// HoldWrite is a write the store refused for another reason: a full
	// disk, a read-only file.
	HoldWrite = "write"
)

// Why a run step waits without a hold.
const (
	// WaitClone is the clone held by its sync.
	WaitClone = "clone_busy"
	// WaitPlace is a merge commit git could not place yet.
	WaitPlace = "placing"
	// WaitBudget is the day's budget spent: every run waits for the next
	// UTC day, the notes go on.
	WaitBudget = "budget"
)

// Kinds of work in flight.
const (
	KindNote  = "note"
	KindRun   = "run"
	KindBrief = "brief"
)

// Busy is the work in flight.
type Busy struct {
	Kind       string
	ProjectKey string
	RepoSlug   string
	// PRKey is a note's, the first of a fold's; PRKeys every note the fold
	// takes. RunID a run's.
	PRKey  string
	PRKeys []string
	RunID  int64
	Since  time.Time
}

// Wait is a run step that will be taken again at the next drain.
type Wait struct {
	RunID  int64
	Reason string
	Since  time.Time
}

// Status is the worker's state at one instant.
type Status struct {
	Busy *Busy
	// HoldUntil and HoldCause are set while a hold is on.
	HoldUntil *time.Time
	HoldCause string
	Wait      *Wait
	// Poll is how often the loop looks on its own.
	Poll time.Duration
	// FoldDelay is how long a repository's notes wait from its oldest.
	FoldDelay time.Duration
}

// Status is a copy of what the worker is doing.
func (w *Worker) Status() Status {
	w.smu.Lock()
	defer w.smu.Unlock()
	s := Status{Poll: w.poll, FoldDelay: w.foldDelay, HoldCause: w.holdCause}
	if w.busy != nil {
		b := *w.busy
		b.PRKeys = slices.Clone(b.PRKeys)
		s.Busy = &b
	}
	if w.waiting != nil {
		v := *w.waiting
		s.Wait = &v
	}
	if !w.holdUntil.IsZero() && w.now().Before(w.holdUntil) {
		until := w.holdUntil
		s.HoldUntil = &until
	} else {
		s.HoldCause = ""
	}
	return s
}

func (w *Worker) setBusy(b Busy) {
	b.Since = w.now().UTC()
	w.smu.Lock()
	w.busy = &b
	w.smu.Unlock()
}

func (w *Worker) clearBusy() {
	w.smu.Lock()
	w.busy = nil
	w.smu.Unlock()
}

// setWait marks a run's step as waiting for reason; the first time of the
// same wait stays. Reports whether the wait is new: a line is logged once,
// not every lap of the poll.
func (w *Worker) setWait(runID int64, reason string) bool {
	w.smu.Lock()
	defer w.smu.Unlock()
	if w.waiting != nil && w.waiting.RunID == runID && w.waiting.Reason == reason {
		return false
	}
	w.waiting = &Wait{RunID: runID, Reason: reason, Since: w.now().UTC()}
	return true
}

func (w *Worker) clearWait() {
	w.smu.Lock()
	w.waiting = nil
	w.smu.Unlock()
}

func (w *Worker) setHold(until time.Time, cause string) {
	w.smu.Lock()
	w.holdUntil, w.holdCause = until, cause
	w.smu.Unlock()
}

// Order is the queue as drain takes it: every note it may fold, oldest
// first (NextQueued); the started run's next step (NextRun takes a started
// run before a queued one); the notes waiting for that run, free once it
// is over; the queued runs, oldest first; last the notes whose repository
// is not due yet (the fold delay), soonest first, each with Due set. Kept
// beside drain: a change to what drain takes first changes this too.
type Order struct {
	Free    []store.PendingNote
	Started *store.BaselineRun
	Held    []store.PendingNote
	Queued  []store.BaselineRun
	Waiting []PendingDue
}

// PendingDue is a note whose repository waits for the fold delay, and
// when it is due: its repository's oldest note's arrival plus the delay.
type PendingDue struct {
	store.PendingNote
	Due time.Time
}

// QueueOrder lays notes (PendingNotes' order) and open runs (OpenRuns'
// order) out as drain takes them, delay the fold delay at now: a note
// waits until its repository's oldest has waited the delay (NextQueued).
func QueueOrder(notes []store.PendingNote, runs []store.BaselineRun, delay time.Duration, now time.Time) Order {
	var o Order
	// The notes come oldest first: a repository's first is its oldest.
	oldest := map[string]time.Time{}
	for _, n := range notes {
		k := n.ProjectKey + "/" + n.RepoSlug
		if _, ok := oldest[k]; !ok {
			oldest[k] = n.ReceivedAt
		}
	}
	for _, n := range notes {
		due := oldest[n.ProjectKey+"/"+n.RepoSlug].Add(delay)
		switch {
		// A started run holds its repository's notes whatever their delay:
		// they fold after it, not at a time the page could promise.
		case n.HeldBy != 0:
			o.Held = append(o.Held, n)
		case delay > 0 && due.After(now):
			o.Waiting = append(o.Waiting, PendingDue{PendingNote: n, Due: due})
		default:
			o.Free = append(o.Free, n)
		}
	}
	slices.SortStableFunc(o.Waiting, func(a, b PendingDue) int { return a.Due.Compare(b.Due) })
	// Under the delay the drain folds a due repository's notes together:
	// listed together, repositories in the order of their oldest notes.
	if delay > 0 {
		first := map[string]int{}
		for i, n := range o.Free {
			if _, ok := first[n.ProjectKey+"/"+n.RepoSlug]; !ok {
				first[n.ProjectKey+"/"+n.RepoSlug] = i
			}
		}
		slices.SortStableFunc(o.Free, func(a, b store.PendingNote) int {
			return first[a.ProjectKey+"/"+a.RepoSlug] - first[b.ProjectKey+"/"+b.RepoSlug]
		})
	}
	for i := range runs {
		switch {
		case runs[i].State == store.RunQueued:
			o.Queued = append(o.Queued, runs[i])
		case o.Started == nil:
			o.Started = &runs[i]
		}
	}
	return o
}

// gatewayCause names a gateway-class failure for the queue page.
func gatewayCause(res inference.Call) string {
	if res.Outcome == inference.OutcomeTimedOut {
		return HoldTimeout
	}
	var rl *llmwire.RateLimitError
	if errors.As(res.Err, &rl) || errors.Is(res.Err, llmwire.ErrRateLimited) {
		return HoldRateLimit
	}
	if errors.Is(res.Err, llmwire.ErrAuth) {
		return HoldKeyRefused
	}
	return HoldUnavailable
}
