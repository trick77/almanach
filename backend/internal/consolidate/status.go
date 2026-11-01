package consolidate

import (
	"errors"
	"time"

	"github.com/trick77/almanach/internal/inference"
	"github.com/trick77/llmwire"
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
)

// Why a run step waits without a hold.
const (
	// WaitClone is the clone held by its sync.
	WaitClone = "clone_busy"
	// WaitPlace is a merge commit git could not place yet.
	WaitPlace = "placing"
)

// Kinds of work in flight.
const (
	KindNote = "note"
	KindRun  = "run"
)

// Busy is the work in flight.
type Busy struct {
	Kind       string
	ProjectKey string
	RepoSlug   string
	// PRKey is a note's; RunID a run's.
	PRKey string
	RunID int64
	Since time.Time
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
}

// Status is a copy of what the worker is doing.
func (w *Worker) Status() Status {
	w.smu.Lock()
	defer w.smu.Unlock()
	s := Status{Poll: w.poll, HoldCause: w.holdCause}
	if w.busy != nil {
		b := *w.busy
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
// same wait stays.
func (w *Worker) setWait(runID int64, reason string) {
	w.smu.Lock()
	defer w.smu.Unlock()
	if w.waiting != nil && w.waiting.RunID == runID && w.waiting.Reason == reason {
		return
	}
	w.waiting = &Wait{RunID: runID, Reason: reason, Since: w.now().UTC()}
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
