// Package config is the instance's environment, read once at startup and
// failed fast: a missing or malformed value names itself and aborts, never a
// knob that quietly does nothing.
package config

import (
	"fmt"
	"maps"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	kitenv "github.com/trick77/ship/kit/env"

	"github.com/trick77/almanach/internal/authroute"
	"github.com/trick77/almanach/internal/diagram"
)

// AuthMode is how the read API knows who asks.
type AuthMode string

const (
	// AuthNone trusts nobody and names nobody: every reader is anonymous.
	// Allowed on any host: the dev override (compose.dev.yaml) runs it on
	// 0.0.0.0 in its container behind a host port; the production stack
	// has no host port and fixes oidc. Nothing in the code keeps it off a
	// public network; that is the deployment's choice of mode.
	AuthNone AuthMode = "none"
	// AuthOIDC signs the browser in at an OpenID provider; every /api route
	// but the login's own two needs kit's signed session cookie.
	AuthOIDC AuthMode = "oidc"
)

// OIDC is the sign-in, all of it required in oidc mode and refused in
// none, where it would look active and change nothing.
type OIDC struct {
	// Issuer is taken exactly as configured: discovery compares it byte for
	// byte with the provider's own, so a path (many providers have one) or
	// a trailing slash stays. https, or http on a loopback host for a local
	// provider.
	Issuer       string
	ClientID     string
	ClientSecret string
	// RedirectURL is the callback as the browser reaches it:
	// https://<host>/api/auth/callback. https only: behind a TLS-terminating
	// proxy the process sees plain HTTP, and the cookies are always Secure.
	RedirectURL string
	// SessionKey signs the session cookie, at least 32 bytes. Rotating it
	// signs everyone out; nothing else revokes a session.
	SessionKey []byte
}

// minSessionSecret is kit's floor for the HMAC key.
const minSessionSecret = 32

// DefaultAddr is where serve listens, and the healthcheck probes, when
// BACKEND_ADDR is unset.
const DefaultAddr = "0.0.0.0:8080"

// renamed is every variable an earlier release read, by the name that
// replaced it. One still set is a startup fault naming the new one, never
// ignored: a stale token key would break every private source unseen, a
// loopback SERVER_HOST turn into a bind on every interface. LOG_LEVEL is
// left out: too common a name to refuse.
var renamed = map[string]string{ //nolint:gosec // variable names, no credentials
	"ALMANACH_DB_PATH":                "BACKEND_DB_PATH",
	"ALMANACH_TEAM_KEYS_PATH":         "BACKEND_TEAM_KEYS_PATH",
	"ALMANACH_AUTH_MODE":              "BACKEND_AUTH_MODE",
	"ALMANACH_OIDC_ISSUER":            "BACKEND_OIDC_ISSUER",
	"ALMANACH_OIDC_CLIENT_ID":         "BACKEND_OIDC_CLIENT_ID",
	"ALMANACH_OIDC_CLIENT_SECRET":     "BACKEND_OIDC_CLIENT_SECRET",
	"ALMANACH_OIDC_REDIRECT_URL":      "BACKEND_OIDC_REDIRECT_URL",
	"ALMANACH_SESSION_SECRET":         "BACKEND_SESSION_SECRET",
	"ALMANACH_MODEL":                  "BACKEND_MODEL",
	"ALMANACH_REPO_MAX_STATEMENTS":    "BACKEND_REPO_MAX_STATEMENTS",
	"ALMANACH_PROJECT_MAX_STATEMENTS": "BACKEND_PROJECT_MAX_STATEMENTS",
	"ALMANACH_REPO_MAX_CHARS":         "BACKEND_REPO_MAX_CHARS",
	"ALMANACH_PROJECT_MAX_CHARS":      "BACKEND_PROJECT_MAX_CHARS",
	"ALMANACH_DIAGRAM_MAX_NODES":      "BACKEND_DIAGRAM_MAX_NODES",
	"ALMANACH_DIAGRAM_MAX_EDGES":      "BACKEND_DIAGRAM_MAX_EDGES",
	"ALMANACH_PROBLEM_WINDOW_DAYS":    "BACKEND_PROBLEM_WINDOW_DAYS",
	"ALMANACH_REPOS_DIR":              "BACKEND_REPOS_DIR",
	"ALMANACH_SYNC_INTERVAL":          "BACKEND_SYNC_INTERVAL",
	"ALMANACH_GIT_CA_FILE":            "BACKEND_GIT_CA_FILE",
	"ALMANACH_BASELINE_MAX_CALLS":     "BACKEND_BASELINE_MAX_CALLS",
	"ALMANACH_BASELINE_MAX_BYTES":     "BACKEND_BASELINE_MAX_BYTES",
	"ALMANACH_BASELINE_RECONCILE":     "BACKEND_BASELINE_RECONCILE",
	"ALMANACH_ENV":                    "BACKEND_ENV",
	"ALMANACH_REASONING_EFFORT":       "BACKEND_MODEL_REASONING_EFFORT",
	"ALMANACH_MAX_DIAGRAMS":           "BACKEND_DIAGRAM_MAX_PER_PAGE",
	"ALMANACH_SECRET_KEY":             "BACKEND_SOURCE_TOKEN_KEY",
	"ALMANACH_CATALOGUE_MODEL":        "BACKEND_BASELINE_CATALOGUE_MODEL",
	"SERVER_HOST":                     "BACKEND_ADDR",
	"SERVER_PORT":                     "BACKEND_ADDR",
}

