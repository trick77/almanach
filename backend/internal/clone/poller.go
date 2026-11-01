// Package clone keeps every source's checkouts current: one goroutine,
// sequential, a cycle per interval and on a nudge, after ../rongo's
// indexer poller. Sequential on purpose: a stampede of parallel clones
// against one forge is how a token gets rate-limited. One repository's
// failure is recorded on its row and the cycle goes on; only a shutdown
// stops it.
package clone

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/trick77/almanach/internal/gitrepo"
	"github.com/trick77/almanach/internal/secret"
	"github.com/trick77/almanach/internal/store"
	"github.com/trick77/almanach/internal/urlorigin"
)

// FirstDelay is the wait before the first cycle: short, so a fresh
// deployment shows its first clone while the operator is still watching,
// and announced in the log so "not yet" reads apart from "broken".
const FirstDelay = 30 * time.Second

// Store is what the poller needs of the store.
type Store interface {
	Sources(ctx context.Context) ([]store.SourceRow, error)
	ApplyListing(ctx context.Context, key string, listed []store.ListedRepo) (store.ListingResult, error)
	MarkSourceListed(ctx context.Context, key, errText string, listed bool) error
	SyncJobs(ctx context.Context) ([]store.SyncJob, error)
	SourceRepos(ctx context.Context, key string) ([]store.SourceRepo, error)
	MarkRepoSyncing(ctx context.Context, key, slug string) error
	MarkRepoReady(ctx context.Context, key, slug, branch, head string) error
	MarkRepoFailed(ctx context.Context, key, slug, errText string) error
	EnqueueRun(ctx context.Context, key, slug, mode, trigger, baseCommit, commit string) (int64, error)
	LastRun(ctx context.Context, key, slug string) (store.BaselineRun, int, bool, error)
	DocumentedRun(ctx context.Context, key, slug string) (store.BaselineRun, bool, error)
	CurrentPage(ctx context.Context, scope, project, repo string) (store.Page, bool, error)
	RepoIDs(ctx context.Context, key, slug string) (projectID, repoID int64, ok bool, err error)
}

// A clone's folder is named by ids, <root>/p<project id>/r<repo id>: a
// rename of the project or the repository never touches the disk, a change
// of case included, which a case-insensitive filesystem would trip over.
func projectSeg(projectID int64) string { return "p" + strconv.FormatInt(projectID, 10) }
func repoSeg(repoID int64) string       { return "r" + strconv.FormatInt(repoID, 10) }

// Describe is when the poller queues baseline runs on its own: a clone's
// first ready, and a reconcile once Reconcile has passed since a
// repository's last run and its head moved off the documented commit
// (zero: buttons only), and a failed run, or a done one that left parts
// or a map behind, once more (describe). Wake tells the worker. Nil Wake: the baseline is
// off and the poller queues nothing.
type Describe struct {
	Reconcile time.Duration
	Wake      func()
}

// Git is what the poller needs of gitrepo.
type Git interface {
	Dir(key, slug string) (string, error)
	ProjectDir(key string) (string, error)
	OriginURL(ctx context.Context, dir string) (string, error)
	EnsureCloned(ctx context.Context, dir, cloneURL string, a gitrepo.Access) error
	DefaultBranch(ctx context.Context, cloneURL string, a gitrepo.Access) (string, error)
	Fetch(ctx context.Context, dir, cloneURL string, a gitrepo.Access) error
	HeadSHA(ctx context.Context, dir, branch string) (string, error)
	Remove(key, slug string) error
	RemoveProject(key string) error
	// TRANSITIONAL(ids): moves a clone from its folder named by names to the
	// one named by ids. Remove when every instance has synced once on a
	// release with ids (no name-named folder left).
	MoveLegacy(fromKey, fromSlug, toKey, toSlug string) (bool, error)
}

// Lister lists a Bitbucket project's repositories.
type Lister interface {
	ListRepos(ctx context.Context, host, project string, a gitrepo.Access) ([]store.ListedRepo, error)
}

// Deps are the poller's collaborators.
type Deps struct {
	Store  Store
	Git    Git
	Lister Lister
	Key    secret.Key
	// Interval is the cycle; zero means cycles run on a nudge only.
	Interval time.Duration
	// FirstDelay overrides the constant; tests set it.
	FirstDelay time.Duration
	Log        *slog.Logger
	Describe   Describe
	// Now is the clock; tests set it.
	Now func() time.Time
}

