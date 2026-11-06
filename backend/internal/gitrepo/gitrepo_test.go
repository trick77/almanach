package gitrepo

import (
	"context"
	"encoding/base64"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
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
	// The tests' remotes are local bare repositories, never a source's.
	c.protocols = remoteProtocols + ":file"
	return c
}

// A source's clone URL reaches a forge over https or http only: a local
// path or file:// remote, another clone on this disk, is refused by git.
func TestEnsureCloned_RefusesALocalRemote(t *testing.T) {
	git, err := Resolve()
	if err != nil {
		t.Skip(err)
	}
	c, err := New(git, t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	remote := bareRemote(t)
	for _, url := range []string{remote, "file://" + remote, "ssh://git@forge.example.invalid/x.git", "git@forge.example.invalid:x.git"} {
		dir, _ := c.Dir("PROJ", "repo")
		if err := c.EnsureCloned(context.Background(), dir, url, Access{}); err == nil || !strings.Contains(err.Error(), "not allowed") {
			t.Errorf("EnsureCloned(%s) = %v, want git to refuse the transport", url, err)
		}
	}
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
	// What a clone killed mid-transfer leaves behind: a repository git had
	// set up (HEAD, config, even refs), without the marker written after
	// git returned.
	gitRun(t, t.TempDir(), "clone", "-q", "--bare", remote, dir)
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

// A git child gets the path, the home, the temp folders, the proxies, the
// TLS roots and GIT_*, never the process's secrets.
func TestCommand_EnvironmentAllowlisted(t *testing.T) {
	c := client(t)
	t.Setenv("BACKEND_SESSION_SECRET", "s3ss10n")
	t.Setenv("LLMWIRE_API_KEY", "sk-xyz")
	t.Setenv("GIT_CONFIG_COUNT", "0")
	t.Setenv("https_proxy", "http://proxy.example.invalid:3128")
	env := c.command(context.Background(), c.root, []string{"GIT_CONFIG_VALUE_9=x"}, "status").Env
	joined := strings.Join(env, "\n")
	for _, secret := range []string{"s3ss10n", "sk-xyz", "BACKEND_SESSION_SECRET", "LLMWIRE_API_KEY"} {
		if strings.Contains(joined, secret) {
			t.Errorf("child env carries %q:\n%s", secret, joined)
		}
	}
	for _, want := range []string{"GIT_CONFIG_COUNT=0", "https_proxy=http://proxy.example.invalid:3128", "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_VALUE_9=x"} {
		if !strings.Contains(joined, want) {
			t.Errorf("child env lacks %q:\n%s", want, joined)
		}
	}
	// PATH itself, not GIT_EXEC_PATH: git finds its helpers by it.
	if !slices.ContainsFunc(env, func(kv string) bool { return strings.HasPrefix(kv, "PATH=") }) {
		t.Errorf("child env lacks PATH:\n%s", joined)
	}
}

// A remote that never answers is cut by the client's timeout, the error
// saying so and naming the setting: a clone's own, every other command's;
// git's stall guard rides on every command.
func TestRemote_TimesOut(t *testing.T) {
	c := client(t).WithTimeouts(500*time.Millisecond, time.Hour)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip(err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			// Hold the connection open, answer nothing.
			defer func() { _ = conn.Close() }()
		}
	}()
	for _, proxy := range []string{"http_proxy", "https_proxy", "all_proxy", "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY"} {
		t.Setenv(proxy, "")
	}
	start := time.Now()
	_, err = c.DefaultBranch(context.Background(), "http://"+ln.Addr().String()+"/x.git", Access{})
	if err == nil || !strings.Contains(err.Error(), "cut after 500ms (BACKEND_GIT_TIMEOUT)") || strings.Count(err.Error(), "ls-remote") != 1 {
		t.Fatalf("ls-remote against a silent remote: err = %v, want the timeout named once", err)
	}
	// The whole process group is terminated at the deadline: git-remote-http
	// goes with git, and the pipes close well inside waitDelay.
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("took %s, want the timeout and little more", took)
	}
	// A clone is under its own bound, the fetch's left out.
	dir, err := c.Dir("tools", "svc")
	if err != nil {
		t.Fatal(err)
	}
	err = c.WithTimeouts(time.Hour, 500*time.Millisecond).EnsureCloned(context.Background(), dir, "http://"+ln.Addr().String()+"/x.git", Access{})
	if err == nil || !strings.Contains(err.Error(), "cut after 500ms (BACKEND_GIT_CLONE_TIMEOUT)") {
		t.Fatalf("clone from a silent remote: err = %v, want the clone timeout named", err)
	}
	// A caller's own deadline is not the client's timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, err := c.WithTimeouts(time.Hour, time.Hour).DefaultBranch(ctx, "http://"+ln.Addr().String()+"/x.git", Access{}); err == nil || strings.Contains(err.Error(), "BACKEND_GIT_TIMEOUT") {
		t.Errorf("caller's deadline: err = %v, want no claim of the client's timeout", err)
	}
	args := strings.Join(c.command(context.Background(), c.root, nil, "fetch").Args, " ")
	if !strings.Contains(args, "-c http.lowSpeedLimit=1 -c http.lowSpeedTime=300 fetch") {
		t.Errorf("git args lack the stall guard before the command: %s", args)
	}
}

