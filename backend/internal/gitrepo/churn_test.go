package gitrepo

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Churn counts the commits that touched each path since a time, a bot's
// and a dependency bump's left out, an umlaut path named as the tree does.
func TestChurn(t *testing.T) {
	base := t.TempDir()
	work := filepath.Join(base, "work")
	gitRun(t, base, "init", "-q", "-b", "main", work)
	commit := func(name, email, msg string, files map[string]string) {
		for p, body := range files {
			full := filepath.Join(work, p)
			if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			gitRun(t, work, "add", p)
		}
		gitRun(t, work, "-c", "user.name="+name, "-c", "user.email="+email, "commit", "-q", "-m", msg)
	}
	commit("Ada", "ada@example.invalid", "billing: monthly run", map[string]string{"billing/run.go": "1", "docs/für.md": "1"})
	commit("Ada", "ada@example.invalid", "billing: retry", map[string]string{"billing/run.go": "2"})
	commit("renovate[bot]", "bot@renovateapp.invalid", "Update module x to v2", map[string]string{"go.mod": "1"})
	commit("Ada", "ada@example.invalid", "chore(deps): bump y", map[string]string{"go.mod": "2", "billing/run.go": "3"})
	commit("Ada", "ada@example.invalid", "Bump z from 1 to 2", map[string]string{"pom.xml": "1"})
	commit("Ada", "ada@example.invalid", "docs: umlaut", map[string]string{"docs/für.md": "2"})
	head := gitRun(t, work, "rev-parse", "HEAD")
	dir := filepath.Join(base, "remote.git")
	gitRun(t, base, "clone", "-q", "--bare", work, dir)
	work = dir

	c := client(t)
	got, err := c.Churn(context.Background(), work, head, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]int{"billing/run.go": 2, "docs/für.md": 2, "go.mod": 0, "pom.xml": 0} {
		if got[p] != want {
			t.Errorf("churn of %s = %d, want %d (all: %v)", p, got[p], want, got)
		}
	}
	if late, err := c.Churn(context.Background(), work, head, time.Now().Add(time.Hour)); err != nil || len(late) != 0 {
		t.Errorf("churn since the future = %v, %v", late, err)
	}
	if _, err := c.Churn(context.Background(), work, "not-a-sha", time.Now()); err == nil {
		t.Error("a bad sha read")
	}
}