// Poller is the worker.
type Poller struct {
	Deps
	wake   chan struct{}
	cancel context.CancelFunc
	done   chan struct{}
	mu     sync.Mutex
	// disk serialises the cycle's work on a clone with a removal from the
	// API: a folder deleted under a running sync would be recreated by the
	// sync and then belong to no row. removed collects what went during a
	// cycle, so a job the cycle already listed is skipped.
	disk    sync.Mutex
	removed map[string]bool
	// current is the sync holding disk, so a removal of that row or its
	// project can cancel it instead of waiting out a long clone. Guarded
	// by cur, which Remove takes before it waits on disk.
	cur     sync.Mutex
	current inFlight
}

type inFlight struct {
	projectID, repoID int64
	cancel            context.CancelFunc
}

// New builds a Poller.
func New(d Deps) *Poller {
	if d.FirstDelay <= 0 {
		d.FirstDelay = FirstDelay
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Poller{Deps: d, wake: make(chan struct{}, 1), removed: map[string]bool{}}
}

// Dir is a clone's folder, by ids, for the API's bodies.
func (p *Poller) Dir(projectID, repoID int64) (string, error) {
	return p.Git.Dir(projectSeg(projectID), repoSeg(repoID))
}

// ProjectDir is the project's folder, by id.
func (p *Poller) ProjectDir(projectID int64) (string, error) {
	return p.Git.ProjectDir(projectSeg(projectID))
}

// Remove deletes one clone, never while a sync touches the disk, and
// marks it so a cycle in flight skips the row. A sync of this very row is
// cancelled first, so the removal waits for git to die, not to finish.
func (p *Poller) Remove(projectID, repoID int64) error {
	p.cutIf(func(f inFlight) bool { return f.projectID == projectID && f.repoID == repoID })
	p.disk.Lock()
	defer p.disk.Unlock()
	p.removed[projectSeg(projectID)+"/"+repoSeg(repoID)] = true
	return p.Git.Remove(projectSeg(projectID), repoSeg(repoID))
}

// RemoveProject deletes a whole project folder under the same lock,
// cancelling a sync of any of its repositories first.
func (p *Poller) RemoveProject(projectID int64) error {
	p.cutIf(func(f inFlight) bool { return f.projectID == projectID })
	p.disk.Lock()
	defer p.disk.Unlock()
	p.removed[projectSeg(projectID)] = true
	return p.Git.RemoveProject(projectSeg(projectID))
}

// idSeg is a folder name the ids use (p12, r7): a legacy move or removal
// never touches one, whatever a project key or slug happened to be called.
// Any case: on a case-insensitive volume P1 is p1.
var idSeg = regexp.MustCompile(`(?i)^[pr][0-9]+$`)

// RemoveLegacy deletes key/slug's clone in the folder named by names, under
// the disk lock, unless the folder could be an id folder.
//
// TRANSITIONAL(ids): a clone from before the ids that no sync moved (a
// missing repository is never synced). Remove when every instance has
// synced once on a release with ids.
func (p *Poller) RemoveLegacy(key, slug string) error {
	if idSeg.MatchString(key) {
		return nil
	}
	p.disk.Lock()
	defer p.disk.Unlock()
	return p.Git.Remove(key, slug)
}

// RemoveLegacyProject is RemoveLegacy for a whole project folder named by
// its key. TRANSITIONAL(ids), goes with RemoveLegacy.
func (p *Poller) RemoveLegacyProject(key string) error {
	if idSeg.MatchString(key) {
		return nil
	}
	p.disk.Lock()
	defer p.disk.Unlock()
	return p.Git.RemoveProject(key)
}

// cutIf cancels the sync in flight when it is one of the removal's.
func (p *Poller) cutIf(match func(inFlight) bool) {
	p.cur.Lock()
	defer p.cur.Unlock()
	if p.current.cancel != nil && match(p.current) {
		p.current.cancel()
	}
}

// gone reports whether a job's row or project was removed since the cycle
// listed it.
func (p *Poller) gone(projectID, repoID int64) bool {
	return p.removed[projectSeg(projectID)] || p.removed[projectSeg(projectID)+"/"+repoSeg(repoID)]
}

// Nudge starts the next cycle now rather than at the end of the wait.
// Buffered by one: a nudge that lands mid-cycle starts another right after.
func (p *Poller) Nudge() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// Start runs the loop until Stop.
func (p *Poller) Start(ctx context.Context) {
	p.mu.Lock()
	defer p.mu.Unlock()
	ctx, p.cancel = context.WithCancel(ctx)
	p.done = make(chan struct{})
	go func() {
		defer close(p.done)
		p.run(ctx)
	}()
}

// Stop cancels the cycle in flight and waits for the loop to end. A clone
// cut off here leaves a directory without a checkout, which the next cycle
// removes and clones again.
func (p *Poller) Stop() {
	p.mu.Lock()
	cancel, done := p.cancel, p.done
	p.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}

func (p *Poller) run(ctx context.Context) {
	delay := jittered(p.FirstDelay)
	p.Log.Info("sync scheduled", "first_cycle_in", delay.Round(time.Second).String(), "interval", p.Interval.String())
	for {
		if !p.wait(ctx, delay) {
			return
		}
		if err := p.CycleOnce(ctx); err != nil && ctx.Err() == nil {
			p.Log.Error("sync cycle failed", "err", err.Error())
		}
		delay = jittered(p.Interval)
	}
}

// wait sleeps for d (forever when d is zero), or until the context ends or
// a nudge arrives. False when the context ended.
func (p *Poller) wait(ctx context.Context, d time.Duration) bool {
	var timer <-chan time.Time
	if d > 0 {
		t := time.NewTimer(d)
		defer t.Stop()
		timer = t.C
	}
	select {
	case <-ctx.Done():
		return false
	case <-timer:
		return true
	case <-p.wake:
		return true
	}
}

// CycleOnce lists every Bitbucket source, then syncs every repository that
// is not missing. It returns an error only for a failure that stops the
// whole cycle (the store not answering); one source's or repository's
// failure is recorded on its row and the loop continues.
func (p *Poller) CycleOnce(ctx context.Context) error {
	started := time.Now()
	sources, err := p.Store.Sources(ctx)
	if err != nil {
		return err
	}
	for _, s := range sources {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if s.Kind == store.SourceBitbucket {
			p.list(ctx, s.ProjectSource)
		}
	}
	// Forget the last cycle's removals before the rows are read, never
	// after: a removal landing between the read and the reset would be
	// lost, and the job it concerned would clone the folder back.
	p.disk.Lock()
	p.removed = map[string]bool{}
	p.disk.Unlock()
	jobs, err := p.Store.SyncJobs(ctx)
	if err != nil {
		return err
	}
	p.Log.Info("sync cycle started", "sources", len(sources), "repositories", len(jobs))
	bound, err := p.bindings(ctx, jobs)
	if err != nil {
		return err
	}
	var synced, unchanged, failed int
	for _, j := range jobs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		repoStart := time.Now()
		p.disk.Lock()
		var head string
		var moved bool
		if p.gone(j.Repo.ProjectID, j.Repo.RepoID) {
			p.disk.Unlock()
			continue
		}
		// The sync runs on its own cancellable context, so a removal of
		// this very row (or its project) can cut it short rather than wait
		// behind a long clone.
		jobCtx, cancel := context.WithCancel(ctx)
		p.cur.Lock()
		p.current = inFlight{projectID: j.Repo.ProjectID, repoID: j.Repo.RepoID, cancel: cancel}
		p.cur.Unlock()
		head, moved, err = p.sync(jobCtx, j, bound[j.Source.ProjectKey])
		cut := jobCtx.Err() != nil && ctx.Err() == nil
		p.cur.Lock()
		p.current = inFlight{}
		p.cur.Unlock()
		cancel()
		p.disk.Unlock()
		switch {
		case err != nil && ctx.Err() != nil:
			// The process is ending, not the repository failing.
			return ctx.Err()
		case cut:
			// Removed under the sync: the row is going, nothing to record.
			continue
		case err != nil:
			failed++
			p.Log.Warn("repo_sync_failed", "project", j.Source.ProjectKey, "repo", j.Repo.Slug, "took", took(repoStart), "err", err.Error())
			if markErr := p.Store.MarkRepoFailed(ctx, j.Source.ProjectKey, j.Repo.Slug, err.Error()); markErr != nil {
				p.Log.Error("recording the failure failed too", "repo", j.Repo.Slug, "err", markErr.Error())
			}
		case moved:
			synced++
			p.Log.Info("repo_synced", "project", j.Source.ProjectKey, "repo", j.Repo.Slug, "head", gitrepo.ShortSHA(head), "took", took(repoStart))
			p.describe(ctx, j, head)
		default:
			unchanged++
			p.Log.Debug("repo_unchanged", "project", j.Source.ProjectKey, "repo", j.Repo.Slug, "head", gitrepo.ShortSHA(head), "took", took(repoStart))
			p.describe(ctx, j, head)
		}
	}
	p.Log.Info("sync cycle finished", "checked", len(jobs), "synced", synced, "unchanged", unchanged, "failed", failed, "took", took(started))
	return nil
}

