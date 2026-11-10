package api

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/trick77/almanach/internal/imports"
	"github.com/trick77/almanach/internal/manifest"
	"github.com/trick77/almanach/internal/store"
)

// A use both the build and the imports say is listed and drawn once, its
// part once.
func TestPartsOf_OneUse(t *testing.T) {
	in := manifest.Inner{
		Units: []manifest.Unit{{Dir: ".", Name: "svc"}, {Dir: "kit", Name: "kit"}},
		Edges: []manifest.InnerEdge{{From: ".", To: "kit", Scope: manifest.ScopeMain}},
	}
	imp := imports.Read{
		Units: []imports.Unit{{Dir: ".", Name: "svc", Kind: imports.KindApp}, {Dir: "kit", Name: "kit"}},
		Edges: []imports.Edge{{From: ".", To: "kit"}},
	}
	got := partsOf(in, imp)
	if len(got.Units) != 2 || len(got.Edges) != 1 || len(got.Graph.Edges) != 1 {
		t.Fatalf("parts = %+v, want two parts, one edge listed and drawn", got)
	}
	// The root's code is an entry point: the build's part says so too.
	if got.Units[0].Dir != "." || got.Units[0].Kind != "app" {
		t.Fatalf("root part = %+v, want an app", got.Units[0])
	}
}

// The packages' graph: the entry points and what two or more of them
// reach, an edge through the packages left out; the table every package.
func TestPartsOf_Imports(t *testing.T) {
	app := func(d string) imports.Unit { return imports.Unit{Dir: d, Name: d, Kind: imports.KindApp} }
	pkg := func(d string) imports.Unit { return imports.Unit{Dir: d, Name: d} }
	e := func(a, b string) imports.Edge { return imports.Edge{From: a, To: b} }
	imp := imports.Read{
		Units: []imports.Unit{app("cmd/a"), app("cmd/b"), pkg("internal/mid"), pkg("internal/one"), pkg("internal/shared")},
		Edges: []imports.Edge{
			e("cmd/a", "internal/mid"), e("internal/mid", "internal/shared"),
			e("cmd/a", "internal/one"),
			e("cmd/b", "internal/shared"),
		},
	}
	got := partsOf(manifest.Inner{}, imp)
	if len(got.Units) != 5 || len(got.Edges) != 4 {
		t.Fatalf("table = %+v %+v, want every package and import", got.Units, got.Edges)
	}
	want := partsGraphBody{
		Nodes: []string{"cmd/a", "cmd/b", "internal/shared"},
		Edges: []partEdgeBody{{From: "cmd/a", To: "internal/shared"}, {From: "cmd/b", To: "internal/shared"}},
	}
	if !reflect.DeepEqual(got.Graph, want) {
		t.Fatalf("graph = %+v, want %+v", got.Graph, want)
	}
	// One entry point: no graph, the table alone.
	one := imports.Read{Units: imp.Units[1:], Edges: imp.Edges[3:]}
	if g := partsOf(manifest.Inner{}, one).Graph; len(g.Nodes) != 0 {
		t.Fatalf("graph of one entry point = %+v, want none", g)
	}
}

// A repository's page carries the parts inside it that use one another,
// main uses alone; the export lists them; a repository of none has
// neither.
func TestRepo_Parts(t *testing.T) {
	st := depsStore()
	st.units = map[store.RepoRef]manifest.Inner{{ProjectKey: "apps", Slug: "rongo"}: {
		Units: []manifest.Unit{
			{Dir: "app", Name: "app", Kind: manifest.KindApp},
			{Dir: "core", Name: "core", Kind: manifest.KindLib},
			{Dir: "testkit", Name: "testkit"},
		},
		Edges: []manifest.InnerEdge{
			{From: "app", To: "core", Scope: manifest.ScopeMain},
			{From: "app", To: "testkit", Scope: manifest.ScopeTest},
		},
	}}
	h, _, _ := sourcesHandlerWith(t, newFakeSources(), st, &fakeWaker{}, false)
	code, body := do(t, h, http.MethodGet, "/api/projects/apps/repos/rongo", "", "", nil)
	var parts partsBody
	if err := json.Unmarshal(body["parts"], &parts); code != 200 || err != nil {
		t.Fatalf("parts: %d %v %s", code, err, body["parts"])
	}
	want := partsBody{
		Units: []partBody{{Dir: "app", Name: "app", Kind: "app"}, {Dir: "core", Name: "core", Kind: "lib"}},
		Edges: []partEdgeBody{{From: "app", To: "core"}},
		Graph: partsGraphBody{Nodes: []string{"app", "core"}, Edges: []partEdgeBody{{From: "app", To: "core"}}},
	}
	if !reflect.DeepEqual(parts, want) {
		t.Fatalf("parts = %+v, want %+v", parts, want)
	}
	rec := getMD(h, "/api/projects/apps/repos/rongo.md", "")
	if wantMD := "## Innere Abhängigkeiten\n\n- `app` verwendet `core`\n- `core`\n"; !strings.HasSuffix(rec.Body.String(), wantMD) {
		t.Fatalf("export: %q, want it to end in %q", rec.Body.String(), wantMD)
	}
	code, body = do(t, h, http.MethodGet, "/api/projects/libs/repos/wire", "", "", nil)
	if code != 200 || string(body["parts"]) != `{"units":[],"edges":[],"graph":{"nodes":[],"edges":[]}}` {
		t.Fatalf("wire parts: %d %s", code, body["parts"])
	}
	if rec = getMD(h, "/api/projects/libs/repos/wire.md", ""); strings.Contains(rec.Body.String(), "Innere") {
		t.Fatalf("export without parts: %q", rec.Body.String())
	}
}
