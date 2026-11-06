// Package consolidate folds notes into the descriptions: one worker, one
// note at a time, oldest first. Serial on purpose: two folds of the same
// project would each rewrite the project description from a base the other
// is replacing. The volume is one note per merged PR; a queue of minutes is
// fine, a wrong description is not.
package consolidate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/trick77/almanach/internal/baseline"
	"github.com/trick77/almanach/internal/config"
	"github.com/trick77/almanach/internal/diagram"
	"github.com/trick77/almanach/internal/heartbeat"
	"github.com/trick77/almanach/internal/inference"
	"github.com/trick77/almanach/internal/lang"
	"github.com/trick77/almanach/internal/markup"
	"github.com/trick77/almanach/internal/prompts"
	"github.com/trick77/almanach/internal/redact"
	"github.com/trick77/almanach/internal/store"
	"github.com/trick77/ship/kit/logging"
	"github.com/trick77/toolkit/llmwire"
)

// Store is what the worker needs of the store.
type Store interface {
	NextQueued(ctx context.Context) (store.QueuedNote, bool, error)
	MarkConsolidated(ctx context.Context, project, repo string, prID int, delivery string) error
	DeferFold(ctx context.Context, project, repo string, prID int, delivery string) (int, error)
	// MarkFoldFailed takes the note delivered by delivery off the queue as
	// failed with its reason, with count an attempt counted, the attempts
	// returned; RequeueFailed puts a repository's failed notes under
	// maxFoldAttempts back on it.
	MarkFoldFailed(ctx context.Context, project, repo string, prID int, delivery, reason string, count bool) (int, error)
	RequeueFailed(ctx context.Context, project, repo string, maxAttempts int) (int, error)
	RequeueFailedBefore(ctx context.Context, before time.Time, maxAttempts int) (int, error)
	CurrentPage(ctx context.Context, scope, project, repo string) (store.Page, bool, error)
	NextStatementID(ctx context.Context, scope, project, repo string) (int, error)
	InsertRevision(ctx context.Context, rev store.NewRevision) (int, error)
	InsertLLMCall(ctx context.Context, c store.LLMCall) (int64, error)
	SourceRepoCount(ctx context.Context, key string) (int, error)
}

// Merger is the model call.
type Merger interface {
	Merge(ctx context.Context, req inference.MergeRequest) inference.MergeResult
}

// Worker is the consolidation loop.
type Worker struct {
	store  Store
	llm    Merger
	limits config.Limits
	log    *slog.Logger
	// wake is poked by the webhook; the loop also polls, so a note queued
	// before a restart is picked up without a webhook.
	wake chan struct{}
	// poll is how often the loop looks on its own.
	poll time.Duration
	// quit asks the loop to end after the fold in flight; cancel cuts that
	// fold off when the grace runs out; done closes when the loop ended.
	quit   chan struct{}
	cancel context.CancelFunc
	done   chan struct{}
	// A gateway-class failure holds the whole worker, not just the note:
	// the next fold would meet the same gateway. retryAt is when the hold
	// ends, backoff how long the next one lasts (doubling, capped); a call
	// that gets through resets both.
	retryAt time.Time
	backoff time.Duration
	now     func() time.Time
	// base is the baseline runs, nil with the sources off. saveFailed is
	// set when a run's progress could not be written: the drain pauses
	// rather than pay for the same step again.
	base       *Baseline
	saveFailed error
	// ancestry is what git said of PR merge commits against the commit of
	// run ancestryRun: the commit is pinned, so the answers hold for the
	// whole run.
	ancestryRun int64
	ancestry    map[string]ancestry
	// placeSince is when each merge commit whose read failed first failed
	// in the run.
	placeSince map[string]time.Time
	// problems is run problemsRun's repository problem areas, read at its
	// first subject step: the cutoff is the pinned commit's, so they hold
	// for the whole run. nil until read.
	problemsRun int64
	problems    []store.RepoProblem
	// libs is run libsRun's repository's shared repositories as its describe steps
	// show them, read at the first step this process runs: a saving of
	// reads, not a pin. A restart mid-run reads them again, and the project
	// step reads its own; a summary that changed between is context, not
	// code, so the steps may see two versions of it. siblings is, read with
	// them, the interfaces the project's other repositories offer.
	libsRun  int64
	libs     string
	siblings string
	// busyRun and busyWrites count the step writes of one run a busy
	// database refused in a row; a written step resets them. In memory:
	// the row's attempts are the gateway's, reset by every answered call,
	// and a busy database would refuse the count's own write.
	busyRun    int64
	busyWrites int
	// smu guards what Status reads from another goroutine: the work in
	// flight, the step that waits, the hold as the queue page shows it.
	smu       sync.Mutex
	busy      *Busy
	waiting   *Wait
	holdUntil time.Time
	holdCause string
	// beat is the loop's last heartbeat.
	beat heartbeat.Beat
}

// WithBaseline gives the worker the baseline runs to drain after the
// notes, and the rule that a note for a repository that is no source is
// ignored. Before Start.
func (w *Worker) WithBaseline(b Baseline) *Worker {
	w.base = &b
	return w
}

// The hold after a gateway-class failure: from backoffMin, doubling to
// backoffMax. A rate limit's Retry-After wins when it is longer.
const (
	backoffMin = 30 * time.Second
	backoffMax = 10 * time.Minute
)

// New builds a worker. It runs nothing until Start.
func New(st Store, llm Merger, limits config.Limits, log *slog.Logger) *Worker {
	return &Worker{store: st, llm: llm, limits: limits, log: log, wake: make(chan struct{}, 1), poll: 30 * time.Second,
		quit: make(chan struct{}), done: make(chan struct{}), now: time.Now}
}

// hold pauses the worker after a gateway-class failure or a database that
// stayed busy past its timeout (cause names which, a Hold code);
// retryAfter is the endpoint's own ask, zero when it made none.
func (w *Worker) hold(ctx context.Context, cause string, retryAfter time.Duration) {
	if w.backoff == 0 {
		w.backoff = backoffMin
	} else {
		w.backoff = min(w.backoff*2, backoffMax)
	}
	wait := max(w.backoff, retryAfter)
	w.retryAt = w.now().Add(wait)
	w.setHold(w.retryAt, cause)
	w.log.WarnContext(ctx, fmt.Sprintf("consolidate: hold (%s) for %s, no fold until then", cause, wait))
}

// holding reports whether a gateway hold is still on.
func (w *Worker) holding() bool { return w.now().Before(w.retryAt) }

// clearHold forgets the hold once a call got through.
func (w *Worker) clearHold() {
	w.backoff, w.retryAt = 0, time.Time{}
	w.setHold(time.Time{}, "")
}

// Wake tells the loop a note was queued. Never blocks: a wake already
// pending is the same wake.
func (w *Worker) Wake() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// Start runs the loop until Stop asks it to end or ctx ends.
func (w *Worker) Start(ctx context.Context) {
	ctx, w.cancel = context.WithCancel(ctx)
	go func() {
		defer close(w.done)
		for {
			w.drain(ctx)
			select {
			case <-ctx.Done():
				return
			case <-w.quit:
				return
			case <-w.wake:
			case <-time.After(w.nextLook()):
			}
		}
	}()
}