// oidcVars is every variable oidc mode reads and none refuses.
var oidcVars = []string{
	"BACKEND_OIDC_ISSUER", "BACKEND_OIDC_CLIENT_ID", "BACKEND_OIDC_CLIENT_SECRET",
	"BACKEND_OIDC_REDIRECT_URL", "BACKEND_SESSION_SECRET",
}

// Limits are the ceilings the merge call is held to: how many statements a
// description may hold and how large the whole description may be. There
// is no cap on a statement's or the summary's own length: a description is
// documentation, and its prose is as long as the thing needs; the whole
// page is what must not grow without bound. Over any of them the rewrite
// is rejected whole and the page stays as it was: never truncated.
type Limits struct {
	RepoMaxStatements    int
	ProjectMaxStatements int
	// RepoMaxChars and ProjectMaxChars are the runes of a whole description:
	// the summary and every statement's name and text.
	RepoMaxChars    int
	ProjectMaxChars int
	// DiagramMaxNodes and DiagramMaxEdges cap every statement diagram: past
	// about a dozen nodes a picture stops reading and should be two. The
	// system map is held to the drawing's own limits (diagram.DrawMaxNodes,
	// DrawMaxEdges) instead. MaxDiagrams is the statement diagrams one page
	// may hold; the map does not count.
	DiagramMaxNodes int
	DiagramMaxEdges int
	MaxDiagrams     int
}

// Sources is the clone feature: off when ReposDir is empty.
type Sources struct {
	// ReposDir is the base folder every clone lands under, as
	// <ReposDir>/<project_key>/<slug>. "" switches the feature off: no
	// poller, the routes answer 404, the UI hides the entry.
	ReposDir string
	// SecretKey is BACKEND_SOURCE_TOKEN_KEY as given (base64 of 32 bytes), parsed
	// by the secret package; "" means no token can be stored.
	SecretKey string
	// SyncInterval is the poller's cycle; zero means manual only.
	SyncInterval time.Duration
	// GitCAFile is a PEM bundle for a forge behind an internal CA, handed
	// to git as GIT_SSL_CAINFO; "" keeps the system store.
	GitCAFile string
}

// Enabled reports whether clones are configured at all.
func (s Sources) Enabled() bool { return s.ReposDir != "" }

// Baseline is how a run reads a clone and what it may spend. The caps are
// checked before every call, so a run ends partial rather than over them.
type Baseline struct {
	// CatalogueModel is the llmwire profile of the catalogue step, the one
	// that picks what to read; "" means Model.
	CatalogueModel string
	// MaxCalls is the model calls one run may make: catalogue, overview, one
	// per subject, project.
	MaxCalls int
	// MaxBytes is the file content one run may read.
	MaxBytes int
	// Reconcile is how long after its last run a repository whose head
	// moved is read again on its own; zero means the buttons only.
	Reconcile time.Duration
}

// minBaselineCalls is a run with no subject: catalogue, overview, project.
const minBaselineCalls = 3

// App is the whole configuration.
type App struct {
	// DBPath is the SQLite database file; its folder must exist and be
	// writable (the file and its -wal, -shm beside it).
	DBPath       string
	TeamKeysPath string
	// Addr is host:port serve listens on.
	Addr     string
	AuthMode AuthMode
	// OIDC is set in oidc mode only.
	OIDC OIDC
	// Model is an llmwire profile id; the gateway host, key and aliases are
	// llmwire's own variables, read by it.
	Model           string
	ReasoningEffort string
	Limits          Limits
	ProblemWindow   int
	Sources         Sources
	Baseline        Baseline
	LogLevel        string
}

