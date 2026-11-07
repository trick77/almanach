package consolidate

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/trick77/almanach/internal/baseline"
	"github.com/trick77/almanach/internal/inference"
	"github.com/trick77/almanach/internal/lang"
	"github.com/trick77/almanach/internal/markup"
	"github.com/trick77/almanach/internal/prompts"
	"github.com/trick77/almanach/internal/redact"
	"github.com/trick77/almanach/internal/store"
	"github.com/trick77/ship/kit/logging"
)

// A repository's brief: its .agents/PROJECT.md, what an agent working in
// it cannot read from its code, written on request from the stored page
// (one call, no code read) and pushed with the source's token to a branch
// of the repository's own (BriefBranch). A push the forge refuses (no
// write access, a token expired) is no failure of the brief: the reader
// copies the text instead. Drained after the notes and before the runs,
// one item a lap: a due push first, it costs no call.

// The brief in the repository: the file agents-md-sync folds into the
// AGENTS.md, the branch it is pushed to, the commit's message and author.
const (
	BriefPath    = baseline.BriefPath
	BriefBranch  = "feature/repository-description-update"
	BriefMessage = "Update the repository description in .agents/PROJECT.md"
)

// briefWarnBytes is past what a brief should be (the prompt asks for at
// most 2500 characters; agents-md-sync warns at 12 KB for the whole
// AGENTS.md): logged, never cut, the reader judges.
const briefWarnBytes = 4096

// BriefStore is what the briefs need of the store.
type BriefStore interface {
	NextBrief(ctx context.Context) (store.BriefJob, bool, error)
	WriteBrief(ctx context.Context, repoID, seq int64, markdown string, pageRevision int) error
	DeferBrief(ctx context.Context, repoID int64) (int, error)
	FailBrief(ctx context.Context, repoID, seq int64, reason string) error
	PushedBrief(ctx context.Context, repoID int64, state, commit, pushErr string) error
	Dependencies(ctx context.Context, all bool) (store.Graph, error)
	SharedIn(ctx context.Context, g store.Graph, key, slug string) ([]store.SharedRepo, error)
}

// Briefer is the model that writes a brief.
type Briefer interface {
	Brief(ctx context.Context, prompt string) inference.BriefResult
}

// Pusher commits one file on top of a repository's default branch, in its
// clone, and pushes the commit to branch, where the remote has none or
// still has owned (the commit pushed there last): changed false when the
// default branch already holds the content (nothing pushed). ErrCloneBusy
// while the poller holds the clone.
type Pusher interface {
	PushFile(ctx context.Context, key, slug, branch, owned, path string, content []byte, message string) (commit string, changed bool, err error)
}

// Briefs is the briefs' part of the worker; nil in Baseline: none.
type Briefs struct {
	Store BriefStore
	Model Briefer
	Push  Pusher
}

// briefStep is how one brief left the drain.
type briefStep int8

const (
	// briefNext: done, the drain goes on.
	briefNext briefStep = iota
	// briefStop: the drain ends (a hold, a store that does not answer).
	briefStop
	// briefSkip: the brief waits (its clone busy); the drain goes on to
	// the runs without the briefs, which the next drain takes again.
	briefSkip
)

// nextBrief is the brief the drain takes next: false with none, or with
// only a request when the day's budget is spent (the runs' rule: the
// request waits for the next day, a due push goes on). err ends the drain.
func (w *Worker) nextBrief(ctx context.Context) (store.BriefJob, bool, error) {
	if w.base == nil || w.base.Briefs == nil {
		return store.BriefJob{}, false, nil
	}
	j, ok, err := w.base.Briefs.Store.NextBrief(ctx)
	if err != nil || !ok || !j.Write {
		return j, ok, err
	}
	over, err := w.overBudget(ctx)
	if err != nil {
		return store.BriefJob{}, false, err
	}
	return j, !over, nil
}

// brief writes or pushes one brief.
func (w *Worker) brief(ctx context.Context, j store.BriefJob) briefStep {
	ctx = logging.With(ctx, "project", j.ProjectKey, "repo", j.Slug)
	w.setBusy(Busy{Kind: KindBrief, ProjectKey: j.ProjectKey, RepoSlug: j.Slug})
	defer w.clearBusy()
	if j.Write {
		return w.writeBrief(ctx, j)
	}
	return w.pushBrief(ctx, j)
}

