package consolidate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/trick77/almanach/internal/baseline"
	"github.com/trick77/almanach/internal/config"
	"github.com/trick77/almanach/internal/diagram"
	"github.com/trick77/almanach/internal/gitrepo"
	"github.com/trick77/almanach/internal/inference"
	"github.com/trick77/almanach/internal/lang"
	"github.com/trick77/almanach/internal/prompts"
	"github.com/trick77/almanach/internal/store"
	"github.com/trick77/ship/kit/logging"
)

// Baseline runs, in the same worker as the notes: a run's step is drained
// after the notes, one model call at most, so two writers of one page never
// exist. A run pins the clone's head when it starts; a repository's notes
// wait while its run is running (the store holds them), so no step reads a
// page holding facts newer than its commit.

// RunStore is what the runs need of the store beside what Store has.
type RunStore interface {
	NextRun(ctx context.Context) (store.BaselineRun, bool, error)
	SaveRun(ctx context.Context, r store.BaselineRun) (bool, error)
	ApplyBaselineStep(ctx context.Context, rev store.NewRevision, r store.BaselineRun) (int, bool, error)
	DocumentedRun(ctx context.Context, key, slug string) (store.BaselineRun, bool, error)
	SourceRepos(ctx context.Context, key string) ([]store.SourceRepo, error)
	IsSourceRepo(ctx context.Context, key, slug string) (bool, error)
	RepoSummaries(ctx context.Context, project string) ([]store.RepoSummary, error)
	RepoProblems(ctx context.Context, project, repo string, until time.Time) ([]store.RepoProblem, error)
}

// Code is the clones as a run reads them, each call under the poller's
// lock so a removal never deletes a clone mid-read.
type Code interface {
	Tree(ctx context.Context, key, slug, sha string) ([]gitrepo.Entry, error)
	Diff(ctx context.Context, key, slug, from, to string) ([]gitrepo.Entry, error)
	// Contains reports whether the clone has ancestor and it is an ancestor
	// of sha: the documented commit of a rewritten branch is not.
	Contains(ctx context.Context, key, slug, ancestor, sha string) (bool, error)
	CommitTime(ctx context.Context, key, slug, sha string) (time.Time, error)
	// ReadFiles reads files at sha, each within FileMaxBytes; a file that is
	// too large, missing or binary is left out, not an error.
	ReadFiles(ctx context.Context, key, slug, sha string, files []baseline.File) (map[string][]byte, error)
}

// Cataloguer is the catalogue model.
type Cataloguer interface {
	Catalogue(ctx context.Context, prompt string) inference.CatalogueResult
}

// Describer is the model that writes: the patch of a step, the project.
type Describer interface {
	Patch(ctx context.Context, prompt string) inference.PatchResult
	Describe(ctx context.Context, prompt string) inference.DescriptionResult
}

// Baseline is the runs' part of the worker; without it (the sources off)
// the worker folds notes only.
type Baseline struct {
	Store     RunStore
	Code      Code
	Catalogue Cataloguer
	Describer Describer
	Limits    config.Baseline
}

// runPlan is a run's plan as stored: the catalogue the next reconcile
// starts from, and what this run reads of it.
type runPlan struct {
	baseline.Plan
	// Overview: this run reads the docs first.
	Overview bool `json:"overview"`
	// Read is the subjects this run reads, by name, in order.
	Read []string `json:"read"`
	// Changed is a reconcile's changed and gone paths: the statements
	// resting on them are named in its prompts.
	Changed []string `json:"changed,omitempty"`
	// SummaryChanged: a step rewrote the repository's summary, so the
	// project step has something to fold.
	SummaryChanged bool `json:"summary_changed,omitempty"`
	// CommittedAt is the pinned commit's time, read once for every
	// baseline source the run attaches.
	CommittedAt time.Time `json:"committed_at"`
	// Unplaced is files no subject holds that the run could not place (no
	// call left, the call failed): the next reconcile places them, since
	// its diff will not name them again.
	Unplaced []string `json:"unplaced,omitempty"`
}

// steps is the reading steps of the plan: the overview, then the subjects.
func (p runPlan) steps() int {
	n := len(p.Read)
	if p.Overview {
		n++
	}
	return n
}

func decodePlan(raw json.RawMessage) (runPlan, error) {
	var p runPlan
	if len(raw) == 0 {
		return p, nil
	}
	err := json.Unmarshal(raw, &p)
	return p, err
}

// errStop ends a drain: a gateway hold or a store that does not answer.
var errStop = errors.New("stop the drain")

// stepRun runs one step of the run. Reports whether the drain may go on.
func (w *Worker) stepRun(ctx context.Context, r store.BaselineRun) bool {
	ctx = logging.With(ctx, "project", r.ProjectKey, "repo", r.Slug, "run_id", r.ID)
	var err error
	switch r.State {
	case store.RunQueued:
		err = w.startRun(ctx, &r)
	case store.RunCataloguing:
		err = w.catalogue(ctx, &r)
	case store.RunDescribing:
		err = w.describeStep(ctx, &r)
	case store.RunProject:
		err = w.projectStep(ctx, &r)
	default:
		err = fmt.Errorf("a run in state %q is not open", r.State)
	}
	if errors.Is(err, errStop) {
		return false
	}
	// The worker is stopping and cut the step: no failure of the run. Not
	// only a context error: a git child killed by the cancel answers
	// "signal: killed".
	if ctx.Err() != nil {
		return false
	}
	// The poller holds the clone (a long clone or fetch): the step is
	// taken again at the next wake or poll, the notes meanwhile go on.
	if errors.Is(err, ErrCloneBusy) {
		w.log.DebugContext(ctx, "baseline: clone busy, step deferred")
		return false
	}
	// Git could not place a merge commit: the same step again later, no
	// call spent, within placeWait (newerIDs).
	if errors.Is(err, errPlaceAgain) {
		return false
	}
	if err != nil {
		w.failRun(ctx, r, err.Error())
	}
	return w.saveFailed == nil
}

// ErrCloneBusy is a Code read that found the clone held by the poller: no
// failure, the step waits for the next drain.
var ErrCloneBusy = errors.New("the clone is busy")

// readErr wraps a failed read of the clone, keeping a busy clone apart.
func readErr(err error) error {
	if errors.Is(err, ErrCloneBusy) {
		return err
	}
	return fmt.Errorf("read the clone: %w", err)
}

// save writes the run's progress. A run that is gone (its repository
// removed) or over is no failure: the drain goes on to the next.
func (w *Worker) save(ctx context.Context, r store.BaselineRun) error {
	ok, err := w.base.Store.SaveRun(ctx, r)
	if err != nil {
		w.log.WarnContext(ctx, "baseline: run progress not saved, drain paused", logging.Err(err))
		w.saveFailed = err
		// A busy database is held off like a gateway: no next drain at
		// once, into the same wait.
		if store.IsBusy(err) {
			w.hold(ctx, holdBusy, 0)
		}
		return errStop
	}
	if !ok {
		w.log.InfoContext(ctx, "baseline: run gone (repository removed) or over, dropped")
	}
	return nil
}

// stepBusy handles a step's write (revision and progress, one transaction)
// that failed. A database busy past its timeout is temporary, like a
// gateway failure: the worker holds, the attempt is counted, and the step
// is taken again after the hold (nothing of it was written); past
// maxFoldAttempts in a row it fails the run, as it stood before the step
// (prev). Any other failure pauses the drain as before.
func (w *Worker) stepBusy(ctx context.Context, r *store.BaselineRun, prev store.BaselineRun, name string, err error) error {
	if !store.IsBusy(err) {
		return errStop
	}
	w.hold(ctx, holdBusy, 0)
	if w.busyRun != r.ID {
		w.busyRun, w.busyWrites = r.ID, 0
	}
	w.busyWrites++
	if w.busyWrites >= maxFoldAttempts {
		n := w.busyWrites
		w.busyRun, w.busyWrites = 0, 0
		*r = prev
		return fmt.Errorf("%s step given up after %d busy database writes: %w", name, n, err)
	}
	w.log.WarnContext(ctx, fmt.Sprintf("baseline: %s step not written: database busy, attempt %d of %d, kept for after the hold",
		name, w.busyWrites, maxFoldAttempts), logging.Err(err))
	return errStop
}

// stepWritten forgets the busy writes once a step's write got through.
func (w *Worker) stepWritten() { w.busyRun, w.busyWrites = 0, 0 }

// failRun ends a run with its reason on the row.
func (w *Worker) failRun(ctx context.Context, r store.BaselineRun, reason string) {
	now := w.now().UTC()
	r.State, r.Error, r.FinishedAt = store.RunFailed, reason, &now
	w.log.WarnContext(ctx, "baseline: run failed: "+reason)
	_ = w.save(ctx, r)
}

