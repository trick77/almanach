package consolidate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"regexp"
	"runtime/debug"
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
	"github.com/trick77/almanach/internal/markup"
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
	ArrangePage(ctx context.Context, revisionID int64, statements []store.Statement) (bool, error)
	JudgeShape(ctx context.Context, revisionID int64, shape string) (bool, error)
	DocumentedRun(ctx context.Context, key, slug string) (store.BaselineRun, bool, error)
	SourceRepos(ctx context.Context, key string) ([]store.SourceRepo, error)
	IsSourceRepo(ctx context.Context, key, slug string) (bool, error)
	RepoSummaries(ctx context.Context, project string) ([]store.RepoSummary, error)
	Shared(ctx context.Context, key, slug string) ([]store.SharedRepo, error)
	InnerEdges(ctx context.Context, key string) ([]store.Edge, error)
	RepoProblems(ctx context.Context, project, repo string, until time.Time) ([]store.RepoProblem, error)
	DepartedRepos(ctx context.Context, key string) ([]string, error)
	DepartureDue(ctx context.Context, key string) (bool, error)
	SettleDepartures(ctx context.Context, key string, slugs []string) error
	RestyleState(ctx context.Context, key, slug string, e store.Epochs, failedBefore time.Time) (store.RestyleState, error)
	ApplyRestyle(ctx context.Context, r store.BaselineRun, scope string, epoch int, w store.Restyled) (int, bool, error)
	MarkProjectRestyled(ctx context.Context, runID int64, epoch int) error
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
	// ReadFiles reads files at sha, each within FileMaxBytes, one the plan
	// marks Cut as its first FileMaxBytes; a file over the cap not so
	// marked, missing or binary is left out, not an error.
	ReadFiles(ctx context.Context, key, slug, sha string, files []baseline.File) (map[string][]byte, error)
}

// Cataloguer is the catalogue model.
type Cataloguer interface {
	Catalogue(ctx context.Context, prompt string) inference.CatalogueResult
}

// Describer is the model that writes: the patch of a step, the project,
// the repository's map, the project's map asked again alone, and a page's
// restyle.
type Describer interface {
	Patch(ctx context.Context, prompt string) inference.PatchResult
	Describe(ctx context.Context, prompt string) inference.DescriptionResult
	RepoMap(ctx context.Context, prompt string) inference.RepoMapResult
	ProjectMap(ctx context.Context, prompt string) inference.RepoMapResult
	Restyle(ctx context.Context, prompt string, project bool) inference.RestyleResult
}

