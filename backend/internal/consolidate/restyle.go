package consolidate

import (
	"context"
	"errors"
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
// redescribe"). One call for the repository's page (a page too large for
// one answer one a part, restyleCut), one for its project's
// when that is due, then the map step as in any run, which draws only when
// the text moved. It learns nothing, so it neither credits nor checks: an
// edit keeps its sources, its files and its last check; a statement split
// out of others carries their sources, and with none to carry it is left
// out. Nothing is dropped from a repository's page; the project's restyle
// judges the project's shape with its repositories' summaries and
// contracts in the prompt, and drops what is one repository's inside, which
// that repository's page holds (a collection's page keeps its summary
// alone). Each page's epoch is written with its step, so
// a restart or a later failure never pays for a step twice.

// epochs is the prompts' generation per scope, as the store asks it.
var epochs = store.Epochs{Repo: prompts.RepoEpoch, Project: prompts.ProjectEpoch}

// restylePlan is a restyle run's progress beside runPlan's ProjectDone:
// Repo, the repository's page is over (restyled, or not due). Manual, set
// when queued, is a button's restyle, run past the tries (store.RestyleDue).
type restylePlan struct {
	Repo   bool `json:"repo,omitempty"`
	Manual bool `json:"manual,omitempty"`
	// Parts is a repository page's restyle in parts so far, nil for one in
	// one call.
	Parts *restyleParts `json:"parts,omitempty"`
}

// restyleParts is a restyle in parts under way: the revision its parts
// were cut from, each part's statement ids, and the answers of those done,
// kept with the run so a restart, a gateway's hold or the next try after a
// failed part pays no part twice (one call a step, the page written once
// all are answered).
type restyleParts struct {
	Revision int64           `json:"revision"`
	Cut      [][]string      `json:"cut"`
	Answers  []restyleAnswer `json:"answers"`
}

// restyleAnswer is one part's answer as the merge takes it.
type restyleAnswer struct {
	Patch   inference.Patch         `json:"patch"`
	Outline []inference.OutlineArea `json:"outline,omitempty"`
}

// startRestyle starts a restyle run: no clone, no head pinned, at the
// documented commit the store queued it with, straight to its steps.
func (w *Worker) startRestyle(ctx context.Context, r *store.BaselineRun) error {
	now := w.now().UTC()
	r.StartedAt, r.State, r.Error, r.Step = &now, store.RunProject, "", store.RestyleRepoStep
	w.log.InfoContext(ctx, fmt.Sprintf("baseline: restyle run started (epoch %d)", r.Epoch))
	return w.save(ctx, *r)
}

// restyleStep is a restyle run's next step: the repository's page, then
// the project's, each only while the store says it is due (the poller's
// own answer, so the run never ends with a page left due it judged not
// due); then the map step.
func (w *Worker) restyleStep(ctx context.Context, r *store.BaselineRun, plan runPlan) error {
	// The past failures' wait is the poller's: the run is queued. One
	// queued by hand runs past the tries too, its button the way out of
	// them.
	st, err := w.base.Store.RestyleState(ctx, r.ProjectKey, r.Slug, epochs, w.now())
	if plan.Restyle.Manual {
		st, err = w.base.Store.RestyleDue(ctx, r.ProjectKey, r.Slug, epochs)
	}
	if err != nil {
		w.log.WarnContext(ctx, "baseline: restyle state read failed, drain paused", logging.Err(err))
		return errStop
	}
	if !plan.Restyle.Repo {
		if !st.RepoDue {
			w.log.InfoContext(ctx, "baseline: restyle: the repository page is not due")
			plan.Restyle.Repo, r.Step = true, store.RestyleProjectStep
			if err := encodeInto(r, plan); err != nil {
				return err
			}
			return w.save(ctx, *r)
		}
		return w.restyleRepo(ctx, r, plan)
	}
	if st.ProjectDue {
		return w.restyleProject(ctx, r, plan)
	}
	return w.restyleMap(ctx, r, plan)
}

// restyleMap ends a restyle's page steps: on to the map step, counted as
// its own (store.RestyleMapStep), so a map that fails spends no page's
// tries.
func (w *Worker) restyleMap(ctx context.Context, r *store.BaselineRun, plan runPlan) error {
	r.Step = store.RestyleMapStep
	return w.projectOver(ctx, r, plan)
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
		parts, shown := restyleCut(page, w.base.Describer.RestyleLimits().PartChars)
		if parts != nil {
			return w.restylePart(ctx, r, plan, page, parts)
		}
		// A run's first call: none left is a cap below one call, never a
		// spent run. Failed, not skipped: a skip would leave the page due
		// and the poller queue the same run every cycle; a failure counts
		// against store.RestyleTries and ends.
		if r.Calls >= w.base.Limits.MaxCalls {
			return fmt.Errorf("no call left for the restyle")
		}
		if err := w.tooLarge(plan, "the repository page", len(shown)); err != nil {
			return err
		}
		res := w.base.Describer.Restyle(ctx, renderRestyle(*r, page, shown, w.limits, nil), len(shown), false)
		okCall, err := w.call(ctx, r, store.PurposeRestyle, res.Call)
		if err != nil {
			return err
		}
		if !okCall {
			w.logRestyledPage(ctx, at, page, len(shown), res.Outcome)
			return callFailed("the restyle call", res.Call)
		}
		if out, err = w.restyled(ctx, at, *r, store.ScopeRepository, page, res); err != nil {
			return err
		}
	}
	plan.Restyle.Repo, r.Step = true, store.RestyleProjectStep
	return w.writeRestyle(ctx, r, plan, store.ScopeRepository, out)
}