// Load reads the environment through lookup (os.Getenv in production),
// collecting every fault before failing.
func Load(lookup func(string) string) (*App, error) {
	r := kitenv.New(lookup)
	app := &App{
		DBPath:          r.Required("BACKEND_DB_PATH"),
		TeamKeysPath:    r.Required("BACKEND_TEAM_KEYS_PATH"),
		Addr:            r.String("BACKEND_ADDR", DefaultAddr),
		AuthMode:        AuthMode(strings.ToLower(r.Enum("BACKEND_AUTH_MODE", string(AuthNone), string(AuthNone), string(AuthOIDC)))),
		Model:           r.Required("BACKEND_MODEL"),
		ReasoningEffort: r.String("BACKEND_MODEL_REASONING_EFFORT", ""),
		Limits: Limits{
			RepoMaxStatements:    positive(r, "BACKEND_REPO_MAX_STATEMENTS", 40),
			ProjectMaxStatements: positive(r, "BACKEND_PROJECT_MAX_STATEMENTS", 24),
			RepoMaxChars:         positive(r, "BACKEND_REPO_MAX_CHARS", 60000),
			ProjectMaxChars:      positive(r, "BACKEND_PROJECT_MAX_CHARS", 40000),
			DiagramMaxNodes:      positive(r, "BACKEND_DIAGRAM_MAX_NODES", 12),
			DiagramMaxEdges:      positive(r, "BACKEND_DIAGRAM_MAX_EDGES", 20),
			MaxDiagrams:          nonNegative(r, "BACKEND_DIAGRAM_MAX_PER_PAGE", 6),
		},
		ProblemWindow: positive(r, "BACKEND_PROBLEM_WINDOW_DAYS", 90),
		Sources: Sources{
			ReposDir:     r.String("BACKEND_REPOS_DIR", ""),
			SecretKey:    r.String("BACKEND_SOURCE_TOKEN_KEY", ""),
			SyncInterval: r.Duration("BACKEND_SYNC_INTERVAL", 30*time.Minute),
			GitCAFile:    r.String("BACKEND_GIT_CA_FILE", ""),
		},
		Baseline: Baseline{
			CatalogueModel: r.String("BACKEND_BASELINE_CATALOGUE_MODEL", ""),
			MaxCalls:       positive(r, "BACKEND_BASELINE_MAX_CALLS", 16),
			MaxBytes:       positive(r, "BACKEND_BASELINE_MAX_BYTES", 2_000_000),
			Reconcile:      r.Duration("BACKEND_BASELINE_RECONCILE", 720*time.Hour),
		},
		LogLevel: r.String("BACKEND_LOG_LEVEL", "INFO"),
	}
	for _, old := range slices.Sorted(maps.Keys(renamed)) {
		if strings.TrimSpace(lookup(old)) != "" {
			r.Fail("%s was renamed to %s; rename it", old, renamed[old])
		}
	}
	if why := badAddr(app.Addr); why != "" {
		r.Fail("BACKEND_ADDR: %s, got %q", why, app.Addr)
	}
	if app.Sources.SyncInterval < 0 {
		r.Fail("BACKEND_SYNC_INTERVAL: must not be negative, got %s", app.Sources.SyncInterval)
	}
	if app.Baseline.MaxCalls > 0 && app.Baseline.MaxCalls < minBaselineCalls {
		r.Fail("BACKEND_BASELINE_MAX_CALLS: a run needs at least %d calls (catalogue, overview, project), got %d", minBaselineCalls, app.Baseline.MaxCalls)
	}
	// A diagram past the drawing's ceilings is not drawn, so the caps may
	// not go past them either.
	if app.Limits.DiagramMaxNodes > diagram.DrawMaxNodes {
		r.Fail("BACKEND_DIAGRAM_MAX_NODES: at most %d, the most the drawing lays out, got %d", diagram.DrawMaxNodes, app.Limits.DiagramMaxNodes)
	}
	if app.Limits.DiagramMaxEdges > diagram.DrawMaxEdges {
		r.Fail("BACKEND_DIAGRAM_MAX_EDGES: at most %d, the most the drawing lays out, got %d", diagram.DrawMaxEdges, app.Limits.DiagramMaxEdges)
	}
	if app.Baseline.Reconcile < 0 {
		r.Fail("BACKEND_BASELINE_RECONCILE: must not be negative, got %s", app.Baseline.Reconcile)
	}
	if app.AuthMode == AuthOIDC {
		app.OIDC = loadOIDC(r)
	} else {
		for _, name := range oidcVars {
			if strings.TrimSpace(lookup(name)) != "" {
				r.Fail("%s is read in oidc mode only, got BACKEND_AUTH_MODE=%s; remove it", name, app.AuthMode)
			}
		}
	}
	if err := r.Err(); err != nil {
		return nil, err
	}
	return app, nil
}

// badAddr is why addr is no host:port to listen on, "" when it is one. An
// empty host listens on every interface. Whether the host resolves is
// listen's to find.
func badAddr(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "want host:port"
	}
	if strings.ContainsAny(host, " \t\n") {
		return "the host has whitespace"
	}
	if strings.Trim(port, "0123456789") != "" {
		return "the port must be 1 to 65535"
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return "the port must be 1 to 65535"
	}
	return ""
}

