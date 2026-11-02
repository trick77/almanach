package consolidate

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/trick77/almanach/internal/config"
	"github.com/trick77/almanach/internal/diagram"
	"github.com/trick77/almanach/internal/prompts"
	"github.com/trick77/almanach/internal/store"
	"github.com/trick77/ship/kit/logging"
)

// A departure run is what a deactivation queues: the repository that left
// is taken out of the project page, through the queue like every other
// write of it. The code does what it can decide alone first (the map's
// node, the statements only the departed repositories taught), then one
// call rewrites what still names them. The code's part is written even
// when the call is not to be had.

// departureStep is a departure run's one step. No page: nothing to take
// out. A project left with fewer than two repositories (no project page
// shown) or a run without a call left gets the code's part alone.
func (w *Worker) departureStep(ctx context.Context, r *store.BaselineRun, plan runPlan) error {
	page, err := w.projectPage(ctx, r.ProjectKey)
	if err != nil {
		return err
	}
	if page.RevisionID == 0 {
		w.log.InfoContext(ctx, "baseline: departure: the project has no page, nothing to take out")
		return w.finish(ctx, r, plan)
	}
	departed, err := w.departed(ctx, r.ProjectKey)
	if err != nil {
		return err
	}
	repos, err := w.repoSummaries(ctx, r)
	if err != nil {
		return err
	}
	active := slugs(repos)
	purged, err := w.purgeDeparted(ctx, r.ProjectKey, page, departed, active)
	if err != nil {
		return err
	}
	// The code's part as a revision, nil when the page named none of them.
	rev := carriedPage(r, page, purged.Statements, purged.Map)
	switch {
	case len(departed) == 0:
		w.log.InfoContext(ctx, "baseline: departure: no repository is deactivated any more, no call")
	case len(active) < 2:
		w.log.InfoContext(ctx, "baseline: departure: a project of one or none, no call")
	case r.Calls >= w.base.Limits.MaxCalls:
		w.log.WarnContext(ctx, "baseline: departure: the run's calls are spent, the code's part alone")
	default:
		res := w.base.Describer.Describe(ctx, renderDeparture(*r, purged, otherRepos(repos, ""), active, departedSlugs(departed), w.limits))
		okCall, err := w.call(ctx, r, store.PurposeProject, res.Call)
		if errors.Is(err, errStop) {
			return err
		}
		switch {
		case err != nil:
			// The gateway's attempts are spent: the code's part all the same.
			w.log.WarnContext(ctx, "baseline: departure: "+err.Error()+", the code's part alone")
		case !okCall:
			w.log.WarnContext(ctx, "baseline: departure: the call failed: "+res.Outcome.String()+", the code's part alone")
		case res.Description == nil:
			w.log.InfoContext(ctx, "baseline: departure: no rewrite asked")
		default:
			// The map is the code's: whatever came back, the purged one.
			d := *res.Description
			d.Map = purged.Map
			if diagram.IsSentinel(d.Map) {
				d.Map = nil
			}
			next, _, refused, err := w.projectRevision(ctx, r, plan, purged, &d, active)
			if err != nil {
				return err
			}
			if refused != "" {
				w.log.WarnContext(ctx, "baseline: departure: rewrite refused ("+refused+"), the code's part alone")
			} else if next != nil {
				rev = w.keepProvenance(ctx, purged, next)
				if rev.Summary == page.Summary && sameStatements(page.Statements, rev.Statements) && diagram.Equal(rev.Map, page.Map) {
					rev = nil
				}
			}
		}
	}
	prev := *r
	if err := w.markDone(ctx, r, plan); err != nil {
		return err
	}
	if rev == nil {
		w.log.InfoContext(ctx, "baseline: departure: the project page names no departed repository, no revision")
		return w.save(ctx, *r)
	}
	n, ok, err := w.base.Store.ApplyBaselineStep(ctx, *rev, *r)
	if err != nil {
		w.log.WarnContext(ctx, "baseline: departure revision write failed", logging.Err(err))
		return w.stepBusy(ctx, r, prev, "departure", err)
	}
	w.stepWritten()
	if ok {
		w.log.InfoContext(ctx, fmt.Sprintf("baseline: departure: project description revision %d (%d statements)", n, len(rev.Statements)))
	}
	return nil
}

// keepProvenance holds a departure rewrite to what it is: taking a
// repository out teaches nothing, so an edited statement keeps the sources
// it had (projectRevision credited the run, the departed repository's own
// commit), and a statement the call added is left out, logged.
func (w *Worker) keepProvenance(ctx context.Context, purged store.Page, rev *store.NewRevision) *store.NewRevision {
	had := map[string]store.Statement{}
	for _, s := range purged.Statements {
		had[s.ID] = s
	}
	out := *rev
	out.Statements = make([]store.Statement, 0, len(rev.Statements))
	var added []string
	for _, s := range rev.Statements {
		old, ok := had[s.ID]
		if !ok {
			added = append(added, s.ID)
			continue
		}
		s.Sources = old.Sources
		out.Statements = append(out.Statements, s)
	}
	if len(added) > 0 {
		w.log.WarnContext(ctx, "baseline: departure: statements added, left out", slog.Any("ids", added))
	}
	out.Chars = prose(out.Summary, out.Statements)
	return &out
}