// restyleProject is the project page's restyle: the page, with what the
// project call judges its shape by (every repository's summary, interfaces
// and entities, the build dependencies between them), never their code;
// its map is carried as a baseline run reads it (projectPage: one stored
// before the tags as none, drawn by the next project step), none for a
// collection.
func (w *Worker) restyleProject(ctx context.Context, r *store.BaselineRun, plan runPlan) error {
	at := "baseline: restyle " + projectLabel
	page, err := w.projectPage(ctx, r.ProjectKey)
	if err != nil {
		return err
	}
	if r.Calls >= w.base.Limits.MaxCalls {
		// The run's calls spent by the repository's page: still due, the
		// next restyle of one of its repositories takes it, with a call.
		w.log.WarnContext(ctx, at+" skipped: the run's calls are spent, still due")
		return w.restyleMap(ctx, r, plan)
	}
	shown := describeTaught(page)
	if err := w.tooLarge(plan, "the project page", len(shown)); err != nil {
		return err
	}
	// The active source repositories, every one of them: the run's own is
	// one among the others here, its page not in the prompt.
	repos, err := w.repoSummaries(ctx, r)
	if err != nil {
		return err
	}
	inner, err := w.innerEdges(ctx, r.ProjectKey)
	if err != nil {
		return err
	}
	ctxt := &projectContext{repos: otherRepos(repos, ""), contracts: otherContracts(repos, ""), inner: inner, slugs: slugs(repos)}
	res := w.base.Describer.Restyle(ctx, renderRestyle(*r, page, shown, w.limits, ctxt), len(shown), true)
	okCall, err := w.call(ctx, r, store.PurposeRestyle, res.Call)
	if err != nil {
		return err
	}
	if !okCall {
		w.logRestyledPage(ctx, at, page, len(shown), res.Outcome)
		return callFailed("the project restyle call", res.Call)
	}
	out, err := w.restyled(ctx, at, *r, store.ScopeProject, page, res)
	if err != nil {
		return err
	}
	plan.ProjectDone, r.Step = true, store.RestyleMapStep
	return w.writeRestyle(ctx, r, plan, store.ScopeProject, out)
}

// tooLarge refuses, for the poller's restyle, a call no answer can hold
// (inference.RestyleLimits' OneCallMax, by the model): it is cut at its cap
// on every try, paid each time. A button's restyle asks all the same.
// Failed, it counts against the page's tries like any.
func (w *Worker) tooLarge(plan runPlan, what string, chars int) error {
	most := w.base.Describer.RestyleLimits().OneCallMax
	if plan.Restyle.Manual || chars <= most {
		return nil
	}
	return fmt.Errorf("%s is too large to restyle in one call (%d chars shown, at most %d), not asked for", what, chars, most)
}

