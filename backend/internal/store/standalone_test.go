package store

import (
	"context"
	"testing"
)

func TestSources_Standalone(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	alone := staticSource("tools")
	alone.Standalone = true
	if err := s.CreateSource(ctx, alone); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSource(ctx, bitbucketSource("SHOP")); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]ProjectSystem{"tools": {Standalone: true}, "SHOP": {}, "nobody": {}} {
		got, err := s.ProjectSystemOf(ctx, key)
		if err != nil || got != want {
			t.Fatalf("ProjectSystemOf(%s) = %+v %v, want %+v", key, got, err, want)
		}
	}
	rows, err := s.Projects(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	flags := map[string]bool{}
	for _, p := range rows {
		flags[p.Key] = p.Standalone
	}
	if !flags["tools"] || flags["SHOP"] {
		t.Fatalf("Projects standalone = %v, want tools only", flags)
	}

	// Becoming a system again marks the project due; the same word twice
	// changes nothing; a fold clears the mark.
	if changed, err := s.SetSourceStandalone(ctx, "tools", false); err != nil || !changed {
		t.Fatalf("SetSourceStandalone: %v %v", changed, err)
	}
	if got, _ := s.ProjectSystemOf(ctx, "tools"); got != (ProjectSystem{Due: true}) {
		t.Fatalf("after the switch on: %+v, want a system with the project due", got)
	}
	if changed, err := s.SetSourceStandalone(ctx, "tools", false); err != nil || changed {
		t.Fatalf("the same word again: changed %v %v, want unchanged", changed, err)
	}
	if err := s.ProjectFolded(ctx, "tools"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.ProjectSystemOf(ctx, "tools"); got != (ProjectSystem{}) {
		t.Fatalf("after the fold: %+v, want nothing due", got)
	}
	if changed, _ := s.SetSourceStandalone(ctx, "tools", true); !changed {
		t.Fatal("switching off again did not change")
	}
	if got, _ := s.ProjectSystemOf(ctx, "tools"); got != (ProjectSystem{Standalone: true}) {
		t.Fatalf("switched off: %+v, want standalone and nothing due", got)
	}
	if changed, err := s.SetSourceStandalone(ctx, "nobody", true); err != nil || changed {
		t.Fatalf("SetSourceStandalone(unknown) = %v %v, want false", changed, err)
	}
}

// The entry's standalone repositories come in one query, by project.
func TestStandaloneRepos(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	alone := staticSource("tools")
	alone.Standalone = true
	if err := s.CreateSource(ctx, alone); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSource(ctx, staticSource("apps")); err != nil {
		t.Fatal(err)
	}
	for _, r := range []SourceRepo{
		{ProjectKey: "tools", Slug: "webfetch", CloneURL: "https://forge.example.invalid/x/webfetch"},
		{ProjectKey: "tools", Slug: "llmwire", CloneURL: "https://forge.example.invalid/x/llmwire"},
		{ProjectKey: "apps", Slug: "loom", CloneURL: "https://forge.example.invalid/x/loom"},
	} {
		if err := s.AddSourceRepo(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.StandaloneRepos(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got["tools"]) != 2 || got["tools"][0].Slug != "llmwire" || got["tools"][1].Slug != "webfetch" {
		t.Fatalf("StandaloneRepos = %+v, want tools' two by slug", got)
	}
}
