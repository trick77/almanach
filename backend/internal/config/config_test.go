package config

import (
	"strings"
	"testing"
)

func lookup(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func minimal() map[string]string {
	return map[string]string{
		"DATABASE_URL":            "postgres://almanach:hunter2@db.internal:5432/almanach",
		"ALMANACH_TEAM_KEYS_PATH": "/run/secrets/team-keys.json",
		"ALMANACH_MODEL":          "gpt-x",
	}
}

func TestLoadRequiredMissing(t *testing.T) {
	_, err := Load(lookup(map[string]string{}))
	if err == nil {
		t.Fatal("Load accepted an empty environment")
	}
	for _, name := range []string{"DATABASE_URL is required", "ALMANACH_TEAM_KEYS_PATH is required", "ALMANACH_MODEL is required"} {
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
	if app.DatabaseURL != minimal()["DATABASE_URL"] || app.TeamKeysPath != "/run/secrets/team-keys.json" || app.Model != "gpt-x" {
		t.Errorf("required values = %+v", app)
	}
	if app.Host != "0.0.0.0" || app.Port != 8080 || app.AuthMode != AuthNone || app.ReasoningEffort != "" {
		t.Errorf("server defaults = %+v", app)
	}
	want := Limits{RepoMaxStatements: 40, ProjectMaxStatements: 24, SummaryMaxChars: 1500, StatementMaxChars: 300}
	if app.Limits != want {
		t.Errorf("Limits = %+v, want %+v", app.Limits, want)
	}
	if app.ProblemWindow != 90 || app.LogLevel != "INFO" {
		t.Errorf("problem window / log level = %d %q", app.ProblemWindow, app.LogLevel)
	}
}

func TestLoadOverrides(t *testing.T) {
	env := minimal()
	env["SERVER_HOST"] = "127.0.0.1"
	env["SERVER_PORT"] = "9090"
	env["ALMANACH_AUTH_MODE"] = "proxy"
	env["ALMANACH_REASONING_EFFORT"] = "high"
	env["ALMANACH_REPO_MAX_STATEMENTS"] = "10"
	env["ALMANACH_PROJECT_MAX_STATEMENTS"] = "5"
	env["ALMANACH_SUMMARY_MAX_CHARS"] = "800"
	env["ALMANACH_STATEMENT_MAX_CHARS"] = "120"
	env["ALMANACH_PROBLEM_WINDOW_DAYS"] = "30"
	env["LOG_LEVEL"] = "DEBUG"
	app, err := Load(lookup(env))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if app.Host != "127.0.0.1" || app.Port != 9090 || app.AuthMode != AuthProxy || app.ReasoningEffort != "high" {
		t.Errorf("server values = %+v", app)
	}
	want := Limits{RepoMaxStatements: 10, ProjectMaxStatements: 5, SummaryMaxChars: 800, StatementMaxChars: 120}
	if app.Limits != want {
		t.Errorf("Limits = %+v, want %+v", app.Limits, want)
	}
	if app.ProblemWindow != 30 || app.LogLevel != "DEBUG" {
		t.Errorf("problem window / log level = %d %q", app.ProblemWindow, app.LogLevel)
	}
}

func TestProxyModeNeedsLoopback(t *testing.T) {
	for host, ok := range map[string]bool{"0.0.0.0": false, "": false, "10.0.0.5": false, "127.0.0.1": true, "localhost": true} {
		env := minimal()
		env["ALMANACH_AUTH_MODE"] = "proxy"
		if host != "" {
			env["SERVER_HOST"] = host
		}
		_, err := Load(lookup(env))
		if ok && err != nil {
			t.Errorf("host %q: Load failed: %v", host, err)
		}
		if !ok {
			if err == nil {
				t.Errorf("host %q: Load accepted proxy mode", host)
			} else if !strings.Contains(err.Error(), "ALMANACH_AUTH_MODE=proxy needs SERVER_HOST=127.0.0.1") {
				t.Errorf("host %q: error = %v", host, err)
			}
		}
	}
}

func TestNonPositiveCap(t *testing.T) {
	for _, name := range []string{"ALMANACH_REPO_MAX_STATEMENTS", "ALMANACH_PROJECT_MAX_STATEMENTS", "ALMANACH_SUMMARY_MAX_CHARS",
		"ALMANACH_STATEMENT_MAX_CHARS", "ALMANACH_PROBLEM_WINDOW_DAYS"} {
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
	env["ALMANACH_SUMMARY_MAX_CHARS"] = "lots"
	if _, err := Load(lookup(env)); err == nil || !strings.Contains(err.Error(), `ALMANACH_SUMMARY_MAX_CHARS: "lots" is not an integer`) {
		t.Errorf("non-integer cap: error = %v", err)
	}
}

func TestInvalidEnum(t *testing.T) {
	env := minimal()
	env["ALMANACH_AUTH_MODE"] = "basic"
	_, err := Load(lookup(env))
	if err == nil {
		t.Fatal("Load accepted ALMANACH_AUTH_MODE=basic")
	}
	if !strings.Contains(err.Error(), `ALMANACH_AUTH_MODE: "basic", want one of none, proxy`) {
		t.Errorf("error = %v", err)
	}
}

func TestSummaryHidesDSN(t *testing.T) {
	app, err := Load(lookup(minimal()))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	s := app.Summary()
	for _, secret := range []string{"hunter2", "postgres://", "db.internal", "/run/secrets"} {
		if strings.Contains(s, secret) {
			t.Errorf("Summary leaks %q: %s", secret, s)
		}
	}
	for _, want := range []string{"addr=0.0.0.0:8080", "auth=none", "model=gpt-x", "limits={repo 40, project 24, summary 1500, statement 300}", "problem_window=90d"} {
		if !strings.Contains(s, want) {
			t.Errorf("Summary lacks %q: %s", want, s)
		}
	}
}
