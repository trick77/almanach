// Package gitrepo drives the git binary for the clones under the base
// folder, after ../rongo's package of the same name. The binary, not a Go
// library: proven on any repository size, and every rule below was paid
// for there. The clones are bare: nothing reads a working tree, phase 2
// reads the object store at the recorded head, and a bare clone has no
// tree to go stale. The token reaches git as an Authorization header
// through the environment of one command and is never in a URL, in argv
// or on disk.
package gitrepo

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ErrBranchGone is a configured branch the remote no longer has. Named so
// the caller surfaces it loudly: a silent stop would freeze the clone while
// every status looked healthy.
var ErrBranchGone = errors.New("configured branch not found on the remote")

// ErrBadSegment is a key or slug that is not one safe path segment.
var ErrBadSegment = errors.New("not a single path segment")

// waitDelay bounds how long a cancelled git command may keep its pipes
// open: git's helpers (git-remote-https, index-pack) inherit them and
// outlive the parent the context killed, and without a bound a shutdown
// would wait on them forever.
const waitDelay = 10 * time.Second

// Access is what one command needs to reach a remote. Token "" is an
// anonymous remote. Auth is "bearer" (Authorization: Bearer) or "basic"
// (Authorization: Basic of User and Token; User defaults to x-access-token,
// which GitHub ignores and Bitbucket DC checks). Either way a header, never
// a URL: a URL is persisted by git, printed in its errors and shown in the
// process list. Origin is the one scheme://host the token may reach (see
// Origin): a token travels only to a URL on it, never to another host, and
// not at all when Origin is "".
type Access struct {
	Token  string
	Auth   string
	User   string
	Origin string
}

// Client runs git against clones under root.
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

// New builds a Client over root, made absolute: git resolves a clone's
// destination against the command's directory, so a relative root would
// nest the clones under themselves. caFile, when set, is handed to git as
// GIT_SSL_CAINFO for a forge behind an internal CA.
func New(gitBin, root, caFile string) (*Client, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("repos dir: %w", err)
	}
	return &Client{git: gitBin, root: abs, caFile: caFile}, nil
}

// Root is the base folder, absolute.
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

// OriginURL is the remote a clone was made from, "" when there is no
// complete clone. The name of a directory is a label; the origin is the
// identity, so a changed clone URL is caught before the old code answers
// under the new name.
func (c *Client) OriginURL(ctx context.Context, dir string) (string, error) {
	if !c.complete(ctx, dir) {
		return "", nil
	}
	out, err := c.run(ctx, dir, nil, "remote", "get-url", "origin")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// marker is the file EnsureCloned writes into a clone once git has
// returned: git writes HEAD and config before any transfer, so their
// presence proves nothing, and a resolvable HEAD would read an empty
// repository as a cut-off clone forever.
const marker = "almanach-complete"

// complete reports whether dir holds a clone that got to the end.
func (c *Client) complete(_ context.Context, dir string) bool {
	_, err := os.Stat(filepath.Join(dir, marker))
	return err == nil
}

// EnsureCloned clones, bare and with full history, when dir holds no
// complete clone. A directory that exists without one (a clone cut off by
// a shutdown) is removed first: git refuses to clone into a non-empty one.
// The credential is a header for this command, so the URL git persists as
// remote.origin.url is the bare one.
func (c *Client) EnsureCloned(ctx context.Context, dir, cloneURL string, a Access) error {
	if c.complete(ctx, dir) {
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
	if _, err := c.run(ctx, c.root, a.env(cloneURL), "clone", "--quiet", "--bare", cloneURL, dir); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, marker), nil, 0o600)
}

// DefaultBranch asks the remote what HEAD points at. Never assume master.
func (c *Client) DefaultBranch(ctx context.Context, cloneURL string, a Access) (string, error) {
	out, err := c.run(ctx, c.root, a.env(cloneURL), "ls-remote", "--symref", cloneURL, "HEAD")
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
	_, err := c.run(ctx, dir, a.env(cloneURL), "fetch", "--quiet", "--prune", cloneURL,
		"+refs/heads/*:refs/remotes/origin/*", "+refs/tags/*:refs/tags/*")
	return err
}

// HeadSHA is the commit the branch points at on the remote-tracking side.
// An absent ref is ErrBranchGone; an unreadable clone is its own error.
func (c *Client) HeadSHA(ctx context.Context, dir, branch string) (string, error) {
	out, err := c.run(ctx, dir, nil, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+branch)
	if err != nil {
		if exitCode(err) != 1 {
			return "", err
		}
		if checkErr := c.readable(ctx, dir); checkErr != nil {
			return "", checkErr
		}
	}
	if sha := strings.TrimSpace(out); sha != "" {
		return sha, nil
	}
	return "", fmt.Errorf("branch %q: %w", branch, ErrBranchGone)
}

