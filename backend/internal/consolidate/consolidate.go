// Package consolidate folds notes into the descriptions: one worker, one
// note at a time, oldest first. Serial on purpose: two folds of the same
// project would each rewrite the project description from a base the other
// is replacing. The volume is one note per merged PR; a queue of minutes is
// fine, a wrong description is not.
package consolidate

import (
	"context"
	"encoding/json"
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
	"github.com/trick77/ship/kit/logging"
)

// Store is what the worker needs of the store.
type Store interface {
	NextQueued(ctx context.Context) (store.QueuedNote, bool, error)
	MarkConsolidated(ctx context.Context, project, repo string, prID int) error
	CurrentPage(ctx context.Context, scope, project, repo string) (store.Page, bool, error)
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
	done chan struct{}
}

// New builds a worker. It runs nothing until Start.
func New(st Store, llm Merger, limits config.Limits, log *slog.Logger) *Worker {
	return &Worker{store: st, llm: llm, limits: limits, log: log, wake: make(chan struct{}, 1), poll: 30 * time.Second, done: make(chan struct{})}
}

// Wake tells the loop a note was queued. Never blocks: a wake already
// pending is the same wake.
func (w *Worker) Wake() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// Start runs the loop until ctx ends; Stop waits for the fold in flight.
func (w *Worker) Start(ctx context.Context) {
	go func() {
		defer close(w.done)
		for {
			w.drain(ctx)
			select {
			case <-ctx.Done():
				return
			case <-w.wake:
			case <-time.After(w.poll):
			}
		}
	}()
}

// Stop waits for the loop to end; the caller cancels its ctx first.
func (w *Worker) Stop() { <-w.done }

// drain folds every queued note, oldest first, until the queue is empty or
// ctx ends. A fold's own failure takes its note off the queue with a log
// line: a note that fails forever would otherwise block every note behind it.
func (w *Worker) drain(ctx context.Context) {
	for ctx.Err() == nil {
		q, ok, err := w.store.NextQueued(ctx)
		if err != nil {
			w.log.WarnContext(ctx, "consolidate: queue read failed", logging.Err(err))
			return
		}
		if !ok {
			return
		}
		w.Fold(ctx, q)
	}
}

// Fold runs one note through the merge: the current pages in, the rewritten
// ones out, each checked against the caps and written as a revision, the
// note marked consolidated either way.
func (w *Worker) Fold(ctx context.Context, q store.QueuedNote) {
	ctx = logging.With(ctx, "team", q.Team, "pr_key", q.PRKey)
	defer func() {
		if err := w.store.MarkConsolidated(ctx, q.ProjectKey, q.RepoSlug, q.PRID); err != nil {
			w.log.WarnContext(ctx, "consolidate: mark failed", logging.Err(err))
		}
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
	if res.Outcome != inference.OutcomeOK {
		w.log.WarnContext(ctx, fmt.Sprintf("consolidate: %s not folded: %s", q.PRKey, res.Outcome), logging.Err(res.Err))
		return
	}
	var id *int64
	if callID != 0 {
		id = &callID
	}
	w.apply(ctx, q, store.ScopeRepository, q.RepoSlug, repoPage, res.Merge.Repository, w.limits.RepoMaxStatements, id)
	w.apply(ctx, q, store.ScopeProject, "", projectPage, res.Merge.Project, w.limits.ProjectMaxStatements, id)
}

// apply checks one rewritten description and writes it as a revision, or
// logs why not. nil = the model wants no change.
func (w *Worker) apply(ctx context.Context, q store.QueuedNote, scope, repo string, current store.Page, d *inference.Description, maxStatements int, callID *int64) {
	if d == nil {
		w.log.InfoContext(ctx, "consolidate: "+scope+" description unchanged")
		return
	}
	statements, reason := attach(current.Statements, d.Statements, q)
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
// starts with this PR. An id the current page does not have is the model
// inventing one: rejected. Spelling is Swiss on every prose field.
func attach(current []store.Statement, proposed []inference.Statement, q store.QueuedNote) ([]store.Statement, string) {
	byID := map[string]store.Statement{}
	for _, s := range current {
		byID[s.ID] = s
	}
	src := store.Source{PRKey: q.PRKey, TicketKey: q.TicketKey, MergedAt: q.MergedAt}
	out := make([]store.Statement, 0, len(proposed))
	seen := map[string]bool{}
	next := nextID(current)
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

// nextID is one past the highest s<n> id on the page.
func nextID(current []store.Statement) int {
	n := 1
	for _, s := range current {
		var v int
		if _, err := fmt.Sscanf(s.ID, "s%d", &v); err == nil && v >= n {
			n = v + 1
		}
	}
	return n
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

// check applies the caps: statement count, text and summary rune lengths.
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