// writeBrief is a request's one call: the page, the project's and the
// dependencies in, the file out and stored with its push due. The attempt
// is counted before the call, so a request the gateway or the store keeps
// failing is given up at maxFoldAttempts, never paid for on every lap; a
// disk without room, or a count the store refuses, holds before any call.
// A page not written yet fails the request.
func (w *Worker) writeBrief(ctx context.Context, j store.BriefJob) briefStep {
	bs := w.base.Briefs
	if w.diskLow(ctx) {
		return briefStop
	}
	attempts, err := bs.Store.DeferBrief(ctx, j.RepoID)
	if errors.Is(err, store.ErrNoRepo) {
		// Its repository removed since NextBrief: the job went with it.
		w.log.InfoContext(ctx, "brief: the repository is gone, request dropped")
		return briefNext
	}
	if err != nil {
		w.holdStore(ctx, err)
		return briefStop
	}
	w.writeOK()
	if attempts > maxFoldAttempts {
		return w.failBrief(ctx, j, fmt.Sprintf("given up after %d attempts", maxFoldAttempts))
	}
	page, ok, err := w.store.CurrentPage(ctx, store.ScopeRepository, j.ProjectKey, j.Slug)
	if err != nil {
		w.log.WarnContext(ctx, "brief: repository page read failed, drain paused", logging.Err(err))
		return briefStop
	}
	if !ok || page.RevisionID == 0 {
		return w.failBrief(ctx, j, "the repository has no description yet")
	}
	project, err := w.briefProject(ctx, j.ProjectKey)
	if err != nil {
		w.log.WarnContext(ctx, "brief: project page read failed, drain paused", logging.Err(err))
		return briefStop
	}
	deps, err := w.briefDependencies(ctx, store.RepoRef{ProjectKey: j.ProjectKey, Slug: j.Slug})
	if err != nil {
		w.log.WarnContext(ctx, "brief: dependencies read failed, drain paused", logging.Err(err))
		return briefStop
	}
	prompt := strings.NewReplacer(
		"{repo}", j.Slug, "{project}", j.ProjectKey, "{project_description}", project,
		"{dependencies}", deps, "{repo_description}", describeBrief(page),
	).Replace(prompts.Brief)
	res := bs.Model.Brief(ctx, prompt)
	if _, err := w.store.InsertLLMCall(ctx, store.LLMCall{
		ProjectKey: j.ProjectKey, RepoSlug: j.Slug, SourceKind: store.FromPage, Purpose: store.PurposeBrief, Outcome: res.Outcome.String(),
		Model: res.Cost.Model, InputTokens: res.Cost.InputTokens, CachedTokens: res.Cost.CachedTokens,
		OutputTokens: res.Cost.OutputTokens, CostNanoUSD: res.Cost.NanoUSD, CallID: res.Cost.CallID, Streamed: res.Cost.Streamed,
	}); err != nil {
		w.log.WarnContext(ctx, "brief: call row failed", logging.Err(err))
	}
	w.log.InfoContext(ctx, fmt.Sprintf("brief: call %s (%s)", res.Outcome, res.Cost.LogLine()))
	if gateway, retryAfter := gatewayClass(res.Call); gateway {
		w.hold(ctx, gatewayCause(res.Call), retryAfter)
		if attempts >= maxFoldAttempts {
			w.failBrief(ctx, j, fmt.Sprintf("given up after %d attempts: %s", attempts, outcomeReason(res.Call)))
		}
		return briefStop
	}
	w.clearHold()
	if res.Outcome != inference.OutcomeOK {
		return w.failBrief(ctx, j, outcomeReason(res.Call))
	}
	md := briefMarkdown(j.ProjectKey, j.Slug, res.Text)
	if len(md) > briefWarnBytes {
		w.log.WarnContext(ctx, fmt.Sprintf("brief: %d bytes, past the %d a brief should stay under; kept whole", len(md), briefWarnBytes))
	}
	if err := bs.Store.WriteBrief(ctx, j.RepoID, j.RequestSeq, md, page.Revision); err != nil {
		w.holdStore(ctx, err)
		return briefStop
	}
	w.writeOK()
	w.log.InfoContext(ctx, fmt.Sprintf("brief: written from revision %d, %d bytes, push due", page.Revision, len(md)))
	return briefNext
}

