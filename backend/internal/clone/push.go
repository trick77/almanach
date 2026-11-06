package clone

import (
	"context"
	"errors"
	"fmt"

	"github.com/trick77/almanach/internal/gitrepo"
	"github.com/trick77/almanach/internal/store"
	"github.com/trick77/almanach/internal/urlorigin"
)

// The one write to a forge: a repository's brief committed on top of its
// default branch in the clone (no checkout) and pushed to a branch of its
// own, with the source's token as a fetch sends it: a header, bound to the
// source's origin, never in a URL or argv. Under the clone's lock, like a
// run's read, so no sync or removal moves the clone under it.

// Writes is what a push needs of gitrepo, on a clone's folder.
type Writes interface {
	HeadSHA(ctx context.Context, dir, branch string) (string, error)
	CommitFile(ctx context.Context, dir, base, path string, content []byte, message string, a gitrepo.Author) (string, bool, error)
	RemoteBranch(ctx context.Context, dir, cloneURL, branch string, a gitrepo.Access) (string, error)
	AuthorEmail(ctx context.Context, dir, sha string) (string, error)
	Push(ctx context.Context, dir, cloneURL, sha, branch, expect string, a gitrepo.Access) error
}

// ErrBranchTaken is a branch on the remote holding a commit Almanach did
// not push there last: someone works on it, and a push would throw that
// away.
var ErrBranchTaken = errors.New("the branch holds commits Almanach did not push: merge or delete it first")

// SourceLookup is what a push reads of a source: its access settings and
// the repository's clone URL and branch.
type SourceLookup interface {
	SourceByKey(ctx context.Context, key string) (store.ProjectSource, bool, error)
	SourceRepoByName(ctx context.Context, key, slug string) (store.SourceRepo, bool, error)
}

// pushAuthor writes the commits Almanach pushes. The address is in a
// reserved domain: no one's mailbox.
var pushAuthor = gitrepo.Author{Name: "Almanach", Email: "almanach@almanach.invalid"}

// WithWrites lets the clones take a push (PushFile).
func (c *Code) WithWrites(w Writes, sources SourceLookup) *Code {
	c.writes, c.sources = w, sources
	return c
}

// PushFile commits content at path on top of the repository's default
// branch as its clone last fetched it and pushes the commit to branch,
// replacing it only where it is Almanach's own (ownBranch; ErrBranchTaken
// otherwise; the push leased to what was read, so none slips in between).
// changed false when the default branch already holds the content: no
// commit, and a branch of its own reset to the default branch. pushed is
// what the branch now holds by this push, "" when nothing was pushed. A
// source without a token, or one whose token is withheld from the clone
// URL's host, fails before git runs: an anonymous push only reads as git's
// bare refusal.
func (c *Code) PushFile(ctx context.Context, key, slug, branch, owned, path string, content []byte, message string) (pushed string, changed bool, err error) {
	if c.writes == nil {
		return "", false, errors.New("pushing is not configured")
	}
	src, ok, err := c.sources.SourceByKey(ctx, key)
	if err != nil {
		return "", false, err
	}
	if !ok {
		return "", false, fmt.Errorf("source %s: %w", key, store.ErrNoRepo)
	}
	repo, ok, err := c.sources.SourceRepoByName(ctx, key, slug)
	if err != nil {
		return "", false, err
	}
	if !ok || repo.ProjectID != src.ProjectID {
		return "", false, fmt.Errorf("repository %s/%s: %w", key, slug, store.ErrNoRepo)
	}
	if repo.Branch == "" || repo.HeadCommit == "" {
		return "", false, errors.New("the clone has no head yet: push once it is synced")
	}
	if !src.HasToken() {
		return "", false, errors.New("the source has no token to push with")
	}
	bind, err := c.p.bindings(ctx, []store.SyncJob{{Source: src, Repo: repo}})
	if err != nil {
		return "", false, err
	}
	b := bind[src.ProjectKey]
	if !urlorigin.Same(repo.CloneURL, b.origin) {
		return "", false, errors.New("token withheld: the clone URL is not on the source's host")
	}
	a, err := c.p.access(src, b.origin)
	if err != nil {
		return "", false, err
	}
	err = c.locked(ctx, key, slug, func(dir string) error {
		var commit string
		base, err := c.writes.HeadSHA(ctx, dir, repo.Branch)
		if err != nil {
			return err
		}
		if commit, changed, err = c.writes.CommitFile(ctx, dir, base, path, content, message, pushAuthor); err != nil {
			return err
		}
		remote, err := c.writes.RemoteBranch(ctx, dir, repo.CloneURL, branch, a)
		if err != nil {
			return err
		}
		mine, err := c.ownBranch(ctx, dir, remote, owned)
		if err != nil {
			return err
		}
		if !changed {
			// The default branch holds the file already: a branch of
			// Almanach's own goes back to it, so an open pull request shows no
			// stale change; none is created.
			if remote == "" || !mine || remote == base {
				return nil
			}
			if err := c.writes.Push(ctx, dir, repo.CloneURL, base, branch, remote, a); err != nil {
				return err
			}
			pushed = base
			return nil
		}
		if !mine {
			return ErrBranchTaken
		}
		if err := c.writes.Push(ctx, dir, repo.CloneURL, commit, branch, remote, a); err != nil {
			return err
		}
		pushed = commit
		return nil
	})
	if err != nil {
		return "", false, err
	}
	return pushed, changed, nil
}

// ownBranch reports whether the remote branch at remote ("" for none) is
// Almanach's to replace: absent, at owned (the commit it recorded pushing
// last), or at a commit this clone holds by Almanach's author, one whose
// push landed but whose record did not (a shutdown, a refused write).
func (c *Code) ownBranch(ctx context.Context, dir, remote, owned string) (bool, error) {
	if remote == "" || remote == owned {
		return true, nil
	}
	email, err := c.writes.AuthorEmail(ctx, dir, remote)
	if err != nil {
		return false, err
	}
	return email == pushAuthor.Email, nil
}
