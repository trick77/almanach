package consolidate

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/trick77/almanach/internal/baseline"
	"github.com/trick77/almanach/internal/config"
	"github.com/trick77/almanach/internal/inference"
	"github.com/trick77/almanach/internal/prompts"
	"github.com/trick77/almanach/internal/store"
	"github.com/trick77/ship/kit/logging"
)

// A restyle run brings stored pages to the prompts of today (prompts.Epoch)
// without reading code: a full run re-reads up to every part of a
// repository to apply a rule of layout, kind or area, where the page alone
// holds all the run needs (user, 2026-11-04: "that's pretty expensive to
// redescribe"). One call for the repository's page, one for its project's
// when that is due, then the map step as in any run, which draws only when
// the text moved. It learns nothing, so it neither credits nor checks: an
// edit keeps its sources, its files and its last check; a statement split
// out of others carries their sources, and with none to carry it is left
// out. Nothing is dropped. Each page's epoch is written with its step, so
// a restart or a later failure never pays for a step twice.

// restylePlan is a restyle run's progress beside runPlan's ProjectDone:
// Repo, the repository's page is over (restyled, or not due).
type restylePlan struct {
	Repo bool `json:"repo,omitempty"`
}

// The steps of a restyle run, as r.Step counts them: the ledger's step of
// each call, so the queue tells the two calls apart.
const (
	restyleRepoStep    = 0
	restyleProjectStep = 1
)

// startRestyle starts a restyle run: no clone, no head pinned, at the
// documented commit the store queued it with, straight to its steps.
func (w *Worker) startRestyle(ctx context.Context, r *store.BaselineRun) error {
	now := w.now().UTC()
	r.StartedAt, r.State, r.Error, r.Step = &now, store.RunProject, "", restyleRepoStep
	w.log.InfoContext(ctx, fmt.Sprintf("baseline: restyle run started (epoch %d)", r.Epoch))
	return w.save(ctx, *r)
}

// restyleStep is a restyle run's next step: the repository's page, then
// the project's, each only while below the epoch; then the map step.
func (w *Worker) restyleStep(ctx context.Context, r *store.BaselineRun, plan runPlan) error {
	repoEpoch, projectEpoch, err := w.base.Store.RestyleEpochs(ctx, r.ProjectKey, r.Slug)
	if err != nil {
		w.log.WarnContext(ctx, "baseline: restyle epochs read failed, drain paused", logging.Err(err))
		return errStop
	}
	if !plan.Restyle.Repo {
		if repoEpoch >= prompts.Epoch {
			w.log.InfoContext(ctx, "baseline: restyle: the repository page is at the epoch already")
			plan.Restyle.Repo, r.Step = true, restyleProjectStep
			if err := encodeInto(r, plan); err != nil {
				return err
			}
			return w.save(ctx, *r)
		}
		return w.restyleRepo(ctx, r, plan)
	}
	due, err := w.projectRestyleDue(ctx, r, projectEpoch)
	if err != nil {
		return err
	}
	if due {
		return w.restyleProject(ctx, r, plan)
	}
	return w.projectOver(ctx, r, plan)
}

// projectRestyleDue reports whether the run restyles its project's page: one
// is written, the project has more than one source repository, and the page
// is below the epoch.
func (w *Worker) projectRestyleDue(ctx context.Context, r *store.BaselineRun, projectEpoch int) (bool, error) {
	if projectEpoch >= prompts.Epoch {
		return false, nil
	}
	n, err := w.store.SourceRepoCount(ctx, r.ProjectKey)
	if err != nil {
		w.log.WarnContext(ctx, "baseline: project source read failed, drain paused", logging.Err(err))
		return false, errStop
	}
	if store.Single(n) {
		return false, nil
	}
	page, _, err := w.store.CurrentPage(ctx, store.ScopeProject, r.ProjectKey, "")
	if err != nil {
		w.log.WarnContext(ctx, "baseline: project page read failed, drain paused", logging.Err(err))
		return false, errStop
	}
	return page.RevisionID != 0, nil
}