// describe queues a baseline run after a sync when one is due: the first
// run on a clone's first ready (its row had no head before), a reconcile
// when the head is off the documented commit and the last run is older
// than the interval, and a failed run again, in its own mode, after
// retryAfter, at most maxRetries times in a row; past that the failed
// repository is back on the reconcile interval. A done run that skipped
// parts, or left the repository page without a map drawn from its text
// (a page described before maps existed, a map step skipped), gets one
// reconcile after retryAfter, whatever the head: it reads the skipped
// parts again and draws the map, the head unchanged. Once: a retry that
// skips again waits for the interval. A repository already cloned when the
// baseline shipped has no run at all and waits for its button. A run
// already open is no failure.
func (p *Poller) describe(ctx context.Context, j store.SyncJob, head string) {
	if p.Describe.Wake == nil {
		return
	}
	key, slug := j.Source.ProjectKey, j.Repo.Slug
	mode, trigger, base := store.RunFull, store.TriggerFirstReady, ""
	if j.Repo.HeadCommit != "" {
		last, failed, ok, err := p.Store.LastRun(ctx, key, slug)
		if err != nil || !ok || last.FinishedAt == nil {
			// No run yet (cloned before the baseline: its button's), or
			// one still open.
			p.logRunCheck(err, key, slug)
			return
		}
		// A done last run is the documented one; the store is asked only
		// after a failure.
		doc, documented := last, last.State == store.RunDone
		if !documented {
			if doc, documented, err = p.Store.DocumentedRun(ctx, key, slug); err != nil {
				p.logRunCheck(err, key, slug)
				return
			}
		}
		since := p.Now().Sub(*last.FinishedAt)
		switch {
		case last.State == store.RunFailed && since >= p.retryAfter() && failed <= maxRetries:
			// A failed run, first, reconcile or a button's, is tried again
			// in its own mode whatever the head, a bounded number of
			// times: a failure that is permanent (a repository too large
			// for one call) must not cost a call a day forever.
			mode, trigger = last.Mode, store.TriggerRetry
			if mode == store.RunReconcile && documented {
				base = doc.Commit
			} else {
				// A reconcile with nothing documented to start from reads
				// afresh.
				mode = store.RunFull
			}
		case last.State == store.RunDone && since >= p.retryAfter() && last.Trigger != store.TriggerRetry && p.unfinished(ctx, last, key, slug):
			// Skipped parts and a missing map are never left for a head
			// that may not move again: the last run's skipped names are
			// read again, the map drawn. A run whose only skip was a
			// subject gone reads nothing and ends without a call.
			mode, trigger, base = store.RunReconcile, store.TriggerRetry, doc.Commit
		case p.Describe.Reconcile <= 0 || since < p.Describe.Reconcile:
			return
		case !documented:
			// Never documented and past its retries: the interval tries
			// the full read again.
			trigger = store.TriggerMonthly
		case doc.Commit == head:
			return
		default:
			mode, trigger, base = store.RunReconcile, store.TriggerMonthly, doc.Commit
		}
	}
	id, err := p.Store.EnqueueRun(ctx, key, slug, mode, trigger, base, head)
	switch {
	case errors.Is(err, store.ErrExists):
		return
	case err != nil:
		p.Log.Warn("baseline_queue_failed", "project", key, "repo", slug, "err", err.Error())
		return
	}
	p.Log.Info("baseline_queued", "project", key, "repo", slug, "run_id", id, "mode", mode, "trigger", trigger, "head", gitrepo.ShortSHA(head))
	p.Describe.Wake()
}