// startRun pins the clone's head and decides what the run reads. No call.
func (w *Worker) startRun(ctx context.Context, r *store.BaselineRun) error {
	repos, err := w.base.Store.SourceRepos(ctx, r.ProjectKey)
	if err != nil {
		w.log.WarnContext(ctx, "baseline: source repos read failed, drain paused", logging.Err(err))
		return errStop
	}
	head := ""
	for _, rp := range repos {
		if rp.Slug == r.Slug {
			head = rp.HeadCommit
		}
	}
	if head == "" {
		return errors.New("the clone has no head")
	}
	now := w.now().UTC()
	r.Commit, r.StartedAt, r.State, r.Error = head, &now, store.RunCataloguing, ""
	if r.Mode == store.RunReconcile {
		ok, err := w.base.Code.Contains(ctx, r.ProjectKey, r.Slug, r.BaseCommit, head)
		if err != nil {
			return readErr(err)
		}
		if !ok {
			w.log.InfoContext(ctx, "baseline: documented commit "+gitrepo.ShortSHA(r.BaseCommit)+" is not in the history any more, reading afresh")
			r.Mode = store.RunFull
		}
	}
	w.log.InfoContext(ctx, fmt.Sprintf("baseline: run started at %s (%s, %s)", gitrepo.ShortSHA(head), r.Mode, r.Trigger))
	return w.save(ctx, *r)
}

// call records one model call in the ledger and classes its failure. ok
// false with errStop is a gateway hold; ok false and nil is a failure of
// this call, which the step handles.
func (w *Worker) call(ctx context.Context, r *store.BaselineRun, purpose string, res inference.Call) (bool, error) {
	runID := r.ID
	if _, err := w.store.InsertLLMCall(ctx, store.LLMCall{
		ProjectKey: r.ProjectKey, RepoSlug: r.Slug, SourceKind: store.FromBaseline, SourceCommit: r.Commit, Purpose: purpose,
		BaselineRunID: &runID, Outcome: res.Outcome.String(), Model: res.Cost.Model, InputTokens: res.Cost.InputTokens,
		CachedTokens: res.Cost.CachedTokens, OutputTokens: res.Cost.OutputTokens, CostNanoUSD: res.Cost.NanoUSD, CallID: res.Cost.CallID,
	}); err != nil {
		w.log.WarnContext(ctx, "baseline: call row failed", logging.Err(err))
	}
	w.log.InfoContext(ctx, fmt.Sprintf("baseline: %s call %s (%s)", purpose, res.Outcome, res.Cost.LogLine()))
	if gateway, retryAfter := gatewayClass(res); gateway {
		w.hold(ctx, holdGateway, retryAfter)
		r.Attempts++
		if r.Attempts >= maxFoldAttempts {
			return false, fmt.Errorf("%s step given up after %d gateway failures: %s", purpose, r.Attempts, res.Outcome)
		}
		w.log.WarnContext(ctx, fmt.Sprintf("baseline: %s step not done: %s, attempt %d of %d, kept for after the hold",
			purpose, res.Outcome, r.Attempts, maxFoldAttempts), logging.Err(res.Err))
		if err := w.save(ctx, *r); err != nil {
			return false, err
		}
		return false, errStop
	}
	w.clearHold()
	r.Calls++
	r.Attempts = 0
	if res.Outcome != inference.OutcomeOK {
		w.log.WarnContext(ctx, fmt.Sprintf("baseline: %s step failed: %s", purpose, res.Outcome), logging.Err(res.Err))
		return false, nil
	}
	return true, nil
}

// maxSubjects is how many subjects a full run's calls can read: the cap
// less the catalogue, the overview and the project.
func (w *Worker) maxSubjects() int {
	return max(w.base.Limits.MaxCalls-3, 0)
}

// catalogue is a run's catalogue step: one call to the catalogue model for
// a full run, or for a reconcile with files no subject holds; none for a
// reconcile whose changes all fall in known subjects.
func (w *Worker) catalogue(ctx context.Context, r *store.BaselineRun) error {
	tree, err := w.base.Code.Tree(ctx, r.ProjectKey, r.Slug, r.Commit)
	if err != nil {
		return readErr(err)
	}
	files := baseline.Files(tree)
	// The commit's time before any paid call: a busy clone here defers the
	// step without throwing a call away.
	at, err := w.base.Code.CommitTime(ctx, r.ProjectKey, r.Slug, r.Commit)
	if err != nil {
		return readErr(err)
	}
	if r.Mode == store.RunReconcile {
		plan, next, err := w.reconcilePlan(ctx, r, files)
		if err != nil || !next {
			return err
		}
		plan.CommittedAt = at
		return w.planned(ctx, r, plan)
	}
	if r.Calls >= w.base.Limits.MaxCalls {
		return errors.New("no call left for the catalogue")
	}
	docs, size, err := w.readDocs(ctx, r, files, baseline.CatalogueDocsBytes)
	if err != nil {
		return err
	}
	prompt := renderCatalogue(*r, baseline.Listing(files), docs, "", w.maxSubjects())
	res := w.base.Catalogue.Catalogue(ctx, prompt)
	ok, err := w.call(ctx, r, store.PurposeCatalogue, res.Call)
	if err != nil {
		return err
	}
	r.Bytes += size
	if !ok {
		return fmt.Errorf("the catalogue failed: %s", res.Outcome)
	}
	p := baseline.BuildPlan(res.Catalogue.Subjects, files, w.maxSubjects())
	plan := runPlan{Plan: p, Overview: true, CommittedAt: at}
	for _, s := range p.Subjects {
		plan.Read = append(plan.Read, s.Name)
	}
	return w.planned(ctx, r, plan)
}

// reconcilePlan is a reconcile's catalogue: the documented run's plan, the
// changes since its commit mapped onto it, new files placed by a catalogue
// call when there are any. next false: the step ended otherwise (a hold, a
// full read instead, a call that failed).
func (w *Worker) reconcilePlan(ctx context.Context, r *store.BaselineRun, files []baseline.File) (runPlan, bool, error) {
	doc, ok, err := w.base.Store.DocumentedRun(ctx, r.ProjectKey, r.Slug)
	if err != nil {
		w.log.WarnContext(ctx, "baseline: documented run read failed, drain paused", logging.Err(err))
		return runPlan{}, false, errStop
	}
	var old runPlan
	if ok {
		old, err = decodePlan(doc.Plan)
	}
	diff, derr := w.base.Code.Diff(ctx, r.ProjectKey, r.Slug, r.BaseCommit, r.Commit)
	if errors.Is(derr, ErrCloneBusy) {
		return runPlan{}, false, derr
	}
	changes := baseline.Sift(diff)
	if !ok || err != nil || derr != nil || baseline.FullInstead(old.Plan, changes) {
		w.log.InfoContext(ctx, "baseline: reconcile reads the repository afresh")
		r.Mode = store.RunFull
		return runPlan{}, false, w.save(ctx, *r)
	}
	_, unplaced := baseline.Touched(old.Plan, changes)
	merged := baseline.MergePlan(old.Plan, nil, files, w.maxSubjects())
	// A file that grew out of its subject's budget left the plan in the
	// merge, and a file the last run could not place is in none: both are
	// placed like new ones, not lost until they change again.
	unplaced = append(unplaced, evicted(old.Plan, merged, files)...)
	unplaced = append(unplaced, stillReadable(old.Unplaced, files, unplaced)...)
	plan := runPlan{}
	if len(unplaced) > 0 && r.Calls < w.base.Limits.MaxCalls {
		placing := "The repository is already catalogued into the subjects below; the files in the listing are new or changed and in none of them. " +
			"Place each: name an existing subject to add it there, or a new subject. Existing subjects:\n\n" + subjectList(old.Plan)
		res := w.base.Catalogue.Catalogue(ctx, renderCatalogue(*r, baseline.Listing(unplaced), "_None._", placing, w.maxSubjects()))
		ok, err := w.call(ctx, r, store.PurposeCatalogue, res.Call)
		if err != nil {
			return runPlan{}, false, err
		}
		if ok {
			merged = baseline.MergePlan(old.Plan, res.Catalogue.Subjects, files, len(old.Subjects)+w.maxSubjects())
		}
	}
	// What no subject holds after the placing (no call left, the call
	// failed, the model left a file out, a subject had no room) stays on
	// the plan for the next reconcile; what it placed is read now, like a
	// change, whether or not the diff named it.
	held := map[string]bool{}
	for _, s := range merged.Subjects {
		for _, f := range s.Files {
			held[f.Path] = true
		}
	}
	reading := changes
	for _, f := range unplaced {
		if held[f.Path] {
			reading.Changed = append(reading.Changed, f)
		} else {
			plan.Unplaced = append(plan.Unplaced, f.Path)
		}
	}
	// Touched over the merged plan with the last run's skipped names, the
	// new plan starting with none of its own.
	touched, _ := baseline.Touched(baseline.Plan{Subjects: merged.Subjects, Skipped: old.Skipped}, reading)
	merged.Skipped = nil
	plan.Plan, plan.Read = merged, touched
	changed := changes.Paths()
	for p := range changed {
		plan.Changed = append(plan.Changed, p)
	}
	slices.Sort(plan.Changed)
	// The overview reads the docs again when one changed or the last run's
	// failed; the project step folds again when the last run's did not.
	plan.Overview = slices.Contains(old.Skipped, overviewName)
	plan.SummaryChanged = slices.Contains(old.Skipped, projectName)
	for _, d := range baseline.Docs(files, baseline.DocsMaxBytes) {
		if changed[d.Path] {
			plan.Overview = true
		}
	}
	return plan, true, nil
}

