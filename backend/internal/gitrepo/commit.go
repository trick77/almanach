package gitrepo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// The one write Almanach makes to a forge: a file committed on top of a
// branch and pushed to a branch of its own. The clone is bare and stays
// so: the commit is built from plumbing (a blob, a tree through a
// temporary index, commit-tree), never a checkout, and nothing of the
// clone's own refs moves. The push carries the source's token as every
// fetch does, a header for one command, and a lease: it replaces only what
// the writer last pushed there.

// Author is who a commit is written by, committer too.
type Author struct {
	Name  string
	Email string
}

// branchName is what a pushed branch may be called: plain path segments,
// never an option or a refspec of its own.
var branchName = regexp.MustCompile(`^[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*$`)

// filePath is a repository path a commit may set: relative, plain
// segments, no .git.
func filePath(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || strings.HasPrefix(p, "-") {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if !ValidSegment(seg) || strings.EqualFold(seg, ".git") {
			return false
		}
	}
	return true
}

// CommitFile writes a commit on top of base that sets path to content,
// in the clone at dir, and returns it; changed false, and base itself,
// when base already holds exactly that content.
func (c *Client) CommitFile(ctx context.Context, dir, base, path string, content []byte, message string, a Author) (sha string, changed bool, err error) {
	if err := checkSHA(base); err != nil {
		return "", false, err
	}
	if !filePath(path) {
		return "", false, fmt.Errorf("path %q: %w", path, ErrBadSegment)
	}
	env := inRepo(dir)
	out, err := c.runIn(ctx, dir, bytes.NewReader(content), env, "hash-object", "-w", "--stdin")
	if err != nil {
		return "", false, err
	}
	blob := strings.TrimSpace(string(out))
	// The file as base has it: exit 1 with nothing is a path base lacks.
	old, err := c.run(ctx, dir, env, "rev-parse", "--verify", "--quiet", base+":"+path)
	if err != nil && exitCode(err) != 1 {
		return "", false, err
	}
	if strings.TrimSpace(old) == blob {
		return base, false, nil
	}
	// A temporary index: the bare clone has none, and none is left behind.
	tmp, err := os.MkdirTemp("", "almanach-index-")
	if err != nil {
		return "", false, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	idx := []string{"GIT_DIR=" + dir, "GIT_INDEX_FILE=" + filepath.Join(tmp, "index")}
	if _, err := c.run(ctx, dir, idx, "read-tree", base); err != nil {
		return "", false, err
	}
	if _, err := c.run(ctx, dir, idx, "update-index", "--add", "--cacheinfo", "100644,"+blob+","+path); err != nil {
		return "", false, err
	}
	tree, err := c.run(ctx, dir, idx, "write-tree")
	if err != nil {
		return "", false, err
	}
	who := []string{"GIT_DIR=" + dir,
		"GIT_AUTHOR_NAME=" + a.Name, "GIT_AUTHOR_EMAIL=" + a.Email,
		"GIT_COMMITTER_NAME=" + a.Name, "GIT_COMMITTER_EMAIL=" + a.Email}
	commit, err := c.run(ctx, dir, who, "commit-tree", strings.TrimSpace(tree), "-p", base, "-m", message)
	if err != nil {
		return "", false, err
	}
	return strings.TrimSpace(commit), true, nil
}

func checkBranch(branch string) error {
	if !branchName.MatchString(branch) || strings.Contains(branch, "..") {
		return errors.New("branch " + branch + ": not a plain branch name")
	}
	return nil
}

// AuthorEmail is the author address of commit sha in the clone at dir, ""
// when the clone does not hold it (a commit pushed from elsewhere).
func (c *Client) AuthorEmail(ctx context.Context, dir, sha string) (string, error) {
	if err := checkSHA(sha); err != nil {
		return "", err
	}
	env := inRepo(dir)
	if _, err := c.run(ctx, dir, env, "cat-file", "-e", sha+"^{commit}"); err != nil {
		if exitCode(err) > 0 {
			return "", c.readable(ctx, dir)
		}
		return "", err
	}
	out, err := c.run(ctx, dir, env, "log", "-1", "--format=%ae", sha)
	return strings.TrimSpace(out), err
}

// RemoteBranch is the commit branch points at on the remote at cloneURL,
// "" when the remote has no such branch.
func (c *Client) RemoteBranch(ctx context.Context, dir, cloneURL, branch string, a Access) (string, error) {
	if err := checkBranch(branch); err != nil {
		return "", err
	}
	out, err := c.remote(ctx, c.fetchBound(), dir, a.env(cloneURL), "ls-remote", cloneURL, "refs/heads/"+branch)
	if err != nil {
		return "", err
	}
	if f := strings.Fields(out); len(f) > 0 {
		return f[0], nil
	}
	return "", nil
}

// Push sets branch on the remote at cloneURL to sha, only while the remote
// still has it at expect ("": no such branch), whatever sha's relation to
// it: the lease, so a commit pushed there since by anyone else is never
// overwritten.
func (c *Client) Push(ctx context.Context, dir, cloneURL, sha, branch, expect string, a Access) error {
	if err := checkSHA(sha); err != nil {
		return err
	}
	if expect != "" {
		if err := checkSHA(expect); err != nil {
			return err
		}
	}
	if err := checkBranch(branch); err != nil {
		return err
	}
	ref := "refs/heads/" + branch
	_, err := c.remote(ctx, c.fetchBound(), dir, a.env(cloneURL), "push", "--quiet", "--no-verify", "--force-with-lease="+ref+":"+expect, cloneURL, sha+":"+ref)
	return err
}