// restyleRepo is the repository page's restyle: one call, its answer applied
// and written with the page's epoch and the run's progress. A page not
// written yet has nothing to restyle and is at the epoch.
func (w *Worker) restyleRepo(ctx context.Context, r *store.BaselineRun, plan runPlan) error {
	at := "baseline: restyle"
	page, ok, err := w.store.CurrentPage(ctx, store.ScopeRepository, r.ProjectKey, r.Slug)
	if err != nil {
		w.log.WarnContext(ctx, "baseline: repository page read failed, drain paused", logging.Err(err))
		return errStop
	}
	var out store.Restyled
	if !ok {
		w.log.InfoContext(ctx, at+": no repository page yet, nothing to restyle")
	} else {
		if r.Calls >= w.base.Limits.MaxCalls {
			return fmt.Errorf("no call left for the restyle")
		}
		res := w.base.Describer.Restyle(ctx, renderRestyle(*r, page, w.limits, false, nil), false)
		okCall, err := w.call(ctx, r, store.PurposeRestyle, res.Call)
		if err != nil {
			return err
		}
		if !okCall {
			return fmt.Errorf("the restyle call failed: %s", res.Outcome)
		}
		if out, err = w.restyled(ctx, at, *r, store.ScopeRepository, page, res); err != nil {
			return err
		}
	}
	plan.Restyle.Repo, r.Step = true, restyleProjectStep
	return w.writeRestyle(ctx, r, plan, store.ScopeRepository, out)
}

// restyleProject is the project page's restyle, the page alone in the
// prompt with its repositories by slug; its map is carried as stored.
func (w *Worker) restyleProject(ctx context.Context, r *store.BaselineRun, plan runPlan) error {
	at := "baseline: restyle " + projectLabel
	page, _, err := w.store.CurrentPage(ctx, store.ScopeProject, r.ProjectKey, "")
	if err != nil {
		w.log.WarnContext(ctx, "baseline: project page read failed, drain paused", logging.Err(err))
		return errStop
	}
	if r.Calls >= w.base.Limits.MaxCalls {
		// Still due: the next restyle of one of its repositories takes it.
		w.log.WarnContext(ctx, at+" skipped: the run's calls are spent, still due")
		return w.projectOver(ctx, r, plan)
	}
	repos, err := w.repoSummaries(ctx, r)
	if err != nil {
		return err
	}
	res := w.base.Describer.Restyle(ctx, renderRestyle(*r, page, w.limits, true, slugs(repos)), true)
	okCall, err := w.call(ctx, r, store.PurposeRestyle, res.Call)
	if err != nil {
		return err
	}
	if !okCall {
		return fmt.Errorf("the project restyle call failed: %s", res.Outcome)
	}
	out, err := w.restyled(ctx, at, *r, store.ScopeProject, page, res)
	if err != nil {
		return err
	}
	plan.ProjectDone = true
	return w.writeRestyle(ctx, r, plan, store.ScopeProject, out)
}

// writeRestyle writes a restyle step with the run's progress and the page's
// epoch in one transaction.
func (w *Worker) writeRestyle(ctx context.Context, r *store.BaselineRun, plan runPlan, scope string, out store.Restyled) error {
	prev := *r
	if err := encodeInto(r, plan); err != nil {
		return err
	}
	n, ok, err := w.base.Store.ApplyRestyle(ctx, *r, scope, prompts.Epoch, out)
	if err != nil {
		w.log.WarnContext(ctx, "baseline: restyle write failed, drain paused", logging.Err(err))
		return w.stepBusy(ctx, r, prev, "restyle", err)
	}
	w.stepWritten()
	if ok && n > 0 {
		w.log.InfoContext(ctx, fmt.Sprintf("baseline: restyle: %s description revision %d (%d statements)", scope, n, len(out.Revision.Statements)))
	}
	return nil
}

