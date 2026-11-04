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
// write of it. The code takes it off the system map (a node is a slug, the
// code's to decide); the statements and the summary are the call's: a
// baseline source says which run wrote a project statement, never what it
// is about, so no source decides one. The map is written even when the
// call is not to be had; the departure then stays due, and the next project
// step of any repository takes it out (DepartureDue).

// departureStep is a departure run's one step. No page: nothing to take
// out. Nothing due (an earlier call took every departed repository out):
// the map alone, no call. A project left with fewer than two repositories
// shows no page: the map alone, the departure settled.
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
	due, err := w.base.Store.DepartureDue(ctx, r.ProjectKey)
	if err != nil {
		w.log.WarnContext(ctx, "baseline: departure read failed, drain paused", logging.Err(err))
		return errStop
	}
	withMap := page
	withMap.Map = w.departedMap(ctx, page, active)
	// The code's part as a revision, nil when the map named none of them.
	rev := carriedPage(r, page, page.Statements, withMap.Map)
	settle := false
	switch {
	case len(departed) == 0:
		w.log.InfoContext(ctx, "baseline: departure: no repository is deactivated any more, no call")
	case !due:
		w.log.InfoContext(ctx, "baseline: departure: already taken out of the page, no call")
	case len(active) < 2:
		w.log.InfoContext(ctx, "baseline: departure: a project of one or none, no call")
		settle = true
	case r.Calls >= w.base.Limits.MaxCalls:
		w.log.WarnContext(ctx, "baseline: departure: the run's calls are spent, the map alone, still due")
	default:
		res := w.base.Describer.Describe(ctx, renderDeparture(*r, withMap, otherRepos(repos, ""), active, departed, w.limits))
		okCall, err := w.call(ctx, r, store.PurposeProject, res.Call)
		if errors.Is(err, errStop) {
			return err
		}
		switch {
		case err != nil:
			w.log.WarnContext(ctx, "baseline: departure: "+err.Error()+", the map alone, still due")
		case !okCall:
			w.log.WarnContext(ctx, "baseline: departure: the call failed: "+res.Outcome.String()+", the map alone, still due")
		case res.Description == nil:
			w.log.InfoContext(ctx, "baseline: departure: no rewrite asked")
			settle = true
		default:
			// The map is the code's: whatever came back, the trimmed one.
			d := *res.Description
			d.Map = withMap.Map
			if diagram.IsSentinel(d.Map) {
				d.Map = nil
			}
			// A departure judges no shape: the page's stays.
			next, _, refused, err := w.projectRevision(ctx, r, plan, withMap, &d, active, page.Shape)
			if err != nil {
				return err
			}
			if refused != "" {
				w.log.WarnContext(ctx, "baseline: departure: rewrite refused ("+refused+"), the map alone, still due")
				break
			}
			settle = true
			if next != nil {
				rev = w.keepProvenance(ctx, withMap, next)
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
		w.log.InfoContext(ctx, "baseline: departure: the project page changes nothing, no revision")
		if err := w.save(ctx, *r); err != nil {
			return err
		}
	} else {
		n, ok, err := w.base.Store.ApplyBaselineStep(ctx, *rev, *r)
		if err != nil {
			w.log.WarnContext(ctx, "baseline: departure revision write failed", logging.Err(err))
			return w.stepBusy(ctx, r, prev, "departure", err)
		}
		w.stepWritten()
		if ok {
			w.log.InfoContext(ctx, fmt.Sprintf("baseline: departure: project description revision %d (%d statements)", n, len(rev.Statements)))
		}
	}
	if settle {
		w.settle(ctx, r.ProjectKey, departed)
	}
	return nil
}

// settle records that a call with the departed repositories in its prompt
// answered. A failed write costs one more call by a later project step,
// never the step: logged.
func (w *Worker) settle(ctx context.Context, key string, departed []string) {
	if len(departed) == 0 {
		return
	}
	if err := w.base.Store.SettleDepartures(ctx, key, departed); err != nil {
		w.log.WarnContext(ctx, "baseline: departure not settled, a later project step takes it out again", logging.Err(err))
	}
}

// departedMap is the page's map with only the active repositories on it
// (carriedMap: the departed nodes out, a box only they talked to with
// them); as it is when there is none to take anything off, none for a
// project too large for one.
func (w *Worker) departedMap(ctx context.Context, page store.Page, active []string) *diagram.Diagram {
	switch {
	case page.Map == nil || diagram.IsSentinel(page.Map):
		return page.Map
	case diagram.MapFits(len(active)):
		return w.carriedMap(ctx, page, active)
	default:
		return nil
	}
}

// keepProvenance holds a departure rewrite to what it is: taking a
// repository out teaches nothing, so an edited statement keeps the sources
// it had (projectRevision credited the run, the departed repository's own
// commit), and a statement the call added is left out, logged.
func (w *Worker) keepProvenance(ctx context.Context, before store.Page, rev *store.NewRevision) *store.NewRevision {
	had := map[string]store.Statement{}
	for _, s := range before.Statements {
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

// departed is the project's deactivated repositories, by current slug.
func (w *Worker) departed(ctx context.Context, key string) ([]string, error) {
	departed, err := w.base.Store.DepartedRepos(ctx, key)
	if err != nil {
		w.log.WarnContext(ctx, "baseline: deactivated repositories read failed, drain paused", logging.Err(err))
		return nil, errStop
	}
	return departed, nil
}

// renderDeparture is the departure call's prompt: the project page with
// the code's map, the remaining repositories and the departed ones.
func renderDeparture(r store.BaselineRun, page store.Page, others string, active, departed []string, l config.Limits) string {
	return strings.NewReplacer(
		"{project}", r.ProjectKey, "{departed}", backticked(departed), "{repo_slugs}", backticked(active),
		"{project_description}", describeTaught(page), "{other_repos}", others,
		"{diagram_rules}", diagramRules(l), "{diagram_field}", diagramField(l),
	).Replace(prompts.ProjectDeparture)
}

// departedRule is the project prompt's rule for the deactivated
// repositories, "" when there are none. Every one, not only those still
// due: a repository's summary may still name one, and the rule keeps it
// from coming back onto the page.
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
