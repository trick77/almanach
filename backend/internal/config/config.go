// Package config is the instance's environment, read once at startup and
// failed fast: a missing or malformed value names itself and aborts, never a
// knob that quietly does nothing.
package config

import (
	"fmt"
	"strings"

	kitenv "github.com/trick77/ship/kit/env"
)

// AuthMode is how the read API knows who asks.
type AuthMode string

const (
	// AuthNone trusts nobody and names nobody: every reader is anonymous.
	AuthNone AuthMode = "none"
	// AuthProxy trusts X-Forwarded-User from an OAuth sidecar in front; the
	// service then listens on loopback only, so the header can come from
	// nowhere else.
	AuthProxy AuthMode = "proxy"
)

// Limits are the caps the merge call is held to. Over any of them the
// rewrite is rejected whole and the page stays as it was: never truncated.
type Limits struct {
	RepoMaxStatements    int
	ProjectMaxStatements int
	SummaryMaxChars      int
	StatementMaxChars    int
}

// App is the whole configuration.
type App struct {
	DatabaseURL  string
	TeamKeysPath string
	Host         string
	Port         int
	AuthMode     AuthMode
	// Model is an llmwire profile id; the gateway host, key and aliases are
	// llmwire's own variables, read by it.
	Model           string
	ReasoningEffort string
	Limits          Limits
	ProblemWindow   int
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
		AuthMode:        AuthMode(strings.ToLower(r.Enum("ALMANACH_AUTH_MODE", string(AuthNone), string(AuthNone), string(AuthProxy)))),
		Model:           r.Required("ALMANACH_MODEL"),
		ReasoningEffort: r.String("ALMANACH_REASONING_EFFORT", ""),
		Limits: Limits{
			RepoMaxStatements:    positive(r, "ALMANACH_REPO_MAX_STATEMENTS", 40),
			ProjectMaxStatements: positive(r, "ALMANACH_PROJECT_MAX_STATEMENTS", 24),
			SummaryMaxChars:      positive(r, "ALMANACH_SUMMARY_MAX_CHARS", 1500),
			StatementMaxChars:    positive(r, "ALMANACH_STATEMENT_MAX_CHARS", 300),
		},
		ProblemWindow: positive(r, "ALMANACH_PROBLEM_WINDOW_DAYS", 90),
		LogLevel:      r.String("LOG_LEVEL", "INFO"),
	}
	// In proxy mode the header is the whole credential, so the listener must
	// be reachable by the sidecar alone.
	if app.AuthMode == AuthProxy && app.Host != "127.0.0.1" && app.Host != "localhost" {
		r.Fail("ALMANACH_AUTH_MODE=proxy needs SERVER_HOST=127.0.0.1 (the sidecar is the only caller), got %q", app.Host)
	}
	if err := r.Err(); err != nil {
		return nil, err
	}
	return app, nil
}

func positive(r *kitenv.Reader, name string, def int) int {
	v := r.Int(name, def)
	if v < 1 {
		r.Fail("%s: must be a positive integer, got %d", name, v)
	}
	return v
}

// Summary is the config as the startup log prints it: no secret, no DSN.
func (a *App) Summary() string {
	return fmt.Sprintf("addr=%s:%d auth=%s model=%s effort=%q limits={repo %d, project %d, summary %d, statement %d} problem_window=%dd",
		a.Host, a.Port, a.AuthMode, a.Model, a.ReasoningEffort,
		a.Limits.RepoMaxStatements, a.Limits.ProjectMaxStatements, a.Limits.SummaryMaxChars, a.Limits.StatementMaxChars, a.ProblemWindow)
}
