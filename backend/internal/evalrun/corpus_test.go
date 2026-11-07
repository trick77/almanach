package evalrun

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/trick77/almanach/internal/store"
)

func TestGlobSet(t *testing.T) {
	s := newGlobSet([]Glob{
		{Glob: "**/testdata/**"},
		{Glob: "src/*.{csv,sql}"},
		{Glob: "{gradlew,gradle/wrapper/**}"},
		{Glob: "docs/?.md"},
	})
	for _, c := range []struct {
		path string
		want bool
	}{
		{"testdata/a.json", true},
		{"pkg/x/testdata/deep/b.txt", true},
		{"src/seed.csv", true},
		{"src/seed.sql", true},
		{"src/sub/seed.csv", false},
		{"gradlew", true},
		{"gradle/wrapper/gradle-wrapper.jar", true},
		{"gradle/other.jar", false},
		{"docs/a.md", true},
		{"docs/ab.md", false},
		{"main.go", false},
	} {
		if got := s.match(c.path); got != c.want {
			t.Errorf("match(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestExpandBracesNested(t *testing.T) {
	got := expandBraces("a/{b,c{d,e}}/f")
	want := []string{"a/b/f", "a/cd/f", "a/ce/f"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestPickedReadSubjectsAndDocs(t *testing.T) {
	plan := map[string]any{
		"subjects": []any{
			map[string]any{"name": "api", "files": []any{map[string]any{"path": "api/a.go", "size": 10}}},
			map[string]any{"name": "jobs", "files": []any{map[string]any{"path": "jobs/j.go", "size": 20}}},
			map[string]any{"name": "ui", "files": []any{map[string]any{"path": "ui/u.ts", "size": 30}}},
		},
		"read":    []any{"api", "jobs"},
		"skipped": []any{"jobs"},
		"docs":    []any{map[string]any{"path": "README.md", "size": 5}},
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	got := picked(raw, store.RunDone, 0)
	if len(got) != 2 || got["api/a.go"] != 10 || got["README.md"] != 5 {
		t.Fatalf("picked = %v, want api/a.go and README.md only", got)
	}
	// Cut off at its first step: nothing of the plan read yet.
	if cut := picked(raw, store.RunFailed, 0); len(cut) != 1 || cut["README.md"] != 5 {
		t.Fatalf("picked at step 0 = %v, want the docs alone", cut)
	}
}

func TestFilesScoresRecallAndNoise(t *testing.T) {
	l := &Labels{
		MustRead:  []MustRead{{Path: "a.go"}, {Path: "b.go"}},
		NeverRead: []Glob{{Glob: "**/*.lock"}},
		Generated: []Glob{{Glob: "gen/**"}},
	}
	rr := RepoResult{
		Picked: map[string]int64{"a.go": 1, "x.lock": 2000, "gen/z.go": 3000},
		Page:   &store.Page{Statements: []store.Statement{{Files: []string{"b.go"}}}},
	}
	var rep RepoReport
	files(&rep, l, rr)
	if rep.MustReadRecall != 0.5 || rep.CitedMustRead != 0.5 {
		t.Fatalf("recall %v cited %v, want 0.5 and 0.5", rep.MustReadRecall, rep.CitedMustRead)
	}
	if len(rep.NoiseFiles) != 2 || rep.NoiseBytes != 5000 {
		t.Fatalf("noise %v (%d bytes), want 2 files, 5000 bytes", rep.NoiseFiles, rep.NoiseBytes)
	}
}

func TestRulesFlagsAdviceAndEszett(t *testing.T) {
	p := &store.Page{Summary: "Der Dienst sollte neu gebaut werden. Grösse", Statements: []store.Statement{{Kind: "rule", Name: "x", Text: "Strasse und Straße"}}}
	got := rules(p)
	if len(got) != 2 {
		t.Fatalf("rules = %v, want advice and Eszett", got)
	}
}

func TestLoadReadsProjectsAndLabels(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(dir, "projects", "p1", "r1")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "projects", "p1", "not-a-repo"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "labels", "p1"), 0o750); err != nil {
		t.Fatal(err)
	}
	labels := "repo: r1\nmust_read:\n  - path: main.go\n    why: entry\nquestions:\n  - id: q1\n    category: domain\n    q: Was?\n    answer: Das.\n    evidence: [main.go]\n"
	if err := os.WriteFile(filepath.Join(dir, "labels", "p1", "r1.yaml"), []byte(labels), 0o600); err != nil {
		t.Fatal(err)
	}
	ps, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 1 || len(ps[0].Repos) != 1 || ps[0].Repos[0].Slug != "r1" {
		t.Fatalf("projects = %+v, want p1 with r1 only", ps)
	}
	l := ps[0].Repos[0].Labels
	if l == nil || len(l.MustRead) != 1 || len(l.Questions) != 1 || l.Questions[0].Evidence[0] != "main.go" {
		t.Fatalf("labels = %+v", l)
	}
}

func TestLoadWithoutProjectsFails(t *testing.T) {
	if _, err := Load(t.TempDir()); err == nil {
		t.Fatal("Load of an empty folder: want an error")
	}
}