// nextLook is how long the loop sleeps: the poll, or less when a hold ends
// sooner, so the next fold starts when the hold says and not up to a poll
// later.
func (w *Worker) nextLook() time.Duration {
	if w.holding() {
		if left := w.retryAt.Sub(w.now()); left < w.poll {
			return left
		}
	}
	return w.poll
}

// Stop ends the loop after the fold in flight: a fold is a paid gateway
// call and its writes, cut off it would be paid for nothing and the note
// folded again after the restart. Past grace the fold is cancelled anyway.
func (w *Worker) Stop(grace time.Duration) {
	close(w.quit)
	select {
	case <-w.done:
	case <-time.After(grace):
		w.log.Warn("consolidate: fold still running after the grace period, cancelling it")
		w.cancel()
		<-w.done
	}
}

// stopping reports whether Stop was called.
func (w *Worker) stopping() bool {
	select {
	case <-w.quit:
		return true
	default:
		return false
	}
}

// drain folds every queued note, oldest first, until the queue is empty,
// a stop was asked for or ctx ends. A fold the model refused takes its
// note off the queue with a log line: a note that fails forever would
// otherwise block every note behind it. A fold the gateway failed, or a
// mark the store refused, ends the drain instead: the same note would come
// back at once, one paid call per lap; the next wake or poll tries again.
//
// Notes come first: a note is a small, timely delta, a run minutes of
// calls. With the baseline on, one step of the oldest open run follows
// when no note is due, and the notes are looked at again after it.
// QueueOrder lays the queue out in this order for the queue page: a
// change here changes it too.
func (w *Worker) drain(ctx context.Context) {
	// The backstop: a fold's and a step's own panics fail their item (Fold,
	// step); one outside them (a source check, a run's failure write) ends
	// the drain with a log line and takes the item it was on off the queue,
	// so it never blocks the rest: none blocks the queue forever.
	var note *store.QueuedNote
	var run *store.BaselineRun
	defer func() {
		if r := recover(); r != nil {
			w.log.ErrorContext(ctx, fmt.Sprintf("consolidate: drain panicked: %v", r), slog.String("stack", string(debug.Stack())))
			w.dropAfterPanic(ctx, note, run, r)
		}
	}()
	w.saveFailed = nil
	// Once per drain, a holding one too (it takes no item); then after
	// every item.
	w.heartbeat()
	w.requeueAged(ctx)
	for ctx.Err() == nil && !w.stopping() && !w.holding() {
		note, run = nil, nil
		q, ok, err := w.store.NextQueued(ctx)
		if err != nil {
			w.log.WarnContext(ctx, "consolidate: queue read failed", logging.Err(err))
			return
		}
		if ok {
			note = &q
			if !w.foldOrIgnore(ctx, q) {
				return
			}
			w.heartbeat()
			continue
		}
		if w.base == nil {
			return
		}
		r, ok, err := w.base.Store.NextRun(ctx)
		if err != nil {
			w.log.WarnContext(ctx, "baseline: run queue read failed", logging.Err(err))
			return
		}
		if !ok {
			return
		}
		loaded := r
		run = &loaded
		if !w.stepRun(ctx, r) {
			return
		}
		w.heartbeat()
	}
}

// dropAfterPanic takes the item a drain panicked on off the queue: the
// note failed, the run failed as it was loaded. A panic here too is only
// logged; the next drain meets the item again.
func (w *Worker) dropAfterPanic(ctx context.Context, note *store.QueuedNote, run *store.BaselineRun, cause any) {
	defer func() {
		if r := recover(); r != nil {
			w.log.ErrorContext(ctx, fmt.Sprintf("consolidate: dropping the item after a panic panicked too: %v", r))
		}
	}()
	switch {
	case note != nil:
		w.failNote(ctx, *note, fmt.Sprintf("panic: %v", cause), false)
	case run != nil:
		w.failRun(ctx, *run, fmt.Sprintf("panicked: %v", cause))
	}
}

// foldOrIgnore folds a note, or with the baseline on takes a note for a
// repository that is no source off the queue unfolded: one queued before
// the sources existed, or for a repository removed since. The source is
// the starting point.
func (w *Worker) foldOrIgnore(ctx context.Context, q store.QueuedNote) bool {
	w.setBusy(Busy{Kind: KindNote, ProjectKey: q.ProjectKey, RepoSlug: q.RepoSlug, PRKey: q.PRKey})
	defer w.clearBusy()
	if w.base != nil {
		known, err := w.base.Store.IsSourceRepo(ctx, q.ProjectKey, q.RepoSlug)
		if err != nil {
			w.log.WarnContext(ctx, "consolidate: source check failed, drain paused", logging.Err(err))
			return false
		}
		if !known {
			w.log.InfoContext(ctx, "consolidate: "+q.PRKey+" ignored: no source repository "+q.ProjectKey+"/"+q.RepoSlug)
			if err := w.store.MarkConsolidated(ctx, q.ProjectKey, q.RepoSlug, q.PRID, q.DeliveryID); err != nil {
				w.log.WarnContext(ctx, "consolidate: mark failed, drain paused", logging.Err(err))
				return false
			}
			return true
		}
	}
	return w.Fold(ctx, q)
}

// maxFoldAttempts bounds how often one note is tried against a gateway
// that keeps failing. Under the backoff that is hours, enough for an
// outage or a key fixed the same day; past it the note is taken off the
// queue with a log line, so one note the gateway always fails cannot hold
// the queue forever. A timed-out call may have been billed: a gateway
// that is consistently slower than the call cap costs up to this many
// merges per note, which the log makes visible.
const maxFoldAttempts = 8

// MaxAttempts is maxFoldAttempts for the queue page: the attempt an item
// is given up at, a note's or a run step's.
const MaxAttempts = maxFoldAttempts

// gatewayClass tells a failure of the gateway as a whole (worth a hold and
// a later retry) from one specific to this call (the note is done). A
// refused key, a 5xx or transport failure, a rate limit, a proxy page, a
// timeout: the gateway. A 4xx (context overflow, rejected format) or a
// 2xx of the wrong shape: this call. retryAfter is the endpoint's own ask.
func gatewayClass(res inference.Call) (gateway bool, retryAfter time.Duration) {
	if res.Outcome == inference.OutcomeTimedOut {
		return true, 0
	}
	if res.Outcome != inference.OutcomeError {
		return false, 0
	}
	var rl *llmwire.RateLimitError
	if errors.As(res.Err, &rl) {
		return true, rl.RetryAfter
	}
	if errors.Is(res.Err, llmwire.ErrBadRequest) || errors.Is(res.Err, llmwire.ErrResponseShape) {
		return false, 0
	}
	return true, 0
}

