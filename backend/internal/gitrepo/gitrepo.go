// Package gitrepo drives the git binary for the clones under the base
// folder, after ../rongo's package of the same name. The binary, not a Go
// library: proven on any repository size, and every rule below was paid
// for there. The token reaches git for one command only (a header or the
// URL of that command) and is never written to disk; a stored remote URL is
// always the bare one.
package gitrepo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// ErrBranchGone is a configured branch the remote no longer has. Named so
// the caller surfaces it loudly: a silent stop would freeze the clone while
// every status looked healthy.
var ErrBranchGone = errors.New("configured branch not found on the remote")

// ErrBadSegment is a key or slug that is not one safe path segment.
var ErrBadSegment = errors.New("not a single path segment")

// Access is what one command needs to reach a remote. Token "" is an
// anonymous remote. Auth is "bearer" (an Authorization header, no username)
// or "basic" (User and Token in the URL of that one command; User defaults
// to x-access-token, which GitHub ignores and Bitbucket DC checks).
type Access struct {
	Token string
	Auth  string
	User  string
}

// Client runs git against checkouts under root.
type Client struct {
	git    string
	root   string
	caFile string
}

// Resolve finds the git binary on PATH; a startup check.
func Resolve() (string, error) {
	p, err := exec.LookPath("git")
	if err != nil {
		return "", errors.New("git not found on PATH")
	}
	return p, nil
}

// New builds a Client over root. caFile, when set, is handed to git as
// GIT_SSL_CAINFO for a forge behind an internal CA.
func New(gitBin, root, caFile string) *Client {
	return &Client{git: gitBin, root: root, caFile: caFile}
}

// Root is the base folder.
func (c *Client) Root() string { return c.root }

// segment is what a key or slug may look like: Bitbucket's charset, the
// tilde of a personal project included. "." and ".." are refused apart.
var segment = regexp.MustCompile(`^[A-Za-z0-9._~-]+$`)

// ValidSegment reports whether s is one safe path segment.
func ValidSegment(s string) bool {
	return s != "" && s != "." && s != ".." && segment.MatchString(s)
}

// ProjectDir is <root>/<key>, validated.
func (c *Client) ProjectDir(key string) (string, error) {
	if !ValidSegment(key) {
		return "", fmt.Errorf("project %q: %w", key, ErrBadSegment)
	}
	return c.under(c.root, filepath.Join(c.root, key))
}

// Dir is <root>/<key>/<slug>, validated: both segments, and the result
// strictly below the project directory (filepath.Rel, never a prefix).
func (c *Client) Dir(key, slug string) (string, error) {
	proj, err := c.ProjectDir(key)
	if err != nil {
		return "", err
	}
	if !ValidSegment(slug) {
		return "", fmt.Errorf("repository %q: %w", slug, ErrBadSegment)
	}
	return c.under(proj, filepath.Join(proj, slug))
}

func (c *Client) under(parent, dir string) (string, error) {
	rel, err := filepath.Rel(parent, dir)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("%q escapes %q: %w", dir, parent, ErrBadSegment)
	}
	return dir, nil
}

// OriginURL is the remote a checkout was made from, "" when there is no
// checkout. The name of a directory is a label; the origin is the identity,
// so a changed clone URL is caught before the old code answers under the
// new name.
func (c *Client) OriginURL(ctx context.Context, dir string) (string, error) {
	if !hasCheckout(dir) {
		return "", nil
	}
	out, err := c.run(ctx, dir, nil, "remote", "get-url", "origin")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// hasCheckout is a directory with a .git/HEAD: a clone that got as far as
// writing its repository. A directory without one is a cut-off clone.
func hasCheckout(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git", "HEAD"))
	return err == nil
}

// EnsureCloned clones when dir holds no checkout. Full history. A directory
// that exists without a checkout (a clone cut off by a shutdown) is removed
// first: git refuses to clone into a non-empty one. git persists the clone
// URL, credentials and all, as remote.origin.url, so the bare URL is written
// back right after; every later remote command passes its own URL.
func (c *Client) EnsureCloned(ctx context.Context, dir, cloneURL string, a Access) error {
	if hasCheckout(dir) {
		return nil
	}
	if _, err := os.Stat(dir); err == nil {
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("remove cut-off clone: %w", err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o750); err != nil {
		return fmt.Errorf("create project directory: %w", err)
	}
	if _, err := c.run(ctx, c.root, a.env(cloneURL), "clone", "--quiet", a.remoteURL(cloneURL), dir); err != nil {
		return err
	}
	if a.Token != "" {
		if _, err := c.run(ctx, dir, nil, "remote", "set-url", "origin", cloneURL); err != nil {
			return err
		}
	}
	return nil
}

// DefaultBranch asks the remote what HEAD points at. Never assume master.
func (c *Client) DefaultBranch(ctx context.Context, cloneURL string, a Access) (string, error) {
	out, err := c.run(ctx, c.root, a.env(cloneURL), "ls-remote", "--symref", a.remoteURL(cloneURL), "HEAD")
	if err != nil {
		return "", err
	}
	// "ref: refs/heads/main\tHEAD"
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "ref:") {
			continue
		}
		if fields := strings.Fields(line); len(fields) >= 2 {
			return strings.TrimPrefix(fields[1], "refs/heads/"), nil
		}
	}
	return "", errors.New("remote reported no default branch")
}