// stillReadable is the paths of the last run's unplaced files that the tree
// still has readable and that are not placed already.
func stillReadable(paths []string, files, already []baseline.File) []baseline.File {
	have := map[string]bool{}
	for _, f := range already {
		have[f.Path] = true
	}
	byPath := map[string]baseline.File{}
	for _, f := range files {
		byPath[f.Path] = f
	}
	var out []baseline.File
	for _, p := range paths {
		if f, ok := byPath[p]; ok && !have[p] {
			have[p] = true
			out = append(out, f)
		}
	}
	return out
}

// planned stores the plan and moves the run on to its reading steps, or
// straight to its end when there is nothing to read. The pinned commit's
// time is read here, once.
func (w *Worker) planned(ctx context.Context, r *store.BaselineRun, plan runPlan) error {
	raw, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	r.Plan, r.Step, r.Steps = raw, 0, plan.steps()+1
	if plan.steps() == 0 {
		// Nothing to read again. A reconcile whose summary stands ends
		// here, in one write; otherwise the project step still runs, and
		// folds when the last run skipped it.
		w.log.InfoContext(ctx, "baseline: nothing to read again")
		if r.Mode == store.RunReconcile && !plan.SummaryChanged {
			page, _, err := w.store.CurrentPage(ctx, store.ScopeProject, r.ProjectKey, "")
			if err != nil {
				w.log.WarnContext(ctx, "baseline: project page read failed, drain paused", logging.Err(err))
				return errStop
			}
			skip, _, err := w.projectStands(ctx, r, plan, page)
			if err != nil {
				return err
			}
			if skip {
				return w.finish(ctx, r, plan)
			}
		}
		r.State = store.RunProject
		return w.save(ctx, *r)
	}

	r.State = store.RunDescribing
	w.log.InfoContext(ctx, fmt.Sprintf("baseline: %d subjects catalogued, %d to read, overview %v", len(plan.Subjects), len(plan.Read), plan.Overview))
	return w.save(ctx, *r)
}

// readDocs reads the docs and manifests and renders them for a prompt.
func (w *Worker) readDocs(ctx context.Context, r *store.BaselineRun, files []baseline.File, limit int64) (string, int64, error) {
	docs := baseline.Docs(files, limit)
	bodies, err := w.base.Code.ReadFiles(ctx, r.ProjectKey, r.Slug, r.Commit, docs)
	if err != nil {
		return "", 0, readErr(err)
	}
	var size int64
	for _, b := range bodies {
		size += int64(len(b))
	}
	return renderFiles(docs, bodies), size, nil
}

// describeStep reads one part, the overview or a subject, and folds its
// patch into the repository page. A failure of the call or a patch the
// rules or the ceilings refuse skips the part; a page already at a ceiling
// ends the reading before any call.
func (w *Worker) describeStep(ctx context.Context, r *store.BaselineRun) error {
	plan, err := decodePlan(r.Plan)
	if err != nil {
		return fmt.Errorf("the run's plan: %w", err)
	}
	i := r.Step
	if i >= plan.steps() {
		r.State = store.RunProject
		return w.save(ctx, *r)
	}
	tree, err := w.base.Code.Tree(ctx, r.ProjectKey, r.Slug, r.Commit)
	if err != nil {
		return readErr(err)
	}
	files := baseline.Files(tree)
	name := partName(plan, i)
	var part, purpose string
	var read []baseline.File
	subject := name != overviewName
	if !subject {
		part, purpose, read = overviewPart, store.PurposeOverview, baseline.Docs(files, baseline.DocsMaxBytes)
	} else {
		s, ok := subjectNamed(plan.Plan, name)
		if !ok {
			return w.skipPart(ctx, r, &plan, name, "no longer in the plan")
		}
		part, purpose, read = fmt.Sprintf("the subject %q: %s", s.Name, s.Purpose), store.PurposeSubject, s.Files
	}
	if r.Calls >= w.base.Limits.MaxCalls {
		return w.endReading(ctx, r, &plan, "the run's calls are spent")
	}
	var size int64
	for _, f := range read {
		size += f.Size
	}
	if r.Bytes+size > int64(w.base.Limits.MaxBytes) {
		return w.skipPart(ctx, r, &plan, name, "over the run's byte budget")
	}
	page, _, err := w.store.CurrentPage(ctx, store.ScopeRepository, r.ProjectKey, r.Slug)
	if err != nil {
		w.log.WarnContext(ctx, "baseline: repository page read failed, drain paused", logging.Err(err))
		return errStop
	}
	// A page already at a ceiling, a lowered one included, gets no call:
	// a part could pay for an answer the ceiling then refuses, every part
	// of every reconcile.
	if prose(page.Summary, page.Statements) >= w.limits.RepoMaxChars || len(page.Statements) >= w.limits.RepoMaxStatements {
		return w.endReading(ctx, r, &plan, "the page is at its ceiling")
	}
	// Only a step that will make a call asks; a held one has read no file.
	newer, err := w.newerIDs(ctx, r, page.Statements, plan.CommittedAt)
	if err != nil {
		return readErr(err)
	}
	if name == overviewName {
		newer = codeBacked(page.Statements, newer)
	}
	bodies, err := w.base.Code.ReadFiles(ctx, r.ProjectKey, r.Slug, r.Commit, read)
	if err != nil {
		return readErr(err)
	}
	// The evidence a pitfall needs: the problem areas the subject's files
	// sit in, from fixes merged by the pinned commit's time (newerSlack
	// after it: the head's own PR is recorded a moment later). Read once a
	// run. The overview reads docs and gets none.
	var problems []store.RepoProblem
	if subject {
		if w.problemsRun != r.ID || w.problems == nil {
			// A plan from before the time was kept has none: no cutoff.
			until := plan.CommittedAt.Add(newerSlack)
			if plan.CommittedAt.IsZero() {
				until = w.now()
			}
			all, err := w.base.Store.RepoProblems(ctx, r.ProjectKey, r.Slug, until)
			if err != nil {
				w.log.WarnContext(ctx, "baseline: problem areas read failed, drain paused", logging.Err(err))
				return errStop
			}
			w.problemsRun, w.problems = r.ID, append([]store.RepoProblem{}, all...)
		}
		problems = problemsFor(w.problems, read)
	}
	focus := w.focus(plan, page, newer)
	left := plan.steps() - i - 1
	b := stepBudget(w.limits, page, left, !subject)
	prompt := renderDescribe(*r, page, w.limits, i+1, r.Steps, left, b, part, focus, renderFiles(read, bodies), renderProblems(problems), newer)
	res := w.base.Describer.Patch(ctx, prompt)
	ok, err := w.call(ctx, r, purpose, res.Call)
	if err != nil {
		return err
	}
	r.Bytes += size
	if !ok {
		return w.skipPart(ctx, r, &plan, name, "the call failed: "+res.Outcome.String())
	}
	var moved int
	res.Patch.Statements, res.Patch.Added, moved = asNew(page.Statements, res.Patch.Statements, res.Patch.Added)
	logAsNew(ctx, w.log, "baseline: "+name, moved)
	// A step shown no problem area had no evidence for a new pitfall.
	if len(problems) == 0 {
		res.Patch.Added = unbackedPitfalls(ctx, w.log, "baseline: "+name, res.Patch.Added)
	}
	return w.foldPatch(ctx, r, &plan, name, page, res.Patch, baseline.Exists(tree), newer, b)
}