// Fold runs one note through the merge: the current pages in, a patch over
// each out, applied under the rules of a step's and written as a revision.
// The note is marked consolidated once the gateway answered and the patches
// were applied, whatever they changed. A failure specific to this call (an
// answer off the schema, cut by the output limit, a 4xx; a patch refused
// whole) leaves the note off the queue as failed, with its reason, for the
// repository's next run to queue again (failNote): the knowledge is not
// lost, only late. A gateway-class failure, or a database that stayed busy
// past its timeout, leaves it queued with its attempt counted and holds the
// worker, up to maxFoldAttempts.
// Reports whether the drain may go on: false after a deferred note or a
// mark the store refused, so the same note is not paid for again in the
// same lap.
func (w *Worker) Fold(ctx context.Context, q store.QueuedNote) (next bool) {
	ctx = logging.With(ctx, "team", q.Team, "pr_key", q.PRKey)
	deferred := false
	failed := ""
	defer func() {
		// A panic is a failure specific to this note: failed, logged.
		// Uncaught it ends the process, and every restart meets the same
		// oldest note again.
		if r := recover(); r != nil {
			w.log.ErrorContext(ctx, fmt.Sprintf("consolidate: %s not folded: panic: %v", q.PRKey, r), slog.String("stack", string(debug.Stack())))
			deferred = false
			failed = fmt.Sprintf("panic: %v", r)
		}
		if deferred {
			next = false
			return
		}
		if failed != "" {
			next = w.failNote(ctx, q, failed, false)
			return
		}
		if err := w.store.MarkConsolidated(ctx, q.ProjectKey, q.RepoSlug, q.PRID, q.DeliveryID); err != nil {
			w.log.WarnContext(ctx, "consolidate: mark failed, drain paused", logging.Err(err))
			next = false
			return
		}
		next = true
	}()

	// A read that fails before the call: a busy database defers the note,
	// anything else fails it, never consolidated with nothing folded.
	readFailed := func(what string, err error) {
		w.log.WarnContext(ctx, "consolidate: "+what+" failed", logging.Err(err))
		if deferred = w.deferBusy(ctx, q, err); !deferred {
			failed = what + " failed: " + err.Error()
		}
	}
	repoPage, _, err := w.store.CurrentPage(ctx, store.ScopeRepository, q.ProjectKey, q.RepoSlug)
	if err != nil {
		readFailed("repository page read", err)
		return
	}
	// A project of one has no project page: the prompt shows none and asks
	// for null, and whatever comes is not folded.
	alone, err := w.single(ctx, q.ProjectKey)
	if err != nil {
		readFailed("project source read", err)
		return
	}
	var projectPage store.Page
	if !alone {
		projectPage, _, err = w.store.CurrentPage(ctx, store.ScopeProject, q.ProjectKey, "")
		if err != nil {
			readFailed("project page read", err)
			return
		}
	}

	projectDescription := describe(projectPage)
	switch {
	case alone:
		projectDescription = singleProject
	case projectPage.Collection():
		projectDescription = collectionProject + "\n\n" + projectDescription
	}
	prompt := renderWith(prompts.Merge, q, repoPage, projectDescription, w.limits)
	res := w.llm.Merge(ctx, inference.MergeRequest{Prompt: prompt})
	callID, err := w.store.InsertLLMCall(ctx, store.LLMCall{
		ProjectKey: q.ProjectKey, RepoSlug: q.RepoSlug, SourcePR: q.PRKey, Outcome: res.Outcome.String(),
		Model: res.Cost.Model, InputTokens: res.Cost.InputTokens, CachedTokens: res.Cost.CachedTokens,
		OutputTokens: res.Cost.OutputTokens, CostNanoUSD: res.Cost.NanoUSD, CallID: res.Cost.CallID,
	})
	if err != nil {
		w.log.WarnContext(ctx, "consolidate: call row failed", logging.Err(err))
	}
	w.log.InfoContext(ctx, fmt.Sprintf("consolidate: merge call %s (%s)", res.Outcome, res.Cost.LogLine()))
	if gateway, retryAfter := gatewayClass(res.Call); gateway {
		w.hold(ctx, gatewayCause(res.Call), retryAfter)
		deferred = w.deferNote(ctx, q, res.Outcome.String(), res.Err)
		return
	}
	// The gateway answered: whatever it said, the hold is over.
	w.clearHold()
	switch res.Outcome {
	case inference.OutcomeOK:
	default:
		w.log.WarnContext(ctx, fmt.Sprintf("consolidate: %s not folded: %s", q.PRKey, res.Outcome), logging.Err(res.Err))
		failed = res.Outcome.String()
		if res.Err != nil {
			failed += ": " + res.Err.Error()
		}
		return
	}
	var id *int64
	if callID != 0 {
		id = &callID
	}
	// The repository's patch first; refused, nothing is written and the note
	// fails whole, so its retry folds it once. The project's patch after,
	// and refused it costs only itself: the repository page already carries
	// the note, a second fold would carry it twice, and the project step of
	// the repository's next run folds the page anew anyway. The tree is
	// read only for a patch that drops: it judges nothing else.
	var tree func(string) bool
	if p := res.Merge.Repository; p != nil && len(p.Drop) > 0 {
		tree = w.dropTree(ctx, q)
	}
	refused, err := w.apply(ctx, q, store.ScopeRepository, q.RepoSlug, repoPage, res.Merge.Repository, id, tree)
	if err != nil {
		deferred = w.deferBusy(ctx, q, err)
		return
	}
	if refused != "" {
		failed = refused
		return
	}
	if alone {
		if res.Merge.Project != nil {
			w.log.InfoContext(ctx, "consolidate: project patch left out, a project of one")
		}
		return
	}
	// The project has no one tree: a project drop is the model's word, as
	// on the project step.
	if _, err := w.apply(ctx, q, store.ScopeProject, "", projectPage, res.Merge.Project, id, nil); err != nil {
		if res.Merge.Repository == nil {
			deferred = w.deferBusy(ctx, q, err)
			return
		}
		// A busy database after the repository's patch: deferred, the retry
		// would fold that patch twice, its new statements under new ids.
		// The note is consolidated, the project page waits for the
		// repository's next run; the worker holds.
		w.log.WarnContext(ctx, "consolidate: project page left unchanged, the repository's patch kept", logging.Err(err))
		w.hold(ctx, HoldBusy, 0)
	}
	return
}

// failedRetry is how long a failed note waits before it is queued again by
// time alone, for a note no run requeues: the sources off, a repository
// without runs, a project page another repository's run wrote.
const failedRetry = 24 * time.Hour

// requeueAged puts the failed notes older than failedRetry back on the
// queue, those under the bound, at the start of every drain: one update
// over an indexed few rows.
func (w *Worker) requeueAged(ctx context.Context) {
	n, err := w.store.RequeueFailedBefore(ctx, w.now().Add(-failedRetry), maxFoldAttempts)
	if err != nil {
		w.log.WarnContext(ctx, "consolidate: aged failed notes not queued again", logging.Err(err))
		return
	}
	if n > 0 {
		w.log.InfoContext(ctx, fmt.Sprintf("consolidate: %s queued again after %s", plural(n, "failed note"), failedRetry))
	}
}

