package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/trick77/almanach/internal/diagram"
	"github.com/trick77/almanach/internal/store"
)

// Two apps use one library of another folder; one app uses a library of
// its own project too.
func depsGraph() store.Graph {
	loom, rongo := store.RepoRef{ProjectKey: "apps", Slug: "loom"}, store.RepoRef{ProjectKey: "apps", Slug: "rongo"}
	wire, fetch := store.RepoRef{ProjectKey: "libs", Slug: "wire"}, store.RepoRef{ProjectKey: "apps", Slug: "fetch"}
	return store.Graph{
		Repos: []store.RepoRef{fetch, loom, rongo, wire},
		Edges: []store.Edge{
			{From: loom, To: fetch, Coords: []string{"go:example.invalid/fetch"}, Version: "v0.2.0"},
			{From: loom, To: wire, Coords: []string{"go:example.invalid/wire"}, Version: "v0.1.8"},
			{From: rongo, To: wire, Coords: []string{"go:example.invalid/wire"}, Version: "v0.1.6"},
		},
		Ambiguous: []store.Ambiguous{{Coord: "npm:shared", Providers: []store.RepoRef{fetch, wire}, Twice: true}},
	}
}

func depsStore() *fakeStore {
	st := newFakeStore()
	st.graph = depsGraph()
	st.projects = []store.ProjectRow{{Key: "apps", Repos: 3}, {Key: "libs", Repos: 1}}
	st.teams["apps"], st.teams["libs"] = "", ""
	st.repos["apps"] = []store.RepoRow{{Slug: "fetch"}, {Slug: "loom"}, {Slug: "rongo"}}
	st.repos["libs"] = []store.RepoRow{{Slug: "wire"}}
	g := depsGraph()
	st.libs = map[string][]store.SharedRepo{"apps": {{RepoRef: g.Edges[1].To, UsedBy: []store.RepoRef{g.Edges[1].From, g.Edges[2].From}}}}
	return st
}

func TestDependencies_Route(t *testing.T) {
	h, _, _ := sourcesHandlerWith(t, newFakeSources(), depsStore(), &fakeWaker{}, false)
	code, body := do(t, h, http.MethodGet, "/api/dependencies", "", "", nil)
	if code != 200 {
		t.Fatalf("dependencies: %d", code)
	}
	var edges []edgeBody
	if err := json.Unmarshal(body["edges"], &edges); err != nil || len(edges) != 3 {
		t.Fatalf("edges: %v %s", err, body["edges"])
	}
	if edges[1].From.Slug != "loom" || edges[1].To.Project != "libs" || edges[1].Version != "v0.1.8" {
		t.Errorf("edge = %+v", edges[1])
	}
	if !strings.Contains(string(body["ambiguous"]), `"coord":"npm:shared"`) || !strings.Contains(string(body["ambiguous"]), `"cloned_twice":true`) ||
		!strings.Contains(string(body["repos"]), "wire") {
		t.Errorf("ambiguous %s, repos %s", body["ambiguous"], body["repos"])
	}

	// A store that fails is a 500, not an empty graph.
	st := depsStore()
	st.graphErr = errors.New("database is locked")
	h, _, _ = sourcesHandlerWith(t, newFakeSources(), st, &fakeWaker{}, false)
	if code, _ := do(t, h, http.MethodGet, "/api/dependencies", "", "", nil); code != 500 {
		t.Fatalf("failing store: %d, want 500", code)
	}
}

// With the sources off there are no source repositories: the graph is
// empty and the store is not asked.
func TestDependencies_SourcesOff(t *testing.T) {
	st := depsStore()
	st.graphErr = errors.New("must not be asked")
	h := handler(t, st, &fakeWaker{}, "")
	code, body := do(t, h, http.MethodGet, "/api/dependencies", "", "", nil)
	if code != 200 || string(body["edges"]) != "[]" || string(body["repos"]) != "[]" {
		t.Fatalf("sources off: %d %v", code, body)
	}
	code, body = do(t, h, http.MethodGet, "/api/projects/libs/repos/wire", "", "", nil)
	if code != 200 || string(body["used_by"]) != "[]" || string(body["uses"]) != "[]" {
		t.Fatalf("repo with the sources off: %d %v", code, body)
	}
}

// A repository's page says what it uses, who else uses each, and who uses
// it, each with the version the user names.
func TestRepo_UsesAndUsedBy(t *testing.T) {
	h, _, _ := sourcesHandlerWith(t, newFakeSources(), depsStore(), &fakeWaker{}, false)
	code, body := do(t, h, http.MethodGet, "/api/projects/apps/repos/rongo", "", "", nil)
	if code != 200 {
		t.Fatalf("repo: %d", code)
	}
	var uses []useBody
	if err := json.Unmarshal(body["uses"], &uses); err != nil || len(uses) != 1 {
		t.Fatalf("uses: %v %s", err, body["uses"])
	}
	if uses[0].Slug != "wire" || uses[0].Version != "v0.1.6" || len(uses[0].Also) != 1 || uses[0].Also[0].Slug != "loom" ||
		uses[0].Also[0].Version != "v0.1.8" {
		t.Fatalf("rongo uses = %+v, want wire at v0.1.6, also loom at v0.1.8", uses)
	}
	code, body = do(t, h, http.MethodGet, "/api/projects/libs/repos/wire", "", "", nil)
	var usedBy []depBody
	if err := json.Unmarshal(body["used_by"], &usedBy); code != 200 || err != nil || len(usedBy) != 2 {
		t.Fatalf("wire used_by: %d %v %s", code, err, body["used_by"])
	}
}