// logRestyledPage says, beside a failed restyle call, what page it was
// asked to restyle: its statements, its flows and its prose, the size the
// answer is held against (a runaway of 2026-11-10 wrote 49k tokens for an
// 18k-token prompt). Only for an answer cut or off its schema: a gateway's
// day or a shutdown says nothing of the page.
func (w *Worker) logRestyledPage(ctx context.Context, at string, page store.Page, shown int, outcome inference.Outcome) {
	if outcome != inference.OutcomeTooLong && outcome != inference.OutcomeUnparseable {
		return
	}
	flows := 0
	for _, s := range page.Statements {
		if s.Kind == "flow" {
			flows++
		}
	}
	w.log.WarnContext(ctx, fmt.Sprintf("%s: the call failed on a page of %s (%s), %d chars of prose, %d chars as shown",
		at, plural(len(page.Statements), "statement"), plural(flows, "flow"), prose(page.Summary, page.Statements), shown))
}

// writeRestyle writes a restyle step with the run's progress and the page's
// epoch in one transaction.
func (w *Worker) writeRestyle(ctx context.Context, r *store.BaselineRun, plan runPlan, scope string, out store.Restyled) error {
	prev := *r
	if err := encodeInto(r, plan); err != nil {
		return err
	}
	// Each page at its own scope's epoch (store.Epochs).
	epoch := prompts.RepoEpoch
	if scope == store.ScopeProject {
		epoch = prompts.ProjectEpoch
	}
	n, ok, err := w.base.Store.ApplyRestyle(ctx, *r, scope, epoch, out)
	if err != nil {
		w.log.WarnContext(ctx, "baseline: restyle write failed", logging.Err(err))
		return w.stepBusy(ctx, r, prev, "restyle", err)
	}
	w.stepWritten()
	if ok && n > 0 {
		w.log.InfoContext(ctx, fmt.Sprintf("baseline: restyle: %s description revision %d (%d statements)", scope, n, len(out.Revision.Statements)))
	}
	return nil
}

