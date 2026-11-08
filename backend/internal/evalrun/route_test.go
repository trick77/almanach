package evalrun

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trick77/toolkit/llmwire/llmwiretest"

	"github.com/trick77/almanach/internal/baseline"
	"github.com/trick77/almanach/internal/consolidate"
	"github.com/trick77/almanach/internal/inference"
)

// The dry run over a one-repository folder against a fake model: the
// repository laid down, one router call, its picks and the docs scored
// against the labels; nothing queued, no page.
func TestRouteOnlyAgainstAFakeModel(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	gitRepo(t, filepath.Join(dir, "projects", "p1", "r1"), map[string]string{
		"README.md": "# r1\n", "main.go": "package main\n\nfunc main() {}\n", "util.go": "package main\n",
	})
	if err := os.MkdirAll(filepath.Join(dir, "labels", "p1"), 0o750); err != nil {
		t.Fatal(err)
	}
	labels := "repo: r1\nmust_read:\n  - path: main.go\n    why: entry\n  - path: README.md\n    why: docs\n"
	if err := os.WriteFile(filepath.Join(dir, "labels", "p1", "r1.yaml"), []byte(labels), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := llmwiretest.NewServer(t)
	srv.SetReply(`{"subjects": [{"name": "Kern", "purpose": "Der Kern.", "paths": ["main.go"], "questions": ["Was startet?"]}]}`)
	env := map[string]string{"LLMWIRE_LITELLM_BASE_URL": srv.URL, "LLMWIRE_LITELLM_API_KEY": "k", "LLMWIRE_LITELLM_MODELS": "mimo-v2.6-pro=" + llmwiretest.ChatModel}
	lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	rep, err := RouteOnly(context.Background(), Options{Dir: dir, Out: filepath.Join(dir, "out"), Model: "mimo-v2.6-pro",
		Lookup: lookup, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Repos) != 1 {
		t.Fatalf("report = %+v", rep)
	}
	r := rep.Repos[0]
	if r.Outcome != "ok" || r.MustReadRecall != 1 || len(r.Subjects) == 0 || r.Subjects[0].Questions[0] != "Was startet?" || r.Files != 3 || !r.Billed {
		t.Errorf("repo = %+v, want both must-reads, the docs counted", r)
	}
	if md := rep.Markdown(); !strings.Contains(md, "| p1/r1 |") {
		t.Errorf("markdown = %q", md)
	}
	again, err := RouteOnly(context.Background(), Options{Dir: dir, Out: filepath.Join(dir, "out"), Model: "mimo-v2.6-pro",
		Lookup: lookup, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil || !strings.Contains(again.Repos[0].Error, "holds a run already") {
		t.Errorf("second dry run into the same folder: %+v, %v", again.Repos, err)
	}
}

// A dry run's picks are scored against the labels: must-read recall, the
// noise picked, the call's tokens and price; the markdown shows each
// subject with its files and questions.
func TestRouteReport(t *testing.T) {
	usd := int64(20_000_000)
	dry := consolidate.RouteDry{
		Files: []baseline.File{{Path: "a.go", Size: 2048}, {Path: "gen/x.go", Size: 1024}, {Path: "b.go", Size: 10}},
		Plan: baseline.Plan{Subjects: []baseline.Subject{{Name: "Kern", Purpose: "Der Kern.",
			Files: []baseline.File{{Path: "a.go", Size: 2048}, {Path: "gen/x.go", Size: 1024}}}}},
		Questions:     map[string][]string{"Kern": {"Was tut der Kern?"}},
		Call:          inference.Call{Outcome: inference.OutcomeOK, Cost: inference.CallCost{InputTokens: 900, OutputTokens: 50, NanoUSD: &usd}},
		ReadableBytes: 3082, SkeletonBytes: 300, ListingBytes: 400,
	}
	l := &Labels{MustRead: []MustRead{{Path: "a.go"}, {Path: "b.go"}}, Generated: []Glob{{Glob: "gen/**"}}}
	rr := routeReport(RouteRepo{Project: "p", Repo: "r"}, dry, l)
	if rr.MustReadRecall != 0.5 || len(rr.MustReadMissed) != 1 || rr.MustReadMissed[0] != "b.go" {
		t.Errorf("must-read = %v %v", rr.MustReadRecall, rr.MustReadMissed)
	}
	if len(rr.NoiseFiles) != 1 || rr.NoiseKB != 1 || rr.PickedFiles != 2 || rr.PickedKB != 3 || *rr.CostUSD != 0.02 || rr.InputTokens != 900 {
		t.Errorf("report = %+v", rr)
	}
	md := (&RouteReport{Model: "m", Repos: []RouteRepo{rr}, CostUSD: 0.02}).Markdown()
	for _, want := range []string{"| p/r | 3 |", "### Kern (3 kB)", "- `a.go`", "- Was tut der Kern?", "Must-read missed: b.go", "Noise picked: gen/x.go"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown lacks %q:\n%s", want, md)
		}
	}
}
