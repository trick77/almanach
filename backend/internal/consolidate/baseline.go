package consolidate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/trick77/almanach/internal/baseline"
	"github.com/trick77/almanach/internal/config"
	"github.com/trick77/almanach/internal/diagram"
	"github.com/trick77/almanach/internal/gitrepo"
	"github.com/trick77/almanach/internal/inference"
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
	SetRepoMap(ctx context.Context, project, repo string, m *diagram.Diagram, basis string, r store.BaselineRun) (bool, error)
	DocumentedRun(ctx context.Context, key, slug string) (store.BaselineRun, bool, error)
	SourceRepos(ctx context.Context, key string) ([]store.SourceRepo, error)
	IsSourceRepo(ctx context.Context, key, slug string) (bool, error)
	RepoSummaries(ctx context.Context, project string) ([]store.RepoSummary, error)
	Libraries(ctx context.Context, key, slug string) ([]store.Library, error)
	ProjectFolded(ctx context.Context, key string) error
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

// Describer is the model that writes: the patch of a step, the project,
// the repository's map.
type Describer interface {
	Patch(ctx context.Context, prompt string) inference.PatchResult
	Describe(ctx context.Context, prompt string) inference.DescriptionResult
	RepoMap(ctx context.Context, prompt string) inference.RepoMapResult
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
	// ProjectDone: the project step is over, written or skipped; the run,
	// still in its project state, has the repository map step left. A
	// restart takes the map step, never the project's call again.
	ProjectDone bool `json:"project_done,omitempty"`
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
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, err
	}
	p.Upgrade()
	return p, nil
}

// errStop ends a drain: a gateway hold or a store that does not answer.
var errStop = errors.New("stop the drain")