// A group gone by the time the deadline fires is os.ErrProcessDone, which
// exec ignores, not an error that fails a command that exited 0.
func TestTerminateGroup_GoneIsDone(t *testing.T) {
	c := client(t)
	cmd := c.command(context.Background(), c.root, nil, "--version")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if err := terminateGroup(cmd.Process.Pid); !errors.Is(err, os.ErrProcessDone) {
		t.Errorf("terminateGroup of a reaped group = %v, want os.ErrProcessDone", err)
	}
}

// A group that ignores SIGTERM, the leader and its child alike, is killed
// after killGrace, not left holding what it held.
func TestTerminateGroup_KillsWhatIgnoresTerm(t *testing.T) {
	old := killGrace
	killGrace = 100 * time.Millisecond
	t.Cleanup(func() { killGrace = old })
	cmd := exec.Command("sh", "-c", "trap '' TERM; sleep 30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Skip(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	time.Sleep(100 * time.Millisecond) // let sh set the trap
	if err := terminateGroup(cmd.Process.Pid); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		t.Fatal("a group ignoring SIGTERM outlived killGrace")
	}
}

func TestAccess_HeadersOnly(t *testing.T) {
	t.Setenv("GIT_CONFIG_COUNT", "")
	url := "https://forge.example.invalid/scm/shop/ui.git"

	bearer := Access{Token: "secret", Auth: "bearer", Origin: "https://forge.example.invalid"}
	want := "GIT_CONFIG_COUNT=1\nGIT_CONFIG_KEY_0=http.extraHeader\nGIT_CONFIG_VALUE_0=Authorization: Bearer secret"
	if got := strings.Join(bearer.env(url), "\n"); got != want {
		t.Errorf("bearer env = %q, want %q", got, want)
	}

	basic := Access{Token: "ghp_secret", Auth: "basic", Origin: "https://github.com"}
	cred := base64.StdEncoding.EncodeToString([]byte("x-access-token:ghp_secret"))
	if got := basic.env("https://github.com/acme/repo.git"); got[2] != "GIT_CONFIG_VALUE_0=Authorization: Basic "+cred {
		t.Errorf("basic env = %v, want x-access-token as the default user", got)
	}
	basic.User, basic.Origin = "x-token-auth", "https://forge.example.invalid"
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

// A token goes to its own origin only: another host, another port, another
// scheme or no origin at all get no header. Case does not matter.
func TestAccess_TokenStaysOnItsOrigin(t *testing.T) {
	t.Setenv("GIT_CONFIG_COUNT", "")
	gh := Access{Token: "ghp_secret", Auth: "basic", Origin: "https://github.com"}
	if env := gh.env("HTTPS://GitHub.COM/acme/repo.git"); env == nil {
		t.Error("same origin in another case: no header")
	}
	if env := gh.env("https://jan@github.com/acme/repo.git"); env == nil {
		t.Error("userinfo is no part of the origin: no header")
	}
	if env := gh.env("https://github.com:443/acme/repo.git"); env == nil {
		t.Error("the default port is the same origin: no header")
	}
	for _, off := range []string{
		"https://gitlab.com/acme/repo.git",
		"https://github.com.evil.example/acme/repo.git",
		"https://github.com:8443/acme/repo.git",
		"http://github.com/acme/repo.git",
	} {
		if env := gh.env(off); env != nil {
			t.Errorf("env(%q) = %v, want nil", off, env)
		}
	}
	if env := (Access{Token: "ghp_secret", Auth: "bearer"}).env("https://github.com/acme/repo.git"); env != nil {
		t.Errorf("token without an origin: env = %v, want nil", env)
	}
}

// Authorization, the one source of the header value, refuses a target off
// the origin too: the Bitbucket listing gets "" for one, as git does.
func TestAccess_AuthorizationBoundToOrigin(t *testing.T) {
	bb := Access{Token: "pat", Auth: "bearer", Origin: "https://forge.example.invalid:443"}
	if got := bb.Authorization("https://FORGE.example.invalid/rest/api/1.0/projects/SHOP/repos"); got != "Bearer pat" {
		t.Errorf("on the origin: %q", got)
	}
	for _, off := range []string{"https://elsewhere.example.invalid/rest", "https://forge.example.invalid:8443/rest", "", "/rest"} {
		if got := bb.Authorization(off); got != "" {
			t.Errorf("Authorization(%q) = %q, want none", off, got)
		}
	}
	if got := (Access{Token: "pat", Auth: "bearer"}).Authorization("https://forge.example.invalid/rest"); got != "" {
		t.Errorf("no origin: %q, want none", got)
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
