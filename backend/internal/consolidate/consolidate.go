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
func (w *Worker) drain(ctx context.Context) {
	for ctx.Err() == nil && !w.stopping() && !w.holding() {
		q, ok, err := w.store.NextQueued(ctx)
		if err != nil {
			w.log.WarnContext(ctx, "consolidate: queue read failed", logging.Err(err))
			return
		}
		if !ok {
			return
		}
		if !w.Fold(ctx, q) {
			return
		}
	}
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
func gatewayClass(res inference.MergeResult) (gateway bool, retryAfter time.Duration) {
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
	if gateway, retryAfter := gatewayClass(res); gateway {
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
	switch res.Outcome {
	case inference.OutcomeOK:
		w.clearHold()
	default:
		w.log.WarnContext(ctx, fmt.Sprintf("consolidate: %s not folded: %s", q.PRKey, res.Outcome), logging.Err(res.Err))
		return
	}
	var id *int64
	if callID != 0 {
		id = &callID
	}
	w.apply(ctx, q, store.ScopeRepository, q.RepoSlug, repoPage, res.Merge.Repository, w.limits.RepoMaxStatements, id)
	w.apply(ctx, q, store.ScopeProject, "", projectPage, res.Merge.Project, w.limits.ProjectMaxStatements, id)
	return
}

// apply checks one rewritten description and writes it as a revision, or
// logs why not. nil = the model wants no change.
func (w *Worker) apply(ctx context.Context, q store.QueuedNote, scope, repo string, current store.Page, d *inference.Description, maxStatements int, callID *int64) {
	if d == nil {
		w.log.InfoContext(ctx, "consolidate: "+scope+" description unchanged")
		return
	}
	next, err := w.store.NextStatementID(ctx, scope, q.ProjectKey, repo)
	if err != nil {
		w.log.WarnContext(ctx, "consolidate: "+scope+" statement id read failed, page unchanged", logging.Err(err))
		return
	}
	statements, reason := attach(current.Statements, d.Statements, q, next)
	// Spelling first: ss for ß adds a rune, and the cap is on what is stored.
	summary := lang.SwissSS(strings.TrimSpace(d.Summary))
	chars, capReason := check(summary, statements, maxStatements, w.limits)
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
// its sources forward and gains this PR when the text changed; a new one
// starts with this PR, numbered from next (one past every id the page's
// history used, so a dropped id is never reissued). An id the current page
// does not have is the model inventing one: rejected. Spelling is Swiss on
// every prose field.
func attach(current []store.Statement, proposed []inference.Statement, q store.QueuedNote, next int) ([]store.Statement, string) {
	byID := map[string]store.Statement{}
	for _, s := range current {
		byID[s.ID] = s
	}
	src := store.Source{PRKey: q.PRKey, TicketKey: q.TicketKey, MergedAt: q.MergedAt}
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

func appendSource(sources []store.Source, s store.Source) []store.Source {
	for _, have := range sources {
		if have.PRKey == s.PRKey {
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

// check applies the caps: statement count, summary, text, name and files.
// chars is the serialized size, for the revision row.
func check(summary string, statements []store.Statement, maxStatements int, l config.Limits) (int, string) {
	if strings.TrimSpace(summary) == "" {
		return 0, "empty summary"
	}
	if utf8.RuneCountInString(summary) > l.SummaryMaxChars {
		return 0, fmt.Sprintf("summary over %d characters", l.SummaryMaxChars)
	}
	if len(statements) > maxStatements {
		return 0, fmt.Sprintf("%d statements over the cap of %d", len(statements), maxStatements)
	}
	for _, s := range statements {
		if utf8.RuneCountInString(s.Text) > l.StatementMaxChars {
			return 0, fmt.Sprintf("statement %s over %d characters", s.ID, l.StatementMaxChars)
		}
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
	b, _ := json.Marshal(statements)
	return utf8.RuneCountInString(summary) + utf8.RuneCount(b), ""
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
		"{statement_max_chars}", fmt.Sprint(l.StatementMaxChars),
		"{summary_max_chars}", fmt.Sprint(l.SummaryMaxChars),
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
	b.WriteString("\n\n")
	for _, s := range p.Statements {
		fmt.Fprintf(&b, "- [%s] %s: %s: %s", s.ID, s.Kind, s.Name, s.Text)
		if len(s.Files) > 0 {
			fmt.Fprintf(&b, " (%s)", strings.Join(s.Files, ", "))
		}
		b.WriteString("\n")
	}
	return b.String()
}