// stepRun runs one step of the run. Reports whether the drain may go on.
func (w *Worker) stepRun(ctx context.Context, r store.BaselineRun) bool {
	ctx = logging.With(ctx, "project", r.ProjectKey, "repo", r.Slug, "run_id", r.ID)
	w.setBusy(Busy{Kind: KindRun, ProjectKey: r.ProjectKey, RepoSlug: r.Slug, RunID: r.ID})
	defer w.clearBusy()
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
		w.setWait(r.ID, WaitClone)
		return false
	}
	// Git could not place a merge commit: the same step again later, no
	// call spent, within placeWait (newerIDs).
	if errors.Is(err, errPlaceAgain) {
		w.setWait(r.ID, WaitPlace)
		return false
	}
	w.clearWait()
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
			w.hold(ctx, HoldBusy, 0)
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
	w.hold(ctx, HoldBusy, 0)
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
	var step *int
	if purpose == store.PurposeOverview || purpose == store.PurposeSubject {
		i := r.Step
		step = &i
	}
	if _, err := w.store.InsertLLMCall(ctx, store.LLMCall{
		ProjectKey: r.ProjectKey, RepoSlug: r.Slug, SourceKind: store.FromBaseline, SourceCommit: r.Commit, Purpose: purpose,
		BaselineRunID: &runID, Step: step, Outcome: res.Outcome.String(), Model: res.Cost.Model, InputTokens: res.Cost.InputTokens,
		CachedTokens: res.Cost.CachedTokens, OutputTokens: res.Cost.OutputTokens, CostNanoUSD: res.Cost.NanoUSD, CallID: res.Cost.CallID,
	}); err != nil {
		w.log.WarnContext(ctx, "baseline: call row failed", logging.Err(err))
	}
	w.log.InfoContext(ctx, fmt.Sprintf("baseline: %s call %s (%s)", purpose, res.Outcome, res.Cost.LogLine()))
	if gateway, retryAfter := gatewayClass(res); gateway {
		w.hold(ctx, gatewayCause(res), retryAfter)
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
// less the catalogue, the overview, the project and the repository map.
func (w *Worker) maxSubjects() int {
	return max(w.base.Limits.MaxCalls-4, 0)
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
	// Touched over the merged plan with the last run's skipped names and
	// those it read but must read again (Again), the new plan starting with
	// none of its own.
	again := append(slices.Clone(old.Skipped), old.Again...)
	touched, _ := baseline.Touched(baseline.Plan{Subjects: merged.Subjects, Skipped: again}, reading)
	merged.Skipped, merged.Why, merged.Steps, merged.Again, merged.AgainOverview = nil, nil, nil, nil, false
	plan.Plan, plan.Read = merged, touched
	changed := changes.Paths()
	for p := range changed {
		plan.Changed = append(plan.Changed, p)
	}
	slices.Sort(plan.Changed)
	// The overview reads the docs again when one changed or the last run's
	// failed or left a held statement alone; the project step folds again
	// when the last run's did not.
	plan.Overview = old.StepSkipped(baseline.PartOverview) || old.AgainOverview
	plan.SummaryChanged = old.StepSkipped(baseline.PartProject)
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
			// A repository page whose map is missing (described before
			// maps existed) or drawn from text that moved on since (a
			// merge, a run whose map step was skipped) goes on to the map
			// step, the project's call still skipped (projectStands says
			// so again there).
			if skip {
				repo, ok, err := w.store.CurrentPage(ctx, store.ScopeRepository, r.ProjectKey, r.Slug)
				if err != nil {
					w.log.WarnContext(ctx, "baseline: repository page read failed, drain paused", logging.Err(err))
					return errStop
				}
				if !ok || repo.MapStands() {
					return w.finish(ctx, r, plan)
				}
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
// rules refuse skips the part, read again by the next run.
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
	name, overview := partAt(plan, i)
	var part, purpose string
	var read []baseline.File
	subject := !overview
	if !subject {
		part, purpose, read = overviewPart, store.PurposeOverview, baseline.Docs(files, baseline.DocsMaxBytes)
	} else {
		s, ok := subjectNamed(plan.Plan, name)
		if !ok {
			return w.skipPart(ctx, r, &plan, baseline.SkipGone, "no longer in the plan")
		}
		part, purpose, read = fmt.Sprintf("the subject %q: %s", s.Name, s.Purpose), store.PurposeSubject, s.Files
	}
	if r.Calls >= w.base.Limits.MaxCalls {
		return w.endReading(ctx, r, &plan, baseline.SkipCalls, "the run's calls are spent")
	}
	var size int64
	for _, f := range read {
		size += f.Size
	}
	if r.Bytes+size > int64(w.base.Limits.MaxBytes) {
		return w.skipPart(ctx, r, &plan, baseline.SkipBytes, "over the run's byte budget")
	}
	page, _, err := w.store.CurrentPage(ctx, store.ScopeRepository, r.ProjectKey, r.Slug)
	if err != nil {
		w.log.WarnContext(ctx, "baseline: repository page read failed, drain paused", logging.Err(err))
		return errStop
	}
	// Only a step that will make a call asks; a held one has read no file.
	newer, err := w.newerIDs(ctx, r, page.Statements, plan.CommittedAt)
	if err != nil {
		return readErr(err)
	}
	if overview {
		newer = codeBacked(page.Statements, newer)
	}
	// The libraries before the files: a store that does not answer costs no
	// read of the clone. Once a run.
	if w.libsRun != r.ID {
		libs, err := w.base.Store.Libraries(ctx, r.ProjectKey, r.Slug)
		if err != nil {
			w.log.WarnContext(ctx, "baseline: libraries read failed, drain paused", logging.Err(err))
			return errStop
		}
		w.libsRun, w.libs = r.ID, libraries(libs, false)
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
	prompt := renderDescribe(*r, page, w.limits, i+1, r.Steps, part, focus, renderFiles(read, bodies), renderProblems(problems), w.libs, newer)
	res := w.base.Describer.Patch(ctx, prompt)
	ok, err := w.call(ctx, r, purpose, res.Call)
	if err != nil {
		return err
	}
	r.Bytes += size
	if !ok {
		return w.skipPart(ctx, r, &plan, baseline.SkipFailed, "the call failed: "+res.Outcome.String())
	}
	res.Patch.Statements, res.Patch.Added = w.asNew(ctx, "baseline: "+name, page.Statements, res.Patch.Statements, res.Patch.Added)
	// A step shown no problem area had no evidence for a new pitfall.
	if len(problems) == 0 {
		res.Patch.Added = unbackedPitfalls(ctx, w.log, "baseline: "+name, res.Patch.Added)
	}
	return w.foldPatch(ctx, r, &plan, name, overview, page, res.Patch, baseline.Exists(tree), newer)
}

// foldPatch applies a step's patch to the page under the rules of a fold
// and writes the revision with the run's progress in one transaction.
func (w *Worker) foldPatch(ctx context.Context, r *store.BaselineRun, plan *runPlan, name string, overview bool, page store.Page, patch inference.Patch, exists func(string) bool, newer map[string]string) error {
	// A statement the step leaves alone (a newer PR taught it, its merge
	// commit unplaced, or code backs it on the overview) is struck from the
	// patch, not the patch refused: one item costs only itself. One the run
	// cannot judge yet has its part read again by the next reconcile.
	patch, leftAlone, again := strikeHeld(patch, newer)
	if len(leftAlone) > 0 {
		w.log.InfoContext(ctx, "baseline: "+name+": left alone: "+strings.Join(leftAlone, ", "))
	}
	if again {
		if overview {
			plan.AgainOverview = true
		} else if !slices.Contains(plan.Again, name) {
			plan.Again = append(plan.Again, name)
		}
	}
	applied := baseline.ApplyPatch(page.Statements, patch, exists, validFor(store.ScopeRepository))
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
	// ApplyPatch left out what attach would refuse, so a reason here is a
	// bug, and costs the part.
	statements, leftOut, reason := attach(store.ScopeRepository, page.Statements, applied.Statements, runSource(*r, *plan), next, limitsOf(w.limits))
	if reason != "" {
		return w.skipPart(ctx, r, plan, baseline.SkipRefused, "patch refused: "+reason)
	}
	leftOut.badEdits = append(applied.BadEdits, leftOut.badEdits...)
	leftOut.badNew, leftOut.badDrops = applied.BadNew, applied.BadDrops
	summary := page.Summary
	if applied.Summary != nil {
		summary = proseSummary(*applied.Summary)
	}
	// Checked travels by id, never by position: a statement this step wrote
	// or confirmed was read against the commit; one whose edit was left out
	// comes back as stored, unchecked, and was not.
	checked, stored := map[string]bool{}, map[string]bool{}
	for i, s := range applied.Statements {
		if s.ID != nil {
			checked[*s.ID] = applied.Checked[i]
		}
	}
	for _, s := range page.Statements {
		stored[s.ID] = true
	}
	for i := range statements {
		id := statements[i].ID
		if !stored[id] || checked[id] {
			statements[i].Checked = r.Commit
		}
	}
	// No ceiling, and no summary required of a step: a page whose summary
	// is not written yet (the overview failed) takes the statements all
	// the same, the summary left to a later step (user, 2026-11-01).
	chars := prose(summary, statements)
	if strings.TrimSpace(summary) == "" {
		w.log.WarnContext(ctx, "baseline: "+name+": no summary yet, the statements taken without one")
	}
	grown, added := growth(page, summary, statements)
	w.log.InfoContext(ctx, fmt.Sprintf("baseline: %s: page grew by %d chars of prose; %s; %d returned unchanged",
		name, grown, plural(added, "new statement"), returnedUnchanged(page.Statements, patch)))
	logLeftOut(ctx, w.log, "baseline: "+name, leftOut)
	if summary != page.Summary {
		plan.SummaryChanged = true
	}
	prev := *r
	r.Step++
	if err := encodeInto(r, *plan); err != nil {
		return err
	}
	if summary == page.Summary && sameStatements(page.Statements, statements) && sameChecks(statements, page.Statements) {
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

// strikeHeld takes the ids a step leaves alone out of a patch, edits, drops
// and confirmations alike, and names each with why (byNewerPR, byUnplaced,
// byCode): the rest of the patch applies (user, 2026-11-01: an item costs
// only itself). again reports a struck id the code may show otherwise once
// the run reads its PR (byNewerPR, byUnplaced): the part is read again by
// the next reconcile. One code backs is the overview's never to touch.
func strikeHeld(p inference.Patch, newer map[string]string) (_ inference.Patch, struck []string, again bool) {
	held := func(id string) bool {
		why := newer[id]
		if why == "" {
			return false
		}
		struck = append(struck, id+" ("+heldWhy[why]+")")
		again = again || why != byCode
		return true
	}
	p.Statements = slices.DeleteFunc(slices.Clone(p.Statements), func(s inference.Statement) bool { return s.ID != nil && held(*s.ID) })
	p.Drop = slices.DeleteFunc(slices.Clone(p.Drop), held)
	p.Confirmed = slices.DeleteFunc(slices.Clone(p.Confirmed), held)
	return p, struck, again
}

// heldWhy words why a step leaves a statement alone, for the log.
var heldWhy = map[string]string{
	byNewerPR:  "taught by a pull request newer than the commit read",
	byUnplaced: "rests on a merge commit git could not place",
	byCode:     "rests on code",
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
func (w *Worker) skipPart(ctx context.Context, r *store.BaselineRun, plan *runPlan, code baseline.Skip, why string) error {
	w.log.WarnContext(ctx, "baseline: "+partLabel(*plan, r.Step)+" skipped: "+why)
	r.Skipped++
	plan.skipAt(r.Step, code)
	r.Step++
	if err := encodeInto(r, *plan); err != nil {
		return err
	}
	return w.save(ctx, *r)
}

// endReading stops the reading steps: every part not read yet is skipped,
// and the run goes on to its project step.
func (w *Worker) endReading(ctx context.Context, r *store.BaselineRun, plan *runPlan, code baseline.Skip, why string) error {
	w.log.WarnContext(ctx, "baseline: reading ends: "+why)
	for i := r.Step; i < plan.steps(); i++ {
		r.Skipped++
		plan.skipAt(i, code)
	}
	r.Step, r.State = plan.steps(), store.RunProject
	if err := encodeInto(r, *plan); err != nil {
		return err
	}
	return w.save(ctx, *r)
}

// repoMapName names the map step in the log; never on the skipped list: a
// stale map is redrawn by the next run, no reconcile needs it.
const repoMapName = "the repository map"

// The overview and the project step in log lines. Never a key: a subject
// may carry either name (the model names them), so the plan holds the two
// by baseline.Part.
const (
	overviewLabel = "the overview"
	projectLabel  = "the project"
)

// partAt is reading step i: the overview, or a subject by name. The name
// is overviewLabel for the overview, for log lines only.
func partAt(plan runPlan, i int) (name string, overview bool) {
	if plan.Overview {
		if i == 0 {
			return overviewLabel, true
		}
		i--
	}
	if i < len(plan.Read) {
		return plan.Read[i], false
	}
	return "", false
}

// partLabel is reading step i in a log line: the overview as itself, a
// subject quoted, so one named like a step still reads as a subject.
func partLabel(plan runPlan, i int) string {
	name, overview := partAt(plan, i)
	if overview {
		return name
	}
	return "the subject " + strconv.Quote(name)
}

// skipAt marks reading step i skipped for code: the overview by its Part,
// a subject by its name.
func (p *runPlan) skipAt(i int, code baseline.Skip) {
	name, overview := partAt(*p, i)
	if overview {
		p.SkipStep(baseline.PartOverview, code)
		return
	}
	p.SkipSubject(name, code)
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

// encodeInto stores the plan on the run, always as the current format: the
// only writer of a plan, so only a plan from before it reads as legacy.
func encodeInto(r *store.BaselineRun, plan runPlan) error {
	plan.Format = baseline.PlanFormat
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
	if plan.ProjectDone {
		return w.repoMapStep(ctx, r, plan)
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
		return w.projectOver(ctx, r, plan)
	}
	if r.Calls >= w.base.Limits.MaxCalls {
		return w.skipProject(ctx, r, plan, baseline.SkipCalls, "the run's calls are spent")
	}
	repoPage, ok, err := w.store.CurrentPage(ctx, store.ScopeRepository, r.ProjectKey, r.Slug)
	if err != nil {
		w.log.WarnContext(ctx, "baseline: repository page read failed, drain paused", logging.Err(err))
		return errStop
	}
	if !ok {
		w.log.InfoContext(ctx, "baseline: no repository description to fold into the project")
		return w.projectOver(ctx, r, plan)
	}
	if repos == nil {
		if repos, err = w.repoSummaries(ctx, r); err != nil {
			return err
		}
	}
	libs, err := w.base.Store.Libraries(ctx, r.ProjectKey, "")
	if err != nil {
		w.log.WarnContext(ctx, "baseline: libraries read failed, drain paused", logging.Err(err))
		return errStop
	}
	res := w.base.Describer.Describe(ctx, renderProject(*r, repoPage, projectPage, otherRepos(repos, r.Slug), libs, slugs(repos), w.limits))
	okCall, err := w.call(ctx, r, store.PurposeProject, res.Call)
	if err != nil {
		return err
	}
	if !okCall {
		return w.skipProject(ctx, r, plan, baseline.SkipFailed, "the call failed: "+res.Outcome.String())
	}
	// An answer, whatever it says, is the fold a folder that became a system
	// again was due; failing to clear the mark costs one more fold, no more.
	if err := w.base.Store.ProjectFolded(ctx, r.ProjectKey); err != nil {
		w.log.WarnContext(ctx, "baseline: project due mark not cleared", logging.Err(err))
	}
	var rev *store.NewRevision
	if res.Description == nil {
		// No change carries the text and the map forward; a project with no
		// page yet has nothing to carry, and is asked again next time.
		if projectPage.RevisionID == 0 {
			return w.skipProject(ctx, r, plan, baseline.SkipNoPage, "no change asked of a project without a page")
		}
		rev = w.carriedRevision(ctx, r, projectPage, slugs(repos))
	} else {
		var refused string
		rev, refused, err = w.projectRevision(ctx, r, plan, projectPage, res.Description, slugs(repos))
		if err != nil {
			return err
		}
		if refused != "" {
			return w.skipProject(ctx, r, plan, baseline.SkipRefused, "rewrite refused: "+refused)
		}
	}
	if rev == nil {
		return w.projectOver(ctx, r, plan)
	}
	// The project revision and the step's end in one transaction: a crash
	// between them would pay for the project call again and write it twice.
	prev := *r
	plan.ProjectDone = true
	if err := encodeInto(r, plan); err != nil {
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
// The one place a run decides on the project: a folder whose repositories
// stand alone has none to write (no call, nothing skipped), and one that
// became a system again is folded whatever a reconcile would skip, so the
// page it kept catches up.
func (w *Worker) projectStands(ctx context.Context, r *store.BaselineRun, plan runPlan, page store.Page) (bool, []store.RepoSummary, error) {
	ps, err := w.store.ProjectSystemOf(ctx, r.ProjectKey)
	if err != nil {
		w.log.WarnContext(ctx, "baseline: project source read failed, drain paused", logging.Err(err))
		return false, nil, errStop
	}
	if ps.Standalone {
		w.log.InfoContext(ctx, "baseline: project step left out, the repositories stand alone")
		return true, nil, nil
	}
	if ps.Due || r.Mode != store.RunReconcile || plan.SummaryChanged {
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
// The page has no ceiling. The map never sinks the description: see
// revisionMap. slugs are the project's source
// repositories.
func (w *Worker) projectRevision(ctx context.Context, r *store.BaselineRun, plan runPlan, page store.Page, d *inference.Description, slugs []string) (*store.NewRevision, string, error) {
	next, err := w.store.NextStatementID(ctx, store.ScopeProject, r.ProjectKey, "")
	if err != nil {
		w.log.WarnContext(ctx, "baseline: statement id read failed, drain paused", logging.Err(err))
		return nil, "", errStop
	}
	answer := *d
	answer.Statements, answer.Added = w.asNew(ctx, "baseline: "+projectLabel, page.Statements, d.Statements, d.Added)
	d = &answer
	taught := map[string]bool{}
	for _, s := range page.Statements {
		taught[s.ID] = baseline.FromPR(s)
	}
	// No tree decides a project drop: a PR-taught one is kept here, before
	// ApplyPatch (which would judge its files against noTree). Unknown ids
	// pass on to ApplyPatch, which names them.
	var drop, kept []string
	for _, id := range d.Drop {
		if taught[id] {
			kept = append(kept, id)
			continue
		}
		drop = append(drop, id)
	}
	if len(kept) > 0 {
		w.log.WarnContext(ctx, "baseline: "+projectLabel+": drop of statements a pull request taught left out", slog.Any("ids", kept))
	}
	summary := proseSummary(d.Summary)
	// An empty summary costs only itself: the stored one stays, the rest of
	// the answer is taken; a first page takes its statements without one.
	if strings.TrimSpace(summary) == "" {
		w.log.WarnContext(ctx, "baseline: "+projectLabel+": empty summary left out, the stored one kept")
		summary = page.Summary
	}
	applied := baseline.ApplyPatch(page.Statements, inference.Patch{Statements: d.Statements, Added: d.Added, Drop: drop}, noTree, validFor(store.ScopeProject))
	if len(applied.Unknown) > 0 {
		w.log.WarnContext(ctx, "baseline: "+projectLabel+": unknown ids left out", slog.Any("ids", applied.Unknown))
	}
	// ApplyPatch left out what attach would refuse, so a reason here is a
	// bug, and costs the step.
	statements, leftOut, reason := attach(store.ScopeProject, page.Statements, applied.Statements, runSource(*r, plan), next, limitsOf(w.limits))
	if reason != "" {
		return nil, reason, nil
	}
	leftOut.badEdits = append(applied.BadEdits, leftOut.badEdits...)
	leftOut.badNew, leftOut.badDrops = applied.BadNew, applied.BadDrops
	// No ceiling: a description is documentation, never cut to a size
	// (user, 2026-11-01).
	chars := prose(summary, statements)
	logLeftOut(ctx, w.log, "baseline: "+projectLabel, leftOut)
	drawn := w.revisionMap(ctx, page, d.Map, slugs)
	if summary == page.Summary && sameStatements(page.Statements, statements) && diagram.Equal(drawn, page.Map) {
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
// one gets none. Otherwise the returned map, its self and repeated edges
// and its departed repositories taken out (RepairEdges, DropDeparted), its
// repository nodes labelled by slug, is held to the map's rules and the
// drawing's limits (not the
// statement diagrams' caps); one that fails, or none returned, keeps the
// stored map, with the reason logged: the map never costs the description.
func (w *Worker) revisionMap(ctx context.Context, page store.Page, proposed *diagram.Diagram, slugs []string) *diagram.Diagram {
	at := "baseline: " + projectLabel + " map"
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
	repaired, dropped := baseline.RepairEdges(proposed)
	if len(dropped) > 0 {
		w.log.InfoContext(ctx, at+": self or repeated edges taken out: "+strings.Join(dropped, ", "))
	}
	drawn, why := baseline.CheckMap(baseline.LabelRepos(w.withoutDeparted(ctx, repaired, slugs)), slugs, baseline.DrawCaps)
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
		w.log.InfoContext(ctx, "baseline: "+projectLabel+" map: no longer source repositories, left out: "+strings.Join(gone, ", "))
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
func (w *Worker) skipProject(ctx context.Context, r *store.BaselineRun, plan runPlan, code baseline.Skip, why string) error {
	w.log.WarnContext(ctx, "baseline: "+projectLabel+" skipped: "+why)
	r.Skipped++
	plan.SkipStep(baseline.PartProject, code)
	return w.projectOver(ctx, r, plan)
}

// projectOver ends the project step without a revision of it and saves
// the run on to its repository map step, the run's last.
func (w *Worker) projectOver(ctx context.Context, r *store.BaselineRun, plan runPlan) error {
	plan.ProjectDone = true
	if err := encodeInto(r, plan); err != nil {
		return err
	}
	return w.save(ctx, *r)
}

// repoMapStep is a run's last step, after the project's: one call draws
// the repository's map from its description as it stands, or keeps the
// map it has (null). Last because it is the first thing a tight budget
// drops: with the run's calls spent it is skipped and the stored map
// stays. A map whose basis is the page's text as it stands is due no
// call. A map off its rules or the drawing's limits is not stored, with
// the reason logged; the description is never touched. What the step
// keeps (a new map, or the stored one confirmed) is set on the page's
// current revision in place, with its basis, and the run's end, in one
// transaction: no revision of its own, the text did not change. A skip,
// a failed call or a refused map writes nothing, so the basis stays stale
// and the next run draws again. The map is optional: a database busy past
// its attempts ends the run done without it, never failed.
func (w *Worker) repoMapStep(ctx context.Context, r *store.BaselineRun, plan runPlan) error {
	at := "baseline: " + repoMapName
	if r.Calls >= w.base.Limits.MaxCalls {
		w.log.WarnContext(ctx, at+" skipped: the run's calls are spent, the stored one kept")
		return w.finish(ctx, r, plan)
	}
	page, ok, err := w.store.CurrentPage(ctx, store.ScopeRepository, r.ProjectKey, r.Slug)
	if err != nil {
		w.log.WarnContext(ctx, "baseline: repository page read failed, drain paused", logging.Err(err))
		return errStop
	}
	if !ok {
		w.log.InfoContext(ctx, at+" skipped: no repository description to draw it from")
		return w.finish(ctx, r, plan)
	}
	if page.MapStands() {
		w.log.InfoContext(ctx, at+" skipped: drawn from the description as it stands, the stored one kept")
		return w.finish(ctx, r, plan)
	}
	res := w.base.Describer.RepoMap(ctx, renderRepoMap(*r, page))
	okCall, err := w.call(ctx, r, store.PurposeRepoMap, res.Call)
	if err != nil {
		return err
	}
	if !okCall {
		w.log.WarnContext(ctx, at+" skipped: the call failed: "+res.Outcome.String()+", the stored one kept")
		return w.finish(ctx, r, plan)
	}
	keep := page.RepoMap
	switch {
	case res.Map == nil && keep == nil:
		// Recorded with the basis like a map: the model is not asked again
		// for the same text (a retry per run otherwise, forever).
		w.log.InfoContext(ctx, at+": none drawn")
	case res.Map == nil:
		w.log.InfoContext(ctx, at+": no change")
	default:
		repaired, dropped := baseline.RepairEdges(res.Map)
		if len(dropped) > 0 {
			w.log.InfoContext(ctx, at+": self or repeated edges taken out: "+strings.Join(dropped, ", "))
		}
		drawn, why := baseline.CheckRepoMap(repaired, baseline.DrawCaps)
		if why != "" {
			w.log.WarnContext(ctx, at+" refused ("+why+"), the stored one kept")
			return w.finish(ctx, r, plan)
		}
		if diagram.Equal(drawn, keep) {
			w.log.InfoContext(ctx, at+": no change")
		} else {
			keep = drawn
			w.log.InfoContext(ctx, fmt.Sprintf("%s: drawn (%d nodes, %d edges)", at, len(drawn.Nodes), len(drawn.Edges)))
		}
	}
	prev := *r
	if err := w.markDone(ctx, r, plan); err != nil {
		return err
	}
	if _, err := w.base.Store.SetRepoMap(ctx, r.ProjectKey, r.Slug, keep, page.MapBasis(), *r); err != nil {
		w.log.WarnContext(ctx, "baseline: repository map write failed", logging.Err(err))
		err = w.stepBusy(ctx, r, prev, repoMapName, err)
		if err == nil || errors.Is(err, errStop) {
			return err
		}
		// The attempts are spent: the map is optional, the run ends done
		// without it, the basis stale for the next run.
		w.log.WarnContext(ctx, at+" skipped: "+err.Error()+", the stored one kept")
		return w.finish(ctx, r, plan)
	}
	w.stepWritten()
	return nil
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

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// growth is what a fold adds to the page, for the log: the prose after it
// less the prose before, and the statements new to it.
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
		// An id edited twice counts once: ApplyPatch takes the first edit.
		delete(was, *st.ID)
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

func renderDescribe(r store.BaselineRun, page store.Page, l config.Limits, step, steps int, part, focus, files, problems, libs string,
	newer map[string]string) string {
	return strings.NewReplacer(
		"{problem_areas}", problems, "{libraries}", libs,
		"{repo}", r.Slug, "{project}", r.ProjectKey, "{commit}", r.Commit,
		"{step}", fmt.Sprint(step), "{steps}", fmt.Sprint(steps),
		"{diagram_change}", diagramChange(l), "{part}", part, "{focus}", focus,
		"{repo_description}", describeSourced(page, newer), "{files}", files, "{diagram_rules}", diagramRules(l), "{diagram_field}", diagramField(l),
	).Replace(prompts.Describe)
}

func renderProject(r store.BaselineRun, repoPage, projectPage store.Page, others string, libs []store.Library, repoSlugs []string,
	l config.Limits) string {
	nodes, edges := mapRoom(libs, len(repoSlugs))
	return strings.NewReplacer(
		"{libraries}", libraries(libs, true), "{repo}", r.Slug, "{project}", r.ProjectKey, "{commit}", r.Commit,
		"{project_description}", describeTaught(projectPage), "{repo_description}", describeRead(repoPage), "{other_repos}", others,
		"{project_map}", projectMap(projectPage), "{repo_slugs}", strings.Join(repoSlugs, ", "), "{diagram_rules}", diagramRules(l), "{diagram_field}", diagramField(l),
		"{map_due}", mapDue(len(repoSlugs)), "{map_max_nodes}", fmt.Sprint(nodes), "{map_max_edges}", fmt.Sprint(edges),
	).Replace(prompts.Project)
}

// mapRoom is the nodes and edges the model may draw on the system map:
// what one drawing holds less what code adds for the libraries (a node
// each, an edge per user), so the map with them still fits; never fewer
// than the repositories need, the UI then showing as many libraries as
// fit.
func mapRoom(libs []store.Library, repos int) (int, int) {
	edges := diagram.DrawMaxEdges
	for _, l := range libs {
		edges -= len(l.UsedBy)
	}
	return max(diagram.DrawMaxNodes-len(libs), repos), max(edges, repos)
}

func renderRepoMap(r store.BaselineRun, page store.Page) string {
	return strings.NewReplacer(
		"{repo}", r.Slug, "{project}", r.ProjectKey, "{commit}", r.Commit,
		"{repo_map}", repoMap(page), "{repo_description}", describeRead(page),
		"{map_max_nodes}", fmt.Sprint(diagram.DrawMaxNodes), "{map_max_edges}", fmt.Sprint(diagram.DrawMaxEdges),
	).Replace(prompts.RepoMap)
}

// repoMap is the repository's current map as the JSON the model returns,
// so the map step can say it stands.
func repoMap(p store.Page) string {
	if p.RepoMap == nil {
		return "_None yet: draw it._"
	}
	j, err := json.Marshal(p.RepoMap)
	if err != nil {
		return "_None yet: draw it._"
	}
	return string(j)
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
	return fitSummaries(repos, self, baseline.ProjectReposMaxBytes, "Also in the project, without a summary here")
}

// libraries is the summaries of the source repositories a repository, or
// a project's repositories, use (store.Libraries), named project/slug, each
// with its users when they are a project's; fitted to LibrariesMaxBytes
// like otherRepos. A prompt's input, never stored text.
func libraries(libs []store.Library, users bool) string {
	repos := make([]store.RepoSummary, 0, len(libs))
	for _, l := range libs {
		name := l.ProjectKey + "/" + l.Slug
		if users && len(l.UsedBy) > 0 {
			var by []string
			for _, u := range l.UsedBy {
				by = append(by, u.Slug)
			}
			name += " (used by " + strings.Join(by, ", ") + ")"
		}
		repos = append(repos, store.RepoSummary{Slug: name, Summary: l.Summary})
	}
	return fitSummaries(repos, "", baseline.LibrariesMaxBytes, "Also used, without a summary here")
}

// fitSummaries is otherRepos' and libraries' fitting: every summary but
// self's within max bytes, the rest named after also.
func fitSummaries(repos []store.RepoSummary, self string, max int, also string) string {
	var others []store.RepoSummary
	for _, r := range repos {
		if r.Slug != self && r.Summary != "" {
			others = append(others, r)
		}
	}
	bySize := slices.Clone(others)
	slices.SortStableFunc(bySize, func(a, b store.RepoSummary) int { return len(a.Summary) - len(b.Summary) })
	text := map[string]string{}
	left := max
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
		fmt.Fprintf(&b, "%s: %s.\n", also, strings.Join(named, ", "))
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
