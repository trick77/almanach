package config

import (
	"strings"
	"testing"
	"time"
)

func lookup(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func minimal() map[string]string {
	return map[string]string{
		"BACKEND_DB_PATH":        "/data/almanach.db",
		"BACKEND_TEAM_KEYS_PATH": "/run/secrets/team-keys.json",
		"BACKEND_MODEL":          "gpt-x",
	}
}

func TestLoadRequiredMissing(t *testing.T) {
	_, err := Load(lookup(map[string]string{}))
	if err == nil {
		t.Fatal("Load accepted an empty environment")
	}
	for _, name := range []string{"BACKEND_DB_PATH is required", "BACKEND_TEAM_KEYS_PATH is required", "BACKEND_MODEL is required"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error lacks %q:\n%v", name, err)
		}
	}
}

func TestLoadDefaults(t *testing.T) {
	app, err := Load(lookup(minimal()))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if app.DBPath != "/data/almanach.db" || app.TeamKeysPath != "/run/secrets/team-keys.json" || app.Model != "gpt-x" {
		t.Errorf("required values = %+v", app)
	}
	if app.Addr != "0.0.0.0:8080" || app.AuthMode != AuthNone || app.ReasoningEffort != "" {
		t.Errorf("server defaults = %+v", app)
	}
	want := Limits{DiagramMaxNodes: 12, DiagramMaxEdges: 20, MaxDiagrams: 6}
	if app.Limits != want {
		t.Errorf("Limits = %+v, want %+v", app.Limits, want)
	}
	if app.ProblemWindow != 90 || app.LogLevel != "INFO" {
		t.Errorf("problem window / log level = %d %q", app.ProblemWindow, app.LogLevel)
	}
}

func TestLoadOverrides(t *testing.T) {
	env := minimal()
	env["BACKEND_ADDR"] = "127.0.0.1:9090"
	env["BACKEND_MODEL_REASONING_EFFORT"] = "high"
	env["BACKEND_PROBLEM_WINDOW_DAYS"] = "30"
	env["BACKEND_DIAGRAM_MAX_NODES"] = "16"
	env["BACKEND_DIAGRAM_MAX_EDGES"] = "8"
	env["BACKEND_DIAGRAM_MAX_PER_PAGE"] = "0"
	env["BACKEND_LOG_LEVEL"] = "DEBUG"
	app, err := Load(lookup(env))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if app.Addr != "127.0.0.1:9090" || app.AuthMode != AuthNone || app.ReasoningEffort != "high" {
		t.Errorf("server values = %+v", app)
	}
	want := Limits{DiagramMaxNodes: 16, DiagramMaxEdges: 8, MaxDiagrams: 0}
	if app.Limits != want {
		t.Errorf("Limits = %+v, want %+v", app.Limits, want)
	}
	if app.ProblemWindow != 30 || app.LogLevel != "DEBUG" {
		t.Errorf("problem window / log level = %d %q", app.ProblemWindow, app.LogLevel)
	}
}

func TestBitbucketHost(t *testing.T) {
	env := minimal()
	env["BACKEND_REPOS_DIR"] = "/app/repos"
	env["BACKEND_BITBUCKET_HOST"] = " https://forge.example.invalid/bitbucket/ "
	app, err := Load(lookup(env))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if app.Sources.BitbucketHost != "https://forge.example.invalid/bitbucket" {
		t.Errorf("BitbucketHost = %q", app.Sources.BitbucketHost)
	}
	bads := []string{"http://forge.example.invalid", "forge.example.invalid", "https://me@forge.example.invalid",
		"https://forge.example.invalid?x=1", "https://forge.example.invalid?", "https://"}
	for _, bad := range bads {
		env["BACKEND_BITBUCKET_HOST"] = bad
		if _, err := Load(lookup(env)); err == nil || !strings.Contains(err.Error(), "BACKEND_BITBUCKET_HOST") {
			t.Errorf("%q: err = %v", bad, err)
		}
	}
	// Sources off: the value is never read, so never checked.
	delete(env, "BACKEND_REPOS_DIR")
	if _, err := Load(lookup(env)); err != nil {
		t.Errorf("sources off: %v", err)
	}
}

const sessionSecret = "0123456789abcdef0123456789abcdef"

func oidcEnv() map[string]string {
	env := minimal()
	env["BACKEND_AUTH_MODE"] = "oidc"
	env["BACKEND_OIDC_ISSUER"] = "https://auth.example.com/"
	env["BACKEND_OIDC_CLIENT_ID"] = "almanach"
	env["BACKEND_OIDC_CLIENT_SECRET"] = "client-secret-xyz"
	env["BACKEND_OIDC_REDIRECT_URL"] = "https://almanach.example.com/api/auth/callback"
	env["BACKEND_SESSION_SECRET"] = sessionSecret
	return env
}