// foldPatch applies a step's patch to the page under the rules of a fold
// and writes the revision with the run's progress in one transaction.
func (w *Worker) foldPatch(ctx context.Context, r *store.BaselineRun, plan *runPlan, name string, page store.Page, patch inference.Patch, exists func(string) bool, newer map[string]string, b budget) error {
	// A code-backed statement the overview names is struck from its patch,
	// not the patch refused: nearly every statement rests on code, and a
	// refusal would throw away the summary over a confirmation.
	patch, leftAlone := strikeCode(patch, newer)
	if len(leftAlone) > 0 {
		w.log.InfoContext(ctx, "baseline: "+name+": left alone, resting on code: "+strings.Join(leftAlone, ", "))
	}
	if id := touchesNewer(newer, patch); id != "" {
		why := " was taught by a pull request newer than the commit read"
		if newer[id] == byUnplaced {
			why = " rests on a merge commit git could not place"
		}
		return w.skipPart(ctx, r, plan, name, "patch refused: "+id+why)
	}
	applied, reason := baseline.ApplyPatch(page.Statements, patch, exists)
	if reason != "" {
		return w.skipPart(ctx, r, plan, name, "patch refused: "+reason)
	}
	if len(applied.Unknown) > 0 {
		w.log.WarnContext(ctx, "baseline: "+name+": unknown ids left out", slog.Any("ids", applied.Unknown))
	}
	// A path the model names for a statement it wrote is held to the tree,
	// forgiven its spelling: a path the tree does not have is invented.
	for i := range applied.Statements {
		if applied.Checked[i] {
			applied.Statements[i].Files = inTree(applied.Statements[i].Files, exists)
		}
	}
	next, err := w.store.NextStatementID(ctx, store.ScopeRepository, r.ProjectKey, r.Slug)
	if err != nil {
		w.log.WarnContext(ctx, "baseline: statement id read failed, drain paused", logging.Err(err))
		return errStop
	}
	statements, leftOut, reason := attach(store.ScopeRepository, page.Statements, applied.Statements, runSource(*r, *plan), next, limitsOf(w.limits))
	if reason != "" {
		return w.skipPart(ctx, r, plan, name, "patch refused: "+reason)
	}
	summary := page.Summary
	if applied.Summary != nil {
		summary = lang.SwissSS(strings.TrimSpace(*applied.Summary))
	}
	for i := range statements {
		if applied.Checked[i] {
			statements[i].Checked = r.Commit
		}
	}
	// A patch that takes the page over a ceiling from free room is rejected
	// whole and the page stays; only this part is skipped. A page already
	// at its ceiling never gets this far: describeStep ends the reading.
	chars, capReason := check(summary, statements, w.limits.RepoMaxStatements, w.limits.RepoMaxChars)
	if capReason != "" {
		return w.skipPart(ctx, r, plan, name, "rewrite rejected, page unchanged: "+capReason)
	}
	// Measured on the fold's result, never enforced: the ceilings reject a
	// rewrite whole, and a patch is never cut.
	grown, added := growth(page, summary, statements)
	w.log.InfoContext(ctx, fmt.Sprintf("baseline: %s: page grew by %d chars of prose, budget %d; %s, budget %d; %d returned unchanged",
		name, grown, b.chars, plural(added, "new statement"), b.statements, returnedUnchanged(page.Statements, patch)))
	logLeftOut(ctx, w.log, "baseline: "+name, leftOut)
	if summary != page.Summary {
		plan.SummaryChanged = true
	}
	prev := *r
	r.Step++
	if err := encodeInto(r, *plan); err != nil {
		return err
	}
	if summary == page.Summary && sameStatements(statements, page.Statements) && sameChecks(statements, page.Statements) {
		w.log.InfoContext(ctx, "baseline: "+name+": page identical, no revision")
		return w.save(ctx, *r)
	}
	runID := r.ID
	n, ok, err := w.base.Store.ApplyBaselineStep(ctx, store.NewRevision{
		Scope: store.ScopeRepository, ProjectKey: r.ProjectKey, RepoSlug: r.Slug, Summary: summary, Statements: statements,
		Chars: chars, SourceKind: store.FromBaseline, SourceCommit: r.Commit, BaselineRunID: &runID,
	}, *r)
	if err != nil {
		w.log.WarnContext(ctx, "baseline: revision write failed, drain paused", logging.Err(err))
		return w.stepBusy(ctx, r, prev, name, err)
	}
	w.stepWritten()
	if ok {
		w.log.InfoContext(ctx, fmt.Sprintf("baseline: %s: repository description revision %d (%d statements, %d chars)", name, n, len(statements), chars))
	}
	return nil
}

// newerSlack is how much later than the read commit a pull request git
// cannot place may have been merged and still count as in it: the PR whose
// merge commit is the head is recorded as merged a moment after the commit
// is made.
const newerSlack = 2 * time.Minute

// notHeldMargin is how long before the read commit a merge commit git does
// not find in it must have been merged to count as off the branch rather
// than newer: wide enough for a committer clock a day off, short enough
// that a merge commit that never reaches the branch soon stops counting.
const notHeldMargin = 24 * time.Hour

// placeWait is how long a run holds its step over a merge commit whose git
// read fails, counted from that commit's first failure in the run, before
// its statements go read-only: by the clock, not by drains, so a burst of
// webhook wakes cannot use it up.
const placeWait = 2 * time.Minute

// Why a step leaves a statement alone: a newer PR taught it, git could not
// place the merge commit of the PR that did, or it rests on code the
// overview does not read.
const (
	byNewerPR  = "newer"
	byUnplaced = "unplaced"
	byCode     = "code"
)

// codeBacked adds to newer the statements resting on code alone, none of
// their files a doc: the overview reads docs alone, and code wins over
// them. One that names a doc beside code is the overview's to keep up.
func codeBacked(statements []store.Statement, newer map[string]string) map[string]string {
	for _, s := range statements {
		if newer[s.ID] != "" || len(s.Files) == 0 || slices.ContainsFunc(s.Files, baseline.IsDoc) {
			continue
		}
		if newer == nil {
			newer = map[string]string{}
		}
		newer[s.ID] = byCode
	}
	return newer
}

// strikeCode takes the code-backed ids out of a patch, edits, drops and
// confirmations alike, and names them.
func strikeCode(p inference.Patch, newer map[string]string) (inference.Patch, []string) {
	var struck []string
	held := func(id string) bool {
		if newer[id] == byCode {
			struck = append(struck, id)
			return true
		}
		return false
	}
	p.Statements = slices.DeleteFunc(slices.Clone(p.Statements), func(s inference.Statement) bool { return s.ID != nil && held(*s.ID) })
	p.Drop = slices.DeleteFunc(slices.Clone(p.Drop), held)
	p.Confirmed = slices.DeleteFunc(slices.Clone(p.Confirmed), held)
	return p, struck
}

// errPlaceAgain is a merge commit git could not place: the step is taken
// again at the next drain, before its files are read or a call made.
var errPlaceAgain = errors.New("merge commit not placed yet")

// ancestry is what git said of a PR's merge commit against a run's commit.
type ancestry int8

const (
	// unplaced: no full commit name; the merge time decides.
	unplaced ancestry = iota
	held
	notHeld
	// unknown: the read failed; asked once a step, never kept for the run.
	unknown
)

// newerIDs is the statements a pull request taught that the commit a run
// reads does not hold: the run pins the clone's last synced head, and a
// note merged since may have been folded before the run started. Such a
// statement describes code the run cannot see.
//
// A merge commit the read commit holds is in it, whatever the clocks say (a
// rebase merge's head is older than the PR's merge time). One it does not
// hold is newer unless merged well before the read commit (notHeldMargin):
// one that never reaches the branch (a release branch, a rewritten history)
// soon stops counting. A source without a merge commit goes by the clock,
// with newerSlack. Git's answers are kept for the run.
//
// A statement that only a failed read leaves open holds the step for up to
// placeWait from its commit's first failure; past that it is read-only for
// the step, and git is asked again in the next. One another source already
// marks newer holds nothing. The answer is each id left alone, and why.
func (w *Worker) newerIDs(ctx context.Context, r *store.BaselineRun, statements []store.Statement, at time.Time) (map[string]string, error) {
	if w.ancestryRun != r.ID || w.ancestry == nil {
		w.ancestryRun, w.ancestry, w.placeSince = r.ID, map[string]ancestry{}, map[string]time.Time{}
	}
	now := w.now()
	newer := map[string]string{}
	failed := map[string]bool{}
	hold, unsure := false, 0
	for _, s := range statements {
		var open []string
		for _, src := range s.Sources {
			if src.IsBaseline() {
				continue
			}
			a, err := w.place(ctx, r, src.Commit, failed)
			if err != nil {
				return nil, err
			}
			if a == unknown {
				open = append(open, src.Commit)
			} else if newerSource(src, a, at) {
				newer[s.ID] = byNewerPR
				break
			}
		}
		if len(open) == 0 || newer[s.ID] != "" {
			continue
		}
		newer[s.ID] = byUnplaced
		unsure++
		for _, c := range open {
			if now.Sub(w.placeSince[c]) < placeWait {
				hold = true
			}
		}
	}
	if hold {
		return nil, errPlaceAgain
	}
	if unsure > 0 {
		w.log.WarnContext(ctx, fmt.Sprintf("baseline: %d statements read-only this step, git could not place their merge commits", unsure))
	}
	return newer, nil
}