// dropTree is the clone's tree a fold judges a drop against: the tree at
// the clone's head where the head holds the note's merge commit, the code
// as it is with the pull request in it. A file the pull request took away
// is one the tree lacks; a file added after it, which a later run may have
// written a statement on, is still there, so that statement stays even
// when a note folded late drops it. Never the tree at the merge commit
// itself: it lacks every file added since. nil when there is none to ask:
// the sources off, a note without a merge commit, a clone not yet fetched
// to the merge, busy or gone; then a drop is the model's word
// (baseline.ApplyMerge), as it was when the answer was the whole page.
// Each git read under the clone's lock, never across the call.
func (w *Worker) dropTree(ctx context.Context, q store.QueuedNote) func(string) bool {
	if w.base == nil || q.MergeCommit == "" {
		return nil
	}
	none := func(why string, err error) func(string) bool {
		if errors.Is(err, ErrCloneBusy) {
			w.log.DebugContext(ctx, "consolidate: clone busy, drops on the model's word")
		} else {
			w.log.InfoContext(ctx, "consolidate: "+why+", drops on the model's word", logging.Err(err))
		}
		return nil
	}
	repos, err := w.base.Store.SourceRepos(ctx, q.ProjectKey)
	if err != nil {
		return none("source repos read failed", err)
	}
	head := ""
	for _, rp := range repos {
		if rp.Slug == q.RepoSlug {
			head = rp.HeadCommit
		}
	}
	if head == "" {
		return none("the clone has no head", nil)
	}
	in, err := w.base.Code.Contains(ctx, q.ProjectKey, q.RepoSlug, q.MergeCommit, head)
	if err != nil || !in {
		// The usual cause: the poller has not fetched the merge yet.
		return none("the clone's head lacks the merge commit", err)
	}
	tree, err := w.base.Code.Tree(ctx, q.ProjectKey, q.RepoSlug, head)
	if err != nil {
		return none("no tree at the clone's head", err)
	}
	return baseline.Exists(tree)
}

// failNote takes a note the call failed off the queue as failed, its reason
// kept, the attempt counted: the repository's next run queues it again
// (requeueFailed), so the knowledge the note carries is folded late, not
// lost. Past maxFoldAttempts in all it is given up, logged. counted is an
// attempt DeferFold counted already, the last a gateway failed: counted
// twice, the note would show one more than was made. Reports whether the
// drain may go on: false when the store refused the mark, so the same note
// is not paid for again in the same lap.
func (w *Worker) failNote(ctx context.Context, q store.QueuedNote, reason string, counted bool) bool {
	// Redacted before the cut: a credential cut short is shorter than its
	// shape, and the store's redaction would miss it.
	reason = truncate(redact.Text(reason), reasonMaxRunes)
	attempts, err := w.store.MarkFoldFailed(ctx, q.ProjectKey, q.RepoSlug, q.PRID, q.DeliveryID, reason, !counted)
	if err != nil {
		w.log.WarnContext(ctx, "consolidate: failure mark failed, drain paused", logging.Err(err))
		return false
	}
	if attempts == 0 {
		w.log.InfoContext(ctx, "consolidate: "+q.PRKey+" consolidated or replaced meanwhile, failure not marked")
		return true
	}
	if attempts >= maxFoldAttempts {
		w.log.WarnContext(ctx, fmt.Sprintf("consolidate: %s given up after %d failed attempts: %s", q.PRKey, attempts, reason))
		return true
	}
	w.log.WarnContext(ctx, fmt.Sprintf("consolidate: %s not folded: %s, attempt %d of %d, queued again by the repository's next run",
		q.PRKey, reason, attempts, maxFoldAttempts))
	return true
}

// reasonMaxRunes bounds a failure's reason as stored and shown: an error
// may carry a gateway's whole body.
const reasonMaxRunes = 300

// truncate cuts s to at most n runes, an ellipsis last.
func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	runes := []rune(s)
	return string(runes[:n-1]) + "…"
}

// requeueFailed puts a repository's failed notes back on the queue, those
// under maxFoldAttempts, after one of its runs ended: the page they failed
// on has been written anew, and its run is over, so they fold next. A store
// that refuses is logged; the next run tries again.
func (w *Worker) requeueFailed(ctx context.Context, project, repo string) {
	n, err := w.store.RequeueFailed(ctx, project, repo, maxFoldAttempts)
	if err != nil {
		w.log.WarnContext(ctx, "consolidate: failed notes not queued again", logging.Err(err))
		return
	}
	if n > 0 {
		w.log.InfoContext(ctx, fmt.Sprintf("consolidate: %s queued again after the run", plural(n, "failed note")))
	}
}

// single reports whether a project is a project of one: its source holds
// one repository, and the sources are on (the baseline with them). With
// them off there are no sources, and every project keeps its page.
func (w *Worker) single(ctx context.Context, key string) (bool, error) {
	if w.base == nil {
		return false, nil
	}
	n, err := w.store.SourceRepoCount(ctx, key)
	return store.Single(n), err
}

// singleProject stands in the merge prompt for the project description of
// a project of one: its repository's page is the project's.
const singleProject = "_None: this project is one repository, it has no project description. Answer `\"project\": null`._"

// collectionProject heads the merge prompt's project description of a
// collection: its page is a summary alone, which a note seldom changes.
const collectionProject = "_This project is a collection: repositories that each stand on their own, grouped for another reason. " +
	"Its description is a short summary alone, no statements. Answer `\"project\": null` unless this note changes what the project " +
	"holds; then the summary alone, `statements` and `added` empty._"

// deferNote leaves a note queued with its failed attempt counted, after a
// hold. Once maxFoldAttempts is reached the note is given up: off the queue
// as failed with its reason (failNote), never consolidated, so what its
// pull request taught is not thrown away for a gateway's bad day. Always
// true: the worker holds, the drain ends either way.
func (w *Worker) deferNote(ctx context.Context, q store.QueuedNote, outcome string, cause error) bool {
	attempts, err := w.store.DeferFold(ctx, q.ProjectKey, q.RepoSlug, q.PRID, q.DeliveryID)
	if err != nil {
		w.log.WarnContext(ctx, "consolidate: defer failed, drain paused", logging.Err(err))
		return true
	}
	if attempts == 0 {
		// A later delivery replaced the note while it was folded: that one
		// is queued with no attempt against it.
		w.log.InfoContext(ctx, fmt.Sprintf("consolidate: %s not folded: %s, delivered again meanwhile, the new note kept for after the hold",
			q.PRKey, outcome), logging.Err(cause))
		return true
	}
	if attempts < maxFoldAttempts {
		w.log.WarnContext(ctx, fmt.Sprintf("consolidate: %s not folded: %s, attempt %d of %d, kept for after the hold",
			q.PRKey, outcome, attempts, maxFoldAttempts), logging.Err(cause))
		return true
	}
	reason := outcome
	if cause != nil {
		reason += ": " + cause.Error()
	}
	w.failNote(ctx, q, reason, true)
	return true
}

// deferBusy is a store error met during a fold. A database that stayed
// busy past its timeout is temporary, like a gateway failure: the worker
// holds and the note is tried again, its attempt counted. Anything else
// reports false: the caller fails the note.
func (w *Worker) deferBusy(ctx context.Context, q store.QueuedNote, err error) bool {
	if !store.IsBusy(err) {
		return false
	}
	w.hold(ctx, HoldBusy, 0)
	return w.deferNote(ctx, q, "database busy", err)
}