// restyled is a restyle answer applied to the page under its rules: what to
// write, a revision when the text changed, else the layout in place. A drop
// or a confirmation, which the schema never offers, is no restyle's; an
// edit keeps its files; a new statement keeps only files of what it came
// from (from) and carries their sources, left out without any.
func (w *Worker) restyled(ctx context.Context, at string, r store.BaselineRun, scope string, stored store.Page, res inference.RestyleResult) (store.Restyled, error) {
	patch := res.Patch
	patch.Confirmed = nil
	// page is the page the answer is applied to: the repository's as
	// stored, which a restyle never drops from; the project's less what the
	// answer drops (projectDrops), under the shape it judged, a collection's
	// its summary alone.
	page, shape := stored, ""
	if scope == store.ScopeProject {
		shape = w.judgedShape(ctx, "restyle "+projectLabel, stored, res.Shape)
		if shape == store.ShapeCollection {
			answered := ""
			if patch.Summary != nil {
				answered = proseSummary(*patch.Summary)
			}
			// A page turned into a collection needs the collection's summary:
			// refused, the step fails and counts against the page's tries.
			if why := collectionWithout(stored, shape, answered); why != "" {
				return store.Restyled{}, fmt.Errorf("project restyle refused: %s", why)
			}
			summary := stored.Summary
			if strings.TrimSpace(answered) != "" {
				summary = answered
			}
			rr := r
			return store.Restyled{Revision: w.collectionRevision(ctx, &rr, stored, summary, append(slices.Clone(patch.Statements), patch.Added...))}, nil
		}
	}
	// A new statement takes its sources from what it came from as stored,
	// one split out of a statement the answer drops included (a component's
	// interface moved out before the rest goes).
	added, sources, leftOut, kept := splitOff(stored, patch.Added, validFor(scope))
	if len(leftOut) > 0 {
		w.log.WarnContext(ctx, at+": new statement left out, what it came from kept as stored", slog.Any("statements", leftOut))
	}
	patch.Added = added
	if kept["summary"] {
		patch.Summary = nil
	}
	if scope == store.ScopeProject {
		// What a left-out statement came from is kept, dropped or not: its
		// text would go with it.
		drop := slices.DeleteFunc(slices.Clone(patch.Drop), func(id string) bool { return kept[id] })
		page.Statements = w.projectDrops(ctx, at, stored.Statements, drop, patch.Statements)
	}
	patch.Drop = nil
	byID := make(map[string]store.Statement, len(page.Statements))
	for _, s := range page.Statements {
		byID[s.ID] = s
	}
	// An edit keeps the files it has, a restyle reads none, and the diagram
	// it has, or none: a reworded text returned without it would drop it,
	// and a restyle drops nothing nor draws what no reading taught. One a
	// left-out statement came from stays as stored.
	edits := make([]inference.Statement, 0, len(patch.Statements))
	returned := map[string]bool{}
	for _, s := range patch.Statements {
		if s.ID != nil {
			if kept[*s.ID] {
				continue
			}
			if old, ok := byID[*s.ID]; ok {
				s.Files, s.Diagram = old.Files, old.Diagram
				returned[*s.ID] = true
			}
		}
		edits = append(edits, s)
	}
	patch.Statements = edits
	// No tree: every edit keeps its stored files and the drops are taken
	// before, so no file check of ApplyPatch has anything to judge.
	applied := baseline.ApplyPatch(page.Statements, patch, func(string) bool { return true }, validFor(scope))
	if len(applied.BadEdits) > 0 {
		w.log.WarnContext(ctx, fmt.Sprintf("%s: edit left out: %s", at, strings.Join(applied.BadEdits, ", ")))
	}
	// An edit left out leaves its statement as stored: no unchanged echo.
	for _, bad := range applied.BadEdits {
		if f := strings.Fields(bad); len(f) > 0 {
			delete(returned, f[0])
		}
	}
	if len(applied.BadNew) > 0 {
		w.log.WarnContext(ctx, fmt.Sprintf("%s: new statement left out: %s", at, strings.Join(applied.BadNew, ", ")))
	}
	next, err := w.store.NextStatementID(ctx, scope, r.ProjectKey, slugOf(scope, r.Slug))
	if err != nil {
		w.log.WarnContext(ctx, "baseline: statement id read failed, drain paused", logging.Err(err))
		return store.Restyled{}, errStop
	}
	statements, parts, reason := attach(scope, page.Statements, applied.Statements, only(store.Source{Kind: store.FromBaseline, Commit: r.Commit, Repo: r.Slug}), next, limitsOf(w.limits))
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
	unchanged := 0
	for i := range statements {
		s := &statements[i]
		if old, ok := byID[s.ID]; ok {
			s.Sources, s.Checked = old.Sources, old.Checked
			if returned[s.ID] && s.Kind == old.Kind && s.Name == old.Name && s.Text == old.Text {
				unchanged++
			}
			continue
		}
		s.Sources, s.Checked = newSources[s.ID], ""
	}
	// The prompt asks for the changed statements alone (prompts.RestyleEdits):
	// one returned exactly as stored, for its area alone or nothing, is answer
	// the call paid for and a big page cannot afford.
	if unchanged > 0 {
		w.log.InfoContext(ctx, fmt.Sprintf("%s: %s returned without a change of kind, name or text", at, plural(unchanged, "statement")))
	}
	// An area goes by the outline alone (prompts.RestyleEdits), which may be
	// null only when every statement has one: none for a page that lacks
	// some leaves it unarranged under the new epoch, so the step fails and
	// counts against the page's tries.
	if res.Outline == nil && !baseline.Arranged(statements) {
		return store.Restyled{}, fmt.Errorf("restyle refused: no outline, and the page has statements without an area")
	}
	statements = w.applyOutline(ctx, at, statements, res.Outline)
	summary := stored.Summary
	if applied.Summary != nil {
		summary = proseSummary(*applied.Summary)
	}
	if summary == stored.Summary && sameStatements(stored.Statements, statements) && !shapeMoves(stored, shape) {
		// A first judgment that moves nothing is set in place.
		var out store.Restyled
		if scope == store.ScopeProject && shape != stored.Shape {
			out.JudgeID, out.Shape = stored.RevisionID, shape
		}
		if slices.EqualFunc(statements, stored.Statements, func(a, b store.Statement) bool { return a.ID == b.ID && a.Area == b.Area }) {
			w.log.InfoContext(ctx, at+": page stands as the rules want it, no revision")
			return out, nil
		}
		w.log.InfoContext(ctx, fmt.Sprintf("%s: page laid out in %s, no revision", at, plural(areaCount(statements), "area")))
		out.ArrangeID, out.Arranged = stored.RevisionID, statements
		return out, nil
	}
	runID := r.ID
	rev := &store.NewRevision{
		Scope: scope, ProjectKey: r.ProjectKey, RepoSlug: slugOf(scope, r.Slug), Summary: summary, Statements: statements,
		Chars: prose(summary, statements), SourceKind: store.FromBaseline, SourceCommit: r.Commit, BaselineRunID: &runID,
	}
	if scope == store.ScopeProject {
		// The map is the project step's: carried as projectPage read it,
		// none where a collection's page had none (due to the next step).
		rev.Map, rev.Shape = stored.Map, shape
	}
	grown, n := growth(stored, summary, statements)
	w.log.InfoContext(ctx, fmt.Sprintf("%s: %d chars of prose more; %s split off", at, grown, plural(n, "statement")))
	return store.Restyled{Revision: rev}, nil
}

