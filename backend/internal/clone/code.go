package clone

import (
	"context"
	"errors"
	"time"

	"github.com/trick77/almanach/internal/baseline"
	"github.com/trick77/almanach/internal/gitrepo"
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

// locked runs fn on the clone's folder under the disk lock.
func (c *Code) locked(key, slug string, fn func(dir string) error) error {
	dir, err := c.p.Git.Dir(key, slug)
	if err != nil {
		return err
	}
	c.p.disk.Lock()
	defer c.p.disk.Unlock()
	return fn(dir)
}

// Tree is every entry at sha.
func (c *Code) Tree(ctx context.Context, key, slug, sha string) ([]gitrepo.Entry, error) {
	var out []gitrepo.Entry
	err := c.locked(key, slug, func(dir string) (err error) {
		out, err = c.reads.ListEntries(ctx, dir, sha)
		return err
	})
	return out, err
}

// Diff is what changed between two commits.
func (c *Code) Diff(ctx context.Context, key, slug, from, to string) ([]gitrepo.Entry, error) {
	var out []gitrepo.Entry
	err := c.locked(key, slug, func(dir string) (err error) {
		out, err = c.reads.ChangedEntries(ctx, dir, from, to)
		return err
	})
	return out, err
}

// Contains reports whether the clone holds ancestor and sha reaches it.
func (c *Code) Contains(ctx context.Context, key, slug, ancestor, sha string) (bool, error) {
	var ok bool
	err := c.locked(key, slug, func(dir string) error {
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
	err := c.locked(key, slug, func(dir string) (err error) {
		at, err = c.reads.CommitTime(ctx, dir, sha)
		return err
	})
	return at, err
}

// ReadFiles reads files at sha through one batch reader. A file over
// FileMaxBytes, gone or binary is left out; a broken reader is an error.
func (c *Code) ReadFiles(ctx context.Context, key, slug, sha string, files []baseline.File) (map[string][]byte, error) {
	out := make(map[string][]byte, len(files))
	err := c.locked(key, slug, func(dir string) error {
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
			if errors.Is(err, gitrepo.ErrReaderBroken) || errors.Is(err, gitrepo.ErrBadSHA) {
				return err
			}
			if err != nil || baseline.Binary(body) {
				continue
			}
			out[f.Path] = body
		}
		return nil
	})
	return out, err
}
