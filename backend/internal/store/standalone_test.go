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
	for key, want := range map[string]bool{"tools": true, "SHOP": false, "nobody": false} {
		got, err := s.ProjectStandalone(ctx, key)
		if err != nil || got != want {
			t.Fatalf("ProjectStandalone(%s) = %v %v, want %v", key, got, err, want)
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

	if ok, err := s.SetSourceStandalone(ctx, "tools", false); err != nil || !ok {
		t.Fatalf("SetSourceStandalone: %v %v", ok, err)
	}
	src, _, _ := s.SourceByKey(ctx, "tools")
	if src.Standalone {
		t.Fatal("tools still stands alone after the switch")
	}
	if ok, err := s.SetSourceStandalone(ctx, "nobody", true); err != nil || ok {
		t.Fatalf("SetSourceStandalone(unknown) = %v %v, want false", ok, err)
	}
}