// place asks git whether the run's commit holds a merge commit: once per
// run for an answer, once per step (failed) for a read that fails, whose
// first failure in the run it keeps. A busy clone is the step's to wait
// out.
func (w *Worker) place(ctx context.Context, r *store.BaselineRun, commit string, failed map[string]bool) (ancestry, error) {
	if commit == "" {
		return unplaced, nil
	}
	if a, ok := w.ancestry[commit]; ok {
		return a, nil
	}
	if failed[commit] {
		return unknown, nil
	}
	in, err := w.base.Code.Contains(ctx, r.ProjectKey, r.Slug, commit, r.Commit)
	a := notHeld
	switch {
	case ctx.Err() != nil:
		return unknown, ctx.Err()
	case err == nil && in:
		a = held
	case err == nil:
	case errors.Is(err, ErrCloneBusy):
		return unknown, err
	case errors.Is(err, gitrepo.ErrBadSHA):
		// Not a commit name (noergler's to send): the clock decides.
		a = unplaced
	default:
		failed[commit] = true
		if _, seen := w.placeSince[commit]; !seen {
			w.placeSince[commit] = w.now()
			w.log.WarnContext(ctx, "baseline: merge commit not placed, step held",
				"commit", gitrepo.ShortSHA(commit), logging.Err(err))
		}
		return unknown, nil
	}
	w.ancestry[commit] = a
	return a, nil
}

func newerSource(src store.Source, a ancestry, at time.Time) bool {
	switch a {
	case held:
		return false
	case notHeld:
		// A read commit with no time (a plan from before it was kept):
		// git's answer stands alone.
		return at.IsZero() || !src.MergedAt.Before(at.Add(-notHeldMargin))
	}
	return !at.IsZero() && src.MergedAt.After(at.Add(newerSlack))
}

// touchesNewer is the first id a patch edits, drops or confirms that a
// newer pull request taught; "" when it touches none.
func touchesNewer(newer map[string]string, p inference.Patch) string {
	if len(newer) == 0 {
		return ""
	}
	for _, st := range p.Statements {
		if st.ID != nil && newer[*st.ID] != "" {
			return *st.ID
		}
	}
	for _, ids := range [][]string{p.Drop, p.Confirmed} {
		for _, id := range ids {
			if newer[id] != "" {
				return id
			}
		}
	}
	return ""
}

// inTree keeps the files the tree has.
func inTree(files []string, exists func(string) bool) []string {
	out := []string{}
	for _, f := range files {
		if exists(f) {
			out = append(out, f)
		}
	}
	return out
}

// skipPart counts a part the run could not fold and moves on; the next
// reconcile reads it again.
func (w *Worker) skipPart(ctx context.Context, r *store.BaselineRun, plan *runPlan, name, why string) error {
	w.log.WarnContext(ctx, "baseline: "+name+" skipped: "+why)
	r.Skipped++
	if !slices.Contains(plan.Skipped, name) {
		plan.Skipped = append(plan.Skipped, name)
	}
	r.Step++
	if err := encodeInto(r, *plan); err != nil {
		return err
	}
	return w.save(ctx, *r)
}

// endReading stops the reading steps: every part not read yet is skipped,
// and the run goes on to its project step.
func (w *Worker) endReading(ctx context.Context, r *store.BaselineRun, plan *runPlan, why string) error {
	w.log.WarnContext(ctx, "baseline: reading ends: "+why)
	for i := r.Step; i < plan.steps(); i++ {
		r.Skipped++
		if name := partName(*plan, i); !slices.Contains(plan.Skipped, name) {
			plan.Skipped = append(plan.Skipped, name)
		}
	}
	r.Step, r.State = plan.steps(), store.RunProject
	if err := encodeInto(r, *plan); err != nil {
		return err
	}
	return w.save(ctx, *r)
}

// The overview and the project step by name in the plan's skipped list:
// the next reconcile reads the docs again, or folds the project again,
// when the last run could not.
const (
	overviewName = "the overview"
	projectName  = "the project"
)

func partName(plan runPlan, i int) string {
	if plan.Overview {
		if i == 0 {
			return overviewName
		}
		i--
	}
	if i < len(plan.Read) {
		return plan.Read[i]
	}
	return ""
}

// evicted is the readable files the old plan held and the merged one no
// longer does.
func evicted(old, merged baseline.Plan, files []baseline.File) []baseline.File {
	held := map[string]bool{}
	for _, s := range merged.Subjects {
		for _, f := range s.Files {
			held[f.Path] = true
		}
	}
	readable := map[string]baseline.File{}
	for _, f := range files {
		readable[f.Path] = f
	}
	var out []baseline.File
	for _, s := range old.Subjects {
		for _, f := range s.Files {
			if now, ok := readable[f.Path]; ok && !held[f.Path] {
				out = append(out, now)
				held[f.Path] = true
			}
		}
	}
	return out
}

func subjectNamed(p baseline.Plan, name string) (baseline.Subject, bool) {
	for _, s := range p.Subjects {
		if s.Name == name {
			return s, true
		}
	}
	return baseline.Subject{}, false
}

func encodeInto(r *store.BaselineRun, plan runPlan) error {
	raw, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	r.Plan = raw
	return nil
}

// overviewPart is the overview step's part; the prompt's code-wins rule
// says what the overview may not do with it.
const overviewPart = "the repository's documentation, manifests and diagram sources, for the overview"

// focus is what a reconcile's step must look at besides its files: the
// statements resting on paths that changed, the paths that are gone.
func (w *Worker) focus(plan runPlan, page store.Page, newer map[string]string) string {
	if len(plan.Changed) == 0 {
		return "This is a fresh reading: document what this part's code shows."
	}
	changed := map[string]bool{}
	for _, p := range plan.Changed {
		changed[p] = true
	}
	// A statement the step must leave alone is no statement to check.
	ids := slices.DeleteFunc(baseline.Resting(page.Statements, changed), func(id string) bool { return newer[id] != "" })
	var b strings.Builder
	b.WriteString("This reading follows earlier ones: the code changed since the last. Check what the description says against the files below.")
	if len(ids) > 0 {
		fmt.Fprintf(&b, " These statements rest on files that changed and must be checked first: %s.", strings.Join(ids, ", "))
	}
	return b.String()
}

// projectStep folds the repository's new description into the project
// page: on a full run always, on a reconcile only when the summary
// changed. A failure of the call ends the run without it.
func (w *Worker) projectStep(ctx context.Context, r *store.BaselineRun) error {
	plan, err := decodePlan(r.Plan)
	if err != nil {
		return fmt.Errorf("the run's plan: %w", err)
	}
	projectPage, _, err := w.store.CurrentPage(ctx, store.ScopeProject, r.ProjectKey, "")
	if err != nil {
		w.log.WarnContext(ctx, "baseline: project page read failed, drain paused", logging.Err(err))
		return errStop
	}
	skip, repos, err := w.projectStands(ctx, r, plan, projectPage)
	if err != nil {
		return err
	}
	if skip {
		return w.finish(ctx, r, plan)
	}
	if r.Calls >= w.base.Limits.MaxCalls {
		return w.skipProject(ctx, r, plan, "the run's calls are spent")
	}
	repoPage, ok, err := w.store.CurrentPage(ctx, store.ScopeRepository, r.ProjectKey, r.Slug)
	if err != nil {
		w.log.WarnContext(ctx, "baseline: repository page read failed, drain paused", logging.Err(err))
		return errStop
	}
	if !ok {
		w.log.InfoContext(ctx, "baseline: no repository description to fold into the project")
		return w.finish(ctx, r, plan)
	}
	if repos == nil {
		if repos, err = w.repoSummaries(ctx, r); err != nil {
			return err
		}
	}
	res := w.base.Describer.Describe(ctx, renderProject(*r, repoPage, projectPage, otherRepos(repos, r.Slug), slugs(repos), w.limits))
	okCall, err := w.call(ctx, r, store.PurposeProject, res.Call)
	if err != nil {
		return err
	}
	if !okCall {
		return w.skipProject(ctx, r, plan, "the call failed: "+res.Outcome.String())
	}
	var rev *store.NewRevision
	if res.Description == nil {
		// No change carries the text and the map forward; a project with no
		// page yet has nothing to carry, and is asked again next time.
		if projectPage.RevisionID == 0 {
			return w.skipProject(ctx, r, plan, "no change asked of a project without a page")
		}
		rev = w.carriedRevision(ctx, r, projectPage, slugs(repos))
	} else {
		var refused string
		rev, refused, err = w.projectRevision(ctx, r, plan, projectPage, res.Description, slugs(repos))
		if err != nil {
			return err
		}
		if refused != "" {
			return w.skipProject(ctx, r, plan, "rewrite refused: "+refused)
		}
	}
	if rev == nil {
		return w.finish(ctx, r, plan)
	}
	// The project revision and the run's end in one transaction: a crash
	// between them would pay for the project call again and write it twice.
	prev := *r
	if err := w.markDone(ctx, r, plan); err != nil {
		return err
	}
	n, ok, err := w.base.Store.ApplyBaselineStep(ctx, *rev, *r)
	if err != nil {
		w.log.WarnContext(ctx, "baseline: project revision write failed, drain paused", logging.Err(err))
		return w.stepBusy(ctx, r, prev, "project", err)
	}
	w.stepWritten()
	if ok {
		w.log.InfoContext(ctx, fmt.Sprintf("baseline: project description revision %d (%d statements, %d chars)", n, len(rev.Statements), rev.Chars))
	}
	return nil
}

