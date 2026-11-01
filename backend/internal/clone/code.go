package clone

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/trick77/almanach/internal/baseline"
	"github.com/trick77/almanach/internal/consolidate"
	"github.com/trick77/almanach/internal/gitrepo"
	"github.com/trick77/almanach/internal/redact"
	"github.com/trick77/almanach/internal/store"
)

// The clones as a baseline run reads them: every read under the poller's
// disk lock, so a removal or a re-clone never deletes a clone mid-read, and
// the lock held for the read only, never across a model call.

// Reads is what a run needs of gitrepo, on a clone's folder.
type Reads interface {
	ListEntries(ctx context.Context, dir, sha string) ([]gitrepo.Entry, error)
	ChangedEntries(ctx context.Context, dir, fromSHA, toSHA string) ([]gitrepo.Entry, error)
	IsAncestor(ctx context.Context, dir, ancestor, descendant string) (bool, error)
	HasCommit(ctx context.Context, dir, sha string) (bool, error)
	CommitTime(ctx context.Context, dir, sha string) (time.Time, error)
	NewReader(ctx context.Context, dir string) (*gitrepo.Reader, error)
}

// Code reads the clones for the baseline worker.
type Code struct {
	p     *Poller
	reads Reads
}

// NewCode is the poller's clones for reading.
func NewCode(p *Poller, reads Reads) *Code { return &Code{p: p, reads: reads} }

// lockWait is how long a read waits for the disk lock before it answers
// busy: the poller holds the lock through a whole clone or fetch, minutes on
// a large repository, and the single worker must not stand behind it (the
// notes of every other repository would wait too).
var lockWait = 2 * time.Second

// locked runs fn on the clone's folder under the disk lock, or answers
// consolidate.ErrCloneBusy when the poller keeps it past lockWait.
func (c *Code) locked(ctx context.Context, key, slug string, fn func(dir string) error) error {
	// Any name the repository ever had finds its folder, named by ids: a
	// run that pinned the old slug reads on after a rename.
	pid, rid, ok, err := c.p.Store.RepoIDs(ctx, key, slug)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("repository %s/%s: %w", key, slug, store.ErrNoRepo)
	}
	dir, err := c.p.Dir(pid, rid)
	if err != nil {
		return err
	}
	deadline := time.Now().Add(lockWait)
	for !c.p.disk.TryLock() {
		if time.Now().After(deadline) {
			return consolidate.ErrCloneBusy
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	defer c.p.disk.Unlock()
	return fn(dir)
}

// Tree is every entry at sha.
func (c *Code) Tree(ctx context.Context, key, slug, sha string) ([]gitrepo.Entry, error) {
	var out []gitrepo.Entry
	err := c.locked(ctx, key, slug, func(dir string) (err error) {
		out, err = c.reads.ListEntries(ctx, dir, sha)
		return err
	})
	return out, err
}

// Diff is what changed between two commits.
func (c *Code) Diff(ctx context.Context, key, slug, from, to string) ([]gitrepo.Entry, error) {
	var out []gitrepo.Entry
	err := c.locked(ctx, key, slug, func(dir string) (err error) {
		out, err = c.reads.ChangedEntries(ctx, dir, from, to)
		return err
	})
	return out, err
}

// Contains reports whether the clone holds ancestor and sha reaches it.
func (c *Code) Contains(ctx context.Context, key, slug, ancestor, sha string) (bool, error) {
	var ok bool
	err := c.locked(ctx, key, slug, func(dir string) error {
		has, err := c.reads.HasCommit(ctx, dir, ancestor)
		if err != nil || !has {
			return err
		}
		ok, err = c.reads.IsAncestor(ctx, dir, ancestor, sha)
		return err
	})
	return ok, err
}

// CommitTime is sha's committer time.
func (c *Code) CommitTime(ctx context.Context, key, slug, sha string) (time.Time, error) {
	var at time.Time
	err := c.locked(ctx, key, slug, func(dir string) (err error) {
		at, err = c.reads.CommitTime(ctx, dir, sha)
		return err
	})
	return at, err
}

// ReadFiles reads files at sha through one batch reader. A file over
// FileMaxBytes, gone or binary is left out; a broken reader is an error.
// Every body passes redact.File: this is the one place code is read for a
// model, so a credential value never leaves the machine.
func (c *Code) ReadFiles(ctx context.Context, key, slug, sha string, files []baseline.File) (map[string][]byte, error) {
	out := make(map[string][]byte, len(files))
	err := c.locked(ctx, key, slug, func(dir string) error {
		if len(files) == 0 {
			return nil
		}
		r, err := c.reads.NewReader(ctx, dir)
		if err != nil {
			return err
		}
		defer func() { _ = r.Close() }()
		for _, f := range files {
			body, err := r.ReadFile(sha, f.Path, baseline.FileMaxBytes)
			// A step cut off, or a reader that broke, is no file left out:
			// a patch written from code it never saw must not follow.
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if errors.Is(err, gitrepo.ErrReaderBroken) || errors.Is(err, gitrepo.ErrBadSHA) {
				return err
			}
			// Too large, gone at this commit, not a file, or binary: left out.
			if err != nil || baseline.Binary(body) {
				continue
			}
			body, ok := redact.File(f.Path, body)
			if !ok {
				c.p.Log.InfoContext(ctx, "baseline: Secret manifest left out", "project", key, "repo", slug, "path", f.Path)
				continue
			}
			out[f.Path] = body
		}
		return nil
	})
	return out, err
}
