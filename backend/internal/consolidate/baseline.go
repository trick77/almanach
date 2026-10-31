package consolidate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
		return errStop
	}
	if !ok {
		w.log.InfoContext(ctx, "baseline: run gone (repository removed) or over, dropped")
	}
	return nil
}

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
		w.hold(ctx, retryAfter)
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
		skip, err := w.projectStands(ctx, r, plan)
		if err != nil {
			return err
		}
		if skip {
			return w.finish(ctx, r, plan)
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
// rules refuse skips the part; a ceiling ends the reading.
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
	if name == overviewName {
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
	focus := w.focus(plan, page, newer)
	prompt := renderDescribe(*r, page, w.limits, i+1, r.Steps, plan.steps()-i-1, part, focus, renderFiles(read, bodies), newer)
	res := w.base.Describer.Patch(ctx, prompt)
	ok, err := w.call(ctx, r, purpose, res.Call)
	if err != nil {
		return err
	}
	r.Bytes += size
	if !ok {
		return w.skipPart(ctx, r, &plan, name, "the call failed: "+res.Outcome.String())
	}
	return w.foldPatch(ctx, r, &plan, name, page, res.Patch, baseline.Exists(tree), newer)
}

// foldPatch applies a step's patch to the page under the rules of a fold
// and writes the revision with the run's progress in one transaction.
func (w *Worker) foldPatch(ctx context.Context, r *store.BaselineRun, plan *runPlan, name string, page store.Page, patch inference.Patch, exists func(string) bool, newer map[string]string) error {
	// A code-backed statement the overview names is struck from its patch,
	// not the patch refused: nearly every statement rests on code, and a
	// refusal would throw away the summary over a confirmation.
	patch, struck := strikeCode(patch, newer)
	if len(struck) > 0 {
		w.log.InfoContext(ctx, "baseline: "+name+": left alone, resting on code: "+strings.Join(struck, ", "))
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
	statements, reason := attach(page.Statements, applied.Statements, runSource(*r, *plan), next, diagramCaps(w.limits))
	if reason != "" {
		return w.skipPart(ctx, r, plan, name, "patch refused: "+reason)
	}
	for i := range statements {
		if applied.Checked[i] {
			statements[i].Checked = r.Commit
		}
	}
	summary := page.Summary
	if applied.Summary != nil {
		summary = lang.SwissSS(strings.TrimSpace(*applied.Summary))
	}
	chars, capReason := check(summary, statements, w.limits.RepoMaxStatements, w.limits.RepoMaxChars, diagramsAllowed(w.limits.MaxDiagrams, page.Statements))
	if capReason != "" {
		w.log.WarnContext(ctx, fmt.Sprintf("baseline: %s rewrite rejected (%s), page unchanged", name, capReason))
		return w.endReading(ctx, r, plan, "the page reached its ceiling at "+name)
	}
	if summary != page.Summary {
		plan.SummaryChanged = true
	}
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
		return errStop
	}
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
	skip, err := w.projectStands(ctx, r, plan)
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
	projectPage, _, err := w.store.CurrentPage(ctx, store.ScopeProject, r.ProjectKey, "")
	if err != nil {
		w.log.WarnContext(ctx, "baseline: project page read failed, drain paused", logging.Err(err))
		return errStop
	}
	repos, err := w.base.Store.RepoSummaries(ctx, r.ProjectKey)
	if err != nil {
		w.log.WarnContext(ctx, "baseline: project repositories read failed, drain paused", logging.Err(err))
		return errStop
	}
	res := w.base.Describer.Describe(ctx, renderProject(*r, repoPage, projectPage, otherRepos(repos, r.Slug), slugs(repos), w.limits))
	okCall, err := w.call(ctx, r, store.PurposeProject, res.Call)
	if err != nil {
		return err
	}
	if !okCall {
		return w.skipProject(ctx, r, plan, "the call failed: "+res.Outcome.String())
	}
	if res.Description == nil {
		// No change carries the map forward; but every project has one, so
		// a page without one yet is not left as it is.
		if projectPage.Map == nil {
			return w.skipProject(ctx, r, plan, "no change asked of a project without a map")
		}
		return w.finish(ctx, r, plan)
	}
	rev, refused, err := w.projectRevision(ctx, r, plan, projectPage, res.Description, slugs(repos))
	if err != nil {
		return err
	}
	if refused != "" {
		return w.skipProject(ctx, r, plan, "rewrite refused: "+refused)
	}
	if rev == nil {
		return w.finish(ctx, r, plan)
	}
	// The project revision and the run's end in one transaction: a crash
	// between them would pay for the project call again and write it twice.
	if err := w.markDone(ctx, r, plan); err != nil {
		return err
	}
	n, ok, err := w.base.Store.ApplyBaselineStep(ctx, *rev, *r)
	if err != nil {
		w.log.WarnContext(ctx, "baseline: project revision write failed, drain paused", logging.Err(err))
		return errStop
	}
	if ok {
		w.log.InfoContext(ctx, fmt.Sprintf("baseline: project description revision %d (%d statements, %d chars)", n, len(rev.Statements), rev.Chars))
	}
	return nil
}

// projectStands reports whether a run may end without its project step: a
// reconcile whose summary stands, of a project that has its map. A project
// without one yet (described before maps existed) is folded as if the
// summary had changed, so its map gets drawn.
func (w *Worker) projectStands(ctx context.Context, r *store.BaselineRun, plan runPlan) (bool, error) {
	if r.Mode != store.RunReconcile || plan.SummaryChanged {
		return false, nil
	}
	page, _, err := w.store.CurrentPage(ctx, store.ScopeProject, r.ProjectKey, "")
	if err != nil {
		w.log.WarnContext(ctx, "baseline: project page read failed, drain paused", logging.Err(err))
		return false, errStop
	}
	return page.Map != nil, nil
}

// projectRevision is the project description a run's last step returned,
// held to the same rules as a note's fold of it, its map to the map's
// (slugs are the project's source repositories); nil when it is refused or
// identical, the map included.
func (w *Worker) projectRevision(ctx context.Context, r *store.BaselineRun, plan runPlan, page store.Page, d *inference.Description, slugs []string) (*store.NewRevision, string, error) {
	next, err := w.store.NextStatementID(ctx, store.ScopeProject, r.ProjectKey, "")
	if err != nil {
		w.log.WarnContext(ctx, "baseline: statement id read failed, drain paused", logging.Err(err))
		return nil, "", errStop
	}
	caps := diagramCaps(w.limits)
	statements, reason := attach(page.Statements, d.Statements, runSource(*r, plan), next, caps)
	summary := lang.SwissSS(strings.TrimSpace(d.Summary))
	chars, capReason := check(summary, statements, w.limits.ProjectMaxStatements, w.limits.ProjectMaxChars, diagramsAllowed(w.limits.MaxDiagrams, page.Statements))
	if reason == "" {
		reason = capReason
	}
	// The map returned as it stands is held to the drawing's ceilings, not
	// to today's caps: lowering one must not freeze the project page. Its
	// slugs are checked still, a repository may have left the project.
	mapCaps := caps
	if diagram.Equal(d.Map, page.Map) {
		mapCaps = baseline.DrawCaps
	}
	drawn, mapReason := baseline.CheckMap(d.Map, slugs, mapCaps)
	if reason == "" && mapReason != "" {
		reason = "map: " + mapReason
	}
	if reason != "" {
		return nil, reason, nil
	}
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

func renderDescribe(r store.BaselineRun, page store.Page, l config.Limits, step, steps, left int, part, focus, files string, newer map[string]string) string {
	return strings.NewReplacer(
		"{repo}", r.Slug, "{project}", r.ProjectKey, "{commit}", r.Commit,
		"{step}", fmt.Sprint(step), "{steps}", fmt.Sprint(steps), "{steps_left}", fmt.Sprint(left), "{part}", part, "{focus}", focus,
		"{repo_max_statements}", fmt.Sprint(l.RepoMaxStatements), "{repo_max_chars}", fmt.Sprint(l.RepoMaxChars),
		"{statements}", fmt.Sprint(len(page.Statements)), "{repo_chars}", fmt.Sprint(prose(page.Summary, page.Statements)),
		"{repo_description}", describeSourced(page, newer), "{files}", files, "{diagram_rules}", diagramRules(l),
	).Replace(prompts.Describe)
}

func renderProject(r store.BaselineRun, repoPage, projectPage store.Page, others string, repoSlugs []string, l config.Limits) string {
	return strings.NewReplacer(
		"{repo}", r.Slug, "{project}", r.ProjectKey, "{commit}", r.Commit,
		"{project_max_statements}", fmt.Sprint(l.ProjectMaxStatements), "{project_max_chars}", fmt.Sprint(l.ProjectMaxChars),
		"{project_chars}", fmt.Sprint(prose(projectPage.Summary, projectPage.Statements)),
		"{project_description}", describeTaught(projectPage), "{repo_description}", describe(repoPage), "{other_repos}", others,
		"{project_map}", projectMap(projectPage), "{repo_slugs}", strings.Join(repoSlugs, ", "), "{diagram_rules}", diagramRules(l),
	).Replace(prompts.Project)
}

// projectMap is the current system map as the JSON the model returns, so
// keeping it means repeating it.
func projectMap(p store.Page) string {
	if p.Map == nil {
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
func describeSourced(p store.Page, newer map[string]string) string { return sourced(p, newer, true) }

// describeTaught is describe saying which statements pull requests taught,
// without their keys: the project step needs the fact, and keys shown are
// keys copied into the prose.
func describeTaught(p store.Page) string { return sourced(p, nil, false) }

func sourced(p store.Page, newer map[string]string, keys bool) string {
	if p.RevisionID == 0 {
		return "_None yet._"
	}
	var b strings.Builder
	b.WriteString(p.Summary)
	b.WriteString("\n")
	for _, s := range p.Statements {
		fmt.Fprintf(&b, "\n### [%s] %s: %s\n\n%s\n", s.ID, s.Kind, s.Name, s.Text)
		if len(s.Files) > 0 {
			fmt.Fprintf(&b, "\nFiles: %s\n", strings.Join(s.Files, ", "))
		}
		b.WriteString(diagramLine(s.Diagram))
		var prs []string
		for _, src := range s.Sources {
			if !src.IsBaseline() {
				prs = append(prs, src.PRKey)
			}
		}
		switch {
		case len(prs) > 0 && keys:
			fmt.Fprintf(&b, "\nLearned from pull requests: %s\n", strings.Join(prs, ", "))
		case len(prs) > 0:
			b.WriteString("\nLearned from pull requests.\n")
		}
		switch newer[s.ID] {
		case byNewerPR:
			b.WriteString("\nA pull request newer than the code shown taught this: leave it as it is, do not edit, drop or confirm it.\n")
		case byUnplaced:
			b.WriteString("\nWhether the code shown holds the pull request that taught this is unknown: leave it as it is, do not edit, drop or confirm it.\n")
		case byCode:
			b.WriteString("\nThis rests on code, which the overview does not read: leave it as it is, do not edit, drop or confirm it.\n")
		}
	}
	return b.String()
}