// The export closes with the edges; a repository with none has no such
// section.
func TestRepoMarkdown_Dependencies(t *testing.T) {
	st := depsStore()
	st.graph.Edges[2].Version = "v0.1.6\n# not `a` [heading](x)"
	h, _, _ := sourcesHandlerWith(t, newFakeSources(), st, &fakeWaker{}, false)
	rec := getMD(h, "/api/projects/libs/repos/wire.md", "")
	want := "## Abhängigkeiten\n\nVerwendet von:\n\n- `apps/loom` (`v0.1.8`)\n- `apps/rongo` (`v0.1.6 # not 'a' [heading](x)`)\n"
	if rec.Code != 200 || !strings.HasSuffix(rec.Body.String(), want) {
		t.Fatalf("export: %d %q, want it to end in %q", rec.Code, rec.Body.String(), want)
	}
	rec = getMD(h, "/api/projects/apps/repos/rongo.md", "")
	if !strings.Contains(rec.Body.String(), "Verwendet:\n\n- `libs/wire` (`v0.1.6") {
		t.Fatalf("rongo export: %q", rec.Body.String())
	}
	st.graph = store.Graph{}
	if rec = getMD(h, "/api/projects/libs/repos/wire.md", ""); strings.Contains(rec.Body.String(), "Abhängigkeiten") {
		t.Fatalf("export without edges: %q", rec.Body.String())
	}
}

// One repository is cloned once: an add or a rename onto a URL another
// source clones is a 409 naming where it is.
func TestSources_ClonedElsewhere(t *testing.T) {
	fs := newFakeSources()
	fs.cloned = map[string]*store.ClonedElsewhereError{"https://forge.example.invalid/x/wire.git": {ProjectKey: "libs", Slug: "wire"}}
	h, _, _ := sourcesHandler(t, fs, false)
	if code, _ := do(t, h, http.MethodPost, "/api/sources", "", `{"project_key":"apps","kind":"static"}`, jsonHdr); code != 201 {
		t.Fatal("create folder")
	}
	code, body := do(t, h, http.MethodPost, "/api/sources/apps/repos", "",
		`{"slug":"wire","clone_url":"https://forge.example.invalid/x/wire.git"}`, jsonHdr)
	if code != 409 || !strings.Contains(field(t, body, "error"), "libs/wire") {
		t.Fatalf("add: %d %v, want 409 naming libs/wire", code, body)
	}
	if code, _ := do(t, h, http.MethodPost, "/api/sources/apps/repos", "",
		`{"slug":"loom","clone_url":"https://forge.example.invalid/x/loom.git"}`, jsonHdr); code != 201 {
		t.Fatalf("add loom: %d", code)
	}
	code, body = do(t, h, http.MethodPatch, "/api/sources/apps/repos/loom", "",
		`{"slug":"loom","clone_url":"https://forge.example.invalid/x/wire.git"}`, jsonHdr)
	if code != 409 || !strings.Contains(field(t, body, "error"), "clone_url") {
		t.Fatalf("rename: %d %v, want 409", code, body)
	}
}

// A project lists the libraries of other projects its repositories use,
// never its own; a standalone folder none, having no map.
func TestProject_Libraries(t *testing.T) {
	st := depsStore()
	h, _, _ := sourcesHandlerWith(t, newFakeSources(), st, &fakeWaker{}, false)
	// No map, no libraries: they are its zone.
	if code, body := do(t, h, http.MethodGet, "/api/projects/apps", "", "", nil); code != 200 || string(body["shared"]) != "[]" {
		t.Fatalf("apps without a map: %d %s, want no libraries", code, body["shared"])
	}
	m := &diagram.Diagram{Kind: diagram.KindFlow, Direction: "LR", Nodes: []diagram.Node{{ID: "loom", Label: "loom", Shape: "step", Zone: "repositories"}},
		Zones: []diagram.Zone{{ID: "repositories", Title: "Repositories"}}}
	st.pages[store.ScopeProject+"|apps|"] = store.Page{RevisionID: 1, Revision: 1, Summary: "Apps.", Map: m}
	code, body := do(t, h, http.MethodGet, "/api/projects/apps", "", "", nil)
	if code != 200 {
		t.Fatalf("project: %d", code)
	}
	var libs []sharedBody
	if err := json.Unmarshal(body["shared"], &libs); err != nil || len(libs) != 1 {
		t.Fatalf("libraries: %v %s", err, body["shared"])
	}
	if libs[0].Project != "libs" || libs[0].Slug != "wire" || len(libs[0].UsedBy) != 2 {
		t.Fatalf("libraries = %+v, want libs/wire used by loom and rongo", libs)
	}
	if code, body = do(t, h, http.MethodGet, "/api/projects/libs", "", "", nil); code != 200 || string(body["shared"]) != "[]" {
		t.Fatalf("libs' libraries: %d %s, want none", code, body["shared"])
	}
	st.single = map[string]store.RepoRow{"apps": {Slug: "loom"}}
	if code, body = do(t, h, http.MethodGet, "/api/projects/apps", "", "", nil); code != 200 || string(body["shared"]) != "[]" {
		t.Fatalf("apps of one: libraries %d %s, want none", code, body["shared"])
	}
	st.single = nil
	st.graphErr = errors.New("database is locked")
	if code, _ := do(t, h, http.MethodGet, "/api/projects/apps", "", "", nil); code != 500 {
		t.Fatalf("failing store: %d, want 500", code)
	}
}