// isSummaryRef reports whether an id in a new statement's from names the
// summary, in any case or spacing the model wrote it.
func isSummaryRef(id string) bool { return strings.EqualFold(strings.TrimSpace(id), "summary") }

// splitOff holds a restyle's new statements to what they came from: the
// statements named in from (ids the page has) and the summary. Files are
// only those the statements named carry, any the page carries when it came
// from the summary; the sources are theirs, and the summary's are those of
// every statement sharing a file with it. One with no source to carry is
// left out, by name: a fact with no origin is a fact learned, which a
// restyle never does. One the page cannot hold (valid) is left out here
// too, so ApplyPatch takes every one and sources stays in the order of
// added, the order attach numbers them in. kept is what a left-out one
// came from: those statements' ids, "summary" for the summary, which the
// restyle keeps as stored, or the text the answer moved out of them would
// be lost with it.
func splitOff(page store.Page, proposed []inference.Statement, valid func(inference.Statement) string) (added []inference.Statement, sources [][]store.Source, leftOut []string, kept map[string]bool) {
	kept = map[string]bool{}
	leave := func(st inference.Statement, why string) {
		leftOut = append(leftOut, st.Name+" ("+why+")")
		for _, id := range st.From {
			if isSummaryRef(id) {
				id = "summary"
			}
			kept[id] = true
		}
	}
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
			leave(st, why)
			continue
		}
		allowed := map[string]bool{}
		var from []store.Statement
		summary := false
		for _, id := range st.From {
			if isSummaryRef(id) {
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
			leave(st, "nothing it came from carries a source")
			continue
		}
		st.From = nil
		added = append(added, st)
		sources = append(sources, src)
	}
	return added, sources, leftOut, kept
}

// slugOf is a page's repository slug as the store keys it: "" for the
// project's.
func slugOf(scope, slug string) string {
	if scope == store.ScopeProject {
		return ""
	}
	return slug
}

// projectContext is what a project restyle reads beside the page: its
// repositories' summaries and contracts as the project step shows them, the
// build dependencies between them, their slugs.
type projectContext struct {
	repos, contracts string
	inner            []store.Edge
	slugs            []string
}

// renderRestyle is a restyle call's prompt: the page as the steps show it
// (shown, describeTaught's: which statements pull requests taught, without
// their keys); with project, the project page's, with what it judges the
// shape by.
func renderRestyle(r store.BaselineRun, page store.Page, shown string, l config.Limits, project *projectContext) string {
	if project == nil {
		return renderRestylePart(r, shown, "", l)
	}
	return strings.NewReplacer(
		"{project}", r.ProjectKey, "{repo_slugs}", backticked(project.slugs), "{project_description}", shown,
		"{project_shape}", shapeShown(page), "{inner_edges}", innerEdgesShown(project.inner),
		"{other_repos}", project.repos, "{other_contracts}", project.contracts,
		"{diagram_rules}", diagramRules(l), "{diagram_field}", diagramField(l),
	).Replace(prompts.ProjectRestyle)
}

