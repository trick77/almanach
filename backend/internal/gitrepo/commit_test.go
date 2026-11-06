package gitrepo

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A file committed on top of the clone's head reaches the remote as a
// branch of its own; the clone stays bare, its refs where they were, and
// the same content again commits nothing.
func TestCommitFilePush_LocalRemote(t *testing.T) {
	ctx := context.Background()
	c := client(t)
	remote := bareRemote(t)
	dir, err := c.Dir("PROJ", "repo")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.EnsureCloned(ctx, dir, remote, Access{}); err != nil {
		t.Fatal(err)
	}
	if err := c.Fetch(ctx, dir, remote, Access{}); err != nil {
		t.Fatal(err)
	}
	head, err := c.HeadSHA(ctx, dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	who := Author{Name: "Almanach", Email: "almanach@example.invalid"}
	content := []byte("## Projekt\n\nWas es ist.\n")
	sha, changed, err := c.CommitFile(ctx, dir, head, ".agents/PROJECT.md", content, "Update .agents/PROJECT.md", who)
	if err != nil || !changed || sha == head {
		t.Fatalf("CommitFile = %q, %v, %v", sha, changed, err)
	}
	if got := gitRun(t, dir, "rev-parse", sha+"^"); got != head {
		t.Fatalf("parent = %q, want the head %q", got, head)
	}
	if got := gitRun(t, dir, "show", sha+":a.txt"); got != "first" {
		t.Fatalf("a.txt at the commit = %q: the rest of the tree must stay", got)
	}
	if got := gitRun(t, dir, "log", "-1", "--format=%an <%ae>", sha); got != "Almanach <almanach@example.invalid>" {
		t.Fatalf("author = %q", got)
	}
	if got, err := c.AuthorEmail(ctx, dir, sha); err != nil || got != "almanach@example.invalid" {
		t.Fatalf("AuthorEmail = %q, %v", got, err)
	}
	if got, err := c.AuthorEmail(ctx, dir, strings.Repeat("e", 40)); err != nil || got != "" {
		t.Fatalf("AuthorEmail of a commit the clone lacks = %q, %v; want none", got, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "index")); !os.IsNotExist(err) {
		t.Fatal("the clone got an index")
	}
	const branch = "feature/repository-description-update"
	if got, err := c.RemoteBranch(ctx, dir, remote, branch, Access{}); err != nil || got != "" {
		t.Fatalf("RemoteBranch before = %q, %v; want none", got, err)
	}
	if err := c.Push(ctx, dir, remote, sha, branch, "", Access{}); err != nil {
		t.Fatal(err)
	}
	if got, err := c.RemoteBranch(ctx, dir, remote, branch, Access{}); err != nil || got != sha {
		t.Fatalf("RemoteBranch after = %q, %v; want %q", got, err, sha)
	}
	if got := gitRun(t, remote, "rev-parse", "refs/heads/main"); got != head {
		t.Fatalf("remote main moved to %q", got)
	}
	// Leased: a branch that is no longer where the writer left it, or one
	// that exists where none was expected, is never overwritten.
	if err := c.Push(ctx, dir, remote, head, branch, "", Access{}); err == nil {
		t.Fatal("push expecting no branch overwrote one")
	}
	if err := c.Push(ctx, dir, remote, head, branch, head, Access{}); err == nil {
		t.Fatal("push with a stale lease overwrote the branch")
	}
	// Where it was left: replaced, even by a commit that is no descendant.
	if err := c.Push(ctx, dir, remote, head, branch, sha, Access{}); err != nil {
		t.Fatalf("leased push: %v", err)
	}
	if got := gitRun(t, remote, "rev-parse", "refs/heads/"+branch); got != head {
		t.Fatalf("remote branch = %q, want %q", got, head)
	}
	// A base that already holds the content: nothing to commit.
	again, changed, err := c.CommitFile(ctx, dir, sha, ".agents/PROJECT.md", content, "x", who)
	if err != nil || changed || again != sha {
		t.Fatalf("same content = %q, %v, %v; want the base, unchanged", again, changed, err)
	}
}

func TestCommitFilePush_RefusesBadInput(t *testing.T) {
	ctx := context.Background()
	c := client(t)
	dir := t.TempDir()
	sha := "0123456789012345678901234567890123456789"
	for _, p := range []string{"", "/abs", "../x", ".git/config", "-x", "a//b"} {
		if _, _, err := c.CommitFile(ctx, dir, sha, p, nil, "m", Author{}); err == nil {
			t.Errorf("CommitFile(%q) accepted", p)
		}
	}
	if _, _, err := c.CommitFile(ctx, dir, "HEAD", "a", nil, "m", Author{}); err == nil {
		t.Error("CommitFile took a base that is no commit id")
	}
	for _, b := range []string{"", "-f", "a..b", "a b", "a:b", "/a"} {
		if err := c.Push(ctx, dir, "https://forge.example.invalid/x.git", sha, b, "", Access{}); err == nil {
			t.Errorf("Push(branch %q) accepted", b)
		}
	}
	if err := c.Push(ctx, dir, "https://forge.example.invalid/x.git", sha, "b", "--force", Access{}); err == nil {
		t.Error("Push took a lease that is no commit id")
	}
}
