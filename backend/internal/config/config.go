// Package config is the instance's environment, read once at startup and
// failed fast: a missing or malformed value names itself and aborts, never a
// knob that quietly does nothing.
package config

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	kitenv "github.com/trick77/ship/kit/env"
)

// AuthMode is how the read API knows who asks.
type AuthMode string

const (
	// AuthNone trusts nobody and names nobody: every reader is anonymous.
	// Allowed on any host: the dev stack binds 0.0.0.0 in its container,
	// and the production compose file hard-codes oidc instead.
	AuthNone AuthMode = "none"
	// AuthOIDC signs the browser in at an OpenID provider; every /api route
	// but the login's own two needs kit's signed session cookie.
	AuthOIDC AuthMode = "oidc"
)

// OIDC is the sign-in, all of it required in oidc mode and refused in
// none, where it would look active and change nothing.
type OIDC struct {
	// Issuer is the provider's bare origin (https://auth.example.com): no
	// path, a trailing slash trimmed. Anything else fails discovery.
	Issuer       string
	ClientID     string
	ClientSecret string
	// RedirectURL is the callback as the browser reaches it, https only:
	// behind a TLS-terminating proxy the process sees plain HTTP, so this
	// URL is the one place that says the cookies travel over TLS.
	RedirectURL string
	// SessionKey signs the session cookie, at least 32 bytes. Rotating it
	// signs everyone out; nothing else revokes a session.
	SessionKey []byte
}

// CookieSecure is the Secure flag of the session and login cookies,
// derived from the redirect URL, which Load refuses unless https.
func (o OIDC) CookieSecure() bool {
	return strings.HasPrefix(strings.ToLower(o.RedirectURL), "https://")
}

// minSessionSecret is kit's floor for the HMAC key.
const minSessionSecret = 32

// oidcVars is every variable oidc mode reads and none refuses.
var oidcVars = []string{
	"ALMANACH_OIDC_ISSUER", "ALMANACH_OIDC_CLIENT_ID", "ALMANACH_OIDC_CLIENT_SECRET",
	"ALMANACH_OIDC_REDIRECT_URL", "ALMANACH_SESSION_SECRET",
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
}

// Sources is the clone feature: off when ReposDir is empty.
type Sources struct {
	// ReposDir is the base folder every clone lands under, as
	// <ReposDir>/<project_key>/<slug>. "" switches the feature off: no
	// poller, the routes answer 404, the UI hides the entry.
	ReposDir string
	// SecretKey is ALMANACH_SECRET_KEY as given (base64 of 32 bytes), parsed
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
	DatabaseURL  string
	TeamKeysPath string
	Host         string
	Port         int
	AuthMode     AuthMode
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
		DatabaseURL:     r.Required("DATABASE_URL"),
		TeamKeysPath:    r.Required("ALMANACH_TEAM_KEYS_PATH"),
		Host:            r.String("SERVER_HOST", "0.0.0.0"),
		Port:            r.Int("SERVER_PORT", 8080),
		AuthMode:        AuthMode(strings.ToLower(r.Enum("ALMANACH_AUTH_MODE", string(AuthNone), string(AuthNone), string(AuthOIDC)))),
		Model:           r.Required("ALMANACH_MODEL"),
		ReasoningEffort: r.String("ALMANACH_REASONING_EFFORT", ""),
		Limits: Limits{
			RepoMaxStatements:    positive(r, "ALMANACH_REPO_MAX_STATEMENTS", 40),
			ProjectMaxStatements: positive(r, "ALMANACH_PROJECT_MAX_STATEMENTS", 24),
			RepoMaxChars:         positive(r, "ALMANACH_REPO_MAX_CHARS", 60000),
			ProjectMaxChars:      positive(r, "ALMANACH_PROJECT_MAX_CHARS", 40000),
		},
		ProblemWindow: positive(r, "ALMANACH_PROBLEM_WINDOW_DAYS", 90),
		Sources: Sources{
			ReposDir:     r.String("ALMANACH_REPOS_DIR", ""),
			SecretKey:    r.String("ALMANACH_SECRET_KEY", ""),
			SyncInterval: r.Duration("ALMANACH_SYNC_INTERVAL", 30*time.Minute),
			GitCAFile:    r.String("ALMANACH_GIT_CA_FILE", ""),
		},
		Baseline: Baseline{
			CatalogueModel: r.String("ALMANACH_CATALOGUE_MODEL", ""),
			MaxCalls:       positive(r, "ALMANACH_BASELINE_MAX_CALLS", 16),
			MaxBytes:       positive(r, "ALMANACH_BASELINE_MAX_BYTES", 2_000_000),
			Reconcile:      r.Duration("ALMANACH_BASELINE_RECONCILE", 720*time.Hour),
		},
		LogLevel: r.String("LOG_LEVEL", "INFO"),
	}
	if app.Sources.SyncInterval < 0 {
		r.Fail("ALMANACH_SYNC_INTERVAL: must not be negative, got %s", app.Sources.SyncInterval)
	}
	if app.Baseline.MaxCalls > 0 && app.Baseline.MaxCalls < minBaselineCalls {
		r.Fail("ALMANACH_BASELINE_MAX_CALLS: a run needs at least %d calls (catalogue, overview, project), got %d", minBaselineCalls, app.Baseline.MaxCalls)
	}
	if app.Baseline.Reconcile < 0 {
		r.Fail("ALMANACH_BASELINE_RECONCILE: must not be negative, got %s", app.Baseline.Reconcile)
	}
	if app.AuthMode == AuthOIDC {
		app.OIDC = loadOIDC(r)
	} else {
		for _, name := range oidcVars {
			if strings.TrimSpace(lookup(name)) != "" {
				r.Fail("%s is read in oidc mode only, got ALMANACH_AUTH_MODE=%s; remove it", name, app.AuthMode)
			}
		}
	}
	if err := r.Err(); err != nil {
		return nil, err
	}
	return app, nil
}

