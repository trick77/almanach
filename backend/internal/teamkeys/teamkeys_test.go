package teamkeys

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func writeKeys(t *testing.T, path, content string, mod time.Time) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
}

var t0 = time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)

func TestLoadAndLookup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.json")
	writeKeys(t, path, `{"platform": "tok-platform", "billing": " tok-billing "}`, t0)
	k, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for token, want := range map[string]string{"tok-platform": "platform", "tok-billing": "billing", " tok-platform ": "platform",
		"nope": "", "": "", "   ": ""} {
		team, reloadErr := k.Lookup(token)
		if reloadErr != nil {
			t.Errorf("Lookup(%q) reloadErr = %v", token, reloadErr)
		}
		if team != want {
			t.Errorf("Lookup(%q) = %q, want %q", token, team, want)
		}
	}
	if got := k.Teams(); !reflect.DeepEqual(got, []string{"billing", "platform"}) {
		t.Errorf("Teams = %v, want sorted", got)
	}
}

func TestLoadFailures(t *testing.T) {
	cases := []struct {
		name, content, want string
	}{
		{"duplicate token", `{"a": "tok", "b": "tok"}`, "share a token"},
		{"empty team", `{"": "tok"}`, "empty team or token"},
		{"empty token", `{"a": "  "}`, "empty team or token"},
		{"no entries", `{}`, "no team keys"},
		{"malformed", `{"a": `, "team keys"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "keys.json")
			writeKeys(t, path, tc.content, t0)
			k, err := Load(path)
			if err == nil {
				t.Fatalf("Load accepted %q: %v", tc.content, k.Teams())
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err, tc.want)
			}
		})
	}
	t.Run("missing file", func(t *testing.T) {
		if _, err := Load(filepath.Join(t.TempDir(), "absent.json")); err == nil {
			t.Fatal("Load accepted a missing file")
		}
	})
}

func TestHotReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.json")
	writeKeys(t, path, `{"platform": "tok-platform"}`, t0)
	k, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if team, _ := k.Lookup("tok-billing"); team != "" {
		t.Fatalf("Lookup before the rewrite = %q", team)
	}

	writeKeys(t, path, `{"platform": "tok-platform", "billing": "tok-billing"}`, t0.Add(time.Hour))
	team, reloadErr := k.Lookup("tok-billing")
	if reloadErr != nil {
		t.Fatalf("reloadErr = %v", reloadErr)
	}
	if team != "billing" {
		t.Errorf("Lookup after the rewrite = %q, want billing", team)
	}
	if got := k.Teams(); !reflect.DeepEqual(got, []string{"billing", "platform"}) {
		t.Errorf("Teams after the rewrite = %v", got)
	}

	// A malformed rewrite reports itself and keeps the last good table.
	writeKeys(t, path, `{"platform": `, t0.Add(2*time.Hour))
	team, reloadErr = k.Lookup("tok-platform")
	if reloadErr == nil {
		t.Fatal("reloadErr = nil for a malformed file")
	}
	if team != "platform" {
		t.Errorf("Lookup during the malformed file = %q, want the old table's platform", team)
	}
	if team, _ := k.Lookup("tok-billing"); team != "billing" {
		t.Errorf("old table lost billing: %q", team)
	}

	// An emptied file ({} from a bad rollout) is refused the same way: the
	// old table stays and the error says so.
	writeKeys(t, path, `{}`, t0.Add(150*time.Minute))
	team, reloadErr = k.Lookup("tok-platform")
	if reloadErr == nil || !strings.Contains(reloadErr.Error(), "no team keys") {
		t.Fatalf("reloadErr = %v for an empty file", reloadErr)
	}
	if team != "platform" {
		t.Errorf("Lookup during the empty file = %q, want the old table's platform", team)
	}

	// A repaired file reloads cleanly; an unchanged mtime then reloads nothing.
	writeKeys(t, path, `{"platform": "tok-platform", "billing": "tok-billing"}`, t0.Add(3*time.Hour))
	if _, reloadErr = k.Lookup("tok-platform"); reloadErr != nil {
		t.Fatalf("reloadErr after the repair = %v", reloadErr)
	}
	if _, reloadErr = k.Lookup("tok-platform"); reloadErr != nil {
		t.Fatalf("reloadErr on an unchanged file = %v", reloadErr)
	}
}