// restyled is a restyle answer applied to the page under its rules: what to
// write, a revision when the text changed, else the layout in place. The
// answer's drops and confirmations were cleared by inference; an edit keeps
// its files; a new statement keeps only files of what it came from (from)
// and carries their sources, left out without any.
func (w *Worker) restyled(ctx context.Context, at string, r store.BaselineRun, scope string, page store.Page, res inference.RestyleResult) (store.Restyled, error) {
	byID := make(map[string]store.Statement, len(page.Statements))
	for _, s := range page.Statements {
		byID[s.ID] = s
	}
	patch := res.Patch
	patch.Drop, patch.Confirmed = nil, nil
	// An edit keeps the files it has: a restyle reads none.
	patch.Statements = slices.Clone(patch.Statements)
	for i, s := range patch.Statements {
		if s.ID != nil {
			if old, ok := byID[*s.ID]; ok {
				patch.Statements[i].Files = old.Files
			}
		}
	}
	added, sources, leftOut := splitOff(page, patch.Added, validFor(scope))
	if len(leftOut) > 0 {
		w.log.WarnContext(ctx, at+": new statement left out", slog.Any("statements", leftOut))
	}
	patch.Added = added
	applied := applyRestyle(page.Statements, patch, scope)
	if len(applied.BadEdits) > 0 {
		w.log.WarnContext(ctx, fmt.Sprintf("%s: edit left out: %s", at, strings.Join(applied.BadEdits, ", ")))
	}
	if len(applied.BadNew) > 0 {
		w.log.WarnContext(ctx, fmt.Sprintf("%s: new statement left out: %s", at, strings.Join(applied.BadNew, ", ")))
	}
	next, err := w.store.NextStatementID(ctx, scope, r.ProjectKey, slugOf(scope, r.Slug))
	if err != nil {
		w.log.WarnContext(ctx, "baseline: statement id read failed, drain paused", logging.Err(err))
		return store.Restyled{}, errStop
	}
	statements, parts, reason := attach(scope, page.Statements, applied.Statements, store.Source{Kind: store.FromBaseline, Commit: r.Commit, Repo: r.Slug}, next, limitsOf(w.limits))
	if reason != "" {
		// ApplyPatch left out what attach refuses: a bug, and the run's.
		return store.Restyled{}, fmt.Errorf("restyle refused: %s", reason)
	}
	logLeftOut(ctx, w.log, at, parts)
	// No credit, no check: the page's own sources and checks stay; a new
	// statement takes those it came from, numbered in the order attach gave.
	newSources := map[string][]store.Source{}
	for i, src := range sources {
		newSources[fmt.Sprintf("s%d", next+i)] = src
	}
	for i := range statements {
		s := &statements[i]
		if old, ok := byID[s.ID]; ok {
			s.Sources, s.Checked = old.Sources, old.Checked
			continue
		}
		s.Sources, s.Checked = newSources[s.ID], ""
	}
	statements = w.applyOutline(ctx, at, statements, res.Outline)
	summary := page.Summary
	if applied.Summary != nil {
		summary = proseSummary(*applied.Summary)
	}
	if summary == page.Summary && sameStatements(page.Statements, statements) {
		if slices.EqualFunc(statements, page.Statements, func(a, b store.Statement) bool { return a.ID == b.ID && a.Area == b.Area }) {
			w.log.InfoContext(ctx, at+": page stands as the rules want it, no revision")
			return store.Restyled{}, nil
		}
		w.log.InfoContext(ctx, fmt.Sprintf("%s: page laid out in %s, no revision", at, plural(areaCount(statements), "area")))
		return store.Restyled{ArrangeID: page.RevisionID, Arranged: statements}, nil
	}
	runID := r.ID
	rev := &store.NewRevision{
		Scope: scope, ProjectKey: r.ProjectKey, RepoSlug: slugOf(scope, r.Slug), Summary: summary, Statements: statements,
		Chars: prose(summary, statements), SourceKind: store.FromBaseline, SourceCommit: r.Commit, BaselineRunID: &runID,
	}
	if scope == store.ScopeProject {
		// The map is the project step's: carried as stored, sentinel and all.
		rev.Map = page.Map
	}
	grown, n := growth(page, summary, statements)
	w.log.InfoContext(ctx, fmt.Sprintf("%s: %d chars of prose more; %s split off", at, grown, plural(n, "statement")))
	return store.Restyled{Revision: rev}, nil
}