// apply takes one description's patch under the rules of a fold
// (baseline.ApplyMerge: an item that breaks a rule costs only itself) and
// writes the page as a revision, or logs why not. nil = the model wants no
// change. exists is the tree a drop is judged against, nil for none.
// refused is why the patch as a whole could not be taken (a page left
// without a summary; a store failure; a bug past ApplyMerge), the page
// unchanged, the note failed. The error is only ever a busy database's
// (store.IsBusy): the caller defers the note.
func (w *Worker) apply(ctx context.Context, q store.QueuedNote, scope, repo string, current store.Page, p *inference.Patch, callID *int64,
	exists func(string) bool) (refused string, err error) {
	if p == nil {
		w.log.InfoContext(ctx, "consolidate: "+scope+" description unchanged")
		return "", nil
	}
	at := "consolidate: " + scope
	patch := *p
	// A collection's page is its summary alone: code holds what the prompt
	// says (collectionProject).
	if scope == store.ScopeProject && current.Collection() {
		if n := len(patch.Statements) + len(patch.Added) + len(patch.Drop); n > 0 {
			w.log.WarnContext(ctx, at+": statements of a collection left out", slog.Int("statements", n))
		}
		patch.Statements, patch.Added, patch.Drop = []inference.Statement{}, nil, nil
	}
	patch.Statements, patch.Added = w.asNew(ctx, at, current.Statements, patch.Statements, patch.Added)
	// A bugfix note is a pitfall's evidence on the merge path; any other
	// note backs none.
	if q.ChangeKind != "bugfix" {
		patch.Added = unbackedPitfalls(ctx, w.log, at, patch.Added)
	}
	applied := baseline.ApplyMerge(current.Statements, patch, exists, validFor(scope))
	if len(applied.Unknown) > 0 {
		w.log.WarnContext(ctx, at+": unknown ids left out", slog.Any("ids", applied.Unknown))
	}
	next, err := w.store.NextStatementID(ctx, scope, q.ProjectKey, repo)
	if err != nil {
		w.log.WarnContext(ctx, at+" statement id read failed, page unchanged", logging.Err(err))
		return storeFailed(scope+" statement id read", err)
	}
	src := store.Source{PRKey: q.PRKey, TicketKey: q.TicketKey, MergedAt: q.MergedAt, Commit: q.MergeCommit}
	// ApplyMerge left out what attach would refuse, so a reason here is a
	// bug, and costs the fold.
	statements, leftOut, reason := attach(scope, current.Statements, applied.Statements, src, next, limitsOf(w.limits))
	if reason != "" {
		w.log.WarnContext(ctx, fmt.Sprintf("%s: patch refused (%s), page unchanged", at, reason))
		return scope + " patch refused: " + reason, nil
	}
	leftOut.badEdits = append(applied.BadEdits, leftOut.badEdits...)
	leftOut.badNew, leftOut.badDrops = applied.BadNew, applied.BadDrops
	summary := current.Summary
	if applied.Summary != nil {
		summary = proseSummary(*applied.Summary)
	}
	// A page needs a summary, the one thing a description must have: a
	// first page whose patch brings none is refused, the note failed, so
	// the repository's run writes the summary and the note folds after.
	chars, emptyReason := check(summary, statements)
	if emptyReason != "" {
		w.log.WarnContext(ctx, fmt.Sprintf("%s: patch refused (%s), page unchanged", at, emptyReason))
		return scope + " patch refused: " + emptyReason, nil
	}
	w.logDropped(ctx, at, current.Statements, applied.Statements)
	logLeftOut(ctx, w.log, at, leftOut)
	if current.Summary == summary && sameStatements(current.Statements, statements) {
		w.log.InfoContext(ctx, at+" description identical, no revision")
		return "", nil
	}
	// The map is the baseline project step's alone: a note's fold carries
	// the project's forward unchanged (a repository page has none).
	n, err := w.store.InsertRevision(ctx, store.NewRevision{
		Scope: scope, ProjectKey: q.ProjectKey, RepoSlug: repo, Summary: summary, Statements: statements,
		Chars: chars, SourcePR: q.PRKey, LLMCallID: callID, Map: current.Map,
	})
	if err != nil {
		w.log.WarnContext(ctx, at+" revision write failed", logging.Err(err))
		return storeFailed(scope+" revision write", err)
	}
	w.log.InfoContext(ctx, fmt.Sprintf("%s description revision %d (%d statements, %d chars) from %s",
		at, n, len(statements), chars, q.PRKey))
	return "", nil
}

// logDropped names what a fold's patch took off the page: how many, and
// the ones a pull request taught, dropped on the model's word.
func (w *Worker) logDropped(ctx context.Context, at string, before []store.Statement, after []inference.Statement) {
	kept := make(map[string]bool, len(after))
	for _, s := range after {
		if s.ID != nil {
			kept[*s.ID] = true
		}
	}
	var gone, taught []string
	for _, s := range before {
		if kept[s.ID] {
			continue
		}
		gone = append(gone, s.ID)
		if baseline.FromPR(s) {
			taught = append(taught, s.ID)
		}
	}
	if len(gone) > 0 {
		w.log.InfoContext(ctx, at+": statements dropped", slog.Any("ids", gone), slog.Any("taught_by_pull_requests", taught))
	}
}

// storeFailed is apply's answer to a store failure the caller has logged:
// a busy database's error passed on (the note deferred), anything else a
// refusal (the note failed, never consolidated with nothing written).
func storeFailed(what string, err error) (string, error) {
	if store.IsBusy(err) {
		return "", err
	}
	return what + " failed: " + err.Error(), nil
}

// The statement kinds a page of each scope may hold: a project page no
// extension, which is a change recipe inside one repository.
var kinds = map[string]map[string]bool{
	store.ScopeRepository: kindSet(inference.RepoKinds),
	store.ScopeProject:    kindSet(inference.ProjectKinds),
}

func kindSet(list []string) map[string]bool {
	out := make(map[string]bool, len(list))
	for _, k := range list {
		out[k] = true
	}
	return out
}

// unbackedPitfalls leaves out the new pitfalls a fold or step had no
// evidence for (a bugfix note, a problem area): the prompts say so, code
// holds it. Only new ones: an existing pitfall was backed when written. Each
// left out is logged by name; the rest of the answer stands.
func unbackedPitfalls(ctx context.Context, log *slog.Logger, where string, added []inference.Statement) []inference.Statement {
	kept := make([]inference.Statement, 0, len(added))
	for _, s := range added {
		if s.Kind == "pitfall" {
			log.WarnContext(ctx, where+": pitfall without evidence left out", slog.String("name", s.Name))
			continue
		}
		kept = append(kept, s)
	}
	return kept
}

// asNew is an answer's edits and new statements for a page without
// statements: there is nothing to edit there, so an edit is the model
// numbering what it writes, the mistake the empty page's note warns of (an
// eval's project step was refused whole for "unknown statement id s2" so).
// Its edits join the new statements, their ids cleared by ApplyPatch,
// logged; an edit repeating a new statement's or an earlier
// edit's kind and name (Swiss) is that statement twice and goes, logged.
// With nothing on the page an edit could delete, an id used twice moves
// too. On a page with statements nothing moves. Every path that applies an
// answer to a page calls it first.
func (w *Worker) asNew(ctx context.Context, where string, current []store.Statement, edits, added []inference.Statement) ([]inference.Statement, []inference.Statement) {
	if len(current) > 0 || len(edits) == 0 {
		return edits, added
	}
	named := map[string]bool{}
	for _, a := range added {
		named[a.Kind+"\x00"+lang.Prose(a.Name)] = true
	}
	moved := make([]inference.Statement, 0, len(edits)+len(added))
	var twice []string
	for _, e := range edits {
		key := e.Kind + "\x00" + lang.Prose(e.Name)
		if named[key] {
			twice = append(twice, e.Name)
			continue
		}
		named[key] = true
		moved = append(moved, e)
	}
	if len(twice) > 0 {
		w.log.InfoContext(ctx, fmt.Sprintf("%s: edit repeating a statement left out: %s", where, strings.Join(twice, ", ")))
	}
	w.log.InfoContext(ctx, fmt.Sprintf("%s: %s on a page without statements taken as new", where, plural(len(moved), "edit")))
	return []inference.Statement{}, append(moved, added...)
}