// pushBrief pushes a written brief to BriefBranch. A clone the poller
// holds leaves it due, the drain going on to the runs; any other failure
// is the push's end, the reason on the row for the reader.
func (w *Worker) pushBrief(ctx context.Context, j store.BriefJob) briefStep {
	bs := w.base.Briefs
	commit, changed, err := bs.Push.PushFile(ctx, j.ProjectKey, j.Slug, BriefBranch, j.PushedCommit, BriefPath, []byte(j.Markdown), BriefMessage)
	if errors.Is(err, ErrCloneBusy) {
		w.log.DebugContext(ctx, "brief: clone busy, push deferred")
		return briefSkip
	}
	if ctx.Err() != nil {
		return briefStop
	}
	state, reason := store.PushPushed, ""
	switch {
	case err != nil:
		// Redacted before the cut: a credential cut short no longer has its shape.
		state, reason = store.PushFailed, truncate(redact.Text(err.Error()), reasonMaxRunes)
		w.log.WarnContext(ctx, "brief: push refused, the text stays to copy", logging.Err(err))
	case !changed:
		// commit is the default branch when a branch of its own was reset
		// to it, "" when nothing was pushed.
		state = store.PushUnchanged
		w.log.InfoContext(ctx, "brief: the default branch already holds it", "branch_reset", commit != "")
	default:
		w.log.InfoContext(ctx, "brief: pushed to "+BriefBranch, "commit", commit)
	}
	if err := bs.Store.PushedBrief(ctx, j.RepoID, state, commit, reason); err != nil {
		w.holdStore(ctx, err)
		return briefStop
	}
	w.writeOK()
	return briefNext
}

// failBrief takes a request off with its reason; a mark the store refuses
// holds the worker, or the same paid call comes again.
func (w *Worker) failBrief(ctx context.Context, j store.BriefJob, reason string) briefStep {
	// Redacted before the cut: a credential cut short no longer has its shape.
	reason = truncate(redact.Text(reason), reasonMaxRunes)
	w.log.WarnContext(ctx, "brief: request failed: "+reason)
	if err := w.base.Briefs.Store.FailBrief(ctx, j.RepoID, j.RequestSeq, reason); err != nil {
		w.holdStore(ctx, err)
		return briefStop
	}
	w.writeOK()
	return briefNext
}

// outcomeReason is a call that did not answer as asked, as a reason.
func outcomeReason(c inference.Call) string {
	if c.Err != nil {
		return c.Outcome.String() + ": " + c.Err.Error()
	}
	return c.Outcome.String()
}

// describeBrief is a page as the brief reads it: read-only (no word asking
// for a write the brief's schema has no field for), what pull requests
// taught marked, no diagrams.
func describeBrief(p store.Page) string {
	return describePage(p, pageView{read: true, prs: prTaught, noDiagrams: true})
}

// briefProject is the project as the brief's prompt shows it: none for a
// project of one, a collection's summary under what it is, a system's page.
func (w *Worker) briefProject(ctx context.Context, key string) (string, error) {
	alone, err := w.single(ctx, key)
	if err != nil {
		return "", err
	}
	if alone {
		return "_None: this project is this one repository._", nil
	}
	page, _, err := w.store.CurrentPage(ctx, store.ScopeProject, key, "")
	if err != nil {
		return "", err
	}
	if page.Collection() {
		return collectionLead + "_\n\n" + page.Summary, nil
	}
	return describeBrief(page), nil
}

// briefDependencies is what repo uses at runtime (main scope), each with
// its summary where it has one (store.Shared), and what uses it, by name:
// code's edges, never the model's.
func (w *Worker) briefDependencies(ctx context.Context, repo store.RepoRef) (string, error) {
	bs := w.base.Briefs.Store
	g, err := bs.Dependencies(ctx, false)
	if err != nil {
		return "", err
	}
	uses, err := bs.SharedIn(ctx, g, repo.ProjectKey, repo.Slug)
	if err != nil {
		return "", err
	}
	name := func(r store.RepoRef) string { return "`" + r.ProjectKey + "/" + r.Slug + "`" }
	var usedBy []string
	for _, e := range g.Edges {
		if e.To == repo {
			usedBy = append(usedBy, name(e.From))
		}
	}
	slices.Sort(usedBy)
	usedBy = slices.Compact(usedBy)
	if len(uses) == 0 && len(usedBy) == 0 {
		return "_None known._", nil
	}
	var b strings.Builder
	for _, u := range uses {
		b.WriteString("Uses " + name(u.RepoRef))
		if s := strings.TrimSpace(u.Summary); s != "" {
			b.WriteString(":\n\n" + s)
		}
		b.WriteString("\n\n")
	}
	if len(usedBy) > 0 {
		b.WriteString("Used by: " + strings.Join(usedBy, ", ") + "\n")
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// briefMarkdown is the file: the repository's current names as its title
// (`# project/slug`, the top of the AGENTS.md agents-md-sync folds it into;
// user, 2026-11-06), then the model's text in the stored layout
// (credentials redacted, Swiss spelling) as Markdown, its sub-headings one
// level down.
func briefMarkdown(project, slug, text string) string {
	return "# " + project + "/" + slug + "\n\n" + markup.Export(lang.Laid(text), "## ") + "\n"
}
