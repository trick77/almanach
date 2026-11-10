package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/trick77/almanach/internal/diagram"
	"github.com/trick77/almanach/internal/manifest"
	"github.com/trick77/almanach/internal/messaging"
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
			{From: loom, To: fetch, Coords: []string{"go:example.invalid/fetch"}, Scope: manifest.ScopeMain},
			{From: loom, To: wire, Coords: []string{"go:example.invalid/wire"}, Scope: manifest.ScopeMain},
			{From: rongo, To: wire, Coords: []string{"go:example.invalid/wire"}, Scope: manifest.ScopeMain},
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
	if edges[1].From.Slug != "loom" || edges[1].To.Project != "libs" {
		t.Errorf("edge = %+v", edges[1])
	}
	// No version: the relation is what counts.
	if strings.Contains(string(body["edges"]), "version") {
		t.Errorf("edges name a version: %s", body["edges"])
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

// The dependency page gets every scope, each edge saying its own; a
// repository's uses and its export main ones only.
func TestDependencies_Scopes(t *testing.T) {
	st := depsStore()
	loom, rongo := store.RepoRef{ProjectKey: "apps", Slug: "loom"}, store.RepoRef{ProjectKey: "apps", Slug: "rongo"}
	st.graph.Edges = append(st.graph.Edges, store.Edge{From: rongo, To: loom, Coords: []string{"go:example.invalid/loom"},
		Scope: manifest.ScopeTest})
	h, _, _ := sourcesHandlerWith(t, newFakeSources(), st, &fakeWaker{}, false)
	_, body := do(t, h, http.MethodGet, "/api/dependencies", "", "", nil)
	var edges []edgeBody
	if err := json.Unmarshal(body["edges"], &edges); err != nil || len(edges) != 4 {
		t.Fatalf("edges: %v %s", err, body["edges"])
	}
	if edges[0].Scope != manifest.ScopeMain || edges[3].Scope != manifest.ScopeTest {
		t.Fatalf("scopes = %q %q, want main, test", edges[0].Scope, edges[3].Scope)
	}
	_, body = do(t, h, http.MethodGet, "/api/projects/apps/repos/rongo", "", "", nil)
	var uses []useBody
	if err := json.Unmarshal(body["uses"], &uses); err != nil || len(uses) != 1 || uses[0].Slug != "wire" {
		t.Fatalf("rongo uses = %s, want wire alone, not its test use", body["uses"])
	}
	if rec := getMD(h, "/api/projects/apps/repos/loom.md", ""); strings.Contains(rec.Body.String(), "apps/rongo") {
		t.Fatalf("loom export names its test user: %q", rec.Body.String())
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
// it.
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
	if uses[0].Slug != "wire" || len(uses[0].Also) != 1 || uses[0].Also[0].Slug != "loom" {
		t.Fatalf("rongo uses = %+v, want wire, also loom", uses)
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
	h, _, _ := sourcesHandlerWith(t, newFakeSources(), st, &fakeWaker{}, false)
	rec := getMD(h, "/api/projects/libs/repos/wire.md", "")
	want := "## Abhängigkeiten\n\nVerwendet von:\n\n- `apps/loom`\n- `apps/rongo`\n"
	if rec.Code != 200 || !strings.HasSuffix(rec.Body.String(), want) {
		t.Fatalf("export: %d %q, want it to end in %q", rec.Code, rec.Body.String(), want)
	}
	rec = getMD(h, "/api/projects/apps/repos/rongo.md", "")
	if !strings.Contains(rec.Body.String(), "Verwendet:\n\n- `libs/wire`\n") {
		t.Fatalf("rongo export: %q", rec.Body.String())
	}
	st.graph = store.Graph{}
	if rec = getMD(h, "/api/projects/libs/repos/wire.md", ""); strings.Contains(rec.Body.String(), "## Abhängigkeiten") {
		t.Fatalf("export without edges: %q", rec.Body.String())
	}
}

// A repository's page says which topics it sends to and receives from,
// with the repositories on the far side, and its schemas; the project page
// lists every topic its repositories touch with both sides; the export
// says the same. With the sources off: none.
func TestMessaging_RepoProjectAndExport(t *testing.T) {
	st := depsStore()
	loom, rongo := store.RepoRef{ProjectKey: "apps", Slug: "loom"}, store.RepoRef{ProjectKey: "apps", Slug: "rongo"}
	wire := store.RepoRef{ProjectKey: "libs", Slug: "wire"}
	tp := func(name, dir string) messaging.Topic {
		return messaging.Topic{Name: name, Direction: dir, Files: []string{"a.go"}}
	}
	st.messages = map[store.RepoRef]messaging.Read{
		loom:  {Topics: []messaging.Topic{tp("jobs.v1", messaging.Produces), tp("tick", messaging.Declares)}, Schemas: []messaging.Schema{{Name: "ex.Job", Kind: "avro", File: "job.avsc"}}},
		rongo: {Topics: []messaging.Topic{tp("jobs.v1", messaging.Consumes)}},
		wire:  {Topics: []messaging.Topic{tp("jobs.v1", messaging.Consumes)}},
	}
	h, _, _ := sourcesHandlerWith(t, newFakeSources(), st, &fakeWaker{}, false)
	code, body := do(t, h, http.MethodGet, "/api/projects/apps/repos/loom", "", "", nil)
	var m repoMessagingBody
	if err := json.Unmarshal(body["messaging"], &m); code != 200 || err != nil {
		t.Fatalf("repo: %d %v", code, err)
	}
	if len(m.Topics) != 2 || m.Topics[0].Name != "jobs.v1" || len(m.Topics[0].Peers) != 2 || m.Topics[0].Peers[1].Project != "libs" ||
		len(m.Schemas) != 1 || m.Schemas[0].Name != "ex.Job" {
		t.Errorf("loom messaging = %+v", m)
	}
	code, body = do(t, h, http.MethodGet, "/api/projects/apps", "", "", nil)
	var flows []topicFlowBody
	if err := json.Unmarshal(body["topics"], &flows); code != 200 || err != nil || len(flows) != 2 {
		t.Fatalf("project topics: %d %v %s", code, err, body["topics"])
	}
	if f := flows[0]; f.Name != "jobs.v1" || len(f.Producers) != 1 || len(f.Consumers) != 2 || len(f.Declared) != 0 {
		t.Errorf("jobs.v1 = %+v", f)
	}
	rec := getMD(h, "/api/projects/apps/repos/loom.md", "")
	want := "## Messaging\n\n- Producer von `jobs.v1`, Consumer: `apps/rongo`, `libs/wire`\n- nennt `tick`\n\nSchemas:\n\n- `ex.Job` (avro, `job.avsc`)\n"
	if !strings.Contains(rec.Body.String(), want) {
		t.Errorf("export: %q, want %q in it", rec.Body.String(), want)
	}
	rec = getMD(h, "/api/projects/apps/repos/rongo.md", "")
	if !strings.Contains(rec.Body.String(), "- Consumer von `jobs.v1`, Producer: `apps/loom`\n") {
		t.Errorf("rongo export: %q", rec.Body.String())
	}
	if rec = getMD(h, "/api/projects/apps/repos/fetch.md", ""); strings.Contains(rec.Body.String(), "Messaging") {
		t.Errorf("export without messages: %q", rec.Body.String())
	}
	rec = getMD(h, "/api/projects/apps.md", "")
	wantFlows := "## Messaging\n\n- `jobs.v1`: Producer `apps/loom`; Consumer `apps/rongo`, `libs/wire`\n- `tick`: Producer –; Consumer –; genannt von `apps/loom`\n"
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), wantFlows) {
		t.Errorf("project export: %d %q, want %q in it", rec.Code, rec.Body.String(), wantFlows)
	}

	// The sources off: the store is not asked.
	st.graphErr = errors.New("must not be asked")
	off := handler(t, st, &fakeWaker{}, "")
	code, body = do(t, off, http.MethodGet, "/api/projects/libs/repos/wire", "", "", nil)
	if code != 200 || string(body["messaging"]) != `{"topics":[],"schemas":[]}` {
		t.Errorf("sources off: %d %s", code, body["messaging"])
	}
	code, body = do(t, off, http.MethodGet, "/api/projects/apps", "", "", nil)
	if code != 200 || string(body["topics"]) != "[]" {
		t.Errorf("project with the sources off: %d %s", code, body["topics"])
	}
	// A store that fails: a 500, never an empty list.
	h, _, _ = sourcesHandlerWith(t, newFakeSources(), st, &fakeWaker{}, false)
	for _, p := range []string{"/api/projects/apps", "/api/projects/apps/repos/loom"} {
		if code, _ := do(t, h, http.MethodGet, p, "", "", nil); code != 500 {
			t.Errorf("%s with the store failing: %d, want 500", p, code)
		}
	}
	if rec := getMD(h, "/api/projects/apps/repos/loom.md", ""); rec.Code != 500 {
		t.Errorf("export with the store failing: %d, want 500", rec.Code)
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