// renderRestylePart is the repository page's restyle prompt: shown the
// page or a part of it, part what RestylePart tells a part, "" for the
// whole page in one call.
func renderRestylePart(r store.BaselineRun, shown, part string, l config.Limits) string {
	return strings.NewReplacer(
		"{repo}", r.Slug, "{project}", r.ProjectKey, "{repo_description}", shown, "{restyle_part}", part,
		"{diagram_rules}", diagramRules(l), "{diagram_field}", diagramField(l),
	).Replace(prompts.Restyle)
}

// restylePiece is one part of a restyle in parts: its statements, shown in
// full to its call, and the summary with the first.
type restylePiece struct {
	statements []store.Statement
	summary    bool
	// chars is the part as shown, about: its statements' and the summary's.
	chars int
}

// restyleCut is a repository page too large for one restyle answer in
// parts of at most budget chars as shown (inference.RestyleLimits'
// PartChars, by the model), nil for a page that fits one call; shown is
// then the whole page as describeTaught shows it, for that one call, and ""
// with parts. Statements keep the page's order, an area's together
// while they fit (the page stands in its areas' order, baseline.Arrange):
// one area too large is split by its statements, one statement too large
// is a part of its own. The summary goes with the first statements, never
// a part alone (a part with no statements is told every one is new). A
// page from before areas is cut the same, its statements without one a run
// of their own: each part lays its own out.
func restyleCut(page store.Page, budget int) (parts []restylePiece, shown string) {
	// Sized by statement, the whole page rendered only for the one call: a
	// restyle in parts cuts the page again on every part's step.
	sizes := make([]int, len(page.Statements))
	total := len(page.Summary)
	for i, s := range page.Statements {
		var b strings.Builder
		describeStatement(&b, s, pageView{prs: prTaught})
		sizes[i] = b.Len()
		total += sizes[i]
	}
	if total <= budget {
		return nil, describeTaught(page)
	}
	cur := restylePiece{summary: true, chars: len(page.Summary)}
	flush := func() {
		if len(cur.statements) > 0 {
			parts = append(parts, cur)
			cur = restylePiece{}
		}
	}
	for i := 0; i < len(page.Statements); {
		j, run := i, 0
		for ; j < len(page.Statements) && page.Statements[j].Area == page.Statements[i].Area; j++ {
			run += sizes[j]
		}
		if cur.chars+run > budget && run <= budget {
			flush()
		}
		for k := i; k < j; k++ {
			if cur.chars+sizes[k] > budget {
				flush()
			}
			cur.statements = append(cur.statements, page.Statements[k])
			cur.chars += sizes[k]
		}
		i = j
	}
	flush()
	if len(parts) < 2 {
		// One statement alone too large: one call, a part being no less.
		return nil, describeTaught(page)
	}
	return parts, ""
}

// cutIDs is the parts' statement ids, what a restyle in parts keeps with
// its answers: answers fit only the cut they were asked for.
func cutIDs(parts []restylePiece) [][]string {
	out := make([][]string, len(parts))
	for i, p := range parts {
		for _, s := range p.statements {
			out[i] = append(out[i], s.ID)
		}
	}
	return out
}

// restylePrompt is a part's prompt: its statements in full, the summary
// with the first, every other statement of the page by id, kind, name and
// area; and its cap's size, the part as shown without that list.
func restylePrompt(r store.BaselineRun, page store.Page, parts []restylePiece, i int, l config.Limits) (string, int) {
	p := parts[i]
	shown := describeTaught(p.page(page))
	mine := map[string]bool{}
	for _, s := range p.statements {
		mine[s.ID] = true
	}
	var others strings.Builder
	for _, s := range page.Statements {
		if mine[s.ID] {
			continue
		}
		fmt.Fprintf(&others, "- [%s] %s: %s", s.ID, s.Kind, s.Name)
		if s.Area != "" {
			fmt.Fprintf(&others, " (%s)", s.Area)
		}
		others.WriteString("\n")
	}
	part := fmt.Sprintf(prompts.RestylePart, i+1, len(parts))
	if !p.summary {
		part += prompts.RestyleLaterPart
	}
	return renderRestylePart(r, shown+"\n## Other parts of the description\n\n"+others.String(), part, l), len(shown)
}