// Baseline is the runs' part of the worker; without it (the sources off)
// the worker folds notes only.
type Baseline struct {
	Store     RunStore
	Code      Code
	Catalogue Cataloguer
	Describer Describer
	Limits    config.Baseline
	// Briefs writes and pushes the repositories' briefs; nil: none.
	Briefs *Briefs
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
	// Kept is the calls the reading leaves to the steps after it, judged
	// once when the plan is made (keptCalls); nil on a plan from before,
	// which keeps one.
	Kept *int `json:"kept,omitempty"`
	// Docs is what the overview step read, once folded: the run's coverage
	// (ReadBytes) counts it with the subjects, a doc a subject holds too
	// once.
	Docs []baseline.File `json:"docs,omitempty"`
	// Cuts: the plan was made knowing files over the cap are read as their
	// heads, so a cut file in none of its subjects was left out on purpose.
	// TRANSITIONAL(cut-files): Remove when every documented plan carries it.
	Cuts bool `json:"cuts,omitempty"`
	// ProjectMap and RepoMap are a refused map asked again by the next
	// step, the system map's and the repository's: one call a step, so a
	// restart resumes the asking, never pays for what came before.
	ProjectMap *mapRetry `json:"project_map,omitempty"`
	RepoMap    *mapRetry `json:"repo_map,omitempty"`
	// Departure: the run a deactivation queued (store.TriggerDeparture),
	// set by the store with the run. It reads nothing: its one step takes
	// the repository out of the project page (departureStep).
	Departure bool `json:"departure,omitempty"`
	// Restyle: a restyle run (store.RunRestyle), set by the store with the
	// run. It reads no code: its steps restyle the pages, then the map step
	// (restyleStep).
	Restyle *restylePlan `json:"restyle,omitempty"`
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
	// The day's budget, asked before a run starts: one started goes on to
	// its end, since its repository's notes wait for it (NextQueued) and a
	// budget that held it would hold them for the day; so the budget may be
	// overshot by one run's calls, never by a day's. The notes go on
	// whatever it says.
	if r.State == store.RunQueued {
		over, err := w.overBudget(ctx)
		if err != nil {
			w.log.WarnContext(ctx, "baseline: spend read failed, drain paused", logging.Err(err))
			return false
		}
		if over {
			if w.setWait(r.ID, WaitBudget) {
				w.log.InfoContext(ctx, "baseline: the day's budget is spent, runs wait for the next day")
			}
			return false
		}
	}
	w.setBusy(Busy{Kind: KindRun, ProjectKey: r.ProjectKey, RepoSlug: r.Slug, RunID: r.ID})
	defer w.clearBusy()
	loaded := r
	err := w.step(ctx, &r)
	if errors.Is(err, errPanicked) {
		// What the step set before it panicked was never committed by
		// it: the failure is written on the run as it was loaded.
		r = loaded
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

// errPanicked marks a step that panicked.
var errPanicked = errors.New("panicked")

// step runs the run's next step. A panic is the run's failure: uncaught it
// ends the process, and every restart meets the same step again.
func (w *Worker) step(ctx context.Context, r *store.BaselineRun) (err error) {
	defer func() {
		if p := recover(); p != nil {
			w.log.ErrorContext(ctx, fmt.Sprintf("baseline: step panicked: %v", p), slog.String("stack", string(debug.Stack())))
			err = fmt.Errorf("step %w: %v", errPanicked, p)
		}
	}()
	switch r.State {
	case store.RunQueued:
		return w.startRun(ctx, r)
	case store.RunCataloguing:
		return w.catalogue(ctx, r)
	case store.RunDescribing:
		return w.describeStep(ctx, r)
	case store.RunProject:
		return w.projectStep(ctx, r)
	default:
		return fmt.Errorf("a run in state %q is not open", r.State)
	}
}

// overBudget reports whether the worker's own calls of the UTC day have
// cost the daily budget (BACKEND_DAILY_BUDGET_USD) or more; never with no
// budget set. Unpriced calls count nothing: a budget needs a priced
// gateway, the config says so.
func (w *Worker) overBudget(ctx context.Context) (bool, error) {
	budget := w.base.Limits.DailyBudgetNanoUSD
	if budget <= 0 {
		return false, nil
	}
	spent, _, err := w.store.Spent(ctx, store.DayStart(w.now()))
	if err != nil {
		return false, err
	}
	return store.OverBudget(spent, budget), nil
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
		// once, into the same wait. Any other refusal holds longer each
		// time: the step before the save was a paid call, so the refusal
		// counts toward maxFoldAttempts (runWritable).
		w.holdStore(ctx, err)
		if w.saveRun != r.ID {
			w.saveRun, w.savesRefused = r.ID, 0
		}
		w.savesRefused++
		return errStop
	}
	w.saveRun, w.savesRefused = 0, 0
	w.writeOK()
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
// (prev). A write the file refused (store.IsRefused: a full disk) is
// counted the same, under the write hold, longer each time (holdWrite):
// the step's call was paid, and without the count every hold's end would
// pay it again. Anything else (a constraint) is the step's content and
// would fail the same way at every retry: it fails the run at once.
func (w *Worker) stepBusy(ctx context.Context, r *store.BaselineRun, prev store.BaselineRun, name string, err error) error {
	busy := store.IsBusy(err)
	if !busy && !store.IsRefused(err) {
		*r = prev
		return fmt.Errorf("%s step not written: %w", name, err)
	}
	why := "database busy"
	if !busy {
		why = "write refused"
	}
	w.holdStore(ctx, err)
	if w.busyRun != r.ID {
		w.busyRun, w.busyWrites = r.ID, 0
	}
	w.busyWrites++
	if w.busyWrites >= maxFoldAttempts {
		n := w.busyWrites
		w.busyRun, w.busyWrites = 0, 0
		*r = prev
		return fmt.Errorf("%s step given up after %d busy or refused database writes: %w", name, n, err)
	}
	w.log.WarnContext(ctx, fmt.Sprintf("baseline: %s step not written: %s, attempt %d of %d, kept for after the hold",
		name, why, w.busyWrites, maxFoldAttempts), logging.Err(err))
	return errStop
}

// runWritable is the write a run held by a refused write takes before its
// step's paid call: its progress saved as it stands. Refused again, the
// worker holds again and pays nothing. A run whose saves were refused
// maxFoldAttempts times in a row, most after a paid step, fails instead of
// paying for the step once more (its own re-save may fit where the step's
// does not).
func (w *Worker) runWritable(ctx context.Context, r store.BaselineRun) bool {
	if w.saveRun == r.ID && w.savesRefused >= maxFoldAttempts {
		n := w.savesRefused
		w.saveRun, w.savesRefused = 0, 0
		w.failRun(ctx, r, fmt.Sprintf("progress not saved after %d busy or refused database writes", n))
		return false
	}
	if w.writeBackoff == 0 {
		return true
	}
	if _, err := w.base.Store.SaveRun(ctx, r); err != nil {
		w.holdStore(ctx, err)
		return false
	}
	return true
}

// stepWritten forgets the busy writes, and the write hold's backoff, once
// a step's write got through.
func (w *Worker) stepWritten() {
	w.busyRun, w.busyWrites = 0, 0
	w.writeOK()
}

// failRun ends a run with its reason on the row.
func (w *Worker) failRun(ctx context.Context, r store.BaselineRun, reason string) {
	now := w.now().UTC()
	r.State, r.Error, r.FinishedAt = store.RunFailed, reason, &now
	w.log.WarnContext(ctx, "baseline: run failed: "+reason)
	_ = w.save(ctx, r)
}

// startRun pins the clone's head and decides what the run reads. No call.
// A departure run has no clone to read: it goes to its project step at the
// commit the store queued it with.
func (w *Worker) startRun(ctx context.Context, r *store.BaselineRun) error {
	plan, err := decodePlan(r.Plan)
	if err != nil {
		return fmt.Errorf("the run's plan: %w", err)
	}
	if plan.Departure {
		now := w.now().UTC()
		r.StartedAt, r.State, r.Error = &now, store.RunProject, ""
		w.log.InfoContext(ctx, "baseline: departure run started: the repository leaves the project page")
		return w.save(ctx, *r)
	}
	if plan.Restyle != nil {
		return w.startRestyle(ctx, r)
	}
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
	readsAfresh(r)
	w.log.InfoContext(ctx, fmt.Sprintf("baseline: run started at %s (%s, %s)", gitrepo.ShortSHA(head), r.Mode, r.Trigger))
	return w.save(ctx, *r)
}

// readsAfresh stamps a full run with the repository prompts' epoch it reads
// under (prompts.RepoEpoch), when it starts reading: done, it puts its
// repository's page at that epoch (the store's), every step having had the
// page in its prompt. Stamped at the start, never the end: a run a deploy
// that bumps the epoch cut in two read its first parts under the old rules,
// and stays due a restyle. The scope's own epoch, never the newer of the
// two: a page stamped past its scope's would stand through that scope's
// next bump.
func readsAfresh(r *store.BaselineRun) {
	if r.Mode == store.RunFull && r.Epoch == 0 {
		r.Epoch = prompts.RepoEpoch
	}
}

// call records one model call in the ledger and classes its failure. ok
// false with errStop is a gateway hold; ok false and nil is a failure of
// this call, which the step handles.
func (w *Worker) call(ctx context.Context, r *store.BaselineRun, purpose string, res inference.Call) (bool, error) {
	runID := r.ID
	var step *int
	if purpose == store.PurposeOverview || purpose == store.PurposeSubject || purpose == store.PurposeRestyle {
		i := r.Step
		step = &i
	}
	if _, err := w.store.InsertLLMCall(ctx, store.LLMCall{
		ProjectKey: r.ProjectKey, RepoSlug: r.Slug, SourceKind: store.FromBaseline, SourceCommit: r.Commit, Purpose: purpose,
		BaselineRunID: &runID, Step: step, Outcome: res.Outcome.String(), Model: res.Cost.Model, InputTokens: res.Cost.InputTokens,
		CachedTokens: res.Cost.CachedTokens, OutputTokens: res.Cost.OutputTokens, CostNanoUSD: res.Cost.NanoUSD, CallID: res.Cost.CallID,
	}); err != nil {
		// No hold here: the gateway's answer below would clear it. On a
		// full disk the step's write or the save after it holds.
		w.log.ErrorContext(ctx, "baseline: call row failed", logging.Err(err))
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

// keptCalls is the calls a run that reads a part leaves to the steps after
// its reading, judged once when the plan is made: one for the repository
// map, always (the reading rewrites the text the map stands on; the parts
// once spent it on every run of a large repository, and loom had no map),
// one for the project's unless that step stands (a project of one, a quiet
// reconcile). A project step that comes due during the reading (a summary
// rewritten) competes for what is left. Under four calls a run keeps what
// it can: best effort.
func (w *Worker) keptCalls(ctx context.Context, r *store.BaselineRun, plan runPlan) (int, error) {
	kept := 1
	project, err := w.projectPage(ctx, r.ProjectKey)
	if err != nil {
		return 0, err
	}
	stands, _, err := w.projectStands(ctx, r, plan, project)
	if err != nil {
		return 0, err
	}
	if !stands {
		kept++
	}
	// The catalogue and one part come first however few the calls.
	return min(kept, max(w.base.Limits.MaxCalls-2, 0)), nil
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
	// What a full run could read, kept on the row: what it read is a share
	// of it, and a reader sees how much of the code the page rests on. A
	// reconcile reads a delta of a tree it never measures: none.
	r.ReadableBytes = 0
	for _, f := range files {
		r.ReadableBytes += f.Size
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
	plan := runPlan{Plan: p, Overview: true, CommittedAt: at, Cuts: true}
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
		// The page's coverage is the last full read's: carried, so the
		// source page keeps saying it past the next reconcile.
		r.ReadableBytes, r.ReadBytes = doc.ReadableBytes, doc.ReadBytes
	}
	diff, derr := w.base.Code.Diff(ctx, r.ProjectKey, r.Slug, r.BaseCommit, r.Commit)
	if errors.Is(derr, ErrCloneBusy) {
		return runPlan{}, false, derr
	}
	changes := baseline.Sift(diff)
	if !ok || err != nil || derr != nil || baseline.FullInstead(old.Plan, changes) {
		w.log.InfoContext(ctx, "baseline: reconcile reads the repository afresh")
		r.Mode = store.RunFull
		readsAfresh(r)
		return runPlan{}, false, w.save(ctx, *r)
	}
	_, unplaced := baseline.Touched(old.Plan, changes)
	merged := baseline.MergePlan(old.Plan, nil, files, w.maxSubjects())
	// A file that grew out of its subject's budget left the plan in the
	// merge, and a file the last run could not place is in none: both are
	// placed like new ones, not lost until they change again.
	unplaced = append(unplaced, evicted(old.Plan, merged, files)...)
	unplaced = append(unplaced, stillReadable(old.Unplaced, files, unplaced)...)
	if !old.Cuts {
		unplaced = append(unplaced, newlyReadable(old, merged, files, unplaced)...)
	}
	plan := runPlan{Cuts: true}
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
	if plan.steps() > 0 {
		kept, err := w.keptCalls(ctx, r, plan)
		if err != nil {
			return err
		}
		plan.Kept = &kept
	}
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
			page, err := w.projectPage(ctx, r.ProjectKey)
			if err != nil {
				return err
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
	kept := 1
	if plan.Kept != nil {
		kept = *plan.Kept
	}
	if r.Calls >= w.base.Limits.MaxCalls-kept {
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
	// The shared repositories before the files: a store that does not answer costs no
	// read of the clone. Once a run.
	if w.libsRun != r.ID {
		libs, err := w.base.Store.Shared(ctx, r.ProjectKey, r.Slug)
		if err != nil {
			w.log.WarnContext(ctx, "baseline: shared repositories read failed, drain paused", logging.Err(err))
			return errStop
		}
		repos, err := w.repoSummaries(ctx, r)
		if err != nil {
			return err
		}
		w.libsRun, w.libs, w.siblings = r.ID, sharedRepos(libs, false), siblingInterfaces(repos, r.Slug)
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
	prompt := renderDescribe(*r, page, w.limits, i+1, r.Steps, part, focus, renderFiles(read, bodies), renderProblems(problems), w.libs, w.siblings, newer)
	res := w.base.Describer.Patch(ctx, prompt)
	ok, err := w.call(ctx, r, purpose, res.Call)
	if err != nil {
		return err
	}
	r.Bytes += size
	if !ok {
		return w.skipPart(ctx, r, &plan, baseline.SkipFailed, "the call failed: "+res.Outcome.String())
	}
	if overview {
		plan.Docs = read
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
		w.log.WarnContext(ctx, "baseline: revision write failed", logging.Err(err))
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
	// projectMapAt heads the system map's log lines, drawn or carried.
	projectMapAt = "baseline: " + projectLabel + " map"
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

// newlyReadable is the cut files a documented plan from before cut files
// (no runPlan.Cuts) knows nothing of: in no subject, not among its
// unplaced, not placed now. A file over the cap was unreadable before it
// was read as its head (baseline.File.Cut), so such a plan has those files
// in neither list; unchanged, they are in no diff, and a reconcile would
// never place them. Cut files only: a whole file a full run's model left
// out of every subject it left out on purpose. Asked of the first
// reconcile after only: its plan carries Cuts, and a cut file left out
// then is like any other.
// TRANSITIONAL(cut-files): Remove when every documented plan carries Cuts.
func newlyReadable(old runPlan, merged baseline.Plan, files, already []baseline.File) []baseline.File {
	known := map[string]bool{}
	for _, s := range old.Subjects {
		for _, f := range s.Files {
			known[f.Path] = true
		}
	}
	for _, s := range merged.Subjects {
		for _, f := range s.Files {
			known[f.Path] = true
		}
	}
	for _, p := range old.Unplaced {
		known[p] = true
	}
	for _, f := range already {
		known[f.Path] = true
	}
	var out []baseline.File
	for _, f := range files {
		if f.Cut && !known[f.Path] {
			out = append(out, f)
		}
	}
	return out
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
	if plan.Departure {
		return w.departureStep(ctx, r, plan)
	}
	if plan.Restyle != nil && !plan.ProjectDone {
		return w.restyleStep(ctx, r, plan)
	}
	if plan.ProjectDone {
		return w.repoMapStep(ctx, r, plan)
	}
	if plan.ProjectMap != nil {
		return w.projectMapStep(ctx, r, plan)
	}
	projectPage, err := w.projectPage(ctx, r.ProjectKey)
	if err != nil {
		return err
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
	libs, err := w.base.Store.Shared(ctx, r.ProjectKey, "")
	if err != nil {
		w.log.WarnContext(ctx, "baseline: shared repositories read failed, drain paused", logging.Err(err))
		return errStop
	}
	departed, err := w.departed(ctx, r.ProjectKey)
	if err != nil {
		return err
	}
	inner, err := w.innerEdges(ctx, r.ProjectKey)
	if err != nil {
		return err
	}
	res := w.base.Describer.Describe(ctx, renderProject(*r, repoPage, projectPage, otherRepos(repos, r.Slug), otherContracts(repos, r.Slug),
		libs, slugs(repos), departed, inner, w.limits, nil))
	okCall, err := w.call(ctx, r, store.PurposeProject, res.Call)
	if err != nil {
		return err
	}
	if !okCall {
		return w.skipProject(ctx, r, plan, baseline.SkipFailed, "the call failed: "+res.Outcome.String())
	}
	// The call answered with the departed in its prompt: a departure still
	// due (its run's call failed) is taken out here. A retried write asks
	// with the same prompt.
	w.settle(ctx, r.ProjectKey, departed)
	shape := w.judgedShape(ctx, projectLabel, projectPage, res.Shape)
	var rev *store.NewRevision
	if res.Description == nil {
		// No change carries the text and the map forward; a project with no
		// page yet has nothing to carry, and is asked again next time.
		if projectPage.RevisionID == 0 {
			return w.skipProject(ctx, r, plan, baseline.SkipNoPage, "no change asked of a project without a page")
		}
		// A page turned into a collection needs the collection's summary: the
		// stored one is a system's, which the shape's rules forbid. Refused,
		// the page stands and the next reconcile folds the project again.
		if why := collectionWithout(projectPage, shape, ""); why != "" {
			return w.skipProject(ctx, r, plan, baseline.SkipRefused, why)
		}
		rev = w.carriedRevision(ctx, r, projectPage, slugs(repos), shape)
	} else {
		var refused string
		rev, plan.ProjectMap, refused, err = w.projectRevision(ctx, r, plan, projectPage, res.Description, slugs(repos), shape)
		if err != nil {
			return err
		}
		if refused != "" {
			return w.skipProject(ctx, r, plan, baseline.SkipRefused, "rewrite refused: "+refused)
		}
	}
	// The project page is laid out by the project step's outline, as a
	// repository's by its map step: in the revision the step writes, or in
	// place on the page as it stands.
	at := "baseline: " + projectLabel
	if rev != nil {
		rev.Statements = w.applyOutline(ctx, at, rev.Statements, res.Outline)
	} else if projectPage.RevisionID != 0 {
		w.arrangePage(ctx, at, &projectPage, res.Outline)
		w.judgeInPlace(ctx, projectPage, shape)
	}
	// A refused map leaves the plan its retry: the description written
	// first, the map asked again by the steps after.
	if rev == nil {
		err = w.projectOver(ctx, r, plan)
	} else {
		err = w.writeProject(ctx, r, plan, rev)
	}
	if err == nil {
		w.projectRestyled(ctx, r, projectPage.RevisionID == 0)
	}
	return err
}

// projectRestyled puts the project page at the prompts' epoch when the
// project step that just answered had the whole page in its prompt under
// today's rules: a full run's, or the one that wrote the page first. A
// reconcile's step folds one repository's change and restyles nothing.
func (w *Worker) projectRestyled(ctx context.Context, r *store.BaselineRun, first bool) {
	// The project step's prompt is rendered when it runs: whenever the run
	// started reading, its project call had today's project rules.
	if !first && r.Mode != store.RunFull {
		return
	}
	// By the run: one gone with its repository, or over, wrote nothing to
	// mark (writeProject and projectOver report no write of a run over).
	if err := w.base.Store.MarkProjectRestyled(ctx, r.ID, prompts.ProjectEpoch); err != nil {
		w.log.WarnContext(ctx, "baseline: project restyle epoch not written, a restyle takes the page later", logging.Err(err))
	}
}

// writeProject writes the project revision and the step's end in one
// transaction: a crash between them would pay for the project call again
// and write it twice. A map still to ask again keeps the step open.
func (w *Worker) writeProject(ctx context.Context, r *store.BaselineRun, plan runPlan, rev *store.NewRevision) error {
	prev := *r
	plan.ProjectDone = plan.ProjectMap == nil
	if err := encodeInto(r, plan); err != nil {
		return err
	}
	n, ok, err := w.base.Store.ApplyBaselineStep(ctx, *rev, *r)
	if err != nil {
		w.log.WarnContext(ctx, "baseline: project revision write failed", logging.Err(err))
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
// The one place a run decides on the project: a project of one has none to
// write (no call, nothing skipped; its repository's page is the project's),
// and a project with no page yet (its second repository just came) is
// folded whatever a reconcile would skip.
func (w *Worker) projectStands(ctx context.Context, r *store.BaselineRun, plan runPlan, page store.Page) (bool, []store.RepoSummary, error) {
	n, err := w.store.SourceRepoCount(ctx, r.ProjectKey)
	if err != nil {
		w.log.WarnContext(ctx, "baseline: project source read failed, drain paused", logging.Err(err))
		return false, nil, errStop
	}
	if store.Single(n) {
		w.log.InfoContext(ctx, "baseline: project step left out, a project of one")
		return true, nil, nil
	}
	if page.RevisionID == 0 || r.Mode != store.RunReconcile || plan.SummaryChanged {
		return false, nil, nil
	}
	// A repository added, or activated again, after the page was written:
	// the page has never seen it, or took it out (a project that grows past
	// one, or grows back to more after standing as one with its old page
	// kept), whatever a reconcile reads. A deactivated one is no part of it.
	rows, err := w.base.Store.SourceRepos(ctx, r.ProjectKey)
	if err != nil {
		w.log.WarnContext(ctx, "baseline: project repositories read failed, drain paused", logging.Err(err))
		return false, nil, errStop
	}
	for _, row := range rows {
		if !row.Active() {
			continue
		}
		if row.CreatedAt.After(page.UpdatedAt) || row.ReactivatedAt != nil && row.ReactivatedAt.After(page.UpdatedAt) {
			return false, nil, nil
		}
	}
	// A deactivation no departure run took out of the page (it failed): the
	// fold does, its prompt naming the departed.
	due, err := w.base.Store.DepartureDue(ctx, r.ProjectKey)
	if err != nil {
		w.log.WarnContext(ctx, "baseline: departure read failed, drain paused", logging.Err(err))
		return false, nil, errStop
	}
	if due {
		return false, nil, nil
	}
	// A collection draws no map: its NULL is no map due.
	if page.Map != nil || page.Collection() {
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
// where a map was due and there is none) under the shape judged; a
// collection's page without its statements and map; nil when that is the
// page as it stands.
func (w *Worker) carriedRevision(ctx context.Context, r *store.BaselineRun, page store.Page, slugs []string, shape string) *store.NewRevision {
	if shape == store.ShapeCollection {
		return w.collectionRevision(ctx, r, page, page.Summary, nil)
	}
	// Carried as the page holds it: a collection's none, so a system it
	// turns into is due its map, never the sentinel.
	var m *diagram.Diagram
	if diagram.MapFits(len(slugs)) {
		m = w.carriedMap(ctx, page, slugs)
	}
	rev := carriedPage(r, page, page.Statements, m)
	if rev == nil && shapeMoves(page, shape) {
		rev = pageRevision(r, page, page.Statements, m)
	}
	if rev != nil {
		rev.Shape = shape
	}
	return rev
}

// shapeMoves reports whether a judged shape changes what the page shows:
// another than the page's, but a system judged on a page written as one
// before shapes (its NULL), which changes nothing but the label and is set
// in place (judgeInPlace).
func shapeMoves(page store.Page, shape string) bool {
	return store.ShapeMoves(page.Shape, shape)
}

// judgeInPlace sets a first judgment that moves nothing (shapeMoves) on the
// page's current revision; logged when it fails, the next step judges again.
func (w *Worker) judgeInPlace(ctx context.Context, page store.Page, shape string) {
	if page.RevisionID == 0 || page.Shape == shape || shapeMoves(page, shape) {
		return
	}
	if _, err := w.base.Store.JudgeShape(ctx, page.RevisionID, shape); err != nil {
		w.log.WarnContext(ctx, "baseline: "+projectLabel+": shape not set, judged again by the next step", logging.Err(err))
	}
}

// collectionWithout is why an answer turning the page into a collection is
// refused: it brings no summary of its own (summary, as the answer's prose,
// "" for none), and the stored one was written for a system, or there is
// none. "" when the answer stands.
func collectionWithout(page store.Page, shape, summary string) string {
	if shape != store.ShapeCollection || strings.TrimSpace(summary) != "" {
		return ""
	}
	if page.Collection() && strings.TrimSpace(page.Summary) != "" {
		return ""
	}
	return "judged a collection without a summary of its own"
}

// judgedShape is the shape a project call answered, logged where it
// changes the page's: the page's own when the answer named none (off the
// schema's enum), a system when neither did. A change is logged at warn
// with the model's reason, since one answer can empty a page: the old page
// stays in the revisions.
func (w *Worker) judgedShape(ctx context.Context, label string, page store.Page, s inference.Shape) string {
	shape := s.Kind
	if shape == "" {
		shape = page.Shape
	}
	if shape == "" {
		shape = store.ShapeSystem
	}
	switch {
	case page.RevisionID == 0:
		w.log.InfoContext(ctx, "baseline: "+label+": judged a "+shape, slog.String("reason", s.Reason))
	case page.Shape == "":
		w.log.InfoContext(ctx, "baseline: "+label+": judged a "+shape+" for the first time", slog.String("reason", s.Reason),
			slog.Int("statements", len(page.Statements)))
	case shape != page.Shape:
		w.log.WarnContext(ctx, "baseline: "+label+": judged a "+shape+", was a "+page.Shape, slog.String("reason", s.Reason),
			slog.Int("statements", len(page.Statements)))
	}
	return shape
}

// collectionRevision is a collection's page: the summary alone, every
// statement off (logged, the old page stays in the revisions), no map, the
// shape set; nil when that is the page as it stands. proposed is what the
// answer offered beside the summary, left out and logged.
func (w *Worker) collectionRevision(ctx context.Context, r *store.BaselineRun, page store.Page, summary string, proposed []inference.Statement) *store.NewRevision {
	at := "baseline: " + projectLabel
	if len(proposed) > 0 {
		w.log.WarnContext(ctx, at+": statements of a collection left out", slog.Int("statements", len(proposed)))
	}
	if len(page.Statements) > 0 {
		w.log.WarnContext(ctx, at+": a collection holds no statements, dropped", slog.Int("statements", len(page.Statements)),
			slog.Any("ids", statementIDs(page.Statements)))
	}
	if page.RevisionID != 0 && page.Collection() && summary == page.Summary && len(page.Statements) == 0 && page.Map == nil {
		return nil
	}
	runID := r.ID
	return &store.NewRevision{
		Scope: store.ScopeProject, ProjectKey: r.ProjectKey, Summary: summary, Statements: []store.Statement{},
		Chars: prose(summary, nil), SourceKind: store.FromBaseline, SourceCommit: r.Commit, BaselineRunID: &runID,
		Shape: store.ShapeCollection,
	}
}

func statementIDs(statements []store.Statement) []string {
	out := make([]string, 0, len(statements))
	for _, s := range statements {
		out = append(out, s.ID)
	}
	return out
}

// innerEdges is the build dependencies between the project's own
// repositories, for the project call's judgment of its shape.
func (w *Worker) innerEdges(ctx context.Context, key string) ([]store.Edge, error) {
	edges, err := w.base.Store.InnerEdges(ctx, key)
	if err != nil {
		w.log.WarnContext(ctx, "baseline: project dependencies read failed, drain paused", logging.Err(err))
		return nil, errStop
	}
	return edges, nil
}

// projectPage is the project's current page as a baseline run reads it. A
// system map stored before the tags (layer, stage) reads as none: it was
// drawn with every build, monitoring and stage box the filter now takes
// out. So every rule for a missing map holds for it, in one place: a quiet
// reconcile runs the project step, the prompt shows no map, the redraw is
// written (one revision, the tags its change where the drawing is the
// same), and a null or refused answer leaves the sentinel, never a map
// asked for again on every reconcile.
func (w *Worker) projectPage(ctx context.Context, key string) (store.Page, error) {
	page, _, err := w.store.CurrentPage(ctx, store.ScopeProject, key, "")
	if err != nil {
		w.log.WarnContext(ctx, "baseline: project page read failed, drain paused", logging.Err(err))
		return store.Page{}, errStop
	}
	// TRANSITIONAL(untagged-map): Remove when every stored map carries tags.
	if diagram.Untagged(page.Map) {
		page.Map = nil
	}
	return page, nil
}

// mapRevision is the page's text with the map m: what a map changes alone
// writes; nil when m is the page's map.
func mapRevision(r *store.BaselineRun, page store.Page, m *diagram.Diagram) *store.NewRevision {
	return carriedPage(r, page, page.Statements, m)
}

// carriedPage is the page's summary with statements and the map m, the
// run's revision of it: what a map alone, or a departure's code, writes;
// nil when that is the page as it stands.
func carriedPage(r *store.BaselineRun, page store.Page, statements []store.Statement, m *diagram.Diagram) *store.NewRevision {
	if sameStatements(page.Statements, statements) && diagram.Equal(m, page.Map) {
		return nil
	}
	return pageRevision(r, page, statements, m)
}

// pageRevision is the page's summary with statements and the map m as the
// run's revision, its shape carried.
func pageRevision(r *store.BaselineRun, page store.Page, statements []store.Statement, m *diagram.Diagram) *store.NewRevision {
	runID := r.ID
	return &store.NewRevision{
		Scope: store.ScopeProject, ProjectKey: r.ProjectKey, Summary: page.Summary, Statements: statements,
		Chars: prose(page.Summary, statements), SourceKind: store.FromBaseline, SourceCommit: r.Commit, BaselineRunID: &runID, Map: m,
	}
}

// projectRevision is the project description a run's last step returned,
// held to the same rules as a note's fold of it; nil when it is refused or
// identical, the map included. Its statements are a patch, as a subject
// step's: edits by id, a statement not named kept, removals in drop, so an
// answer that names none no longer empties the page (an eval's second
// repository wiped the first one's eleven so). The project has no one tree,
// so no file decides a drop: the model's word decides it (projectDrops);
// an edit may change any statement's files. A collection's page is its
// summary alone (collectionRevision), refused without a summary of its
// own. The page has no ceiling. The map never sinks the description: see
// revisionMap, whose retry, when its map was refused, comes back for the
// plan. slugs are the project's source repositories.
func (w *Worker) projectRevision(ctx context.Context, r *store.BaselineRun, plan runPlan, page store.Page, d *inference.Description,
	slugs []string, shape string) (*store.NewRevision, *mapRetry, string, error) {
	summary := proseSummary(d.Summary)
	if why := collectionWithout(page, shape, summary); why != "" {
		return nil, nil, why, nil
	}
	// An empty summary costs only itself: the stored one stays, the rest of
	// the answer is taken; a first page takes its statements without one.
	if strings.TrimSpace(summary) == "" {
		w.log.WarnContext(ctx, "baseline: "+projectLabel+": empty summary left out, the stored one kept")
		summary = page.Summary
	}
	if shape == store.ShapeCollection {
		return w.collectionRevision(ctx, r, page, summary, append(slices.Clone(d.Statements), d.Added...)), nil, "", nil
	}
	next, err := w.store.NextStatementID(ctx, store.ScopeProject, r.ProjectKey, "")
	if err != nil {
		w.log.WarnContext(ctx, "baseline: statement id read failed, drain paused", logging.Err(err))
		return nil, nil, "", errStop
	}
	answer := *d
	answer.Statements, answer.Added = w.asNew(ctx, "baseline: "+projectLabel, page.Statements, d.Statements, d.Added)
	d = &answer
	current := w.projectDrops(ctx, "baseline: "+projectLabel, page.Statements, d.Drop, d.Statements)
	applied := baseline.ApplyPatch(current, inference.Patch{Statements: d.Statements, Added: d.Added}, noTree, validFor(store.ScopeProject))
	if len(applied.Unknown) > 0 {
		w.log.WarnContext(ctx, "baseline: "+projectLabel+": unknown ids left out", slog.Any("ids", applied.Unknown))
	}
	// ApplyPatch left out what attach would refuse, so a reason here is a
	// bug, and costs the step.
	statements, leftOut, reason := attach(store.ScopeProject, page.Statements, applied.Statements, runSource(*r, plan), next, limitsOf(w.limits))
	if reason != "" {
		return nil, nil, reason, nil
	}
	leftOut.badEdits = append(applied.BadEdits, leftOut.badEdits...)
	leftOut.badNew, leftOut.badDrops = applied.BadNew, applied.BadDrops
	// No ceiling: a description is documentation, never cut to a size
	// (user, 2026-11-01).
	chars := prose(summary, statements)
	logLeftOut(ctx, w.log, "baseline: "+projectLabel, leftOut)
	drawn, retry, err := w.revisionMap(ctx, r, page, d.Map, slugs)
	if err != nil {
		return nil, nil, "", err
	}
	if summary == page.Summary && sameStatements(page.Statements, statements) && diagram.Equal(drawn, page.Map) && !shapeMoves(page, shape) {
		w.log.InfoContext(ctx, "baseline: project description identical, no revision")
		return nil, retry, "", nil
	}
	runID := r.ID
	return &store.NewRevision{
		Scope: store.ScopeProject, ProjectKey: r.ProjectKey, Summary: summary, Statements: statements, Chars: chars,
		SourceKind: store.FromBaseline, SourceCommit: r.Commit, BaselineRunID: &runID, Map: drawn, Shape: shape,
	}, retry, "", nil
}

// projectDrops is the project page's statements less those an answer
// drops. No tree decides a project drop, so code takes the model's word,
// a statement a pull request taught included: how one repository works
// inside is its page's, where the same note was folded and the tree guards
// it (user, 2026-11-04). The prompts keep a PR-taught statement about the
// wiring unless the descriptions show it wrong. A drop of a statement the
// answer edits too is left out, the edit taken; an unknown id is logged.
// Before ApplyPatch, which would judge a drop's files against noTree.
func (w *Worker) projectDrops(ctx context.Context, at string, page []store.Statement, drop []string, edits []inference.Statement) []store.Statement {
	if len(drop) == 0 {
		return page
	}
	edited := map[string]bool{}
	for _, s := range edits {
		if s.ID != nil {
			edited[*s.ID] = true
		}
	}
	known := map[string]store.Statement{}
	for _, s := range page {
		known[s.ID] = s
	}
	gone := map[string]bool{}
	var taught, unknown, both []string
	for _, id := range drop {
		s, ok := known[id]
		switch {
		case !ok:
			unknown = append(unknown, id)
		case edited[id]:
			both = append(both, id)
		default:
			gone[id] = true
			if baseline.FromPR(s) {
				taught = append(taught, id)
			}
		}
	}
	if len(unknown) > 0 {
		w.log.WarnContext(ctx, at+": drop of unknown ids left out", slog.Any("ids", unknown))
	}
	if len(both) > 0 {
		w.log.WarnContext(ctx, at+": drop of an edited statement left out, the edit taken", slog.Any("ids", both))
	}
	if len(gone) > 0 {
		w.log.InfoContext(ctx, at+": statements dropped", slog.Int("dropped", len(gone)), slog.Any("taught_by_pull_requests", taught))
	}
	return slices.DeleteFunc(slices.Clone(page), func(s store.Statement) bool { return gone[s.ID] })
}

// revisionMap is the map a project revision carries. A project too large for
// one gets none. Otherwise the returned map, its self and repeated edges
// and its departed repositories taken out (projectMapOf), is held to the
// map's rules and caps (not the statement diagrams'); none returned keeps
// the stored map (the sentinel where there is none). One refused keeps
// the map as stored, never the sentinel, and comes back as the retry the
// plan carries: projectMapStep asks for the map alone, a step of its own
// per call, after the description is written. The map never costs the
// description, nor pays for it twice.
func (w *Worker) revisionMap(ctx context.Context, r *store.BaselineRun, page store.Page, proposed *diagram.Diagram,
	slugs []string) (*diagram.Diagram, *mapRetry, error) {
	at := "baseline: " + projectLabel + " map"
	if !diagram.MapFits(len(slugs)) {
		if proposed != nil || page.Map != nil {
			w.log.InfoContext(ctx, fmt.Sprintf("%s: %d repositories, more than a map holds, none drawn", at, len(slugs)))
		}
		return nil, nil, nil
	}
	if proposed == nil {
		w.log.WarnContext(ctx, at+": none returned, the stored one kept")
		return w.carriedMap(ctx, page, slugs), nil, nil
	}
	drawn, why, draft := w.projectMapOf(ctx, proposed, slugs)
	if why == "" {
		return drawn, nil, nil
	}
	room, err := w.mapRetryRoom(ctx, r)
	if err != nil {
		return nil, nil, err
	}
	if !room {
		w.log.WarnContext(ctx, at+": not asked again, no call to spare, the stored one kept")
		return w.asStored(ctx, page, slugs), nil, nil
	}
	return w.asStored(ctx, page, slugs), &mapRetry{Draft: draft, Why: why, Tries: 1}, nil
}

// projectMapOf is a proposed system map repaired and checked: its self and
// repeated edges, what it shows of the build, the deployment, the
// monitoring and the other stages, and its departed repositories out, its
// repository nodes labelled by slug, held to the map's rules and caps. A
// refusal is logged with the map's labels by zone and returns the draft as
// checked, the one the reason counts, for the retry to show.
func (w *Worker) projectMapOf(ctx context.Context, proposed *diagram.Diagram, slugs []string) (*diagram.Diagram, string, *diagram.Diagram) {
	at := projectMapAt
	repaired, dropped := baseline.RepairEdges(proposed)
	if len(dropped) > 0 {
		w.log.InfoContext(ctx, at+": self or repeated edges taken out: "+strings.Join(dropped, ", "))
	}
	runtime := w.runtimeOnly(ctx, at, w.withoutDeparted(ctx, repaired, slugs))
	drawn, why := baseline.CheckNewMap(baseline.LabelRepos(runtime), slugs, baseline.MapCaps)
	if why != "" {
		w.log.WarnContext(ctx, at+" refused ("+why+")", slog.String("nodes", mapLabels(runtime)))
		return nil, why, runtime
	}
	return drawn, "", nil
}

// runtimeOnly is RuntimeOnly on the system map with its log line, after
// the departed repositories are out: a box only they talked to goes too.
func (w *Worker) runtimeOnly(ctx context.Context, at string, m *diagram.Diagram) *diagram.Diagram {
	kept, dropped := baseline.RuntimeOnly(m)
	w.logLeftOut(ctx, at, dropped)
	return kept
}

// repoRuntimeOnly is RepoRuntimeOnly on a repository map with its log line.
func (w *Worker) repoRuntimeOnly(ctx context.Context, at string, m *diagram.Diagram) *diagram.Diagram {
	kept, dropped := baseline.RepoRuntimeOnly(m)
	w.logLeftOut(ctx, at, dropped)
	return kept
}

func (w *Worker) logLeftOut(ctx context.Context, at string, dropped []string) {
	if len(dropped) > 0 {
		w.log.InfoContext(ctx, at+": not run in production, left out: "+strings.Join(dropped, ", "))
	}
}

// asStored is the system map as stored, its departed repositories out:
// what a refused map leaves while it is asked again, or when it is not.
// Never the sentinel where none is stored, so a later run asks again.
func (w *Worker) asStored(ctx context.Context, page store.Page, slugs []string) *diagram.Diagram {
	if page.Map == nil || diagram.IsSentinel(page.Map) {
		return page.Map
	}
	return w.carriedMap(ctx, page, slugs)
}

// mapRetryRoom reports whether the run has a call for a system map asked
// again: one is kept for the repository map only while that map is due.
func (w *Worker) mapRetryRoom(ctx context.Context, r *store.BaselineRun) (bool, error) {
	repo, ok, err := w.store.CurrentPage(ctx, store.ScopeRepository, r.ProjectKey, r.Slug)
	if err != nil {
		w.log.WarnContext(ctx, "baseline: repository page read failed, drain paused", logging.Err(err))
		return false, errStop
	}
	kept := 0
	if ok && !repo.MapStands() {
		kept = 1
	}
	return r.Calls < w.base.Limits.MaxCalls-kept, nil
}

// projectMapStep asks for the system map alone after the one the project
// step returned was refused: one call, one step, the refused draft and the
// rule it broke in the prompt, the description written before it. A map
// that holds is set on the page as a revision of the map alone. Refused
// again, the next step asks once more, at most mapRetries calls; refused on
// every one, the stored map is kept, the sentinel where there is none. Cut
// short (no call to spare, a failed call, null) the map stays as stored, no
// sentinel, so a later run asks again. A hold of the gateway leaves the
// step where it is, resumed after the hold.
func (w *Worker) projectMapStep(ctx context.Context, r *store.BaselineRun, plan runPlan) error {
	at := "baseline: " + projectLabel + " map"
	retry := plan.ProjectMap
	cut := func(why string) error {
		w.log.WarnContext(ctx, at+": not asked again ("+why+"), the stored one kept")
		plan.ProjectMap = nil
		return w.projectOver(ctx, r, plan)
	}
	room, err := w.mapRetryRoom(ctx, r)
	if err != nil {
		return err
	}
	if !room {
		return cut("no call to spare")
	}
	projectPage, err := w.projectPage(ctx, r.ProjectKey)
	if err != nil {
		return err
	}
	repoPage, _, err := w.store.CurrentPage(ctx, store.ScopeRepository, r.ProjectKey, r.Slug)
	if err != nil {
		w.log.WarnContext(ctx, "baseline: repository page read failed, drain paused", logging.Err(err))
		return errStop
	}
	repos, err := w.repoSummaries(ctx, r)
	if err != nil {
		return err
	}
	libs, err := w.base.Store.Shared(ctx, r.ProjectKey, "")
	if err != nil {
		w.log.WarnContext(ctx, "baseline: shared repositories read failed, drain paused", logging.Err(err))
		return errStop
	}
	departed, err := w.departed(ctx, r.ProjectKey)
	if err != nil {
		return err
	}
	inner, err := w.innerEdges(ctx, r.ProjectKey)
	if err != nil {
		return err
	}
	prompt := renderProject(*r, repoPage, projectPage, otherRepos(repos, r.Slug), otherContracts(repos, r.Slug), libs, slugs(repos), departed,
		inner, w.limits, retry)
	res := w.base.Describer.ProjectMap(ctx, prompt)
	ok, err := w.call(ctx, r, store.PurposeProjectMap, res.Call)
	if errors.Is(err, errStop) {
		return err
	}
	if err != nil || !ok {
		r.Attempts = 0
		return cut("the call failed: " + res.Outcome.String())
	}
	if res.Map == nil {
		return cut("none returned")
	}
	m, why, draft := w.projectMapOf(ctx, res.Map, slugs(repos))
	if why != "" {
		if retry.Tries+1 < 1+mapRetries {
			plan.ProjectMap = &mapRetry{Draft: draft, Why: why, Tries: retry.Tries + 1}
			if err := encodeInto(r, plan); err != nil {
				return err
			}
			return w.save(ctx, *r)
		}
		w.log.WarnContext(ctx, at+": still refused, the stored one kept")
		m = w.carriedMap(ctx, projectPage, slugs(repos))
	}
	plan.ProjectMap = nil
	if rev := mapRevision(r, projectPage, m); rev != nil {
		return w.writeProject(ctx, r, plan, rev)
	}
	return w.projectOver(ctx, r, plan)
}

// mapRetry is a refused map asked again, carried by the run's plan from
// step to step: the draft refused last, the rule it broke, and the calls
// made for the map so far, the first included.
type mapRetry struct {
	Draft *diagram.Diagram `json:"draft"`
	Why   string           `json:"why"`
	Tries int              `json:"tries"`
}

// mapRetries is how many more calls a refused map is asked again in the
// same run, the rule it broke and the draft in the prompt.
const mapRetries = 2

// mapLabels is a refused map's node labels by zone, so the log says what
// the model drew: "entry: HTTP-API, SPA; parts: …".
func mapLabels(d *diagram.Diagram) string {
	var zones []string
	by := map[string][]string{}
	for _, n := range d.Nodes {
		if _, ok := by[n.Zone]; !ok {
			zones = append(zones, n.Zone)
		}
		by[n.Zone] = append(by[n.Zone], n.Label)
	}
	out := make([]string, 0, len(zones))
	for _, z := range zones {
		name := z
		if name == "" {
			name = "no zone"
		}
		out = append(out, name+": "+strings.Join(by[z], ", "))
	}
	return strings.Join(out, "; ")
}

// refusedMap is the line a map asked again carries: the rule the last
// one broke, its draft shown below with the data. "" on the first ask.
func refusedMap(retry *mapRetry, only bool) string {
	if retry == nil {
		return ""
	}
	again := "Return the whole map again with that fixed"
	if only {
		again = "Answer with the map alone this time, `{\"map\": <flow>}`, the whole map with that fixed; your description is taken from your last answer"
	}
	return "\n- Your last map, shown below after the current one, was refused and not drawn: " + retry.Why + ". " + again +
		". Every rule here still holds."
}

// refusedDraft is the refused draft as the data shows it, after the
// current map: a model's answer, never instructions.
func refusedDraft(retry *mapRetry) string {
	if retry == nil || retry.Draft == nil {
		return ""
	}
	j, err := json.Marshal(retry.Draft)
	if err != nil {
		return ""
	}
	return "\n\n## Your last map, refused (" + retry.Why + ")\n\n" + string(j)
}

// noTree is the project's file check: no path pins a project statement.
func noTree(string) bool { return false }

// carriedMap is the stored map as a revision carries it on: its departed
// repositories taken out, so a carried map never names a repository that
// left. A project that was due a map and has none gets the sentinel: asked
// for, not drawn, so a quiet reconcile does not run the step again for it
// (a run that folds the project anyway asks again).
func (w *Worker) carriedMap(ctx context.Context, page store.Page, slugs []string) *diagram.Diagram {
	// A collection has no map, nor the sentinel: none was asked for, and a
	// project judged a system later is due one at once.
	if page.Collection() {
		return nil
	}
	if page.Map == nil || diagram.IsSentinel(page.Map) {
		return diagram.Sentinel()
	}
	// Checked again only for what the trim may break: a map whose last
	// repository left holds nothing of the project, and an emptied zone goes.
	// The filter as on a new map, so a box only a departed repository talked
	// to goes here too.
	carried, why := baseline.CheckMap(w.runtimeOnly(ctx, projectMapAt, w.withoutDeparted(ctx, page.Map, slugs)), slugs, baseline.MapCaps)
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
// the run on to its repository map step, the run's last, or to the system
// map asked again when the plan carries its retry.
func (w *Worker) projectOver(ctx context.Context, r *store.BaselineRun, plan runPlan) error {
	plan.ProjectDone = plan.ProjectMap == nil
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
// call. A map off its rules or caps is asked again in the same run, a
// step per call, the refused draft and the rule it broke in the prompt
// (the plan carries them), at most mapRetries more calls, each one against
// the run's budget; the description is never touched. What the step keeps
// (a new map, or the stored one confirmed) is set on the page's current
// revision in place, with its basis, and the run's end, in one
// transaction: no revision of its own, the text did not change. Before the
// check, RepoRuntimeOnly takes out what does not run in production (build,
// CI, delivery, monitoring, other stages); a map it empties is refused like
// any. A map still refused keeps a stored one, writing nothing; with none
// stored and every try run and refused, or emptied still on the last try
// (a CI library: nothing of it runs) whatever is stored, the sentinel is set with
// the basis: declined, said on the page, not asked again until the text
// changes. A map stored before the tags counts as none. A skip, a failed call, the
// calls spent before every try ran, or null for a page without a map write
// nothing, so the basis stays stale and the next run asks again (null was
// recorded once, and a map then never came). A hold
// of the gateway leaves the step where it is, resumed after the hold. The
// map is optional: a database busy past its attempts ends the run done
// without it, never failed.
func (w *Worker) repoMapStep(ctx context.Context, r *store.BaselineRun, plan runPlan) error {
	at := "baseline: " + repoMapName
	retry := plan.RepoMap
	plan.RepoMap = nil
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
	// The layout rides on the map's due: a page written before areas has a
	// basis of the old shape (MapBasis), so its map, and with it the
	// layout, is due once; after that only when the text changes, so an
	// outline that leaves a statement out never costs a call per run.
	if retry == nil && page.MapStands() {
		w.log.InfoContext(ctx, at+" skipped: drawn from the description as it stands, the stored one kept")
		return w.finish(ctx, r, plan)
	}
	// The sentinel is no map: declined for an older text. Nor is a map
	// stored before the tags: drawn with the build and CI boxes the filter
	// takes out, it is drawn again, never carried, and null for it records
	// nothing, as for a page without a map.
	keep := page.RepoMap
	// TRANSITIONAL(untagged-map): Remove when every stored map carries tags.
	if diagram.IsSentinel(keep) || diagram.Untagged(keep) {
		keep = nil
	}
	res := w.base.Describer.RepoMap(ctx, renderRepoMap(*r, page, retry))
	okCall, err := w.call(ctx, r, store.PurposeRepoMap, res.Call)
	if errors.Is(err, errStop) || (err != nil && retry == nil) {
		return err
	}
	if err != nil || !okCall {
		// A retry given up ends the step without a map, the description
		// untouched.
		r.Attempts = 0
		w.log.WarnContext(ctx, at+" skipped: the call failed: "+res.Outcome.String()+", the stored one kept")
		return w.finish(ctx, r, plan)
	}
	w.arrangePage(ctx, at, &page, res.Outline)
	tries := 1
	if retry != nil {
		tries = retry.Tries + 1
	}
	// The draft is the map as checked, what the build, CI and the other
	// stages left: the one a refusal's reason counts, for the retry to show.
	var drawn, draft *diagram.Diagram
	var refused string
	emptied := false
	if res.Map != nil {
		repaired, dropped := baseline.RepairEdges(res.Map)
		if len(dropped) > 0 {
			w.log.InfoContext(ctx, at+": self or repeated edges taken out: "+strings.Join(dropped, ", "))
		}
		draft = w.repoRuntimeOnly(ctx, at, repaired)
		// Untagged, the filter cannot be trusted to have emptied it.
		refused = baseline.CheckTagged(repaired)
		switch {
		case refused != "":
		case len(draft.Nodes) == 0:
			// Every box went with its arrows (one drawn with none stays):
			// the model's tags say nothing of the repository runs in
			// production. A refusal like any, asked again with the answer,
			// so one slip never costs a map; said on the last try, declined.
			emptied = true
			refused = "every node was taken out: by your tags nothing of the repository runs in production"
			draft = repaired
		default:
			drawn, refused = baseline.CheckRepoMap(draft, baseline.MapCaps)
		}
		if refused != "" {
			w.log.WarnContext(ctx, at+" refused ("+refused+")", slog.String("nodes", mapLabels(draft)))
		}
	}
	switch {
	case emptied && tries >= 1+mapRetries:
		w.log.InfoContext(ctx, at+": nothing of it runs in production, still, declined")
		keep = diagram.Sentinel()
	case drawn != nil && diagram.Equal(drawn, keep), res.Map == nil && keep != nil:
		w.log.InfoContext(ctx, at+": no change")
	case drawn != nil:
		keep = drawn
		w.log.InfoContext(ctx, fmt.Sprintf("%s: drawn (%d nodes, %d edges)", at, len(drawn.Nodes), len(drawn.Edges)))
	case res.Map == nil:
		// None drawn, first or on a retry: nothing recorded.
		w.log.InfoContext(ctx, at+": none drawn, asked again by the next run")
		return w.finish(ctx, r, plan)
	case tries < 1+mapRetries && r.Calls < w.base.Limits.MaxCalls:
		w.log.InfoContext(ctx, fmt.Sprintf("%s: asked again, %d of %d", at, tries, mapRetries))
		plan.RepoMap = &mapRetry{Draft: draft, Why: refused, Tries: tries}
		if err := encodeInto(r, plan); err != nil {
			return err
		}
		return w.save(ctx, *r)
	case keep != nil || tries < 1+mapRetries:
		// A stored map stays; one refused before every try ran (no call
		// left) is not declined: the next run asks again.
		w.log.WarnContext(ctx, at+": still refused, the stored one kept")
		return w.finish(ctx, r, plan)
	default:
		w.log.WarnContext(ctx, at+": still refused, declined")
		keep = diagram.Sentinel()
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
		// The attempts are spent, or the write fails for its content: the
		// map is optional, the run ends done without it, the basis stale
		// for the next run.
		w.log.WarnContext(ctx, at+" skipped: "+err.Error()+", the stored one kept")
		return w.finish(ctx, r, plan)
	}
	w.stepWritten()
	w.requeueFailed(ctx, r.ProjectKey, r.Slug)
	return nil
}

// applyOutline is statements laid out as an outline says
// (baseline.ApplyOutline), what it left out logged; nil keeps them as they
// are.
func (w *Worker) applyOutline(ctx context.Context, at string, statements []store.Statement, outline []inference.OutlineArea) []store.Statement {
	if outline == nil {
		if !baseline.Arranged(statements) {
			w.log.InfoContext(ctx, at+": no outline returned, statements without an area stay under none")
		}
		return statements
	}
	arranged, bad := baseline.ApplyOutline(statements, outline)
	if len(bad) > 0 {
		w.log.WarnContext(ctx, at+": outline items left out", slog.Any("items", bad))
	}
	return arranged
}

// arrangePage lays a page out as its step's outline says (applyOutline),
// in place on its current revision: the same statements, no source, no
// revision. None returned, or one that changes no order and no area,
// writes nothing. The layout is optional, as the map is: a failed write is
// logged and the page stays as it was, laid out by the next step that
// reads it whole.
func (w *Worker) arrangePage(ctx context.Context, at string, page *store.Page, outline []inference.OutlineArea) {
	if outline == nil {
		w.applyOutline(ctx, at, page.Statements, nil)
		return
	}
	arranged := w.applyOutline(ctx, at, page.Statements, outline)
	if slices.EqualFunc(arranged, page.Statements, func(a, b store.Statement) bool { return a.ID == b.ID && a.Area == b.Area }) {
		w.log.InfoContext(ctx, at+": page laid out as it stands")
		return
	}
	ok, err := w.base.Store.ArrangePage(ctx, page.RevisionID, arranged)
	if err != nil {
		w.log.WarnContext(ctx, at+": layout write failed, the page kept as it was", logging.Err(err))
		return
	}
	if !ok {
		w.log.InfoContext(ctx, at+": layout not written: the page has a newer revision, laid out by its next run")
		return
	}
	page.Statements = arranged
	w.log.InfoContext(ctx, fmt.Sprintf("%s: page laid out in %s", at, plural(areaCount(arranged), "area")))
}

// areaCount is how many areas a page's statements stand in.
func areaCount(statements []store.Statement) int {
	seen := map[string]bool{}
	for _, s := range statements {
		seen[s.Area] = true
	}
	return len(seen)
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
	if r.Mode == store.RunFull && r.Trigger != store.TriggerDeparture {
		r.ReadBytes = coverage(plan)
	}
	w.log.InfoContext(ctx, fmt.Sprintf("baseline: run done at %s: %d calls, %d bytes read, %d skipped",
		gitrepo.ShortSHA(r.Commit), r.Calls, r.Bytes, r.Skipped))
	return nil
}

// coverage is the distinct file content a run's folded parts read: every
// file of a subject read and not skipped, once however many subjects hold
// it, and the docs the overview folded, a doc a subject holds too counted
// once. The share of the tree the page
// rests on; the budget's count (Bytes) is another figure, every call's
// input, failed ones too.
func coverage(plan runPlan) int64 {
	skipped := map[string]bool{}
	for _, name := range plan.Skipped {
		skipped[name] = true
	}
	read := map[string]bool{}
	for _, name := range plan.Read {
		read[name] = true
	}
	seen := map[string]bool{}
	var n int64
	count := func(files []baseline.File) {
		for _, f := range files {
			if !seen[f.Path] {
				seen[f.Path] = true
				n += f.Size
			}
		}
	}
	// Docs is set once the overview call answers; a patch the fold then
	// refused skips the step, and its docs were read for nothing.
	if !plan.StepSkipped(baseline.PartOverview) {
		count(plan.Docs)
	}
	for _, s := range plan.Subjects {
		if read[s.Name] && !skipped[s.Name] {
			count(s.Files)
		}
	}
	return n
}

// finish ends a run done and saves it; the repository's failed notes go
// back on the queue after it (requeueFailed).
func (w *Worker) finish(ctx context.Context, r *store.BaselineRun, plan runPlan) error {
	if err := w.markDone(ctx, r, plan); err != nil {
		return err
	}
	if err := w.save(ctx, *r); err != nil {
		return err
	}
	w.requeueFailed(ctx, r.ProjectKey, r.Slug)
	return nil
}

// sameChecks reports whether a's statements carry b's last checks, by id:
// the two may stand in another order.
func sameChecks(a, b []store.Statement) bool {
	checked := make(map[string]string, len(b))
	for _, s := range b {
		checked[s.ID] = s.Checked
	}
	for _, s := range a {
		if c, ok := checked[s.ID]; !ok || c != s.Checked {
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
		head := ""
		if f.Cut {
			// What was sent: the head is cut back to a line's end.
			head = fmt.Sprintf(" (the first %d bytes of a larger file)", len(body))
		}
		fmt.Fprintf(&b, "<<<FILE %s%s>>>\n%s\n<<<END FILE %s>>>\n\n", f.Path, head, strings.TrimRight(string(body), "\n"), f.Path)
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

func renderDescribe(r store.BaselineRun, page store.Page, l config.Limits, step, steps int, part, focus, files, problems, libs, siblings string,
	newer map[string]string) string {
	return strings.NewReplacer(
		"{problem_areas}", problems, "{shared}", libs, "{sibling_interfaces}", siblings,
		"{repo}", r.Slug, "{project}", r.ProjectKey, "{commit}", r.Commit,
		"{step}", fmt.Sprint(step), "{steps}", fmt.Sprint(steps),
		"{diagram_change}", diagramChange(l), "{part}", part, "{focus}", focus,
		"{repo_description}", describeSourced(page, newer), "{files}", files, "{diagram_rules}", diagramRules(l), "{diagram_field}", diagramField(l),
	).Replace(prompts.Describe)
}

// renderProject is the project step's prompt; retry is the refused map,
// asking for the map alone again (nil the first time).
func renderProject(r store.BaselineRun, repoPage, projectPage store.Page, others, contracts string, libs []store.SharedRepo, repoSlugs, departed []string,
	inner []store.Edge, l config.Limits, retry *mapRetry) string {
	nodes, edges := mapRoom(libs, len(repoSlugs))
	return strings.NewReplacer(
		"{project_shape}", shapeShown(projectPage), "{inner_edges}", innerEdgesShown(inner),
		"{departed}", departedRule(departed),
		"{shared}", sharedRepos(libs, true), "{repo}", r.Slug, "{project}", r.ProjectKey, "{commit}", r.Commit,
		"{project_description}", describeTaught(projectPage), "{repo_description}", describeRead(repoPage), "{other_repos}", others,
		"{other_contracts}", contracts, "{project_map}", projectMap(projectPage)+refusedDraft(retry), "{repo_slugs}", strings.Join(repoSlugs, ", "),
		"{diagram_rules}", diagramRules(l), "{diagram_field}", diagramField(l),
		"{map_due}", mapDue(len(repoSlugs)), "{map_max_nodes}", fmt.Sprint(nodes), "{map_max_edges}", fmt.Sprint(edges),
		"{map_refused}", refusedMap(retry, true),
	).Replace(prompts.Project)
}

// shapeShown is the project's shape as the page holds it, for a project
// call to keep or change: a page no step judged yet says so.
func shapeShown(p store.Page) string {
	switch {
	case p.RevisionID == 0:
		return "_None yet: judge it._"
	case p.Shape == "":
		return "_Not judged yet: the description below was written as a system's; judge it._"
	}
	return "`" + p.Shape + "`"
}

// innerEdgesShown is the build dependencies between the project's own
// repositories, one line each: who builds on whose code, by which
// coordinates.
func innerEdgesShown(edges []store.Edge) string {
	if len(edges) == 0 {
		return "_None: no repository of the project builds on another's code._"
	}
	var b strings.Builder
	for _, e := range edges {
		fmt.Fprintf(&b, "- `%s` builds on `%s` (%s)\n", e.From.Slug, e.To.Slug, strings.Join(e.Coords, ", "))
	}
	return strings.TrimRight(b.String(), "\n")
}

// mapRoom is the nodes and edges the model may draw on the system map:
// what one map holds less what code adds for the shared repositories (a
// node each, an edge per user), so the map with them still fits; never fewer
// than the repositories need, the UI then showing as many of them as
// fit.
func mapRoom(libs []store.SharedRepo, repos int) (int, int) {
	edges := diagram.MapMaxEdges
	for _, l := range libs {
		edges -= len(l.UsedBy)
	}
	return max(diagram.MapMaxNodes-len(libs), repos), max(edges, repos)
}

// renderRepoMap is the map step's prompt; retry is the refused map (nil the
// first time).
func renderRepoMap(r store.BaselineRun, page store.Page, retry *mapRetry) string {
	return strings.NewReplacer(
		"{repo}", r.Slug, "{project}", r.ProjectKey, "{commit}", r.Commit,
		"{repo_map}", repoMap(page)+refusedDraft(retry), "{repo_description}", describeRead(page),
		"{map_max_nodes}", fmt.Sprint(diagram.MapMaxNodes), "{map_max_edges}", fmt.Sprint(diagram.MapMaxEdges),
		"{map_refused}", refusedMap(retry, false),
	).Replace(prompts.RepoMap)
}

// repoMap is the repository's current map as the JSON the model returns,
// so the map step can say it stands. The sentinel is no map, nor one
// stored before the tags: shown, the model would keep it as it stands.
func repoMap(p store.Page) string {
	// TRANSITIONAL(untagged-map): Remove when every stored map carries tags.
	if p.RepoMap == nil || diagram.IsSentinel(p.RepoMap) || diagram.Untagged(p.RepoMap) {
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
	if p.Collection() {
		return "_None: a collection has no map. Judged a system, draw it._"
	}
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

// otherContracts is the project's other repositories' interfaces and
// entities, by slug, for the project step: kind, name, the lead and the
// files each, so who offers what and who uses it is read from both sides,
// not guessed from a summary. A budget of its own (ContractsMaxBytes),
// fitted as otherRepos is; a repository without a summary still shows its
// own. A prompt's input, never stored text.
func otherContracts(repos []store.RepoSummary, self string) string {
	lists := make([]store.RepoSummary, 0, len(repos))
	for _, r := range repos {
		var lines []string
		for _, s := range r.Contracts {
			line := fmt.Sprintf("- %s `%s`", s.Kind, s.Name)
			if l := lead(s.Text); l != "" {
				line += ": " + l
			}
			if len(s.Files) > 0 {
				line += " (" + strings.Join(s.Files, ", ") + ")"
			}
			lines = append(lines, line)
		}
		lists = append(lists, store.RepoSummary{Slug: r.Slug, Summary: strings.Join(lines, "\n")})
	}
	return fitSummaries(lists, self, baseline.ContractsMaxBytes, "Also in the project, with none here")
}

// lead is a statement text's first paragraph of prose, on one line: a
// laid-out text may open with a sub-heading or a list (markup.FromBlocks),
// which says nothing on its own and would read as more list items. ""
// without one.
func lead(text string) string {
	for _, p := range strings.Split(text, "\n\n") {
		p = strings.TrimSpace(p)
		if p == "" || strings.HasPrefix(p, "#") || strings.HasPrefix(p, "- ") || numbered.MatchString(p) {
			continue
		}
		return strings.Join(strings.Fields(p), " ")
	}
	return ""
}

// numbered is a numbered list item's start, as markup.FromBlocks writes it.
var numbered = regexp.MustCompile(`^\d+\. `)

// siblingInterfaces names the interfaces and entities the project's other
// repositories offer, for a describe step: a call this part's code makes
// to one, a type it shares, is named by its repository and name, not
// guessed. Names only, beside every step: past SiblingsMaxBytes the rest
// are counted, never read. "_None._" without any.
func siblingInterfaces(repos []store.RepoSummary, self string) string {
	var b strings.Builder
	left := 0
	for _, r := range repos {
		if r.Slug == self {
			continue
		}
		for _, s := range r.Contracts {
			line := fmt.Sprintf("- `%s`: %s `%s`\n", r.Slug, s.Kind, s.Name)
			if left > 0 || b.Len()+len(line) > baseline.SiblingsMaxBytes {
				left++
				continue
			}
			b.WriteString(line)
		}
	}
	if b.Len() == 0 && left == 0 {
		return "_None._"
	}
	out := strings.TrimSuffix(b.String(), "\n")
	if left > 0 {
		out += fmt.Sprintf("\n\n%d more, past what a step reads.", left)
	}
	return out
}

// sharedRepos is the summaries of the source repositories a repository, or
// a project's repositories, depend on (store.Shared), named project/slug,
// each with its users when they are a project's; fitted to SharedMaxBytes
// like otherRepos. A prompt's input, never stored text.
func sharedRepos(libs []store.SharedRepo, users bool) string {
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
	return fitSummaries(repos, "", baseline.SharedMaxBytes, "Also depended on, without a summary here")
}

// fitSummaries is otherRepos' and sharedRepos' fitting: every summary but
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
	// A part's heading, whole or cut, with none of its text after it says
	// nothing and would take the mark into its title.
	for i := strings.LastIndex(cut, "\n\n"); i >= 0; i = strings.LastIndex(cut, "\n\n") {
		last := cut[i+2:]
		if _, h := markup.StoredHeading(last); !h || strings.Contains(last, "\n") {
			break
		}
		cut = cut[:i]
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