// applyRestyle is baseline.ApplyPatch for a page with no tree behind it: a
// file exists when the page names it, so a statement a pull request taught
// never sheds one.
func applyRestyle(current []store.Statement, p inference.Patch, scope string) baseline.Applied {
	files := map[string]bool{}
	for _, s := range current {
		for _, f := range s.Files {
			files[f] = true
		}
	}
	return baseline.ApplyPatch(current, p, func(path string) bool { return files[path] }, validFor(scope))
}

// splitOff holds a restyle's new statements to what they came from: the
// statements named in from (ids the page has) and the summary. Files are
// only those the statements named carry, any the page carries when it came
// from the summary; the sources are theirs, and the summary's are those of
// every statement sharing a file with it. One with no source to carry is
// left out, by name: a fact with no origin is a fact learned, which a
// restyle never does. One the page cannot hold (valid) is left out here
// too, so ApplyPatch takes every one and sources stays in the order of
// added, the order attach numbers them in.
func splitOff(page store.Page, proposed []inference.Statement, valid func(inference.Statement) string) (added []inference.Statement, sources [][]store.Source, leftOut []string) {
	byID := make(map[string]store.Statement, len(page.Statements))
	pageFiles := map[string]bool{}
	for _, s := range page.Statements {
		byID[s.ID] = s
		for _, f := range s.Files {
			pageFiles[f] = true
		}
	}
	for _, st := range proposed {
		if why := valid(st); why != "" {
			leftOut = append(leftOut, st.Name+" ("+why+")")
			continue
		}
		allowed := map[string]bool{}
		var from []store.Statement
		summary := false
		for _, id := range st.From {
			if strings.EqualFold(strings.TrimSpace(id), "summary") {
				summary = true
				continue
			}
			if s, ok := byID[id]; ok {
				from = append(from, s)
				for _, f := range s.Files {
					allowed[f] = true
				}
			}
		}
		if summary {
			allowed = pageFiles
		}
		st.Files = slices.DeleteFunc(slices.Clone(st.Files), func(f string) bool { return !allowed[f] })
		if summary {
			// The summary has no sources of its own: those of the
			// statements that rest on the files the new one names.
			for _, s := range page.Statements {
				if slices.ContainsFunc(s.Files, func(f string) bool { return slices.Contains(st.Files, f) }) {
					from = append(from, s)
				}
			}
		}
		var src []store.Source
		for _, s := range from {
			for _, x := range s.Sources {
				src = appendSource(src, x)
			}
		}
		if len(src) == 0 {
			leftOut = append(leftOut, st.Name+" (nothing it came from carries a source)")
			continue
		}
		st.From = nil
		added = append(added, st)
		sources = append(sources, src)
	}
	return added, sources, leftOut
}

// slugOf is a page's repository slug as the store keys it: "" for the
// project's.
func slugOf(scope, slug string) string {
	if scope == store.ScopeProject {
		return ""
	}
	return slug
}

// renderRestyle is a restyle call's prompt: the page as the steps show it,
// which statements pull requests taught without their keys; a project's
// with its repositories by slug.
func renderRestyle(r store.BaselineRun, page store.Page, l config.Limits, project bool, repoSlugs []string) string {
	tmpl := prompts.Restyle
	if project {
		tmpl = prompts.ProjectRestyle
	}
	return strings.NewReplacer(
		"{repo}", r.Slug, "{project}", r.ProjectKey, "{repo_slugs}", backticked(repoSlugs),
		"{repo_description}", describeTaught(page), "{project_description}", describeTaught(page),
		"{diagram_rules}", diagramRules(l), "{diagram_field}", diagramField(l),
	).Replace(tmpl)
}