// restylePart is a step of a restyle in parts: the next part's call, its
// answer kept with the run; once all are answered, the answers as one
// (mergeParts) applied and written as a restyle in one call is. Answers
// fit only the cut they were asked for: kept from another revision or
// another cut, they start over. A run's first part takes the answers the
// newest failed try paid for (store.FailedRestylePlan), so a part that
// failed is asked again, never those before it.
func (w *Worker) restylePart(ctx context.Context, r *store.BaselineRun, plan runPlan, page store.Page, parts []restylePiece) error {
	at := "baseline: restyle"
	cut := cutIDs(parts)
	done := plan.Restyle.Parts
	if done == nil {
		done = w.failedParts(ctx, r)
	}
	if done != nil && (done.Revision != page.RevisionID || !slices.EqualFunc(done.Cut, cut, slices.Equal)) {
		w.log.InfoContext(ctx, fmt.Sprintf("%s: the answers kept were asked for revision %d's %s, the page is revision %d in %s: its parts start over",
			at, done.Revision, plural(len(done.Cut), "part"), page.RevisionID, plural(len(cut), "part")))
		done = nil
	}
	carried := false
	if done == nil {
		done = &restyleParts{Revision: page.RevisionID, Cut: cut}
	} else if plan.Restyle.Parts == nil && len(done.Answers) > 0 {
		w.log.InfoContext(ctx, fmt.Sprintf("%s: %s of %d taken from the failed try", at, plural(len(done.Answers), "part"), len(parts)))
		carried = true
	}
	plan.Restyle.Parts = done
	if carried {
		// Kept with this run at once: should its next part fail too, the
		// try after it takes them from here.
		if err := encodeInto(r, plan); err != nil {
			return err
		}
	}
	if i := len(done.Answers); i < len(parts) {
		label := fmt.Sprintf("part %d of %d", i+1, len(parts))
		for k := i; k < len(parts); k++ {
			if err := w.tooLarge(plan, fmt.Sprintf("part %d of %d", k+1, len(parts)), parts[k].chars); err != nil {
				return err
			}
		}
		// The run's calls spent: failed with the answers so far kept, so
		// the next try goes on from here (a page of more parts than one
		// run's calls takes several tries).
		if r.Calls >= w.base.Limits.MaxCalls {
			return fmt.Errorf("the run's calls are spent after %d of %s, the next try goes on", i, plural(len(parts), "part"))
		}
		prompt, chars := restylePrompt(*r, page, parts, i, w.limits)
		w.log.InfoContext(ctx, fmt.Sprintf("%s: %s, %d chars as shown", at, label, chars))
		res := w.base.Describer.Restyle(ctx, prompt, chars, false)
		okCall, err := w.call(ctx, r, store.PurposeRestyle, res.Call)
		if err != nil {
			return err
		}
		if !okCall {
			w.logRestyledPage(ctx, at+" ("+label+")", parts[i].page(page), chars, res.Outcome)
			return callFailed("the restyle call ("+label+")", res.Call)
		}
		// Kept before anything else: a hold or a refused write after it
		// never asks this part again.
		done.Answers = append(done.Answers, restyleAnswer{Patch: res.Patch, Outline: res.Outline})
		if err := encodeInto(r, plan); err != nil {
			return err
		}
		return w.save(ctx, *r)
	}
	merged, err := w.mergeParts(ctx, at, parts, done.Answers)
	var out store.Restyled
	if err == nil {
		out, err = w.restyled(ctx, at, *r, store.ScopeRepository, page, merged)
	}
	if err != nil && !errors.Is(err, errStop) {
		// Refused for what they say: the answers go with the failure, so
		// the next try asks again rather than refusing the same ones for
		// free until its tries are spent.
		plan.Restyle.Parts = nil
		if encErr := encodeInto(r, plan); encErr != nil {
			return encErr
		}
	}
	if err != nil {
		return err
	}
	plan.Restyle.Parts = nil
	plan.Restyle.Repo, r.Step = true, store.RestyleProjectStep
	return w.writeRestyle(ctx, r, plan, store.ScopeRepository, out)
}

// page is a part as a page: what a failed part's log line counts.
func (p restylePiece) page(whole store.Page) store.Page {
	out := store.Page{RevisionID: whole.RevisionID, Statements: p.statements}
	if p.summary {
		out.Summary = whole.Summary
	}
	return out
}