// unfinished reports whether a done run left work behind: parts skipped,
// or a repository page whose map was not drawn from its text. Read last,
// after the cheap checks: a page read per repository per cycle otherwise.
// A failed read counts as finished: the next cycle asks again.
func (p *Poller) unfinished(ctx context.Context, last store.BaselineRun, key, slug string) bool {
	if last.Skipped > 0 {
		return true
	}
	page, ok, err := p.Store.CurrentPage(ctx, store.ScopeRepository, key, slug)
	if err != nil {
		p.logRunCheck(err, key, slug)
		return false
	}
	return ok && !page.MapStands()
}

// retryWait is how long a failed run waits before it is queued again:
// long enough for a gateway outage, short of the monthly reconcile.
const retryWait = 24 * time.Hour

// retryAfter is retryWait, or the reconcile interval when that is shorter:
// a failure must not slow the cadence it interrupted.
func (p *Poller) retryAfter() time.Duration {
	if r := p.Describe.Reconcile; r > 0 && r < retryWait {
		return r
	}
	return retryWait
}

// maxRetries is how many times in a row the poller queues a failed run
// again; past it the repository waits for the reconcile interval or a
// button.
const maxRetries = 3

func (p *Poller) logRunCheck(err error, key, slug string) {
	if err != nil {
		p.Log.Warn("baseline_check_failed", "project", key, "repo", slug, "err", err.Error())
	}
}

