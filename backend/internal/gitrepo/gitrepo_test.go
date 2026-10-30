package gitrepo

import (
	"context"
	"encoding/base64"
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
	c, err := New(git, t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestNew_MakesTheRootAbsolute(t *testing.T) {
	c, err := New("git", "repos", "")
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(c.Root()) || filepath.Base(c.Root()) != "repos" {
		t.Fatalf("Root = %q, want an absolute path ending in repos", c.Root())
	}
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
	// Bare: HEAD at the top, no working tree.
	if _, err := os.Stat(filepath.Join(dir, "HEAD")); err != nil {
		t.Fatalf("no HEAD at the clone's root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "a.txt")); !os.IsNotExist(err) {
		t.Fatal("a working tree was checked out")
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
	// The object store holds the new file at the new head: what phase 2 reads.
	if got := gitRun(t, dir, "show", head2+":b.txt"); got != "second" {
		t.Fatalf("show at head = %q", got)
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
		t.Fatal("clone still there after Remove")
	}
}

func TestEnsureCloned_ReplacesACutOffClone(t *testing.T) {
	ctx := context.Background()
	c := client(t)
	remote := bareRemote(t)
	dir, _ := c.Dir("PROJ", "repo")
	// What a clone killed mid-transfer leaves behind: HEAD written, no refs.
	if err := os.MkdirAll(filepath.Join(dir, "refs", "heads"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "junk"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if origin, err := c.OriginURL(ctx, dir); origin != "" || err != nil {
		t.Fatalf("cut-off clone reported an origin: %q %v", origin, err)
	}
	if err := c.EnsureCloned(ctx, dir, remote, Access{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "junk")); !os.IsNotExist(err) {
		t.Fatal("junk survived: the cut-off directory was not replaced")
	}
	if !c.complete(ctx, dir) {
		t.Fatal("no complete clone after EnsureCloned")
	}
}

func TestEnsureCloned_PersistsNoCredential(t *testing.T) {
	// A token with a file:// remote is not sent anywhere (env is nil for a
	// non-http URL); the point is that nothing credentialed is ever handed
	// to git as a URL, so the persisted origin is the bare one.
	ctx := context.Background()
	c := client(t)
	remote := bareRemote(t)
	dir, _ := c.Dir("PROJ", "repo")
	if err := c.EnsureCloned(ctx, dir, remote, Access{Token: "secret", Auth: "basic", User: "me"}); err != nil {
		t.Fatal(err)
	}
	cfg, _ := os.ReadFile(filepath.Join(dir, "config"))
	if strings.Contains(string(cfg), "secret") || strings.Contains(string(cfg), "me@") {
		t.Fatal("credential in the clone's config")
	}
	origin, _ := c.OriginURL(ctx, dir)
	if origin != remote {
		t.Fatalf("origin = %q, want %q", origin, remote)
	}
}

func TestDir_RefusesEscapes(t *testing.T) {
	c, _ := New("git", "/base", "")
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

func TestAccess_HeadersOnly(t *testing.T) {
	t.Setenv("GIT_CONFIG_COUNT", "")
	url := "https://forge.example.invalid/scm/shop/ui.git"

	bearer := Access{Token: "secret", Auth: "bearer"}
	want := "GIT_CONFIG_COUNT=1\nGIT_CONFIG_KEY_0=http.extraHeader\nGIT_CONFIG_VALUE_0=Authorization: Bearer secret"
	if got := strings.Join(bearer.env(url), "\n"); got != want {
		t.Errorf("bearer env = %q, want %q", got, want)
	}

	basic := Access{Token: "ghp_secret", Auth: "basic"}
	cred := base64.StdEncoding.EncodeToString([]byte("x-access-token:ghp_secret"))
	if got := basic.env("https://github.com/acme/repo.git"); got[2] != "GIT_CONFIG_VALUE_0=Authorization: Basic "+cred {
		t.Errorf("basic env = %v, want x-access-token as the default user", got)
	}
	basic.User = "x-token-auth"
	cred = base64.StdEncoding.EncodeToString([]byte("x-token-auth:ghp_secret"))
	if got := basic.env(url); got[2] != "GIT_CONFIG_VALUE_0=Authorization: Basic "+cred {
		t.Errorf("basic env with user = %v", got)
	}

	if env := (Access{}).env(url); env != nil {
		t.Errorf("anonymous env = %v, want nil", env)
	}
	if env := bearer.env("/local/path.git"); env != nil {
		t.Errorf("non-http env = %v, want nil", env)
	}

	t.Setenv("GIT_CONFIG_COUNT", "1")
	if got := bearer.env(url); got[0] != "GIT_CONFIG_COUNT=2" || got[1] != "GIT_CONFIG_KEY_1=http.extraHeader" {
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
		out := Redact(in)
		if strings.Contains(out, "ghp_abc") || strings.Contains(out, "tok@") || strings.Contains(out, "s3cr3t") {
			t.Errorf("Redact(%q) = %q leaks", in, out)
		}
		if !strings.Contains(out, "://REDACTED@") {
			t.Errorf("Redact(%q) = %q has no marker", in, out)
		}
	}
	if got := Redact("fatal: repository 'https://github.com/acme/none.git/' not found"); got != "fatal: repository 'https://github.com/acme/none.git/' not found" {
		t.Errorf("Redact changed a bare URL: %q", got)
	}
}