// projectStands reports whether a run may end without its project step: a
// reconcile whose summary stands, of a project that has its map or is too
// large for one. A project without one yet (described before maps
// existed) is folded as if the summary had changed, so its map gets drawn.
// A map is due only while the page's map is NULL (never asked for): the
// sentinel says a step asked and none was accepted, and only a run that
// folds the project anyway asks again. page is the project page the caller
// read; the repositories, when it had to read them, come back for reuse.
func (w *Worker) projectStands(ctx context.Context, r *store.BaselineRun, plan runPlan, page store.Page) (bool, []store.RepoSummary, error) {
	if r.Mode != store.RunReconcile || plan.SummaryChanged {
		return false, nil, nil
	}
	if page.Map != nil {
		return true, nil, nil
	}
	repos, err := w.repoSummaries(ctx, r)
	if err != nil {
		return false, nil, err
	}
	return !diagram.MapFits(len(repos)), repos, nil
}

// repoSummaries is the project's source repositories with their summaries.
func (w *Worker) repoSummaries(ctx context.Context, r *store.BaselineRun) ([]store.RepoSummary, error) {
	repos, err := w.base.Store.RepoSummaries(ctx, r.ProjectKey)
	if err != nil {
		w.log.WarnContext(ctx, "baseline: project repositories read failed, drain paused", logging.Err(err))
		return nil, errStop
	}
	return repos, nil
}

// carriedRevision is what a "no change" answer still writes: the page's
// text with its map carried on (departed repositories out, the sentinel
// where a map was due and there is none); nil when that is the page as it
// stands.
func (w *Worker) carriedRevision(ctx context.Context, r *store.BaselineRun, page store.Page, slugs []string) *store.NewRevision {
	var m *diagram.Diagram
	if diagram.MapFits(len(slugs)) {
		m = w.carriedMap(ctx, page, slugs)
	}
	if diagram.Equal(m, page.Map) {
		return nil
	}
	runID := r.ID
	return &store.NewRevision{
		Scope: store.ScopeProject, ProjectKey: r.ProjectKey, Summary: page.Summary, Statements: page.Statements,
		Chars: prose(page.Summary, page.Statements), SourceKind: store.FromBaseline, SourceCommit: r.Commit, BaselineRunID: &runID, Map: m,
	}
}

// projectRevision is the project description a run's last step returned,
// held to the same rules as a note's fold of it; nil when it is refused or
// identical, the map included. Its statements are a patch, as a subject
// step's: edits by id, a statement not named kept, removals in drop, so an
// answer that names none no longer empties the page (an eval's second
// repository wiped the first one's eleven so). The project has no one tree,
// so no file decides a drop: a PR-taught statement is never dropped here
// (a note's fold, which carries what the PR taught, may rewrite it), its
// drop left out and logged; an edit may change any statement's files.
// When only the new statements take the page over a ceiling, they are left
// out and the rest stands, so a full page still gets its summary, map,
// edits and drops. The map never sinks the description: see revisionMap.
// slugs are the project's source repositories.
func (w *Worker) projectRevision(ctx context.Context, r *store.BaselineRun, plan runPlan, page store.Page, d *inference.Description, slugs []string) (*store.NewRevision, string, error) {
	next, err := w.store.NextStatementID(ctx, store.ScopeProject, r.ProjectKey, "")
	if err != nil {
		w.log.WarnContext(ctx, "baseline: statement id read failed, drain paused", logging.Err(err))
		return nil, "", errStop
	}
	answer := *d
	var moved int
	answer.Statements, answer.Added, moved = asNew(page.Statements, d.Statements, d.Added)
	logAsNew(ctx, w.log, "baseline: "+projectName, moved)
	d = &answer
	taught := map[string]bool{}
	for _, s := range page.Statements {
		taught[s.ID] = baseline.FromPR(s)
	}
	var drop, kept, unknown []string
	for _, id := range d.Drop {
		pr, on := taught[id]
		switch {
		case !on:
			unknown = append(unknown, id)
		case pr:
			kept = append(kept, id)
		default:
			drop = append(drop, id)
		}
	}
	if len(unknown) > 0 {
		w.log.WarnContext(ctx, "baseline: "+projectName+": unknown ids left out", slog.Any("ids", unknown))
	}
	if len(kept) > 0 {
		w.log.WarnContext(ctx, "baseline: "+projectName+": drop of statements a pull request taught left out", slog.Any("ids", kept))
	}
	summary := lang.SwissSS(strings.TrimSpace(d.Summary))
	fold := func(added []inference.Statement) ([]store.Statement, diagramsLeftOut, int, string, string) {
		applied, reason := baseline.ApplyPatch(page.Statements, inference.Patch{Statements: d.Statements, Added: added, Drop: drop}, noTree)
		if reason != "" {
			return nil, diagramsLeftOut{}, 0, reason, ""
		}
		statements, leftOut, reason := attach(store.ScopeProject, page.Statements, applied.Statements, runSource(*r, plan), next, limitsOf(w.limits))
		if reason != "" {
			return nil, diagramsLeftOut{}, 0, reason, ""
		}
		chars, capReason := check(summary, statements, w.limits.ProjectMaxStatements, w.limits.ProjectMaxChars)
		return statements, leftOut, chars, "", capReason
	}
	statements, leftOut, chars, reason, capReason := fold(d.Added)
	if reason == "" && capReason != "" && len(d.Added) > 0 {
		if s, l, c, re, ca := fold(nil); re == "" && ca == "" {
			w.log.WarnContext(ctx, fmt.Sprintf("baseline: %s: %s left out, the page at its ceiling (%s)", projectName, plural(len(d.Added), "new statement"), capReason))
			statements, leftOut, chars, capReason = s, l, c, ""
		}
	}
	if reason == "" {
		reason = capReason
	}
	if reason != "" {
		return nil, reason, nil
	}
	logLeftOut(ctx, w.log, "baseline: "+projectName, leftOut)
	drawn := w.revisionMap(ctx, page, d.Map, slugs)
	if summary == page.Summary && sameStatements(statements, page.Statements) && diagram.Equal(drawn, page.Map) {
		w.log.InfoContext(ctx, "baseline: project description identical, no revision")
		return nil, "", nil
	}
	runID := r.ID
	return &store.NewRevision{
		Scope: store.ScopeProject, ProjectKey: r.ProjectKey, Summary: summary, Statements: statements, Chars: chars,
		SourceKind: store.FromBaseline, SourceCommit: r.Commit, BaselineRunID: &runID, Map: drawn,
	}, "", nil
}

// revisionMap is the map a project revision carries. A project too large for
// one gets none. Otherwise the returned map, its departed repositories
// taken out, is held to the map's rules and the drawing's limits (not the
// statement diagrams' caps); one that fails, or none returned, keeps the
// stored map, with the reason logged: the map never costs the description.
func (w *Worker) revisionMap(ctx context.Context, page store.Page, proposed *diagram.Diagram, slugs []string) *diagram.Diagram {
	at := "baseline: " + projectName + " map"
	if !diagram.MapFits(len(slugs)) {
		if proposed != nil || page.Map != nil {
			w.log.InfoContext(ctx, fmt.Sprintf("%s: %d repositories, more than a map holds, none drawn", at, len(slugs)))
		}
		return nil
	}
	if proposed == nil {
		w.log.WarnContext(ctx, at+": none returned, the stored one kept")
		return w.carriedMap(ctx, page, slugs)
	}
	drawn, why := baseline.CheckMap(baseline.LabelRepos(w.withoutDeparted(ctx, proposed, slugs)), slugs, baseline.DrawCaps)
	if why != "" {
		w.log.WarnContext(ctx, at+" refused ("+why+"), the stored one kept")
		return w.carriedMap(ctx, page, slugs)
	}
	return drawn
}