// validFor is the check a statement as returned must pass to stand on a
// scope's page, "" when it does: a kind the page holds, a name and a text.
// A patch leaves a statement that fails it out (baseline.ApplyPatch,
// ApplyMerge); attach refuses over one, which is then a bug.
func validFor(scope string) func(inference.Statement) string {
	return func(p inference.Statement) string {
		if !kinds[scope][p.Kind] {
			return "statement kind " + p.Kind + " not on a " + scope + " page"
		}
		if s := cleanStatement(p); s.Name == "" || s.Text == "" {
			return "empty statement"
		}
		return ""
	}
}

// attach turns the model's statements into stored ones: a kept id carries
// its sources forward and gains src (this PR, or a baseline run's commit)
// when the text changed; a new one starts with src, numbered from next (one
// past every id the page's history used, so a dropped id is never
// reissued). An id the current page does not have is the model inventing
// one, an id edited twice a muddle: rejected. On a merge, a whole page,
// leaving such an edit out would delete what it meant to edit; a step's
// patch leaves it out before (baseline.ApplyPatch), its unnamed ids kept.
// Spelling is Swiss on every prose field. A diagram is held
// to baseline.CheckDiagram; one that breaks a rule, or one past the page's
// count, is left out (its statement kept) and named in leftOut: one bad
// diagram costs that diagram, never the statements of a whole part. A
// stored diagram stays beside an unchanged name and text and goes when
// they changed unless returned again (statementDiagram), named in leftOut
// too. A changed diagram is a changed statement, like changed text. The
// commit a statement was last checked against stays while its name, text
// and diagram do; a change voids it, the caller sets a new one when it read
// the code. A new or changed statement validFor refuses (a kind the
// scope's page may not hold, an empty name or text) rejects a merge's
// rewrite; a step's patch has left those out before (baseline.ApplyPatch).
// A statement returned as stored is never held to it: one written before a
// rule stays.
func attach(scope string, current []store.Statement, proposed []inference.Statement, src store.Source, next int, dl diagramLimits) (out []store.Statement, leftOut leftOutParts, reason string) {
	valid := validFor(scope)
	byID := map[string]store.Statement{}
	for _, s := range current {
		byID[s.ID] = s
	}
	out = make([]store.Statement, 0, len(proposed))
	olds := make([]*store.Statement, 0, len(proposed))
	seen := map[string]bool{}
	for _, p := range proposed {
		var old *store.Statement
		if p.ID != nil && *p.ID != "" {
			o, ok := byID[*p.ID]
			if !ok {
				return nil, leftOutParts{}, "unknown statement id " + *p.ID
			}
			if seen[*p.ID] {
				return nil, leftOutParts{}, "statement id used twice " + *p.ID
			}
			seen[*p.ID] = true
			old = &o
		}
		asStored := old != nil && p.Kind == old.Kind && p.Name == old.Name && p.Text == old.Text
		if why := valid(p); why != "" && !asStored {
			return nil, leftOutParts{}, why
		}
		s := cleanStatement(p)
		// No cap on a name or on the file count: a statement is never left
		// out for its size (user, 2026-11-01). A path no tree can hold
		// leaves a new or changed file list, the statement kept; a list
		// stored so stays as it is.
		// An edit whose every path is bad keeps the stored list rather than
		// lose all its files.
		if old == nil || !slices.Equal(old.Files, s.Files) {
			kept, bad := splitPaths(s.Files)
			if len(bad) > 0 {
				leftOut.paths = append(leftOut.paths, fmt.Sprintf("%q (%s)", s.Name, strings.Join(bad, ", ")))
			}
			if len(kept) == 0 && len(bad) > 0 && old != nil {
				kept = old.Files
			}
			s.Files = kept
		}
		// A returned flow loses its self and repeated edges first, as the
		// map does: one such edge cost a whole diagram.
		flow, dropped := p.Diagram, []string(nil)
		if flow != nil && flow.Kind == diagram.KindFlow {
			flow, dropped = baseline.RepairEdges(flow)
		}
		d, stale, why := statementDiagram(flow, old, s, dl.caps)
		if len(dropped) > 0 && why == "" {
			leftOut.edges = append(leftOut.edges, fmt.Sprintf("%q (%s)", s.Name, strings.Join(dropped, ", ")))
		}
		if why != "" {
			leftOut.invalid = append(leftOut.invalid, fmt.Sprintf("%q (%s)", s.Name, why))
		}
		if stale {
			leftOut.stale = append(leftOut.stale, s.Name)
		}
		s.Diagram = d
		out = append(out, s)
		olds = append(olds, old)
	}
	leftOut.overCount = stripOverCount(out, olds, max(dl.max, countDiagrams(current)))
	for i := range out {
		s, old := &out[i], olds[i]
		if old != nil {
			s.ID = old.ID
			s.Sources = old.Sources
			// An area is no text: none returned keeps the stored one, and
			// a new one alone credits nothing.
			if s.Area == "" {
				s.Area = old.Area
			}
			// Umlauts written in where old text had ae, oe, ue are spelling,
			// a list or a bold term where there was a sentence is layout:
			// not something this source taught, stored, never credited.
			if !sameText(old.Text, s.Text) || !lang.SameButSpelling(old.Name, s.Name) || !diagram.Equal(old.Diagram, s.Diagram) {
				s.Sources = appendSource(s.Sources, src)
			} else {
				s.Checked = old.Checked
			}
		} else {
			s.ID = fmt.Sprintf("s%d", next)
			next++
			s.Sources = []store.Source{src}
		}
	}
	// Every page written is laid out by its areas: a statement given an
	// area joins its own, wherever the answer put it.
	return baseline.Arrange(out), leftOut, ""
}

// cleanStatement is a returned statement's kind, name, text and files as
// attach stores them: trimmed, Swiss, empty paths dropped, the text in
// the stored layout (lang.Laid).
func cleanStatement(p inference.Statement) store.Statement {
	return store.Statement{Kind: p.Kind, Name: lang.Prose(p.Name), Text: lang.Laid(p.Text), Files: cleanFiles(p.Files), Area: baseline.CleanArea(p.Area)}
}

// proseSummary is a returned summary as stored: a statement's layout,
// sub-headings and all (lang.Laid).
func proseSummary(s string) string { return lang.Laid(s) }

// sameText reports whether a statement's new text says what its old one
// did: umlauts spelled in (lang.SameButSpelling) or only laid out anew,
// a paragraph turned into a list, a term set in bold (markup.Words). The
// old text was stored laid out (lang.Laid), as the new one is.
func sameText(old, text string) bool {
	return lang.SameButSpelling(old, text) || lang.SameButSpelling(markup.Words(old), markup.Words(text))
}

// diagramLimits is what a page's statement diagrams are held to: the caps
// per diagram, and how many a page may hold.
type diagramLimits struct {
	caps baseline.DiagramCaps
	max  int
}