// list reconciles one Bitbucket source's repositories with the API.
func (p *Poller) list(ctx context.Context, s store.ProjectSource) {
	errText, applied := "", false
	a, err := p.access(s, urlorigin.Of(s.Host))
	if err == nil {
		var listed []store.ListedRepo
		listed, err = p.Lister.ListRepos(ctx, s.Host, s.ProjectKey, a)
		if err == nil {
			var res store.ListingResult
			res, err = p.Store.ApplyListing(ctx, s.ProjectKey, listed)
			if err == nil {
				p.Log.Info("source_listed", "project", s.ProjectKey, "repositories", len(listed), "added", res.Added,
					"renamed", res.Renamed, "missing", res.Missing)
				applied = true
				if len(res.Conflicts) > 0 {
					// A name is never given to another repository: the
					// listed one is left out, the source page says why.
					// The listing itself applied: its time still counts.
					errText = "left out, the slug was another repository's: " + strings.Join(res.Conflicts, ", ")
					p.Log.Warn("source_listing_conflicts", "project", s.ProjectKey, "repos", strings.Join(res.Conflicts, ","))
				}
			}
		}
	}
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		errText = err.Error()
		p.Log.Warn("source_listing_failed", "project", s.ProjectKey, "err", errText)
	}
	if markErr := p.Store.MarkSourceListed(ctx, s.ProjectKey, errText, applied); markErr != nil {
		p.Log.Error("recording the listing failed", "project", s.ProjectKey, "err", markErr.Error())
	}
}

// access opens the source's token for one command's worth of use, bound to
// origin: git sends it nowhere else.
func (p *Poller) access(s store.ProjectSource, origin string) (gitrepo.Access, error) {
	a := gitrepo.Access{Auth: s.TokenAuth, User: s.TokenUser, Origin: origin}
	if !s.HasToken() {
		return a, nil
	}
	tok, err := p.Key.Open(s.TokenEnc)
	if err != nil {
		if errors.Is(err, secret.ErrNoKey) {
			return a, errors.New("a token is stored but BACKEND_SOURCE_TOKEN_KEY is not set")
		}
		return a, fmt.Errorf("stored token cannot be opened (key changed?): %w", err)
	}
	a.Token = tok
	return a, nil
}

// binding is where a source's token may go: origin, "" for nowhere, and
// the folder's hosts when a static one spans several.
type binding struct {
	origin string
	hosts  []string
}

// bindings is each token-bearing source's binding for the cycle: a
// Bitbucket source's host; a static folder's single host, by the API's
// definition over all its rows (store.Origins), none when they span
// several. The API keeps a folder on one host; rows from before that rule
// may not be, and their token then goes nowhere rather than to a guess.
func (p *Poller) bindings(ctx context.Context, jobs []store.SyncJob) (map[string]binding, error) {
	out := map[string]binding{}
	for _, j := range jobs {
		key := j.Source.ProjectKey
		if _, done := out[key]; done || !j.Source.HasToken() {
			continue
		}
		if j.Source.Kind != store.SourceStatic {
			out[key] = binding{origin: urlorigin.Of(j.Source.Host)}
			continue
		}
		repos, err := p.Store.SourceRepos(ctx, key)
		if err != nil {
			return nil, err
		}
		hosts := store.Origins(repos)
		if len(hosts) == 1 {
			out[key] = binding{origin: hosts[0]}
		} else {
			out[key] = binding{hosts: hosts}
		}
	}
	return out, nil
}