// noTree is the project's file check: no path pins a project statement.
func noTree(string) bool { return false }

// carriedMap is the stored map as a revision carries it on: its departed
// repositories taken out, so a carried map never names a repository that
// left. A project that was due a map and has none gets the sentinel: asked
// for, not drawn, so a quiet reconcile does not run the step again for it
// (a run that folds the project anyway asks again).
func (w *Worker) carriedMap(ctx context.Context, page store.Page, slugs []string) *diagram.Diagram {
	if page.Map == nil || diagram.IsSentinel(page.Map) {
		return diagram.Sentinel()
	}
	// Checked again only for what the trim may break: a map whose last
	// repository left holds nothing of the project, and an emptied zone goes.
	carried, why := baseline.CheckMap(w.withoutDeparted(ctx, page.Map, slugs), slugs, baseline.DrawCaps)
	if why != "" {
		return diagram.Sentinel()
	}
	return carried
}

// withoutDeparted is DropDeparted with its log line.
func (w *Worker) withoutDeparted(ctx context.Context, m *diagram.Diagram, slugs []string) *diagram.Diagram {
	trimmed, gone := baseline.DropDeparted(m, slugs)
	if len(gone) > 0 {
		w.log.InfoContext(ctx, "baseline: "+projectName+" map: no longer source repositories, left out: "+strings.Join(gone, ", "))
	}
	return trimmed
}

// slugs is the project's source repositories by slug: what a map's
// repository node may be named.
func slugs(repos []store.RepoSummary) []string {
	out := make([]string, 0, len(repos))
	for _, r := range repos {
		out = append(out, r.Slug)
	}
	return out
}

// skipProject ends the run without its project step, counted as a skipped
// part and named on the plan, so the next reconcile folds the project.
func (w *Worker) skipProject(ctx context.Context, r *store.BaselineRun, plan runPlan, why string) error {
	w.log.WarnContext(ctx, "baseline: "+projectName+" skipped: "+why)
	r.Skipped++
	if !slices.Contains(plan.Skipped, projectName) {
		plan.Skipped = append(plan.Skipped, projectName)
	}
	return w.finish(ctx, r, plan)
}

// runSource is the source a run attaches: its commit, the commit's time and
// the repository, which on the project page names whose commit it is.
func runSource(r store.BaselineRun, plan runPlan) store.Source {
	return store.Source{Kind: store.FromBaseline, Commit: r.Commit, CommittedAt: plan.CommittedAt, Repo: r.Slug}
}

// markDone marks the run done in memory: its commit is the one documented now,
// even when nothing changed on the page, or every reconcile would read it
// again.
func (w *Worker) markDone(ctx context.Context, r *store.BaselineRun, plan runPlan) error {
	if err := encodeInto(r, plan); err != nil {
		return err
	}
	now := w.now().UTC()
	r.State, r.FinishedAt = store.RunDone, &now
	w.log.InfoContext(ctx, fmt.Sprintf("baseline: run done at %s: %d calls, %d bytes read, %d skipped",
		gitrepo.ShortSHA(r.Commit), r.Calls, r.Bytes, r.Skipped))
	return nil
}

// finish ends a run done and saves it.
func (w *Worker) finish(ctx context.Context, r *store.BaselineRun, plan runPlan) error {
	if err := w.markDone(ctx, r, plan); err != nil {
		return err
	}
	return w.save(ctx, *r)
}

func sameChecks(a, b []store.Statement) bool {
	for i := range a {
		if a[i].Checked != b[i].Checked {
			return false
		}
	}
	return true
}

func subjectList(p baseline.Plan) string {
	var b strings.Builder
	for _, s := range p.Subjects {
		fmt.Fprintf(&b, "- %s: %s\n", s.Name, s.Purpose)
	}
	return b.String()
}

// renderFiles lays files out for a prompt, each between markers no source
// file is likely to hold; a file the read left out is not shown.
func renderFiles(files []baseline.File, bodies map[string][]byte) string {
	var b strings.Builder
	for _, f := range files {
		body, ok := bodies[f.Path]
		if !ok {
			continue
		}
		fmt.Fprintf(&b, "<<<FILE %s>>>\n%s\n<<<END FILE %s>>>\n\n", f.Path, strings.TrimRight(string(body), "\n"), f.Path)
	}
	if b.Len() == 0 {
		return "_None._"
	}
	return b.String()
}

func renderCatalogue(r store.BaselineRun, listing, docs, placing string, maxSubjects int) string {
	if placing == "" {
		placing = "Catalogue the whole repository."
	}
	if docs == "" {
		docs = "_None._"
	}
	return strings.NewReplacer(
		"{repo}", r.Slug, "{project}", r.ProjectKey, "{commit}", r.Commit, "{placing}", placing,
		"{max_subjects}", fmt.Sprint(maxSubjects), "{listing}", listing, "{docs}", docs,
	).Replace(prompts.Catalogue)
}

// The budget of a describe step: what it may add to the page, net. The
// ceilings are for the whole page, so the room it has left is shared out
// over this part and those after it, the overview weighing overviewShare,
// the summary and what spans the repository being its to write.
// minStepBudget lets a part whose share is small still add a paragraph,
// never past the room the page has; a correction that adds nothing costs
// nothing, so even a full page takes one. A step may add one statement
// while the page is under its statement cap and has room for one
// (minStatementChars); otherwise none.
const (
	minStepBudget     = 2000
	minStatementChars = 300
	overviewShare     = 2
)

// budget is what a step may add to the page at most: characters of prose
// and new statements.
type budget struct {
	chars, statements int
}

// stepBudget gives this part its weighted share of the page's room under
// each ceiling: weight overviewShare for the overview when parts follow it
// (its extra share is theirs to give), else 1, over the weights of this
// part and the left after it; at least the floor, never past the room.
func stepBudget(l config.Limits, page store.Page, left int, overview bool) budget {
	w := 1
	if overview && left > 0 {
		w = overviewShare
	}
	shares := left + w
	chars := l.RepoMaxChars - prose(page.Summary, page.Statements)
	room := l.RepoMaxStatements - len(page.Statements)
	var b budget
	if chars > 0 {
		b.chars = min(max(w*chars/shares, minStepBudget), chars)
	}
	if room > 0 && chars >= minStatementChars {
		b.statements = min(max(w*room/shares, 1), room)
	}
	return b
}