// loadOIDC reads the sign-in. Every gap is fatal, not a warning: a
// half-configured instance would start, serve the shell and fail every
// login at the callback, which reads as a provider outage.
func loadOIDC(r *kitenv.Reader) OIDC {
	need := func(name string) string {
		v := r.String(name, "")
		if v == "" {
			r.Fail("ALMANACH_AUTH_MODE=oidc requires %s", name)
		}
		return v
	}
	o := OIDC{
		Issuer:       strings.TrimRight(need("ALMANACH_OIDC_ISSUER"), "/"),
		ClientID:     need("ALMANACH_OIDC_CLIENT_ID"),
		ClientSecret: need("ALMANACH_OIDC_CLIENT_SECRET"),
		RedirectURL:  need("ALMANACH_OIDC_REDIRECT_URL"),
	}
	if o.Issuer != "" && !bareOrigin(o.Issuer) {
		r.Fail("ALMANACH_OIDC_ISSUER: want a bare origin like https://auth.example.com, got %q", o.Issuer)
	}
	if o.RedirectURL != "" && !o.CookieSecure() {
		r.Fail("ALMANACH_OIDC_REDIRECT_URL: want https, the session cookie is always Secure, got %q", o.RedirectURL)
	}
	if secret := need("ALMANACH_SESSION_SECRET"); secret != "" {
		if len(secret) < minSessionSecret {
			r.Fail("ALMANACH_SESSION_SECRET: want at least %d bytes (openssl rand -base64 48), got %d", minSessionSecret, len(secret))
		} else {
			o.SessionKey = []byte(secret)
		}
	}
	return o
}

// bareOrigin reports whether s is scheme and host alone: no user, path,
// query or fragment.
func bareOrigin(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" &&
		u.User == nil && u.Path == "" && u.RawQuery == "" && u.Fragment == "" && !u.ForceQuery
}

func positive(r *kitenv.Reader, name string, def int) int {
	v := r.Int(name, def)
	if v < 1 {
		r.Fail("%s: must be a positive integer, got %d", name, v)
	}
	return v
}

// Summary is the config as the startup log prints it: no secret, no DSN;
// of the sign-in only the issuer and the client id.
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
	auth := string(a.AuthMode)
	if a.AuthMode == AuthOIDC {
		auth = fmt.Sprintf("{oidc issuer %s, client %s}", a.OIDC.Issuer, a.OIDC.ClientID)
	}
	return fmt.Sprintf("addr=%s:%d auth=%s model=%s effort=%q limits={repo %d statements / %d chars, project %d statements / %d chars} problem_window=%dd sources=%s",
		a.Host, a.Port, auth, a.Model, a.ReasoningEffort,
		a.Limits.RepoMaxStatements, a.Limits.RepoMaxChars, a.Limits.ProjectMaxStatements, a.Limits.ProjectMaxChars, a.ProblemWindow, sources)
}