// failedParts is the parts the newest failed restyle of the run's
// repository at its epoch kept, nil for none or a plan that does not read:
// a read that fails only costs those parts again.
func (w *Worker) failedParts(ctx context.Context, r *store.BaselineRun) *restyleParts {
	raw, err := w.base.Store.FailedRestylePlan(ctx, r.ProjectKey, r.Slug, r.Epoch)
	if err != nil {
		w.log.WarnContext(ctx, "baseline: restyle: the failed try's parts not read, asked again", logging.Err(err))
		return nil
	}
	if raw == nil {
		return nil
	}
	failed, err := decodePlan(raw)
	if err != nil || failed.Restyle == nil {
		return nil
	}
	return failed.Restyle.Parts
}

// mergeParts is the parts' answers as one restyle answer: each part's edits,
// new statements and outline of its own statements, what one names of
// another's left out and logged; the summary the first part's. A part that
// keeps its layout (outline null) stands as laid out, its statements' areas
// in its outline, so the parts' chapters keep the page's order; one that
// does with statements lacking an area is refused, as one call's is.
func (w *Worker) mergeParts(ctx context.Context, at string, parts []restylePiece, answers []restyleAnswer) (inference.RestyleResult, error) {
	var out inference.RestyleResult
	if len(answers) != len(parts) {
		return out, fmt.Errorf("restyle refused: %d answers for %s", len(answers), plural(len(parts), "part"))
	}
	out.Patch.Statements = []inference.Statement{}
	var foreign []string
	for i, a := range answers {
		if a.Outline == nil {
			for _, s := range parts[i].statements {
				switch {
				case s.Area == "":
					return out, fmt.Errorf("restyle refused: part %d of %d gave no outline, and its statements lack an area", i+1, len(parts))
				case len(out.Outline) > 0 && out.Outline[len(out.Outline)-1].Area == s.Area:
					last := &out.Outline[len(out.Outline)-1]
					last.IDs = append(last.IDs, s.ID)
				default:
					out.Outline = append(out.Outline, inference.OutlineArea{Area: s.Area, IDs: []string{s.ID}})
				}
			}
		}
		mine := map[string]bool{}
		for _, s := range parts[i].statements {
			mine[s.ID] = true
		}
		if parts[i].summary {
			out.Patch.Summary = a.Patch.Summary
		} else if a.Patch.Summary != nil {
			foreign = append(foreign, fmt.Sprintf("summary (part %d)", i+1))
		}
		// A new statement taken partly from another part's statement is
		// left out, and what it came from here kept as stored (as splitOff
		// keeps it): that other statement was not shortened, so its text
		// would stand twice.
		kept := map[string]bool{}
		for _, s := range a.Patch.Added {
			own := func(id string) bool { return mine[id] || (parts[i].summary && isSummaryRef(id)) }
			if slices.ContainsFunc(s.From, func(id string) bool { return !own(id) }) {
				foreign = append(foreign, fmt.Sprintf("new %q taken from another part's (part %d)", s.Name, i+1))
				for _, id := range s.From {
					if isSummaryRef(id) {
						id = "summary"
					}
					kept[id] = true
				}
				continue
			}
			out.Patch.Added = append(out.Patch.Added, s)
		}
		if kept["summary"] && parts[i].summary {
			out.Patch.Summary = nil
		}
		for _, s := range a.Patch.Statements {
			if s.ID != nil && !mine[*s.ID] {
				foreign = append(foreign, fmt.Sprintf("%s (part %d)", *s.ID, i+1))
				continue
			}
			if s.ID != nil && kept[*s.ID] {
				continue
			}
			out.Patch.Statements = append(out.Patch.Statements, s)
		}
		for _, area := range a.Outline {
			ids := slices.DeleteFunc(slices.Clone(area.IDs), func(id string) bool {
				if !mine[id] {
					foreign = append(foreign, fmt.Sprintf("%s in the outline (part %d)", id, i+1))
				}
				return !mine[id]
			})
			if len(ids) > 0 {
				out.Outline = append(out.Outline, inference.OutlineArea{Area: area.Area, IDs: ids})
			}
		}
	}
	if len(foreign) > 0 {
		w.log.WarnContext(ctx, at+": another part's left out: "+strings.Join(foreign, ", "))
	}
	return out, nil
}