// allowance is the budget as the prompt says it, finishing "This part ...".
func allowance(b budget) string {
	switch {
	case b.chars == 0:
		return "may not grow the description or add a statement: correct, tighten or merge existing statements instead"
	case b.statements == 0:
		return fmt.Sprintf("may grow the description by at most about %d characters of prose but add no statement: deepen or merge existing ones instead", b.chars)
	}
	return fmt.Sprintf("may grow the description by at most about %d characters of prose and add at most %s", b.chars, plural(b.statements, "new statement"))
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// growth is what a fold adds to the page, counted like the ceilings: the
// prose after it less the prose before, and the statements new to it.
func growth(page store.Page, summary string, statements []store.Statement) (chars, added int) {
	had := make(map[string]bool, len(page.Statements))
	for _, s := range page.Statements {
		had[s.ID] = true
	}
	for _, s := range statements {
		if !had[s.ID] {
			added++
		}
	}
	return prose(summary, statements) - prose(page.Summary, page.Statements), added
}

// returnedUnchanged counts the statements a patch returns by id exactly as
// stored, after the cleanup attach applies: output paid for that belonged
// in confirmed. One with any attempted change counts not, even one the
// fold then took out (an invented path, a diagram left out): the model
// tried.
func returnedUnchanged(before []store.Statement, p inference.Patch) int {
	was := make(map[string]store.Statement, len(before))
	for _, s := range before {
		was[s.ID] = s
	}
	n := 0
	for _, st := range p.Statements {
		if st.ID == nil {
			continue
		}
		old, ok := was[*st.ID]
		if !ok {
			continue
		}
		c := cleanStatement(st)
		c.ID, c.Diagram = old.ID, old.Diagram
		// null, the stored diagram again, or none on a statement without one
		// change nothing; anything else is an attempt.
		switch {
		case st.Diagram == nil, diagram.Equal(st.Diagram, old.Diagram):
		case st.Diagram.Kind == diagram.KindNone && old.Diagram == nil:
		default:
			c.Diagram = st.Diagram
		}
		if sameStatement(old, c) {
			n++
		}
	}
	return n
}

// problemsFor is the problem areas a subject's files sit in: an area whose
// directory is one of the files' directories, holds one or lies inside
// one. The root area backs no subject: ingest files a fix there whose
// files share no directory, or that named none, so it is no evidence about
// root files; and a subject with a file at the root is not tied to every
// area.
func problemsFor(problems []store.RepoProblem, files []baseline.File) []store.RepoProblem {
	dirs := map[string]bool{}
	for _, f := range files {
		if d := path.Dir(f.Path); d != "." {
			dirs[d] = true
		}
	}
	var out []store.RepoProblem
	for _, p := range problems {
		if p.Area == "" {
			continue
		}
		for d := range dirs {
			if p.Area == d || strings.HasPrefix(d, p.Area+"/") || strings.HasPrefix(p.Area, d+"/") {
				out = append(out, p)
				break
			}
		}
	}
	return out
}

// renderProblems lays the problem areas out for the describe prompt, data
// like the files.
func renderProblems(problems []store.RepoProblem) string {
	if len(problems) == 0 {
		return "_None._"
	}
	var b strings.Builder
	for _, p := range problems {
		fmt.Fprintf(&b, "- `%s/`: %d bugfixes, cause seen most `%s`, newest fixes %s\n", p.Area, p.Bugfixes, p.TopCause, strings.Join(p.PRKeys, ", "))
	}
	return b.String()
}

func renderDescribe(r store.BaselineRun, page store.Page, l config.Limits, step, steps, left int, b budget, part, focus, files, problems string, newer map[string]string) string {
	return strings.NewReplacer(
		"{problem_areas}", problems,
		"{repo}", r.Slug, "{project}", r.ProjectKey, "{commit}", r.Commit,
		"{step}", fmt.Sprint(step), "{steps}", fmt.Sprint(steps), "{steps_left}", fmt.Sprint(left),
		"{step_budget}", allowance(b), "{diagram_change}", diagramChange(l), "{part}", part, "{focus}", focus,
		"{repo_max_statements}", fmt.Sprint(l.RepoMaxStatements), "{repo_max_chars}", fmt.Sprint(l.RepoMaxChars),
		"{statements}", fmt.Sprint(len(page.Statements)), "{repo_chars}", fmt.Sprint(prose(page.Summary, page.Statements)),
		"{repo_description}", describeSourced(page, newer), "{files}", files, "{diagram_rules}", diagramRules(l), "{diagram_field}", diagramField(l),
	).Replace(prompts.Describe)
}

func renderProject(r store.BaselineRun, repoPage, projectPage store.Page, others string, repoSlugs []string, l config.Limits) string {
	return strings.NewReplacer(
		"{repo}", r.Slug, "{project}", r.ProjectKey, "{commit}", r.Commit,
		"{project_max_statements}", fmt.Sprint(l.ProjectMaxStatements), "{project_max_chars}", fmt.Sprint(l.ProjectMaxChars),
		"{project_chars}", fmt.Sprint(prose(projectPage.Summary, projectPage.Statements)),
		"{project_description}", describeTaught(projectPage), "{repo_description}", describeRead(repoPage), "{other_repos}", others,
		"{project_map}", projectMap(projectPage), "{repo_slugs}", strings.Join(repoSlugs, ", "), "{diagram_rules}", diagramRules(l), "{diagram_field}", diagramField(l),
		"{map_due}", mapDue(len(repoSlugs)), "{map_max_nodes}", fmt.Sprint(diagram.DrawMaxNodes), "{map_max_edges}", fmt.Sprint(diagram.DrawMaxEdges),
	).Replace(prompts.Project)
}

// mapDue says whether this project's rewrite carries a map: always, unless
// the project has more repositories than one map holds; only then null.
func mapDue(repos int) string {
	if diagram.MapFits(repos) {
		return "Every rewrite carries `map`, never null"
	}
	return fmt.Sprintf("This project has %d repositories, more than one map holds: `map` is null", repos)
}

// projectMap is the current system map as the JSON the model returns, so
// keeping it means repeating it. The sentinel is no map.
func projectMap(p store.Page) string {
	if p.Map == nil || diagram.IsSentinel(p.Map) {
		return "_None yet: draw it._"
	}
	j, err := json.Marshal(p.Map)
	if err != nil {
		return "_None yet: draw it._"
	}
	return string(j)
}

// otherRepos is the summaries of the project's other source repositories,
// by slug, so the project is written as a whole. Over ProjectReposMaxBytes
// each gets an even share, what a short one leaves going to the rest, and a
// longer one is cut to its share at a paragraph or sentence and says so:
// no repository is left out for its length. Those without a summary, or
// with a share too small to hold one (hundreds of repositories), are
// named. A prompt's input, never stored text.
func otherRepos(repos []store.RepoSummary, self string) string {
	var others []store.RepoSummary
	for _, r := range repos {
		if r.Slug != self && r.Summary != "" {
			others = append(others, r)
		}
	}
	bySize := slices.Clone(others)
	slices.SortStableFunc(bySize, func(a, b store.RepoSummary) int { return len(a.Summary) - len(b.Summary) })
	text := map[string]string{}
	left := baseline.ProjectReposMaxBytes
	for i, r := range bySize {
		// The heading counts against the share; the names after are not.
		frame := len(repoEntry(r.Slug, ""))
		if s := shorten(r.Summary, left/(len(bySize)-i)-frame); s != "" {
			text[r.Slug] = s
			left -= frame + len(s)
		}
	}
	var b strings.Builder
	var named []string
	for _, r := range repos {
		switch {
		case r.Slug == self:
		case text[r.Slug] == "":
			named = append(named, r.Slug)
		default:
			b.WriteString(repoEntry(r.Slug, text[r.Slug]))
		}
	}
	if len(named) > 0 {
		fmt.Fprintf(&b, "Also in the project, without a summary here: %s.\n", strings.Join(named, ", "))
	}
	if b.Len() == 0 {
		return "_None._"
	}
	return b.String()
}

// shortenedMark ends a summary cut to its share.
const shortenedMark = " […shortened]"

// shorten cuts s to at most n bytes at the last paragraph break, else the
// last sentence end, else a rune boundary, and marks the cut; a break only
// in the second half, so an early one does not waste the share. "" when n
// does not hold the mark.
func shorten(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= len(shortenedMark) {
		return ""
	}
	cut := s[:n-len(shortenedMark)]
	for !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	if i := strings.LastIndex(cut, "\n\n"); i > len(cut)/2 {
		cut = cut[:i]
	} else if i := sentenceEnd(cut); i > len(cut)/2 {
		cut = cut[:i+1]
	}
	return cut + shortenedMark
}

// abbreviations end in a period without ending a sentence ("z. B." and
// its kin are single letters, caught apart).
var abbreviations = map[string]bool{"bzw": true, "usw": true, "etc": true, "inkl": true, "ggf": true, "evtl": true,
	"vgl": true, "sog": true, "bspw": true, "max": true, "min": true, "ca": true, "Nr": true, "resp": true}

// sentenceEnd is the index of the last ". " in s that ends a sentence, not
// an abbreviation; -1 when none does.
func sentenceEnd(s string) int {
	for i := strings.LastIndex(s, ". "); i > 0; i = strings.LastIndex(s[:i], ". ") {
		word := s[strings.LastIndexAny(s[:i], " \n(")+1 : i]
		if utf8.RuneCountInString(word) > 1 && !abbreviations[word] {
			return i
		}
	}
	return -1
}

func repoEntry(slug, summary string) string { return "### " + slug + "\n\n" + summary + "\n\n" }

// describeSourced is describe with each statement's provenance: a step
// must know which statements a pull request taught.
func describeSourced(p store.Page, newer map[string]string) string {
	return describePage(p, pageView{prs: prKeys, newer: newer})
}

// describeTaught is describe saying which statements pull requests taught,
// without their keys: the project step needs the fact, and keys shown are
// keys copied into the prose.
func describeTaught(p store.Page) string { return describePage(p, pageView{prs: prTaught}) }

// leftAloneNote says why a step must leave a statement as it is.
func leftAloneNote(why string) string {
	switch why {
	case byNewerPR:
		return "\nA pull request newer than the code shown taught this: leave it as it is, do not edit, drop or confirm it.\n"
	case byUnplaced:
		return "\nWhether the code shown holds the pull request that taught this is unknown: leave it as it is, do not edit, drop or confirm it.\n"
	case byCode:
		return "\nThis rests on code, which the overview does not read: leave it as it is, do not edit, drop or confirm it.\n"
	}
	return ""
}