// departed is the project's deactivated repositories.
func (w *Worker) departed(ctx context.Context, key string) ([]store.DepartedRepo, error) {
	departed, err := w.base.Store.DepartedRepos(ctx, key)
	if err != nil {
		w.log.WarnContext(ctx, "baseline: deactivated repositories read failed, drain paused", logging.Err(err))
		return nil, errStop
	}
	return departed, nil
}

// departedSlugs is the deactivated repositories by current slug, as the
// prompts name them.
func departedSlugs(departed []store.DepartedRepo) []string {
	out := make([]string, 0, len(departed))
	for _, d := range departed {
		out = append(out, d.Slug)
	}
	return out
}

// purgeDeparted is the project page with what the code can take out alone:
// every statement whose sources are all the departed repositories' (a
// baseline commit of one, by any name it ever had, or a pull request noted
// for one), and the map carried on with only the active repositories
// (carriedMap: their nodes out, a box only they talked to with them). A
// statement with no source, or one source the code cannot place, stays:
// the call decides it.
func (w *Worker) purgeDeparted(ctx context.Context, key string, page store.Page, departed []store.DepartedRepo, active []string) (store.Page, error) {
	out := page
	switch {
	case page.Map == nil || diagram.IsSentinel(page.Map):
		// No map to take anything off: as it is.
	case diagram.MapFits(len(active)):
		out.Map = w.carriedMap(ctx, page, active)
	default:
		out.Map = nil
	}
	if len(departed) == 0 {
		return out, nil
	}
	gone := map[string]bool{}
	for _, d := range departed {
		gone[d.Slug] = true
		for _, n := range d.Names {
			gone[n] = true
		}
	}
	var prs []string
	for _, s := range page.Statements {
		for _, src := range s.Sources {
			if !src.IsBaseline() && src.PRKey != "" {
				prs = append(prs, src.PRKey)
			}
		}
	}
	byPR, err := w.base.Store.PRRepos(ctx, key, prs)
	if err != nil {
		w.log.WarnContext(ctx, "baseline: pull request repositories read failed, drain paused", logging.Err(err))
		return store.Page{}, errStop
	}
	out.Statements = make([]store.Statement, 0, len(page.Statements))
	var dropped []string
	for _, s := range page.Statements {
		if onlyDeparted(s, gone, byPR) {
			dropped = append(dropped, s.ID)
			continue
		}
		out.Statements = append(out.Statements, s)
	}
	if len(dropped) > 0 {
		w.log.InfoContext(ctx, "baseline: departure: statements only departed repositories taught, dropped", slog.Any("ids", dropped))
	}
	return out, nil
}

// onlyDeparted reports whether every source of the statement is a
// departed repository's; a statement without any is not.
func onlyDeparted(s store.Statement, gone map[string]bool, byPR map[string]string) bool {
	if len(s.Sources) == 0 {
		return false
	}
	for _, src := range s.Sources {
		repo := src.Repo
		if !src.IsBaseline() {
			repo = byPR[src.PRKey]
		}
		if repo == "" || !gone[repo] {
			return false
		}
	}
	return true
}

// renderDeparture is the departure call's prompt: the project page after
// the code's part, the remaining repositories and the departed ones.
func renderDeparture(r store.BaselineRun, page store.Page, others string, active, departed []string, l config.Limits) string {
	return strings.NewReplacer(
		"{project}", r.ProjectKey, "{departed}", backticked(departed), "{repo_slugs}", backticked(active),
		"{project_description}", describeTaught(page), "{other_repos}", others,
		"{diagram_rules}", diagramRules(l), "{diagram_field}", diagramField(l),
	).Replace(prompts.ProjectDeparture)
}

// departedRule is the project prompt's rule for the deactivated
// repositories, "" when there are none.
func departedRule(departed []string) string {
	if len(departed) == 0 {
		return ""
	}
	return "\n- These repositories were taken out of the project and are no part of it: " + backticked(departed) +
		". Write nothing about them; a statement or a part of the summary that still names one is rewritten without it, or dropped when it is about one alone."
}

// backticked is slugs as the prompts name them: in backticks, comma-separated.
func backticked(slugs []string) string {
	out := make([]string, len(slugs))
	for i, s := range slugs {
		out[i] = "`" + s + "`"
	}
	return strings.Join(out, ", ")
}