func TestOIDCMode(t *testing.T) {
	app, err := Load(lookup(oidcEnv()))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// The issuer as written: its trailing slash stays.
	want := OIDC{Issuer: "https://auth.example.com/", ClientID: "almanach", ClientSecret: "client-secret-xyz",
		RedirectURL: "https://almanach.example.com/api/auth/callback", SessionKey: []byte(sessionSecret)}
	if app.AuthMode != AuthOIDC || app.OIDC.Issuer != want.Issuer || app.OIDC.ClientID != want.ClientID ||
		app.OIDC.ClientSecret != want.ClientSecret || app.OIDC.RedirectURL != want.RedirectURL ||
		string(app.OIDC.SessionKey) != sessionSecret {
		t.Errorf("OIDC = %+v, want %+v", app.OIDC, want)
	}
	// Any host: the dev stack and the production stack both bind 0.0.0.0.
	if app.Addr != "0.0.0.0:8080" {
		t.Errorf("Addr = %q", app.Addr)
	}
	s := app.Summary()
	if !strings.Contains(s, "auth={oidc issuer https://auth.example.com/, client almanach}") {
		t.Errorf("Summary = %s", s)
	}
	for _, secret := range []string{"client-secret-xyz", sessionSecret} {
		if strings.Contains(s, secret) {
			t.Errorf("Summary leaks %q: %s", secret, s)
		}
	}
}

func TestOIDCModeRequiresEverything(t *testing.T) {
	for _, name := range oidcVars {
		env := oidcEnv()
		delete(env, name)
		_, err := Load(lookup(env))
		if err == nil || !strings.Contains(err.Error(), "BACKEND_AUTH_MODE=oidc requires "+name) {
			t.Errorf("without %s: err = %v", name, err)
		}
	}
}

func TestOIDCModeRefusesBadValues(t *testing.T) {
	for _, tc := range []struct{ name, value, want string }{
		{"BACKEND_OIDC_ISSUER", "auth.example.com", "want an absolute URL"},
		{"BACKEND_OIDC_ISSUER", "https://auth.example.com/%zz", "want an absolute URL"},
		{"BACKEND_OIDC_ISSUER", "https://auth.example.com?x=1", "want no user info, query or fragment"},
		{"BACKEND_OIDC_ISSUER", "https://auth.example.com#x", "want no user info, query or fragment"},
		{"BACKEND_OIDC_ISSUER", "https://user@auth.example.com", "want no user info, query or fragment"},
		{"BACKEND_OIDC_ISSUER", "http://auth.example.com", "want https (http only on a loopback host)"},
		{"BACKEND_OIDC_ISSUER", "ftp://auth.example.com", "want https (http only on a loopback host)"},
		{"BACKEND_OIDC_REDIRECT_URL", "http://almanach.example.com/api/auth/callback", "want https://<host>/api/auth/callback"},
		{"BACKEND_OIDC_REDIRECT_URL", "https:///api/auth/callback", "want https://<host>/api/auth/callback"},
		{"BACKEND_OIDC_REDIRECT_URL", "https://almanach.example.com/", "want the path /api/auth/callback"},
		{"BACKEND_OIDC_REDIRECT_URL", "https://almanach.example.com/wiki/api/auth/callback", "want the path /api/auth/callback"},
		{"BACKEND_OIDC_REDIRECT_URL", "https://almanach.example.com/api/auth/callback?x=1", "want the path /api/auth/callback"},
		{"BACKEND_SESSION_SECRET", "too-short", "want at least 32 bytes"},
	} {
		env := oidcEnv()
		env[tc.name] = tc.value
		_, err := Load(lookup(env))
		if err == nil || !strings.Contains(err.Error(), tc.name+": "+tc.want) {
			t.Errorf("%s=%q: err = %v", tc.name, tc.value, err)
		}
	}
}

// Real providers' issuers: paths, a trailing slash, a local one on http.
func TestOIDCIssuerAsWritten(t *testing.T) {
	for _, issuer := range []string{
		"https://idp.example.com/realms/wiki",
		"https://login.example.com/00000000-0000-0000-0000-000000000000/v2.0",
		"https://tenant.example.com/",
		"https://auth.example.com/application/o/almanach/",
		"http://127.0.0.1:5556/dex",
		"http://localhost:8080",
		"http://[::1]:8080",
	} {
		env := oidcEnv()
		env["BACKEND_OIDC_ISSUER"] = issuer
		app, err := Load(lookup(env))
		if err != nil {
			t.Errorf("%s: %v", issuer, err)
			continue
		}
		if app.OIDC.Issuer != issuer {
			t.Errorf("Issuer = %q, want %q as written", app.OIDC.Issuer, issuer)
		}
	}
}

