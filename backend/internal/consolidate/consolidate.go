// Package consolidate folds notes into the descriptions: one worker, one
// note at a time, oldest first. Serial on purpose: two folds of the same
// project would each rewrite the project description from a base the other
// is replacing. The volume is one note per merged PR; a queue of minutes is
// fine, a wrong description is not.
package consolidate

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/trick77/almanach/internal/config"
	"github.com/trick77/almanach/internal/inference"
	"github.com/trick77/almanach/internal/lang"
	"github.com/trick77/almanach/internal/prompts"
	"github.com/trick77/almanach/internal/store"
	"github.com/trick77/llmwire"
	"github.com/trick77/ship/kit/logging"
)

// Store is what the worker needs of the store.
type Store interface {
	NextQueued(ctx context.Context) (store.QueuedNote, bool, error)
	MarkConsolidated(ctx context.Context, project, repo string, prID int) error
	DeferFold(ctx context.Context, project, repo string, prID int) (int, error)
	CurrentPage(ctx context.Context, scope, project, repo string) (store.Page, bool, error)
	NextStatementID(ctx context.Context, scope, project, repo string) (int, error)
	InsertRevision(ctx context.Context, rev store.NewRevision) (int, error)
	InsertLLMCall(ctx context.Context, c store.LLMCall) (int64, error)
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
	placeFails  map[string]int
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

// hold pauses the worker after a gateway-class failure; retryAfter is the
// endpoint's own ask, zero when it made none.
func (w *Worker) hold(ctx context.Context, retryAfter time.Duration) {
	if w.backoff == 0 {
		w.backoff = backoffMin
	} else {
		w.backoff = min(w.backoff*2, backoffMax)
	}
	wait := max(w.backoff, retryAfter)
	w.retryAt = w.now().Add(wait)
	w.log.WarnContext(ctx, fmt.Sprintf("consolidate: gateway hold for %s, no fold until then", wait))
}

// holding reports whether a gateway hold is still on.
func (w *Worker) holding() bool { return w.now().Before(w.retryAt) }

// clearHold forgets the hold once a call got through.
func (w *Worker) clearHold() { w.backoff, w.retryAt = 0, time.Time{} }

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
			case <-time.After(w.poll):
			}
		}
	}()
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
func (w *Worker) drain(ctx context.Context) {
	w.saveFailed = nil
	for ctx.Err() == nil && !w.stopping() && !w.holding() {
		q, ok, err := w.store.NextQueued(ctx)
		if err != nil {
			w.log.WarnContext(ctx, "consolidate: queue read failed", logging.Err(err))
			return
		}
		if ok {
			if !w.foldOrIgnore(ctx, q) {
				return
			}
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
		if !ok || !w.stepRun(ctx, r) {
			return
		}
	}
}

// foldOrIgnore folds a note, or with the baseline on takes a note for a
// repository that is no source off the queue unfolded: one queued before
// the sources existed, or for a repository removed since. The source is
// the starting point.
func (w *Worker) foldOrIgnore(ctx context.Context, q store.QueuedNote) bool {
	if w.base != nil {
		known, err := w.base.Store.IsSourceRepo(ctx, q.ProjectKey, q.RepoSlug)
		if err != nil {
			w.log.WarnContext(ctx, "consolidate: source check failed, drain paused", logging.Err(err))
			return false
		}
		if !known {
			w.log.InfoContext(ctx, "consolidate: "+q.PRKey+" ignored: no source repository "+q.ProjectKey+"/"+q.RepoSlug)
			if err := w.store.MarkConsolidated(ctx, q.ProjectKey, q.RepoSlug, q.PRID); err != nil {
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

// Fold runs one note through the merge: the current pages in, the rewritten
// ones out, each checked against the caps and written as a revision. The
// note is marked consolidated whatever the model answered and after a
// failure specific to this call; a gateway-class failure leaves it queued
// with its attempt counted and holds the worker, up to maxFoldAttempts.
// Reports whether the drain may go on: false after a deferred note or a
// mark the store refused, so the same note is not paid for again in the
// same lap.
func (w *Worker) Fold(ctx context.Context, q store.QueuedNote) (next bool) {
	ctx = logging.With(ctx, "team", q.Team, "pr_key", q.PRKey)
	deferred := false
	defer func() {
		if deferred {
			next = false
			return
		}
		if err := w.store.MarkConsolidated(ctx, q.ProjectKey, q.RepoSlug, q.PRID); err != nil {
			w.log.WarnContext(ctx, "consolidate: mark failed, drain paused", logging.Err(err))
			next = false
			return
		}
		next = true
	}()

	repoPage, _, err := w.store.CurrentPage(ctx, store.ScopeRepository, q.ProjectKey, q.RepoSlug)
	if err != nil {
		w.log.WarnContext(ctx, "consolidate: repository page read failed", logging.Err(err))
		return
	}
	projectPage, _, err := w.store.CurrentPage(ctx, store.ScopeProject, q.ProjectKey, "")
	if err != nil {
		w.log.WarnContext(ctx, "consolidate: project page read failed", logging.Err(err))
		return
	}

	prompt := render(prompts.Merge, q, repoPage, projectPage, w.limits)
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
		w.hold(ctx, retryAfter)
		attempts, derr := w.store.DeferFold(ctx, q.ProjectKey, q.RepoSlug, q.PRID)
		if derr != nil {
			w.log.WarnContext(ctx, "consolidate: defer failed, drain paused", logging.Err(derr))
			deferred = true
			return
		}
		if attempts < maxFoldAttempts {
			w.log.WarnContext(ctx, fmt.Sprintf("consolidate: %s not folded: %s, attempt %d of %d, kept for after the hold",
				q.PRKey, res.Outcome, attempts, maxFoldAttempts), logging.Err(res.Err))
			deferred = true
			return
		}
		w.log.WarnContext(ctx, fmt.Sprintf("consolidate: %s given up after %d failed calls: %s", q.PRKey, attempts, res.Outcome), logging.Err(res.Err))
		return
	}
	// The gateway answered: whatever it said, the hold is over.
	w.clearHold()
	switch res.Outcome {
	case inference.OutcomeOK:
	default:
		w.log.WarnContext(ctx, fmt.Sprintf("consolidate: %s not folded: %s", q.PRKey, res.Outcome), logging.Err(res.Err))
		return
	}
	var id *int64
	if callID != 0 {
		id = &callID
	}
	w.apply(ctx, q, store.ScopeRepository, q.RepoSlug, repoPage, res.Merge.Repository, w.limits.RepoMaxStatements, w.limits.RepoMaxChars, id)
	w.apply(ctx, q, store.ScopeProject, "", projectPage, res.Merge.Project, w.limits.ProjectMaxStatements, w.limits.ProjectMaxChars, id)
	return
}

// apply checks one rewritten description and writes it as a revision, or
// logs why not. nil = the model wants no change.
func (w *Worker) apply(ctx context.Context, q store.QueuedNote, scope, repo string, current store.Page, d *inference.Description, maxStatements, maxChars int, callID *int64) {
	if d == nil {
		w.log.InfoContext(ctx, "consolidate: "+scope+" description unchanged")
		return
	}
	next, err := w.store.NextStatementID(ctx, scope, q.ProjectKey, repo)
	if err != nil {
		w.log.WarnContext(ctx, "consolidate: "+scope+" statement id read failed, page unchanged", logging.Err(err))
		return
	}
	statements, reason := attach(current.Statements, d.Statements, store.Source{PRKey: q.PRKey, TicketKey: q.TicketKey, MergedAt: q.MergedAt, Commit: q.MergeCommit}, next)
	// Spelling first: ss for ß adds a rune, and the cap is on what is stored.
	summary := lang.SwissSS(strings.TrimSpace(d.Summary))
	chars, capReason := check(summary, statements, maxStatements, maxChars)
	if reason == "" {
		reason = capReason
	}
	if reason != "" {
		w.log.WarnContext(ctx, fmt.Sprintf("consolidate: %s rewrite rejected (%s), page unchanged", scope, reason))
		return
	}
	if summary == current.Summary && sameStatements(statements, current.Statements) {
		w.log.InfoContext(ctx, "consolidate: "+scope+" description identical, no revision")
		return
	}
	n, err := w.store.InsertRevision(ctx, store.NewRevision{
		Scope: scope, ProjectKey: q.ProjectKey, RepoSlug: repo, Summary: summary, Statements: statements,
		Chars: chars, SourcePR: q.PRKey, LLMCallID: callID,
	})
	if err != nil {
		w.log.WarnContext(ctx, "consolidate: "+scope+" revision write failed", logging.Err(err))
		return
	}
	w.log.InfoContext(ctx, fmt.Sprintf("consolidate: %s description revision %d (%d statements, %d chars) from %s",
		scope, n, len(statements), chars, q.PRKey))
}

// Statement kinds a description may hold.
var kinds = map[string]bool{"component": true, "rule": true, "invariant": true, "entity": true}

// attach turns the model's statements into stored ones: a kept id carries
// its sources forward and gains src (this PR, or a baseline run's commit)
// when the text changed; a new one starts with src, numbered from next (one
// past every id the page's history used, so a dropped id is never
// reissued). An id the current page does not have is the model inventing
// one: rejected. Spelling is Swiss on every prose field. The commit a
// statement was last checked against stays while its name and text do; a
// change voids it, the caller sets a new one when it read the code.
func attach(current []store.Statement, proposed []inference.Statement, src store.Source, next int) ([]store.Statement, string) {
	byID := map[string]store.Statement{}
	for _, s := range current {
		byID[s.ID] = s
	}
	out := make([]store.Statement, 0, len(proposed))
	seen := map[string]bool{}
	for _, p := range proposed {
		if !kinds[p.Kind] {
			return nil, "unknown statement kind " + p.Kind
		}
		s := store.Statement{Kind: p.Kind, Name: lang.SwissSS(strings.TrimSpace(p.Name)), Text: lang.SwissSS(strings.TrimSpace(p.Text)), Files: cleanFiles(p.Files)}
		if s.Name == "" || s.Text == "" {
			return nil, "empty statement"
		}
		if p.ID != nil && *p.ID != "" {
			old, ok := byID[*p.ID]
			if !ok {
				return nil, "unknown statement id " + *p.ID
			}
			if seen[*p.ID] {
				return nil, "statement id used twice " + *p.ID
			}
			seen[*p.ID] = true
			s.ID = old.ID
			s.Sources = old.Sources
			if old.Text != s.Text || old.Name != s.Name {
				s.Sources = appendSource(s.Sources, src)
			} else {
				s.Checked = old.Checked
			}
		} else {
			s.ID = fmt.Sprintf("s%d", next)
			next++
			s.Sources = []store.Source{src}
		}
		out = append(out, s)
	}
	return out, ""
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

// Caps on the fields the config does not size: a statement's name and its
// file list, the same numbers noergler holds a note's items to.
const (
	nameMaxChars = 80
	filesMax     = 5
	pathMaxChars = 200
)

// check applies the ceilings: the statement count, the prose of the whole
// description, and the fixed caps on a name and on files. chars is that
// prose count, stored on the revision row.
func check(summary string, statements []store.Statement, maxStatements, maxChars int) (int, string) {
	if strings.TrimSpace(summary) == "" {
		return 0, "empty summary"
	}
	if len(statements) > maxStatements {
		return 0, fmt.Sprintf("%d statements over the cap of %d", len(statements), maxStatements)
	}
	// The ceiling is on the whole description, never on one statement: a
	// description is documentation and a statement is as long as its
	// subject needs. Prose runes only: names, texts, the summary.
	size := prose(summary, statements)
	if size > maxChars {
		return 0, fmt.Sprintf("description is %d characters, over the cap of %d", size, maxChars)
	}
	for _, s := range statements {
		if utf8.RuneCountInString(s.Name) > nameMaxChars {
			return 0, fmt.Sprintf("statement %s name over %d characters", s.ID, nameMaxChars)
		}
		if len(s.Files) > filesMax {
			return 0, fmt.Sprintf("statement %s names %d files, over %d", s.ID, len(s.Files), filesMax)
		}
		for _, f := range s.Files {
			if utf8.RuneCountInString(f) > pathMaxChars || strings.ContainsAny(f, " \t\n") {
				return 0, fmt.Sprintf("statement %s has a path over %d characters or with whitespace", s.ID, pathMaxChars)
			}
		}
	}
	// chars on the revision is the figure the ceiling gated, so the API
	// shows the same number the cap applies to.
	return size, ""
}

func sameStatements(a, b []store.Statement) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ID != b[i].ID || a[i].Kind != b[i].Kind || a[i].Name != b[i].Name || a[i].Text != b[i].Text || strings.Join(a[i].Files, "\x00") != strings.Join(b[i].Files, "\x00") {
			return false
		}
	}
	return true
}

// render fills the merge prompt in one pass.
func render(tmpl string, q store.QueuedNote, repoPage, projectPage store.Page, l config.Limits) string {
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
		"{repo_max_statements}", fmt.Sprint(l.RepoMaxStatements),
		"{project_max_statements}", fmt.Sprint(l.ProjectMaxStatements),
		"{repo_max_chars}", fmt.Sprint(l.RepoMaxChars),
		"{project_max_chars}", fmt.Sprint(l.ProjectMaxChars),
		"{repo_chars}", fmt.Sprint(prose(repoPage.Summary, repoPage.Statements)),
		"{project_chars}", fmt.Sprint(prose(projectPage.Summary, projectPage.Statements)),
		"{repo_description}", describe(repoPage),
		"{project_description}", describe(projectPage),
		"{note}", string(q.Body),
	).Replace(tmpl)
}

// describe renders a page for the prompt: the summary, then one line per
// statement with its id, so the model can keep or edit by id.
func describe(p store.Page) string {
	if p.RevisionID == 0 {
		return "_None yet._"
	}
	var b strings.Builder
	b.WriteString(p.Summary)
	b.WriteString("\n")
	// One block per statement, its id in the heading: the text is prose
	// with paragraphs of its own, so a bullet per statement would leave
	// every second paragraph outside any id.
	for _, s := range p.Statements {
		fmt.Fprintf(&b, "\n### [%s] %s: %s\n\n%s\n", s.ID, s.Kind, s.Name, s.Text)
		if len(s.Files) > 0 {
			fmt.Fprintf(&b, "\nFiles: %s\n", strings.Join(s.Files, ", "))
		}
	}
	return b.String()
}

// prose is the runes a description's ceiling counts: the summary and every
// statement's name and text. Paths and ids are not prose.
func prose(summary string, statements []store.Statement) int {
	n := utf8.RuneCountInString(summary)
	for _, s := range statements {
		n += utf8.RuneCountInString(s.Name) + utf8.RuneCountInString(s.Text)
	}
	return n
}