// Fetch updates the remote-tracking refs and the tags, pruning what the
// remote dropped, by explicit refspecs rather than the persisted config.
func (c *Client) Fetch(ctx context.Context, dir, cloneURL string, a Access) error {
	_, err := c.run(ctx, dir, a.env(cloneURL), "fetch", "--quiet", "--prune", a.remoteURL(cloneURL),
		"+refs/heads/*:refs/remotes/origin/*", "+refs/tags/*:refs/tags/*")
	return err
}

// HeadSHA is the commit the branch points at on the remote-tracking side.
// An absent ref is ErrBranchGone; an unreadable checkout is its own error.
func (c *Client) HeadSHA(ctx context.Context, dir, branch string) (string, error) {
	out, err := c.run(ctx, dir, nil, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+branch)
	if err != nil {
		if exitCode(err) != 1 {
			return "", err
		}
		if _, checkErr := c.run(ctx, dir, nil, "rev-parse", "--git-dir"); checkErr != nil {
			return "", fmt.Errorf("repository unreadable: %w", checkErr)
		}
	}
	if sha := strings.TrimSpace(out); sha != "" {
		return sha, nil
	}
	return "", fmt.Errorf("branch %q: %w", branch, ErrBranchGone)
}

// Remove deletes one checkout. The segments are validated again here even
// though the API did: the operation is RemoveAll and the cost of being wrong
// is deleting outside the base.
func (c *Client) Remove(key, slug string) error {
	dir, err := c.Dir(key, slug)
	if err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

// RemoveProject deletes a whole project folder, clones and all.
func (c *Client) RemoveProject(key string) error {
	dir, err := c.ProjectDir(key)
	if err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

// DirSize is the bytes under dir, 0 when it does not exist.
func DirSize(dir string) int64 {
	var n int64
	_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			n += info.Size()
		}
		return nil
	})
	return n
}

// run is one git command to completion: the ownership exemption for dir,
// no prompting, the CA file, and extra for the remote's auth. stderr is
// redacted before it reaches an error, since git quotes the remote it
// failed against, credentials included.
func (c *Client) run(ctx context.Context, dir string, extra []string, args ...string) (string, error) {
	full := append([]string{"-c", "safe.directory=" + dir}, args...)
	cmd := exec.CommandContext(ctx, c.git, full...) //nolint:gosec // argv, no shell; paths come from validated segments
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if c.caFile != "" {
		cmd.Env = append(cmd.Env, "GIT_SSL_CAINFO="+c.caFile)
	}
	cmd.Env = append(cmd.Env, extra...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(redact(stderr.String())))
	}
	return stdout.String(), nil
}

func exitCode(err error) int {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	return -1
}

// remoteURL is the URL one command reaches the remote at: bare for bearer
// or anonymous, with the credentials for basic.
func (a Access) remoteURL(cloneURL string) string {
	if a.Token == "" || a.Auth == "bearer" {
		return cloneURL
	}
	u, err := url.Parse(cloneURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return cloneURL
	}
	user := a.User
	if user == "" {
		user = "x-access-token"
	}
	u.User = url.UserPassword(user, a.Token)
	return u.String()
}

// env is the Authorization header for bearer, through git's environment
// config rather than -c (argv shows in the process list), appended after
// whatever GIT_CONFIG_* the process already carries.
func (a Access) env(cloneURL string) []string {
	if a.Token == "" || a.Auth != "bearer" || !isHTTPURL(cloneURL) {
		return nil
	}
	n, _ := strconv.Atoi(os.Getenv("GIT_CONFIG_COUNT"))
	i := strconv.Itoa(n)
	return []string{
		"GIT_CONFIG_COUNT=" + strconv.Itoa(n+1),
		"GIT_CONFIG_KEY_" + i + "=http.extraHeader",
		"GIT_CONFIG_VALUE_" + i + "=Authorization: Bearer " + a.Token,
	}
}

func isHTTPURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http")
}

// credentialInURL matches the userinfo of any URL-shaped substring.
var credentialInURL = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.\-]*://)[^/@\s]+@`)

// redact strips the userinfo from anything URL-shaped in text about to be
// logged. On the raw string, not via url.Parse: git wraps the remote in
// quotes and appends a colon, so the field is not a parseable URL.
func redact(s string) string {
	return credentialInURL.ReplaceAllString(s, "${1}REDACTED@")
}

// ShortSHA is the seven-character form for log lines.
func ShortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