// Remove deletes one clone. The segments are validated again here even
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
		if err != nil {
			// An unreadable entry is skipped, not a failure: the size is a
			// figure on a page, and a missing directory is simply zero.
			return nil //nolint:nilerr // skipping is the intent
		}
		if d.IsDir() {
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
// failed against; the header keeps credentials out of it, the redaction is
// the belt to that brace.
func (c *Client) run(ctx context.Context, dir string, extra []string, args ...string) (string, error) {
	out, err := c.runIn(ctx, dir, nil, extra, args...)
	return string(out), err
}

// runIn is run with stdin and the raw bytes: a file's content is not text.
func (c *Client) runIn(ctx context.Context, dir string, stdin io.Reader, extra []string, args ...string) ([]byte, error) {
	cmd := c.command(ctx, dir, extra, args...)
	cmd.Stdin = stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return stdout.Bytes(), fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(Redact(stderr.String())))
	}
	return stdout.Bytes(), nil
}

// readable tells "the thing asked for is absent" from "the clone cannot be
// read at all": a command that answered no is followed by this probe, so a
// broken clone is never mistaken for a missing branch or commit.
func (c *Client) readable(ctx context.Context, dir string) error {
	if _, err := c.run(ctx, dir, inRepo(dir), "rev-parse", "--git-dir"); err != nil {
		return fmt.Errorf("repository unreadable: %w", err)
	}
	return nil
}

// command is one git process in dir, every rule of run applied, not
// started. core.quotePath is off for every command: by default git C-quotes
// any path with a non-ASCII byte, and the quoted form is no path.
func (c *Client) command(ctx context.Context, dir string, extra []string, args ...string) *exec.Cmd {
	full := append([]string{"-c", "safe.directory=" + dir, "-c", "core.quotePath=false"}, args...)
	cmd := exec.CommandContext(ctx, c.git, full...) //nolint:gosec // argv, no shell; paths come from validated segments
	cmd.Dir = dir
	cmd.WaitDelay = waitDelay
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if c.caFile != "" {
		cmd.Env = append(cmd.Env, "GIT_SSL_CAINFO="+c.caFile)
	}
	cmd.Env = append(cmd.Env, extra...)
	return cmd
}

func exitCode(err error) int {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	return -1
}

// env is the Authorization header for this one command, through git's
// environment config rather than -c (argv shows in the process list),
// appended after whatever GIT_CONFIG_* the process already carries.
// Nothing for an anonymous remote, a non-http one or one off the access's
// origin: the token stays home rather than reach a host it was not made for.
func (a Access) env(cloneURL string) []string {
	if a.Token == "" || !isHTTPURL(cloneURL) || !SameOrigin(cloneURL, a.Origin) {
		return nil
	}
	n, _ := strconv.Atoi(os.Getenv("GIT_CONFIG_COUNT"))
	i := strconv.Itoa(n)
	return []string{
		"GIT_CONFIG_COUNT=" + strconv.Itoa(n+1),
		"GIT_CONFIG_KEY_" + i + "=http.extraHeader",
		"GIT_CONFIG_VALUE_" + i + "=Authorization: " + a.Authorization(),
	}
}

// Authorization is the header value the token travels as, for git and for
// the Bitbucket listing alike, so the two cannot disagree: "Bearer <token>"
// or "Basic <base64 user:token>". "" without a token.
func (a Access) Authorization() string {
	if a.Token == "" {
		return ""
	}
	if a.Auth == "basic" {
		user := a.User
		if user == "" {
			user = "x-access-token"
		}
		return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+a.Token))
	}
	return "Bearer " + a.Token
}

// Origin is a URL's scheme://host, lower case, the port kept; "" when it
// does not parse or names no host. The unit a token is bound to.
func Origin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return strings.ToLower(u.Scheme + "://" + u.Host)
}

// SameOrigin reports whether a and b are on one origin; "" matches nothing.
func SameOrigin(a, b string) bool {
	oa := Origin(a)
	return oa != "" && oa == Origin(b)
}

func isHTTPURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http")
}

// credentialInURL matches the userinfo of any URL-shaped substring.
var credentialInURL = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.\-]*://)[^/@\s]+@`)

// Redact strips the userinfo from anything URL-shaped in text about to be
// logged. On the raw string, not via url.Parse: git wraps the remote in
// quotes and appends a colon, so the field is not a parseable URL.
func Redact(s string) string {
	return credentialInURL.ReplaceAllString(s, "${1}REDACTED@")
}

// ShortSHA is the seven-character form for log lines.
func ShortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