func limitsOf(l config.Limits) diagramLimits {
	return diagramLimits{caps: baseline.DiagramCaps{MaxNodes: l.DiagramMaxNodes, MaxEdges: l.DiagramMaxEdges}, max: l.MaxDiagrams}
}

// statementDiagram is the diagram a statement keeps, s its new name and
// text. Carried by id in code, like provenance: null keeps the stored one
// while the name and text stay, {"kind": "none"} removes it, a new one
// replaces it, the stored one returned keeps it. Whenever no diagram the
// model returned is taken, null or one that breaks a rule, a stored one
// beside a changed name or text goes (stale): it would picture what is no
// longer said. Only a new or changed diagram is held to the caps of today:
// one stored under higher caps stays as it is, or lowering a cap would
// freeze every page that has one. why is the rule a returned one broke.
func statementDiagram(proposed *diagram.Diagram, old *store.Statement, s store.Statement, caps baseline.DiagramCaps) (d *diagram.Diagram, stale bool, why string) {
	if proposed != nil && proposed.Kind == diagram.KindNone {
		return nil, false, ""
	}
	if old != nil && proposed != nil && diagram.Equal(proposed, old.Diagram) {
		return old.Diagram, false, ""
	}
	if proposed != nil {
		if d, why = baseline.CheckDiagram(proposed, caps); why == "" {
			return d, false, ""
		}
	}
	// No returned diagram taken: the stored one, while it still fits.
	if old == nil || old.Diagram == nil {
		return nil, false, why
	}
	if lang.SameButSpelling(old.Name, s.Name) && sameText(old.Text, s.Text) {
		return old.Diagram, false, why
	}
	return nil, true, why
}

// stripOverCount holds the page to allowed diagrams (the cap, or the
// page's count when a lowered cap left it higher, so it may keep them but
// not grow): the diagrams added past it, last first, are stripped, their
// statements kept. One diagram too many costs that diagram, never the
// rewrite or the run's reading. A diagram a statement already had never
// counts as added, so the page's own are kept. Returns the statements
// stripped, by name.
func stripOverCount(out []store.Statement, olds []*store.Statement, allowed int) []string {
	over := countDiagrams(out) - allowed
	var stripped []string
	for i := len(out) - 1; i >= 0 && over > 0; i-- {
		added := out[i].Diagram != nil && (olds[i] == nil || olds[i].Diagram == nil)
		if added {
			out[i].Diagram = nil
			stripped = append(stripped, out[i].Name)
			over--
		}
	}
	slices.Reverse(stripped)
	return stripped
}

// leftOutParts is what attach left out of the statements it kept: the
// diagrams, by name past the page's count, by name and reason for breaking
// a rule, by name gone stale beside a changed name or text; the paths no
// tree can hold taken off a statement's files, by name and path (paths);
// the edits, new statements and drops a step's patch left out, with their
// reasons (badEdits, badNew, badDrops: baseline.ApplyPatch); the self and
// repeated edges taken out of a flow that passed its check, by name and
// edge (edges).
type leftOutParts struct {
	overCount, invalid, stale []string
	paths, badEdits, edges    []string
	badNew, badDrops          []string
}

// logLeftOut says which statements lost a diagram or a path, and which
// items of a step's patch were left out, and why. Called once the rewrite
// is taken: a refused one logs its refusal only.
func logLeftOut(ctx context.Context, log *slog.Logger, at string, s leftOutParts) {
	if len(s.badEdits) > 0 {
		log.WarnContext(ctx, fmt.Sprintf("%s: edit left out: %s", at, strings.Join(s.badEdits, ", ")))
	}
	if len(s.badDrops) > 0 {
		log.WarnContext(ctx, fmt.Sprintf("%s: drop left out: %s", at, strings.Join(s.badDrops, ", ")))
	}
	if len(s.badNew) > 0 {
		log.WarnContext(ctx, fmt.Sprintf("%s: new statement left out: %s", at, strings.Join(s.badNew, ", ")))
	}
	if len(s.paths) > 0 {
		log.WarnContext(ctx, fmt.Sprintf("%s: path over %d characters or with whitespace taken off, statement kept: %s", at, pathMaxChars, strings.Join(s.paths, "; ")))
	}
	if len(s.invalid) > 0 {
		log.WarnContext(ctx, fmt.Sprintf("%s: diagram breaking a rule left out: %s", at, strings.Join(s.invalid, ", ")))
	}
	if len(s.edges) > 0 {
		log.InfoContext(ctx, fmt.Sprintf("%s: self or repeated edges taken out: %s", at, strings.Join(s.edges, ", ")))
	}
	if len(s.stale) > 0 {
		log.InfoContext(ctx, fmt.Sprintf("%s: stored diagram dropped with a changed name or text: %s", at, strings.Join(s.stale, ", ")))
	}
	if len(s.overCount) > 0 {
		log.WarnContext(ctx, fmt.Sprintf("%s: diagram over the page's count left out: %s", at, strings.Join(s.overCount, ", ")))
	}
}

func countDiagrams(statements []store.Statement) int {
	n := 0
	for _, s := range statements {
		if s.Diagram != nil {
			n++
		}
	}
	return n
}

// appendSource adds s unless the statement has it already: a PR once, a
// commit once, told apart by kind.
func appendSource(sources []store.Source, s store.Source) []store.Source {
	for _, have := range sources {
		if have.Key() == s.Key() {
			return sources
		}
	}
	return append(append([]store.Source(nil), sources...), s)
}

