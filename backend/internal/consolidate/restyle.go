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
	"github.com/trick77/almanach/internal/lang"
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
// out. Nothing is dropped from a repository's page; the project's restyle
// judges the project's shape with its repositories' summaries and
// contracts in the prompt, and drops what is one repository's inside, which
// that repository's page holds (a collection's page keeps its summary
// alone). Each page's epoch is written with its step, so
// a restart or a later failure never pays for a step twice.

// epochs is the prompts' generation per scope, as the store asks it.
var epochs = store.Epochs{Repo: prompts.RepoEpoch, Project: prompts.ProjectEpoch}

// restylePlan is a restyle run's progress beside runPlan's ProjectDone:
// Repo, the repository's page is over (restyled, or not due).
type restylePlan struct {
	Repo bool `json:"repo,omitempty"`
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
	// The past failures' wait is the poller's: the run is queued.
	st, err := w.base.Store.RestyleState(ctx, r.ProjectKey, r.Slug, epochs, w.now())
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
		// A run's first call: none left is a cap below one call, never a
		// spent run. Failed, not skipped: a skip would leave the page due
		// and the poller queue the same run every cycle; a failure counts
		// against store.RestyleTries and ends.
		if r.Calls >= w.base.Limits.MaxCalls {
			return fmt.Errorf("no call left for the restyle")
		}
		res := w.base.Describer.Restyle(ctx, renderRestyle(*r, page, w.limits, nil), false)
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
	res := w.base.Describer.Restyle(ctx, renderRestyle(*r, page, w.limits, ctxt), true)
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
	plan.ProjectDone, r.Step = true, store.RestyleMapStep
	return w.writeRestyle(ctx, r, plan, store.ScopeProject, out)
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
	unchanged := 0
	for _, s := range patch.Statements {
		if s.ID != nil {
			if kept[*s.ID] {
				continue
			}
			if old, ok := byID[*s.ID]; ok {
				s.Files, s.Diagram = old.Files, old.Diagram
				if unchangedEdit(old, s) {
					unchanged++
				}
			}
		}
		edits = append(edits, s)
	}
	patch.Statements = edits
	// The prompt asks for the changed statements alone (prompts.RestyleEdits):
	// one returned as stored, or for its area alone, is answer the call paid
	// for and a big page cannot afford.
	if unchanged > 0 {
		w.log.InfoContext(ctx, fmt.Sprintf("%s: %s returned without a change of kind, name or text", at, plural(unchanged, "statement")))
	}
	// No tree: every edit keeps its stored files and the drops are taken
	// before, so no file check of ApplyPatch has anything to judge.
	applied := baseline.ApplyPatch(page.Statements, patch, func(string) bool { return true }, validFor(scope))
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
	for i := range statements {
		s := &statements[i]
		if old, ok := byID[s.ID]; ok {
			s.Sources, s.Checked = old.Sources, old.Checked
			continue
		}
		s.Sources, s.Checked = newSources[s.ID], ""
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

// unchangedEdit reports whether a restyle's edit of old says what old says:
// the same kind, its name and text the same once stored (cleanStatement),
// whatever its area.
func unchangedEdit(old store.Statement, s inference.Statement) bool {
	c := cleanStatement(s)
	return c.Kind == old.Kind && lang.SameButSpelling(old.Name, c.Name) && sameText(old.Text, c.Text)
}

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
			if strings.EqualFold(strings.TrimSpace(id), "summary") {
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

// renderRestyle is a restyle call's prompt: the page as the steps show it,
// which statements pull requests taught without their keys; with project,
// the project page's, with what it judges the shape by.
func renderRestyle(r store.BaselineRun, page store.Page, l config.Limits, project *projectContext) string {
	if project == nil {
		return strings.NewReplacer(
			"{repo}", r.Slug, "{project}", r.ProjectKey, "{repo_description}", describeTaught(page),
			"{diagram_rules}", diagramRules(l), "{diagram_field}", diagramField(l),
		).Replace(prompts.Restyle)
	}
	return strings.NewReplacer(
		"{project}", r.ProjectKey, "{repo_slugs}", backticked(project.slugs), "{project_description}", describeTaught(page),
		"{project_shape}", shapeShown(page), "{inner_edges}", innerEdgesShown(project.inner),
		"{other_repos}", project.repos, "{other_contracts}", project.contracts,
		"{diagram_rules}", diagramRules(l), "{diagram_field}", diagramField(l),
	).Replace(prompts.ProjectRestyle)
}