// Set under none, an oidc variable would look active and change nothing.
func TestNoneModeRefusesOIDCVariables(t *testing.T) {
	for _, name := range oidcVars {
		env := oidcEnv()
		env["BACKEND_AUTH_MODE"] = "none"
		for _, other := range oidcVars {
			if other != name {
				delete(env, other)
			}
		}
		_, err := Load(lookup(env))
		if err == nil || !strings.Contains(err.Error(), name+" is read in oidc mode only, got BACKEND_AUTH_MODE=none") {
			t.Errorf("%s under none: err = %v", name, err)
		}
	}
}

func TestProxyModeIsGone(t *testing.T) {
	env := minimal()
	env["BACKEND_AUTH_MODE"] = "proxy"
	if _, err := Load(lookup(env)); err == nil || !strings.Contains(err.Error(), `BACKEND_AUTH_MODE: "proxy", want one of none, oidc`) {
		t.Errorf("err = %v", err)
	}
}

func TestNonPositiveCap(t *testing.T) {
	for _, name := range []string{"BACKEND_DIAGRAM_MAX_NODES", "BACKEND_PROBLEM_WINDOW_DAYS"} {
		for _, v := range []string{"0", "-3"} {
			env := minimal()
			env[name] = v
			_, err := Load(lookup(env))
			if err == nil {
				t.Errorf("%s=%s accepted", name, v)
				continue
			}
			if !strings.Contains(err.Error(), name+": must be a positive integer") {
				t.Errorf("%s=%s: error = %v", name, v, err)
			}
		}
	}
	env := minimal()
	env["BACKEND_PROBLEM_WINDOW_DAYS"] = "lots"
	if _, err := Load(lookup(env)); err == nil || !strings.Contains(err.Error(), `BACKEND_PROBLEM_WINDOW_DAYS: "lots" is not an integer`) {
		t.Errorf("non-integer cap: error = %v", err)
	}
}

func TestDiagramCaps(t *testing.T) {
	for name, c := range map[string]struct{ value, fault string }{
		"BACKEND_DIAGRAM_MAX_NODES":    {"17", "at most 16, the most the drawing lays out"},
		"BACKEND_DIAGRAM_MAX_EDGES":    {"25", "at most 24, the most the drawing lays out"},
		"BACKEND_DIAGRAM_MAX_PER_PAGE": {"-1", "must not be negative"},
	} {
		env := minimal()
		env[name] = c.value
		if _, err := Load(lookup(env)); err == nil || !strings.Contains(err.Error(), name+": "+c.fault) {
			t.Errorf("%s=%s: error = %v", name, c.value, err)
		}
	}
	env := minimal()
	env["BACKEND_DIAGRAM_MAX_NODES"] = "0"
	if _, err := Load(lookup(env)); err == nil || !strings.Contains(err.Error(), "BACKEND_DIAGRAM_MAX_NODES: must be a positive integer") {
		t.Errorf("no nodes: error = %v", err)
	}
}

func TestBaselineDefaultsAndChecks(t *testing.T) {
	app, err := Load(lookup(minimal()))
	if err != nil {
		t.Fatal(err)
	}
	want := Baseline{MaxCalls: 16, MaxBytes: 4_000_000, Reconcile: 720 * time.Hour}
	if app.Baseline != want {
		t.Fatalf("Baseline = %+v, want %+v", app.Baseline, want)
	}
	env := minimal()
	env["BACKEND_BASELINE_CATALOGUE_MODEL"] = "cheap"
	env["BACKEND_BASELINE_MAX_CALLS"] = "3"
	env["BACKEND_BASELINE_RECONCILE"] = "0"
	app, err = Load(lookup(env))
	if err != nil || app.Baseline.CatalogueModel != "cheap" || app.Baseline.MaxCalls != 3 || app.Baseline.Reconcile != 0 {
		t.Fatalf("overrides = %+v %v", app.Baseline, err)
	}
	for name, c := range map[string]struct{ value, fault string }{
		"BACKEND_BASELINE_MAX_CALLS": {"2", "a run needs at least 3 calls"},
		"BACKEND_BASELINE_MAX_BYTES": {"0", "must be a positive integer"},
		"BACKEND_BASELINE_RECONCILE": {"-1h", "must not be negative"},
	} {
		env := minimal()
		env[name] = c.value
		if _, err := Load(lookup(env)); err == nil || !strings.Contains(err.Error(), name+": "+c.fault) {
			t.Errorf("%s=%s: error = %v", name, c.value, err)
		}
	}
}