func cleanFiles(files []string) []string {
	out := []string{}
	for _, f := range files {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// pathMaxChars is the longest path a statement's files keep: past it, or
// with whitespace, it is no path of a tree, the same rule noergler holds a
// note's files to.
const pathMaxChars = 200

// check refuses an empty summary, the one thing a description must have;
// there is no ceiling (user, 2026-11-01). chars is the prose count, stored
// on the revision row.
func check(summary string, statements []store.Statement) (int, string) {
	if strings.TrimSpace(summary) == "" {
		return 0, "empty summary"
	}
	return prose(summary, statements), ""
}

// splitPaths parts files, in one pass, into the paths a tree can hold and
// those it cannot: over pathMaxChars or with whitespace.
func splitPaths(files []string) (kept, bad []string) {
	kept = make([]string, 0, len(files))
	for _, f := range files {
		if utf8.RuneCountInString(f) > pathMaxChars || strings.ContainsAny(f, " \t\n") {
			bad = append(bad, f)
			continue
		}
		kept = append(kept, f)
	}
	return kept, bad
}

// sameStatement compares what a reader sees of a stored statement and a
// returned one: id, kind, name, text (both laid out: a store read relays
// the stored one), files and diagram, not sources or last check.
func sameStatement(stored, returned store.Statement) bool {
	return stored.ID == returned.ID && stored.Kind == returned.Kind && stored.Name == returned.Name &&
		stored.Text == returned.Text &&
		slices.Equal(stored.Files, returned.Files) && diagram.Equal(stored.Diagram, returned.Diagram)
}

// sameStatements is sameStatement over a page's stored and returned
// statements, by id: an answer that only lays the page out anew (another
// order, other areas) changed no text and writes no revision; the map
// step lays it out in place (baseline.ApplyOutline).
func sameStatements(stored, returned []store.Statement) bool {
	if len(stored) != len(returned) {
		return false
	}
	byID := make(map[string]store.Statement, len(returned))
	for _, s := range returned {
		byID[s.ID] = s
	}
	for _, s := range stored {
		r, ok := byID[s.ID]
		if !ok || !sameStatement(s, r) {
			return false
		}
	}
	return true
}

// render fills the merge prompt in one pass.
func render(tmpl string, q store.QueuedNote, repoPage, projectPage store.Page, l config.Limits) string {
	return renderWith(tmpl, q, repoPage, describe(projectPage), l)
}

// renderWith is render with the project description as the prompt shows
// it: a page, or the line of a folder whose repositories stand alone.
func renderWith(tmpl string, q store.QueuedNote, repoPage store.Page, projectDescription string, l config.Limits) string {
	ticket := q.TicketKey
	if ticket == "" {
		ticket = "none"
	}
	kind := q.ChangeKind
	if kind == "" {
		kind = "unknown"
	}
	return strings.NewReplacer(
		"{repo}", q.RepoSlug,
		"{project}", q.ProjectKey,
		"{pr_key}", q.PRKey,
		"{ticket_key}", ticket,
		"{change_kind}", kind,
		"{repo_description}", describe(repoPage),
		"{project_description}", projectDescription,
		"{diagram_rules}", diagramRules(l), "{diagram_field}", diagramField(l),
		"{note}", string(q.Body),
	).Replace(tmpl)
}

// diagramRules is the shared diagram section with its caps filled in, one
// rendering for the merge, describe and project prompts; with
// BACKEND_DIAGRAM_MAX_PER_PAGE=0 the line that says statements carry none.
func diagramRules(l config.Limits) string {
	if l.MaxDiagrams == 0 {
		return strings.TrimSpace(prompts.DiagramsOff)
	}
	return strings.TrimSpace(strings.NewReplacer(
		"{diagram_max_nodes}", fmt.Sprint(l.DiagramMaxNodes), "{diagram_max_edges}", fmt.Sprint(l.DiagramMaxEdges),
		"{max_diagrams}", fmt.Sprint(l.MaxDiagrams),
	).Replace(prompts.Diagrams))
}

// diagramField is a statement's diagram in the output formats, as the
// schema offers it: not at all with BACKEND_DIAGRAM_MAX_PER_PAGE=0.
func diagramField(l config.Limits) string {
	if l.MaxDiagrams == 0 {
		return ""
	}
	return `, "diagram": <diagram> | {"kind": "none"} | null`
}

// diagramChange ends the describe prompt's list of the changes a statement
// may be returned for: its files, and its diagram unless diagrams are off.
func diagramChange(l config.Limits) string {
	if l.MaxDiagrams == 0 {
		return " or files"
	}
	return ", files or diagram (one added, replaced, or removed with `{\"kind\": \"none\"}`)"
}

// An empty page as the prompts show it, saying in so many words that every
// statement is new: shown nothing, a model numbers its new statements, and
// an id the page does not have is invented and rejects the rewrite. The
// schema keeps new statements in `added`, where no id can be written. A
// patch's summary may be null to keep the stored one: with none stored,
// null leaves the page without one, and it is refused.
const (
	allNew          = "every statement you write is new and goes in `added`; `statements` stays empty"
	noneYet         = "_None yet: write the summary, never `null`; " + allNew + "._"
	noStatementsYet = "\n_No statements yet: " + allNew + "._\n"
)

// TRANSITIONAL(lost-layout): Remove when no current revision's text is
// markup.LayoutLost.
//
// lostSummary and lostText mark a stored text that lost its layout
// (markup.LayoutLost): written on one line by a model before the schema
// carried blocks. The step that sees one writes it anew, its content
// kept: a summary as if there were none, a statement as an edit.
const (
	lostSummary = "_No summary yet: the stored one below lost its layout. Write the summary anew, in blocks, its content kept; never `null`._\n\n"
	lostText    = "\n_This text lost its layout: return the statement in `statements`, its text in blocks, its content kept._\n"
)

// describe renders a page the prompt writes: the summary, then one line per
// statement with its id, so the model can keep or edit by id.
func describe(p store.Page) string { return describePage(p, pageView{}) }

// describeRead renders a page the prompt only reads: nothing there to
// write, so an empty one says no more than that.
func describeRead(p store.Page) string { return describePage(p, pageView{read: true}) }

// pageView is how a prompt shows a page: read only or written, which pull
// requests its statements name, and which a step must leave alone and why.
type pageView struct {
	read  bool
	prs   prShown
	newer map[string]string
}

// prShown is how a statement's pull requests are shown: not at all, by key,
// or only that some taught it.
type prShown int8

const (
	prNone prShown = iota
	prKeys
	prTaught
)

func describePage(p store.Page, v pageView) string {
	if p.RevisionID == 0 {
		if v.read {
			return "_None yet._"
		}
		return noneYet
	}
	var b strings.Builder
	if !v.read && markup.LayoutLost(p.Summary) {
		// Stored on one line: shown as no summary, so a step writes one in
		// blocks rather than keeping it (null). A rerun is how such a page
		// gets its layout back.
		b.WriteString(lostSummary)
	}
	b.WriteString(p.Summary)
	b.WriteString("\n")
	if len(p.Statements) == 0 && !v.read {
		b.WriteString(noStatementsYet)
	}
	// One block per statement, its id in the heading: the text is prose
	// with paragraphs of its own, so a bullet per statement would leave
	// every second paragraph outside any id.
	for _, s := range p.Statements {
		fmt.Fprintf(&b, "\n### [%s] %s: %s\n\n%s\n", s.ID, s.Kind, s.Name, s.Text)
		// A statement the step must leave alone is not asked for: its edit
		// would be struck.
		if !v.read && v.newer[s.ID] == "" && markup.LayoutLost(s.Text) {
			b.WriteString(lostText)
		}
		if s.Area != "" {
			fmt.Fprintf(&b, "\nArea: %s\n", s.Area)
		}
		if len(s.Files) > 0 {
			fmt.Fprintf(&b, "\nFiles: %s\n", strings.Join(s.Files, ", "))
		}
		b.WriteString(diagramLine(s.Diagram))
		if v.prs != prNone {
			var prs []string
			for _, src := range s.Sources {
				if !src.IsBaseline() {
					prs = append(prs, src.PRKey)
				}
			}
			switch {
			case len(prs) == 0:
			case v.prs == prKeys:
				fmt.Fprintf(&b, "\nLearned from pull requests: %s\n", strings.Join(prs, ", "))
			default:
				b.WriteString("\nLearned from pull requests.\n")
			}
		}
		b.WriteString(leftAloneNote(v.newer[s.ID]))
	}
	return b.String()
}

// diagramLine shows a statement's diagram as the JSON it is, for the model
// to read and correct; returning null keeps it.
func diagramLine(d *diagram.Diagram) string {
	if d == nil {
		return ""
	}
	j, err := json.Marshal(d)
	if err != nil {
		return ""
	}
	return "\nDiagram: " + string(j) + "\n"
}

// prose is the runes a revision's chars counts: the summary and every
// statement's name and text. Paths and ids are not prose.
func prose(summary string, statements []store.Statement) int {
	n := utf8.RuneCountInString(summary)
	for _, s := range statements {
		n += utf8.RuneCountInString(s.Name) + utf8.RuneCountInString(s.Text)
	}
	return n
}
