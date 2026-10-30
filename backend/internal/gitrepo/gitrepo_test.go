package gitrepo

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// bareRemote is a bare repository on branch main with one commit, the
// remote every test clones from. No network anywhere.
func bareRemote(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	work := filepath.Join(base, "work")
	gitRun(t, base, "init", "-q", "-b", "main", work)
	if err := os.WriteFile(filepath.Join(work, "a.txt"), []byte("first\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, work, "add", "a.txt")
	gitRun(t, work, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-q", "-m", "first")
	bare := filepath.Join(base, "remote.git")
	gitRun(t, base, "clone", "-q", "--bare", work, bare)
	gitRun(t, bare, "symbolic-ref", "HEAD", "refs/heads/main")
	return bare
}

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func client(t *testing.T) *Client {
	t.Helper()
	git, err := Resolve()
	if err != nil {
		t.Skip(err)
	}
	return New(git, t.TempDir(), "")
}

func TestCloneFetchHead_LocalRemote(t *testing.T) {
	ctx := context.Background()
	c := client(t)
	remote := bareRemote(t)
	dir, err := c.Dir("PROJ", "repo")
	if err != nil {
		t.Fatal(err)
	}
	if origin, _ := c.OriginURL(ctx, dir); origin != "" {
		t.Fatalf("origin before clone = %q", origin)
	}
	if err := c.EnsureCloned(ctx, dir, remote, Access{}); err != nil {
		t.Fatal(err)
	}
	origin, err := c.OriginURL(ctx, dir)
	if err != nil || origin != remote {
		t.Fatalf("origin = %q, %v; want %q", origin, err, remote)
	}
	branch, err := c.DefaultBranch(ctx, remote, Access{})
	if err != nil || branch != "main" {
		t.Fatalf("DefaultBranch = %q, %v", branch, err)
	}
	if err := c.Fetch(ctx, dir, remote, Access{}); err != nil {
		t.Fatal(err)
	}
	head, err := c.HeadSHA(ctx, dir, "main")
	if err != nil || len(head) != 40 {
		t.Fatalf("HeadSHA = %q, %v", head, err)
	}
	// A second EnsureCloned is a no-op; a new commit upstream moves the head.
	if err := c.EnsureCloned(ctx, dir, remote, Access{}); err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(t.TempDir(), "w")
	gitRun(t, t.TempDir(), "clone", "-q", remote, work)
	if err := os.WriteFile(filepath.Join(work, "b.txt"), []byte("second\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, work, "add", "b.txt")
	gitRun(t, work, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-q", "-m", "second")
	gitRun(t, work, "push", "-q", "origin", "main")
	if err := c.Fetch(ctx, dir, remote, Access{}); err != nil {
		t.Fatal(err)
	}
	head2, _ := c.HeadSHA(ctx, dir, "main")
	if head2 == head {
		t.Fatal("head did not move after a push and fetch")
	}
	if _, err := c.HeadSHA(ctx, dir, "develop"); !errors.Is(err, ErrBranchGone) {
		t.Fatalf("missing branch: %v, want ErrBranchGone", err)
	}
	if DirSize(dir) == 0 {
		t.Fatal("DirSize = 0 for a clone")
	}
	if err := c.Remove("PROJ", "repo"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("checkout still there after Remove")
	}
}

func TestEnsureCloned_ReplacesACutOffClone(t *testing.T) {
	ctx := context.Background()
	c := client(t)
	remote := bareRemote(t)
	dir, _ := c.Dir("PROJ", "repo")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "junk"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := c.EnsureCloned(ctx, dir, remote, Access{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "junk")); !os.IsNotExist(err) {
		t.Fatal("junk survived: the cut-off directory was not replaced")
	}
	if !hasCheckout(dir) {
		t.Fatal("no checkout after EnsureCloned")
	}
}

func TestEnsureCloned_WritesTheBareURLBack(t *testing.T) {
	// A token with a file:// remote is not sent anywhere; the point is
	// that the origin written to .git/config is the bare URL, whatever the
	// clone command was handed.
	ctx := context.Background()
	c := client(t)
	remote := bareRemote(t)
	dir, _ := c.Dir("PROJ", "repo")
	if err := c.EnsureCloned(ctx, dir, remote, Access{Token: "secret", Auth: "bearer"}); err != nil {
		t.Fatal(err)
	}
	cfg, _ := os.ReadFile(filepath.Join(dir, ".git", "config"))
	if strings.Contains(string(cfg), "secret") {
		t.Fatal("token in .git/config")
	}
	origin, _ := c.OriginURL(ctx, dir)
	if origin != remote {
		t.Fatalf("origin = %q, want %q", origin, remote)
	}
}

func TestDir_RefusesEscapes(t *testing.T) {
	c := New("git", "/base", "")
	for _, bad := range [][2]string{{"..", "x"}, {"PROJ", ".."}, {"PROJ", "."}, {"", "x"}, {"PROJ", ""}, {"a/b", "x"}, {"PROJ", "x/y"}, {"PROJ", "x y"}} {
		if _, err := c.Dir(bad[0], bad[1]); !errors.Is(err, ErrBadSegment) {
			t.Errorf("Dir(%q, %q) = %v, want ErrBadSegment", bad[0], bad[1], err)
		}
	}
	for _, ok := range [][2]string{{"PROJ", "shop-ui"}, {"~jan", "x.y_z"}, {"pay.v2", "api"}} {
		if _, err := c.Dir(ok[0], ok[1]); err != nil {
			t.Errorf("Dir(%q, %q) = %v, want ok", ok[0], ok[1], err)
		}
	}
	if err := c.Remove("PROJ", ".."); !errors.Is(err, ErrBadSegment) {
		t.Fatalf("Remove(..) = %v", err)
	}
}

func TestAccess_BasicPutsCredentialsInTheURLOnly(t *testing.T) {
	a := Access{Token: "ghp_secret", Auth: "basic"}
	if got := a.remoteURL("https://github.com/acme/repo.git"); got != "https://x-access-token:ghp_secret@github.com/acme/repo.git" {
		t.Errorf("remoteURL = %q", got)
	}
	a.User = "x-token-auth"
	if got := a.remoteURL("https://forge.example.invalid/scm/shop/ui.git"); got != "https://x-token-auth:ghp_secret@forge.example.invalid/scm/shop/ui.git" {
		t.Errorf("remoteURL with user = %q", got)
	}
	if env := a.env("https://github.com/acme/repo.git"); env != nil {
		t.Errorf("basic env = %v, want nil", env)
	}
	if got := (Access{}).remoteURL("https://github.com/acme/repo.git"); got != "https://github.com/acme/repo.git" {
		t.Errorf("anonymous remoteURL = %q", got)
	}
}

func TestAccess_BearerKeepsTheTokenOutOfTheURL(t *testing.T) {
	t.Setenv("GIT_CONFIG_COUNT", "")
	a := Access{Token: "secret", Auth: "bearer"}
	url := "https://forge.example.invalid/scm/shop/ui.git"
	if got := a.remoteURL(url); got != url {
		t.Errorf("remoteURL = %q, want the bare URL", got)
	}
	want := "GIT_CONFIG_COUNT=1\nGIT_CONFIG_KEY_0=http.extraHeader\nGIT_CONFIG_VALUE_0=Authorization: Bearer secret"
	if got := strings.Join(a.env(url), "\n"); got != want {
		t.Errorf("env = %q, want %q", got, want)
	}
	t.Setenv("GIT_CONFIG_COUNT", "1")
	if got := a.env(url); got[0] != "GIT_CONFIG_COUNT=2" || got[1] != "GIT_CONFIG_KEY_1=http.extraHeader" {
		t.Errorf("env after an existing entry = %v", got)
	}
}

// The three shapes git's authentication failures take, as ../rongo pinned
// them after a leak: every one must lose the credentials.
func TestRedact_RealisticGitErrorShapes(t *testing.T) {
	cases := []string{
		"fatal: Authentication failed for 'https://x-access-token:ghp_abc@github.com/acme/repo.git/'",
		"fatal: unable to access 'https://user:tok@forge.example.invalid/scm/p/r.git/': The requested URL returned error: 403",
		"remote: Invalid username or password.\nfatal: Authentication failed for 'https://x-token-auth:s3cr3t@bitbucket.example.invalid/scm/shop/ui.git/'",
	}
	for _, in := range cases {
		out := redact(in)
		if strings.Contains(out, "ghp_abc") || strings.Contains(out, "tok@") || strings.Contains(out, "s3cr3t") {
			t.Errorf("redact(%q) = %q leaks", in, out)
		}
		if !strings.Contains(out, "://REDACTED@") {
			t.Errorf("redact(%q) = %q has no marker", in, out)
		}
	}
	if got := redact("fatal: repository 'https://github.com/acme/none.git/' not found"); got != "fatal: repository 'https://github.com/acme/none.git/' not found" {
		t.Errorf("redact changed a bare URL: %q", got)
	}
}