// sync is one repository's cycle: origin check, clone, branch, fetch,
// head. Reports the head and whether it moved. A clone URL off the token's
// binding goes anonymous, so a public repository still syncs; a failure
// then carries why the token was withheld, not git's bare auth error.
func (p *Poller) sync(ctx context.Context, j store.SyncJob, b binding) (string, bool, error) {
	key, slug := j.Source.ProjectKey, j.Repo.Slug
	a, err := p.access(j.Source, b.origin)
	if err != nil {
		return "", false, err
	}
	withheld := ""
	if a.Token != "" && !urlorigin.Same(j.Repo.CloneURL, b.origin) {
		withheld = "token withheld: clone URL is on " + urlorigin.Of(j.Repo.CloneURL) + ", the source's token belongs to " + b.origin
		if b.origin == "" {
			withheld = "token withheld: the folder's repositories are on several hosts (" + strings.Join(b.hosts, ", ") + ")"
		}
		a.Token = ""
		p.Log.Warn("token_withheld", "project", key, "repo", slug, "reason", withheld)
	}
	because := func(err error) error {
		if withheld == "" {
			return err
		}
		return fmt.Errorf("%s; %w", withheld, err)
	}
	pseg, rseg := projectSeg(j.Repo.ProjectID), repoSeg(j.Repo.RepoID)
	dir, err := p.Git.Dir(pseg, rseg)
	if err != nil {
		return "", false, err
	}
	if err := p.Store.MarkRepoSyncing(ctx, key, slug); err != nil {
		return "", false, err
	}
	// TRANSITIONAL(ids): a clone from before the ids sits in a folder named
	// by its names; moved once, it is not cloned again. Remove when every
	// instance has synced once on a release with ids.
	if !idSeg.MatchString(key) {
		if moved, err := p.Git.MoveLegacy(key, slug, pseg, rseg); err != nil {
			p.Log.Warn("moving the clone to its id folder failed; cloning afresh", "project", key, "repo", slug, "err", err.Error())
		} else if moved {
			p.Log.Info("clone moved to its id folder", "project", key, "repo", slug, "dir", dir)
		}
	}
	// The directory is a label; the origin is the identity. A corrected
	// clone URL must not go on serving the old code under the new name.
	origin, err := p.Git.OriginURL(ctx, dir)
	if err != nil {
		return "", false, err
	}
	if origin != "" && origin != j.Repo.CloneURL {
		p.Log.Info("clone points at a different remote; re-cloning", "project", key, "repo", slug, "clone_origin", gitrepo.Redact(origin), "configured", j.Repo.CloneURL)
		if err := p.Git.Remove(pseg, rseg); err != nil {
			return "", false, err
		}
	}
	if err := p.Git.EnsureCloned(ctx, dir, j.Repo.CloneURL, a); err != nil {
		return "", false, because(err)
	}
	branch := j.Repo.Branch
	if branch == "" {
		branch, err = p.Git.DefaultBranch(ctx, j.Repo.CloneURL, a)
		if err != nil {
			return "", false, because(err)
		}
		p.Log.Info("branch resolved from the remote", "project", key, "repo", slug, "branch", branch)
	}
	if err := p.Git.Fetch(ctx, dir, j.Repo.CloneURL, a); err != nil {
		return "", false, because(err)
	}
	head, err := p.Git.HeadSHA(ctx, dir, branch)
	if err != nil {
		return "", false, err
	}
	if err := p.Store.MarkRepoReady(ctx, key, slug, branch, head); err != nil {
		return "", false, err
	}
	return head, head != j.Repo.HeadCommit, nil
}

// jittered is d plus up to ten percent, so a fleet coming up together does
// not reach one forge in the same second. Zero stays zero.
func jittered(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	return d + time.Duration(rand.Int64N(int64(d)/10+1)) //nolint:gosec // schedule jitter, not a secret
}

// took renders a duration as a string: the JSON handler would print a
// Duration as nanoseconds.
func took(start time.Time) string {
	d := time.Since(start)
	if d < time.Second {
		return d.Round(time.Millisecond).String()
	}
	return d.Round(100 * time.Millisecond).String()
}