// loadOIDC reads the sign-in. Every gap is fatal, not a warning: a
// half-configured instance would start, serve the shell and fail every
// login at the callback, which reads as a provider outage.
func loadOIDC(r *kitenv.Reader) OIDC {
	need := func(name string) string {
		v := r.String(name, "")
		if v == "" {
			r.Fail("BACKEND_AUTH_MODE=oidc requires %s", name)
		}
		return v
	}
	o := OIDC{
		Issuer:       need("BACKEND_OIDC_ISSUER"),
		ClientID:     need("BACKEND_OIDC_CLIENT_ID"),
		ClientSecret: need("BACKEND_OIDC_CLIENT_SECRET"),
		RedirectURL:  need("BACKEND_OIDC_REDIRECT_URL"),
	}
	if o.Issuer != "" {
		if why := issuerFault(o.Issuer); why != "" {
			r.Fail("BACKEND_OIDC_ISSUER: %s, got %q", why, o.Issuer)
		}
	}
	if o.RedirectURL != "" {
		if why := redirectFault(o.RedirectURL); why != "" {
			r.Fail("BACKEND_OIDC_REDIRECT_URL: %s, got %q", why, o.RedirectURL)
		}
	}
	if secret := need("BACKEND_SESSION_SECRET"); secret != "" {
		if len(secret) < minSessionSecret {
			r.Fail("BACKEND_SESSION_SECRET: want at least %d bytes (openssl rand -base64 48), got %d", minSessionSecret, len(secret))
		} else {
			o.SessionKey = []byte(secret)
		}
	}
	return o
}

// issuerFault says what is wrong with an issuer, "" when nothing. A path
// and a trailing slash are fine: the issuer is compared as written.
func issuerFault(s string) string {
	u, err := url.Parse(s)
	switch {
	case err != nil || u.Host == "":
		return "want an absolute URL like https://auth.example.com"
	case u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "":
		return "want no user info, query or fragment"
	case u.Scheme == "https", u.Scheme == "http" && loopback(u.Hostname()):
		return ""
	default:
		return "want https (http only on a loopback host)"
	}
}

// redirectFault says what is wrong with a redirect URL, "" when nothing.
func redirectFault(s string) string {
	u, err := url.Parse(s)
	switch {
	case err != nil || u.Scheme != "https" || u.Host == "":
		return "want https://<host>" + authroute.Callback + ", the session cookie is always Secure"
	case u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Path != authroute.Callback:
		return "want the path " + authroute.Callback + " and nothing else after the host"
	}
	return ""
}

// loopback reports whether host is this machine: only there may a local
// provider speak plain http.
func loopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func positive(r *kitenv.Reader, name string, def int) int {
	v := r.Int(name, def)
	if v < 1 {
		r.Fail("%s: must be a positive integer, got %d", name, v)
	}
	return v
}

// nonNegative is an int that may be 0: no statement diagrams at all.
func nonNegative(r *kitenv.Reader, name string, def int) int {
	v := r.Int(name, def)
	if v < 0 {
		r.Fail("%s: must not be negative, got %d", name, v)
	}
	return v
}

// Summary is the config as the startup log prints it: no secret (not the
// token key, the client secret, the session secret); of the sign-in only
// the issuer and the client id, of the token key only whether it is set.
func (a *App) Summary() string {
	sources := "off"
	if a.Sources.Enabled() {
		catalogue := a.Baseline.CatalogueModel
		if catalogue == "" {
			catalogue = a.Model
		}
		sources = fmt.Sprintf("{dir %s, interval %s, token_key %v, baseline {catalogue %s, %d calls / %d bytes a run, reconcile %s}}",
			a.Sources.ReposDir, a.Sources.SyncInterval, a.Sources.SecretKey != "",
			catalogue, a.Baseline.MaxCalls, a.Baseline.MaxBytes, a.Baseline.Reconcile)
	}
	mode := string(a.AuthMode)
	if a.AuthMode == AuthOIDC {
		mode = fmt.Sprintf("{oidc issuer %s, client %s}", a.OIDC.Issuer, a.OIDC.ClientID)
	}
	return fmt.Sprintf("addr=%s auth=%s model=%s effort=%q limits={repo %d statements / %d chars, project %d statements / %d chars, diagrams %d a page / %d nodes / %d edges} problem_window=%dd sources=%s",
		a.Addr, mode, a.Model, a.ReasoningEffort,
		a.Limits.RepoMaxStatements, a.Limits.RepoMaxChars, a.Limits.ProjectMaxStatements, a.Limits.ProjectMaxChars,
		a.Limits.MaxDiagrams, a.Limits.DiagramMaxNodes, a.Limits.DiagramMaxEdges, a.ProblemWindow, sources)
}