func TestSummaryNamesTheCatalogueModel(t *testing.T) {
	env := minimal()
	env["BACKEND_REPOS_DIR"] = "/repos"
	app, err := Load(lookup(env))
	if err != nil {
		t.Fatal(err)
	}
	if s := app.Summary(); !strings.Contains(s, "baseline {catalogue gpt-x, 16 calls / 4000000 bytes a run, reconcile 720h0m0s}") {
		t.Errorf("Summary = %s", s)
	}
	env["BACKEND_BASELINE_CATALOGUE_MODEL"] = "cheap"
	if app, err = Load(lookup(env)); err != nil {
		t.Fatal(err)
	}
	if s := app.Summary(); !strings.Contains(s, "catalogue cheap,") {
		t.Errorf("Summary with a catalogue model = %s", s)
	}
}

func TestInvalidEnum(t *testing.T) {
	env := minimal()
	env["BACKEND_AUTH_MODE"] = "basic"
	_, err := Load(lookup(env))
	if err == nil {
		t.Fatal("Load accepted BACKEND_AUTH_MODE=basic")
	}
	if !strings.Contains(err.Error(), `BACKEND_AUTH_MODE: "basic", want one of none, oidc`) {
		t.Errorf("error = %v", err)
	}
}

// A variable an earlier release read is a fault naming its new name, never
// ignored.
func TestRenamedVariablesRefused(t *testing.T) {
	for old, now := range map[string]string{
		"ALMANACH_SECRET_KEY": "BACKEND_SOURCE_TOKEN_KEY",
		"SERVER_HOST":         "BACKEND_ADDR",
		"ALMANACH_MODEL":      "BACKEND_MODEL",
	} {
		env := minimal()
		env[old] = "x"
		if _, err := Load(lookup(env)); err == nil || !strings.Contains(err.Error(), old+" was renamed to "+now) {
			t.Errorf("%s set: error = %v, want it named with %s", old, err, now)
		}
	}
}

// A page ceiling an earlier release read is a fault, never ignored: a
// description has none.
func TestRetiredCeilingsRefused(t *testing.T) {
	for _, old := range []string{"BACKEND_REPO_MAX_STATEMENTS", "BACKEND_PROJECT_MAX_CHARS", "ALMANACH_REPO_MAX_CHARS"} {
		env := minimal()
		env[old] = "100000"
		if _, err := Load(lookup(env)); err == nil || !strings.Contains(err.Error(), old+" is no longer read") {
			t.Errorf("%s set: error = %v", old, err)
		}
	}
}

func TestBadAddr(t *testing.T) {
	for addr, want := range map[string]string{
		"8080":         "want host:port",
		"0.0.0.0":      "want host:port",
		"0.0.0.0:http": "the port must be 1 to 65535",
		":0":           "the port must be 1 to 65535",
		":70000":       "the port must be 1 to 65535",
		":+80":         "the port must be 1 to 65535",
		"0.0.0.0 :80":  "the host has whitespace",
	} {
		env := minimal()
		env["BACKEND_ADDR"] = addr
		if _, err := Load(lookup(env)); err == nil || !strings.Contains(err.Error(), "BACKEND_ADDR: "+want) {
			t.Errorf("BACKEND_ADDR=%q: error = %v, want %q", addr, err, want)
		}
	}
	env := minimal()
	env["BACKEND_ADDR"] = ":9090"
	if app, err := Load(lookup(env)); err != nil || app.Addr != ":9090" {
		t.Errorf("BACKEND_ADDR=:9090: %v %+v, want every interface", err, app)
	}
}

// The startup line names no secret: not the sources' token key, not the
// OIDC client secret, not the session secret.
func TestSummaryHidesSecrets(t *testing.T) {
	env := oidcEnv()
	env["BACKEND_REPOS_DIR"] = "/repos"
	const tokenKey = "c2VjcmV0LWtleS1zZWNyZXQta2V5LXNlY3JldC1rZXk="
	env["BACKEND_SOURCE_TOKEN_KEY"] = tokenKey
	app, err := Load(lookup(env))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	s := app.Summary()
	for _, secret := range []string{tokenKey, "client-secret-xyz", sessionSecret} {
		if strings.Contains(s, secret) {
			t.Errorf("Summary leaks %q: %s", secret, s)
		}
	}
	if !strings.Contains(s, "token_key true") {
		t.Errorf("Summary lacks whether a token key is set: %s", s)
	}
	for _, want := range []string{"addr=0.0.0.0:8080", "model=gpt-x", "limits={diagrams 6 a page / 12 nodes / 20 edges}", "problem_window=90d"} {
		if !strings.Contains(s, want) {
			t.Errorf("Summary lacks %q: %s", want, s)
		}
	}
}
